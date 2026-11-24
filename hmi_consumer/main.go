package main

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"hmi1.1_consumer/cleaner"
	"hmi1.1_consumer/common"
	"hmi1.1_consumer/config"
	"hmi1.1_consumer/consumers"
	"hmi1.1_consumer/logx"
)

// ============================================================================
// 程序说明：
// 1. 本程序负责上位机端全量 11 个业务队列的消费与 TDengine 入库。
// 2. 采用工程化分包管理：config(配置), common(工具), consumers(业务逻辑)。
// 3. 所有队列均开启 Single Active Consumer (SAC) 模式，保证多机部署时只有一个实例在写，确保 NOW 顺序。
// 4. 所有逻辑均支持 Manual Ack 模式：写入失败时探活区分脏数据与 DB 宕机——
//    脏数据(生产端JSON不合规)立即丢弃不阻塞队列；DB 宕机则阻塞等待恢复后重试，绝不丢数据。
// 5. 引入全局 Context 机制，支持 MQ 自动重连和系统的优雅退出。
// ============================================================================

func main() {
	// 0. 按环境变量 LOG_LEVEL 设置日志级别(DEBUG/INFO/WARN/ERROR)，默认 INFO
	logx.SetLevelFromEnv()

	// 1. 初始化全局 TDengine 数据库连接池
	// 连接参数由 config.TaosDSN 统一管理
	common.InitDB()
	logx.Infof("SYS", "TDengine 数据库连接池初始化完成")

	// 2. 创建监听系统退出信号的全局上下文
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 监听系统的终止信号（Ctrl+C 或 systemctl stop 触发的 SIGTERM）
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		logx.Infof("SYS", "接收到系统信号 %v，正在安全关闭所有消费者...", sig)
		cancel() // 广播退出信号，所有收到该信号的消费者会立刻停止重连并退出
	}()

	var wg sync.WaitGroup

	// --- 1. 启动周期存消费者 (13位毫秒 ts, 手动确认回滚模式) ---
	// 处理 HMI_Parameter-30min 和 HMI_BizVarsIdHotHandle-24h
	wg.Add(3)
	go consumers.StartBaseWorker(ctx, &wg, config.QueueParam, config.StableMap[config.QueueParam], consumers.HandleParameter)
	go consumers.StartBaseWorker(ctx, &wg, config.QueueHotHandle, config.StableMap[config.QueueHotHandle], consumers.HandleHotHandle)
	go consumers.StartBaseWorker(ctx, &wg, config.QueueCCDData, config.StableMap[config.QueueCCDData], consumers.HandleCCDData)

	// --- 2. 启动特殊触发存消费者 (文件系统操作 + 数组解析) ---
	// 处理 HMI_SOPConfig 和 HMI_Record 和 HMI_Alarm
	wg.Add(3)
	go consumers.StartBaseWorker(ctx, &wg, config.QueueSOP, config.StableMap[config.QueueSOP], consumers.HandleSOP)
	go consumers.StartBaseWorker(ctx, &wg, config.QueueRecord, config.StableMap[config.QueueRecord], consumers.HandleRecordPacket)
	go consumers.StartBaseWorker(ctx, &wg, config.QueueAlarm, config.StableMap[config.QueueAlarm], consumers.HandleAlarmPacket)

	// --- 3. 启动常规触发存消费者 (5个队列合并处理) ---
	// 处理 Cam1, Cam2, CrucPara, DualCam, IdNum
	genericQueues := []string{
		config.QueueCam1,
		config.QueueCam2,
		config.QueueCrucPara,
		config.QueueDualCam,
		config.QueueIdNum,
	}

	for _, qName := range genericQueues {
		wg.Add(1)
		// 常规触发队列复用 StartBaseWorker，业务逻辑由 HandleGenericTrigger 提供
		go consumers.StartBaseWorker(ctx, &wg, qName, config.StableMap[qName], consumers.HandleGenericTrigger)
	}

	// --- 4. 启动 SOP 文件定时清理 (后台协程，仅保留最近一个月) ---
	wg.Add(1)
	go cleaner.Start(ctx, &wg)

	logx.Infof("SYS", "上位机全量 11 个业务队列已启动监听...")

	// 阻塞主进程，等待所有工作协程安全运行完毕后退出
	wg.Wait()
	logx.Infof("SYS", "所有消费者已安全退出，主进程关闭。")
}
