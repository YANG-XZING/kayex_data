// Package extract 是原 TDengine 取数网站（Flask 版 TD_frontend）的 Go 实现：
// PLC / CCD 数据按时间关联、按工步分段、按状态机划分炉次 / 晶棒，以及硬件信息查询，结果可导出 CSV。
//
// 导出的 CSV 与原网站一致：原网站经 REST 接口取数后用 pandas 写 CSV，这里按同样的规则格式化——
// 时间为 UTC 的 2006-01-02T15:04:05.000Z，开关量为 True / False，整列都是整数且没有空值时写成整数，
// 否则按 Python 的 repr 写浮点（13.0、1e-05），空值为空串。
package extract

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// Kind 是列的值类型。
type Kind uint8

const (
	KindUnknown Kind = iota // 还没遇到非空值
	KindFloat               // FLOAT / DOUBLE
	KindInt                 // 各种整数
	KindBool
	KindString
	KindTime // TIMESTAMP，存毫秒
)

// Column 是一列数据，按类型分开存，避免每个值装箱。Null[i] 为 true 表示第 i 行为空。
type Column struct {
	Name string
	Kind Kind
	F    []float64
	I    []int64 // KindInt，或 KindTime 的毫秒
	B    []bool
	S    []string
	Null []bool
	// NumFmt 数值的写法：0 按本列数据决定，1 写整数，2 写浮点。分段是从整张表切出来的，
	// pandas 切片保留整列的类型，所以分段沿用整张表的写法
	NumFmt uint8
}

const (
	numFmtAuto uint8 = iota
	numFmtInt
	numFmtFloat
)

// Table 是一次查询的结果，各列等长。
type Table struct {
	Cols []*Column
}

// Rows 返回行数。
func (t *Table) Rows() int {
	if len(t.Cols) == 0 {
		return 0
	}
	return len(t.Cols[0].Null)
}

// Col 按列名查找。
func (t *Table) Col(name string) *Column {
	for _, c := range t.Cols {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// Len 返回行数。
func (c *Column) Len() int { return len(c.Null) }

// setKind 确定列类型，并为之前的空行补上零值。
func (c *Column) setKind(k Kind) {
	c.Kind = k
	n := len(c.Null)
	switch k {
	case KindFloat:
		c.F = make([]float64, n)
	case KindInt, KindTime:
		c.I = make([]int64, n)
	case KindBool:
		c.B = make([]bool, n)
	case KindString:
		c.S = make([]string, n)
	}
}

func (c *Column) appendNull() {
	c.Null = append(c.Null, true)
	switch c.Kind {
	case KindFloat:
		c.F = append(c.F, 0)
	case KindInt, KindTime:
		c.I = append(c.I, 0)
	case KindBool:
		c.B = append(c.B, false)
	case KindString:
		c.S = append(c.S, "")
	}
}

// Append 追加一个驱动返回的值。float32 先转成最短的十进制再转 float64，与原网站经 REST（JSON）取到的值一致。
func (c *Column) Append(v any) {
	k, f, i, b, s := classify(v)
	if k == KindUnknown {
		c.appendNull()
		return
	}
	if c.Kind == KindUnknown {
		c.setKind(k)
	}
	if k != c.Kind { // 同一列类型不会变；万一变了按字符串保存
		c.toString()
		k, s = KindString, valueString(k, f, i, b, s)
	}
	c.Null = append(c.Null, false)
	switch k {
	case KindFloat:
		c.F = append(c.F, f)
	case KindInt, KindTime:
		c.I = append(c.I, i)
	case KindBool:
		c.B = append(c.B, b)
	case KindString:
		c.S = append(c.S, s)
	}
}

func (c *Column) toString() {
	if c.Kind == KindString {
		return
	}
	s := make([]string, c.Len())
	for i := range s {
		if !c.Null[i] {
			s[i] = c.rawString(i)
		}
	}
	c.Kind, c.S, c.F, c.I, c.B = KindString, s, nil, nil, nil
}

func classify(v any) (Kind, float64, int64, bool, string) {
	switch x := v.(type) {
	case nil:
		return KindUnknown, 0, 0, false, ""
	case float32:
		if math.IsNaN(float64(x)) {
			return KindUnknown, 0, 0, false, ""
		}
		f, _ := strconv.ParseFloat(strconv.FormatFloat(float64(x), 'g', -1, 32), 64)
		return KindFloat, f, 0, false, ""
	case float64:
		if math.IsNaN(x) {
			return KindUnknown, 0, 0, false, ""
		}
		return KindFloat, x, 0, false, ""
	case int8:
		return KindInt, 0, int64(x), false, ""
	case int16:
		return KindInt, 0, int64(x), false, ""
	case int32:
		return KindInt, 0, int64(x), false, ""
	case int64:
		return KindInt, 0, x, false, ""
	case int:
		return KindInt, 0, int64(x), false, ""
	case uint8:
		return KindInt, 0, int64(x), false, ""
	case uint16:
		return KindInt, 0, int64(x), false, ""
	case uint32:
		return KindInt, 0, int64(x), false, ""
	case uint64:
		return KindInt, 0, int64(x), false, ""
	case bool:
		return KindBool, 0, 0, x, ""
	case time.Time:
		return KindTime, 0, x.UnixMilli(), false, ""
	case []byte:
		return KindString, 0, 0, false, string(x)
	case string:
		return KindString, 0, 0, false, x
	}
	return KindString, 0, 0, false, ""
}

// gather 按行号取出一列（行号为 -1 时为空），用于关联、排序、切片。
func (c *Column) gather(name string, idx []int) *Column {
	out := &Column{Name: name, Kind: c.Kind, Null: make([]bool, len(idx))}
	switch c.Kind {
	case KindFloat:
		out.F = make([]float64, len(idx))
	case KindInt, KindTime:
		out.I = make([]int64, len(idx))
	case KindBool:
		out.B = make([]bool, len(idx))
	case KindString:
		out.S = make([]string, len(idx))
	}
	for j, i := range idx {
		if i < 0 || c.Kind == KindUnknown || c.Null[i] {
			out.Null[j] = true
			continue
		}
		switch c.Kind {
		case KindFloat:
			out.F[j] = c.F[i]
		case KindInt, KindTime:
			out.I[j] = c.I[i]
		case KindBool:
			out.B[j] = c.B[i]
		case KindString:
			out.S[j] = c.S[i]
		}
	}
	return out
}

// gather 取出若干行组成新表。
func (t *Table) gather(idx []int) *Table {
	out := &Table{Cols: make([]*Column, len(t.Cols))}
	for i, c := range t.Cols {
		out.Cols[i] = c.gather(c.Name, idx)
	}
	return out
}

// slice 取出 [from, to) 行，数值写法沿用整张表的。
func (t *Table) slice(from, to int) *Table {
	idx := make([]int, to-from)
	for i := range idx {
		idx[i] = from + i
	}
	out := t.gather(idx)
	for i, c := range t.Cols {
		if newFormatter(c).intish {
			out.Cols[i].NumFmt = numFmtInt
		} else {
			out.Cols[i].NumFmt = numFmtFloat
		}
	}
	return out
}

// ---------- 格式化（与原网站 pandas 写出的 CSV 一致） ----------

// formatter 按整列决定数值的写法：pandas 从 JSON 读进来时，整列都是整数且没有空值才是 int64 列。
type formatter struct {
	c      *Column
	intish bool
}

func newFormatter(c *Column) formatter {
	f := formatter{c: c}
	switch c.NumFmt {
	case numFmtInt:
		f.intish = true
		return f
	case numFmtFloat:
		return f
	}
	switch c.Kind {
	case KindInt:
		f.intish = !anyTrue(c.Null)
	case KindFloat:
		f.intish = !anyTrue(c.Null)
		for i, v := range c.F {
			if !c.Null[i] && (v != math.Trunc(v) || math.Abs(v) >= 1e18) {
				f.intish = false
				break
			}
		}
	}
	return f
}

func (f formatter) cell(i int) string {
	c := f.c
	if c.Kind == KindUnknown || c.Null[i] {
		return ""
	}
	switch c.Kind {
	case KindFloat:
		if f.intish {
			return strconv.FormatInt(int64(c.F[i]), 10)
		}
		return pyRepr(c.F[i])
	case KindInt:
		if f.intish {
			return strconv.FormatInt(c.I[i], 10)
		}
		return pyRepr(float64(c.I[i]))
	}
	return c.rawString(i)
}

// rawString 是值本身的字符串（时间为 REST 接口的 UTC 格式）。
func (c *Column) rawString(i int) string {
	switch c.Kind {
	case KindFloat:
		return pyRepr(c.F[i])
	case KindInt:
		return strconv.FormatInt(c.I[i], 10)
	case KindBool:
		if c.B[i] {
			return "True"
		}
		return "False"
	case KindTime:
		return restTime(c.I[i])
	case KindString:
		return c.S[i]
	}
	return ""
}

// jsonValue 是页面表格里显示的值。
func (c *Column) jsonValue(i int) any {
	if c.Kind == KindUnknown || c.Null[i] {
		return nil
	}
	switch c.Kind {
	case KindFloat:
		if math.IsInf(c.F[i], 0) {
			return pyRepr(c.F[i])
		}
		return c.F[i]
	case KindInt:
		return c.I[i]
	case KindBool:
		return c.B[i]
	case KindTime:
		return restTime(c.I[i])
	}
	return c.S[i]
}

func valueString(k Kind, f float64, i int64, b bool, s string) string {
	c := &Column{Kind: k, F: []float64{f}, I: []int64{i}, B: []bool{b}, S: []string{s}, Null: []bool{false}}
	return c.rawString(0)
}

// restTime 是 taosAdapter REST 接口返回时间的格式：UTC，毫秒三位。
func restTime(ms int64) string {
	return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z")
}

// pyRepr 按 Python repr(float) 的规则写浮点：最短表示；指数在 [-4, 16) 内用定点并保证带小数点，否则用科学计数法。
func pyRepr(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	}
	e := strconv.FormatFloat(f, 'e', -1, 64)
	exp, _ := strconv.Atoi(e[strings.IndexByte(e, 'e')+1:])
	if exp < -4 || exp >= 16 {
		return e
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.ContainsAny(s, ".") {
		s += ".0"
	}
	return s
}

func anyTrue(b []bool) bool {
	for _, v := range b {
		if v {
			return true
		}
	}
	return false
}

// Preview 返回 [from, to) 中前 DisplayRows 行，供页面表格显示。
func Preview(t *Table, from, to int) ([]string, [][]any) {
	cols := make([]string, len(t.Cols))
	for i, c := range t.Cols {
		cols[i] = c.Name
	}
	to = min(to, from+DisplayRows)
	rows := make([][]any, 0, max(0, to-from))
	for r := from; r < to; r++ {
		row := make([]any, len(t.Cols))
		for i, c := range t.Cols {
			row[i] = c.jsonValue(r)
		}
		rows = append(rows, row)
	}
	return cols, rows
}

// CellString 返回某列某行的原值字符串（分段的开始 / 结束时间用），没有这一列或为空时为空串。
func CellString(t *Table, col string, row int) string {
	c := t.Col(col)
	if c == nil || row < 0 || row >= c.Len() || c.Kind == KindUnknown || c.Null[row] {
		return ""
	}
	return c.rawString(row)
}
