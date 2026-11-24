package consumers

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"hmi1.1_consumer/common"
	"hmi1.1_consumer/logx"

	"github.com/tidwall/gjson"
)

// checkAndUpdateTag 内部辅助逻辑：检查炉台号是否变动，若变动则更新 TDengine 的 Tag
func checkAndUpdateTag(tableName, fID string) {
	if val, ok := common.TableTagCache.Load(tableName); !ok || val.(string) != fID {
		// 仅更新 Tag (device_id)
		alterSql := fmt.Sprintf("ALTER TABLE kayex_data.%s SET TAG device_id = '%s'", tableName, fID)
		if _, err := common.DB.Exec(alterSql); err == nil {
			common.TableTagCache.Store(tableName, fID)
			logx.Infof("TAG", "表 %s 的标签已更新为: %s", tableName, fID)
		} else {
			logx.Warnf("TAG", "更新表 %s 的标签失败: %v", tableName, err)
		}
	}
}

// safeFormatHdlElement 是 HandleHotHandle 中专门针对 hthdl 数据的安全格式化函数，针对不同列进行适当处理
func safeFormatHdlElement(col int, val string) string {
	val = strings.TrimSpace(val)

	// 强制加单引号列：3 (名称), 4 (材质), 5 (编码), 6 (开始时间), 9 (更换原因), 10 (更换日期)
	if col == 3 || col == 4 || col == 5 || col == 6 || col == 9 || col == 10 {
		escaped := strings.ReplaceAll(val, "'", "''") // 转义内容中可能存在的单引号，防止 SQL 注入或提前截断
		return "'" + escaped + "'"
	}

	// 布尔类型字段（2）
	if col == 2 {
		if val == "true" || val == "false" {
			return val
		}
		return "false" // 默认防御值
	}

	// 数值类型字段（1, 7, 8）：不加单引号
	if col == 1 || col == 7 || col == 8 {
		if val == "" {
			return "0"
		}
		return val
	}

	return val
}

// HandleParameter 处理 30min 存。
// 返回 nil 表示成功；返回 error 表示写入失败（error 即简洁失败原因，供上层限流打印）。
func HandleParameter(body []byte, stableName string) error {
	res := gjson.ParseBytes(body)
	ip := res.Get("IP").String()
	fID := res.Get("ID").String()
	if ip == "" {
		return nil
	}

	tableName := "para_" + strings.ReplaceAll(ip, ".", "_")
	common.EnsureTableExists(tableName, stableName, fID, ip)

	// 更新 Tag
	checkAndUpdateTag(tableName, fID)

	nowMs := time.Now().UnixMilli()
	if val, ok := common.GlobalLastTsMap.Load(tableName); ok {
		lastTs := val.(int64)
		if nowMs <= lastTs {
			nowMs = lastTs + 1
		}
	}
	common.GlobalLastTsMap.Store(tableName, nowMs)

	if err := common.ExecDynamicInsert(stableName, tableName, nowMs, res.Get("Time").String(), res); err != nil {
		return fmt.Errorf("Parameter 写入表 %s 失败: %w", tableName, err)
	}
	return nil
}

// HandleHotHandle 处理 24h 存。
// 返回 nil 表示成功；返回 error 表示写入失败（error 即简洁失败原因，供上层限流打印）。
func HandleHotHandle(body []byte, stableName string) error {
	res := gjson.ParseBytes(body)
	ip := res.Get("IP").String()
	fID := res.Get("ID").String()
	capTime := res.Get("Time").String()
	if ip == "" {
		return nil
	}

	tableName := "hot_" + strings.ReplaceAll(ip, ".", "_")
	common.EnsureTableExists(tableName, stableName, fID, ip)

	// 更新 Tag
	checkAndUpdateTag(tableName, fID)

	nowMs := time.Now().UnixMilli()
	if val, ok := common.GlobalLastTsMap.Load(tableName); ok {
		lastTs := val.(int64)
		if nowMs <= lastTs {
			nowMs = lastTs + 1
		}
	}

	// 1. 先收集所有行的数据
	var rows []string
	var currentFinalTs int64

	res.ForEach(func(key, value gjson.Result) bool {
		kStr := key.String()
		if kStr == "ID" || kStr == "IP" || kStr == "Time" {
			return true
		}
		parts := strings.Split(value.String(), ",")
		if len(parts) < 10 {
			return true
		}

		idx, _ := strconv.Atoi(kStr)
		ts := nowMs + int64(idx)
		currentFinalTs = ts

		var rowBuilder strings.Builder
		rowBuilder.WriteString(fmt.Sprintf("(%d, '%s', %d", ts, capTime, idx))
		for i, part := range parts {
			// 使用本地的 safeFormatHdlElement 按列做类型安全格式化
			rowBuilder.WriteString("," + safeFormatHdlElement(i+1, part))
		}
		rowBuilder.WriteString(")")

		rows = append(rows, rowBuilder.String())
		return true
	})

	// 2. 分批次写入数据库（每 100 条执行一次 SQL）
	const batchSize = 100
	totalRows := len(rows)

	for batchStart := 0; batchStart < totalRows; batchStart += batchSize {
		batchEnd := batchStart + batchSize
		if batchEnd > totalRows {
			batchEnd = totalRows
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("INSERT INTO kayex_data.%s (ts, capture_time, `hthdl_row_index`, `hthdl_001`, `hthdl_002`, `hthdl_003`, `hthdl_004`, `hthdl_005`, `hthdl_006`, `hthdl_007`, `hthdl_008`, `hthdl_009`, `hthdl_010`) VALUES ", tableName))
		sb.WriteString(strings.Join(rows[batchStart:batchEnd], ","))

		if _, err := common.DB.Exec(sb.String()); err != nil {
			return fmt.Errorf("HotHandle 写入表 %s 失败: %w", tableName, err)
		}
	}

	common.GlobalLastTsMap.Store(tableName, currentFinalTs)
	return nil
}

// HandleCCDData 处理 1s 存一次的数据。
// 返回 nil 表示成功；返回 error 表示写入失败（error 即简洁失败原因，供上层限流打印）。
func HandleCCDData(body []byte, stableName string) error {
	res := gjson.ParseBytes(body)
	ip := res.Get("IP").String()
	fID := res.Get("ID").String()
	capTime := res.Get("Time").String()

	if ip == "" {
		return nil
	}

	tableName := "ccd_" + strings.ReplaceAll(ip, ".", "_")
	common.EnsureTableExists(tableName, stableName, fID, ip)

	// 更新 Tag
	checkAndUpdateTag(tableName, fID)

	nowMs := time.Now().UnixMilli()
	if val, ok := common.GlobalLastTsMap.Load(tableName); ok {
		lastTs := val.(int64)
		if nowMs <= lastTs {
			nowMs = lastTs + 1
		}
	}
	common.GlobalLastTsMap.Store(tableName, nowMs)

	if err := common.ExecDynamicInsert(stableName, tableName, nowMs, capTime, res); err != nil {
		return fmt.Errorf("CCDData 写入表 %s 失败: %w", tableName, err)
	}
	return nil
}
