package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	apperr "swift-devops/internal/pkg/errors"
	wspkg "swift-devops/internal/pkg/ws"
	"swift-devops/internal/service"
)

// PipelineWSHandler 推送 pipeline run 的实时事件。
//
// 路径：GET /api/v1/ws/pipelines/:id?ticket=<one-time>
// 流程：Consume ticket → 升级 → 立即发 snapshot 帧 → 转发 hub 增量直到断开。
type PipelineWSHandler struct {
	svc      *service.PipelineService
	tickets  *wspkg.TicketStore
	hub      *wspkg.Hub
	upgrader *wspkg.Upgrader
}

func NewPipelineWSHandler(svc *service.PipelineService, tickets *wspkg.TicketStore, hub *wspkg.Hub, upgrader *wspkg.Upgrader) *PipelineWSHandler {
	return &PipelineWSHandler{svc: svc, tickets: tickets, hub: hub, upgrader: upgrader}
}

func (h *PipelineWSHandler) Stream(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	subject := service.PipelineTopic(id) // "pipeline:<id>"

	// 1. 一次性 ticket 鉴权（替代 JWT；浏览器原生 WS 不带 header）
	ticket := c.Query("ticket")
	if ticket == "" {
		apperr.Respond(c, apperr.New("UNAUTHORIZED", "ticket 必填", http.StatusUnauthorized))
		return
	}
	if _, valid := h.tickets.Consume(ticket, subject); !valid {
		apperr.Respond(c, apperr.New("UNAUTHORIZED", "ticket 无效或已过期", http.StatusUnauthorized))
		return
	}

	// 2. 升级前确认 run 存在；升级后再失败前端就只能看错位的 close frame
	if _, err := h.svc.Get(id); err != nil {
		apperr.Respond(c, err)
		return
	}

	// 3. 升级
	conn, err := h.upgrader.Upgrade(c.Writer, c.Request)
	if err != nil {
		// upgrader 已写回错误响应
		return
	}
	wc := wspkg.NewConn(conn, 64)

	// 4. 订阅 hub。先订阅再发 snapshot——避免 snapshot 与首条增量之间的窗口丢事件。
	recv, cancel := h.hub.Subscribe(subject, 64)
	defer cancel()

	// 5. 先发 snapshot 让晚到的客户端拿到全量
	if data := h.svc.SnapshotEventBytes(id); data != nil {
		wc.Send(data)
	}

	wc.Run(c.Request.Context())

	// 6. 转发 hub 消息到 ws
	go func() {
		for msg := range recv {
			if !wc.Send(msg) {
				// 客户端跟不上时丢帧；snapshot 仍然能让其追上全量
			}
		}
	}()

	// 阻塞直到连接关闭
	<-wc.Done()
}
