// Package hmi 查询上位机记录：上位机写入 kayex_data 的操作记录（hmilog_record）和报警记录（hmilog_alarm）。
// 两张都是超级表，每台炉台一张子表，标签 device_id、device_ip。用扩大后的 ts 范围缩小扫描范围，
// 按上位机时间 capture_time 筛选和显示，结果按 ts 升序排列。分页返回给表格，导出逐行流式写 CSV。
package hmi

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"kx-opc-web/internal/config"
	"kx-opc-web/internal/history"
	"kx-opc-web/internal/td"
)

// ErrInvalid 是参数校验错误，接口返回 400。
var ErrInvalid = errors.New("参数不合法")

// Column 是记录表格的一列：Key 为数据库列名，Label 为表头。
type Column struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// kind 是一种记录：超级表和要显示的列（首列为上位机时间，末列为炉台号）。
type kind struct {
	label  string
	stable string
	fields []Column // 记录本身的列，也是关键字搜索的范围
}

// Kinds 是记录种类的名字（接口参数 kind）
const (
	KindRecord = "record" // 操作记录
	KindAlarm  = "alarm"  // 报警记录
)

// Query 是一次查询：种类、炉台 IP（空为全部炉台）、上位机时间范围 [From, To)、关键字（空为不筛选）。
type Query struct {
	Kind    string
	IP      string
	From    time.Time
	To      time.Time
	Keyword string
	Color   string
}

// Page 是表格的一页（按行）和符合条件的总条数（每页都给，翻页时总页数跟着新记录变）。
type Page struct {
	Total   int        `json:"total"`
	Columns []Column   `json:"columns"`
	Rows    [][]string `json:"rows"`
}

// MaxPageRows 是一页最多多少行
const MaxPageRows = 5000

const (
	maxKeywordRunes  = 64
	recordTimeLayout = "2006-01-02 15:04:05"
	recordTSMargin   = 24 * time.Hour
)

// capture_time 存储的是上位机的北京时间字符串。
var recordTimeZone = time.FixedZone("CST", 8*60*60)

// 颜色选项对应的历史写法，英文名称由 SQL 忽略大小写匹配。
var colorAliases = map[string][]string{
	"white":  {"白", "白色", "#ffffff", "0xffffff", "-1"},
	"yellow": {"黄", "黄色", "#ffff00", "0xffff00", "-256"},
	"red":    {"红", "红色", "#ff0000", "0xff0000", "-65536"},
	"orange": {"橙", "橙色", "#ffa500", "0xffa500", "-23296"},
	"green":  {"绿", "绿色", "#008000", "0x008000", "-16744448"},
	"blue":   {"蓝", "蓝色", "#0000ff", "0x0000ff", "-16776961"},
	"purple": {"紫", "紫色", "#800080", "0x800080", "-8388480"},
	"gray":   {"灰", "灰色", "grey", "#808080", "0x808080", "-8355712"},
	"black":  {"黑", "黑色", "#000000", "0x000000", "-16777216"},
}

// Service 执行上位机记录查询。
type Service struct {
	db       *td.DB
	database string
	kinds    map[string]kind
	maxRange time.Duration
}

// New 创建查询服务。
func New(db *td.DB, cfg config.HMI) *Service {
	return &Service{
		db: db, database: cfg.Database, maxRange: cfg.MaxRange,
		kinds: map[string]kind{
			KindRecord: {label: "操作记录", stable: cfg.RecordStable,
				fields: []Column{{"name", "名称"}, {"value", "值"}, {"color", "颜色"}}},
			KindAlarm: {label: "报警记录", stable: cfg.AlarmStable, fields: []Column{{"content", "内容"}}},
		},
	}
}

// MaxRange 返回一次最多查多长时间。
func (s *Service) MaxRange() time.Duration { return s.maxRange }

// Label 返回记录种类的中文名，用于导出文件名。
func (s *Service) Label(k string) string { return s.kinds[k].label }

func (s *Service) validate(q Query) (kind, error) {
	k, ok := s.kinds[q.Kind]
	if !ok {
		return k, fmt.Errorf("%w：kind 必须是 %s 或 %s", ErrInvalid, KindRecord, KindAlarm)
	}
	if !q.From.Before(q.To) {
		return k, fmt.Errorf("%w：开始时间必须早于结束时间", ErrInvalid)
	}
	if q.To.Sub(q.From) > s.maxRange {
		return k, fmt.Errorf("%w：查询范围不能超过 %s", ErrInvalid, history.FmtDuration(s.maxRange))
	}
	if len([]rune(q.Keyword)) > maxKeywordRunes {
		return k, fmt.Errorf("%w：关键字不能超过 %d 个字", ErrInvalid, maxKeywordRunes)
	}
	if q.Color != "" && q.Kind != KindRecord {
		return k, fmt.Errorf("%w：颜色筛选仅用于操作记录", ErrInvalid)
	}
	return k, nil
}

// recordTimeBound 将毫秒边界向上取整到秒，与 capture_time 的精度一致。
func recordTimeBound(t time.Time) string {
	second := t.Truncate(time.Second)
	if t.After(second) {
		second = second.Add(time.Second)
	}
	return second.In(recordTimeZone).Format(recordTimeLayout)
}

// where 用 ts 限定候选行，再用 capture_time 字符串筛选实际区间。
func (s *Service) where(k kind, q Query) string {
	var b strings.Builder
	fmt.Fprintf(&b, " FROM %s.%s WHERE ts >= %d AND ts < %d AND capture_time >= %s AND capture_time < %s",
		td.Quote(s.database), td.Quote(k.stable), q.From.Add(-recordTSMargin).UnixMilli(), q.To.Add(recordTSMargin).UnixMilli(),
		td.String(recordTimeBound(q.From)), td.String(recordTimeBound(q.To)))
	if q.IP != "" {
		fmt.Fprintf(&b, " AND device_ip = %s", td.String(q.IP))
	}
	if kw := strings.TrimSpace(q.Keyword); kw != "" {
		like := td.String("%" + escapeLike(kw) + "%")
		conds := make([]string, len(k.fields))
		for i, f := range k.fields {
			conds[i] = td.Quote(f.Key) + " LIKE " + like
		}
		fmt.Fprintf(&b, " AND (%s)", strings.Join(conds, " OR "))
	}
	if color := strings.TrimSpace(q.Color); color != "" {
		name := strings.ToLower(color)
		if aliases, ok := colorAliases[name]; ok {
			values := []string{td.String(name)}
			for _, alias := range aliases {
				values = append(values, td.String(alias))
			}
			fmt.Fprintf(&b, " AND LOWER(TRIM(`color`)) IN (%s)", strings.Join(values, ", "))
		} else {
			fmt.Fprintf(&b, " AND LOWER(TRIM(`color`)) = %s", td.String(name))
		}
	}
	return b.String()
}

// escapeLike 让关键字里的 % 和 _ 按原样匹配
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (s *Service) selectSQL(k kind, q Query) (string, []Column) {
	cols := append([]Column{{"capture_time", "上位机时间"}}, k.fields...)
	cols = append(cols, Column{"device_id", "炉台号"})
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = td.Quote(c.Key) // value 是 TD 关键字，必须加反引号
	}
	return "SELECT " + strings.Join(quoted, ", ") + s.where(k, q) + " ORDER BY ts ASC", cols
}

// Colors 返回所选炉台和时间范围内实际出现过的操作记录颜色。
func (s *Service) Colors(ctx context.Context, q Query) ([]string, error) {
	q.Kind, q.Keyword, q.Color = KindRecord, "", ""
	k, err := s.validate(q)
	if err != nil {
		return nil, err
	}
	result, err := s.db.Query(ctx, "SELECT DISTINCT `color`"+s.where(k, q)+" AND `color` IS NOT NULL")
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool, len(result.Rows))
	colors := make([]string, 0, len(result.Rows))
	for _, row := range result.Rows {
		name := strings.ToLower(strings.TrimSpace(td.AsString(row[0])))
		if name == "" {
			continue
		}
		for canonical, aliases := range colorAliases {
			if slices.Contains(aliases, name) {
				name = canonical
				break
			}
		}
		if !seen[name] {
			seen[name] = true
			colors = append(colors, name)
		}
	}
	slices.Sort(colors)
	return colors, nil
}

// PageOf 返回第 offset 行起的 limit 行（按 ts 升序）和总条数。
func (s *Service) PageOf(ctx context.Context, q Query, offset, limit int) (*Page, error) {
	k, err := s.validate(q)
	if err != nil {
		return nil, err
	}
	query, cols := s.selectSQL(k, q)
	out := &Page{Columns: cols, Rows: [][]string{}}
	res, err := s.db.Query(ctx, "SELECT COUNT(*)"+s.where(k, q))
	if err != nil {
		return nil, err
	}
	if len(res.Rows) > 0 {
		out.Total, _ = strconv.Atoi(td.AsString(res.Rows[0][0]))
	}
	query += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
	err = s.db.QueryEach(ctx, query, nil, func(row []any) error {
		out.Rows = append(out.Rows, cells(row))
		return nil
	})
	return out, err
}

// Export 逐行写出 CSV（UTF-8 带 BOM），列与页面一致。
func (s *Service) Export(ctx context.Context, q Query, w io.Writer) error {
	k, err := s.validate(q)
	if err != nil {
		return err
	}
	query, cols := s.selectSQL(k, q)
	bw := bufio.NewWriterSize(w, 64*1024)
	if _, err := bw.WriteString("\xef\xbb\xbf"); err != nil {
		return err
	}
	cw := csv.NewWriter(bw)
	header := make([]string, len(cols))
	for i, c := range cols {
		header[i] = c.Label
	}
	if err := cw.Write(header); err != nil {
		return err
	}
	err = s.db.QueryEach(ctx, query, nil, func(row []any) error { return cw.Write(cells(row)) })
	if err != nil {
		return err
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}
	return bw.Flush()
}

// Validate 只校验参数（导出前先校验，出错时还能返回 400）。
func (s *Service) Validate(q Query) error {
	_, err := s.validate(q)
	return err
}

func cells(row []any) []string {
	out := make([]string, len(row))
	for i, v := range row {
		if v != nil {
			out[i] = strings.TrimSpace(td.AsString(v))
		}
	}
	return out
}
