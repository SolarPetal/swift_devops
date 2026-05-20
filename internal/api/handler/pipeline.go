package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	apperr "swift-devops/internal/pkg/errors"
	"swift-devops/internal/service"
)

// PipelineHandler 流水线触发与查询
type PipelineHandler struct {
	svc *service.PipelineService
}

func NewPipelineHandler(svc *service.PipelineService) *PipelineHandler {
	return &PipelineHandler{svc: svc}
}

// Deploy POST /apps/:id/deploy  body: {"artifact_id": N, "strategy": "single"}
func (h *PipelineHandler) Deploy(c *gin.Context) {
	appID, ok := parseID(c)
	if !ok {
		return
	}
	var body struct {
		ArtifactID uint   `json:"artifact_id" binding:"required"`
		Strategy   string `json:"strategy,omitempty"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		apperr.Respond(c, apperr.Wrap(err, "BAD_REQUEST", err.Error(), http.StatusBadRequest))
		return
	}
	actor := "unknown"
	if v, ok := c.Get("user"); ok {
		if s, ok := v.(string); ok {
			actor = s
		}
	}
	out, err := h.svc.Trigger(appID, body.ArtifactID, actor, body.Strategy)
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusAccepted, out)
}

// List GET /pipelines?app_id=N
func (h *PipelineHandler) List(c *gin.Context) {
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

// Get GET /pipelines/:id
func (h *PipelineHandler) Get(c *gin.Context) {
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
