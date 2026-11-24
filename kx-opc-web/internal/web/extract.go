package web

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"kx-opc-web/internal/extract"
)

// 取数接口，请求和返回的格式与原网站（/api/query 等）一致，只是挂在 /api/extract/ 下，且数据库连接不再由页面传入。

const extractTimeout = 10 * time.Minute

func (s *Server) extractRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/extract/defaults", s.extractDefaults)
	mux.HandleFunc("GET /api/extract/devices", s.extractDevices)
	mux.HandleFunc("GET /api/extract/crystals", s.extractCrystals)
	mux.HandleFunc("POST /api/extract/query", s.extractQuery)
	mux.HandleFunc("POST /api/extract/hardware", s.extractHardware)
	mux.HandleFunc("GET /api/extract/segment_rows", s.extractSegmentRows)
	mux.HandleFunc("GET /api/extract/export", s.extractExport)
	mux.HandleFunc("GET /api/extract/export_split", s.extractExportSplit)
	mux.HandleFunc("GET /api/extract/export_segment", s.extractExportSegment)
	mux.HandleFunc("GET /api/extract/export_all_segments", s.extractExportAllSegments)
}

// extractCrystals 返回一台设备最近 days 天的晶棒（kayex_data 的晶棒台账），按晶棒取数时选用。
func (s *Server) extractCrystals(w http.ResponseWriter, r *http.Request) {
	device := strings.TrimSpace(r.URL.Query().Get("device"))
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if device == "" {
		writeError(w, http.StatusBadRequest, "请先选择设备")
		return
	}
	if days <= 0 || days > 366 {
		days = 30
	}
	list, err := s.Extract.Crystals(r.Context(), device, s.Monitor.TDNow().AddDate(0, 0, -days))
	if err != nil {
		writeError(w, http.StatusBadGateway, "读取晶棒台账失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"crystals": list, "now": s.Monitor.TDNow().UnixMilli()})
}

// extractDevices 返回 kayex_data 里有 PLC 数据的设备，供取数页选择（和 OPC 监控的炉台清单 kx_opc 不是一回事）。
func (s *Server) extractDevices(w http.ResponseWriter, r *http.Request) {
	list, err := s.Extract.Devices(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "读取设备清单失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) extractDefaults(w http.ResponseWriter, r *http.Request) {
	c := s.ExtractCfg
	writeJSON(w, http.StatusOK, map[string]any{
		"plc_points": c.PLCPoints, "ccd_points": c.CCDPoints, "step_labels": c.StepLabels,
		"database": c.Database, "plc_stable": c.PLCStable, "ccd_stable": c.CCDStable,
		"hardware_table": c.HardwareTable, "max_rows": c.MaxRows, "areas": s.Areas,
	})
}

// extractBody 是原网站 /api/query 的请求体；数值字段原网站用 int()/float() 转，字符串和数字都接受。
type extractBody struct {
	Device       string          `json:"device"`
	Start        string          `json:"start"`
	End          string          `json:"end"`
	IncludeCCD   *bool           `json:"include_ccd"`
	EndPlusDay   *bool           `json:"end_plus_day"`
	PLCPoints    []extract.Point `json:"plc_points"`
	CCDPoints    []extract.Point `json:"ccd_points"`
	JoinMode     string          `json:"join_mode"`
	AsofTol      any             `json:"asof_tol"`
	MinSegRows   any             `json:"min_seg_rows"`
	StartStep    any             `json:"start_step"`
	AllowedSteps any             `json:"allowed_steps"`
	// StartMs、EndMs 按晶棒取数时用：晶棒台账里的起止 ts（毫秒，含两端），给了就不用 start、end
	StartMs *int64 `json:"start_ms"`
	EndMs   *int64 `json:"end_ms"`
}

func enabledPoints(ps []extract.Point) []extract.Point {
	var out []extract.Point
	for _, p := range ps {
		p.Point = strings.TrimSpace(p.Point)
		if p.Point == "" || !p.On {
			continue
		}
		if p.Name = strings.TrimSpace(p.Name); p.Name == "" {
			p.Name = p.Point
		}
		out = append(out, p)
	}
	return out
}

func toInt(v any, fallback int) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(x)); err == nil {
			return n
		}
	}
	return fallback
}

// writeExtractError 返回 {"ok": false, "error": ...}，与原网站一致。
func writeExtractError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	msg := err.Error()
	if errors.Is(err, extract.ErrInvalid) {
		status = http.StatusBadRequest
		msg = strings.TrimPrefix(msg, extract.ErrInvalid.Error()+"：")
	}
	writeJSON(w, status, map[string]any{"ok": false, "error": msg})
}

func (s *Server) extractQuery(w http.ResponseWriter, r *http.Request) {
	var b extractBody
	if !readJSON(w, r, &b) {
		return
	}
	req := extract.Request{
		Device: strings.TrimSpace(b.Device), PLCPoints: enabledPoints(b.PLCPoints),
		IncludeCCD: b.IncludeCCD == nil || *b.IncludeCCD, EndPlusDay: b.EndPlusDay == nil || *b.EndPlusDay,
		JoinMode: b.JoinMode, AsofTol: toInt(b.AsofTol, 1), MinSegRows: toInt(b.MinSegRows, 5),
		StartStep: extract.ParseFloat(b.StartStep, 13), AllowedSteps: map[float64]bool{},
	}
	if req.JoinMode == "" {
		req.JoinMode = "exact"
	}
	if req.IncludeCCD {
		req.CCDPoints = enabledPoints(b.CCDPoints)
	}
	var steps []any
	switch x := b.AllowedSteps.(type) {
	case string:
		for _, p := range strings.Split(strings.ReplaceAll(x, "，", ","), ",") {
			if p = strings.TrimSpace(p); p != "" {
				steps = append(steps, p)
			}
		}
	case []any:
		steps = x
	}
	for _, v := range steps {
		if f := extract.ParseFloat(v, -1e300); f != -1e300 {
			req.AllowedSteps[f] = true
		}
	}
	if len(req.AllowedSteps) == 0 {
		req.AllowedSteps = map[float64]bool{13: true, 0: true}
	}
	if req.Device == "" {
		writeExtractError(w, fmt.Errorf("%w：请填写设备 IP 地址", extract.ErrInvalid))
		return
	}
	if len(req.PLCPoints) == 0 {
		writeExtractError(w, fmt.Errorf("%w：请至少启用一个 PLC 点位", extract.ErrInvalid))
		return
	}
	var err1, err2 error
	if b.StartMs != nil && b.EndMs != nil {
		// 按晶棒：查询是 [Start, End)，台账的结束时间是这根晶最后一行，所以加 1 毫秒
		req.Start, req.End, req.EndPlusDay = time.UnixMilli(*b.StartMs), time.UnixMilli(*b.EndMs+1), false
	} else {
		req.Start, err1 = s.Extract.ParseTime(b.Start)
		req.End, err2 = s.Extract.ParseTime(b.End)
	}
	if err1 != nil || err2 != nil {
		writeExtractError(w, fmt.Errorf("%w：时间格式不正确", extract.ErrInvalid))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), extractTimeout)
	defer cancel()
	res, err := s.Extract.Query(ctx, req)
	if err != nil {
		writeExtractError(w, err)
		return
	}
	s.writeExtractResult(w, res)
}

func (s *Server) extractHardware(w http.ResponseWriter, r *http.Request) {
	var b extractBody
	if !readJSON(w, r, &b) {
		return
	}
	start, err1 := s.Extract.ParseTime(b.Start)
	end, err2 := s.Extract.ParseTime(b.End)
	if strings.TrimSpace(b.Device) == "" {
		writeExtractError(w, fmt.Errorf("%w：请填写查询所需的 IP 地址", extract.ErrInvalid))
		return
	}
	if err1 != nil || err2 != nil {
		writeExtractError(w, fmt.Errorf("%w：时间格式不正确", extract.ErrInvalid))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), extractTimeout)
	defer cancel()
	res, err := s.Extract.Hardware(ctx, strings.TrimSpace(b.Device), start, end)
	if err != nil {
		writeExtractError(w, err)
		return
	}
	s.writeExtractResult(w, res)
}

// writeExtractResult 保存结果并返回前 1000 行和分段信息。
func (s *Server) writeExtractResult(w http.ResponseWriter, res *extract.Result) {
	token, err := s.ExtractCache.Put(res)
	if err != nil {
		writeExtractError(w, err)
		return
	}
	t := res.Table
	segs := make([]map[string]any, len(res.Segments))
	for i, sg := range res.Segments {
		segs[i] = map[string]any{"index": i + 1, "rows": sg.To - sg.From,
			"start": extract.CellString(t, res.TimeCol, sg.From), "end": extract.CellString(t, res.TimeCol, sg.To-1)}
	}
	var stepCol any
	if res.StepCol != "" {
		stepCol = res.StepCol
	}
	cols, rows := extract.Preview(t, 0, t.Rows())
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "token": token, "device": res.Device, "columns": cols, "rows": rows,
		"total": t.Rows(), "shown": len(rows), "capped": t.Rows() > extract.DisplayRows,
		"q_start": s.Extract.FormatTime(res.QStart), "q_end": s.Extract.FormatTime(res.QEnd),
		"step_col": stepCol, "segments": segs,
	})
}

func (s *Server) extractSegmentRows(w http.ResponseWriter, r *http.Request) {
	res, seg, ok := s.loadSegment(w, r, true)
	if !ok {
		return
	}
	cols, rows := extract.Preview(res.Table, seg.From, seg.To)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "columns": cols, "rows": rows,
		"total": seg.To - seg.From, "shown": len(rows), "capped": seg.To-seg.From > extract.DisplayRows})
}

func (s *Server) extractExport(w http.ResponseWriter, r *http.Request) {
	res, ok := s.loadResult(w, r)
	if !ok {
		return
	}
	name := res.Device + "_combined.csv"
	if res.Table.Col("cpu_model") != nil {
		name = res.Device + "_hardware_combined.csv"
	}
	s.sendCSV(w, name, res.Table)
}

func (s *Server) extractExportSplit(w http.ResponseWriter, r *http.Request) {
	res, ok := s.loadResult(w, r)
	if !ok {
		return
	}
	t := extract.SplitRuns(res.Table, res.StepCol, res.TimeCol, s.ExtractCfg.TsCol)
	s.sendCSV(w, res.Device+"_炉次晶棒划分.csv", t)
}

func (s *Server) extractExportSegment(w http.ResponseWriter, r *http.Request) {
	res, seg, ok := s.loadSegment(w, r, false)
	if !ok {
		return
	}
	s.sendCSV(w, fmt.Sprintf("%s_Seg%s.csv", res.Device, r.URL.Query().Get("index")), extract.Slice(res.Table, seg))
}

func (s *Server) extractExportAllSegments(w http.ResponseWriter, r *http.Request) {
	res, ok := s.loadResult(w, r)
	if !ok {
		return
	}
	if len(res.Segments) == 0 {
		http.Error(w, "没有可导出的分段", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", attachment(res.Device+"_segments.zip"))
	zw := zip.NewWriter(w)
	for i, seg := range res.Segments {
		st, et := "START", "END"
		if res.Table.Col(res.TimeCol) != nil {
			st = safeT(extract.CellString(res.Table, res.TimeCol, seg.From))
			et = safeT(extract.CellString(res.Table, res.TimeCol, seg.To-1))
		}
		f, err := zw.Create(fmt.Sprintf("%s_Seg%d_%s_to_%s.csv", res.Device, i+1, st, et))
		if err == nil {
			err = extract.WriteCSV(f, extract.Slice(res.Table, seg))
		}
		if err != nil {
			s.Log.Warn("导出分段失败", "error", err)
			return
		}
	}
	_ = zw.Close()
}

// safeT 与原网站 _safe_t 一致：冒号换成 -、空格换成 _，去掉小数秒。
func safeT(v string) string {
	v = strings.NewReplacer(":", "-", " ", "_").Replace(v)
	before, _, _ := strings.Cut(v, ".")
	return before
}

func (s *Server) loadResult(w http.ResponseWriter, r *http.Request) (*extract.Result, bool) {
	res, err := s.ExtractCache.Get(r.URL.Query().Get("token"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return nil, false
	}
	return res, true
}

// loadSegment 取出 ?token=&index= 对应的分段；asJSON 时错误按 {"ok":false} 返回。
func (s *Server) loadSegment(w http.ResponseWriter, r *http.Request, asJSON bool) (*extract.Result, extract.Segment, bool) {
	fail := func() (*extract.Result, extract.Segment, bool) {
		if asJSON {
			writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": extract.ErrExpired.Error()})
		} else {
			http.Error(w, extract.ErrExpired.Error(), http.StatusNotFound)
		}
		return nil, extract.Segment{}, false
	}
	idx, _ := strconv.Atoi(r.URL.Query().Get("index"))
	meta, err := s.ExtractCache.Meta(r.URL.Query().Get("token"))
	if err != nil || idx < 1 || idx > len(meta.Segments) {
		return fail()
	}
	res, err := s.ExtractCache.Get(r.URL.Query().Get("token"))
	if err != nil {
		return fail()
	}
	return res, res.Segments[idx-1], true
}

func (s *Server) sendCSV(w http.ResponseWriter, name string, t *extract.Table) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", attachment(name))
	w.Header().Set("Cache-Control", "no-store")
	if err := extract.WriteCSV(w, t); err != nil {
		s.Log.Warn("导出 CSV 失败", "file", name, "error", err)
	}
}

func attachment(name string) string {
	return "attachment; filename*=UTF-8''" + url.PathEscape(name)
}
