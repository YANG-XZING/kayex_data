package common

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"hmi1.1_consumer/logx"

	"github.com/tidwall/gjson"
)

// SchemaReloadInterval 遇到未知点位时重载超级表结构的最小间隔
const SchemaReloadInterval = 60 * time.Second

var (
	// colCache 超级表已有列缓存：key = "超级表|小写列名"，value = 是否字符串类型
	colCache sync.Map
	// schemaLoaded 已成功读取过表结构的超级表
	schemaLoaded sync.Map
	// skippedWarned 已提示过"点位被丢弃"的列，避免重复刷日志
	skippedWarned sync.Map
	schemaMu      sync.Mutex
	lastReload    = map[string]time.Time{} // 超级表 -> 上次重载表结构时间，受 schemaMu 保护
)

// loadStableSchema 读取超级表现有列及类型到缓存。调用方需持有 schemaMu。
func loadStableSchema(stableName string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rows, err := DB.QueryContext(ctx, fmt.Sprintf("DESCRIBE kayex_data.%s", stableName))
	if err != nil {
		return err
	}
	defer rows.Close()
	// DESCRIBE 的列数随 TDengine 版本不同（4 列或 7 列），按实际列数读，只取前两列：字段名、类型
	colNames, err := rows.Columns()
	if err != nil {
		return err
	}
	cells := make([]sql.NullString, len(colNames))
	dest := make([]any, len(colNames))
	for i := range cells {
		dest[i] = &cells[i]
	}
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return err
		}
		t := strings.ToUpper(cells[1].String)
		colCache.Store(stableName+"|"+strings.ToLower(cells[0].String), strings.Contains(t, "CHAR") || strings.Contains(t, "BINARY"))
	}
	return rows.Err()
}

// unknownKeys 返回消息里超级表没有对应列的键：这些点位不写入、不自动加列，只记一次日志，其余点位照常入库。
// 发现未知点位时最多每 SchemaReloadInterval 重新读一次表结构，手工 ALTER 加的列无需重启就能生效。
// 表结构从未读成功过时不过滤（原样放行），避免把所有点位都误丢。
func unknownKeys(stableName string, keys []string) map[string]bool {
	hasUnknown := false
	for _, k := range keys {
		if _, ok := colCache.Load(stableName + "|" + strings.ToLower(k)); !ok {
			hasUnknown = true
			break
		}
	}
	if !hasUnknown {
		return nil
	}

	schemaMu.Lock()
	if time.Since(lastReload[stableName]) >= SchemaReloadInterval {
		lastReload[stableName] = time.Now()
		if err := loadStableSchema(stableName); err != nil {
			logx.Warnf("SCHEMA", "读取超级表 %s 结构失败: %v", stableName, err)
		} else {
			schemaLoaded.Store(stableName, true)
		}
	}
	schemaMu.Unlock()

	if _, ok := schemaLoaded.Load(stableName); !ok {
		return nil
	}
	skip := map[string]bool{}
	for _, k := range keys {
		ck := stableName + "|" + strings.ToLower(k)
		if _, ok := colCache.Load(ck); ok {
			continue
		}
		skip[k] = true
		if _, warned := skippedWarned.LoadOrStore(ck, true); !warned {
			logx.Warnf("SCHEMA_SKIP", "点位 %s 在超级表 %s 中没有对应列，已丢弃该点位（只提示一次）", k, stableName)
		}
	}
	return skip
}

// BuildDynamicColumns 把消息里除 ID/IP/Time 外的所有键转成 (列名列表, 值列表)。
// 超级表里没有的键直接丢弃（不加列）；字符串列的值一律按字符串写入，其余走 FormatSQLValue。
func BuildDynamicColumns(stableName string, res gjson.Result) (cols, vals []string) {
	cols, vals, _ = buildColumns(stableName, res, false)
	return cols, vals
}

// ExecDynamicInsert 写入一行动态列数据。先按常规方式写；失败后降级重试一次：
// 数值列收到的非数字字符串按 NULL 处理，避免一个坏字段拖垮整行。
// 降级也失败则返回带疑似问题字段的错误（由上层判断是否脏数据）。
func ExecDynamicInsert(stableName, tableName string, nowMs int64, capTime string, res gjson.Result) error {
	build := func(lenient bool) (string, []string) {
		cols, vals, bad := buildColumns(stableName, res, lenient)
		colPart, valPart := "", ""
		if len(cols) > 0 {
			colPart = ", " + strings.Join(cols, ",")
			valPart = ", " + strings.Join(vals, ",")
		}
		return fmt.Sprintf("INSERT INTO kayex_data.%s (ts, capture_time%s) VALUES (%d, '%s'%s)",
			tableName, colPart, nowMs, CleanForTDengine(capTime), valPart), bad
	}

	sql, _ := build(false)
	_, err := DB.Exec(sql)
	if err == nil {
		return nil
	}

	retrySQL, bad := build(true)
	if len(bad) == 0 {
		return err
	}
	if _, err2 := DB.Exec(retrySQL); err2 != nil {
		return fmt.Errorf("%w（疑似问题字段: %s）", err, strings.Join(bad, ","))
	}
	logx.Warnf("SCHEMA", "表 %s 部分字段类型不符，已按 NULL 写入: %s（原错误: %v）",
		tableName, strings.Join(bad, ","), err)
	return nil
}

// buildColumns 是 BuildDynamicColumns 的实现。超级表里没有的列一律丢弃；
// lenient=true 时非字符串列收到不能解析成有限数字的字符串 → NULL，bad 返回这些键名。
func buildColumns(stableName string, res gjson.Result, lenient bool) (cols, vals, bad []string) {
	var keys []string
	var values []gjson.Result
	res.ForEach(func(k, v gjson.Result) bool {
		key := k.String()
		if key == "ID" || key == "IP" || key == "Time" {
			return true
		}
		keys = append(keys, key)
		values = append(values, v)
		return true
	})

	skip := unknownKeys(stableName, keys)

	for i, key := range keys {
		if skip[key] {
			continue
		}
		v := values[i]
		isStr, known := colCache.Load(stableName + "|" + strings.ToLower(key))
		cols = append(cols, "`"+key+"`")
		switch {
		case known && isStr.(bool):
			vals = append(vals, formatStringColumn(v))
		case lenient && v.Type == gjson.String && !isFiniteNumber(v.String()):
			bad = append(bad, key)
			vals = append(vals, "NULL")
		default:
			vals = append(vals, FormatSQLValue(v))
		}
	}
	return cols, vals, bad
}

// isFiniteNumber 判断字符串能否解析成有限浮点数（NaN / Inf / 非数字 都返回 false）。
func isFiniteNumber(s string) bool {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
}

// formatStringColumn 字符串列的取值：null/缺失/空串 -> NULL，其余（含数字、布尔）保留原文并加引号。
func formatStringColumn(v gjson.Result) string {
	if !v.Exists() || v.Type == gjson.Null {
		return "NULL"
	}
	s := v.Raw
	if v.Type == gjson.String {
		s = v.String()
	}
	if s == "" {
		return "NULL"
	}
	return fmt.Sprintf("'%s'", CleanForTDengine(s))
}
