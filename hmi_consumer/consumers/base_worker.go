package consumers

import (
	"context"
	"sync"
	"time"

	"hmi1.1_consumer/common"
	"hmi1.1_consumer/config"
	"hmi1.1_consumer/logx"

	"github.com/rabbitmq/amqp091-go"
)

// dbDownPollInterval DB 不可达时的探活轮询间隔（此期间队列阻塞，等待 DB 恢复，绝不丢数据）
var dbDownPollInterval = config.DBRetry

// dbReachable 轻量探活：判断 TDengine 是否可达。
// 用于区分两类写入失败：DB 可达 → 数据本身有问题(脏数据)；DB 不可达 → 基础设施故障。
func dbReachable() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return common.DB.PingContext(ctx) == nil
}

// logThrottle 同类日志限流器：间隔内的重复调用被计数，下次打印时汇总被抑制的条数，防止刷屏。
// emit 为已绑定级别与标签的实际输出函数（如 logx.Warnf("WRITE", ...)）。
type logThrottle struct {
	mu         sync.Mutex
	suppressed int
	last       time.Time
	interval   time.Duration
	emit       func(format string, args ...any)
}

func newLogThrottle(interval time.Duration, emit func(format string, args ...any)) *logThrottle {
	return &logThrottle{interval: interval, emit: emit}
}

// printf 限流打印。距上次打印超过 interval 才真正输出，并带出期间被抑制的条数；否则只累加计数。
func (l *logThrottle) printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if l.last.IsZero() || now.Sub(l.last) >= l.interval {
		if l.suppressed > 0 {
			l.emit(format+"（期间另有 %d 条同类被抑制未打印）", append(args, l.suppressed)...)
		} else {
			l.emit(format, args...)
		}
		l.suppressed = 0
		l.last = now
	} else {
		l.suppressed++
	}
}

// StartBaseWorker 启动通用消费者（支持自动重连、优雅退出）。
// 失败处理策略：写入失败时先探活区分脏数据与 DB 宕机 ——
//   - DB 可达：判定为脏数据(生产端 JSON 不合规，永远插不进去)，立即丢弃，不阻塞队列；
//   - DB 不可达：判定为基础设施故障(数据是好的)，阻塞等待 DB 恢复后重试，绝不丢数据。
func StartBaseWorker(ctx context.Context, wg *sync.WaitGroup, qName string, stableName string, handler func([]byte, string) error) {
	defer wg.Done()

	// 日志限流器：脏数据丢弃(WARN，高频)与 DB 宕机(ERROR，持续)两类日志分别限流，防止刷屏
	dropLog := newLogThrottle(5*time.Second, func(f string, a ...any) { logx.Warnf("WRITE", f, a...) })
	dbDownLog := newLogThrottle(30*time.Second, func(f string, a ...any) { logx.Errorf("WRITE", f, a...) })

	for {
		// 检查系统是否已经要求退出
		select {
		case <-ctx.Done():
			logx.Infof("CONSUMER", "%s 收到退出信号，终止消费协程", qName)
			return
		default:
		}

		// 1. 建立 MQ 连接
		logx.Infof("MQ", "%s 正在尝试连接 MQ...", qName)
		conn, err := amqp091.Dial(config.MQURL)
		if err != nil {
			logx.Errorf("MQ", "%s 连接 MQ 失败: %v，5秒后重试...", qName, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}

		ch, err := conn.Channel()
		if err != nil {
			logx.Errorf("MQ", "%s 打开 Channel 失败: %v，稍后重试...", qName, err)
			_ = conn.Close()
			time.Sleep(2 * time.Second)
			continue
		}

		// 2. 声明队列 (开启 SAC 模式保证顺序)
		args := amqp091.Table{"x-single-active-consumer": true}
		_, err = ch.QueueDeclare(qName, true, false, false, false, args)
		if err != nil {
			logx.Errorf("MQ", "%s 声明队列失败: %v，稍后重试...", qName, err)
			_ = ch.Close()
			_ = conn.Close()
			time.Sleep(2 * time.Second)
			continue
		}

		_ = ch.Qos(config.Prefetch, 0, false)

		// 3. 开启消费
		msgs, err := ch.Consume(qName, "", false, false, false, false, nil)
		if err != nil {
			logx.Errorf("MQ", "%s 开启消费失败: %v，稍后重试...", qName, err)
			_ = ch.Close()
			_ = conn.Close()
			time.Sleep(2 * time.Second)
			continue
		}

		logx.Infof("CONSUMER", "%s 已启动并开始监听", qName)

		// 4. 监听连接异常关闭信号
		closeChan := make(chan *amqp091.Error, 1)
		conn.NotifyClose(closeChan)

		// 用于控制当前连接周期的上下文
		connCtx, connCancel := context.WithCancel(ctx)

		// 在后台监控连接状态与系统退出信号
		go func() {
			select {
			case <-connCtx.Done():
				return
			case <-ctx.Done():
				_ = ch.Close()
				_ = conn.Close()
			case err := <-closeChan:
				logx.Warnf("MQ", "%s 连接丢失: %v", qName, err)
				_ = ch.Close()
				_ = conn.Close()
			}
		}()

		// 5. 消费循环
		for d := range msgs {
			// 执行业务处理函数
			if err := handler(d.Body, stableName); err == nil {
				// --- 处理成功 ---
				_ = d.Ack(false)
			} else if dbReachable() {
				// --- 处理失败 & DB 可达：脏数据，重试也永远插不进去 → 立即丢弃，不阻塞队列 ---
				dropLog.printf("%s 丢弃脏数据(生产端JSON不合规): %v", qName, err)
				_ = d.Ack(false)
			} else {
				// --- 处理失败 & DB 不可达：基础设施故障，数据是好的 → 阻塞等待恢复后重试，绝不丢数据 ---
				dbDownLog.printf("%s 数据库不可达，暂停消费并回滚消息，等待恢复后重试...", qName)

				// Nack 退回队头，等待期间可响应系统退出信号立即停机
				_ = d.Nack(false, true)
				select {
				case <-ctx.Done():
					connCancel()
					return
				case <-time.After(dbDownPollInterval):
					// 轮询等待 DB 恢复
				}
			}
		}

		// 本次连接循环结束，释放相关资源，进入下一轮重连循环
		connCancel()
	}
}
