package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	apperr "swift-devops/internal/pkg/errors"
	"swift-devops/internal/service"
)

// BuildHandler 构建任务的 HTTP 入口（Sprint 5.3）。
type BuildHandler struct {
	svc *service.BuildService
}

func NewBuildHandler(svc *service.BuildService) *BuildHandler {
	return &BuildHandler{svc: svc}
}

// Trigger POST /apps/:id/build  body: {"git_ref","mvn_args","cred_id"}
func (h *BuildHandler) Trigger(c *gin.Context) {
	appID, ok := parseID(c)
	if !ok {
		return
	}
	var in service.BuildTriggerInput
	if err := c.ShouldBindJSON(&in); err != nil {
		// 允许空 body：JSON 解析失败但 body 实际为空时不报错
		if err.Error() != "EOF" {
			apperr.Respond(c, apperr.Wrap(err, "BAD_REQUEST", err.Error(), http.StatusBadRequest))
			return
		}
	}
	actor := "unknown"
	if v, ok := c.Get("user"); ok {
		if s, ok := v.(string); ok {
			actor = s
		}
	}
	out, err := h.svc.Trigger(appID, actor, in)
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusAccepted, out)
}

// List GET /builds?app_id=N
func (h *BuildHandler) List(c *gin.Context) {
	var appID uint
	if s := c.Query("app_id"); s != "" {
		n, err := strconv.ParseUint(s, 10, 32)
		if err != nil {
			apperr.Respond(c, apperr.New("BAD_REQUEST", "app_id 必须是整数", http.StatusBadRequest))
			return
		}
		appID = uint(n)
	}
	out, err := h.svc.List(appID)
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

// Get GET /builds/:id
func (h *BuildHandler) Get(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	out, err := h.svc.Get(id)
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// GetLog GET /builds/:id/log → text/plain 全文
func (h *BuildHandler) GetLog(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	log, err := h.svc.GetLog(id)
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.String(http.StatusOK, log)
}
