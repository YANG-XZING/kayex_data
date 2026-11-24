package alarm

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"kx-opc-web/internal/td"
)

// maxHistoryRows 告警记录一次最多读多少行事件，超出时只返回前面的部分并标记 truncated
const maxHistoryRows = 20000

// ErrInvalid 是参数校验错误，接口返回 400。
var ErrInvalid = errors.New("参数不合法")

// Episode 是一次告警：触发与对应的恢复配成一对。
type Episode struct {
	DeviceIP string `json:"device_ip"`
	DeviceID string `json:"device_id"`
	Type     string `json:"alarm_type"`
	// Start 开始时间（TD 时钟，毫秒）：心跳停止为心跳最后一次变化的时刻；查询范围内找不到触发行时为 null
	Start *int64 `json:"start"`
	// End 恢复时间，还没恢复（或恢复在查询范围之后）为 null
	End *int64 `json:"end"`
	// Seconds 触发时的值：心跳已停多少秒 / 时间差；没有触发行时为恢复时的值
	Seconds float64 `json:"seconds"`
	// DurationS 持续秒数，开始或结束未知时为 null
	DurationS *float64 `json:"duration_s"`
}

// Query 是告警记录的查询条件。
type Query struct {
	From, To time.Time // TD 时钟，[From, To)
	Device   string    // 炉台号或 IP，空为全部
	Type     string    // 告警类型，空为全部
}

// History 查询告警记录，按开始时间倒序；truncated 表示事件超过上限只处理了一部分。
func (e *Engine) History(ctx context.Context, q Query, maxRange time.Duration) ([]Episode, bool, error) {
	if !q.From.Before(q.To) {
		return nil, false, fmt.Errorf("%w：开始时间必须早于结束时间", ErrInvalid)
	}
	if q.To.Sub(q.From) > maxRange {
		return nil, false, fmt.Errorf("%w：查询范围不能超过 %d 天", ErrInvalid, int(maxRange.Hours()/24))
	}
	conds := []string{fmt.Sprintf("ts >= %d AND ts < %d", q.From.UnixMilli(), q.To.UnixMilli())}
	if q.Device != "" {
		d := td.String(q.Device)
		conds = append(conds, fmt.Sprintf("(device_ip = %s OR device_id = %s)", d, d))
	}
	var jumps []Episode
	var jumpTruncated bool
	switch q.Type {
	case TypeHeartbeatJump:
		return e.heartbeatJumps(ctx, conds)
	case "":
		var err error
		jumps, jumpTruncated, err = e.heartbeatJumps(ctx, conds)
		if err != nil {
			return nil, false, err
		}
	case TypeHeartbeat, TypeSkew:
		conds = append(conds, "alarm_type = "+td.String(q.Type))
	default:
		return nil, false, fmt.Errorf("%w：告警类型 %q", ErrInvalid, q.Type)
	}
	query := fmt.Sprintf("SELECT ts, device_ip, device_id, alarm_type, active, seconds FROM %s WHERE %s ORDER BY ts LIMIT %d",
		e.table, strings.Join(conds, " AND "), maxHistoryRows+1)
	res, err := e.db.Query(ctx, query)
	if err != nil {
		return nil, false, err
	}
	truncated := len(res.Rows) > maxHistoryRows
	if truncated {
		res.Rows = res.Rows[:maxHistoryRows]
	}
	events := make([]Event, len(res.Rows))
	for i, row := range res.Rows {
		ev := Event{DeviceIP: td.AsString(row[1]), DeviceID: td.AsString(row[2]), Type: td.AsString(row[3])}
		ev.Ts, _ = row[0].(time.Time)
		ev.Active, _ = row[4].(bool)
		ev.Seconds, _ = row[5].(float64)
		events[i] = ev
	}
	episodes := append(pairEpisodes(events), jumps...)
	sortEpisodes(episodes)
	if len(episodes) > maxHistoryRows {
		return episodes[:maxHistoryRows], true, nil
	}
	return episodes, truncated || jumpTruncated, nil
}

// heartbeatJumps 读取 Client 记录的瞬时事件，旧版 Client 或尚未跳变时没有事件表。
func (e *Engine) heartbeatJumps(ctx context.Context, conds []string) ([]Episode, bool, error) {
	const table = "opc_heartbeat_jump"
	query := fmt.Sprintf("SELECT ts, device_ip, device_id, seconds FROM %s WHERE %s ORDER BY ts DESC LIMIT %d",
		e.db.Qualified(table), strings.Join(conds, " AND "), maxHistoryRows+1)
	res, err := e.db.Query(ctx, query)
	if err != nil {
		exists, checkErr := e.db.Query(ctx, "SHOW TABLES LIKE "+td.String(table))
		if checkErr == nil && len(exists.Rows) == 0 {
			return []Episode{}, false, nil
		}
		return nil, false, err
	}
	count := min(len(res.Rows), maxHistoryRows)
	out := make([]Episode, 0, count)
	for _, row := range res.Rows[:count] {
		t, ok := row[0].(time.Time)
		if !ok {
			continue
		}
		start := t.UnixMilli()
		seconds, _ := row[3].(int64)
		out = append(out, Episode{DeviceIP: td.AsString(row[1]), DeviceID: td.AsString(row[2]),
			Type: TypeHeartbeatJump, Start: &start, Seconds: float64(seconds)})
	}
	return out, len(res.Rows) > maxHistoryRows, nil
}

// pairEpisodes 把按时间排序的事件配成告警：触发开始一次告警，同一台炉台同一类型的下一次恢复结束它。
func pairEpisodes(events []Event) []Episode {
	out := []Episode{}    // 没有告警时返回空数组而不是 null，前端按数组处理
	open := map[key]int{} // 未结束的告警在 out 中的下标
	for _, ev := range events {
		k := key{ev.DeviceIP, ev.Type}
		ts := ev.Ts.UnixMilli()
		if ev.Active {
			start := ts
			if ev.Type == TypeHeartbeat {
				start = ev.Ts.Add(-seconds(ev.Seconds)).UnixMilli()
			}
			open[k] = len(out)
			out = append(out, Episode{DeviceIP: ev.DeviceIP, DeviceID: ev.DeviceID, Type: ev.Type, Start: &start, Seconds: ev.Seconds})
			continue
		}
		i, ok := open[k]
		if !ok {
			// 触发在查询范围之前：心跳停止的恢复行记着一共停了多少秒，可以推出开始时间
			ep := Episode{DeviceIP: ev.DeviceIP, DeviceID: ev.DeviceID, Type: ev.Type, End: &ts, Seconds: ev.Seconds}
			if ev.Type == TypeHeartbeat {
				start := ev.Ts.Add(-seconds(ev.Seconds)).UnixMilli()
				d := ev.Seconds
				ep.Start, ep.DurationS = &start, &d
			}
			out = append(out, ep)
			continue
		}
		delete(open, k)
		ep := &out[i]
		ep.End = &ts
		d := round1(float64(ts-*ep.Start) / 1000)
		ep.DurationS = &d
	}
	return out
}

func sortEpisodes(out []Episode) {
	// 开始时间倒序；开始未知的按结束时间
	at := func(ep Episode) int64 {
		if ep.Start != nil {
			return *ep.Start
		}
		return *ep.End
	}
	slices.SortStableFunc(out, func(a, b Episode) int { return cmp.Compare(at(b), at(a)) })
}
