package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	apperr "swift-devops/internal/pkg/errors"
	wspkg "swift-devops/internal/pkg/ws"
)

// WSTicketHandler 颁发一次性 WebSocket 升级 ticket。
//
// 流程：前端 JWT 调 POST /api/v1/ws-tickets {subject:"pipeline:5"}，拿 ticket 后
// 立刻发 WS 升级。ticket 5s 后过期 + 一次性消费 + subject 强匹配。
type WSTicketHandler struct {
	store *wspkg.TicketStore
}

func NewWSTicketHandler(store *wspkg.TicketStore) *WSTicketHandler {
	return &WSTicketHandler{store: store}
}

// 允许的 subject 前缀白名单——挡住客户端瞎传 subject 当作越权探测的手段。
// 后续业务 stream 类型加进来：build:/metrics:/log: 等。
var allowedSubjectPrefixes = []string{
	"pipeline:", // 流水线步骤
	"build:",    // 构建日志
	"metrics:",  // 监控指标
	"log:",      // tail -f 日志
}

type wsTicketReq struct {
	Subject string `json:"subject" binding:"required"`
}

type wsTicketResp struct {
	Ticket    string `json:"ticket"`
	ExpiresAt string `json:"expires_at"`
}

// Issue POST /api/v1/ws-tickets
func (h *WSTicketHandler) Issue(c *gin.Context) {
	var in wsTicketReq
	if err := c.ShouldBindJSON(&in); err != nil {
		apperr.Respond(c, apperr.New("BAD_REQUEST", "subject 必填", http.StatusBadRequest))
		return
	}
	subject := strings.TrimSpace(in.Subject)
	if !subjectAllowed(subject) {
		apperr.Respond(c, apperr.New("BAD_REQUEST",
			"subject 必须以 pipeline:/build:/metrics:/log: 开头", http.StatusBadRequest))
		return
	}

	user := "anonymous"
	if v, ok := c.Get("user"); ok {
		if s, ok := v.(string); ok && s != "" {
			user = s
		}
	}

	ticket, exp, err := h.store.Issue(user, subject)
	if err != nil {
		apperr.Respond(c, apperr.Wrap(err, "INTERNAL", "issue ws ticket", http.StatusInternalServerError))
		return
	}
	c.JSON(http.StatusOK, wsTicketResp{
		Ticket:    ticket,
		ExpiresAt: exp.UTC().Format(time.RFC3339),
	})
}

func subjectAllowed(subject string) bool {
	for _, p := range allowedSubjectPrefixes {
		if strings.HasPrefix(subject, p) && len(subject) > len(p) {
			return true
		}
	}
	return false
}
