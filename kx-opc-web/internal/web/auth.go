package web

import (
	"net/http"
	"strings"
	"time"

	"kx-opc-web/internal/auth"
	"kx-opc-web/internal/config"
)

// loginFailDelay 登录失败后多等一会儿再返回，减慢猜密码
const loginFailDelay = 500 * time.Millisecond

// customerAPI 是客户账户能调用的接口，其余接口返回 403。点位、实时推送、历史数据只给客户可见的点位（见各接口）
var customerAPI = map[string]bool{
	"GET /api/me": true, "POST /api/logout": true,
	"GET /api/furnaces": true, "GET /api/clock": true, "GET /api/settings": true,
	"GET /api/columns": true, "GET /api/points": true, "GET /ws": true,
	"GET /api/export/page": true, "GET /api/history/page": true, "GET /api/history/export": true, "GET /api/export": true,
	"GET /api/hmi/records": true, "GET /api/hmi/records/export": true, "GET /api/hmi/records/colors": true,
}

// guard 校验登录：接口和 /ws 要先登录（登录接口除外），客户账户只能调 customerAPI 里的接口；页面文件不校验（未登录时前端显示登录页）。
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if !strings.HasPrefix(path, "/api/") && path != "/ws" || path == "/api/login" {
			next.ServeHTTP(w, r)
			return
		}
		sess, ok := s.Auth.FromRequest(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "请先登录")
			return
		}
		if sess.Customer() && !customerAPI[r.Method+" "+path] {
			writeError(w, http.StatusForbidden, "当前账户没有权限")
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithSession(r.Context(), sess)))
	})
}

// adminOnly 只允许内部账户（guard 已拦住客户的大部分接口，这里用于客户可调用接口里的写操作等）。
func adminOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if auth.FromContext(r.Context()).Customer() {
			writeError(w, http.StatusForbidden, "当前账户没有权限")
			return
		}
		next(w, r)
	}
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	sess := auth.FromContext(r.Context())
	writeAccount(w, sess, s.Auth.Enabled())
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	sess, token, ok := s.Auth.Login(strings.TrimSpace(body.User), body.Password, time.Now())
	if !ok {
		time.Sleep(loginFailDelay)
		s.Log.Warn("登录失败", "user", body.User, "remote", r.RemoteAddr)
		writeError(w, http.StatusUnauthorized, "用户名或密码不正确")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: auth.CookieName, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		MaxAge: int(s.Auth.TTL().Seconds()),
	})
	s.Log.Info("登录", "user", sess.User, "role", sess.Role, "remote", r.RemoteAddr)
	writeAccount(w, sess, true)
}

func canViewAllHMI(sess auth.Session) bool {
	return sess.User == "KayexAdmin" && sess.Role == config.RoleAdmin
}

func writeAccount(w http.ResponseWriter, sess auth.Session, enabled bool) {
	writeJSON(w, http.StatusOK, map[string]any{
		"user": sess.User, "role": sess.Role, "auth": enabled, "hmi_all": canViewAllHMI(sess),
	})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: auth.CookieName, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// visible 返回当前账户能看到的点位过滤：内部账户为 nil（全部），客户为客户可见的点位。
func (s *Server) visible(r *http.Request) func(col string) bool {
	if !auth.FromContext(r.Context()).Customer() {
		return nil
	}
	return s.Points.Customer
}

func (s *Server) ws(w http.ResponseWriter, r *http.Request) {
	s.Hub.ServeWS(w, r, s.visible(r))
}
