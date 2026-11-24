package config

import (
	"os"
)

const (
	// 基础连接配置--晶品
	MQURL   = "amqp://admin:rabbitmq@10.65.104.172:5672/"
	TaosDSN = "root:taosdata@ws(10.65.104.171:6041)/kayex_data"
	// // 基础连接配置--无锡
	// MQURL   = "amqp://rabbitmq:root@172.16.2.124:5672/"
	// TaosDSN = "root:taosdata@ws(172.16.2.124:6041)/kayex_data"

	// --- 1. 周期存队列 (3个) ---
	QueueParam     = "HMI_Parameter-30min"
	QueueHotHandle = "HMI_BizVarsIdHotHandle-24h"
	QueueCCDData   = "HMI_CCD_Data"

	// --- 2. 触发存队列 - 特殊逻辑 (3个) ---
	QueueRecord = "HMI_Record"
	QueueSOP    = "HMI_SOPConfig"
	QueueAlarm  = "HMI_Alarm"

	// --- 3. 触发存队列 - 常规逻辑 (5个) ---
	QueueCam1     = "HMI_BizVarsCam1"
	QueueCam2     = "HMI_BizVarsCam2"
	QueueCrucPara = "HMI_BizVarsCrucPara"
	QueueDualCam  = "HMI_BizVarsDualCam"
	QueueIdNum    = "HMI_BizVarsIdNum"
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
	if os.PathSeparator == '\\' {
		return "D:\\hmi_sop_files" // Windows 本地测试路径
	}
	return "/home/hmi_sop_files" // Linux 生产路径
}
