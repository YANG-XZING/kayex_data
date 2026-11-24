package realtime

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	sendBuffer   = 64
	writeTimeout = 10 * time.Second
	pingInterval = 30 * time.Second
	readTimeout  = 2 * pingInterval
	maxMessage   = 16 * 1024
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:    4096,
	WriteBufferSize:   16 * 1024,
	EnableCompression: true,
	// 只推送只读数据，内网使用，允许开发时经 Vite 代理跨源连接
	CheckOrigin: func(*http.Request) bool { return true },
}

// clientMessage 是前端发来的指令：{"op":"sub","tables":["opc_10_65_111_1"]}，tables 为空即全部退订。
type clientMessage struct {
	Op     string   `json:"op"`
	Tables []string `json:"tables"`
}

// conn 是一个浏览器连接。发送队列满了（浏览器太慢）就断开，由前端自动重连，避免拖慢推送。
type conn struct {
	ws     *websocket.Conn
	out    chan []byte
	closed chan struct{}
	tables []string              // 当前订阅，由 Hub 在持锁时读写
	allow  func(col string) bool // 只能看部分点位时（客户账户）的过滤，nil 为全部点位
}

// ServeWS 处理 /ws 连接；allow 不为 nil 时只推送它允许的点位。
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request, allow func(col string) bool) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade 已经写回了错误响应
	}
	ws.SetReadLimit(maxMessage)
	c := &conn{ws: ws, out: make(chan []byte, sendBuffer), closed: make(chan struct{}), allow: allow}
	go c.writeLoop()
	defer func() {
		h.unsubscribeAll(c)
		close(c.closed)
		ws.Close()
	}()

	_ = ws.SetReadDeadline(time.Now().Add(readTimeout))
	ws.SetPongHandler(func(string) error { return ws.SetReadDeadline(time.Now().Add(readTimeout)) })
	for {
		var msg clientMessage
		if err := ws.ReadJSON(&msg); err != nil {
			return
		}
		_ = ws.SetReadDeadline(time.Now().Add(readTimeout))
		if msg.Op == "sub" {
			if ignored := h.subscribe(c, msg.Tables); len(ignored) > 0 {
				c.sendJSON(map[string]any{"t": "error", "msg": "以下炉台不存在或超出订阅上限", "tables": ignored})
			}
		}
	}
}

// send 非阻塞地放入发送队列，队列满则断开连接。
func (c *conn) send(data []byte) {
	select {
	case <-c.closed:
	case c.out <- data:
	default:
		_ = c.ws.Close() // 读循环随之退出并清理订阅
	}
}

// sendRow 推送一行：data 是已编码好的 msg（全部点位）；只能看部分点位的连接按自己的过滤重新编码
func (c *conn) sendRow(msg rowMessage, data []byte) {
	if c.allow == nil && data != nil {
		c.send(data)
		return
	}
	if c.allow != nil {
		v := make(map[string]any, len(msg.Value))
		for k, x := range msg.Value {
			if c.allow(k) {
				v[k] = x
			}
		}
		msg.Value = v
	}
	c.sendJSON(msg)
}

func (c *conn) sendJSON(v any) {
	if data, err := json.Marshal(v); err == nil {
		c.send(data)
	}
}

func (c *conn) writeLoop() {
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	for {
		select {
		case <-c.closed:
			return
		case data := <-c.out:
			_ = c.ws.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := c.ws.WriteMessage(websocket.TextMessage, data); err != nil {
				_ = c.ws.Close()
				return
			}
		case <-ping.C:
			_ = c.ws.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := c.ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				_ = c.ws.Close()
				return
			}
		}
	}
}
