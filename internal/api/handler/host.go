package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	apperr "swift-devops/internal/pkg/errors"
	"swift-devops/internal/service"
)

// HostHandler 主机管理 HTTP 接口
type HostHandler struct {
	svc *service.HostService
}

func NewHostHandler(svc *service.HostService) *HostHandler {
	return &HostHandler{svc: svc}
}

func (h *HostHandler) Create(c *gin.Context) {
	var in service.HostInput
	if err := c.ShouldBindJSON(&in); err != nil {
		apperr.Respond(c, apperr.Wrap(err, "BAD_REQUEST", err.Error(), http.StatusBadRequest))
		return
	}
	out, err := h.svc.Create(in)
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusCreated, out)
}

func (h *HostHandler) List(c *gin.Context) {
	out, err := h.svc.List()
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

func (h *HostHandler) Get(c *gin.Context) {
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

func (h *HostHandler) Update(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var in service.HostInput
	if err := c.ShouldBindJSON(&in); err != nil {
		apperr.Respond(c, apperr.Wrap(err, "BAD_REQUEST", err.Error(), http.StatusBadRequest))
		return
	}
	out, err := h.svc.Update(id, in)
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *HostHandler) Delete(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	if err := h.svc.Delete(id); err != nil {
		apperr.Respond(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *HostHandler) TestConnect(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	result, err := h.svc.TestConnect(id)
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// parseID 解析 :id 参数。失败时已经写过响应，返回 ok=false 让 caller 早退。
func parseID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		apperr.Respond(c, apperr.Wrap(err, "BAD_REQUEST", "invalid id", http.StatusBadRequest))
		return 0, false
	}
	return uint(id), true
}
