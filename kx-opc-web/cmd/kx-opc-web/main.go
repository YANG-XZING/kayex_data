// kx-opc-web：OPC 服务可视化网站。炉台实时数据、实时 / 历史曲线、通讯预警、全量数据导出、上位机记录，数据都来自 TDengine。
// 内部账户看全部功能；客户账户只看上位机数据（管理员勾选的点位）和上位机记录。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"

	"kx-opc-web/internal/alarm"
	"kx-opc-web/internal/auth"
	"kx-opc-web/internal/clock"
	"kx-opc-web/internal/config"
	"kx-opc-web/internal/crystal"
	"kx-opc-web/internal/extract"
	"kx-opc-web/internal/history"
	"kx-opc-web/internal/hmi"
	"kx-opc-web/internal/monitor"
	"kx-opc-web/internal/points"
	"kx-opc-web/internal/realtime"
	"kx-opc-web/internal/td"
	"kx-opc-web/internal/web"
)

// version 在构建时通过 -ldflags "-X main.version=..." 注入
var version = "dev"

// 日志文件滚动参数
const (
	logMaxSizeMB  = 100
	logMaxBackups = 30
	logMaxAgeDays = 30

	shutdownTimeout = 10 * time.Second
)

func main() {
	configPath := flag.String("config", defaultConfigPath(), "配置文件路径")
	showVersion := flag.Bool("version", false, "打印版本号")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(2)
	}
	log, closeLog := newLogger(cfg.Log)
	defer closeLog()
	log.Info("kx-opc-web 启动", "version", version, "config", *configPath, "listen", cfg.Listen, "tdengine", cfg.TDengine.Address())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cfg, log); err != nil {
		log.Error("运行失败", "error", err)
		os.Exit(1)
	}
	log.Info("kx-opc-web 已停止")
}

func run(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	db, err := td.Open(cfg.TDengine)
	if err != nil {
		return err
	}
	defer db.Close()

	clk := clock.New(cfg.Clock, log)
	mon := monitor.New(cfg.Monitor, cfg.AreaOf, db, clk, log)
	hub := realtime.NewHub(db, mon, cfg.Realtime.PollInterval, log)
	pts := points.New(db, clk.Now, log)
	limits := history.Limits{MaxRange: cfg.History.MaxRange, MaxTags: cfg.History.MaxTags}
	hist := history.New(db, limits)
	alarms := alarm.New(db, cfg.Alarm.SkewConfirm, cfg.Alarm.Record, log)
	extractCache, err := extract.NewCache(cfg.Extract.CacheDir, cfg.Extract.CacheTTL, cfg.Extract.CacheMax, log)
	if err != nil {
		return err
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Minute):
				extractCache.Sweep()
			}
		}
	}()
	mon.SetObserver(alarms.Observe)
	go func() {
		db.EnsureTables(ctx, log) // 表建好后再读点位信息
		pts.Run(ctx)
	}()
	go clk.Run(ctx)
	go mon.Run(ctx)
	go hub.Run(ctx)

	handler := (&web.Server{
		DB: db, Clock: clk, Monitor: mon, Hub: hub, Points: pts, History: hist, Limits: limits, TableMaxRange: cfg.History.TableMaxRange,
		HeartColumn: cfg.History.HeartColumn,
		Alarms:      alarms, AlarmMaxRange: cfg.Alarm.HistoryMaxRange, SkewLimit: cfg.Monitor.ClockSkewLimit,
		Extract: extract.New(db, cfg.Extract), ExtractCache: extractCache, ExtractCfg: cfg.Extract, Areas: cfg.Areas,
		Crystal: crystal.New(db, cfg.Crystal, cfg.Monitor), CrystalCfg: cfg.Crystal,
		Auth: auth.New(cfg.Accounts, cfg.SessionTTL), HMI: hmi.New(db, cfg.HMI), Log: log, Version: version,
	}).Handler()
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()

	select {
	case err := <-errc:
		return fmt.Errorf("监听 %s: %w", cfg.Listen, err)
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}

// defaultConfigPath 默认读取程序同目录下的 config.yaml。
func defaultConfigPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "config.yaml"
	}
	return filepath.Join(filepath.Dir(exe), "config.yaml")
}

func newLogger(cfg config.Log) (*slog.Logger, func()) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.ToUpper(cfg.Level))); err != nil {
		level = slog.LevelInfo
	}
	var out io.Writer = os.Stdout
	closeLog := func() {}
	if cfg.File != "" {
		file := &lumberjack.Logger{Filename: cfg.File, MaxSize: logMaxSizeMB, MaxBackups: logMaxBackups, MaxAge: logMaxAgeDays, LocalTime: true}
		out = io.MultiWriter(os.Stdout, file)
		closeLog = func() { _ = file.Close() }
	}
	return slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: level})), closeLog
}
