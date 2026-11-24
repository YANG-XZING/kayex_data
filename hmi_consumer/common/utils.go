package common

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

// CleanForTDengine 处理 Null 字符、转义反斜杠和单引号，防止畸形值破坏 SQL 结构。
func CleanForTDengine(s string) string {
	// 1. 首先剔除 Null 字符 (\x00)
	s = strings.ReplaceAll(s, "\x00", "")

	// 2. 其次处理反斜杠：将 \ 替换为 \\
	// 必须在处理单引号之前做，防止反斜杠转义掉我们用来包裹字符串的单引号
	s = strings.ReplaceAll(s, "\\", "\\\\")

	// 3. 处理单引号：将 ' 替换为 ''
	s = strings.ReplaceAll(s, "'", "''")

	// 4. 去除可能存在的换行符，防止破坏 SQL 结构
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")

	return strings.TrimSpace(s)
}

// FormatSQLValue 将 GJSON 提取的值转为符合 TDengine 语法的字符串
func FormatSQLValue(v gjson.Result) string {
	// 1. 处理空值和 Null
	if !v.Exists() || v.Type == gjson.Null || (v.Type == gjson.String && v.String() == "") {
		return "NULL"
	}
	switch v.Type {
	case gjson.True:
		return "1"
	case gjson.False:
		return "0"
	case gjson.Number:
		// 生产端可能发出 NaN / Infinity（传感器无效值），TDengine 不认，写 NULL
		s := v.String()
		if f, err := strconv.ParseFloat(s, 64); err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return "NULL"
		}
		return s
	case gjson.String:
		// 生产端可能把 NaN / Infinity 当字符串发（"NaN"），写进数值列会报 illegal float data，写 NULL
		switch strings.ToLower(strings.TrimSpace(v.String())) {
		case "nan", "inf", "+inf", "-inf", "infinity", "+infinity", "-infinity":
			return "NULL"
		}
		// 调用统一清洗函数
		return fmt.Sprintf("'%s'", CleanForTDengine(v.String()))
	default:
		return "NULL"
	}
}
