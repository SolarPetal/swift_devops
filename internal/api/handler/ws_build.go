package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	apperr "swift-devops/internal/pkg/errors"
	wspkg "swift-devops/internal/pkg/ws"
	"swift-devops/internal/service"
)

// BuildWSHandler 推送一次构建的实时日志（Sprint 5.5）。
//
// 路径：GET /api/v1/ws/builds/:id?ticket=<one-time>
// 流程：Consume ticket → 确认 build 存在 → 升级 → 先发 snapshot（全量日志）→ 转发 hub 增量直到断开。
// 与 ws_pipeline.go 同构，仅 topic / snapshot 来源不同。
type BuildWSHandler struct {
	svc      *service.BuildService
	tickets  *wspkg.TicketStore
	hub      *wspkg.Hub
	upgrader *wspkg.Upgrader
}

func NewBuildWSHandler(svc *service.BuildService, tickets *wspkg.TicketStore, hub *wspkg.Hub, upgrader *wspkg.Upgrader) *BuildWSHandler {
	return &BuildWSHandler{svc: svc, tickets: tickets, hub: hub, upgrader: upgrader}
}

func (h *BuildWSHandler) Stream(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	subject := service.BuildTopic(id) // "build:<id>"

	// 1. 一次性 ticket 鉴权（浏览器原生 WS 不带 header）
	ticket := c.Query("ticket")
	if ticket == "" {
		apperr.Respond(c, apperr.New("UNAUTHORIZED", "ticket 必填", http.StatusUnauthorized))
		return
	}
	if _, valid := h.tickets.Consume(ticket, subject); !valid {
		apperr.Respond(c, apperr.New("UNAUTHORIZED", "ticket 无效或已过期", http.StatusUnauthorized))
		return
	}

	// 2. 升级前确认 build 存在
	if _, err := h.svc.Get(id); err != nil {
		apperr.Respond(c, err)
		return
	}

	// 3. 升级
	conn, err := h.upgrader.Upgrade(c.Writer, c.Request)
	if err != nil {
		return
	}
	wc := wspkg.NewConn(conn, 64)

	// 4. 先订阅再发 snapshot——避免 snapshot 与首条增量之间漏帧
	recv, cancel := h.hub.Subscribe(subject, 64)
	defer cancel()

	// 5. 发 snapshot 让晚到的客户端拿到全量日志
	if data := h.svc.SnapshotLogBytes(id); data != nil {
		wc.Send(data)
	}

	wc.Run(c.Request.Context())

	// 6. 转发 hub 增量到 ws
	go func() {
		for msg := range recv {
			if !wc.Send(msg) {
				// 客户端跟不上时丢帧；snapshot 仍能让其追上全量
			}
		}
	}()

	<-wc.Done()
}
