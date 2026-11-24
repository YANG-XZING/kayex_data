// Package web 提供 HTTP 接口和内嵌的前端页面。
package web

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

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
)

// dist 是前端打包结果（web/ 下 npm run build 输出到这里）
//
//go:embed all:dist
var dist embed.FS

const apiTimeout = 15 * time.Second

// Server 汇总各模块，注册路由。
type Server struct {
	DB      *td.DB
	Clock   *clock.Clock
	Monitor *monitor.Monitor
	Hub     *realtime.Hub
	Points  *points.Store
	History *history.Service
	Limits  history.Limits
	// TableMaxRange 历史数据表格（分页）和导出 CSV 一次最多查多长时间
	TableMaxRange time.Duration
	// HeartColumn OPC 导出 CSV 时加在“时间”后面的上位机心跳时间列，为空不加
	HeartColumn string
	Alarms      *alarm.Engine
	// Extract、ExtractCache 原 TDengine 取数网站的功能
	Extract      *extract.Service
	ExtractCache *extract.Cache
	ExtractCfg   config.Extract
	Areas        []config.Area // 网段前缀与区号，取数页的对照表
	// Crystal 晶棒数据导出
	Crystal    *crystal.Service
	CrystalCfg config.Crystal
	// SkewLimit 上位机时钟与基准时间相差超过它算时间不一致，页面上显示这个阈值
	SkewLimit time.Duration
	// AlarmMaxRange 告警记录一次最多查多长时间
	AlarmMaxRange time.Duration
	// Auth 账户登录；客户账户只能看上位机数据（客户可见的点位）和上位机记录
	Auth *auth.Auth
	// HMI 上位机记录（kayex_data 的操作记录、报警记录）
	HMI     *hmi.Service
	Log     *slog.Logger
	Version string
}

// Handler 返回全部路由。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/furnaces", s.furnaces)
	mux.HandleFunc("GET /api/columns", s.columns)
	mux.HandleFunc("GET /api/clock", s.clock)
	mux.HandleFunc("GET /ws", s.ws)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("GET /api/me", s.me)

	mux.HandleFunc("GET /api/settings", s.settings)
	mux.HandleFunc("GET /api/history", s.historyQuery)
	mux.HandleFunc("GET /api/history/page", s.historyPage)
	mux.HandleFunc("GET /api/export/page", s.exportPage)
	mux.HandleFunc("GET /api/history/export", s.historyExport)
	mux.HandleFunc("GET /api/hmi/records", s.hmiRecords)
	mux.HandleFunc("GET /api/hmi/records/colors", s.hmiRecordColors)
	mux.HandleFunc("GET /api/hmi/records/export", s.hmiRecordsExport)
	mux.HandleFunc("GET /api/alarms/active", s.alarmsActive)
	mux.HandleFunc("GET /api/alarms", s.alarmsHistory)
	s.extractRoutes(mux)
	s.crystalRoutes(mux)

	mux.HandleFunc("GET /api/points", s.pointsGet)
	mux.HandleFunc("GET /api/points/export", s.pointsExport)
	mux.HandleFunc("POST /api/points/import", adminOnly(s.pointsImport))
	mux.HandleFunc("POST /api/points/customer", adminOnly(s.pointsCustomer))

	static, _ := fs.Sub(dist, "dist")
	files := http.FileServerFS(static)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			// 文件名带内容哈希，可以长期缓存
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
	return s.recoverer(s.guard(mux))
}

func (s *Server) furnaces(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Monitor.Snapshot())
}

func (s *Server) columns(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), apiTimeout)
	defer cancel()
	cols, err := s.DB.Columns(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, "读取点位列表失败: "+err.Error())
		return
	}
	if allow := s.visible(r); allow != nil { // 客户账户只看客户可见的点位
		cols = slices.DeleteFunc(slices.Clone(cols), func(c td.Column) bool { return !allow(c.Name) }) // Columns 返回的是缓存，不能原地改
	}
	type column struct {
		td.Column
		Numeric bool `json:"numeric"`
	}
	out := make([]column, len(cols))
	for i, c := range cols {
		out[i] = column{Column: c, Numeric: c.Numeric()}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) clock(w http.ResponseWriter, r *http.Request) {
	snap := s.Monitor.Snapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"reference":    s.Clock.Status(),
		"now":          s.Clock.Now().UnixMilli(),
		"td_offset_ms": snap.TDOffsetMs,
		"version":      s.Version,
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.Log.Error("请求处理异常", "path", r.URL.Path, "panic", v)
				writeError(w, http.StatusInternalServerError, "服务器内部错误")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
