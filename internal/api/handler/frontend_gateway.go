package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	apperr "swift-devops/internal/pkg/errors"
	"swift-devops/internal/service"
)

// FrontendGatewayHandler 维护前端网关容器与域名路由。
type FrontendGatewayHandler struct {
	svc *service.FrontendGatewayService
}

func NewFrontendGatewayHandler(svc *service.FrontendGatewayService) *FrontendGatewayHandler {
	return &FrontendGatewayHandler{svc: svc}
}

func (h *FrontendGatewayHandler) EnsureGateway(c *gin.Context) {
	hostID, ok := parseID(c)
	if !ok {
		return
	}
	var in service.FrontendGatewayEnsureInput
	if err := c.ShouldBindJSON(&in); err != nil && err.Error() != "EOF" {
		apperr.Respond(c, apperr.Wrap(err, "BAD_REQUEST", err.Error(), http.StatusBadRequest))
		return
	}
	out, err := h.svc.Ensure(c.Request.Context(), hostID, in)
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *FrontendGatewayHandler) GetGateway(c *gin.Context) {
	hostID, ok := parseID(c)
	if !ok {
		return
	}
	out, err := h.svc.GetByHost(hostID)
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *FrontendGatewayHandler) ApplyGateway(c *gin.Context) {
	hostID, ok := parseID(c)
	if !ok {
		return
	}
	out, err := h.svc.Apply(c.Request.Context(), hostID, queryBool(c, "force_recreate"))
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *FrontendGatewayHandler) ListRoutes(c *gin.Context) {
	hostID, ok := parseID(c)
	if !ok {
		return
	}
	out, err := h.svc.ListRoutes(hostID)
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

func (h *FrontendGatewayHandler) CreateRoute(c *gin.Context) {
	hostID, ok := parseID(c)
	if !ok {
		return
	}
	var in service.FrontendGatewayRouteInput
	if err := c.ShouldBindJSON(&in); err != nil {
		apperr.Respond(c, apperr.Wrap(err, "BAD_REQUEST", err.Error(), http.StatusBadRequest))
		return
	}
	out, err := h.svc.CreateRoute(c.Request.Context(), hostID, in, queryBool(c, "apply"))
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusCreated, out)
}

func (h *FrontendGatewayHandler) GetRoute(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	out, err := h.svc.GetRoute(id)
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *FrontendGatewayHandler) UpdateRoute(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var in service.FrontendGatewayRouteInput
	if err := c.ShouldBindJSON(&in); err != nil {
		apperr.Respond(c, apperr.Wrap(err, "BAD_REQUEST", err.Error(), http.StatusBadRequest))
		return
	}
	out, err := h.svc.UpdateRoute(c.Request.Context(), id, in, queryBool(c, "apply"))
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *FrontendGatewayHandler) DeleteRoute(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteRoute(c.Request.Context(), id, queryBool(c, "apply")); err != nil {
		apperr.Respond(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *FrontendGatewayHandler) PreviewRoute(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	out, err := h.svc.PreviewRoute(id)
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func queryBool(c *gin.Context, key string) bool {
	raw := c.Query(key)
	if raw == "" {
		return false
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return raw == "1" || raw == "yes" || raw == "y"
	}
	return v
}
