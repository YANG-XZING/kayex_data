package consumers

import (
	"fmt"
	"strings"
	"time"

	"hmi1.1_consumer/common"
	"hmi1.1_consumer/logx"

	"github.com/tidwall/gjson"
)

// HandleRecordPacket 处理历史记录包：分批次写入，防止单个 SQL 语句超长。
// 返回 nil 表示成功；返回 error 表示写入失败（error 即简洁失败原因，供上层限流打印）。
func HandleRecordPacket(body []byte, stableName string) error {
	res := gjson.ParseBytes(body)
	ip := res.Get("IP").String()
	furnaceID := res.Get("ID").String()

	// 1. 提取 Records 数组
	recordArray := res.Get("Records").Array()

	if ip == "" || len(recordArray) == 0 {
		return nil // 无效或空数据直接确认丢弃
	}

	// 2. 准备子表
	tableName := "record_" + strings.ReplaceAll(ip, ".", "_")
	common.EnsureTableExists(tableName, stableName, furnaceID, ip)

	// --- 检查并更新 Tag (炉台号) ---
	// 只有当炉台号发生变化时，才执行 ALTER TABLE
	if val, ok := common.TableTagCache.Load(tableName); !ok || val.(string) != furnaceID {
		safeFurnaceID := common.CleanForTDengine(furnaceID)
		alterSql := fmt.Sprintf("ALTER TABLE kayex_data.%s SET TAG device_id = '%s'", tableName, safeFurnaceID)

		if _, err := common.DB.Exec(alterSql); err == nil {
			common.TableTagCache.Store(tableName, furnaceID)
			logx.Infof("TAG", "表 %s 的标签已更新为: %s", tableName, furnaceID)
		} else {
			logx.Warnf("TAG", "更新表 %s 的标签失败: %v", tableName, err)
		}
	}

	// 3. 计算这一包数据的起始时间戳
	nowMs := time.Now().UnixMilli()
	var lastTs int64
	if val, ok := common.GlobalLastTsMap.Load(tableName); ok {
		lastTs = val.(int64)
	}

	startTs := nowMs
	if startTs <= lastTs {
		startTs = lastTs + 1
	}

	// 4. 分批次构造并执行插入语句（防止单个 SQL 语句超长报错）
	const batchSize = 100 // 每次最多写入 100 条记录
	totalRecords := len(recordArray)

	for batchStart := 0; batchStart < totalRecords; batchStart += batchSize {
		batchEnd := batchStart + batchSize
		if batchEnd > totalRecords {
			batchEnd = totalRecords
		}

		var sb strings.Builder
		sb.Grow((batchEnd - batchStart) * 250)

		sb.WriteString(fmt.Sprintf("INSERT INTO kayex_data.%s (ts, capture_time, `name`, `value`, `color`) VALUES ", tableName))

		for i := batchStart; i < batchEnd; i++ {
			r := recordArray[i]
			if i > batchStart {
				sb.WriteString(",")
			}

			// --- 统一调用 FormatSQLValue 处理数据类型与清洗 ---
			// 自动脱敏并去除空字符 (\x00) 与转义，不再手动处理 ReplaceAll
			sqlTime := common.FormatSQLValue(r.Get("time"))
			sqlName := common.FormatSQLValue(r.Get("name"))
			sqlValue := common.FormatSQLValue(r.Get("value"))
			sqlColor := common.FormatSQLValue(r.Get("color"))

			// 当前行时间戳 = 起始毫秒 + 循环索引
			rowTs := startTs + int64(i)

			// 占位符修改为无引号格式: (%d, %s, %s, %s, %s)
			sb.WriteString(fmt.Sprintf("(%d, %s, %s, %s, %s)",
				rowTs, sqlTime, sqlName, sqlValue, sqlColor))
		}

		// 5. 执行当前批次的写入
		if _, err := common.DB.Exec(sb.String()); err != nil {
			return fmt.Errorf("Record 写入表 %s 失败: %w", tableName, err)
		}
	}

	// 6. 写入成功后，更新该表的最后时间戳缓存
	finalTsOfBatch := startTs + int64(len(recordArray)-1)
	common.GlobalLastTsMap.Store(tableName, finalTsOfBatch)

	return nil
}
