package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	apperr "swift-devops/internal/pkg/errors"
	"swift-devops/internal/service"
)

// AppServiceHandler AppService（微服务层）CRUD —— Sprint X.4。
//
// 路由：
//   POST   /apps/:id/services           Create
//   GET    /apps/:id/services           List
//   GET    /app-services/:id            Get
//   PUT    /app-services/:id            Update
//   DELETE /app-services/:id            Delete
type AppServiceHandler struct {
	svc *service.AppServiceService
}

func NewAppServiceHandler(svc *service.AppServiceService) *AppServiceHandler {
	return &AppServiceHandler{svc: svc}
}

func (h *AppServiceHandler) Create(c *gin.Context) {
	appID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apperr.Respond(c, apperr.New("BAD_REQUEST", "app id 非法", http.StatusBadRequest))
		return
	}
	var in service.AppServiceInput
	if err := c.ShouldBindJSON(&in); err != nil {
		apperr.Respond(c, apperr.Wrap(err, "BAD_REQUEST", err.Error(), http.StatusBadRequest))
		return
	}
	out, err := h.svc.Create(uint(appID), in)
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusCreated, out)
}

func (h *AppServiceHandler) List(c *gin.Context) {
	appID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apperr.Respond(c, apperr.New("BAD_REQUEST", "app id 非法", http.StatusBadRequest))
		return
	}
	out, err := h.svc.List(uint(appID))
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

func (h *AppServiceHandler) Get(c *gin.Context) {
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

func (h *AppServiceHandler) Update(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var in service.AppServiceInput
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

func (h *AppServiceHandler) Delete(c *gin.Context) {
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
