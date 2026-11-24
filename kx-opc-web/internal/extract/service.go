package extract

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"kx-opc-web/internal/config"
	"kx-opc-web/internal/td"
)

const (
	// DisplayRows 页面表格最多显示的行数（导出仍是全量）
	DisplayRows  = 1000
	chunkTimeout = 2 * time.Minute
	inputLayout  = "2006-01-02 15:04:05"
)

// ErrInvalid 是参数错误，接口返回 400。
var ErrInvalid = errors.New("参数不合法")

// Point 是一个要取的点位。
type Point struct {
	Point string `json:"point"`
	Name  string `json:"name"`
	On    bool   `json:"on"`
}

// Request 是 PLC / CCD 取数条件，字段与原网站一致（数据库连接改为在 config.yaml 里配置）。
type Request struct {
	Device       string
	Start, End   time.Time
	EndPlusDay   bool // 原网站固定把结束时间往后延 1 天
	PLCPoints    []Point
	CCDPoints    []Point
	IncludeCCD   bool
	JoinMode     string // exact / asof
	AsofTol      int
	MinSegRows   int
	StartStep    float64
	AllowedSteps map[float64]bool
}

// Result 是一次查询的结果和分段。
type Result struct {
	Table    *Table
	Device   string
	QStart   time.Time
	QEnd     time.Time
	StepCol  string // 工步列名（点位 33.0-0 的名称），没有为空
	TimeCol  string
	Segments []Segment
	Hardware bool
}

// Service 执行取数。
type Service struct {
	db   *td.DB
	cfg  config.Extract
	zone *time.Location
	sem  chan struct{} // 同时进行的查询数，限制内存峰值

	devices devicesCache
}

// New 创建取数服务。
func New(db *td.DB, cfg config.Extract) *Service {
	zone, _ := time.LoadLocation(cfg.Timezone) // 配置校验时已确认合法
	return &Service{db: db, cfg: cfg, zone: zone, sem: make(chan struct{}, cfg.MaxConcurrent)}
}

// Zone 返回解析输入时间所用的时区。
func (s *Service) Zone() *time.Location { return s.zone }

// ParseTime 解析页面传来的时间（如 2026-10-28 16:00 或 2026-10-28T16:00:05），按服务器时区。
func (s *Service) ParseTime(v string) (time.Time, error) {
	v = strings.Replace(strings.TrimSpace(v), "T", " ", 1)
	for _, layout := range []string{inputLayout, "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, v, s.zone); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%w：时间格式不正确", ErrInvalid)
}

// FormatTime 把时间写成原网站返回的 q_start / q_end 格式。
func (s *Service) FormatTime(t time.Time) string { return t.In(s.zone).Format(inputLayout) }

// PointToColumn 把点位（如 33.24-0）转成库里的列名 f_33_24_0；已是 f_ 开头的原样返回。
func PointToColumn(point string) string {
	p := strings.TrimSpace(point)
	if p == "" || strings.HasPrefix(p, "f_") {
		return p
	}
	var parts []string
	for _, x := range strings.FieldsFunc(p, func(r rune) bool { return r == '.' || r == '-' || r == '_' }) {
		parts = append(parts, x)
	}
	if len(parts) == 2 {
		parts = append(parts, "0")
	}
	return "f_" + strings.Join(parts, "_")
}

func (s *Service) acquire(ctx context.Context) (func(), error) {
	select {
	case s.sem <- struct{}{}:
		return func() { <-s.sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Query 取 PLC（可选关联 CCD）数据并按工步分段。
func (s *Service) Query(ctx context.Context, r Request) (*Result, error) {
	if r.Device == "" {
		return nil, fmt.Errorf("%w：请填写设备 IP 地址", ErrInvalid)
	}
	if len(r.PLCPoints) == 0 {
		return nil, fmt.Errorf("%w：请至少启用一个 PLC 点位", ErrInvalid)
	}
	release, err := s.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	qEnd := r.End
	if r.EndPlusDay {
		qEnd = qEnd.AddDate(0, 0, 1)
	}
	ts, tc := s.cfg.TsCol, s.cfg.TimeCol

	plcCols := make([]string, len(r.PLCPoints))
	nameMap := map[string]string{}
	stepCol := ""
	for i, p := range r.PLCPoints {
		col := PointToColumn(p.Point)
		plcCols[i] = col
		nameMap[strings.ToLower(col)] = p.Name
		if stepCol == "" && col == PointToColumn("33.0-0") {
			stepCol = p.Name
		}
	}
	plc, err := s.fetch(ctx, s.cfg.Database+"."+s.cfg.PLCStable, r.Device, plcCols, r.Start, qEnd)
	if err != nil {
		return nil, err
	}
	for _, c := range plc.Cols {
		if name, ok := nameMap[c.Name]; ok {
			c.Name = name
		}
	}

	table := plc
	if r.IncludeCCD && len(r.CCDPoints) > 0 {
		ccdCols := make([]string, len(r.CCDPoints))
		ccdMap := map[string]string{}
		for i, p := range r.CCDPoints {
			ccdCols[i] = p.Point
			ccdMap[strings.ToLower(p.Point)] = "ccd_" + p.Name
		}
		ccd, err := s.fetch(ctx, s.cfg.Database+"."+s.cfg.CCDStable, r.Device, ccdCols, r.Start, qEnd)
		if err != nil {
			return nil, err
		}
		for _, c := range ccd.Cols {
			if c.Name == ts || c.Name == tc {
				continue
			}
			if name, ok := ccdMap[c.Name]; ok {
				c.Name = name
			} else {
				c.Name = "ccd_" + c.Name
			}
		}
		if r.JoinMode == "asof" {
			table = joinAsof(plc, ccd, ts, tc, r.AsofTol)
		} else {
			table = joinExact(plc, ccd, ts, tc)
		}
	}
	timeFirst(table, ts, tc)

	res := &Result{Table: table, Device: r.Device, QStart: r.Start, QEnd: qEnd, StepCol: stepCol, TimeCol: tc}
	if stepCol != "" {
		if c := table.Col(stepCol); c != nil {
			allowed := map[float64]bool{r.StartStep: true}
			for k := range r.AllowedSteps {
				allowed[k] = true
			}
			res.Segments = stepSegments(c, r.MinSegRows, r.StartStep, allowed)
		}
	}
	return res, nil
}

// Hardware 查询一台工控机的硬件信息（全部列）。
func (s *Service) Hardware(ctx context.Context, device string, start, end time.Time) (*Result, error) {
	if device == "" {
		return nil, fmt.Errorf("%w：请填写查询所需的 IP 地址", ErrInvalid)
	}
	release, err := s.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	query := fmt.Sprintf("SELECT * FROM %s WHERE %s = %s AND ts >= %d AND ts < %d ORDER BY ts ASC",
		s.cfg.HardwareTable, s.cfg.HardwareIPCol, td.String(device), start.UnixMilli(), end.UnixMilli())
	ctx, cancel := context.WithTimeout(ctx, chunkTimeout)
	defer cancel()
	t, err := s.collectInto(ctx, query, nil)
	if err != nil {
		return nil, err
	}
	return &Result{Table: t, Device: device, QStart: start, QEnd: end, TimeCol: "ts", Hardware: true}, nil
}

// fetch 按 3 天一段查询一台设备的若干列（前两列为 ts、采集时间），按 ts 升序拼接，与原网站 fetch_columns 一致。
func (s *Service) fetch(ctx context.Context, stable, device string, columns []string, start, end time.Time) (*Table, error) {
	ts, tc := s.cfg.TsCol, s.cfg.TimeCol
	sel := []string{ts, tc}
	fallback := []string{ts, tc}
	for _, c := range columns {
		if l := strings.ToLower(c); l != ts && l != tc {
			sel = append(sel, td.Quote(c))
			fallback = append(fallback, l)
		}
	}
	var table *Table
	for cs := start; cs.Before(end); {
		ce := cs.Add(s.cfg.Chunk)
		if ce.After(end) {
			ce = end
		}
		query := fmt.Sprintf("SELECT %s FROM %s WHERE %s = %s AND %s >= %d AND %s < %d ORDER BY %s ASC",
			strings.Join(sel, ", "), stable, s.cfg.DeviceTag, td.String(device), ts, cs.UnixMilli(), ts, ce.UnixMilli(), ts)
		cctx, cancel := context.WithTimeout(ctx, chunkTimeout)
		var err error
		table, err = s.collectInto(cctx, query, table)
		cancel()
		if err != nil {
			return nil, err
		}
		if table.Rows() > s.cfg.MaxRows {
			return nil, fmt.Errorf("%w：结果行数超过上限 %d，请缩小时间范围。", ErrInvalid, s.cfg.MaxRows)
		}
		cs = ce
	}
	if table == nil || len(table.Cols) == 0 {
		table = &Table{}
		for _, name := range fallback {
			table.Cols = append(table.Cols, &Column{Name: name})
		}
	}
	return table, nil
}

// collectInto 执行查询，把结果追加到 into（列名小写，与原网站读 REST 结果时的处理一致）。
func (s *Service) collectInto(ctx context.Context, query string, into *Table) (*Table, error) {
	var cols []*Column
	err := s.db.QueryEach(ctx, query, func(names []string) {
		if into != nil && len(into.Cols) == len(names) {
			cols = into.Cols
			return
		}
		cols = make([]*Column, len(names))
		for i, n := range names {
			cols[i] = &Column{Name: strings.ToLower(strings.Trim(strings.TrimSpace(n), "`\"'"))}
		}
	}, func(values []any) error {
		for i, v := range values {
			cols[i].Append(v)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if into == nil || len(into.Cols) == 0 {
		return &Table{Cols: cols}, nil
	}
	return into, nil
}

// ParseFloat 解析工步号等数值参数，解析不了用 fallback。
func ParseFloat(v any, fallback float64) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil {
			return f
		}
	}
	return fallback
}
