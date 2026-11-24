// Package clock 提供基准时间：服务器时钟不可信（晶品 171 慢约 4 分钟，且被虚拟化平台每分钟改写），
// 所以定期向 NTP（不通则用 HTTPS 响应头的 Date）取标准时间，之后用单调时钟推算，不受本机改时间影响。
// 不修改系统时钟。
package clock

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"kx-opc-web/internal/config"
)

const (
	ntpTimeout  = 3 * time.Second
	httpTimeout = 5 * time.Second
	ntpEpoch    = 2208988800 // 1900-01-01 到 1970-01-01 的秒数
	// retryInterval 一个来源都不通时，多久后重试
	retryInterval = time.Minute
)

// Status 是基准时间的状态，返回给前端显示。
type Status struct {
	Synced   bool      `json:"synced"`    // false：从没校准成功过，基准时间就是本机时间，偏差未知
	Source   string    `json:"source"`    // 最近一次校准的来源，如 ntp:ntp.aliyun.com
	SyncedAt time.Time `json:"synced_at"` // 最近一次校准成功的时刻（基准时间）
	// LocalOffsetMs 本机时钟比基准时间快多少毫秒（负数为慢）
	LocalOffsetMs int64  `json:"local_offset_ms"`
	Error         string `json:"error,omitempty"` // 最近一次校准失败的原因
}

// Clock 是基准时间，并发安全。
type Clock struct {
	cfg  config.Clock
	log  *slog.Logger
	http *http.Client

	mu       sync.RWMutex
	synced   bool
	ref      time.Time // 校准时刻的标准时间
	mono     time.Time // 校准时刻的本机时间（带单调时钟读数）
	source   string
	lastErr  string
	syncedAt time.Time
}

// New 创建基准时间；Run 之前 Now 等于本机时间。
func New(cfg config.Clock, log *slog.Logger) *Clock {
	return &Clock{cfg: cfg, log: log.With("module", "clock"), http: &http.Client{Timeout: httpTimeout}}
}

// Now 返回基准时间：校准时的标准时间 + 之后经过的单调时长。
func (c *Clock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.synced {
		return time.Now()
	}
	return c.ref.Add(time.Since(c.mono))
}

// Status 返回校准状态。
func (c *Clock) Status() Status {
	now := c.Now()
	c.mu.RLock()
	defer c.mu.RUnlock()
	return Status{
		Synced:        c.synced,
		Source:        c.source,
		SyncedAt:      c.syncedAt,
		LocalOffsetMs: time.Now().Sub(now).Milliseconds(),
		Error:         c.lastErr,
	}
}

// Run 立即校准一次，之后按周期校准，直到 ctx 结束。
func (c *Clock) Run(ctx context.Context) {
	for {
		wait := c.cfg.SyncInterval
		if err := c.sync(ctx); err != nil {
			wait = retryInterval
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (c *Clock) sync(ctx context.Context) error {
	var errs []error
	try := func(source string, measure func() (time.Time, time.Time, error)) bool {
		ref, mono, err := measure()
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", source, err))
			return false
		}
		c.mu.Lock()
		first := !c.synced
		c.synced, c.ref, c.mono, c.source, c.lastErr, c.syncedAt = true, ref, mono, source, "", ref
		c.mu.Unlock()
		offset := mono.Round(0).Sub(ref)
		if first {
			c.log.Info("基准时间已校准", "source", source, "local_offset", offset.Round(time.Millisecond))
		} else {
			c.log.Debug("基准时间已校准", "source", source, "local_offset", offset.Round(time.Millisecond))
		}
		return true
	}
	for _, server := range c.cfg.NTPServers {
		if try("ntp:"+server, func() (time.Time, time.Time, error) { return queryNTP(ctx, server) }) {
			return nil
		}
	}
	for _, url := range c.cfg.HTTPURLs {
		if try("http:"+url, func() (time.Time, time.Time, error) { return c.queryHTTP(ctx, url) }) {
			return nil
		}
	}
	err := errors.Join(errs...)
	if err == nil {
		err = errors.New("没有配置任何时间来源")
	}
	c.mu.Lock()
	c.lastErr = err.Error()
	synced := c.synced
	c.mu.Unlock()
	c.log.Warn("基准时间校准失败", "synced_before", synced, "error", err)
	return err
}

// queryNTP 按 SNTP 取一次时间，返回 (标准时间, 同一时刻的本机时间)。用往返时间的中点消除网络延迟。
func queryNTP(ctx context.Context, server string) (time.Time, time.Time, error) {
	var dialer net.Dialer
	dctx, cancel := context.WithTimeout(ctx, ntpTimeout)
	defer cancel()
	conn, err := dialer.DialContext(dctx, "udp", net.JoinHostPort(server, "123"))
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(ntpTimeout))

	req := make([]byte, 48)
	req[0] = 0x1b // LI=0, VN=3, Mode=3（客户端）
	t1 := time.Now()
	if _, err := conn.Write(req); err != nil {
		return time.Time{}, time.Time{}, err
	}
	resp := make([]byte, 48)
	n, err := conn.Read(resp)
	t4 := time.Now()
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if n < 48 || resp[0]&0x07 != 4 && resp[0]&0x07 != 5 {
		return time.Time{}, time.Time{}, errors.New("NTP 响应格式不对")
	}
	if stratum := resp[1]; stratum == 0 || stratum > 15 {
		return time.Time{}, time.Time{}, fmt.Errorf("NTP 服务器未同步（stratum=%d）", stratum)
	}
	t2 := ntpTime(resp[32:40]) // 服务器收到请求
	t3 := ntpTime(resp[40:48]) // 服务器发出响应
	// 标准时间对应本机的 t4：t3 + 回程时延，回程时延取 (往返 - 服务器处理) / 2
	rtt := t4.Sub(t1) - t3.Sub(t2)
	return t3.Add(rtt / 2), t4, nil
}

func ntpTime(b []byte) time.Time {
	sec := binary.BigEndian.Uint32(b[:4])
	frac := binary.BigEndian.Uint32(b[4:])
	nsec := (int64(frac) * 1e9) >> 32
	return time.Unix(int64(sec)-ntpEpoch, nsec)
}

// queryHTTP 用 HTTPS 响应头的 Date 取时间（精确到秒，判断分钟级偏差足够）。Date 是秒的整数部分，取 +0.5 秒作为期望值。
func (c *Clock) queryHTTP(ctx context.Context, url string) (time.Time, time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	t1 := time.Now()
	resp, err := c.http.Do(req)
	t4 := time.Now()
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	resp.Body.Close()
	date, err := http.ParseTime(resp.Header.Get("Date"))
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("响应头 Date 无法解析: %w", err)
	}
	mid := t1.Add(t4.Sub(t1) / 2)
	return date.Add(500 * time.Millisecond), mid, nil
}
