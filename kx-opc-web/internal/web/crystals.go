package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"kx-opc-web/internal/crystal"
	"kx-opc-web/internal/monitor"
	"kx-opc-web/internal/td"
)

// 晶棒数据导出：列出晶棒、统计工步、整棒 / 按工步导出 CSV。

func (s *Server) crystalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/crystals", s.crystalList)
	mux.HandleFunc("GET /api/crystals/steps", s.crystalSteps)
	mux.HandleFunc("GET /api/crystals/series", s.crystalSeries)
	mux.HandleFunc("GET /api/crystals/export", s.crystalExport)
	mux.HandleFunc("GET /api/export", s.crystalExport)
}

// exportHeaders 是导出 CSV 的表头：中文名 [点位]，没配置中文名的只写点位。
func (s *Server) exportHeaders(cols []string) []string {
	fields := s.Points.All()
	headers := make([]string, len(cols))
	for i, c := range cols {
		if c == s.HeartColumn && i == 0 {
			headers[i] = "上位机心跳时间"
			continue
		}
		f := fields[c]
		if f.Name != "" {
			headers[i] = fmt.Sprintf("%s [%s]", f.Name, c)
		} else {
			headers[i] = c
		}
	}
	return headers
}

// furnace 按子表名找炉台。
func (s *Server) furnace(table string) (monitor.Furnace, bool) {
	for _, f := range s.Monitor.Snapshot().Furnaces {
		if f.Table == table {
			return f, true
		}
	}
	return monitor.Furnace{}, false
}

func writeCrystalError(w http.ResponseWriter, err error) {
	if errors.Is(err, crystal.ErrInvalid) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeError(w, http.StatusBadGateway, err.Error())
}

func (s *Server) crystalList(w http.ResponseWriter, r *http.Request) {
	f, ok := s.furnace(r.URL.Query().Get("table"))
	if !ok {
		writeError(w, http.StatusBadRequest, "炉台不存在")
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 366 {
		days = s.CrystalCfg.ListDays[0]
	}
	now := s.Monitor.TDNow()
	list, err := s.Crystal.List(r.Context(), f.Table, f.DeviceIP, now.AddDate(0, 0, -days), now)
	if err != nil {
		writeCrystalError(w, err)
		return
	}
	if list == nil {
		list = []crystal.Crystal{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"crystals": list, "list_days": s.CrystalCfg.ListDays, "now": now.UnixMilli()})
}

// crystalRange 解析 ?table=&crystal=&from=&to=（毫秒）。
func (s *Server) crystalRange(r *http.Request) (monitor.Furnace, string, time.Time, time.Time, error) {
	q := r.URL.Query()
	f, ok := s.furnace(q.Get("table"))
	if !ok {
		return f, "", time.Time{}, time.Time{}, fmt.Errorf("%w：炉台不存在", crystal.ErrInvalid)
	}
	from, err1 := strconv.ParseInt(q.Get("from"), 10, 64)
	to, err2 := strconv.ParseInt(q.Get("to"), 10, 64)
	if err1 != nil || err2 != nil {
		return f, "", time.Time{}, time.Time{}, fmt.Errorf("%w：from、to 必须是毫秒时间戳", crystal.ErrInvalid)
	}
	return f, q.Get("crystal"), time.UnixMilli(from), time.UnixMilli(to), nil
}

func (s *Server) crystalSteps(w http.ResponseWriter, r *http.Request) {
	f, no, from, to, err := s.crystalRange(r)
	if err == nil {
		var stats *crystal.StepStats
		if stats, err = s.Crystal.Steps(r.Context(), f.Table, no, from, to); err == nil {
			writeJSONGzip(w, r, stats)
			return
		}
	}
	writeCrystalError(w, err)
}

// crystalExport 处理 ?table=&crystal=&from=&to=&steps=13,14&cols=A,B（cols 为空导出全部点位）。
// crystal 为空时不限晶棒，就是 OPC 数据按时间段的全量导出（/api/export 也指向这里）。客户账户只导出客户可见的点位。
func (s *Server) crystalExport(w http.ResponseWriter, r *http.Request) {
	f, no, from, to, err := s.crystalRange(r)
	if err != nil {
		writeCrystalError(w, err)
		return
	}
	q := r.URL.Query()
	steps, codes, err := parseSteps(q.Get("steps"))
	if err != nil {
		writeCrystalError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	all, err := s.DB.Columns(ctx)
	if err != nil {
		writeCrystalError(w, err)
		return
	}
	known := map[string]bool{}
	var cols []string
	allow := s.visible(r)
	for _, c := range all {
		known[c.Name] = allow == nil || allow(c.Name)
	}
	if raw := q.Get("cols"); raw != "" {
		for _, c := range strings.Split(raw, ",") {
			if c = strings.TrimSpace(c); known[c] {
				cols = append(cols, c)
			}
		}
	} else {
		for _, c := range all {
			if known[c.Name] {
				cols = append(cols, c.Name)
			}
		}
	}
	if len(cols) == 0 {
		writeError(w, http.StatusBadRequest, "没有可导出的点位")
		return
	}

	short := no
	if len(short) > 8 {
		short = short[:8]
	}
	name := fmt.Sprintf("%s_%s_晶棒%s", f.DeviceID, from.Format("20060102_1504"), short)
	if no == "" { // 按时间全量导出
		name = fmt.Sprintf("%s_%s-%s_全量", f.DeviceID, from.Format("20060102_150405"), to.Format("20060102_150405"))
	}
	if len(codes) > 0 {
		name += "_工步" + strings.Join(codes, "-")
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", attachment(name+".csv"))
	w.Header().Set("Cache-Control", "no-store")
	if q.Get("cols") != "" {
		cols = s.withHeartOrdered(ctx, cols)
	} else {
		cols = s.withHeart(ctx, cols)
	}
	req := crystal.ExportRequest{Table: f.Table, CrystalNo: no, From: from, To: to, Steps: steps, Cols: cols, Headers: s.exportHeaders(cols)}
	if err := s.Crystal.Export(ctx, req, w); err != nil {
		if errors.Is(err, crystal.ErrInvalid) {
			// 还没写出内容时可以返回错误；参数错误在写之前就会发现
			writeCrystalError(w, err)
			return
		}
		s.Log.Warn("晶棒导出失败", "table", f.Table, "crystal", no, "error", err)
	}
}

// parseSteps 解析 steps=13,24.1，返回 ×10 的工步号和原样的文本（用于文件名）。
func parseSteps(raw string) ([]int, []string, error) {
	var steps []int
	var codes []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		f, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return nil, nil, fmt.Errorf("%w：工步号必须是数字，如 13.0、24.1", crystal.ErrInvalid)
		}
		steps = append(steps, crystal.Tenths(f))
		codes = append(codes, p)
	}
	return steps, codes, nil
}

// crystalSeries 处理 ?table=&crystal=&from=&to=&cols=A,B[&steps=13,24.1][&points=2000]：一根晶的抽样曲线。
func (s *Server) crystalSeries(w http.ResponseWriter, r *http.Request) {
	f, no, from, to, err := s.crystalRange(r)
	if err != nil {
		writeCrystalError(w, err)
		return
	}
	q := r.URL.Query()
	steps, _, err := parseSteps(q.Get("steps"))
	if err != nil {
		writeCrystalError(w, err)
		return
	}
	points, _ := strconv.Atoi(q.Get("points"))
	points = min(max(points, 200), 5000)
	if q.Get("points") == "" {
		points = 2000
	}
	ctx, cancel := context.WithTimeout(r.Context(), apiTimeout*4)
	defer cancel()
	all, err := s.DB.Columns(ctx)
	if err != nil {
		writeCrystalError(w, err)
		return
	}
	plottable := map[string]bool{}
	for _, c := range all {
		plottable[c.Name] = c.Numeric() || c.Type == "BOOL"
	}
	var cols []string
	for _, c := range strings.Split(q.Get("cols"), ",") {
		if c = strings.TrimSpace(c); c == "" {
			continue
		}
		if !plottable[c] {
			writeError(w, http.StatusBadRequest, "点位不存在或不能画曲线: "+c)
			return
		}
		cols = append(cols, c)
	}
	if len(cols) == 0 || len(cols) > s.Limits.MaxTags {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("请选择 1~%d 个点位", s.Limits.MaxTags))
		return
	}
	series, bucket, err := s.Crystal.Series(ctx, f.Table, no, from, to, steps, cols, points)
	if err != nil {
		writeCrystalError(w, err)
		return
	}
	writeJSONGzip(w, r, map[string]any{"ts": series.Ts, "values": series.Values, "rows": series.Rows, "bucket_s": bucket})
}

// withHeartOrdered 保留用户选择的列顺序；未选心跳列时仍自动补在最前面。
func (s *Server) withHeartOrdered(ctx context.Context, cols []string) []string {
	if slices.Contains(cols, s.HeartColumn) {
		return cols
	}
	return s.withHeart(ctx, cols)
}

// withHeart 把上位机心跳时间列放到导出点位的最前面（CSV 里紧跟“时间”列），已选了它就挪到最前面；
// 没配置或采集表里没有这一列时原样返回。
func (s *Server) withHeart(ctx context.Context, cols []string) []string {
	if s.HeartColumn == "" {
		return cols
	}
	all, err := s.DB.Columns(ctx)
	if err != nil || !slices.ContainsFunc(all, func(c td.Column) bool { return c.Name == s.HeartColumn }) {
		return cols
	}
	out := []string{s.HeartColumn}
	for _, c := range cols {
		if c != s.HeartColumn {
			out = append(out, c)
		}
	}
	return out
}
