// Package history 查询一台炉台一段时间内若干点位的历史数据：按列返回给前端画曲线、分页返回给表格（时间长也不整体读进内存），
// 或逐行流式导出 CSV。
package history

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"kx-opc-web/internal/td"
)

// utf8BOM 写在 CSV 开头，Excel 据此按 UTF-8 打开，中文不乱码
const utf8BOM = "\xef\xbb\xbf"

// ErrInvalid 是参数校验错误，接口返回 400。
var ErrInvalid = errors.New("参数不合法")

// Limits 是查询范围限制。
type Limits struct {
	MaxRange time.Duration
	MaxTags  int
}

// Request 是一次查询：子表、点位、时间范围（TD 时钟，[From, To)）。
type Request struct {
	Table string
	Cols  []string
	From  time.Time
	To    time.Time
}

// Series 是按列组织的查询结果，体积比按行小，前端可直接交给 uPlot。
type Series struct {
	Ts     []int64          `json:"ts"` // 毫秒
	Values map[string][]any `json:"values"`
	Rows   int              `json:"rows"`
}

// Page 是表格的一页（按列组织，值原样，文本点位也能看）。Total 只在第一页（offset = 0）时给出。
type Page struct {
	Total  *int             `json:"total,omitempty"`
	Ts     []int64          `json:"ts"`
	Values map[string][]any `json:"values"`
}

// MaxPageRows 是一页最多多少行
const MaxPageRows = 5000

// Service 执行历史查询。
type Service struct {
	db     *td.DB
	limits Limits
}

// New 创建查询服务。
func New(db *td.DB, limits Limits) *Service { return &Service{db: db, limits: limits} }

// Validate 按默认限制校验请求，见 ValidateWithin。
func (s *Service) Validate(ctx context.Context, r *Request, numericOnly bool) error {
	return s.ValidateWithin(ctx, r, numericOnly, s.limits)
}

// ValidateWithin 校验请求：时间范围、点位数（按 limits，不同账户限制不同）、点位是否存在；
// numericOnly 时只允许数值和开关量点位（画曲线用）。
func (s *Service) ValidateWithin(ctx context.Context, r *Request, numericOnly bool, limits Limits) error {
	if !r.From.Before(r.To) {
		return fmt.Errorf("%w：开始时间必须早于结束时间", ErrInvalid)
	}
	if r.To.Sub(r.From) > limits.MaxRange {
		return fmt.Errorf("%w：查询范围不能超过 %s", ErrInvalid, FmtDuration(limits.MaxRange))
	}
	if len(r.Cols) == 0 || len(r.Cols) > limits.MaxTags {
		return fmt.Errorf("%w：请选择 1~%d 个点位", ErrInvalid, limits.MaxTags)
	}
	columns, err := s.db.Columns(ctx)
	if err != nil {
		return err
	}
	known := make(map[string]td.Column, len(columns))
	for _, c := range columns {
		known[c.Name] = c
	}
	seen := map[string]bool{}
	for _, name := range r.Cols {
		c, ok := known[name]
		if !ok {
			return fmt.Errorf("%w：点位 %s 不存在", ErrInvalid, name)
		}
		if numericOnly && !c.Numeric() && c.Type != "BOOL" {
			return fmt.Errorf("%w：点位 %s 不是数值，不能画曲线", ErrInvalid, name)
		}
		if seen[name] {
			return fmt.Errorf("%w：点位 %s 重复", ErrInvalid, name)
		}
		seen[name] = true
	}
	return nil
}

func (s *Service) sql(r Request) (string, error) {
	table, err := s.db.Table(r.Table)
	if err != nil {
		return "", fmt.Errorf("%w：%v", ErrInvalid, err)
	}
	cols := make([]string, len(r.Cols))
	for i, c := range r.Cols {
		cols[i] = td.Quote(c)
	}
	return fmt.Sprintf("SELECT ts, %s FROM %s WHERE ts >= %d AND ts < %d ORDER BY ts",
		strings.Join(cols, ", "), table, r.From.UnixMilli(), r.To.UnixMilli()), nil
}

// Query 返回按列组织的数据，调用前需先 Validate。
func (s *Service) Query(ctx context.Context, r Request) (*Series, error) {
	query, err := s.sql(r)
	if err != nil {
		return nil, err
	}
	out := &Series{Ts: []int64{}, Values: make(map[string][]any, len(r.Cols))}
	for _, c := range r.Cols {
		out.Values[c] = []any{}
	}
	err = s.db.QueryEach(ctx, query, nil, func(row []any) error {
		ts, _ := row[0].(time.Time)
		out.Ts = append(out.Ts, ts.UnixMilli())
		for i, c := range r.Cols {
			out.Values[c] = append(out.Values[c], chartValue(row[i+1]))
		}
		return nil
	})
	out.Rows = len(out.Ts)
	return out, err
}

// PageOf 返回第 offset 行起的 limit 行（按时间先后），offset = 0 时同时给出总行数。调用前需先 Validate。
func (s *Service) PageOf(ctx context.Context, r Request, offset, limit int) (*Page, error) {
	query, err := s.sql(r)
	if err != nil {
		return nil, err
	}
	out := &Page{Ts: []int64{}, Values: make(map[string][]any, len(r.Cols))}
	if offset == 0 {
		table, _ := s.db.Table(r.Table)
		res, err := s.db.Query(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE ts >= %d AND ts < %d", table, r.From.UnixMilli(), r.To.UnixMilli()))
		if err != nil {
			return nil, err
		}
		total := 0
		if len(res.Rows) > 0 {
			total, _ = strconv.Atoi(td.AsString(res.Rows[0][0]))
		}
		out.Total = &total
	}
	for _, c := range r.Cols {
		out.Values[c] = []any{}
	}
	query += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
	err = s.db.QueryEach(ctx, query, nil, func(row []any) error {
		ts, _ := row[0].(time.Time)
		out.Ts = append(out.Ts, ts.UnixMilli())
		for i, c := range r.Cols {
			out.Values[c] = append(out.Values[c], td.JSONValue(row[i+1]))
		}
		return nil
	})
	return out, err
}

// Export 逐行写出 CSV（UTF-8 带 BOM，Excel 直接打开不乱码）。headers 为各点位的表头。
func (s *Service) Export(ctx context.Context, r Request, headers []string, w io.Writer) error {
	query, err := s.sql(r)
	if err != nil {
		return err
	}
	bw := bufio.NewWriterSize(w, 64*1024)
	if _, err := bw.WriteString(utf8BOM); err != nil {
		return err
	}
	cw := csv.NewWriter(bw)
	if err := cw.Write(append([]string{"时间"}, headers...)); err != nil {
		return err
	}
	record := make([]string, len(r.Cols)+1)
	err = s.db.QueryEach(ctx, query, nil, func(row []any) error {
		ts, _ := row[0].(time.Time)
		record[0] = ts.Local().Format("2006-01-02 15:04:05.000")
		for i := range r.Cols {
			record[i+1] = CSVValue(row[i+1])
		}
		return cw.Write(record)
	})
	if err != nil {
		return err
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}
	return bw.Flush()
}

// chartValue 把值转成曲线用的数：开关量为 0/1，NaN/Inf 和非数值为 null。
func chartValue(v any) any {
	switch x := v.(type) {
	case bool:
		if x {
			return 1
		}
		return 0
	case float32:
		f := float64(x)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil
		}
		// 按 float32 的精度输出，避免 1423.6 变成 1423.5999755859375
		v, _ := strconv.ParseFloat(strconv.FormatFloat(f, 'g', -1, 32), 64)
		return v
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil
		}
		return x
	case int8, int16, int32, int64, uint8, uint16, uint32, uint64:
		return x
	}
	return nil
}

// CSVValue 把查询出的值写成 CSV 的一格：空为空串，开关量为 0 / 1，float32 按本身的精度，时间为本地时间。
func CSVValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case bool:
		if x {
			return "1"
		}
		return "0"
	case float32:
		return strconv.FormatFloat(float64(x), 'g', -1, 32)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case time.Time:
		return x.Local().Format("2006-01-02 15:04:05.000")
	case []byte:
		return string(x)
	}
	return fmt.Sprint(v)
}

// FmtDuration 把时长写成“2 小时”“31 天”这样的文字，用在错误提示里。
func FmtDuration(d time.Duration) string {
	if d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%d 天", d/(24*time.Hour))
	}
	if d%time.Hour == 0 {
		return fmt.Sprintf("%d 小时", d/time.Hour)
	}
	return fmt.Sprintf("%d 分钟", d/time.Minute)
}

// ChartValue 把值转成曲线用的数（供其他模块复用）：开关量为 0/1，NaN/Inf 和非数值为 null。
func ChartValue(v any) any { return chartValue(v) }
