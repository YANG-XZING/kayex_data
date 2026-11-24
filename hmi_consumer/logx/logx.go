// Package logx 是一个零依赖的轻量分级日志封装，基于标准库 log。
//
//	级别(severity)：DEBUG < INFO < WARN < ERROR，可通过 LOG_LEVEL 环境变量过滤；
//	标签(tag)     ：事件类型/模块，固定一小组：SYS / MQ / CONSUMER / DB / TAG / WRITE / CLEAN。
//
// 统一输出格式： <时间> <级别> [<标签>] <消息>
package logx

import (
	"fmt"
	"log"
	"os"
	"strings"
)

// Level 日志级别
type Level int

const (
	DEBUG Level = iota
	INFO
	WARN
	ERROR
)

// 级别名统一补齐为 5 字符，保证列对齐、便于肉眼扫读与 grep
var levelNames = map[Level]string{
	DEBUG: "DEBUG",
	INFO:  "INFO ",
	WARN:  "WARN ",
	ERROR: "ERROR",
}

// currentLevel 全局级别阈值，低于它的日志被丢弃。默认 INFO。
var currentLevel = INFO

// SetLevel 手动设置全局日志级别
func SetLevel(l Level) { currentLevel = l }

// SetLevelFromEnv 从环境变量 LOG_LEVEL 读取级别(DEBUG/INFO/WARN/ERROR)，未设置或非法则保持默认 INFO。
// 生产环境可设为 WARN 以屏蔽 INFO 级别的常规信息。
func SetLevelFromEnv() {
	switch strings.ToUpper(strings.TrimSpace(os.Getenv("LOG_LEVEL"))) {
	case "DEBUG":
		currentLevel = DEBUG
	case "INFO":
		currentLevel = INFO
	case "WARN", "WARNING":
		currentLevel = WARN
	case "ERROR":
		currentLevel = ERROR
	}
}

// output 是所有分级函数的统一出口
func output(l Level, tag, format string, args ...any) {
	if l < currentLevel {
		return
	}
	msg := fmt.Sprintf(format, args...)
	log.Printf("%s [%s] %s", levelNames[l], tag, msg)
}

// Debugf 调试信息（默认不输出，需 LOG_LEVEL=DEBUG）
func Debugf(tag, format string, args ...any) { output(DEBUG, tag, format, args...) }

// Infof 常规信息（启动、连接、生命周期等）
func Infof(tag, format string, args ...any) { output(INFO, tag, format, args...) }

// Warnf 警告（可自愈或被吞掉的异常，如脏数据丢弃、标签更新失败）
func Warnf(tag, format string, args ...any) { output(WARN, tag, format, args...) }

// Errorf 错误（连接失败、写库失败、DB 不可达等）
func Errorf(tag, format string, args ...any) { output(ERROR, tag, format, args...) }

// Fatalf 打印 ERROR 级别日志后退出进程（仅用于启动期不可恢复的错误）
func Fatalf(tag, format string, args ...any) {
	output(ERROR, tag, format, args...)
	os.Exit(1)
}
