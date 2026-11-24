package web

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"kx-opc-web/internal/alarm"
)

var alarmTypeText = map[string]string{alarm.TypeHeartbeat: "心跳停止", alarm.TypeSkew: "时间不一致", alarm.TypeHeartbeatJump: "心跳跳变"}

func (s *Server) alarmsActive(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ready": s.Alarms.Ready(), "alarms": s.Alarms.Active()})
}

// alarmsHistory 处理 ?from=&to=（毫秒，TD 时钟）&device=&type=&format=csv。
func (s *Server) alarmsHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, err1 := strconv.ParseInt(q.Get("from"), 10, 64)
	to, err2 := strconv.ParseInt(q.Get("to"), 10, 64)
	if err1 != nil || err2 != nil {
		writeError(w, http.StatusBadRequest, "from、to 必须是毫秒时间戳")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), apiTimeout)
	defer cancel()
	query := alarm.Query{From: time.UnixMilli(from), To: time.UnixMilli(to), Device: q.Get("device"), Type: q.Get("type")}
	episodes, truncated, err := s.Alarms.History(ctx, query, s.AlarmMaxRange)
	if err != nil {
		if errors.Is(err, alarm.ErrInvalid) {
			writeError(w, http.StatusBadRequest, err.Error())
		} else {
			writeError(w, http.StatusBadGateway, "查询告警记录失败: "+err.Error())
		}
		return
	}
	if q.Get("format") != "csv" {
		writeJSONGzip(w, r, map[string]any{"episodes": episodes, "truncated": truncated})
		return
	}

	name := fmt.Sprintf("告警记录_%s-%s.csv", query.From.Format("20060102"), query.To.Format("20060102"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte("\xef\xbb\xbf"))
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"炉台号", "IP", "告警类型", "开始时间", "恢复时间", "持续（秒）", "触发时的值（秒）"})
	opt := func(ms *int64) string {
		if ms == nil {
			return ""
		}
		return time.UnixMilli(*ms).Local().Format("2006-01-02 15:04:05")
	}
	for _, ep := range episodes {
		dur := ""
		if ep.DurationS != nil {
			dur = strconv.FormatFloat(*ep.DurationS, 'f', -1, 64)
		}
		_ = cw.Write([]string{ep.DeviceID, ep.DeviceIP, alarmTypeText[ep.Type], opt(ep.Start), opt(ep.End), dur,
			strconv.FormatFloat(ep.Seconds, 'f', -1, 64)})
	}
	cw.Flush()
}
