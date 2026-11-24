package web

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"kx-opc-web/internal/history"
	"kx-opc-web/internal/points"
	"kx-opc-web/internal/td"
)

const (
	maxBodyBytes  = 1 << 20
	exportTimeout = 5 * time.Minute
)

// settings 返回前端需要的限制和版本。
func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"history_max_range_ms": s.Limits.MaxRange.Milliseconds(),
		"history_max_tags":     s.Limits.MaxTags,
		"table_max_range_ms":   s.TableMaxRange.Milliseconds(),
		"hmi_max_range_ms":     s.HMI.MaxRange().Milliseconds(),
		"alarm_max_range_ms":   s.AlarmMaxRange.Milliseconds(),
		"export_max_range_ms":  s.CrystalCfg.MaxRange.Milliseconds(),
		"skew_limit_s":         s.SkewLimit.Seconds(),
		"version":              s.Version,
	})
}

// ---------- 历史查询 ----------

// parseHistory 解析 ?table=&cols=A,B&from=毫秒&to=毫秒，并校验炉台存在、点位当前账户可见。
func (s *Server) parseHistory(r *http.Request) (history.Request, error) {
	q := r.URL.Query()
	req := history.Request{Table: q.Get("table")}
	if !s.Monitor.HasTable(req.Table) {
		return req, fmt.Errorf("%w：炉台 %q 不存在", history.ErrInvalid, req.Table)
	}
	for _, c := range strings.Split(q.Get("cols"), ",") {
		if c = strings.TrimSpace(c); c != "" {
			req.Cols = append(req.Cols, c)
		}
	}
	if allow := s.visible(r); allow != nil {
		for _, c := range req.Cols {
			if !allow(c) {
				return req, fmt.Errorf("%w：点位 %s 不可查看", history.ErrInvalid, c)
			}
		}
	}
	from, err1 := strconv.ParseInt(q.Get("from"), 10, 64)
	to, err2 := strconv.ParseInt(q.Get("to"), 10, 64)
	if err1 != nil || err2 != nil {
		return req, fmt.Errorf("%w：from、to 必须是毫秒时间戳", history.ErrInvalid)
	}
	req.From, req.To = time.UnixMilli(from), time.UnixMilli(to)
	return req, nil
}

// tableLimits 是历史数据表格和导出的限制：点位数与曲线相同，时间更长（分页读取 / 流式导出，不占内存）
func (s *Server) tableLimits() history.Limits {
	return history.Limits{MaxRange: s.TableMaxRange, MaxTags: s.Limits.MaxTags}
}

// historyPage 历史数据表格的一页：?table=&cols=&from=&to=&offset=&limit=
func (s *Server) historyPage(w http.ResponseWriter, r *http.Request) {
	s.historyTablePage(w, r, false)
}

// exportPage 给全量导出页提供分页预览，时间、点位可见范围与导出一致。
func (s *Server) exportPage(w http.ResponseWriter, r *http.Request) {
	s.historyTablePage(w, r, true)
}

func (s *Server) historyTablePage(w http.ResponseWriter, r *http.Request, full bool) {
	ctx, cancel := context.WithTimeout(r.Context(), apiTimeout)
	defer cancel()
	limits := s.tableLimits()
	maxRows := history.MaxPageRows
	if full {
		columns, err := s.DB.Columns(ctx)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		limits = history.Limits{MaxRange: s.CrystalCfg.MaxRange, MaxTags: len(columns)}
		maxRows = 1000 // 支持自定义条数，同时限制单页宽表的大小。
	}
	req, err := s.parseHistory(r)
	if err == nil {
		err = s.History.ValidateWithin(ctx, &req, false, limits)
	}
	if err != nil {
		writeAPIError(w, err)
		return
	}
	offset, limit := parsePage(r, maxRows)
	page, err := s.History.PageOf(ctx, req, offset, limit)
	if err != nil {
		writeError(w, http.StatusBadGateway, "查询失败: "+err.Error())
		return
	}
	writeJSONGzip(w, r, page)
}

func (s *Server) historyQuery(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), apiTimeout)
	defer cancel()
	req, err := s.parseHistory(r)
	if err == nil {
		err = s.History.Validate(ctx, &req, true)
	}
	if err != nil {
		writeAPIError(w, err)
		return
	}
	series, err := s.History.Query(ctx, req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "查询失败: "+err.Error())
		return
	}
	writeJSONGzip(w, r, series)
}

func (s *Server) historyExport(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), exportTimeout)
	defer cancel()
	req, err := s.parseHistory(r)
	if err == nil {
		err = s.History.ValidateWithin(ctx, &req, false, s.tableLimits())
	}
	if err != nil {
		writeAPIError(w, err)
		return
	}

	req.Cols = s.withHeartOrdered(ctx, req.Cols)
	headers := s.exportHeaders(req.Cols)
	device := req.Table
	if f, ok := s.furnace(req.Table); ok {
		device = f.DeviceID
	}
	name := fmt.Sprintf("%s_%s-%s.csv", device, req.From.Format("20060102_150405"), req.To.Format("150405"))

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
	w.Header().Set("Cache-Control", "no-store")
	if err := s.History.Export(ctx, req, headers, w); err != nil {
		// 已经开始写文件，无法再改状态码；记日志，浏览器会得到一个不完整的文件
		s.Log.Warn("导出失败", "table", req.Table, "error", err)
	}
}

// ---------- 点位信息 ----------

// pointsGet 返回点位信息；客户账户只返回客户可见的点位。
func (s *Server) pointsGet(w http.ResponseWriter, r *http.Request) {
	all := s.Points.All()
	if allow := s.visible(r); allow != nil {
		maps.DeleteFunc(all, func(p string, _ points.Info) bool { return !allow(p) })
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"points": all,
		"loaded": s.Points.Loaded(),
		"table":  s.DB.Qualified(td.PointTable),
	})
}

// pointsExport 下载全部点位（按 opc_data 的列顺序）及当前的 tag、中文名、单位，改完可以再上传。
func (s *Server) pointsExport(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), apiTimeout)
	defer cancel()
	cols, err := s.DB.Columns(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, "读取点位列表失败: "+err.Error())
		return
	}
	all := s.Points.All()
	names := make([]string, 0, len(cols))
	seen := map[string]bool{}
	for _, c := range cols {
		names = append(names, c.Name)
		seen[c.Name] = true
	}
	for _, p := range slices.Sorted(maps.Keys(all)) { // 表里有、opc_data 里已经没有的点位也写出来
		if !seen[p] {
			names = append(names, p)
		}
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape("点位信息.csv"))
	_ = points.WriteCSV(w, names, all)
}

// pointsImport 上传 CSV（请求体就是文件内容），只修改文件里出现的点位。
func (s *Server) pointsImport(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "文件太大或读取失败（最大 4MB）")
		return
	}
	items, err := points.ParseCSV(data)
	if err == nil {
		var n int
		if n, err = s.Points.Set(r.Context(), items); err == nil {
			writeJSON(w, http.StatusOK, map[string]any{"rows": len(items), "changed": n, "points": s.Points.All()})
			return
		}
	}
	writeAPIError(w, err)
}

// ---------- 工具 ----------

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "请求内容不是合法的 JSON: "+err.Error())
		return false
	}
	return true
}

func writeAPIError(w http.ResponseWriter, err error) {
	if errors.Is(err, history.ErrInvalid) || errors.Is(err, points.ErrInvalid) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeError(w, http.StatusBadGateway, err.Error())
}

// writeJSONGzip 在浏览器支持时压缩响应（历史数据几百 KB，压缩后约十分之一）。
func writeJSONGzip(w http.ResponseWriter, r *http.Request, v any) {
	if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		writeJSON(w, http.StatusOK, v)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Add("Vary", "Accept-Encoding")
	gz, _ := gzip.NewWriterLevel(w, gzip.BestSpeed)
	defer gz.Close()
	_ = json.NewEncoder(gz).Encode(v)
}
