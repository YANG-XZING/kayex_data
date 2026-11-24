// Package monitor 定期检查全部炉台的心跳：一条 SQL 取每台炉台最新一行的 ts 和心跳值，
// 判断心跳是否停止、心跳时间与基准时间是否一致，结果作为炉台目录供页面使用。
//
// 时间换算：数据的 ts 是采集端（TD 服务器）时钟打的，心跳值是工控机时钟，都不准；
// 每轮用 SELECT NOW() 测出 TD 时钟与基准时间之差，把 ts 换算成基准时间再比较。
package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"kx-opc-web/internal/clock"
	"kx-opc-web/internal/config"
	"kx-opc-web/internal/td"
)

const (
	queryTimeout = 10 * time.Second
	// heartLookback LAST_ROW 读到心跳为空时，往前找最近一个心跳值的时间范围
	heartLookback = 5 * time.Minute
)

// 炉台状态
const (
	StatusOnline    = "online"  // 心跳正常
	StatusStopped   = "stopped" // 心跳停止：超过 heartbeat_timeout 没有变化
	StatusNoData    = "no_data" // 没有任何数据，或心跳值为空 / 无法解析
	heartbeatLayout = "2006-01-02 15:04:05"
)

// Furnace 是一台炉台的目录信息和状态。
type Furnace struct {
	Table    string `json:"table"`
	DeviceID string `json:"device_id"`
	DeviceIP string `json:"device_ip"`
	Area     string `json:"area,omitempty"`
	Status   string `json:"status"`
	// LastTs 最新一行的 ts（TD 时钟，毫秒）；0 表示没有数据
	LastTs int64 `json:"last_ts"`
	// Heartbeat 最新一行的心跳原值
	Heartbeat string `json:"heartbeat"`
	// HeartbeatAgeS 心跳多少秒没有变化（基准时间）
	HeartbeatAgeS float64 `json:"heartbeat_age_s"`
	// SkewS 心跳时间 - 基准时间（秒），正数为工控机时钟快；心跳停止或无数据时为 null
	SkewS *float64 `json:"skew_s"`
	// SkewAlarm 时间差绝对值超过 clock_skew_limit
	SkewAlarm bool `json:"skew_alarm"`
}

// Snapshot 是一轮检查的结果。
type Snapshot struct {
	CheckedAt time.Time `json:"checked_at"` // 检查时刻（基准时间）
	// TDOffsetMs TD 服务器时钟比基准时间快多少毫秒（负数为慢）
	TDOffsetMs int64     `json:"td_offset_ms"`
	Furnaces   []Furnace `json:"furnaces"`
	Error      string    `json:"error,omitempty"` // 本轮查询失败的原因；失败时 Furnaces 为上一轮的结果
}

// heartState 跟踪一台炉台心跳值的变化。
type heartState struct {
	value     string
	changedAt time.Time // 心跳值最近一次变化的时刻（基准时间）
}

// Monitor 定期检查炉台状态，并发安全。
type Monitor struct {
	cfg   config.Monitor
	areas func(ip string) string
	db    *td.DB
	clock *clock.Clock
	log   *slog.Logger
	zone  *time.Location

	// 以下只在 Run 所在协程中访问
	tags   map[string]Furnace // 子表 -> 标签
	tagsAt time.Time
	hearts map[string]*heartState

	mu       sync.RWMutex
	snapshot Snapshot
	tdOffset time.Duration
	tables   map[string]bool

	// observer 每轮检查成功后调用（在 Run 所在协程中），用于告警判定
	observer func(context.Context, Snapshot)
}

// SetObserver 设置每轮检查成功后的回调，需在 Run 之前调用。
func (m *Monitor) SetObserver(fn func(context.Context, Snapshot)) { m.observer = fn }

// New 创建检查器。
func New(cfg config.Monitor, areas func(string) string, db *td.DB, clk *clock.Clock, log *slog.Logger) *Monitor {
	zone, _ := time.LoadLocation(cfg.HeartbeatZone) // 配置校验时已确认合法
	return &Monitor{
		cfg: cfg, areas: areas, db: db, clock: clk, log: log.With("module", "monitor"), zone: zone,
		hearts: map[string]*heartState{}, tables: map[string]bool{},
		snapshot: Snapshot{Furnaces: []Furnace{}, Error: "启动中，尚未完成第一次检查"},
	}
}

// Snapshot 返回最近一轮的检查结果。
func (m *Monitor) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snapshot
}

// HasTable 返回子表是否是已知炉台（用于校验前端传来的子表名）。
func (m *Monitor) HasTable(table string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tables[table]
}

// TDNow 返回 TD 服务器时钟的当前时间（由基准时间和最近一次测得的偏差推算），用于计算数据新鲜度。
func (m *Monitor) TDNow() time.Time {
	m.mu.RLock()
	offset := m.tdOffset
	m.mu.RUnlock()
	return m.clock.Now().Add(offset)
}

// Run 立即检查一次，之后按周期检查，直到 ctx 结束。
func (m *Monitor) Run(ctx context.Context) {
	ticker := time.NewTicker(m.cfg.Interval)
	defer ticker.Stop()
	failing := false
	for {
		if err := m.check(ctx); err != nil {
			if !failing {
				m.log.Warn("炉台状态检查失败，恢复前沿用上一次的结果", "error", err)
			}
			failing = true
			m.mu.Lock()
			m.snapshot.Error = err.Error()
			m.mu.Unlock()
		} else {
			if failing {
				failing = false
				m.log.Info("炉台状态检查恢复")
			}
			if m.observer != nil {
				m.observer(ctx, m.Snapshot())
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *Monitor) check(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	tdOffset, err := m.measureTDOffset(ctx)
	if err != nil {
		return fmt.Errorf("SELECT NOW(): %w", err)
	}
	if m.tags == nil || time.Since(m.tagsAt) >= m.cfg.TagsRefresh {
		if err := m.loadTags(ctx); err != nil {
			return fmt.Errorf("读取炉台清单: %w", err)
		}
	}
	last, err := m.lastRows(ctx)
	if err != nil {
		return fmt.Errorf("读取最新行: %w", err)
	}

	now := m.clock.Now()
	furnaces := make([]Furnace, 0, len(m.tags))
	tables := make(map[string]bool, len(m.tags))
	for table, f := range m.tags {
		tables[table] = true
		f.Status = StatusNoData
		if row, ok := last[table]; ok {
			m.evaluate(&f, row, tdOffset, now)
		}
		furnaces = append(furnaces, f)
	}
	sort.Slice(furnaces, func(i, j int) bool {
		if furnaces[i].DeviceID != furnaces[j].DeviceID {
			return furnaces[i].DeviceID < furnaces[j].DeviceID
		}
		return furnaces[i].Table < furnaces[j].Table
	})

	m.mu.Lock()
	m.snapshot = Snapshot{CheckedAt: now, TDOffsetMs: tdOffset.Milliseconds(), Furnaces: furnaces}
	m.tdOffset, m.tables = tdOffset, tables
	m.mu.Unlock()
	return nil
}

type lastRow struct {
	ts    time.Time
	heart string
}

// evaluate 根据最新一行判断心跳状态和时间差。
func (m *Monitor) evaluate(f *Furnace, row lastRow, tdOffset time.Duration, now time.Time) {
	// 别的程序对同一行做部分列写入（如 inner_* 每 5 秒补写）时，LAST_ROW 的缓存会短暂返回心跳为 NULL 的行：沿用上一次的心跳值
	if st := m.hearts[f.Table]; row.heart == "" && st != nil {
		row.heart = st.value
	}
	f.LastTs = row.ts.UnixMilli()
	f.Heartbeat = row.heart
	if row.heart == "" || row.ts.IsZero() {
		return
	}
	// 这一行到达采集端的时刻，换算成基准时间
	rowAt := row.ts.Add(-tdOffset)

	st := m.hearts[f.Table]
	if st == nil {
		st = &heartState{value: row.heart, changedAt: rowAt}
		m.hearts[f.Table] = st
	} else if st.value != row.heart {
		st.value, st.changedAt = row.heart, rowAt
	}
	age := now.Sub(st.changedAt)
	f.HeartbeatAgeS = roundSeconds(age)
	if age > m.cfg.HeartbeatTimeout {
		f.Status = StatusStopped
		return
	}

	beat, err := time.ParseInLocation(heartbeatLayout, row.heart, m.zone)
	if err != nil {
		f.Status = StatusNoData
		return
	}
	f.Status = StatusOnline
	// 心跳值精确到秒（截断），按 +0.5 秒计
	skew := beat.Add(500 * time.Millisecond).Sub(rowAt)
	s := roundSeconds(skew)
	f.SkewS = &s
	f.SkewAlarm = skew > m.cfg.ClockSkewLimit || skew < -m.cfg.ClockSkewLimit
}

// measureTDOffset 测 TD 服务器时钟比基准时间快多少：用请求往返的中点对齐。
func (m *Monitor) measureTDOffset(ctx context.Context) (time.Duration, error) {
	before := m.clock.Now()
	tdNow, err := m.db.Now(ctx)
	after := m.clock.Now()
	if err != nil {
		return 0, err
	}
	return tdNow.Sub(before.Add(after.Sub(before) / 2)), nil
}

func (m *Monitor) loadTags(ctx context.Context) error {
	res, err := m.db.Query(ctx, "SELECT DISTINCT tbname, device_id, device_ip FROM "+m.db.Stable())
	if err != nil {
		return err
	}
	tags := make(map[string]Furnace, len(res.Rows))
	for _, row := range res.Rows {
		f := Furnace{Table: td.AsString(row[0]), DeviceID: td.AsString(row[1]), DeviceIP: td.AsString(row[2])}
		f.Area = m.areas(f.DeviceIP)
		tags[f.Table] = f
	}
	m.tags, m.tagsAt = tags, time.Now()
	return nil
}

func (m *Monitor) lastRows(ctx context.Context) (map[string]lastRow, error) {
	query := fmt.Sprintf("SELECT tbname, LAST_ROW(ts), LAST_ROW(%s) FROM %s PARTITION BY tbname", td.Quote(m.cfg.HeartbeatColumn), m.db.Stable())
	res, err := m.db.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	last := make(map[string]lastRow, len(res.Rows))
	var missing []string
	for _, row := range res.Rows {
		ts, _ := row[1].(time.Time)
		r := lastRow{ts: ts, heart: td.AsString(row[2])}
		last[td.AsString(row[0])] = r
		if r.heart == "" && !ts.IsZero() {
			missing = append(missing, td.String(td.AsString(row[0])))
		}
	}
	if len(missing) == 0 {
		return last, nil
	}
	// kx-inner-processor 每 5 秒对最新几行补写 inner_* 列，补写后 LAST_ROW 缓存里这一行的心跳会短暂为 NULL。
	// 本检查也是 5 秒一轮，两者相位接近时会连续一分钟都读到 NULL，被误判为心跳停止；
	// 所以对心跳为空的炉台，再用 LAST（跳过 NULL）取最近一段时间里最新的心跳值。LAST 不走缓存，限定时间范围才快
	query = fmt.Sprintf("SELECT tbname, LAST(%s) FROM %s WHERE ts > NOW - %ds AND tbname IN (%s) PARTITION BY tbname",
		td.Quote(m.cfg.HeartbeatColumn), m.db.Stable(), int(heartLookback.Seconds()), strings.Join(missing, ","))
	res, err = m.db.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	for _, row := range res.Rows {
		table := td.AsString(row[0])
		if r, ok := last[table]; ok {
			r.heart = td.AsString(row[1])
			last[table] = r
		}
	}
	return last, nil
}

func roundSeconds(d time.Duration) float64 {
	return float64(d.Round(100*time.Millisecond).Milliseconds()) / 1000
}
