package consumers

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"hmi1.1_consumer/common"
	"hmi1.1_consumer/config"
	"hmi1.1_consumer/logx"

	"github.com/tidwall/gjson"
)

// HandleSOP 处理 SOP 触发存：写本地 CSV 文件 + 存数据库路径索引。
// 返回 nil 表示成功；返回 error 表示写入失败（error 即简洁失败原因，供上层限流打印）。
func HandleSOP(body []byte, stableName string) error {
	res := gjson.ParseBytes(body)
	ip := res.Get("IP").String()
	furnaceID := res.Get("ID").String()
	capTime := res.Get("Time").String()
	fileName := res.Get("FileName").String()
	sopText := res.Get("SOPText").String()

	if ip == "" || sopText == "" {
		return nil // 无效数据直接确认丢弃
	}

	// 1. 文件系统持久化
	// 格式化时间字符串，去除非法文件名字符
	safeTime := strings.ReplaceAll(strings.ReplaceAll(capTime, ":", ""), " ", "_")

	// 路径结构: 根目录/IP/文件名_时间.csv
	dirPath := filepath.Join(config.GetSOPRoot(), ip)
	fullFileName := fmt.Sprintf("%s_%s.csv", strings.TrimSuffix(fileName, ".csv"), safeTime)
	savePath := filepath.Join(dirPath, fullFileName)

	// 创建多级目录
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return fmt.Errorf("创建SOP目录 %s 失败: %w", dirPath, err)
	}

	// 写入原始文本内容
	if err := os.WriteFile(savePath, []byte(sopText), 0644); err != nil {
		return fmt.Errorf("SOP文件写入 %s 失败: %w", savePath, err)
	}

	// 2. 数据库索引记录逻辑
	tableName := "sop_" + strings.ReplaceAll(ip, ".", "_")
	common.EnsureTableExists(tableName, stableName, furnaceID, ip)

	// 检查并更新 Tag (炉台号) ---
	if val, ok := common.TableTagCache.Load(tableName); !ok || val.(string) != furnaceID {
		alterSql := fmt.Sprintf("ALTER TABLE kayex_data.%s SET TAG device_id = '%s'", tableName, furnaceID)
		if _, err := common.DB.Exec(alterSql); err == nil {
			common.TableTagCache.Store(tableName, furnaceID)
			logx.Infof("TAG", "表 %s 的标签已更新为: %s", tableName, furnaceID)
		} else {
			logx.Warnf("TAG", "更新表 %s 的标签失败: %v", tableName, err)
		}
	}

	// 3. 时间戳平滑处理 (ts)
	nowMs := time.Now().UnixMilli()
	if val, ok := common.GlobalLastTsMap.Load(tableName); ok {
		lastTs := val.(int64)
		if nowMs <= lastTs {
			nowMs = lastTs + 1
		}
	}

	// 4. 统一路径斜杠方向并执行插入
	dbPath := strings.ReplaceAll(savePath, "\\", "/")

	sql := fmt.Sprintf("INSERT INTO kayex_data.%s (ts, capture_time, filename, sopaddress) VALUES (%d, '%s', '%s', '%s')",
		tableName, nowMs, capTime, fileName, dbPath)

	if _, err := common.DB.Exec(sql); err != nil {
		return fmt.Errorf("SOP 索引写入表 %s 失败: %w", tableName, err)
	}

	// 5. 写入成功后更新全局时间戳缓存
	common.GlobalLastTsMap.Store(tableName, nowMs)

	return nil
}
