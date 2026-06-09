package handler

import (
	"net/http"
	"strconv"

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

func (h *FrontendDeployHandler) ListStates(c *gin.Context) {
	appID, ok := parseID(c)
	if !ok {
		return
	}
	var hostID uint
	if raw := c.Query("host_id"); raw != "" {
		id, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			apperr.Respond(c, apperr.New("BAD_REQUEST", "host_id 必须是正整数", http.StatusBadRequest))
			return
		}
		hostID = uint(id)
	}
	out, err := h.svc.ListStates(appID, service.FrontendDeploymentStateFilter{
		HostID:      hostID,
		ServiceCode: c.Query("service_code"),
		Domain:      c.Query("domain"),
	})
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
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
	actor := "unknown"
	if v, ok := c.Get("user"); ok {
		if s, ok := v.(string); ok {
			actor = s
		}
	}
	out, err := h.svc.Deploy(c.Request.Context(), appID, in, actor)
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusAccepted, out)
}

func (h *FrontendDeployHandler) Rollback(c *gin.Context) {
	appID, ok := parseID(c)
	if !ok {
		return
	}
	var in service.FrontendRollbackInput
	if err := c.ShouldBindJSON(&in); err != nil {
		apperr.Respond(c, apperr.Wrap(err, "BAD_REQUEST", err.Error(), http.StatusBadRequest))
		return
	}
	actor := "unknown"
	if v, ok := c.Get("user"); ok {
		if s, ok := v.(string); ok {
			actor = s
		}
	}
	out, err := h.svc.Rollback(c.Request.Context(), appID, in, actor)
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusAccepted, out)
}
