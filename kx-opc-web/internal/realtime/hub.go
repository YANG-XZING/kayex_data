// Package realtime 通过 WebSocket 推送炉台实时数据。
//
// 只查“有人在看”的炉台：每个周期用一条 SQL 取所有被订阅子表的最新一行（LAST_ROW 走 TD 的缓存），
// 同一台炉台不论多少人在看只查一次；ts 没变就不推送，变了只推送变化的点位，新订阅者先收到一份全量。
package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"kx-opc-web/internal/td"
)

const (
	queryTimeout = 5 * time.Second
	// maxTablesPerConn 一个连接最多同时订阅的炉台数
	maxTablesPerConn = 16
)

// Directory 用于校验订阅的子表是否存在，并给出 TD 时钟的当前时间。
type Directory interface {
	HasTable(table string) bool
	TDNow() time.Time
}

// rowMessage 是推送给前端的一行数据。
type rowMessage struct {
	Type  string         `json:"t"` // row
	Table string         `json:"table"`
	Ts    int64          `json:"ts"`   // 采集时间（TD 时钟，毫秒）
	Full  bool           `json:"full"` // true：Values 是全部点位；false：只有变化的点位
	Value map[string]any `json:"v"`
}

// nowMessage 每个周期发一次，前端用它计算数据新鲜度（不依赖浏览器时钟）。
type nowMessage struct {
	Type string `json:"t"` // now
	Now  int64  `json:"now"`
}

type tableState struct {
	ts     int64
	values map[string]any
}

// Hub 管理订阅并定期查询，并发安全。
type Hub struct {
	db       *td.DB
	dir      Directory
	interval time.Duration
	log      *slog.Logger
	kick     chan struct{}

	mu     sync.Mutex
	subs   map[string]map[*conn]struct{} // 子表 -> 订阅者
	states map[string]*tableState        // 子表 -> 最近一次推送的值
}

// NewHub 创建推送中心。
func NewHub(db *td.DB, dir Directory, interval time.Duration, log *slog.Logger) *Hub {
	return &Hub{
		db: db, dir: dir, interval: interval, log: log.With("module", "realtime"),
		kick: make(chan struct{}, 1),
		subs: map[string]map[*conn]struct{}{}, states: map[string]*tableState{},
	}
}

// Run 按周期查询并推送，直到 ctx 结束。有新订阅时立即查一次，不等下个周期。
func (h *Hub) Run(ctx context.Context) {
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()
	failing := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-h.kick:
		}
		if err := h.poll(ctx); err != nil {
			if !failing {
				h.log.Warn("实时数据查询失败", "error", err)
			}
			failing = true
		} else if failing {
			failing = false
			h.log.Info("实时数据查询恢复")
		}
	}
}

// subscribe 把连接的订阅替换为 tables；返回被忽略的子表（不存在或超出上限）。
func (h *Hub) subscribe(c *conn, tables []string) []string {
	var accepted, ignored []string
	for _, t := range tables {
		if len(accepted) < maxTablesPerConn && h.dir.HasTable(t) && !slices.Contains(accepted, t) {
			accepted = append(accepted, t)
		} else {
			ignored = append(ignored, t)
		}
	}

	h.mu.Lock()
	for _, t := range c.tables {
		if !slices.Contains(accepted, t) {
			h.removeLocked(c, t)
		}
	}
	var fresh []string
	for _, t := range accepted {
		if slices.Contains(c.tables, t) {
			continue
		}
		if h.subs[t] == nil {
			h.subs[t] = map[*conn]struct{}{}
		}
		h.subs[t][c] = struct{}{}
		if st := h.states[t]; st != nil {
			c.sendRow(rowMessage{Type: "row", Table: t, Ts: st.ts, Full: true, Value: st.values}, nil)
		} else {
			fresh = append(fresh, t)
		}
	}
	c.tables = accepted
	h.mu.Unlock()

	if len(fresh) > 0 {
		select {
		case h.kick <- struct{}{}:
		default:
		}
	}
	return ignored
}

// unsubscribeAll 在连接关闭时调用。
func (h *Hub) unsubscribeAll(c *conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, t := range c.tables {
		h.removeLocked(c, t)
	}
	c.tables = nil
}

func (h *Hub) removeLocked(c *conn, table string) {
	delete(h.subs[table], c)
	if len(h.subs[table]) == 0 {
		delete(h.subs, table)
		delete(h.states, table)
	}
}

func (h *Hub) poll(ctx context.Context) error {
	h.mu.Lock()
	tables := slices.Sorted(maps.Keys(h.subs))
	conns := map[*conn]struct{}{}
	for _, subs := range h.subs {
		maps.Copy(conns, subs)
	}
	h.mu.Unlock()
	if len(tables) == 0 {
		return nil
	}

	now, _ := json.Marshal(nowMessage{Type: "now", Now: h.dir.TDNow().UnixMilli()})
	for c := range conns {
		c.send(now)
	}

	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	quoted := make([]string, len(tables))
	for i, t := range tables {
		quoted[i] = "'" + t + "'" // 子表名已在订阅时校验为已知炉台
	}
	query := fmt.Sprintf("SELECT tbname, LAST_ROW(*) FROM %s WHERE tbname IN (%s) PARTITION BY tbname", h.db.Stable(), strings.Join(quoted, ","))
	res, err := h.db.Query(ctx, query)
	if err != nil {
		return err
	}
	// 列：tbname, last_row(ts), last_row(点位)…
	names := make([]string, len(res.Columns))
	for i, c := range res.Columns {
		names[i] = td.UnwrapFunc(c)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	for _, row := range res.Rows {
		table := td.AsString(row[0])
		subs := h.subs[table]
		if len(subs) == 0 {
			continue // 查询期间已退订
		}
		tsTime, ok := row[1].(time.Time)
		if !ok {
			continue
		}
		ts := tsTime.UnixMilli()
		st := h.states[table]
		if st != nil && st.ts == ts {
			continue
		}
		values := make(map[string]any, len(row)-2)
		for i := 2; i < len(row); i++ {
			v := td.JSONValue(row[i])
			// 采集端每行都写全量，NULL 只会来自别的程序对同一行的部分列写入（如 inner_* 每 5 秒补写一次）：
			// 这时 LAST_ROW 的缓存会短暂返回只有这几列的行，其余点位沿用上一次的值，曲线才不会断
			if v == nil && st != nil {
				v = st.values[names[i]]
			}
			values[names[i]] = v
		}
		msg := rowMessage{Type: "row", Table: table, Ts: ts, Full: st == nil, Value: values}
		if st != nil {
			msg.Value = diff(st.values, values)
		}
		h.states[table] = &tableState{ts: ts, values: values}
		data, err := json.Marshal(msg)
		if err != nil {
			h.log.Warn("实时数据编码失败", "table", table, "error", err)
			continue
		}
		for c := range subs {
			c.sendRow(msg, data)
		}
	}
	return nil
}

// diff 返回 next 中相对 prev 变化了的点位（含新增的点位）。
func diff(prev, next map[string]any) map[string]any {
	changed := map[string]any{}
	for k, v := range next {
		old, ok := prev[k]
		if !ok || !reflect.DeepEqual(old, v) {
			changed[k] = v
		}
	}
	return changed
}
