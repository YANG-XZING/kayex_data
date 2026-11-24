package consumers

import (
	"fmt"
	"strings"
	"time"

	"hmi1.1_consumer/common"
	"hmi1.1_consumer/logx"

	"github.com/tidwall/gjson"
)

// HandleGenericTrigger 处理常规触发点位数据（Cam1/Cam2/CrucPara/DualCam/IdNum 共用）。
// 由 StartBaseWorker 统一驱动，无需独立的消费循环。
// 返回 nil 表示成功；返回 error 表示写入失败（error 即简洁失败原因，供上层限流打印）。
func HandleGenericTrigger(body []byte, stableName string) error {
	res := gjson.ParseBytes(body)
	ip := res.Get("IP").String()
	furnaceID := res.Get("ID").String()
	capTimeStr := res.Get("Time").String()
	if ip == "" {
		return nil // 无效数据不重试
	}

	// 逻辑：hmi_bizvars_cam1 -> 截取末尾 cam1 -> 生成 cam1_192_168...
	parts := strings.Split(stableName, "_")
	prefix := parts[len(parts)-1]
	tableName := prefix + "_" + strings.ReplaceAll(ip, ".", "_")

	common.EnsureTableExists(tableName, stableName, furnaceID, ip)

	// --- 检查并更新 Tag (炉台号) ---
	if val, ok := common.TableTagCache.Load(tableName); !ok || val.(string) != furnaceID {
		// 修改 Tag 的 SQL 语句
		alterSql := fmt.Sprintf("ALTER TABLE kayex_data.%s SET TAG device_id = '%s'", tableName, furnaceID)
		if _, err := common.DB.Exec(alterSql); err == nil {
			common.TableTagCache.Store(tableName, furnaceID)
			logx.Infof("TAG", "表 %s 的标签已更新为: %s", tableName, furnaceID)
		} else {
			logx.Warnf("TAG", "更新表 %s 的标签失败: %v", tableName, err)
		}
	}

	// 时间戳平滑处理
	nowMs := time.Now().UnixMilli()
	if val, ok := common.GlobalLastTsMap.Load(tableName); ok {
		lastTs := val.(int64)
		if nowMs <= lastTs {
			nowMs = lastTs + 1
		}
	}

	// 动态解析 JSON 键值对（超级表缺的列自动补成字符串列；写入失败自动降级重试）
	if err := common.ExecDynamicInsert(stableName, tableName, nowMs, capTimeStr, res); err != nil {
		return fmt.Errorf("Trigger 写入表 %s 失败: %w", tableName, err)
	}

	// 写入成功后更新本地缓存
	common.GlobalLastTsMap.Store(tableName, nowMs)

	return nil
}
