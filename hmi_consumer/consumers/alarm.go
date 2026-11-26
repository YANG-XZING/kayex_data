package consumers

import (
	"fmt"
	"hmi1.1_consumer/config"
	"strings"
	"time"

	"hmi1.1_consumer/common"
	"hmi1.1_consumer/logx"

	"github.com/tidwall/gjson"
)

// HandleAlarmPacket 处理报警数据包：分批次写入。
// 返回 nil 表示成功；返回 error 表示写入失败（error 即简洁失败原因，供上层限流打印）。
func HandleAlarmPacket(body []byte, stableName string) error {
	res := gjson.ParseBytes(body)
	ip := res.Get("IP").String()
	furnaceID := res.Get("ID").String() // 炉台号

	alarmArray := res.Get("Alarms").Array()

	if ip == "" || len(alarmArray) == 0 {
		return nil // 无效数据跳过
	}

	// 1. 准备子表：如果表不存在则创建
	tableName := "alarm_" + strings.ReplaceAll(ip, ".", "_")
	common.EnsureTableExists(tableName, stableName, furnaceID, ip)

	// 2. 检查并更新 Tag (炉台号)
	// 当内存缓存中的 ID 与当前 ID 不符时，仅修改 TDengine 的标签（Tag）
	if cachedID, ok := common.TableTagCache.Load(tableName); !ok || cachedID.(string) != furnaceID {
		safeFurnaceID := common.CleanForTDengine(furnaceID)
		alterSql := fmt.Sprintf("ALTER TABLE kayex_data.%s SET TAG device_id = '%s'", tableName, safeFurnaceID)

		if _, err := common.DB.Exec(alterSql); err == nil {
			common.TableTagCache.Store(tableName, furnaceID)
			logx.Infof("TAG", "表 %s 的标签已更新为: %s", tableName, furnaceID)
		} else {
			logx.Warnf("TAG", "更新表 %s 的标签失败: %v", tableName, err)
		}
	}

	// 3. 计算起始时间戳 (平滑处理)
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
	batchSize := config.BatchSize // 每次最多写入 100 条记录
	totalAlarms := len(alarmArray)

	for batchStart := 0; batchStart < totalAlarms; batchStart += batchSize {
		batchEnd := batchStart + batchSize
		if batchEnd > totalAlarms {
			batchEnd = totalAlarms
		}

		var sb strings.Builder
		sb.Grow((batchEnd - batchStart) * 150)

		sb.WriteString(fmt.Sprintf("INSERT INTO kayex_data.%s (ts, capture_time, `content`) VALUES ", tableName))

		for i := batchStart; i < batchEnd; i++ {
			item := alarmArray[i]
			if i > batchStart {
				sb.WriteString(",")
			}

			// --- 统一调用 FormatSQLValue 处理数据类型与清洗 ---
			// 此时返回的 sqlTime 和 sqlContent 已经是带有单引号的合法 TDengine 字符串，或者 "NULL"
			sqlTime := common.FormatSQLValue(item.Get("time"))
			sqlContent := common.FormatSQLValue(item.Get("content"))

			rowTs := startTs + int64(i)

			// 占位符去除了单引号包裹，改为: (%d, %s, %s)
			sb.WriteString(fmt.Sprintf("(%d, %s, %s)", rowTs, sqlTime, sqlContent))
		}

		// 5. 执行当前批次的写入
		if _, err := common.DB.Exec(sb.String()); err != nil {
			return fmt.Errorf("Alarm 写入表 %s 失败: %w", tableName, err)
		}
	}

	// 6. 更新末次时间戳缓存
	finalTsOfBatch := startTs + int64(len(alarmArray)-1)
	common.GlobalLastTsMap.Store(tableName, finalTsOfBatch)

	return nil
}
