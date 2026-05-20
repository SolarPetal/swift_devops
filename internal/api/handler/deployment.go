package handler

import (
	"net/http"

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

// UpdateGroup PATCH /deployments/:id  body: {"group_tag":"blue"}
func (h *DeploymentHandler) UpdateGroup(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var body struct {
		GroupTag string `json:"group_tag"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		apperr.Respond(c, apperr.Wrap(err, "BAD_REQUEST", err.Error(), http.StatusBadRequest))
		return
	}
	if err := h.svc.UpdateGroup(id, body.GroupTag); err != nil {
		apperr.Respond(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
