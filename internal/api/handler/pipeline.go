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

// Deploy POST /apps/:id/deploy  body: {"artifact_id": N, "bundle_id": M, "strategy": "single|rolling", "batch_size": 2}
//
// Sprint X.6：body 加 bundle_id（多 service Bundle 链路，优先于 artifact_id）。
// 二选一：artifact_id 是旧单 jar 链路兼容；bundle_id 是新多 service 链路。
func (h *PipelineHandler) Deploy(c *gin.Context) {
	appID, ok := parseID(c)
	if !ok {
		return
	}
	var body struct {
		ArtifactID uint   `json:"artifact_id,omitempty"` // 旧链路；bundle_id==0 时必填
		BundleID   uint   `json:"bundle_id,omitempty"`   // Sprint X.6 新链路
		Strategy   string `json:"strategy,omitempty"`    // 默认 single
		BatchSize  int    `json:"batch_size,omitempty"`  // rolling 必填，>=1
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		apperr.Respond(c, apperr.Wrap(err, "BAD_REQUEST", err.Error(), http.StatusBadRequest))
		return
	}
	if body.ArtifactID == 0 && body.BundleID == 0 {
		apperr.Respond(c, apperr.New("BAD_REQUEST",
			"必须提供 artifact_id 或 bundle_id 之一", http.StatusBadRequest))
		return
	}
	actor := "unknown"
	if v, ok := c.Get("user"); ok {
		if s, ok := v.(string); ok {
			actor = s
		}
	}
	out, err := h.svc.Trigger(appID, body.ArtifactID, actor, service.TriggerOptions{
		Strategy:  body.Strategy,
		BatchSize: body.BatchSize,
		BundleID:  body.BundleID,
	})
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

// Cancel POST /pipelines/:id/cancel
// 请求取消运行中的 pipeline。已结束的 run 返回 409。
// 取消是异步的：本接口立刻返回 202，执行线程会在最近的阶段间隙退出，
// 由 execute 收口把 status 落成 cancelled，并对未触达主机标 skipped。
func (h *PipelineHandler) Cancel(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	if err := h.svc.Cancel(id); err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"id": id, "cancel": "requested"})
}

// Rollback POST /apps/:id/rollback
// 一键回滚：每个 deployment 退到自己的 previous_artifact_id。
// 没有任何 deployment 有 previous_artifact_id → 400 BAD_REQUEST。
func (h *PipelineHandler) Rollback(c *gin.Context) {
	appID, ok := parseID(c)
	if !ok {
		return
	}
	actor := "unknown"
	if v, ok := c.Get("user"); ok {
		if s, ok := v.(string); ok {
			actor = s
		}
	}
	out, err := h.svc.Rollback(appID, actor)
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusAccepted, out)
}
