package extract

import (
	"math"
	"slices"
	"strings"
	"time"
)

// 关联 PLC 与 CCD 数据，规则与原网站（pandas）一致：
//
//	exact：两边的采集时间都四舍五入到秒（恰好 .5 秒时取偶数秒，与 pandas 的 round 相同），
//	       CCD 同一秒有多行时取最后一行，按 PLC 的行左关联；
//	asof： 各自按采集时间排序后，每一行 PLC 取时间最近的 CCD 行，相差超过容差（秒，含等于）不关联；
//	       两边一样近时取较早的那一行；采集时间解析不了的 PLC 行不输出。

// timeKey 把采集时间解析成纳秒；解析不了返回 false。
func timeKey(c *Column, i int) (int64, bool) {
	if c == nil || c.Kind == KindUnknown || c.Null[i] {
		return 0, false
	}
	switch c.Kind {
	case KindTime:
		return c.I[i] * int64(time.Millisecond), true
	case KindString:
		t, ok := parseTime(c.S[i])
		return t.UnixNano(), ok
	}
	return 0, false
}

var timeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05.999999999",
	"2006/01/02 15:04:05.999999999",
	"2006-01-02 15:04",
	"2006-01-02",
}

// parseTime 解析常见的时间写法；不带时区的按 UTC 处理（两边用同一规则，只比较先后和差值）。
func parseTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// roundSecond 按“四舍六入五取偶”取整到秒。
func roundSecond(ns int64) int64 {
	sec := int64(math.Floor(float64(ns) / 1e9))
	rem := ns - sec*1e9
	switch {
	case rem > 5e8:
		sec++
	case rem == 5e8 && sec%2 != 0:
		sec++
	}
	return sec
}

// joinExact 按秒对齐关联。plc、ccd 的前两列为 ts、采集时间；结果为 plc 的全部列 + ccd 除这两列外的列。
func joinExact(plc, ccd *Table, tsCol, timeCol string) *Table {
	type key struct {
		sec   int64
		valid bool
	}
	keyOf := func(c *Column, i int) key {
		ns, ok := timeKey(c, i)
		if !ok {
			return key{} // 解析不了的时间彼此相同（pandas 关联时 NaT 也能匹配 NaT）
		}
		return key{roundSecond(ns), true}
	}
	lastRow := map[key]int{}
	ct := ccd.Col(timeCol)
	for i := 0; i < ccd.Rows(); i++ {
		lastRow[keyOf(ct, i)] = i // 同一秒保留最后一行
	}
	pt := plc.Col(timeCol)
	idx := make([]int, plc.Rows())
	for i := range idx {
		if j, ok := lastRow[keyOf(pt, i)]; ok {
			idx[i] = j
		} else {
			idx[i] = -1
		}
	}
	out := &Table{Cols: slices.Clone(plc.Cols)}
	for _, c := range ccd.Cols {
		if c.Name != tsCol && c.Name != timeCol {
			out.Cols = append(out.Cols, c.gather(c.Name, idx))
		}
	}
	return out
}

// joinAsof 按最近时刻关联，tol 为容差（秒）。结果按 PLC 采集时间排序，CCD 的采集时间列名为 ccd_<采集时间列>。
func joinAsof(plc, ccd *Table, tsCol, timeCol string, tol int) *Table {
	type row struct {
		i  int
		ns int64
	}
	sorted := func(t *Table) []row {
		c := t.Col(timeCol)
		var rows []row
		for i := 0; i < t.Rows(); i++ {
			if ns, ok := timeKey(c, i); ok {
				rows = append(rows, row{i, ns})
			}
		}
		slices.SortStableFunc(rows, func(a, b row) int {
			switch {
			case a.ns < b.ns:
				return -1
			case a.ns > b.ns:
				return 1
			}
			return 0
		})
		return rows
	}
	left, right := sorted(plc), sorted(ccd)
	tolNs := int64(tol) * int64(time.Second)

	lIdx := make([]int, len(left))
	rIdx := make([]int, len(left))
	for k, l := range left {
		lIdx[k] = l.i
		// backward：最后一个 <= l 的；forward：第一个 >= l 的
		hi, _ := slices.BinarySearchFunc(right, l.ns+1, func(r row, t int64) int { return cmpInt(r.ns, t) })
		lo, _ := slices.BinarySearchFunc(right, l.ns, func(r row, t int64) int { return cmpInt(r.ns, t) })
		b, f := -1, -1
		if hi > 0 && l.ns-right[hi-1].ns <= tolNs {
			b = hi - 1
		}
		if lo < len(right) && right[lo].ns-l.ns <= tolNs {
			f = lo
		}
		switch {
		case b >= 0 && f >= 0 && right[f].ns-l.ns < l.ns-right[b].ns:
			rIdx[k] = right[f].i
		case b >= 0:
			rIdx[k] = right[b].i
		case f >= 0:
			rIdx[k] = right[f].i
		default:
			rIdx[k] = -1
		}
	}

	out := plc.gather(lIdx)
	for _, c := range ccd.Cols {
		switch c.Name {
		case tsCol:
		case timeCol:
			out.Cols = append(out.Cols, c.gather("ccd_"+timeCol, rIdx))
		default:
			out.Cols = append(out.Cols, c.gather(c.Name, rIdx))
		}
	}
	return out
}

func cmpInt(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// timeFirst 把采集时间、ts 移到最前（与原网站的列顺序一致）。
func timeFirst(t *Table, tsCol, timeCol string) {
	for _, name := range []string{timeCol, tsCol} {
		if i := slices.IndexFunc(t.Cols, func(c *Column) bool { return c.Name == name }); i > 0 {
			c := t.Cols[i]
			t.Cols = slices.Insert(slices.Delete(t.Cols, i, i+1), 0, c)
		}
	}
}
