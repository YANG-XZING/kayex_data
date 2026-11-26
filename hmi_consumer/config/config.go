package config

import (
	"os"
	"strconv"
	"time"
)

var (
	// 基础连接配置--晶品
	MQURL   = deploymentString("MQ_URL", "amqp://admin:rabbitmq@10.65.104.172:5672/")
	TaosDSN = deploymentString("TD_DSN", "root:taosdata@ws(10.65.104.171:6041)/kayex_data")
	// // 基础连接配置--无锡
	// MQURL   = "amqp://rabbitmq:root@172.16.2.124:5672/"
	// TaosDSN = "root:taosdata@ws(172.16.2.124:6041)/kayex_data"

	// --- 1. 周期存队列 (3个) ---
	QueueParam     = deploymentString("HMI_QUEUE_PARAM", "HMI_Parameter-30min")
	QueueHotHandle = deploymentString("HMI_QUEUE_HOT_HANDLE", "HMI_BizVarsIdHotHandle-24h")
	QueueCCDData   = deploymentString("HMI_QUEUE_CCD", "HMI_CCD_Data")

	// --- 2. 触发存队列 - 特殊逻辑 (3个) ---
	QueueRecord = deploymentString("HMI_QUEUE_RECORD", "HMI_Record")
	QueueSOP    = deploymentString("HMI_QUEUE_SOP", "HMI_SOPConfig")
	QueueAlarm  = deploymentString("HMI_QUEUE_ALARM", "HMI_Alarm")

	// --- 3. 触发存队列 - 常规逻辑 (5个) ---
	QueueCam1     = deploymentString("HMI_QUEUE_CAM1", "HMI_BizVarsCam1")
	QueueCam2     = deploymentString("HMI_QUEUE_CAM2", "HMI_BizVarsCam2")
	QueueCrucPara = deploymentString("HMI_QUEUE_CRUC_PARA", "HMI_BizVarsCrucPara")
	QueueDualCam  = deploymentString("HMI_QUEUE_DUAL_CAM", "HMI_BizVarsDualCam")
	QueueIdNum    = deploymentString("HMI_QUEUE_ID_NUM", "HMI_BizVarsIdNum")
)

// STABLE_NAME 映射表，方便程序根据队列名找表名
var StableMap = map[string]string{
	QueueParam:     "hmi_parameter",
	QueueHotHandle: "hmi_bizvars_idhothandle",
	QueueCCDData:   "hmi_ccddata",
	QueueRecord:    "hmilog_record",
	QueueSOP:       "hmilog_sop",
	QueueCam1:      "hmi_bizvars_cam1",
	QueueCam2:      "hmi_bizvars_cam2",
	QueueCrucPara:  "hmi_bizvars_crucpara",
	QueueDualCam:   "hmi_bizvars_dualcam",
	QueueIdNum:     "hmi_bizvars_idnum",
	QueueAlarm:     "hmilog_alarm",
}

// TablePrefixMap 映射表写在这里，首字母大写以便外部引用
var TablePrefixMap = map[string]string{
	"hmi_bizvars_cam1":        "cam1_",
	"hmi_bizvars_cam2":        "cam2_",
	"hmi_bizvars_crucpara":    "crucpara_",
	"hmi_bizvars_dualcam":     "dualcam_",
	"hmi_bizvars_idhothandle": "hot_",
	"hmi_bizvars_idnum":       "idnum_",
	"hmi_parameter":           "para_",
	"hmi_record":              "record_",
	"hmi_sop":                 "sop_",
	"hmi_alarm":               "alarm_",
	"hmi_ccddata":             "ccd_",
}

// GetSOPRoot 自动识别系统环境返回路径
func GetSOPRoot() string {
	if path := os.Getenv("SOP_ROOT"); path != "" {
		return path
	}
	if os.PathSeparator == '\\' {
		return "D:\\hmi_sop_files" // Windows 本地测试路径
	}
	return "/home/hmi_sop_files" // Linux 生产路径
}

func deploymentString(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

var (
	MaxOpenConns    = PositiveInt("HMI_MAX_OPEN_CONNS", 100)
	MaxIdleConns    = PositiveInt("HMI_MAX_IDLE_CONNS", 50)
	BatchSize       = PositiveInt("HMI_BATCH_SIZE", 100)
	Prefetch        = PositiveInt("HMI_PREFETCH", 100)
	SOPRetention    = PositiveDuration("HMI_SOP_RETENTION", 30*24*time.Hour)
	SOPScanInterval = PositiveDuration("HMI_SOP_SCAN_INTERVAL", 24*time.Hour)
	DBRetry         = PositiveDuration("HMI_DB_RETRY", 5*time.Second)
)

func PositiveInt(key string, fallback int) int {
	if value := os.Getenv(key); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			panic(key + " must be a positive integer")
		}
		return n
	}
	return fallback
}
func PositiveDuration(key string, fallback time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		d, err := time.ParseDuration(value)
		if err != nil || d <= 0 {
			panic(key + " must be a positive duration")
		}
		return d
	}
	return fallback
}
