package extract

import (
	"bufio"
	"io"
	"strings"
)

const utf8BOM = "\xef\xbb\xbf"

// WriteCSV 写出整张表：UTF-8 带 BOM（Excel 直接打开不乱码），换行为 \n，
// 只在含逗号、引号或换行时加引号（与 Python csv 的 QUOTE_MINIMAL 一致）。
func WriteCSV(w io.Writer, t *Table) error {
	bw := bufio.NewWriterSize(w, 64*1024)
	if _, err := bw.WriteString(utf8BOM); err != nil {
		return err
	}
	fmts := make([]formatter, len(t.Cols))
	for i, c := range t.Cols {
		fmts[i] = newFormatter(c)
		if i > 0 {
			bw.WriteByte(',')
		}
		writeField(bw, c.Name)
	}
	bw.WriteByte('\n')
	for r := 0; r < t.Rows(); r++ {
		for i, f := range fmts {
			if i > 0 {
				bw.WriteByte(',')
			}
			writeField(bw, f.cell(r))
		}
		if err := bw.WriteByte('\n'); err != nil {
			return err
		}
	}
	return bw.Flush()
}

func writeField(w *bufio.Writer, s string) {
	if !strings.ContainsAny(s, ",\"\r\n") {
		w.WriteString(s)
		return
	}
	w.WriteByte('"')
	w.WriteString(strings.ReplaceAll(s, `"`, `""`))
	w.WriteByte('"')
}
