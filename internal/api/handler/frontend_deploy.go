package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	apperr "swift-devops/internal/pkg/errors"
	"swift-devops/internal/service"
)

// FrontendDeployHandler 管理前端项目 Docker 部署配置与触发。
type FrontendDeployHandler struct {
	svc *service.FrontendDeployService
}

func NewFrontendDeployHandler(svc *service.FrontendDeployService) *FrontendDeployHandler {
	return &FrontendDeployHandler{svc: svc}
}

func (h *FrontendDeployHandler) GetConfig(c *gin.Context) {
	appID, ok := parseID(c)
	if !ok {
		return
	}
	out, err := h.svc.GetConfig(appID, c.DefaultQuery("service_code", "web"))
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *FrontendDeployHandler) SaveConfig(c *gin.Context) {
	appID, ok := parseID(c)
	if !ok {
		return
	}
	var in service.FrontendAppConfigInput
	if err := c.ShouldBindJSON(&in); err != nil {
		apperr.Respond(c, apperr.Wrap(err, "BAD_REQUEST", err.Error(), http.StatusBadRequest))
		return
	}
	out, err := h.svc.SaveConfig(appID, in)
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *FrontendDeployHandler) Preview(c *gin.Context) {
	appID, ok := parseID(c)
	if !ok {
		return
	}
	out, err := h.svc.Preview(appID, c.DefaultQuery("service_code", "web"))
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *FrontendDeployHandler) Deploy(c *gin.Context) {
	appID, ok := parseID(c)
	if !ok {
		return
	}
	var in service.FrontendDeployInput
	if err := c.ShouldBindJSON(&in); err != nil {
		apperr.Respond(c, apperr.Wrap(err, "BAD_REQUEST", err.Error(), http.StatusBadRequest))
		return
	}
	out, err := h.svc.Deploy(c.Request.Context(), appID, in)
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}
