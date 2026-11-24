package alarm

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"kx-opc-web/internal/monitor"
	"kx-opc-web/internal/td"
)

const (
	writeTimeout = 10 * time.Second
	insertBatch  = 500
	// maxPending TD 写不进去时最多在内存里攒多少条事件，超出丢最旧的
	maxPending = 20000
)

// Engine 判定告警并写入 web_alarm，并发安全：Observe 只在 monitor 的协程里调用，查询接口可在任意协程调用。
type Engine struct {
	db     *td.DB
	log    *slog.Logger
	table  string
	record bool // false：只判定不写库（本机调试时）

	mu      sync.RWMutex
	t       *tracker
	loaded  bool
	pending []Event // 还没写进 TD 的事件，写入失败时下一轮重试

	loadFailing, writeFailing bool // 只在 Observe 中访问
}

// New 创建告警引擎。skewConfirm 为时间差连续超限 / 正常多少轮才触发 / 恢复；record 为 false 时只判定不写库。
func New(db *td.DB, skewConfirm int, record bool, log *slog.Logger) *Engine {
	e := &Engine{db: db, log: log.With("module", "alarm"), table: db.Qualified(td.AlarmTable), t: newTracker(skewConfirm), record: record}
	if !record {
		e.log.Warn("alarm.record 为 false：告警只判定、不写入 web_alarm（只用于本机调试）")
	}
	return e
}

// Observe 处理一轮检查结果：先确保已从 TD 恢复状态（恢复前不判定，避免重启后重复触发），再判定并写入事件。
func (e *Engine) Observe(ctx context.Context, snap monitor.Snapshot) {
	if !e.isLoaded() {
		if err := e.load(ctx); err != nil {
			if !e.loadFailing {
				e.log.Warn("读取告警状态失败，恢复前暂不判定告警", "error", err)
			}
			e.loadFailing = true
			return
		}
		e.loadFailing = false
	}

	e.mu.Lock()
	events := e.t.observe(snap)
	e.pending = append(e.pending, events...)
	if over := len(e.pending) - maxPending; over > 0 {
		e.log.Warn("告警事件积压过多，丢弃最旧的", "dropped", over)
		e.pending = slices.Clone(e.pending[over:])
	}
	pending := slices.Clone(e.pending)
	e.mu.Unlock()

	for _, ev := range events {
		action := "告警触发"
		if !ev.Active {
			action = "告警恢复"
		}
		e.log.Info(action, "furnace", ev.DeviceID, "ip", ev.DeviceIP, "type", ev.Type, "seconds", ev.Seconds)
	}
	if len(pending) == 0 {
		return
	}
	if !e.record {
		e.mu.Lock()
		e.pending = nil
		e.mu.Unlock()
		return
	}
	if err := e.insert(ctx, pending); err != nil {
		if !e.writeFailing {
			e.log.Warn("写入告警记录失败，下一轮重试", "pending", len(pending), "error", err)
		}
		e.writeFailing = true
		return
	}
	if e.writeFailing {
		e.log.Info("写入告警记录恢复", "written", len(pending))
	}
	e.writeFailing = false
	e.mu.Lock()
	e.pending = e.pending[len(pending):]
	e.mu.Unlock()
}

// Active 返回当前还没恢复的告警，按开始时间倒序。
func (e *Engine) Active() []Active {
	e.mu.RLock()
	out := make([]Active, 0, len(e.t.active))
	for _, a := range e.t.active {
		out = append(out, *a)
	}
	e.mu.RUnlock()
	slices.SortFunc(out, func(a, b Active) int {
		if c := cmp.Compare(b.Since, a.Since); c != 0 {
			return c
		}
		return strings.Compare(a.DeviceID+a.Type, b.DeviceID+b.Type)
	})
	return out
}

// Ready 返回是否已从 TD 恢复状态（之前 Active 为空不代表没有告警）。
func (e *Engine) Ready() bool { return e.isLoaded() }

func (e *Engine) isLoaded() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.loaded
}

func (e *Engine) load(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	query := fmt.Sprintf("SELECT device_ip, alarm_type, LAST_ROW(ts), LAST_ROW(device_id), LAST_ROW(active), LAST_ROW(seconds) "+
		"FROM %s PARTITION BY device_ip, alarm_type", e.table)
	res, err := e.db.Query(ctx, query)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, row := range res.Rows {
		ev := Event{DeviceIP: td.AsString(row[0]), Type: td.AsString(row[1]), DeviceID: td.AsString(row[3])}
		ev.Ts, _ = row[2].(time.Time)
		ev.Active, _ = row[4].(bool)
		ev.Seconds, _ = row[5].(float64)
		e.t.restore(ev)
	}
	e.loaded = true
	e.log.Info("告警状态已恢复", "active", len(e.t.active))
	return nil
}

func (e *Engine) insert(ctx context.Context, events []Event) error {
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	for start := 0; start < len(events); start += insertBatch {
		var b strings.Builder
		fmt.Fprintf(&b, "INSERT INTO %s (ts, device_ip, device_id, alarm_type, active, seconds) VALUES", e.table)
		for _, ev := range events[start:min(start+insertBatch, len(events))] {
			fmt.Fprintf(&b, " (%d, %s, %s, %s, %t, %g)", ev.Ts.UnixMilli(), td.String(ev.DeviceIP), td.String(ev.DeviceID),
				td.String(ev.Type), ev.Active, ev.Seconds)
		}
		if err := e.db.Exec(ctx, b.String()); err != nil {
			return err
		}
	}
	return nil
}
