// ccdpeek 本地窥探工具：从 HMI_CCD_Data 队列取消息，只打印指定炉台 IP 的原始 JSON。
// 取到的消息不 Ack，窗口结束后全部 Nack 并放回队列（requeue），生产消费者照常入库，数据不丢。
//
// 用法: go run ./tools/ccdpeek -ips 10.65.111.1,10.65.111.3 -dur 60s -max 3
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"hmi1.1_consumer/config"

	"github.com/rabbitmq/amqp091-go"
	"github.com/tidwall/gjson"
)

func main() {
	ips := flag.String("ips", "10.65.111.1,10.65.111.3", "要打印的炉台 IP，逗号分隔")
	dur := flag.Duration("dur", 60*time.Second, "总运行时长")
	maxPer := flag.Int("max", 3, "每个 IP 最多打印条数")
	window := flag.Duration("window", 2*time.Second, "每轮持有消息的最长时间，到点放回队列")
	flag.Parse()

	want := map[string]int{}
	for _, ip := range strings.Split(*ips, ",") {
		want[strings.TrimSpace(ip)] = 0
	}

	conn, err := amqp091.Dial(config.MQURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "连接 MQ 失败:", err)
		os.Exit(1)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		fmt.Fprintln(os.Stderr, "打开 Channel 失败:", err)
		os.Exit(1)
	}
	defer ch.Close()

	deadline := time.Now().Add(*dur)
	for time.Now().Before(deadline) && !allDone(want, *maxPer) {
		var held []amqp091.Delivery
		roundEnd := time.Now().Add(*window)
		for time.Now().Before(roundEnd) && len(held) < 30 {
			d, ok, err := ch.Get(config.QueueCCDData, false)
			if err != nil {
				fmt.Fprintln(os.Stderr, "Get 失败:", err)
				break
			}
			if !ok {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			held = append(held, d)
			ip := gjson.GetBytes(d.Body, "IP").String()
			if n, hit := want[ip]; hit && n < *maxPer {
				want[ip]++
				fmt.Printf("---- ip=%s  %s ----\n%s\n", ip, time.Now().Format("15:04:05"), d.Body)
			}
		}
		// 放回队列，让生产消费者继续处理
		for i := len(held) - 1; i >= 0; i-- {
			_ = held[i].Nack(false, true)
		}
	}
	fmt.Println("---- 结束，已打印:", want, "----")
}

func allDone(want map[string]int, max int) bool {
	for _, n := range want {
		if n < max {
			return false
		}
	}
	return true
}
