package ws

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Upgrader gin/net.http handler 用的 ws 升级器封装。
// CheckOrigin 默认放行同源；前后端跨域走 nginx 时也兼容。
type Upgrader struct {
	u websocket.Upgrader
}

// NewUpgrader 默认 16KiB 读写缓冲，允许 origin 同源或匹配 allowedOrigins 任一前缀。
// allowedOrigins 留空表示不做 origin 检查（仅适合内网/受信前端）。
func NewUpgrader(allowedOrigins []string) *Upgrader {
	return &Upgrader{u: websocket.Upgrader{
		ReadBufferSize:  16 * 1024,
		WriteBufferSize: 16 * 1024,
		CheckOrigin: func(r *http.Request) bool {
			if len(allowedOrigins) == 0 {
				return true
			}
			origin := r.Header.Get("Origin")
			for _, o := range allowedOrigins {
				if strings.HasPrefix(origin, o) {
					return true
				}
			}
			return false
		},
	}}
}

// Upgrade 把 http 请求升级成 ws 连接。失败时 upgrader 已写回错误响应。
func (u *Upgrader) Upgrade(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	return u.u.Upgrade(w, r, nil)
}

// Conn 包装 websocket.Conn，提供心跳 + send 队列。
// 读端在独立 goroutine 跑，遇到任何错误关闭连接；写端串行从 send 取数据，避免并发写 panic。
type Conn struct {
	c        *websocket.Conn
	send     chan []byte
	close    sync.Once
	closeCh  chan struct{}
	pingEach time.Duration
	readWait time.Duration
}

// NewConn 包装一条已升级的 ws 连接。send 缓冲建议 32。
func NewConn(c *websocket.Conn, sendBuf int) *Conn {
	if sendBuf <= 0 {
		sendBuf = 32
	}
	return &Conn{
		c:        c,
		send:     make(chan []byte, sendBuf),
		closeCh:  make(chan struct{}),
		pingEach: defaultPingInterval,
		readWait: defaultReadDeadline,
	}
}

// Send 推一条消息（非阻塞）。满了立即丢，返回 false。
func (c *Conn) Send(msg []byte) bool {
	select {
	case c.send <- msg:
		return true
	case <-c.closeCh:
		return false
	default:
		return false
	}
}

// Close 关闭连接。幂等。
func (c *Conn) Close() {
	c.close.Do(func() {
		close(c.closeCh)
		_ = c.c.Close()
	})
}

// Done 返回连接已关闭的 channel；调用方用来挂起 handler。
func (c *Conn) Done() <-chan struct{} { return c.closeCh }

// Run 启动写 pump 与读 pump（读 pump 仅维持心跳 / 探测断线）。
// 内部 ctx 控制：ctx 取消会 graceful close 连接。
func (c *Conn) Run(ctx context.Context) {
	// pong 处理：每收到 pong 推迟读 deadline
	c.c.SetReadDeadline(time.Now().Add(c.readWait))
	c.c.SetPongHandler(func(string) error {
		c.c.SetReadDeadline(time.Now().Add(c.readWait))
		return nil
	})

	go c.readPump()
	go c.writePump(ctx)
}

func (c *Conn) readPump() {
	defer c.Close()
	for {
		// 我们不消费业务消息，只检测断线和 pong
		if _, _, err := c.c.ReadMessage(); err != nil {
			return
		}
	}
}

func (c *Conn) writePump(ctx context.Context) {
	ticker := time.NewTicker(c.pingEach)
	defer func() {
		ticker.Stop()
		c.Close()
	}()
	for {
		select {
		case <-ctx.Done():
			_ = c.c.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutdown"),
				time.Now().Add(defaultWriteDeadline))
			return
		case <-c.closeCh:
			return
		case msg, ok := <-c.send:
			if !ok {
				return
			}
			c.c.SetWriteDeadline(time.Now().Add(defaultWriteDeadline))
			if err := c.c.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			c.c.SetWriteDeadline(time.Now().Add(defaultWriteDeadline))
			if err := c.c.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// ErrTicketInvalid ticket 校验失败的统一错误。
var ErrTicketInvalid = errors.New("ws ticket invalid or expired")
