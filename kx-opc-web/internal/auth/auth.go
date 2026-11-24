// Package auth 是账户登录：账户写在 config.yaml 里，登录后发一个签名的 Cookie（用户名 + 过期时间 + HMAC），服务端不存会话。
// 签名密钥由全部账户的用户名、密码、角色算出：改了密码或删了账户，之前发出的 Cookie 自动失效；重启程序不用重新登录。
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"

	"kx-opc-web/internal/config"
)

// CookieName 是登录 Cookie 的名字。
const CookieName = "kx_session"

// Session 是当前请求的登录用户。
type Session struct {
	User string `json:"user"`
	Role string `json:"role"`
}

// Customer 返回是否是客户账户（只能看上位机数据、上位机记录）。
func (s Session) Customer() bool { return s.Role == config.RoleCustomer }

// Auth 校验账户和登录 Cookie，并发安全（创建后只读）。
type Auth struct {
	accounts map[string]config.Account
	key      []byte
	ttl      time.Duration
}

// New 创建登录校验；accounts 为空时不需要登录。
func New(accounts []config.Account, ttl time.Duration) *Auth {
	a := &Auth{accounts: map[string]config.Account{}, ttl: ttl}
	h := sha256.New()
	h.Write([]byte("kx-opc-web session\x00"))
	for _, acc := range accounts {
		a.accounts[acc.User] = acc
		h.Write([]byte(acc.User + "\x00" + acc.Password + "\x00" + acc.Role + "\x00"))
	}
	a.key = h.Sum(nil)
	return a
}

// Enabled 返回是否需要登录。
func (a *Auth) Enabled() bool { return len(a.accounts) > 0 }

// TTL 返回登录有效期。
func (a *Auth) TTL() time.Duration { return a.ttl }

// Login 校验用户名密码，成功时返回会话和 Cookie 的值。
func (a *Auth) Login(user, password string, now time.Time) (Session, string, bool) {
	acc, ok := a.accounts[user]
	// 用户不存在时也比较一次，响应时间不暴露用户名是否存在
	want := acc.Password
	if !ok {
		want = "\x00"
	}
	if subtle.ConstantTimeCompare([]byte(password), []byte(want)) != 1 || !ok {
		return Session{}, "", false
	}
	exp := strconv.FormatInt(now.Add(a.ttl).Unix(), 10)
	payload := base64.RawURLEncoding.EncodeToString([]byte(user)) + "." + exp
	return Session{User: user, Role: acc.Role}, payload + "." + a.sign(payload), true
}

// Parse 校验 Cookie 的值，返回会话；角色按当前配置（改了角色立即生效）。
func (a *Auth) Parse(token string, now time.Time) (Session, bool) {
	payload, sig, ok := cutLast(token, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(a.sign(payload))) {
		return Session{}, false
	}
	userB64, expStr, ok := strings.Cut(payload, ".")
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if !ok || err != nil || now.Unix() >= exp {
		return Session{}, false
	}
	user, err := base64.RawURLEncoding.DecodeString(userB64)
	if err != nil {
		return Session{}, false
	}
	acc, ok := a.accounts[string(user)]
	if !ok {
		return Session{}, false
	}
	return Session{User: acc.User, Role: acc.Role}, true
}

// FromRequest 从请求的 Cookie 取会话；不需要登录时为内部账户。
func (a *Auth) FromRequest(r *http.Request) (Session, bool) {
	if !a.Enabled() {
		return Session{Role: config.RoleAdmin}, true
	}
	c, err := r.Cookie(CookieName)
	if err != nil {
		return Session{}, false
	}
	return a.Parse(c.Value, time.Now())
}

func (a *Auth) sign(payload string) string {
	m := hmac.New(sha256.New, a.key)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func cutLast(s, sep string) (string, string, bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

type ctxKey struct{}

// WithSession 把会话放进 context，接口里用 FromContext 取。
func WithSession(ctx context.Context, s Session) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

// FromContext 取当前请求的会话（中间件已校验过）。
func FromContext(ctx context.Context) Session {
	s, _ := ctx.Value(ctxKey{}).(Session)
	return s
}
