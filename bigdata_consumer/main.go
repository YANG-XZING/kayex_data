// // // 核心逻辑说明：
// // // 1. 同一 IP 的数据通过 CRC32 哈希分配给固定 Worker，保证时序严格递增。
// // // 2. 使用 sync.Map 缓存表信息和 Tag 信息，只有在炉台号(ID)真正变化时才执行 ALTER TABLE。
// // // 3. 内存攒批写入，大幅提升 TDengine 吞吐量。
// // // 4. 支持 MQ 自动重连机制，且不影响程序的优雅退出。

// // package main

// // import (
// // 	"context"
// // 	"database/sql"
// // 	"fmt"
// // 	"hash/crc32"
// // 	"log"
// // 	"os"
// // 	"os/signal"
// // 	"strings"
// // 	"sync"
// // 	"syscall"
// // 	"time"

// // 	"github.com/rabbitmq/amqp091-go"
// // 	_ "github.com/taosdata/driver-go/v3/taosWS"
// // 	"github.com/tidwall/gjson"
// // )

// // // ============================================================================
// // // 1. 系统常量配置
// // // ============================================================================
// // const (
// // 	MQURL           = "amqp://admin:rabbitmq@10.65.104.172:5672/"
// // 	NormalQueueName = "dbcenter.plc.direct" // 实时数据队列
// // 	PatchQueueName  = "dbcenter.plc.sqlite" // 补发数据队列
// // 	PrefetchCount   = 1000                  // MQ 预取限制

// // 	TaosDSN = "root:taosdata@ws(10.65.104.171:6041)/kayex_data" // 数据库连接地址

// // 	DBName           = "kayex_data"    // 库名
// // 	SuperTableName   = "dbcenter_data" // 超级表名
// // 	ChildTablePrefix = "dc_"           // 子表前缀

// // 	WorkerCount    = 20                     // 并发处理协程数
// // 	BatchPerWorker = 5                      // 攒批大小：满5个消息包触发一次写入
// // 	MaxWaitTime    = 500 * time.Millisecond // 强制写入阈值：即使没攒够包，500ms后也执行写入
// // 	MaxOpenConns   = 500                    // DB最大连接数
// // 	MaxIdleConns   = 200                    // DB最大空闲连接数
// // )

// // // ============================================================================
// // // 2. 全局状态变量
// // // ============================================================================
// // var (
// // 	db                  *sql.DB
// // 	tableLocks          sync.Map // 用于建表时的并发锁，确保一个 IP 只触发一次建表逻辑
// // 	tableExistenceCache sync.Map // 缓存子表是否存在 (tableName -> true)
// // 	tableTagCache       sync.Map // 缓存子表当前的 Tag (tableName -> deviceNo)
// // 	lastTsCache         sync.Map // 缓存每个子表最后一次写入的时间戳，实现跨包 TS 平滑递增
// // )

// // // WrappedDelivery 包装 MQ 消息，标记数据来源（正常或补发）
// // type WrappedDelivery struct {
// // 	QueueTag int // 1: 正常, 0: 补发
// // 	Delivery amqp091.Delivery
// // }

// // // RowItem 业务行对象
// // type RowItem struct {
// // 	CaptureTime string
// // 	Seq         int64
// // 	Queue       int
// // 	DeviceNo    string
// // 	DeviceIP    string
// // 	RawData     gjson.Result
// // }

// // // ============================================================================
// // // 3. 初始化与主循环
// // // ============================================================================

// // func main() {
// // 	log.Println("[INFO] >>> 大数据网关启动中...")
// // 	initTDengine()
// // 	defer db.Close()

// // 	// 创建可取消的上下文，用于管理所有协程的生命周期
// // 	ctx, cancel := context.WithCancel(context.Background())
// // 	defer cancel()

// // 	// 监听系统退出信号 (SIGINT, SIGTERM)
// // 	sigChan := make(chan os.Signal, 1)
// // 	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
// // 	go func() {
// // 		sig := <-sigChan
// // 		log.Printf("[INFO] 接收到系统信号 %v，正在准备退出...", sig)
// // 		cancel() // 广播退出信号
// // 	}()

// // 	// 初始化 Worker 管道并运行（Worker 的生命周期与进程相同）
// // 	workerChans := make([]chan WrappedDelivery, WorkerCount)
// // 	for i := 0; i < WorkerCount; i++ {
// // 		workerChans[i] = make(chan WrappedDelivery, 500)
// // 		go startBatchWorker(ctx, i, workerChans[i])
// // 	}

// // 	// 启动带自动重连的 MQ 消费者（阻塞运行，直到收到退出信号）
// // 	runRabbitMQConsumerWithRetry(ctx, workerChans)

// // 	log.Println("[INFO] 大数据网关已安全退出。")
// // }

// // // initTDengine 初始化数据库连接池
// // func initTDengine() {
// // 	var err error
// // 	db, err = sql.Open("taosWS", TaosDSN)
// // 	if err != nil {
// // 		log.Fatalf("[FATAL] 驱动加载失败: %v", err)
// // 	}
// // 	db.SetMaxOpenConns(MaxOpenConns)
// // 	db.SetMaxIdleConns(MaxIdleConns)
// // 	if err := db.Ping(); err != nil {
// // 		log.Fatalf("[FATAL] 数据库连接验证失败: %v", err)
// // 	}
// // 	log.Printf("[INFO] TDengine 连接池就绪: %s", DBName)
// // }

// // // runRabbitMQConsumerWithRetry 监听多个队列，并包含自动重连与优雅退出的逻辑
// // func runRabbitMQConsumerWithRetry(ctx context.Context, workerChans []chan WrappedDelivery) {
// // 	for {
// // 		select {
// // 		case <-ctx.Done():
// // 			return
// // 		default:
// // 		}

// // 		log.Println("[INFO] 正在尝试连接 RabbitMQ...")
// // 		conn, err := amqp091.Dial(MQURL)
// // 		if err != nil {
// // 			log.Printf("[ERROR] MQ 连接失败: %v，5秒后重试...", err)
// // 			select {
// // 			case <-ctx.Done():
// // 				return
// // 			case <-time.After(5 * time.Second):
// // 				continue
// // 			}
// // 		}

// // 		log.Println("[INFO] MQ 连接建立成功，正在创建通道...")
// // 		ch, err := conn.Channel()
// // 		if err != nil {
// // 			log.Printf("[ERROR] MQ 通道创建失败: %v，正在关闭连接并重试...", err)
// // 			_ = conn.Close()
// // 			time.Sleep(2 * time.Second)
// // 			continue
// // 		}

// // 		_ = ch.Qos(PrefetchCount, 0, false)

// // 		// 监听连接关闭事件
// // 		closeChan := make(chan *amqp091.Error, 1)
// // 		conn.NotifyClose(closeChan)

// // 		// 用于控制当前连接下属的消费分发协程的上下文
// // 		connCtx, connCancel := context.WithCancel(ctx)

// // 		qConfigs := []struct {
// // 			name string
// // 			tag  int
// // 		}{
// // 			{NormalQueueName, 1},
// // 			{PatchQueueName, 0},
// // 		}
// // 		args := amqp091.Table{"x-single-active-consumer": true}

// // 		hasError := false
// // 		for _, cfg := range qConfigs {
// // 			if _, err := ch.QueueDeclare(cfg.name, true, false, false, false, args); err != nil {
// // 				log.Printf("[ERROR] 队列 %s 声明失败: %v", cfg.name, err)
// // 				hasError = true
// // 				break
// // 			}
// // 			msgs, err := ch.Consume(cfg.name, "", false, false, false, false, nil)
// // 			if err != nil {
// // 				log.Printf("[ERROR] 队列 %s 消费监听失败: %v", cfg.name, err)
// // 				hasError = true
// // 				break
// // 			}
// // 			log.Printf("[INFO] 监听队列成功: %s", cfg.name)

// // 			// 启动分发协程
// // 			go func(data <-chan amqp091.Delivery, tag int) {
// // 				for {
// // 					select {
// // 					case <-connCtx.Done():
// // 						return
// // 					case d, ok := <-data:
// // 						if !ok {
// // 							return
// // 						}
// // 						ip := gjson.GetBytes(d.Body, "ip").String()
// // 						if ip == "" {
// // 							_ = d.Ack(false)
// // 							continue
// // 						}
// // 						idx := int(crc32.ChecksumIEEE([]byte(ip))) % WorkerCount

// // 						// 将消息发送给对应 Worker，同时响应退出信号，防止 channel 满时阻塞无法退出
// // 						select {
// // 						case <-connCtx.Done():
// // 							return
// // 						case workerChans[idx] <- WrappedDelivery{QueueTag: tag, Delivery: d}:
// // 						}
// // 					}
// // 				}
// // 			}(msgs, cfg.tag)
// // 		}

// // 		if hasError {
// // 			connCancel()
// // 			_ = ch.Close()
// // 			_ = conn.Close()
// // 			time.Sleep(2 * time.Second)
// // 			continue
// // 		}

// // 		// 阻塞等待：直到连接断开，或者系统下发退出信号
// // 		select {
// // 		case <-ctx.Done():
// // 			log.Println("[INFO] 收到退出信号，正在关闭 MQ 连接并退出消费者...")
// // 			connCancel()
// // 			_ = ch.Close()
// // 			_ = conn.Close()
// // 			return
// // 		case err := <-closeChan:
// // 			log.Printf("[WARNING] MQ 连接意外断开: %v，准备重新连接...", err)
// // 			connCancel()
// // 			_ = ch.Close()
// // 			_ = conn.Close()
// // 			// 进入下一轮循环尝试重连
// // 		}
// // 	}
// // }

// // // ============================================================================
// // // 4. 业务逻辑处理 (攒批与写入)
// // // ============================================================================

// // // startBatchWorker 工作协程：负责内存攒批，支持 Context 退出控制
// // func startBatchWorker(ctx context.Context, id int, myChan <-chan WrappedDelivery) {
// // 	var batch []WrappedDelivery
// // 	for {
// // 		select {
// // 		case <-ctx.Done():
// // 			log.Printf("[INFO] Worker-%02d 收到退出信号，结束运行", id)
// // 			return
// // 		case msg, ok := <-myChan:
// // 			if !ok {
// // 				return
// // 			}
// // 			batch = append(batch, msg)
// // 			timeout := time.After(MaxWaitTime)

// // 		LOOP:
// // 			for len(batch) < BatchPerWorker {
// // 				select {
// // 				case nextMsg, ok := <-myChan:
// // 					if !ok {
// // 						break LOOP
// // 					}
// // 					batch = append(batch, nextMsg)
// // 				case <-timeout:
// // 					break LOOP // 达到最大等待时间，即使没攒够也执行写入
// // 				case <-ctx.Done():
// // 					// 即使收到退出信号，也将当前已攒到的数据尝试写入
// // 					if len(batch) > 0 {
// // 						processCombinedBatch(id, batch)
// // 					}
// // 					log.Printf("[INFO] Worker-%02d 收到退出信号，处理完残留数据后结束运行", id)
// // 					return
// // 				}
// // 			}

// // 			// 即使重连期间连接失效导致 Ack 失败也没关系，重连后 MQ 会自动重新投递未确认的消息
// // 			if processCombinedBatch(id, batch) {
// // 				for _, m := range batch {
// // 					_ = m.Delivery.Ack(false)
// // 				}
// // 			} else {
// // 				for _, m := range batch {
// // 					_ = m.Delivery.Nack(false, true)
// // 				}
// // 			}
// // 			batch = batch[:0]
// // 		}
// // 	}
// // }

// // // processCombinedBatch 解析数据并拼接批量 INSERT SQL
// // func processCombinedBatch(workerID int, tasks []WrappedDelivery) bool {
// // 	if len(tasks) == 0 {
// // 		return true
// // 	}

// // 	groups := make(map[string][]RowItem)
// // 	groupColPaths := make(map[string][]string)
// // 	totalRows := 0

// // 	// 1. 解析 JSON 并按 IP 分组
// // 	for _, task := range tasks {
// // 		res := gjson.ParseBytes(task.Delivery.Body)
// // 		ip := res.Get("ip").String()
// // 		deviceNo := res.Get("deviceNo").String()
// // 		records := res.Get("datas").Array()

// // 		if ip == "" || deviceNo == "" {
// // 			continue
// // 		}

// // 		for _, rec := range records {
// // 			groups[ip] = append(groups[ip], RowItem{
// // 				CaptureTime: rec.Get("capture_time").String(),
// // 				Seq:         rec.Get("seq").Int(),
// // 				Queue:       task.QueueTag,
// // 				DeviceNo:    deviceNo,
// // 				DeviceIP:    ip,
// // 				RawData:     rec.Get("data"),
// // 			})
// // 			totalRows++
// // 		}
// // 	}
// // 	if totalRows == 0 {
// // 		return true
// // 	}

// // 	// 2. 构造多表 INSERT 语句
// // 	var sb strings.Builder
// // 	sb.WriteString("INSERT INTO ")
// // 	tempLastTsMap := make(map[string]int64)

// // 	for ip, rows := range groups {
// // 		tableName := ChildTablePrefix + strings.ReplaceAll(ip, ".", "_")

// // 		// 检查子表是否存在 & 处理 Tag 变更逻辑
// // 		ensureTableExists(tableName, rows[0].DeviceNo, rows[0].DeviceIP)

// // 		// [核心逻辑] 时间戳平滑计算：防止同一毫秒内多条数据导致 TDengine 主键覆盖
// // 		var lastTs int64
// // 		if val, ok := lastTsCache.Load(tableName); ok {
// // 			lastTs = val.(int64)
// // 		}
// // 		startTs := time.Now().UnixMilli()
// // 		if startTs <= lastTs {
// // 			startTs = lastTs + 1 // 强制将本批次的起点挪到上一批次的终点之后
// // 		}

// // 		var colPaths []string
// // 		var header strings.Builder
// // 		header.WriteString(" (ts, capture_time, crystal_number, seqno, queue")

// // 		// 动态列探测：自动扫描 JSON 键生成 f_组名_点位名
// // 		rows[0].RawData.ForEach(func(key, value gjson.Result) bool {
// // 			gK := key.String()
// // 			if gK == "capture_time" || gK == "404" {
// // 				return true
// // 			}
// // 			value.ForEach(func(pK, pV gjson.Result) bool {
// // 				sK := pK.String()
// // 				header.WriteString(fmt.Sprintf(",`f_%s_%s` ", gK, strings.ReplaceAll(sK, "-", "_")))
// // 				colPaths = append(colPaths, gK+"."+sK)
// // 				return true
// // 			})
// // 			return true
// // 		})
// // 		header.WriteString(") VALUES ")
// // 		groupColPaths[ip] = colPaths

// // 		sb.WriteString(fmt.Sprintf(" %s.%s %s ", DBName, tableName, header.String()))
// // 		for i, row := range rows {
// // 			if i > 0 {
// // 				sb.WriteByte(',')
// // 			}
// // 			rowTs := startTs + int64(i) // 包内每行数据顺延 1ms，确保 ts 绝对唯一
// // 			sb.WriteString(fmt.Sprintf("(%d,'%s','%s',%d,%d",
// // 				rowTs, row.CaptureTime, getHimCrystalNumber(row.RawData.Get("404")), row.Seq, row.Queue))

// // 			for _, path := range colPaths {
// // 				sb.WriteByte(',')
// // 				formatValueOptimized(&sb, row.RawData.Get(path))
// // 			}
// // 			sb.WriteByte(')')
// // 		}
// // 		tempLastTsMap[tableName] = startTs + int64(len(rows)-1)
// // 	}

// // 	// 3. 执行写入
// // 	_, err := db.Exec(sb.String())
// // 	if err != nil {
// // 		log.Printf("[DB_ERROR] Worker-%02d 批量失败，进入单行诊断...", workerID)
// // 		handleBatchRetry(workerID, groups, groupColPaths)
// // 		return true
// // 	}

// // 	// 写入成功后，批量更新该 IP 的最新时间戳缓存
// // 	for tName, newTs := range tempLastTsMap {
// // 		lastTsCache.Store(tName, newTs)
// // 	}
// // 	return true
// // }

// // // ensureTableExists 负责创建子表以及同步 Tag (炉台号)
// // func ensureTableExists(tableName string, deviceNo string, deviceIP string) {
// // 	// 1. 如果子表完全不存在，则创建它
// // 	if _, ok := tableExistenceCache.Load(tableName); !ok {
// // 		lockIntf, _ := tableLocks.LoadOrStore(tableName, &sync.Mutex{})
// // 		lock := lockIntf.(*sync.Mutex)
// // 		lock.Lock()
// // 		if _, ok := tableExistenceCache.Load(tableName); !ok {
// // 			createSql := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s.%s USING %s.%s TAGS ('%s', '%s')",
// // 				DBName, tableName, DBName, SuperTableName, deviceNo, deviceIP)
// // 			if _, err := db.Exec(createSql); err == nil {
// // 				tableExistenceCache.Store(tableName, true)
// // 				tableTagCache.Store(tableName, deviceNo)
// // 			}
// // 		}
// // 		lock.Unlock()
// // 	}

// // 	// 2. 如果炉台号 ID 变了，则更新 TDengine 子表的 Tag
// // 	cachedTag, ok := tableTagCache.Load(tableName)
// // 	if !ok || cachedTag.(string) != deviceNo {
// // 		alterSql := fmt.Sprintf("ALTER TABLE %s.%s SET TAG device_id = '%s'", DBName, tableName, deviceNo)
// // 		if _, err := db.Exec(alterSql); err == nil {
// // 			tableTagCache.Store(tableName, deviceNo)
// // 			log.Printf("[TAG_UPDATE] 表 %s 的炉台号已变更为: %s", tableName, deviceNo)
// // 		}
// // 	}
// // }

// // // handleBatchRetry 诊断模式：将批量失败的包拆为单行插入，并在失败时输出详细的数据库报错与对应 SQL
// // func handleBatchRetry(workerID int, groups map[string][]RowItem, colPathsMap map[string][]string) {
// // 	for ip, rows := range groups {
// // 		tableName := ChildTablePrefix + strings.ReplaceAll(ip, ".", "_")
// // 		paths := colPathsMap[ip]
// // 		for _, row := range rows {
// // 			rowTs := time.Now().UnixMilli()
// // 			if val, ok := lastTsCache.Load(tableName); ok {
// // 				if rowTs <= val.(int64) {
// // 					rowTs = val.(int64) + 1
// // 				}
// // 			}

// // 			var rsb strings.Builder
// // 			rsb.WriteString(fmt.Sprintf("INSERT INTO %s.%s (ts, capture_time, crystal_number, seqno, queue", DBName, tableName))
// // 			for _, path := range paths {
// // 				p := strings.Split(path, ".")
// // 				rsb.WriteString(fmt.Sprintf(",`f_%s_%s` ", p[0], strings.ReplaceAll(p[1], "-", "_")))
// // 			}
// // 			rsb.WriteString(fmt.Sprintf(") VALUES (%d,'%s','%s',%d,%d",
// // 				rowTs, row.CaptureTime, getHimCrystalNumber(row.RawData.Get("404")), row.Seq, row.Queue))

// // 			for _, path := range paths {
// // 				rsb.WriteByte(',')
// // 				formatValueOptimized(&rsb, row.RawData.Get(path))
// // 			}
// // 			rsb.WriteString(")")

// // 			// 🛠️ 核心修改：捕获单行执行的具体 Error 并输出，停止静默失败
// // 			_, err := db.Exec(rsb.String())
// // 			if err != nil {
// // 				log.Printf("[DB_RETRY_ERROR] Worker-%02d 单行重试写入失败: %v", workerID, err)
// // 			} else {
// // 				lastTsCache.Store(tableName, rowTs)
// // 			}
// // 		}
// // 	}
// // }

// // // formatValueOptimized 优化的值格式化
// // func formatValueOptimized(sb *strings.Builder, v gjson.Result) {
// // 	if !v.Exists() {
// // 		sb.WriteString("NULL")
// // 		return
// // 	}
// // 	switch v.Type {
// // 	case gjson.True:
// // 		sb.WriteByte('1')
// // 	case gjson.False:
// // 		sb.WriteByte('0')
// // 	case gjson.Number:
// // 		sb.WriteString(v.Raw)
// // 	case gjson.String:
// // 		sb.WriteByte('\'')
// // 		sb.WriteString(strings.ReplaceAll(v.Str, "'", "''"))
// // 		sb.WriteByte('\'')
// // 	default:
// // 		sb.WriteString("NULL")
// // 	}
// // }

// // // getHimCrystalNumber 1:1 复刻 Java 版本的晶编解析与严格校验逻辑
// // func getHimCrystalNumber(data404 gjson.Result) string {
// // 	if !data404.Exists() {
// // 		return ""
// // 	}

// // 	var chars []rune
// // 	lastNonZeroIndex := 0

// // 	// 1. 遍历 28 到 126 字节（步长为2）
// // 	for i := 28; i < 128; i += 2 {
// // 		val := data404.Get(fmt.Sprintf("%d-0", i))
// // 		if val.Exists() {
// // 			asciiValue := int(val.Int())
// // 			character := rune(asciiValue)
// // 			chars = append(chars, character)

// // 			// 只要字符不是 \0 且不是空格(32)，就更新最后一个有效字符的索引
// // 			if asciiValue != 0 && asciiValue != 32 {
// // 				lastNonZeroIndex = len(chars)
// // 			}
// // 		}
// // 	}

// // 	// 如果没有任何有效字符，返回空
// // 	if lastNonZeroIndex == 0 {
// // 		return ""
// // 	}

// // 	// 2. 截断到最后一个非0、非空格的字符
// // 	truncated := chars[:lastNonZeroIndex]

// // 	// 3. 核心物理校验：截断后的最后一个字符必须是换行符 '\n' (ASCII 10)
// // 	if truncated[len(truncated)-1] != '\n' {
// // 		return ""
// // 	}

// // 	// 4. 去除末尾的连续空格（对标 Java 的 trimIndex 逻辑）
// // 	trimIndex := len(truncated)
// // 	for trimIndex > 0 && truncated[trimIndex-1] == ' ' {
// // 		trimIndex--
// // 	}
// // 	truncated = truncated[:trimIndex]

// // 	// 5. 将结果转为字符串，并移除所有的换行符 '\n'
// // 	resultStr := string(truncated)
// // 	resultStr = strings.ReplaceAll(resultStr, "\n", "")

// // 	return resultStr
// // }

// 核心逻辑说明：
// 1. 同一 IP 的数据通过 CRC32 哈希分配给固定 Worker，保证时序严格递增。
// 2. 使用 sync.Map 缓存表信息和 Tag 信息，只有在炉台号(ID)真正变化时才执行 ALTER TABLE。
// 3. 内存攒批写入，大幅提升 TDengine 吞吐量。
// 4. 支持 MQ 自动重连机制，且不影响程序的优雅退出。
//
// 本版新增：
//   inner_furnace_no  —— 内部炉次号
//   inner_crystal_no  —— 内部晶编号
//   两者均为状态机：依据 f_33_0_0 (工步号 STATE_NO) 结合上一条记录判断是否换号。

package main

import (
	"context"
	"database/sql"
	"fmt"
	"hash/crc32"
	"log"
	"math"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/rabbitmq/amqp091-go"
	_ "github.com/taosdata/driver-go/v3/taosWS"
	"github.com/tidwall/gjson"
)

// ============================================================================
// 1. 系统常量配置
// ============================================================================
var (
	PrefetchCount = deploymentInt("PLC_PREFETCH", 2000) // MQ 预取限制：≈ WorkerCount×BatchPerWorker，保证 Worker 有活干又不至于
	//                                         一次性挂太多未 Ack 消息（挂太多会触发 broker 的 consumer_timeout 掐通道）

	DBName           = "kayex_data"                                    // 库名
	SuperTableName   = deploymentString("PLC_STABLE", "dbcenter_data") // 超级表名
	ChildTablePrefix = deploymentString("PLC_TABLE_PREFIX", "dc_")     // 子表前缀

	WorkerCount    = deploymentInt("PLC_WORKERS", 20)    // 并发处理协程数
	BatchPerWorker = deploymentInt("PLC_BATCH_SIZE", 10) // 攒批大小：满10个消息包才触发一次写入。调大=单条 INSERT 携带更多行、
	//                                         DB 往返次数大幅减少，是提吞吐、排积压的最主要杠杆（TDengine 尤其吃大批量写入）
	MaxWaitTime   = deploymentDuration("PLC_MAX_WAIT", 500*time.Millisecond) // 强制写入阈值：即使没攒够包，500ms后也执行写入（保证低峰期时延）
	WorkerChanBuf = deploymentInt("PLC_WORKER_BUFFER", 200)                  // 每个 Worker 入口 channel 缓冲：给一点缓冲避免分发协程阻塞即可；
	//                                         不宜过大——太大=大量消息在内存里挂着迟迟不 Ack，会撞 broker 的 ack 超时
	MaxOpenConns = deploymentInt("PLC_MAX_OPEN_CONNS", 500) // DB最大连接数
	MaxIdleConns = deploymentInt("PLC_MAX_IDLE_CONNS", 200) // DB最大空闲连接数

	// 韧性相关：给每次 DB 操作加超时，避免 taosadapter/taosd 假死时 db.Exec 无限阻塞导致 Worker 僵死。
	DBOpTimeout = deploymentDuration("PLC_DB_TIMEOUT", 10*time.Second) // 单次 DB 操作(Exec/Ping/Query)超时
	DBDownRetry = deploymentDuration("PLC_DB_RETRY", 5*time.Second)    // DB 不可用时的原地重试间隔

	// 内部炉次号/晶编号相关的工步号来源点位
	StateNoPath = deploymentString("PLC_STATE_POINT", "33.0-0") // 工步号 STATE_NO -> 数据库列 f_33_0_0
)

// ============================================================================
// 2. 全局状态变量
// ============================================================================
var (
	db                  *sql.DB
	tableLocks          sync.Map // 用于建表时的并发锁，确保一个 IP 只触发一次建表逻辑
	tableExistenceCache sync.Map // 缓存子表是否存在 (tableName -> true)
	tableTagCache       sync.Map // 缓存子表当前的 Tag (tableName -> deviceNo)
	lastTsCache         sync.Map // 缓存每个子表最后一次写入的时间戳，实现跨包 TS 平滑递增
	stateCache          sync.Map // 缓存每个子表上一条记录的状态机信息 (tableName -> *StateInfo)
	knownCols           sync.Map // 超级表已有列缓存 (小写列名 -> 是否字符串类型)，库里没有的点位据此丢弃
	skippedWarned       sync.Map // 已提示过"点位被丢弃"的列，避免重复刷日志
	schemaOK            atomic.Bool
	schemaMu            sync.Mutex
	lastSchemaReload    time.Time
)

// SchemaReloadInterval 遇到未知点位时重载超级表结构的最小间隔
const SchemaReloadInterval = 60 * time.Second

// WrappedDelivery 包装 MQ 消息，标记数据来源（正常或补发）
type WrappedDelivery struct {
	QueueTag int // 1: 正常, 0: 补发
	Delivery amqp091.Delivery
}

// RowItem 业务行对象
type RowItem struct {
	CaptureTime    string
	Seq            int64
	Queue          int
	DeviceNo       string
	DeviceIP       string
	RawData        gjson.Result
	InnerFurnaceNo string // 内部炉次号（在解析阶段一次性算好，供批量/单行重试复用）
	InnerCrystalNo string // 内部晶编号
}

// StateInfo 记录某设备"上一条记录"的状态机快照，用于跨消息、跨批次计算内部炉次号/晶编号。
// 对标 Java 端存入 Redis 的那条记录里的四个字段。
type StateInfo struct {
	StateNo            string // 上一条记录的工步号 STATE_NO
	PreStateNo         string // 上一条记录的"最后一个非手动工步号" PRE_STATE_NO（用于穿透手动 0.0）
	DeviceSeq          string // 当前内部炉次号
	InnerCrystalNumber string // 当前内部晶编号
	CrystalEnding      bool   // 当前是否处于"结束段(收尾/放断/停炉)"——晶编号"以结束为界"判定用
	FurnaceStopped     bool   // 当前是否已停炉、等待下一次冷启动——炉次号"停炉后首个冷启动"判定用
}

// ============================================================================
// 3. 初始化与主循环
// ============================================================================

func main() {
	log.Println("[INFO] >>> 大数据网关启动中...")
	initTDengine()
	defer db.Close()

	// 确保超级表已具备两列新字段
	ensureInnerColumns()

	// 载入超级表现有列，供自适应加列判断（失败不致命，ensureColumns 会在需要时重载）
	if err := loadSchema(); err != nil {
		log.Printf("[WARN] 读取超级表结构失败: %v", err)
	}

	// 启动预热：用一条批量查询把所有子表的"最后一行状态"一次性载入内存缓存。
	// 这样重启后按需反查(rehydrateFromDB)几乎不会在热路径触发，既不拖慢积压消费、也不会因慢查询卡死。
	warmupStateCache()

	// 创建可取消的上下文，用于管理所有协程的生命周期
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 监听系统退出信号 (SIGINT, SIGTERM)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		log.Printf("[INFO] 接收到系统信号 %v，正在准备退出...", sig)
		cancel() // 广播退出信号
	}()

	// 初始化 Worker 管道并运行（Worker 的生命周期与进程相同）
	workerChans := make([]chan WrappedDelivery, WorkerCount)
	for i := 0; i < WorkerCount; i++ {
		workerChans[i] = make(chan WrappedDelivery, WorkerChanBuf)
		go startBatchWorker(ctx, i, workerChans[i])
	}

	// 启动带自动重连的 MQ 消费者（阻塞运行，直到收到退出信号）
	runRabbitMQConsumerWithRetry(ctx, workerChans)

	log.Println("[INFO] 大数据网关已安全退出。")
}

// initTDengine 初始化数据库连接池
func initTDengine() {
	var err error
	db, err = sql.Open("taosWS", TaosDSN)
	if err != nil {
		log.Fatalf("[FATAL] 驱动加载失败: %v", err)
	}
	db.SetMaxOpenConns(MaxOpenConns)
	db.SetMaxIdleConns(MaxIdleConns)
	// DB 未就绪时不再 Fatal 退出（否则 DB 宕机期间进程被拉起就会秒死、反复 flap）。
	// 改为阻塞等待，直到连上；systemd 会看到进程一直存活，DB 恢复后自然开始工作。
	for !dbAlive() {
		log.Printf("[WARN] TDengine 暂不可用，%v 后重试...", DBDownRetry)
		time.Sleep(DBDownRetry)
	}
	log.Printf("[INFO] TDengine 连接池就绪: %s", DBName)
}

// execCtx 带超时执行 SQL。这是根治"Worker 僵死"的关键：taosadapter 在线但后端 taosd 假死时，
// 不带超时的 db.Exec 会无限阻塞，20 个 Worker 全卡住、进程活着却不干活、systemd 也不会重启它。
func execCtx(query string) error {
	ctx, cancel := context.WithTimeout(context.Background(), DBOpTimeout)
	defer cancel()
	_, err := db.ExecContext(ctx, query)
	return err
}

// dbAlive 带超时探活，用于区分"脏数据(DB在线，个别行不合规)"与"DB宕机(整体不可用)"。
func dbAlive() bool {
	ctx, cancel := context.WithTimeout(context.Background(), DBOpTimeout)
	defer cancel()
	return db.PingContext(ctx) == nil
}

// ensureInnerColumns 给超级表补上内部炉次号/晶编号两列。
// 子表通过 USING 超级表创建，会自动继承这两列，因此只需在超级表上加一次。
// 列已存在时 TDengine 会报错，这里直接忽略（幂等，重启无害）。
func ensureInnerColumns() {
	ddls := []struct {
		col string
		sql string
	}{
		{"inner_furnace_no", fmt.Sprintf("ALTER STABLE %s.%s ADD COLUMN inner_furnace_no NCHAR(64) COMPRESS 'zstd' LEVEL 'high'", DBName, SuperTableName)},
		{"inner_crystal_no", fmt.Sprintf("ALTER STABLE %s.%s ADD COLUMN inner_crystal_no NCHAR(64) COMPRESS 'zstd' LEVEL 'high'", DBName, SuperTableName)},
	}
	for _, d := range ddls {
		if err := execCtx(d.sql); err != nil {
			log.Printf("[SCHEMA] 添加列 %s 跳过（多为已存在，可忽略）: %v", d.col, err)
		} else {
			log.Printf("[SCHEMA] 已为超级表添加列 %s", d.col)
		}
	}
}

// colName 把点位路径(组.点位)转成超级表列名，与 INSERT 里拼的列名保持一致（小写，TDengine 列名不区分大小写）。
func colName(path string) string {
	p := strings.SplitN(path, ".", 2)
	if len(p) < 2 {
		return strings.ToLower("f_" + path)
	}
	return strings.ToLower("f_" + p[0] + "_" + strings.ReplaceAll(p[1], "-", "_"))
}

// colIsString 该点位对应的列是否为字符串类型（未知列按非字符串处理）。
func colIsString(path string) bool {
	v, ok := knownCols.Load(colName(path))
	return ok && v.(bool)
}

// loadSchema 读取超级表现有列及类型到内存缓存（列名 -> 是否字符串类型）。
func loadSchema() error {
	ctx, cancel := context.WithTimeout(context.Background(), DBOpTimeout)
	defer cancel()
	rows, err := db.QueryContext(ctx, fmt.Sprintf("DESCRIBE %s.%s", DBName, SuperTableName))
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
		knownCols.Store(strings.ToLower(cells[0].String), strings.Contains(t, "CHAR") || strings.Contains(t, "BINARY"))
	}
	if err := rows.Err(); err != nil {
		return err
	}
	schemaOK.Store(true)
	return nil
}

// filterKnownPaths 只保留超级表里已有的点位列；库里没有的点位直接丢弃（不写、不加列），其余点位照常入库。
// 发现未知点位时最多每 SchemaReloadInterval 重新读一次表结构，这样手工 ALTER 加的列无需重启就能生效。
// 表结构从未读成功过时不过滤（原样放行），避免把所有点位都误丢。
func filterKnownPaths(paths []string) []string {
	unknown := false
	for _, p := range paths {
		if _, ok := knownCols.Load(colName(p)); !ok {
			unknown = true
			break
		}
	}
	if !unknown {
		return paths
	}

	schemaMu.Lock()
	if time.Since(lastSchemaReload) >= SchemaReloadInterval {
		lastSchemaReload = time.Now()
		if err := loadSchema(); err != nil {
			log.Printf("[SCHEMA] 重载超级表结构失败: %v", err)
		}
	}
	schemaMu.Unlock()

	if !schemaOK.Load() {
		return paths
	}
	kept := make([]string, 0, len(paths))
	for _, p := range paths {
		name := colName(p)
		if _, ok := knownCols.Load(name); ok {
			kept = append(kept, p)
		} else if _, warned := skippedWarned.LoadOrStore(name, true); !warned {
			log.Printf("[SCHEMA_SKIP] 点位 %s 在超级表中没有对应列 %s，已丢弃该点位（只提示一次）", p, name)
		}
	}
	return kept
}

// runRabbitMQConsumerWithRetry 监听多个队列，并包含自动重连与优雅退出的逻辑
func runRabbitMQConsumerWithRetry(ctx context.Context, workerChans []chan WrappedDelivery) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		log.Println("[INFO] 正在尝试连接 RabbitMQ...")
		conn, err := amqp091.Dial(MQURL)
		if err != nil {
			log.Printf("[ERROR] MQ 连接失败: %v，5秒后重试...", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}

		log.Println("[INFO] MQ 连接建立成功，正在创建通道...")
		ch, err := conn.Channel()
		if err != nil {
			log.Printf("[ERROR] MQ 通道创建失败: %v，正在关闭连接并重试...", err)
			_ = conn.Close()
			time.Sleep(2 * time.Second)
			continue
		}

		_ = ch.Qos(PrefetchCount, 0, false)

		// 触发重连的统一信号。任一情况发生都必须重连（旧代码只监听"连接断开"，
		// 漏掉了"消费者被取消 / 通道关闭"——SAC 重新选举、broker 取消消费者、channel 出错时，
		// 连接还活着但已经收不到消息，主循环却一直干等，于是就出现了"连着却永不消费"的卡死）。
		reconnect := make(chan struct{}, 1)
		signalReconnect := func(reason string) {
			select {
			case reconnect <- struct{}{}:
				log.Printf("[WARNING] MQ 需要重连: %s", reason)
			default: // 已有信号在排队，忽略重复
			}
		}

		// 1) 连接级关闭
		connClose := make(chan *amqp091.Error, 1)
		conn.NotifyClose(connClose)
		go func() {
			if e, ok := <-connClose; ok {
				signalReconnect(fmt.Sprintf("连接断开: %v", e))
			}
		}()
		// 2) 通道级关闭（连接还在、channel 出错关闭）
		chClose := make(chan *amqp091.Error, 1)
		ch.NotifyClose(chClose)
		go func() {
			if e, ok := <-chClose; ok {
				signalReconnect(fmt.Sprintf("通道关闭: %v", e))
			}
		}()
		// 3) 消费者被 broker 取消（SAC 重新选举、队列删除等）——"连着却不消费"的关键漏网点
		cancelCh := make(chan string, 2)
		ch.NotifyCancel(cancelCh)
		go func() {
			if tag, ok := <-cancelCh; ok {
				signalReconnect(fmt.Sprintf("消费者被取消(consumer-tag=%s)", tag))
			}
		}()

		// 用于控制当前连接下属的消费分发协程的上下文
		connCtx, connCancel := context.WithCancel(ctx)

		qConfigs := []struct {
			name string
			tag  int
		}{
			{NormalQueueName, 1},
			{PatchQueueName, 0},
		}
		args := amqp091.Table{"x-single-active-consumer": true}

		hasError := false
		for _, cfg := range qConfigs {
			if _, err := ch.QueueDeclare(cfg.name, true, false, false, false, args); err != nil {
				log.Printf("[ERROR] 队列 %s 声明失败: %v", cfg.name, err)
				hasError = true
				break
			}
			msgs, err := ch.Consume(cfg.name, "", false, false, false, false, nil)
			if err != nil {
				log.Printf("[ERROR] 队列 %s 消费监听失败: %v", cfg.name, err)
				hasError = true
				break
			}
			log.Printf("[INFO] 监听队列成功: %s", cfg.name)

			// 启动分发协程
			go func(data <-chan amqp091.Delivery, qname string, tag int) {
				for {
					select {
					case <-connCtx.Done():
						return
					case d, ok := <-data:
						if !ok {
							// 消费通道被关闭：若不是我方主动退出，触发重连（这是本次卡死的直接根因）
							if connCtx.Err() == nil {
								signalReconnect(fmt.Sprintf("队列 %s 的消费通道意外关闭", qname))
							}
							return
						}
						ip := gjson.GetBytes(d.Body, "ip").String()
						if ip == "" {
							_ = d.Ack(false)
							continue
						}
						idx := int(crc32.ChecksumIEEE([]byte(ip))) % WorkerCount

						// 将消息发送给对应 Worker，同时响应退出信号，防止 channel 满时阻塞无法退出
						select {
						case <-connCtx.Done():
							return
						case workerChans[idx] <- WrappedDelivery{QueueTag: tag, Delivery: d}:
						}
					}
				}
			}(msgs, cfg.name, cfg.tag)
		}

		if hasError {
			connCancel()
			_ = ch.Close()
			_ = conn.Close()
			time.Sleep(2 * time.Second)
			continue
		}

		// 阻塞等待：退出信号，或任一重连信号（连接断开/通道关闭/消费者取消/消费通道关闭）
		select {
		case <-ctx.Done():
			log.Println("[INFO] 收到退出信号，正在关闭 MQ 连接并退出消费者...")
			connCancel()
			_ = ch.Close()
			_ = conn.Close()
			return
		case <-reconnect:
			connCancel()
			_ = ch.Close()
			_ = conn.Close()
			time.Sleep(2 * time.Second) // 略等，给 broker 释放旧消费者 / SAC 重新选举的时间
			// 进入下一轮循环尝试重连
		}
	}
}

// ============================================================================
// 4. 业务逻辑处理 (攒批与写入)
// ============================================================================

// startBatchWorker 工作协程：负责内存攒批，支持 Context 退出控制
func startBatchWorker(ctx context.Context, id int, myChan <-chan WrappedDelivery) {
	var batch []WrappedDelivery
	for {
		select {
		case <-ctx.Done():
			log.Printf("[INFO] Worker-%02d 收到退出信号，结束运行", id)
			return
		case msg, ok := <-myChan:
			if !ok {
				return
			}
			batch = append(batch, msg)
			timeout := time.After(MaxWaitTime)

		LOOP:
			for len(batch) < BatchPerWorker {
				select {
				case nextMsg, ok := <-myChan:
					if !ok {
						break LOOP
					}
					batch = append(batch, nextMsg)
				case <-timeout:
					break LOOP // 达到最大等待时间，即使没攒够也执行写入
				case <-ctx.Done():
					// 即使收到退出信号，也将当前已攒到的数据尽力写一次（ctx 已取消，execCtx 用独立超时仍会尝试一次；
					// 写不进也不阻塞退出，未 Ack 的消息重启后 MQ 会重投）
					if len(batch) > 0 {
						processCombinedBatch(ctx, id, batch)
					}
					log.Printf("[INFO] Worker-%02d 收到退出信号，处理完残留数据后结束运行", id)
					return
				}
			}

			// processCombinedBatch 返回 true=已入库/脏行已隔离，可 Ack；false=正在退出，整批 Nack 由 MQ 重投。
			// DB 宕机时它会在内部原地阻塞重试、绝不返回 true 丢数（除非收到退出信号）。
			if processCombinedBatch(ctx, id, batch) {
				for _, m := range batch {
					_ = m.Delivery.Ack(false)
				}
			} else {
				for _, m := range batch {
					_ = m.Delivery.Nack(false, true)
				}
			}
			batch = batch[:0]
		}
	}
}

// processCombinedBatch 解析数据并拼接批量 INSERT SQL
func processCombinedBatch(ctx context.Context, workerID int, tasks []WrappedDelivery) bool {
	if len(tasks) == 0 {
		return true
	}

	groups := make(map[string][]RowItem)
	groupColPaths := make(map[string][]string)
	totalRows := 0

	// 1. 解析 JSON 并按 IP 分组
	//    注意：内部炉次号/晶编号是"状态机"，必须在解析这一步、按记录到达顺序逐条推进并算好，
	//    存进 RowItem。后续批量拼 SQL 或单行重试都只是读取，绝不重复推进状态机。
	for _, task := range tasks {
		res := gjson.ParseBytes(task.Delivery.Body)
		ip := res.Get("ip").String()
		deviceNo := res.Get("deviceNo").String()
		records := res.Get("datas").Array()

		if ip == "" || deviceNo == "" {
			continue
		}

		tableName := ChildTablePrefix + strings.ReplaceAll(ip, ".", "_")

		for _, rec := range records {
			rawData := rec.Get("data")

			// —— 计算内部炉次号 & 内部晶编号 ——
			// 仅实时数据(QueueTag==1)参与状态机推进；补发数据(QueueTag==0)不参与、不改动缓存，
			// 两列留空(写库时落 NULL)，避免历史回补数据打乱实时状态机或盖上错误的号。
			var furnaceNo, crystalNo string
			if task.QueueTag == 1 {
				furnaceNo, crystalNo = computeInnerNumbers(tableName, rawData)
			}

			groups[ip] = append(groups[ip], RowItem{
				CaptureTime:    rec.Get("capture_time").String(),
				Seq:            rec.Get("seq").Int(),
				Queue:          task.QueueTag,
				DeviceNo:       deviceNo,
				DeviceIP:       ip,
				RawData:        rawData,
				InnerFurnaceNo: furnaceNo,
				InnerCrystalNo: crystalNo,
			})
			totalRows++
		}
	}
	if totalRows == 0 {
		return true
	}

	// 2. 构造多表 INSERT 语句
	var sb strings.Builder
	sb.WriteString("INSERT INTO ")
	tempLastTsMap := make(map[string]int64)

	for ip, rows := range groups {
		tableName := ChildTablePrefix + strings.ReplaceAll(ip, ".", "_")

		// [核心逻辑] 时间戳平滑计算：防止同一毫秒内多条数据导致 TDengine 主键覆盖
		var lastTs int64
		if val, ok := lastTsCache.Load(tableName); ok {
			lastTs = val.(int64)
		}
		startTs := time.Now().UnixMilli()
		if startTs <= lastTs {
			startTs = lastTs + 1 // 强制将本批次的起点挪到上一批次的终点之后
		}

		// 动态列探测：自动扫描 JSON 键得到 组名.点位名
		var allPaths []string
		rows[0].RawData.ForEach(func(key, value gjson.Result) bool {
			gK := key.String()
			if gK == "capture_time" || gK == "404" {
				return true
			}
			value.ForEach(func(pK, pV gjson.Result) bool {
				allPaths = append(allPaths, gK+"."+pK.String())
				return true
			})
			return true
		})

		// 超级表里没有的点位直接丢弃，只写库里已有的列
		colPaths := filterKnownPaths(allPaths)
		groupColPaths[ip] = colPaths

		var header strings.Builder
		header.WriteString(" (ts, capture_time, crystal_number, inner_furnace_no, inner_crystal_no, seqno, queue")
		for _, p := range colPaths {
			header.WriteString(",`" + colName(p) + "` ")
		}
		header.WriteString(") VALUES ")

		strFlags := make([]bool, len(colPaths))
		for i, p := range colPaths {
			strFlags[i] = colIsString(p)
		}

		sb.WriteString(fmt.Sprintf(" %s.%s %s ", DBName, tableName, header.String()))
		for i, row := range rows {
			if i > 0 {
				sb.WriteByte(',')
			}
			rowTs := startTs + int64(i) // 包内每行数据顺延 1ms，确保 ts 绝对唯一
			sb.WriteString(fmt.Sprintf("(%d,'%s','%s',%s,%s,%d,%d",
				rowTs, row.CaptureTime,
				getHimCrystalNumber(row.RawData.Get("404")),
				quoteOrNull(row.InnerFurnaceNo), quoteOrNull(row.InnerCrystalNo), // 补发行为空 -> NULL
				row.Seq, row.Queue))

			for ci, path := range colPaths {
				sb.WriteByte(',')
				formatValue(&sb, row.RawData.Get(path), strFlags[ci])
			}
			sb.WriteByte(')')
		}
		tempLastTsMap[tableName] = startTs + int64(len(rows)-1)
	}

	// 3. 执行写入（带超时 + 韧性重试）：
	//    - 成功            -> 更新时间戳缓存，Ack。
	//    - DB 宕机         -> 原地阻塞重试同一批 SQL（数据与内部号都不变，绝不丢、不重算），直到 DB 恢复或收到退出信号。
	//    - DB 在线但整批失败 -> 批内有脏数据，转单行隔离（坏行丢弃、好行入库），Ack（脏行不该无限重投堵队列）。
	for {
		// 每次尝试前确保子表存在/Tag 同步（幂等，缓存命中即 no-op；也能在 DB 恢复后补建首次出现的子表）
		for ip, rows := range groups {
			tn := ChildTablePrefix + strings.ReplaceAll(ip, ".", "_")
			ensureTableExists(tn, rows[0].DeviceNo, rows[0].DeviceIP)
		}

		err := execCtx(sb.String())
		if err == nil {
			for tName, newTs := range tempLastTsMap {
				lastTsCache.Store(tName, newTs)
			}
			return true
		}

		if ctx.Err() != nil {
			return false // 正在优雅退出：整批 Nack，重启后 MQ 重投
		}

		if !dbAlive() {
			log.Printf("[DB_DOWN] Worker-%02d 批量写入失败且 DB 不可用，%v 后原地重试（数据不丢、内部号不变）: %v", workerID, DBDownRetry, err)
			select {
			case <-ctx.Done():
				return false
			case <-time.After(DBDownRetry):
			}
			continue // 重试同一条 SQL
		}

		// DB 在线但整批失败 -> 批内混有脏数据，逐行隔离
		log.Printf("[DB_ERROR] Worker-%02d 批量失败但 DB 在线（疑似脏数据），转单行隔离: %v", workerID, err)
		handleBatchRetry(ctx, workerID, groups, groupColPaths)
		return true
	}
}

// ensureTableExists 负责创建子表以及同步 Tag (炉台号)
func ensureTableExists(tableName string, deviceNo string, deviceIP string) {
	// 1. 如果子表完全不存在，则创建它
	if _, ok := tableExistenceCache.Load(tableName); !ok {
		lockIntf, _ := tableLocks.LoadOrStore(tableName, &sync.Mutex{})
		lock := lockIntf.(*sync.Mutex)
		lock.Lock()
		if _, ok := tableExistenceCache.Load(tableName); !ok {
			createSql := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s.%s USING %s.%s TAGS ('%s', '%s')",
				DBName, tableName, DBName, SuperTableName, deviceNo, deviceIP)
			if err := execCtx(createSql); err == nil {
				tableExistenceCache.Store(tableName, true)
				tableTagCache.Store(tableName, deviceNo)
			}
		}
		lock.Unlock()
	}

	// 2. 如果炉台号 ID 变了，则更新 TDengine 子表的 Tag
	cachedTag, ok := tableTagCache.Load(tableName)
	if !ok || cachedTag.(string) != deviceNo {
		alterSql := fmt.Sprintf("ALTER TABLE %s.%s SET TAG device_id = '%s'", DBName, tableName, deviceNo)
		if err := execCtx(alterSql); err == nil {
			tableTagCache.Store(tableName, deviceNo)
			log.Printf("[TAG_UPDATE] 表 %s 的炉台号已变更为: %s", tableName, deviceNo)
		}
	}
}

// handleBatchRetry 单行隔离：批量失败(且 DB 在线)时逐行插入，把脏数据行从好数据里择出去。
//   - 单行成功            -> 入库。
//   - 单行失败但 DB 在线   -> 该行确为脏数据(JSON 不合规/越界等)，记录并丢弃，不阻塞队列。
//   - 单行失败且 DB 又宕机 -> 阻塞等待恢复后重试本行，绝不丢数（除非收到退出信号）。
func handleBatchRetry(ctx context.Context, workerID int, groups map[string][]RowItem, colPathsMap map[string][]string) {
	for ip, rows := range groups {
		tableName := ChildTablePrefix + strings.ReplaceAll(ip, ".", "_")
		paths := colPathsMap[ip]
		for _, row := range rows {
			for {
				rowTs := time.Now().UnixMilli()
				if val, ok := lastTsCache.Load(tableName); ok {
					if rowTs <= val.(int64) {
						rowTs = val.(int64) + 1
					}
				}

				var rsb strings.Builder
				rsb.WriteString(fmt.Sprintf("INSERT INTO %s.%s (ts, capture_time, crystal_number, inner_furnace_no, inner_crystal_no, seqno, queue", DBName, tableName))
				for _, path := range paths {
					p := strings.Split(path, ".")
					rsb.WriteString(fmt.Sprintf(",`f_%s_%s` ", p[0], strings.ReplaceAll(p[1], "-", "_")))
				}
				rsb.WriteString(fmt.Sprintf(") VALUES (%d,'%s','%s',%s,%s,%d,%d",
					rowTs, row.CaptureTime,
					getHimCrystalNumber(row.RawData.Get("404")),
					quoteOrNull(row.InnerFurnaceNo), quoteOrNull(row.InnerCrystalNo), // 复用批量阶段已算好的号；补发行为空 -> NULL
					row.Seq, row.Queue))
				for _, path := range paths {
					rsb.WriteByte(',')
					formatValue(&rsb, row.RawData.Get(path), colIsString(path))
				}
				rsb.WriteString(")")

				err := execCtx(rsb.String())
				if err == nil {
					lastTsCache.Store(tableName, rowTs)
					break // 本行完成
				}
				if ctx.Err() != nil {
					return // 优雅退出
				}
				if dbAlive() {
					// DB 在线仍失败 -> 确为脏数据，丢弃该行(不阻塞队列)并输出，便于定位生产端问题
					log.Printf("[DIRTY_DROP] Worker-%02d 脏数据行已丢弃(表 %s): %v", workerID, tableName, err)
					break
				}
				// 重试途中 DB 又宕机 -> 等待后重试本行，不丢数
				log.Printf("[DB_DOWN] Worker-%02d 单行重试期间 DB 不可用，%v 后重试本行...", workerID, DBDownRetry)
				select {
				case <-ctx.Done():
					return
				case <-time.After(DBDownRetry):
				}
			}
		}
	}
}

// quoteOrNull 把字符串转成 SQL 字面量：非空则加单引号并转义，空串则写 NULL。
// 用于内部炉次号/晶编号两列——补发行这两列为空，落库时写 NULL 而非空字符串。
func quoteOrNull(s string) string {
	if s == "" {
		return "NULL"
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// formatValue 按目标列类型格式化：字符串列(自适应加出来的列)一律写成带引号的字符串，其余走原有逻辑。
func formatValue(sb *strings.Builder, v gjson.Result, strCol bool) {
	if !strCol {
		formatValueOptimized(sb, v)
		return
	}
	if !v.Exists() || v.Type == gjson.Null {
		sb.WriteString("NULL")
		return
	}
	s := v.Raw
	if v.Type == gjson.String {
		s = v.Str
	}
	sb.WriteByte('\'')
	sb.WriteString(strings.ReplaceAll(s, "'", "''"))
	sb.WriteByte('\'')
}

// formatValueOptimized 优化的值格式化
func formatValueOptimized(sb *strings.Builder, v gjson.Result) {
	if !v.Exists() {
		sb.WriteString("NULL")
		return
	}
	switch v.Type {
	case gjson.True:
		sb.WriteByte('1')
	case gjson.False:
		sb.WriteByte('0')
	case gjson.Number:
		// 生产端可能发出 NaN / Infinity（无效值），TDengine 不认，写 NULL 以免整行被丢
		if f, err := strconv.ParseFloat(v.Raw, 64); err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			sb.WriteString("NULL")
			return
		}
		sb.WriteString(v.Raw)
	case gjson.String:
		sb.WriteByte('\'')
		sb.WriteString(strings.ReplaceAll(v.Str, "'", "''"))
		sb.WriteByte('\'')
	default:
		sb.WriteString("NULL")
	}
}

// ============================================================================
// 5. 内部炉次号 / 内部晶编号 状态机（对标 Java getDeviceSeq / getInnerCrystalNumber）
// ============================================================================

// computeInnerNumbers 依据当前记录的工步号，结合该设备上一条记录的缓存状态，
// 计算并返回本条记录的 (内部炉次号, 内部晶编号)，同时推进（更新）状态缓存。
//
// 顺序保证：同一 IP 恒定路由到同一 Worker，Worker 串行处理，故同一 tableName 的状态机天然单线程，
// 无需额外加锁。
func computeInnerNumbers(tableName string, rawData gjson.Result) (furnaceNo, crystalNo string) {
	stateNo := normalizeState(rawData.Get(StateNoPath))

	prevIntf, ok := stateCache.Load(tableName)

	// 内存无缓存：可能是进程重启后该设备的第一条。先尝试从 TDengine 最后一行恢复状态，
	// 避免把"重启后第一条"误判为首条而凭空铸出新炉次/晶编号（造成假边界）。
	if !ok {
		if info, recovered := rehydrateFromDB(tableName); recovered {
			prevIntf, ok = info, true
		}
	}

	// 真·首条（内存没有、库里也查不到该设备历史）：直接采用全新号
	if !ok {
		furnaceNo = uuid.NewString()
		crystalNo = uuid.NewString()
		stateCache.Store(tableName, &StateInfo{
			StateNo:            stateNo,
			PreStateNo:         "",
			DeviceSeq:          furnaceNo,
			InnerCrystalNumber: crystalNo,
			CrystalEnding:      isEndingState(stateNo),
			FurnaceStopped:     furnaceStopSteps[stateNo],
		})
		return
	}
	prev := prevIntf.(*StateInfo)

	// 本条无有效工步号：保持上一次的号，且不推进状态机，避免瞬时丢点污染状态。
	if stateNo == "" {
		return prev.DeviceSeq, prev.InnerCrystalNumber
	}

	// 计算"有效的上一个工步号"：上一条若为手动(0.0)，则穿透取其 PRE_STATE_NO
	preStateNo := prev.StateNo
	if preStateNo == "0.0" {
		preStateNo = prev.PreStateNo
	}

	newFurnace := uuid.NewString()
	newCrystal := uuid.NewString()

	// 炉次号：停炉(16.x) 置标记 → 之后第一个冷启动(1.0/2.0/3.0/4.0) 开新炉
	var furnaceStopped bool
	furnaceNo, furnaceStopped = decideDeviceSeq(stateNo, preStateNo, prev.DeviceSeq, newFurnace, prev.FurnaceStopped)
	// 晶编号："以结束(收尾14.x/取段20.x/放断21.2/停炉16.x)为界"的状态机
	var crystalEnding bool
	crystalNo, crystalEnding = decideInnerCrystal(stateNo, preStateNo, prev.InnerCrystalNumber, prev.CrystalEnding, newCrystal)

	// 更新缓存：PRE_STATE_NO 存"截至本条为止的最后一个非手动工步号"
	newPre := preStateNo
	if stateNo != "0.0" {
		newPre = stateNo
	}
	stateCache.Store(tableName, &StateInfo{
		StateNo:            stateNo,
		PreStateNo:         newPre,
		DeviceSeq:          furnaceNo,
		InnerCrystalNumber: crystalNo,
		CrystalEnding:      crystalEnding,
		FurnaceStopped:     furnaceStopped,
	})
	return
}

// warmupStateCache 启动时一次性预热：用一条 PARTITION BY tbname + LAST_ROW 的批量查询，
// 取出每个子表的最后一行状态，全部载入 stateCache。相比"每台设备第一条消息各查一次库"的按需反查，
// 这里只发 1 条查询、在消费开始之前完成，既不占用 Worker 热路径（提速、减堆压），
// 也不会因反查慢而卡住消费。预热失败不致命——退化为原来的按需 rehydrateFromDB。
func warmupStateCache() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	q := fmt.Sprintf(
		"SELECT tbname, LAST_ROW(f_33_0_0), LAST_ROW(inner_furnace_no), LAST_ROW(inner_crystal_no) FROM %s.%s PARTITION BY tbname",
		DBName, SuperTableName)
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		log.Printf("[WARMUP] 批量预热失败（将退化为按需反查，不影响正确性）: %v", err)
		return
	}
	defer rows.Close()

	n := 0
	for rows.Next() {
		var tb string
		var st sql.NullFloat64
		var fno, cno sql.NullString
		if err := rows.Scan(&tb, &st, &fno, &cno); err != nil {
			continue
		}
		if !fno.Valid || fno.String == "" {
			continue // 该子表历史行还没写过内部号（老数据）-> 留给首条逻辑铸号
		}
		stateNo := ""
		if st.Valid {
			stateNo = strconv.FormatFloat(st.Float64, 'f', 1, 64)
		}
		stateCache.Store(tb, &StateInfo{
			StateNo: stateNo,
			// 近似：仅当最后一行恰为手动 0.0 时 PreStateNo 会略有偏差（只影响该设备重启后第一次穿透判断），
			// 概率极低、影响面仅一行，忽略即可；如需绝对精确可另跑一次 last(非0工步)。
			PreStateNo:         stateNo,
			DeviceSeq:          fno.String,
			InnerCrystalNumber: cno.String,
			CrystalEnding:      isEndingState(stateNo),
			FurnaceStopped:     furnaceStopSteps[stateNo],
		})
		n++
	}
	if err := rows.Err(); err != nil {
		log.Printf("[WARMUP] 预热读取中断: %v（已恢复 %d 个子表，其余走按需反查）", err, n)
		return
	}
	log.Printf("[WARMUP] 状态预热完成，一次性恢复 %d 个子表的炉次/晶编号", n)
}

// rehydrateFromDB 进程重启后，从 TDengine 子表最后一行恢复该设备的状态机缓存。
//
// 之所以反查库就够用，关键在于本程序"写库成功后才 Ack"：库里的最后一行 == MQ 续传起点的前一条，
// 恢复点与消息重投点天然对齐，不会漏也不会错位。
// 返回恢复出的状态与是否成功；子表无数据(真·首次)或历史行还没写过内部号(老数据)时返回 false，
// 交给"首条"逻辑重新铸号。
//
// 并发安全：同一 tableName 恒由固定 Worker 串行处理，本函数不会并发进入同一 key。
func rehydrateFromDB(tableName string) (*StateInfo, bool) {
	// 用 ORDER BY ts DESC LIMIT 1 取最后一行，版本兼容性最好、单行各列取值一致。
	qctx, qcancel := context.WithTimeout(context.Background(), DBOpTimeout)
	defer qcancel()
	q := fmt.Sprintf(
		"SELECT f_33_0_0, inner_furnace_no, inner_crystal_no FROM %s.%s ORDER BY ts DESC LIMIT 1",
		DBName, tableName)
	var st sql.NullFloat64
	var fno, cno sql.NullString
	if err := db.QueryRowContext(qctx, q).Scan(&st, &fno, &cno); err != nil {
		return nil, false // 子表不存在 / 无数据 -> 视为首条
	}
	if !fno.Valid || fno.String == "" {
		return nil, false // 老数据行还没有内部号 -> 视为首条重新铸号
	}

	stateNo := ""
	if st.Valid {
		stateNo = strconv.FormatFloat(st.Float64, 'f', 1, 64)
	}

	// 还原 PRE_STATE_NO：仅当最后一行恰停在手动(0.0)时，才回查最近一个非手动工步；否则用不上。
	preStateNo := stateNo
	if stateNo == "0.0" {
		preStateNo = ""
		pq := fmt.Sprintf(
			"SELECT f_33_0_0 FROM %s.%s WHERE f_33_0_0 <> 0 ORDER BY ts DESC LIMIT 1",
			DBName, tableName)
		pctx, pcancel := context.WithTimeout(context.Background(), DBOpTimeout)
		var pre sql.NullFloat64
		if err := db.QueryRowContext(pctx, pq).Scan(&pre); err == nil && pre.Valid {
			preStateNo = strconv.FormatFloat(pre.Float64, 'f', 1, 64)
		}
		pcancel()
	}

	info := &StateInfo{
		StateNo:            stateNo,
		PreStateNo:         preStateNo,
		DeviceSeq:          fno.String,
		InnerCrystalNumber: cno.String,
		CrystalEnding:      isEndingState(stateNo),    // 若最后一行正处于收尾/放断/停炉段，恢复结束阶段标记
		FurnaceStopped:     furnaceStopSteps[stateNo], // 若最后一行是停炉码，恢复"已停炉、待重启"标记
	}
	stateCache.Store(tableName, info)
	log.Printf("[REHYDRATE] 表 %s 重启恢复: state=%s furnace=%s crystal=%s", tableName, stateNo, fno.String, cno.String)
	return info, true
}

// furnaceStopSteps 停炉码：16.0 非取段停炉 / 16.1 取段停炉 / 16.2 异常停炉——一炉的终点标志。
var furnaceStopSteps = map[string]bool{"16.0": true, "16.1": true, "16.2": true}

// furnaceColdSteps 冷启动码：1.0 初始化 / 2.0 抽空 / 3.0 检漏 / 4.0 压力化。
// 停炉之后遇到其中第一个即为新炉起点。含 3.0：初始化/抽空常被手动 0.0 吞掉，
// 检漏是能露出来的冷启动，用它兜底"手动里藏了 1/2 工步"的情况。
var furnaceColdSteps = map[string]bool{"1.0": true, "2.0": true, "3.0": true, "4.0": true}

// decideDeviceSeq 内部炉次号判定（停炉做边界 + 停炉后首个冷启动开新炉）。
//
// 规则：
//   - 见停炉码 16.x → 置"已停炉、待重启"标记，炉次号此刻不变（停炉段仍属当前炉）；
//   - 处于"已停炉"状态时，遇到第一个冷启动 1.0/2.0/3.0/4.0（且工步变化）→ 开新炉、清标记；
//     一炉只切一次（清标记后，同一炉后续再出现的冷启动不再触发，避免把一炉切成两半）。
//
// 入参 stopped=上一条是否处于"已停炉"；返回本条炉次号与新的 stopped 标记。
func decideDeviceSeq(stateNo, preStateNo, oldDeviceSeq, newDeviceSeq string, stopped bool) (string, bool) {
	// 停炉：本段仍属当前炉，置"已停炉、待重启"标记
	if furnaceStopSteps[stateNo] {
		return oldDeviceSeq, true
	}
	// 停炉之后遇到的第一个冷启动 → 开新炉
	changed := preStateNo == "" || stateNo != preStateNo
	if stopped && changed && furnaceColdSteps[stateNo] {
		return newDeviceSeq, false
	}
	// 其余（含一炉中途/未停炉时的这些工步、空闲段）：维持当前炉号与标记
	return oldDeviceSeq, stopped
}

// endingStates 结束段工步集合——一根晶棒的“收官”标志。进入其中任一工步即视为正在结束，
// 直到离开结束段（进入复投化料/预热等下一轮）才生成下一根晶。依据工步图 + 真实数据校准：
//
//	收尾三选一：14.0 循环收尾 / 14.1 完结收尾 / 15.0 快速收尾
//	取段冷却三选一：20.0 取段冷却加料 / 20.1 取段冷却调温 / 20.2 取段冷却结晶
//	放断：21.2 放断提出（断线，等径中途断棒）
//	停炉三选一：16.0 非取段停炉 / 16.1 取段停炉 / 16.2 异常停炉（
//
// 注：32.0 回熔、30.0 紧急安全保护 是否也计为“结束”待确认，暂不纳入。
var endingStates = map[string]bool{
	"14.0": true, // 循环收尾
	"14.1": true, // 完结收尾
	"15.0": true, // 快速收尾
	"20.0": true, // 取段冷却加料
	"20.1": true, // 取段冷却调温
	"20.2": true, // 取段冷却结晶
	"21.2": true, // 放断提出（断线）
	"16.0": true, // 非取段停炉
	"16.1": true, // 取段停炉
	"16.2": true, // 异常停炉
}

func isEndingState(s string) bool { return endingStates[s] }

// decideInnerCrystal 内部晶编号判定（以“结束”为界，而非以“预热起点/初始化”为界）。
//
// 定义：一根晶 = 相邻两次“结束”之间的整段。结束 = 收尾(14.0/14.1/15.0) / 取段冷却(20.0/20.1/20.2)
// / 放断(21.2) / 停炉(16.x)。
// 关键点：
//   - 收尾/放断/停炉段本身仍归属“正在结束的这根晶”；只有离开结束段、进入下一轮
//     （复投化料 / 预热 / 引晶等）时，才生成下一根晶的号。这样一根晶内部因回熔重来而反复
//     预热/引晶（如 7.0→0.0→7.0）不会被误切成多根。
//   - 不再对 1.0 初始化单独换晶：开机阶段工步会 1,2,3,0,1,2,3 反复出现 1.0，若见 1.0 就换晶会把
//     本该是一根的开机段误切成多根。换炉时“下一炉晶编绝对变化”已由 16.x 停炉（属结束段）兜住——
//     一炉以停炉收尾，离开该结束段进入下一炉开机时自然起新晶。
//
// 入参 inEnd 为“上一条是否处于结束阶段”；返回本条应用的晶编号，以及写回缓存的新结束阶段标记。
func decideInnerCrystal(stateNo, preStateNo, oldInnerCrystal string, inEnd bool, newInnerCrystal string) (string, bool) {
	// 手动 0.0：中性——既不换号也不改变结束阶段标记（与炉次号一致地穿透手动）。
	if stateNo == "0.0" {
		return oldInnerCrystal, inEnd
	}

	changed := preStateNo == "" || stateNo != preStateNo

	// 进入/停留在结束段（收尾/取段/放断/停炉）：仍算当前这根晶，标记“正在收官”。
	if isEndingState(stateNo) {
		if changed {
			return oldInnerCrystal, true
		}
		return oldInnerCrystal, inEnd
	}

	// 其它工步（引晶/放肩/等径/复投化料等）：
	// 若刚从结束段离开，则本行开启下一根晶；否则维持当前晶。
	if inEnd {
		return newInnerCrystal, false
	}
	return oldInnerCrystal, false
}

// normalizeState 把工步号统一归一化为一位小数的字符串（如 1 -> "1.0", 7.2 -> "7.2", 10 -> "10.0"），
// 从而无论 PLC 上送的是数字还是字符串，都能与 "1.0"/"2.0"/"7.2" 等常量稳定比较。
// 取不到或无法解析为数字时返回空串。
func normalizeState(v gjson.Result) string {
	if !v.Exists() {
		return ""
	}
	switch v.Type {
	case gjson.Number:
		return strconv.FormatFloat(v.Num, 'f', 1, 64)
	case gjson.String:
		s := strings.TrimSpace(v.Str)
		if s == "" {
			return ""
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return strconv.FormatFloat(f, 'f', 1, 64)
		}
		return s // 非数字则原样返回
	default:
		return ""
	}
}

// getHimCrystalNumber 1:1 复刻 Java 版本的晶编解析与严格校验逻辑
func getHimCrystalNumber(data404 gjson.Result) string {
	if !data404.Exists() {
		return ""
	}

	var chars []rune
	lastNonZeroIndex := 0

	// 1. 遍历 28 到 126 字节（步长为2）
	for i := 28; i < 128; i += 2 {
		val := data404.Get(fmt.Sprintf("%d-0", i))
		if val.Exists() {
			asciiValue := int(val.Int())
			character := rune(asciiValue)
			chars = append(chars, character)

			// 只要字符不是 \0 且不是空格(32)，就更新最后一个有效字符的索引
			if asciiValue != 0 && asciiValue != 32 {
				lastNonZeroIndex = len(chars)
			}
		}
	}

	// 如果没有任何有效字符，返回空
	if lastNonZeroIndex == 0 {
		return ""
	}

	// 2. 截断到最后一个非0、非空格的字符
	truncated := chars[:lastNonZeroIndex]

	// 3. 核心物理校验：截断后的最后一个字符必须是换行符 '\n' (ASCII 10)
	if truncated[len(truncated)-1] != '\n' {
		return ""
	}

	// 4. 去除末尾的连续空格（对标 Java 的 trimIndex 逻辑）
	trimIndex := len(truncated)
	for trimIndex > 0 && truncated[trimIndex-1] == ' ' {
		trimIndex--
	}
	truncated = truncated[:trimIndex]

	// 5. 将结果转为字符串，并移除所有的换行符 '\n'
	resultStr := string(truncated)
	resultStr = strings.ReplaceAll(resultStr, "\n", "")

	return resultStr
}
