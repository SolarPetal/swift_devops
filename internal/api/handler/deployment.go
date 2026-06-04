package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	apperr "swift-devops/internal/pkg/errors"
	"swift-devops/internal/service"
)

// DeploymentHandler 处理 App×Host 绑定关系
type DeploymentHandler struct {
	svc *service.DeploymentService
}

func NewDeploymentHandler(svc *service.DeploymentService) *DeploymentHandler {
	return &DeploymentHandler{svc: svc}
}

// Bind POST /apps/:id/hosts
func (h *DeploymentHandler) Bind(c *gin.Context) {
	appID, ok := parseID(c)
	if !ok {
		return
	}
	var in service.DeploymentInput
	if err := c.ShouldBindJSON(&in); err != nil {
		apperr.Respond(c, apperr.Wrap(err, "BAD_REQUEST", err.Error(), http.StatusBadRequest))
		return
	}
	out, err := h.svc.Bind(appID, in)
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusCreated, out)
}

// ListByApp GET /apps/:id/hosts
func (h *DeploymentHandler) ListByApp(c *gin.Context) {
	appID, ok := parseID(c)
	if !ok {
		return
	}
	out, err := h.svc.ListByApp(appID)
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

// CheckRuntimeByApp POST /apps/:id/hosts/runtime-check
func (h *DeploymentHandler) CheckRuntimeByApp(c *gin.Context) {
	appID, ok := parseID(c)
	if !ok {
		return
	}
	out, err := h.svc.SyncRuntimeByApp(c.Request.Context(), appID)
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

// Unbind DELETE /deployments/:id
func (h *DeploymentHandler) Unbind(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	if err := h.svc.Unbind(id); err != nil {
		apperr.Respond(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// RuntimeLogs GET /deployments/:id/runtime-logs?lines=300&timestamps=1
func (h *DeploymentHandler) RuntimeLogs(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	lines, _ := strconv.Atoi(c.DefaultQuery("lines", "300"))
	timestamps := c.Query("timestamps") == "1" || c.Query("timestamps") == "true"
	out, err := h.svc.RuntimeLogs(c.Request.Context(), id, service.DeploymentRuntimeLogsInput{
		Lines:      lines,
		Timestamps: timestamps,
	})
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// StopRuntime POST /deployments/:id/runtime/stop
func (h *DeploymentHandler) StopRuntime(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	out, err := h.svc.StopRuntime(c.Request.Context(), id)
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// RestartRuntime POST /deployments/:id/runtime/restart
func (h *DeploymentHandler) RestartRuntime(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	out, err := h.svc.RestartRuntime(c.Request.Context(), id)
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}
