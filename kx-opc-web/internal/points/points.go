// Package points 管理点位信息：点位（opc_data 的列名，就是 OPC 的 tag）对应的中文名和 DB 点位（PLC 地址，如 33.24-0），
// 全站显示和导出表头都用它；以及客户可见（客户账户的“上位机数据”只看这些点位，管理员在“配置信息”里勾选）。
//
// 存在 opc_point_info 表（普通表，主键 ts + point）：每次修改追加一行，每个点位的最新一行即当前值。
// 可以直接在数据库里 INSERT，也可以在页面上传 CSV；中文名、DB 点位都为空且不是客户可见表示没有配置。
package points

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"

	"kx-opc-web/internal/td"
)

const (
	maxPointBytes   = 64 // 与 TD 列名长度一致
	maxNameRunes    = 64
	maxDBPointBytes = 64
	maxImportRows   = 5000
	queryTimeout    = 15 * time.Second
	reloadEvery     = time.Minute // 别人直接改了数据库，一分钟内生效

	// refreshAge 超过这么久没改的点位重写一行，避免被库的 KEEP（1095 天）删掉
	refreshAge      = 365 * 24 * time.Hour
	refreshInterval = 24 * time.Hour
)

// ErrInvalid 是参数校验错误，接口返回 400。
var ErrInvalid = errors.New("参数不合法")

// Info 是一个点位的信息。
type Info struct {
	Name     string `json:"name,omitempty"`     // 中文名
	DBPoint  string `json:"db_point,omitempty"` // DB 点位（PLC 地址）
	Customer bool   `json:"customer,omitempty"` // 客户可见
}

// Patch 是上传的一行：nil 表示文件里没有这一列，沿用原值。
type Patch struct {
	Name     *string
	DBPoint  *string
	Customer *bool
}

type entry struct {
	ts   time.Time
	info Info
}

// Store 是点位信息的内存副本，定期从 TD 重新读取，并发安全。
type Store struct {
	db    *td.DB
	now   func() time.Time
	log   *slog.Logger
	table string

	mu      sync.RWMutex
	entries map[string]entry
	loaded  bool
}

// New 创建点位信息存储，Run 之后才有数据。
func New(db *td.DB, now func() time.Time, log *slog.Logger) *Store {
	return &Store{db: db, now: now, log: log.With("module", "points"), table: db.Qualified(td.PointTable), entries: map[string]entry{}}
}

// Run 每分钟重新读取一次，每天把长期没改的点位重写一行，直到 ctx 结束。
func (s *Store) Run(ctx context.Context) {
	var lastRefresh time.Time
	for {
		if err := s.load(ctx); err != nil {
			s.log.Warn("读取点位信息失败，稍后重试", "error", err)
		} else if time.Since(lastRefresh) > refreshInterval {
			lastRefresh = time.Now()
			if err := s.refreshOld(ctx); err != nil {
				s.log.Warn("重写长期未改的点位信息失败", "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(reloadEvery):
		}
	}
}

func (s *Store) load(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	res, err := s.db.Query(ctx, fmt.Sprintf("SELECT point, LAST_ROW(ts), LAST_ROW(name), LAST_ROW(db_point), LAST_ROW(customer) FROM %s PARTITION BY point", s.table))
	if err != nil {
		return err
	}
	entries := make(map[string]entry, len(res.Rows))
	for _, row := range res.Rows {
		ts, _ := row[1].(time.Time)
		customer, _ := row[4].(bool)
		entries[td.AsString(row[0])] = entry{ts: ts, info: Info{Name: str(row[2]), DBPoint: str(row[3]), Customer: customer}}
	}
	s.mu.Lock()
	first := !s.loaded
	s.entries, s.loaded = entries, true
	s.mu.Unlock()
	if first {
		s.log.Info("点位信息已加载", "points", len(entries))
	}
	return nil
}

func str(v any) string {
	if v == nil {
		return ""
	}
	return td.AsString(v)
}

// All 返回全部已配置的点位信息（点位 -> 信息）。
func (s *Store) All() map[string]Info {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]Info, len(s.entries))
	for p, e := range s.entries {
		if e.info != (Info{}) {
			out[p] = e.info
		}
	}
	return out
}

// Customer 返回点位是否客户可见。
func (s *Store) Customer(point string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.entries[point].info.Customer
}

// Loaded 返回是否已从 TD 读出。
func (s *Store) Loaded() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loaded
}

// ParseCSV 解析上传的 CSV：表头含“点位”（必需）以及“中文名”“DB点位”（可选，顺序不限）。
// Excel 另存的 CSV 一般是 GBK 编码，不是合法 UTF-8 时按 GBK 解码。
func ParseCSV(data []byte) (map[string]Patch, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(data) {
		dec, err := simplifiedchinese.GB18030.NewDecoder().Bytes(data)
		if err != nil {
			return nil, fmt.Errorf("%w：文件既不是 UTF-8 也不是 GBK 编码", ErrInvalid)
		}
		data = dec
	}
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("%w：读不到表头", ErrInvalid)
	}
	pointCol, nameCol, dbCol := -1, -1, -1
	for i, h := range header {
		switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(h), " ", "")) {
		case "点位", "tag", "opc点位", "point":
			pointCol = i
		case "中文名", "名称", "name":
			nameCol = i
		case "db点位", "db", "plc点位", "db_point":
			dbCol = i
		}
	}
	if pointCol < 0 {
		return nil, fmt.Errorf("%w：表头里没有“点位”列（应为 点位,中文名,DB点位）", ErrInvalid)
	}
	cell := func(rec []string, i int) *string {
		if i < 0 {
			return nil
		}
		v := ""
		if i < len(rec) {
			v = strings.TrimSpace(rec[i])
		}
		return &v
	}
	out := map[string]Patch{}
	for line := 2; ; line++ {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w：第 %d 行格式错误：%v", ErrInvalid, line, err)
		}
		p := *cell(rec, pointCol)
		if p == "" {
			continue
		}
		out[p] = Patch{Name: cell(rec, nameCol), DBPoint: cell(rec, dbCol)}
		if len(out) > maxImportRows {
			return nil, fmt.Errorf("%w：一次最多导入 %d 个点位", ErrInvalid, maxImportRows)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w：文件里没有点位", ErrInvalid)
	}
	return out, nil
}

// Set 批量修改点位信息，返回实际改动的点位数（没有变化的不写库）。
func (s *Store) Set(ctx context.Context, patches map[string]Patch) (int, error) {
	if !s.Loaded() {
		return 0, errors.New("点位信息还没从数据库读出，请稍后再试")
	}
	cur := s.All()
	changed := map[string]Info{}
	for p, pa := range patches {
		if strings.ContainsAny(p, "'\\`") || len(p) > maxPointBytes {
			return 0, fmt.Errorf("%w：点位名 %q", ErrInvalid, p)
		}
		old := cur[p]
		next := old
		if pa.Name != nil {
			next.Name = *pa.Name
		}
		if pa.DBPoint != nil {
			next.DBPoint = NormalizeDBPoint(*pa.DBPoint)
		}
		if pa.Customer != nil {
			next.Customer = *pa.Customer
		}
		if utf8.RuneCountInString(next.Name) > maxNameRunes || len(next.DBPoint) > maxDBPointBytes {
			return 0, fmt.Errorf("%w：点位 %s 的中文名不能超过 %d 字、DB 点位不能超过 %d 字节", ErrInvalid, p, maxNameRunes, maxDBPointBytes)
		}
		if next != old {
			changed[p] = next
		}
	}
	return len(changed), s.insert(ctx, changed)
}

// insert 把一批点位写成 INSERT（同一时刻），ts 保证比该点位的上一行晚。
func (s *Store) insert(ctx context.Context, items map[string]Info) error {
	if len(items) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ts := s.now()
	for p := range items {
		if last := s.entries[p].ts; !ts.After(last) {
			ts = last.Add(time.Millisecond)
		}
	}
	ts = ts.Truncate(time.Millisecond)
	keys := slices.Sorted(maps.Keys(items))
	// 一条 SQL 太长 TD 会拒绝，按 500 个点位一批
	for start := 0; start < len(keys); start += 500 {
		var b strings.Builder
		fmt.Fprintf(&b, "INSERT INTO %s (ts, point, name, db_point, customer) VALUES", s.table)
		for _, p := range keys[start:min(start+500, len(keys))] {
			in := items[p]
			fmt.Fprintf(&b, " (%d, %s, %s, %s, %t)", ts.UnixMilli(), td.String(p), td.String(in.Name), td.String(in.DBPoint), in.Customer)
		}
		cctx, cancel := context.WithTimeout(ctx, queryTimeout)
		err := s.db.Exec(cctx, b.String())
		cancel()
		if err != nil {
			return fmt.Errorf("写入点位信息: %w", err)
		}
	}
	for p, in := range items {
		s.entries[p] = entry{ts: ts, info: in}
	}
	return nil
}

// refreshOld 把超过 refreshAge 没改过的点位原样重写一行。
func (s *Store) refreshOld(ctx context.Context) error {
	cutoff := s.now().Add(-refreshAge)
	stale := map[string]Info{}
	s.mu.RLock()
	for p, e := range s.entries {
		if e.info != (Info{}) && e.ts.Before(cutoff) {
			stale[p] = e.info
		}
	}
	s.mu.RUnlock()
	if len(stale) == 0 {
		return nil
	}
	if err := s.insert(ctx, stale); err != nil {
		return err
	}
	s.log.Info("已重写长期未改的点位信息", "points", len(stale))
	return nil
}

// SetCustomer 把客户可见的点位设为 list（其余点位都取消），返回实际改动的点位数。
func (s *Store) SetCustomer(ctx context.Context, list []string) (int, error) {
	want := map[string]bool{}
	for _, p := range list {
		want[p] = true
	}
	patches := map[string]Patch{}
	s.mu.RLock()
	for p, e := range s.entries {
		if e.info.Customer && !want[p] {
			patches[p] = Patch{Customer: new(bool)}
		}
	}
	s.mu.RUnlock()
	yes := true
	for p := range want {
		patches[p] = Patch{Customer: &yes}
	}
	return s.Set(ctx, patches)
}

// WriteCSV 按 columns 的顺序写出点位信息（带 BOM，Excel 直接打开），用于下载后修改再上传。
func WriteCSV(w io.Writer, columns []string, all map[string]Info) error {
	if _, err := w.Write([]byte("\xef\xbb\xbf")); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"点位", "中文名", "DB点位"})
	for _, c := range columns {
		in := all[c]
		_ = cw.Write([]string{c, in.Name, in.DBPoint})
	}
	cw.Flush()
	return cw.Error()
}

var (
	// DB15.DBD184、DB15.DB184、DB48DBD180、DB10.DBX12.2 这类 S7 写法
	dbAddrPattern = regexp.MustCompile(`(?i)^DB(\d+)\.?DB[DXWB]?(\d+)(?:\.(\d+))?$`)
	// 15.120 这种漏了 -0 的写法
	shortAddrPattern = regexp.MustCompile(`^(\d+)\.(\d+)$`)
)

// NormalizeDBPoint 把 DB 点位统一写成“块号.偏移-位”，如 DB15.DBD184 → 15.184-0、DB10.DBX12.2 → 10.12-2、15.120 → 15.120-0；
// 与取数页的 PLC 点位写法一致。不是地址的（如“上位机”）原样返回。
func NormalizeDBPoint(s string) string {
	s = strings.TrimSpace(s)
	if m := dbAddrPattern.FindStringSubmatch(s); m != nil {
		bit := m[3]
		if bit == "" {
			bit = "0"
		}
		return m[1] + "." + m[2] + "-" + bit
	}
	if m := shortAddrPattern.FindStringSubmatch(s); m != nil {
		return m[1] + "." + m[2] + "-0"
	}
	return s
}
