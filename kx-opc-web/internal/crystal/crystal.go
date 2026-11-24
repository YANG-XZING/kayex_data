// Package crystal 按晶棒导出采集数据：kx-inner-processor 给 opc_data 每行补了 inner_crystal_no（晶编号）、
// inner_furnace_no（炉次号）、is_first（首棒），并维护晶棒台账 inner_crystal_info（开始 / 结束、棒次、引晶 / 等径时间点、
// 进入引晶时的功率）和炉次台账 inner_furnace_info（熔料 / 预热准备 / 预热熔接时间点）。
//
// 晶棒列表以主表为准（按晶编号汇总时间范围和行数），再补上台账里的信息：台账只记录“看得到开头”的晶棒，
// inner-processor 上线时正在拉的那一根不在台账里，但主表里有它的号，照样能导出。
package crystal

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"kx-opc-web/internal/config"
	"kx-opc-web/internal/history"
	"kx-opc-web/internal/td"
)

// ErrInvalid 是参数错误，接口返回 400。
var ErrInvalid = errors.New("参数不合法")

var crystalPattern = regexp.MustCompile(`^[0-9A-Za-z-]{1,64}$`)

const (
	// ongoingWithin 不在台账里的最新一根晶，最后一行在这么久以内算“进行中”
	ongoingWithin = 10 * time.Minute
	queryTimeout  = time.Minute
)

// Crystal 是一根晶棒。时间都是 TD 时钟的毫秒。
type Crystal struct {
	CrystalNo string `json:"crystal_no"`
	FurnaceNo string `json:"furnace_no"`
	IsFirst   bool   `json:"is_first"`
	Start     int64  `json:"start"`     // 台账的开始时间；不在台账里为第一行的时间
	End       *int64 `json:"end"`       // 台账的结束时间；不在台账里且已结束为最后一行的时间；进行中为 null
	FirstRow  int64  `json:"first_row"` // 查询范围内第一行 / 最后一行，导出按这个范围查
	LastRow   int64  `json:"last_row"`
	Rows      int64  `json:"rows"`
	Ongoing   bool   `json:"ongoing"`
	InLedger  bool   `json:"in_ledger"` // 台账里有：开头完整
	Seq       *int64 `json:"seq"`       // 棒次：本炉第几根，从 1 起；看不到开炉的炉次为 null
	Neck      *int64 `json:"neck_ts"`   // 进入引晶（工步 10）
	// 进入引晶那一行的主加功率、主加功率设定值（引晶功率设定值）
	NeckPower   *float64 `json:"neck_main_power"`
	NeckPowerSP *float64 `json:"neck_power_sp"`
	Body        *int64   `json:"body_ts"` // 进入等径（工步 13）
	// 所属炉次的初始阶段（炉次台账）：进入熔料（5）、熔料预热准备（7.x）、预热/熔接（8）
	Melt        *int64  `json:"melt_ts"`
	PreheatPrep *int64  `json:"preheat_prep_ts"`
	PreheatDip  *int64  `json:"preheat_dip_ts"`
	Seconds     float64 `json:"seconds"` // 时长：心跳从第一行到最后一行（进行中为到最新一行）
}

// Step 是一根晶里的一个工步号（一位小数，如 24.1）的汇总。工步常常来回跳（如 23.0 ↔ 24.1 交替多次），
// 所以时长是每一次进入后实际停留时间的总和，不是第一次进入到最后一次离开的跨度。
type Step struct {
	Code    string  `json:"code"` // 如 "24.1"
	Name    string  `json:"name"`
	Sub     bool    `json:"sub"` // 子工步
	Rows    int64   `json:"rows"`
	Seconds float64 `json:"seconds"` // 按心跳时间累计的停留时长
	Visits  int     `json:"visits"`  // 进入了几次
	First   int64   `json:"first"`   // 第一次进入（ts 毫秒），用于排序
}

// Run 是工步连续不变的一段，按时间顺序排列就是这根晶的工步时间线。
type Run struct {
	Code    string  `json:"code"`
	Start   int64   `json:"start"` // ts 毫秒
	Seconds float64 `json:"seconds"`
	Rows    int64   `json:"rows"`
}

// StepStats 是一根晶的工步统计。
type StepStats struct {
	Steps      []Step  `json:"steps"`       // 按第一次进入的时间排序
	Runs       []Run   `json:"runs"`        // 时间线
	Seconds    float64 `json:"seconds"`     // 心跳从第一行到最后一行的跨度
	GapSeconds float64 `json:"gap_seconds"` // 其中断数（相邻两行心跳相差超过 gap_limit）的时长，不计入任何工步
	Rows       int64   `json:"rows"`
}

// Service 查询和导出晶棒数据。
type Service struct {
	db        *td.DB
	cfg       config.Crystal
	heartCol  string                  // 上位机心跳列，工步和晶棒时长都按它算
	heartZone *time.Location          // 心跳值的时区
	names     map[int]config.StepName // 工步号 ×10 -> 名称
}

// New 创建服务；心跳列和时区与通讯预警用的一致（monitor 配置）。
func New(db *td.DB, cfg config.Crystal, mon config.Monitor) *Service {
	names := map[int]config.StepName{}
	for _, s := range cfg.StepNames {
		f, _ := strconv.ParseFloat(s.Code, 64) // 配置校验时已确认是数字
		names[Tenths(f)] = s
	}
	zone, err := time.LoadLocation(mon.HeartbeatZone)
	if err != nil {
		zone = time.UTC
	}
	return &Service{db: db, cfg: cfg, heartCol: mon.HeartbeatColumn, heartZone: zone, names: names}
}

// parseHeart 解析心跳值（yyyy-MM-dd HH:mm:ss），失败返回零值。
func (s *Service) parseHeart(v any) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(td.AsString(v)), s.heartZone)
	if err != nil || v == nil {
		return time.Time{}
	}
	return t
}

// Tenths 把工步号转成 ×10 的整数（24.1 → 241），避免 float32 比较不相等。
func Tenths(code float64) int { return int(math.Round(code * 10)) }

// StepInfo 返回工步号（×10）的名称和是否子工步。
func (s *Service) StepInfo(tenths int) (string, bool) {
	if n, ok := s.names[tenths]; ok {
		return n.Name, n.Sub
	}
	return "工步 " + codeString(tenths), false
}

func codeString(tenths int) string { return fmt.Sprintf("%.1f", float64(tenths)/10) }

// List 列出一台炉台 since 以来的晶棒，按开始时间倒序。tdNow 为 TD 时钟的当前时间。
func (s *Service) List(ctx context.Context, table, deviceIP string, since, tdNow time.Time) ([]Crystal, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	t, err := s.db.Table(table)
	if err != nil {
		return nil, fmt.Errorf("%w：%v", ErrInvalid, err)
	}
	q := fmt.Sprintf("SELECT %[1]s, FIRST(ts), LAST(ts), COUNT(*), LAST(%[2]s), LAST(%[3]s), FIRST(%[6]s), LAST(%[6]s) FROM %[4]s "+
		"WHERE ts >= %[5]d AND %[1]s IS NOT NULL GROUP BY %[1]s",
		td.Quote(s.cfg.CrystalColumn), td.Quote(s.cfg.FurnaceColumn), td.Quote(s.cfg.FirstColumn), t, since.UnixMilli(), td.Quote(s.heartCol))
	res, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	var list []Crystal
	byNo := map[string]*Crystal{}
	for _, row := range res.Rows {
		first, _ := row[1].(time.Time)
		last, _ := row[2].(time.Time)
		c := Crystal{CrystalNo: td.AsString(row[0]), FurnaceNo: td.AsString(row[4]), IsFirst: isTrue(row[5]),
			FirstRow: first.UnixMilli(), LastRow: last.UnixMilli(), Rows: toInt64(row[3])}
		if h0, h1 := s.parseHeart(row[6]), s.parseHeart(row[7]); !h0.IsZero() && !h1.IsZero() {
			c.Seconds = h1.Sub(h0).Seconds()
		}
		c.Start = c.FirstRow
		list = append(list, c)
	}
	for i := range list {
		byNo[list[i].CrystalNo] = &list[i]
	}

	// 台账：开始 / 结束时间更准（以换号那一行为界），还有棒次、工步时间点、进入引晶时的功率。
	// 新版 kx-inner-processor 上线前台账里还没有后加的列，退回只读基本列
	where := fmt.Sprintf("FROM %s WHERE device_ip = %s", s.db.Qualified(s.cfg.LedgerTable), td.String(deviceIP))
	crystalCol := td.Quote(s.cfg.CrystalColumn)
	ledger, err := s.db.Query(ctx, fmt.Sprintf("SELECT %s, start_ts, end_ts, is_first, crystal_seq, neck_ts, neck_main_power, neck_power_sp, body_ts %s", crystalCol, where))
	if err != nil {
		ledger, err = s.db.Query(ctx, fmt.Sprintf("SELECT %s, start_ts, end_ts, is_first %s", crystalCol, where))
	}
	if err != nil {
		return nil, fmt.Errorf("读取晶棒台账: %w", err)
	}
	for _, row := range ledger.Rows {
		c := byNo[td.AsString(row[0])]
		if c == nil {
			continue // 不在查询范围内
		}
		c.InLedger = true
		if ts, ok := row[1].(time.Time); ok {
			c.Start = ts.UnixMilli()
		}
		if ts, ok := row[2].(time.Time); ok {
			e := ts.UnixMilli()
			c.End = &e
		}
		c.IsFirst = isTrue(row[3])
		if len(row) > 4 {
			if f, ok := toFloat(row[4]); ok {
				n := int64(f)
				c.Seq = &n
			}
			c.Neck, c.NeckPower, c.NeckPowerSP, c.Body = msPtr(row[5]), floatPtr(row[6]), floatPtr(row[7]), msPtr(row[8])
		}
	}

	// 炉次台账：每一炉初始阶段的时间点，挂到这一炉的每根晶上。没有这张表或这几列时不显示
	fq := fmt.Sprintf("SELECT inner_furnace_no, melt_ts, preheat_prep_ts, preheat_dip_ts FROM %s WHERE device_ip = %s",
		s.db.Qualified(s.cfg.FurnaceTable), td.String(deviceIP))
	if furnaces, err := s.db.Query(ctx, fq); err == nil {
		for _, row := range furnaces.Rows {
			no := td.AsString(row[0])
			for i := range list {
				if c := &list[i]; c.FurnaceNo == no && no != "" {
					c.Melt, c.PreheatPrep, c.PreheatDip = msPtr(row[1]), msPtr(row[2]), msPtr(row[3])
				}
			}
		}
	}

	// 进行中：台账里没有结束时间的；不在台账里的，只有最新一根且最近还有数据才算
	latest := ""
	var latestRow int64 = math.MinInt64
	for _, c := range list {
		if c.LastRow > latestRow {
			latest, latestRow = c.CrystalNo, c.LastRow
		}
	}
	for i := range list {
		c := &list[i]
		switch {
		case c.InLedger:
			c.Ongoing = c.End == nil
		case c.CrystalNo == latest && tdNow.Sub(time.UnixMilli(c.LastRow)) < ongoingWithin:
			c.Ongoing = true
		default:
			e := c.LastRow
			c.End = &e
		}
		if c.Ongoing {
			c.End = nil
		}
	}
	slices.SortFunc(list, func(a, b Crystal) int { return cmp.Compare(b.Start, a.Start) })
	return list, nil
}

// Steps 按时间顺序逐行扫一根晶的工步号和心跳，统计每个工步号实际停留的时长和进入次数，并给出时间线。
// 相邻两行的心跳差算给前一行的工步；差值超过 gap_limit（采集中断）或为负（心跳回跳）的不计入。
// 用心跳而不是 ts：心跳是上位机按自己的时钟每秒写的，不受采集端服务器时钟被改写的影响。
func (s *Service) Steps(ctx context.Context, table, crystalNo string, from, to time.Time) (*StepStats, error) {
	where, err := s.where(table, crystalNo, from, to, nil)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	query := fmt.Sprintf("SELECT ts, %s, %s FROM %s ORDER BY ts", td.Quote(s.cfg.StepColumn), td.Quote(s.heartCol), where)
	sc := s.newStepCounter()
	err = s.db.QueryEach(ctx, query, nil, func(row []any) error {
		if code, ok := toFloat(row[1]); ok { // 没有工步号的行（不应出现）跳过
			ts, _ := row[0].(time.Time)
			sc.add(ts, code, s.parseHeart(row[2]))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return sc.result(), nil
}

// stepCounter 逐行累计工步统计，行必须按时间顺序传入。
type stepCounter struct {
	s     *Service
	gap   float64
	stats *StepStats
	agg   map[int]*Step

	prevCode              int
	prevHeart             time.Time
	firstHeart, lastHeart time.Time
}

func (s *Service) newStepCounter() *stepCounter {
	return &stepCounter{s: s, gap: s.cfg.GapLimit.Seconds(), stats: &StepStats{Steps: []Step{}, Runs: []Run{}},
		agg: map[int]*Step{}, prevCode: math.MinInt}
}

// add 加入一行：ts 为数据时间，heart 为心跳时间（解析失败为零值，这一行不参与计时）。
func (c *stepCounter) add(ts time.Time, code float64, heart time.Time) {
	k := Tenths(code)
	st := c.stats
	st.Rows++

	// 上一行到这一行的心跳差，算给上一行的工步
	if c.prevCode != math.MinInt && !heart.IsZero() && !c.prevHeart.IsZero() {
		dt := heart.Sub(c.prevHeart).Seconds()
		if dt >= 0 && dt <= c.gap {
			c.agg[c.prevCode].Seconds += dt
			st.Runs[len(st.Runs)-1].Seconds += dt
		} else if dt > c.gap {
			st.GapSeconds += dt
		}
	}

	a := c.agg[k]
	if a == nil {
		name, sub := c.s.StepInfo(k)
		a = &Step{Code: codeString(k), Name: name, Sub: sub, First: ts.UnixMilli()}
		c.agg[k] = a
	}
	a.Rows++
	if k != c.prevCode {
		a.Visits++
		st.Runs = append(st.Runs, Run{Code: a.Code, Start: ts.UnixMilli()})
	}
	st.Runs[len(st.Runs)-1].Rows++
	c.prevCode = k
	if !heart.IsZero() {
		c.prevHeart = heart
		if c.firstHeart.IsZero() {
			c.firstHeart = heart
		}
		c.lastHeart = heart
	}
}

func (c *stepCounter) result() *StepStats {
	st := c.stats
	if !c.firstHeart.IsZero() {
		st.Seconds = c.lastHeart.Sub(c.firstHeart).Seconds()
	}
	for _, a := range c.agg {
		st.Steps = append(st.Steps, *a)
	}
	slices.SortFunc(st.Steps, func(a, b Step) int { return cmp.Compare(a.First, b.First) })
	return st
}

// ExportRequest 是导出条件：一根晶（CrystalNo 为空则不限晶棒）在 [From, To] 内的行，Steps 不为空时只要这些工步号（×10），Cols 为要导出的点位。
type ExportRequest struct {
	Table     string
	CrystalNo string
	From, To  time.Time
	Steps     []int
	Cols      []string
	Headers   []string // 与 Cols 对应的表头
}

// exportChunk 导出时按这么长一段查询：一整根晶几十万行、上百列，一次查完结果太大，连接容易断
const exportChunk = time.Hour

// Export 逐行写出 CSV（UTF-8 带 BOM），按时间分段查询，不把结果整体读进内存。
// 中途出错时在文件末尾写一行“导出中断”，不会给出一个看起来完整、其实缺数据的文件。
func (s *Service) Export(ctx context.Context, r ExportRequest, w io.Writer) error {
	if r.To.Sub(r.From) > s.cfg.MaxRange {
		return fmt.Errorf("%w：一次最多导出 %d 天", ErrInvalid, int(s.cfg.MaxRange.Hours()/24))
	}
	if _, err := s.where(r.Table, r.CrystalNo, r.From, r.To, r.Steps); err != nil {
		return err // 参数错误在写文件之前返回
	}
	cols := make([]string, len(r.Cols))
	for i, c := range r.Cols {
		cols[i] = td.Quote(c)
	}

	bw := bufio.NewWriterSize(w, 256*1024)
	bw.WriteString("\xef\xbb\xbf")
	writeRecord(bw, append([]string{"时间"}, r.Headers...))
	record := make([]string, len(r.Cols)+1)
	for from := r.From; !from.After(r.To); from = from.Add(exportChunk) {
		to := from.Add(exportChunk - time.Millisecond) // 每段 [from, to]，首尾相接不重叠
		if to.After(r.To) {
			to = r.To
		}
		where, _ := s.where(r.Table, r.CrystalNo, from, to, r.Steps)
		query := fmt.Sprintf("SELECT ts, %s FROM %s ORDER BY ts", strings.Join(cols, ", "), where)
		err := s.db.QueryEach(ctx, query, nil, func(row []any) error {
			ts, _ := row[0].(time.Time)
			record[0] = ts.Local().Format("2006-01-02 15:04:05.000")
			for i := range r.Cols {
				record[i+1] = history.CSVValue(row[i+1])
			}
			return writeRecord(bw, record)
		})
		if err != nil {
			bw.WriteString("导出中断，数据不完整：" + err.Error() + "\n")
			bw.Flush()
			return err
		}
	}
	return bw.Flush()
}

// where 返回 “表 WHERE 条件”。
func (s *Service) where(table, crystalNo string, from, to time.Time, steps []int) (string, error) {
	t, err := s.db.Table(table)
	if err != nil {
		return "", fmt.Errorf("%w：%v", ErrInvalid, err)
	}
	if crystalNo != "" && !crystalPattern.MatchString(crystalNo) {
		return "", fmt.Errorf("%w：晶编号不合法", ErrInvalid)
	}
	if from.After(to) {
		return "", fmt.Errorf("%w：开始时间晚于结束时间", ErrInvalid)
	}
	w := fmt.Sprintf("%s WHERE ts >= %d AND ts <= %d", t, from.UnixMilli(), to.UnixMilli())
	if crystalNo != "" { // 为空时不按晶棒筛选：按时间全量导出
		w += fmt.Sprintf(" AND %s = %s", td.Quote(s.cfg.CrystalColumn), td.String(crystalNo))
	}
	if len(steps) > 0 {
		parts := make([]string, len(steps))
		for i, st := range steps {
			parts[i] = strconv.Itoa(st)
		}
		// 工步号是 FLOAT，24.1 存成 24.100000381，直接和 24.1 比不相等，按 ×10 取整比较
		w += fmt.Sprintf(" AND ROUND(%s * 10) IN (%s)", td.Quote(s.cfg.StepColumn), strings.Join(parts, ","))
	}
	return w, nil
}

// writeRecord 写一行 CSV，只在需要时加引号。
func writeRecord(w *bufio.Writer, fields []string) error {
	for i, f := range fields {
		if i > 0 {
			w.WriteByte(',')
		}
		if strings.ContainsAny(f, ",\"\r\n") {
			w.WriteByte('"')
			w.WriteString(strings.ReplaceAll(f, `"`, `""`))
			w.WriteByte('"')
		} else {
			w.WriteString(f)
		}
	}
	return w.WriteByte('\n')
}

func isTrue(v any) bool {
	f, ok := toFloat(v)
	if b, isBool := v.(bool); isBool {
		return b
	}
	return ok && f != 0
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float32:
		f, _ := strconv.ParseFloat(strconv.FormatFloat(float64(x), 'g', -1, 32), 64)
		return f, true
	case float64:
		return x, true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint8:
		return float64(x), true
	}
	return 0, false
}

func toInt64(v any) int64 {
	f, _ := toFloat(v)
	return int64(f)
}

func floatPtr(v any) *float64 {
	if f, ok := toFloat(v); ok && !math.IsNaN(f) && !math.IsInf(f, 0) {
		return &f
	}
	return nil
}

func msPtr(v any) *int64 {
	if t, ok := v.(time.Time); ok {
		ms := t.UnixMilli()
		return &ms
	}
	return nil
}

// Series 返回一根晶（steps 不为空时只取这些工步）的曲线数据。一根晶常有十几个小时到几天，逐行画太多，
// 所以按 maxPoints 把时间均分成段（INTERVAL），每段取最后一个值（LAST，开关量也适用）；
// 没有数据的段（如被工步筛掉的时间）为 null，曲线在那里断开。返回每段的秒数。
func (s *Service) Series(ctx context.Context, table, crystalNo string, from, to time.Time, steps []int, cols []string, maxPoints int) (*history.Series, int, error) {
	if crystalNo == "" {
		return nil, 0, fmt.Errorf("%w：请选择晶棒", ErrInvalid)
	}
	where, err := s.where(table, crystalNo, from, to, steps)
	if err != nil {
		return nil, 0, err
	}
	bucket := int(math.Ceil(to.Sub(from).Seconds() / float64(maxPoints)))
	bucket = max(bucket, 1)
	sel := make([]string, len(cols))
	for i, c := range cols {
		sel[i] = "LAST(" + td.Quote(c) + ")"
	}
	query := fmt.Sprintf("SELECT _wstart, %s FROM %s INTERVAL(%ds) FILL(NULL)", strings.Join(sel, ", "), where, bucket)
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	out := &history.Series{Ts: []int64{}, Values: map[string][]any{}}
	for _, c := range cols {
		out.Values[c] = []any{}
	}
	err = s.db.QueryEach(ctx, query, nil, func(row []any) error {
		ts, _ := row[0].(time.Time)
		out.Ts = append(out.Ts, ts.UnixMilli())
		for i, c := range cols {
			out.Values[c] = append(out.Values[c], history.ChartValue(row[i+1]))
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	out.Rows = len(out.Ts)
	return out, bucket, nil
}
