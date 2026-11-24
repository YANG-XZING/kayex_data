package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"kx-opc-web/internal/auth"
	"kx-opc-web/internal/hmi"
)

// parseRecords 解析 ?kind=record|alarm&ip=&from=&to=（毫秒，按上位机时间）&q=关键字&color=颜色
func parseRecords(r *http.Request) (hmi.Query, error) {
	q := r.URL.Query()
	from, err1 := strconv.ParseInt(q.Get("from"), 10, 64)
	to, err2 := strconv.ParseInt(q.Get("to"), 10, 64)
	if err1 != nil || err2 != nil {
		return hmi.Query{}, fmt.Errorf("%w：from、to 必须是毫秒时间戳", hmi.ErrInvalid)
	}
	return hmi.Query{Kind: q.Get("kind"), IP: strings.TrimSpace(q.Get("ip")), From: time.UnixMilli(from), To: time.UnixMilli(to), Keyword: q.Get("q"), Color: strings.TrimSpace(q.Get("color"))}, nil
}

// parsePage 解析 ?offset=&limit=，limit 不超过 maxRows
func parsePage(r *http.Request, maxRows int) (int, int) {
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > maxRows {
		limit = maxRows
	}
	return max(offset, 0), limit
}

func writeRecordsError(w http.ResponseWriter, err error) {
	if errors.Is(err, hmi.ErrInvalid) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeError(w, http.StatusBadGateway, "查询上位机记录失败: "+err.Error())
}

func allowHMIQuery(w http.ResponseWriter, r *http.Request, q hmi.Query) bool {
	if q.IP == "" && !canViewAllHMI(auth.FromContext(r.Context())) {
		writeError(w, http.StatusForbidden, "当前账户没有查看全部炉台记录的权限")
		return false
	}
	return true
}

func (s *Server) hmiRecords(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), apiTimeout)
	defer cancel()
	q, err := parseRecords(r)
	if err != nil {
		writeRecordsError(w, err)
		return
	}
	if !allowHMIQuery(w, r, q) {
		return
	}
	offset, limit := parsePage(r, hmi.MaxPageRows)
	page, err := s.HMI.PageOf(ctx, q, offset, limit)
	if err != nil {
		writeRecordsError(w, err)
		return
	}
	writeJSONGzip(w, r, page)
}

func (s *Server) hmiRecordColors(w http.ResponseWriter, r *http.Request) {
	q, err := parseRecords(r)
	if err != nil {
		writeRecordsError(w, err)
		return
	}
	if !allowHMIQuery(w, r, q) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), apiTimeout)
	defer cancel()
	colors, err := s.HMI.Colors(ctx, q)
	if err != nil {
		writeRecordsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, colors)
}

func (s *Server) hmiRecordsExport(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), exportTimeout)
	defer cancel()
	q, err := parseRecords(r)
	if err != nil {
		writeRecordsError(w, err)
		return
	}
	if !allowHMIQuery(w, r, q) {
		return
	}
	if err := s.HMI.Validate(q); err != nil {
		writeRecordsError(w, err)
		return
	}
	who := "全部炉台"
	if q.IP != "" {
		who = q.IP
		for _, f := range s.Monitor.Snapshot().Furnaces {
			if f.DeviceIP == q.IP {
				who = f.DeviceID
			}
		}
	}
	name := fmt.Sprintf("%s_%s_%s-%s.csv", s.HMI.Label(q.Kind), who, q.From.Format("20060102_150405"), q.To.Format("20060102_150405"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
	w.Header().Set("Cache-Control", "no-store")
	if err := s.HMI.Export(ctx, q, w); err != nil {
		s.Log.Warn("导出上位机记录失败", "kind", q.Kind, "ip", q.IP, "error", err)
	}
}

// pointsCustomer 设置客户可见的点位：请求体 {"points": [...]}，没列出的点位都取消。
func (s *Server) pointsCustomer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Points []string `json:"points"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	n, err := s.Points.SetCustomer(r.Context(), body.Points)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"changed": n, "points": s.Points.All()})
}
