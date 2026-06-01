package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	apperr "swift-devops/internal/pkg/errors"
	"swift-devops/internal/service"
)

// DockerfileTemplateHandler 管理应用级 Dockerfile 模板。
type DockerfileTemplateHandler struct {
	svc *service.DockerfileTemplateService
}

func NewDockerfileTemplateHandler(svc *service.DockerfileTemplateService) *DockerfileTemplateHandler {
	return &DockerfileTemplateHandler{svc: svc}
}

func (h *DockerfileTemplateHandler) List(c *gin.Context) {
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

func (h *DockerfileTemplateHandler) Create(c *gin.Context) {
	appID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apperr.Respond(c, apperr.New("BAD_REQUEST", "app id 非法", http.StatusBadRequest))
		return
	}
	var in service.DockerfileTemplateInput
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

func (h *DockerfileTemplateHandler) Get(c *gin.Context) {
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

func (h *DockerfileTemplateHandler) Update(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var in service.DockerfileTemplateInput
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

func (h *DockerfileTemplateHandler) Delete(c *gin.Context) {
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

func (h *DockerfileTemplateHandler) CreateDefault(c *gin.Context) {
	appID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apperr.Respond(c, apperr.New("BAD_REQUEST", "app id 非法", http.StatusBadRequest))
		return
	}
	out, err := h.svc.CreateDefault(uint(appID))
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}
