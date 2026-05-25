package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	apperr "swift-devops/internal/pkg/errors"
	"swift-devops/internal/service"
)

// BuilderEnvHandler 构建机环境配置（Sprint 5.4）。
type BuilderEnvHandler struct {
	svc *service.BuilderEnvService
}

func NewBuilderEnvHandler(svc *service.BuilderEnvService) *BuilderEnvHandler {
	return &BuilderEnvHandler{svc: svc}
}

// Get GET /builder-env
func (h *BuilderEnvHandler) Get(c *gin.Context) {
	out, err := h.svc.View()
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// Update PUT /builder-env
func (h *BuilderEnvHandler) Update(c *gin.Context) {
	var in service.BuilderEnvInput
	if err := c.ShouldBindJSON(&in); err != nil {
		apperr.Respond(c, apperr.Wrap(err, "BAD_REQUEST", err.Error(), http.StatusBadRequest))
		return
	}
	out, err := h.svc.Update(in)
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// Detect POST /builder-env/detect
func (h *BuilderEnvHandler) Detect(c *gin.Context) {
	out, err := h.svc.Detect()
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}
