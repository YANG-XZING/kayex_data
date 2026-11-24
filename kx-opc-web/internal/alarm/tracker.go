// Package alarm 根据每轮炉台检查的结果判定告警，触发、恢复各写一行到 web_alarm，并提供当前告警和告警记录查询。
//
//	heartbeat_stop 心跳停止：炉台状态为“心跳停止”即触发（heartbeat_timeout 本身就是防抖），心跳恢复即恢复；
//	               seconds：触发时为已停止的秒数，恢复时为一共停了多少秒
//	time_skew      时间不一致：时间差连续 skew_confirm 轮超限才触发，连续 skew_confirm 轮正常才恢复；
//	               心跳停止期间不判断；seconds 为当时的时间差（工控机快为正）
//
// 告警的 ts 用 TD 服务器时钟，与采集数据的 ts 一致，方便对照历史曲线。
package alarm

import (
	"math"
	"time"

	"kx-opc-web/internal/monitor"
)

// 告警类型
const (
	TypeHeartbeat     = "heartbeat_stop"
	TypeSkew          = "time_skew"
	TypeHeartbeatJump = "heartbeat_jump"
)

// Event 是写入 web_alarm 的一行。
type Event struct {
	Ts       time.Time
	DeviceIP string
	DeviceID string
	Type     string
	Active   bool // true 触发，false 恢复
	Seconds  float64
}

// Active 是一条还没恢复的告警。
type Active struct {
	DeviceIP string  `json:"device_ip"`
	DeviceID string  `json:"device_id"`
	Type     string  `json:"alarm_type"`
	Since    int64   `json:"since"`   // 开始时间（TD 时钟，毫秒）：心跳停止为心跳最后一次变化的时刻
	Seconds  float64 `json:"seconds"` // 当前值：心跳已停多少秒 / 当前时间差
}

type key struct{ ip, typ string }

// tracker 是告警判定的状态机，不做任何 IO，便于测试。
type tracker struct {
	confirm  int
	active   map[key]*Active
	skewBad  map[string]int       // IP -> 连续超限的轮数
	skewGood map[string]int       // IP -> 连续正常的轮数
	lastTs   map[string]time.Time // IP -> 最近一条事件的 ts（主键是 ts + device_ip，同一台炉台不能重复）
}

func newTracker(confirm int) *tracker {
	return &tracker{
		confirm: confirm, active: map[key]*Active{},
		skewBad: map[string]int{}, skewGood: map[string]int{}, lastTs: map[string]time.Time{},
	}
}

// restore 用 web_alarm 里每台炉台每种告警的最后一行恢复状态（程序重启后接着判定，不重复触发）。
func (t *tracker) restore(last Event) {
	if prev, ok := t.lastTs[last.DeviceIP]; !ok || last.Ts.After(prev) {
		t.lastTs[last.DeviceIP] = last.Ts
	}
	if !last.Active {
		return
	}
	since := last.Ts
	if last.Type == TypeHeartbeat {
		since = last.Ts.Add(-seconds(last.Seconds))
	}
	t.active[key{last.DeviceIP, last.Type}] = &Active{
		DeviceIP: last.DeviceIP, DeviceID: last.DeviceID, Type: last.Type, Since: since.UnixMilli(), Seconds: last.Seconds,
	}
}

// observe 根据一轮检查结果更新状态，返回需要写入的事件。
func (t *tracker) observe(snap monitor.Snapshot) []Event {
	now := snap.CheckedAt.Add(time.Duration(snap.TDOffsetMs) * time.Millisecond).Truncate(time.Millisecond)
	var events []Event
	emit := func(f monitor.Furnace, typ string, active bool, secs float64) {
		ts := now
		if last, ok := t.lastTs[f.DeviceIP]; ok && !ts.After(last) {
			ts = last.Add(time.Millisecond)
		}
		t.lastTs[f.DeviceIP] = ts
		events = append(events, Event{Ts: ts, DeviceIP: f.DeviceIP, DeviceID: f.DeviceID, Type: typ, Active: active, Seconds: round1(secs)})
	}

	for _, f := range snap.Furnaces {
		if f.DeviceIP == "" {
			continue
		}
		// 心跳停止
		hk := key{f.DeviceIP, TypeHeartbeat}
		switch a := t.active[hk]; {
		case f.Status == monitor.StatusStopped && a == nil:
			emit(f, TypeHeartbeat, true, f.HeartbeatAgeS)
			t.active[hk] = &Active{DeviceIP: f.DeviceIP, DeviceID: f.DeviceID, Type: TypeHeartbeat,
				Since: now.Add(-seconds(f.HeartbeatAgeS)).UnixMilli(), Seconds: round1(f.HeartbeatAgeS)}
		case f.Status == monitor.StatusStopped:
			a.DeviceID, a.Seconds = f.DeviceID, round1(f.HeartbeatAgeS)
		case f.Status == monitor.StatusOnline && a != nil:
			emit(f, TypeHeartbeat, false, float64(now.UnixMilli()-a.Since)/1000)
			delete(t.active, hk)
		}

		// 时间不一致：只在心跳正常、算得出时间差时判断
		if f.Status != monitor.StatusOnline || f.SkewS == nil {
			continue
		}
		if f.SkewAlarm {
			t.skewBad[f.DeviceIP]++
			t.skewGood[f.DeviceIP] = 0
		} else {
			t.skewGood[f.DeviceIP]++
			t.skewBad[f.DeviceIP] = 0
		}
		sk := key{f.DeviceIP, TypeSkew}
		a := t.active[sk]
		switch {
		case a == nil && t.skewBad[f.DeviceIP] >= t.confirm:
			emit(f, TypeSkew, true, *f.SkewS)
			t.active[sk] = &Active{DeviceIP: f.DeviceIP, DeviceID: f.DeviceID, Type: TypeSkew, Since: now.UnixMilli(), Seconds: round1(*f.SkewS)}
		case a != nil && t.skewGood[f.DeviceIP] >= t.confirm:
			emit(f, TypeSkew, false, *f.SkewS)
			delete(t.active, sk)
		case a != nil:
			a.DeviceID, a.Seconds = f.DeviceID, round1(*f.SkewS)
		}
	}
	return events
}

func seconds(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

func round1(v float64) float64 { return math.Round(v*10) / 10 }
