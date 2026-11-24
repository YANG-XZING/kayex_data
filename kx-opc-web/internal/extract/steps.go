package extract

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Segment 是一个工步分段：[From, To) 行。
type Segment struct {
	From, To int
}

// numericAt 把一格转成数，转不了为 ok=false（pandas to_numeric(errors="coerce")）。
func numericAt(c *Column, i int) (float64, bool) {
	if c.Kind == KindUnknown || c.Null[i] {
		return 0, false
	}
	switch c.Kind {
	case KindFloat:
		return c.F[i], true
	case KindInt:
		return float64(c.I[i]), true
	case KindBool:
		if c.B[i] {
			return 1, true
		}
		return 0, true
	case KindString:
		f, err := strconv.ParseFloat(strings.TrimSpace(c.S[i]), 64)
		return f, err == nil
	}
	return 0, false
}

// stepSegments 以 startStep 开始、之后连续落在 allowed 内的行为一段，至少 minRows 行（与原网站 extract_step_segments 一致）。
func stepSegments(c *Column, minRows int, startStep float64, allowed map[float64]bool) []Segment {
	n := c.Len()
	steps := make([]float64, n)
	for i := range steps {
		if v, ok := numericAt(c, i); ok {
			steps[i] = v
		} else {
			steps[i] = -1
		}
	}
	var segs []Segment
	for i := 0; i < n; {
		if steps[i] != startStep {
			i++
			continue
		}
		j := i + 1
		for j < n && allowed[steps[j]] {
			j++
		}
		if j-i >= minRows {
			segs = append(segs, Segment{i, j})
		}
		i = j
	}
	return segs
}

// ---------- 炉次 / 晶棒划分（与原网站 split_dataframe 一致） ----------

var (
	stopSteps   = set("16.0", "16.1", "16.2")
	coldSteps   = set("1.0", "2.0", "3.0", "4.0")
	endingSteps = set("14.0", "14.1", "15.0", "20.0", "20.1", "20.2", "21.2", "16.0", "16.1", "16.2")
)

func set(v ...string) map[string]bool {
	m := map[string]bool{}
	for _, s := range v {
		m[s] = true
	}
	return m
}

// normStep 统一成一位小数的字符串；空值为空串。
func normStep(c *Column, i int) string {
	if c.Kind == KindUnknown || c.Null[i] {
		return ""
	}
	var s string
	if c.Kind == KindBool { // Python 里 str(True) 转不成数，原样保留
		return c.rawString(i)
	}
	if c.Kind == KindString {
		s = strings.TrimSpace(c.S[i])
		if s == "" || strings.EqualFold(s, "nan") {
			return ""
		}
	}
	if f, ok := numericAt(c, i); ok {
		return fmt.Sprintf("%.1f", f)
	}
	return s
}

// findStepColumn 按原网站的顺序找工步列：先找 preferred（“工步”），再找 f_33_0_0，再找名字含 33_0_0 的；
// 都找不到时用 fallback（取数时点位 33.0-0 对应的列，原网站这种情况会原样返回、不划分）。
func findStepColumn(t *Table, preferred, fallback string) *Column {
	for _, name := range []string{preferred, "f_33_0_0", "F_33_0_0"} {
		if c := t.Col(name); c != nil {
			return c
		}
	}
	for _, c := range t.Cols {
		if strings.Contains(c.Name, "33_0_0") {
			return c
		}
	}
	if fallback != "" {
		return t.Col(fallback)
	}
	return nil
}

// splitRuns 按采集时间（找不到时按 ts）排序，用状态机给每行编炉次序号、晶棒序号，返回新表（末尾加两列）。
// 找不到工步列时返回 nil。
func splitRuns(t *Table, stepPreferred, stepFallback, timeCol, tsCol string) *Table {
	step := findStepColumn(t, stepPreferred, stepFallback)
	if step == nil {
		return nil
	}
	order := make([]int, t.Rows())
	for i := range order {
		order[i] = i
	}
	sortCol := t.Col(timeCol)
	if sortCol == nil {
		sortCol = t.Col(tsCol)
	}
	if sortCol != nil {
		slices.SortStableFunc(order, func(a, b int) int { return compareCells(sortCol, a, b) })
	}
	sorted := t.gather(order)
	step = sorted.Col(step.Name)

	n := sorted.Rows()
	fur := &Column{Name: "炉次序号", Kind: KindInt, I: make([]int64, n), Null: make([]bool, n)}
	cry := &Column{Name: "晶棒序号", Kind: KindInt, I: make([]int64, n), Null: make([]bool, n)}
	var (
		prevStep, prevReal string // 上一行工步 / 上一个非手动工步
		dev, cc            int64  = 1, 1
		stopped, inEnd     bool
	)
	for i := 0; i < n; i++ {
		st := normStep(step, i)
		if i == 0 {
			prevStep, prevReal = st, ""
			stopped, inEnd = stopSteps[st], endingSteps[st]
			fur.I[i], cry.I[i] = dev, cc
			continue
		}
		if st == "" {
			fur.I[i], cry.I[i] = dev, cc
			continue
		}
		pre := prevStep
		if prevStep == "0.0" { // 手动工步穿透，取它之前最后一个非手动工步
			pre = prevReal
		}
		changed := pre == "" || st != pre
		// 炉次
		if stopSteps[st] {
			stopped = true
		} else if stopped && changed && coldSteps[st] {
			dev++
			stopped = false
		}
		// 晶棒
		switch {
		case st == "0.0":
		case endingSteps[st]:
			if changed {
				inEnd = true
			}
		case inEnd:
			cc++
			inEnd = false
		}
		prevStep = st
		if st != "0.0" {
			prevReal = st
		} else {
			prevReal = pre
		}
		fur.I[i], cry.I[i] = dev, cc
	}
	sorted.Cols = append(sorted.Cols, fur, cry)
	return sorted
}

// compareCells 比较同一列的两行，空值排在最后（pandas sort_values 的默认行为）。
func compareCells(c *Column, a, b int) int {
	na, nb := c.Kind == KindUnknown || c.Null[a], c.Kind == KindUnknown || c.Null[b]
	switch {
	case na && nb:
		return 0
	case na:
		return 1
	case nb:
		return -1
	}
	switch c.Kind {
	case KindFloat:
		return cmpFloat(c.F[a], c.F[b])
	case KindInt, KindTime:
		return cmpInt(c.I[a], c.I[b])
	case KindString:
		return strings.Compare(c.S[a], c.S[b])
	case KindBool:
		return cmpInt(b2i(c.B[a]), b2i(c.B[b]))
	}
	return 0
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// SplitRuns 导出“分炉次 / 晶棒”用：工步列按原网站的规则找（“工步”、f_33_0_0…），都找不到时用 stepFallback；
// 仍找不到则原样返回（与原网站一致）。
func SplitRuns(t *Table, stepFallback, timeCol, tsCol string) *Table {
	if out := splitRuns(t, "工步", stepFallback, timeCol, tsCol); out != nil {
		return out
	}
	return t
}

// Slice 取出一个分段。
func Slice(t *Table, s Segment) *Table { return t.slice(s.From, s.To) }
