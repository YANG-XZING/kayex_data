package main

import (
	"os"
	"strconv"
	"time"
)

var (
	MQURL           = deploymentString("MQ_URL", "amqp://admin:rabbitmq@10.65.104.172:5672/")
	TaosDSN         = deploymentString("TD_DSN", "root:taosdata@ws(10.65.104.171:6041)/kayex_data")
	NormalQueueName = deploymentString("PLC_QUEUE", "dbcenter.plc.direct")
	PatchQueueName  = deploymentString("PLC_PATCH_QUEUE", "dbcenter.plc.sqlite")
)

func deploymentString(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func deploymentInt(key string, fallback int) int {
	if value := os.Getenv(key); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			panic(key + " must be a positive integer")
		}
		return n
	}
	return fallback
}
func deploymentDuration(key string, fallback time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		d, err := time.ParseDuration(value)
		if err != nil || d <= 0 {
			panic(key + " must be a positive duration")
		}
		return d
	}
	return fallback
}
