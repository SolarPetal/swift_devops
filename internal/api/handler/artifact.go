package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	apperr "swift-devops/internal/pkg/errors"
	"swift-devops/internal/service"
)

// ArtifactHandler 制品（注册 + multipart 上传）
type ArtifactHandler struct {
	svc            *service.ArtifactService
	maxUploadBytes int64
}

func NewArtifactHandler(svc *service.ArtifactService, maxUploadBytes int64) *ArtifactHandler {
	return &ArtifactHandler{svc: svc, maxUploadBytes: maxUploadBytes}
}

// Create POST /artifacts （按已存在本地路径注册）
func (h *ArtifactHandler) Create(c *gin.Context) {
	var in service.ArtifactInput
	if err := c.ShouldBindJSON(&in); err != nil {
		apperr.Respond(c, apperr.Wrap(err, "BAD_REQUEST", err.Error(), http.StatusBadRequest))
		return
	}
	out, err := h.svc.Register(in)
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusCreated, out)
}

// Upload POST /artifacts/upload multipart/form-data
// 字段：app_id (uint, form), version_tag (string, form), file (file, form)。
// 服务端用 MaxBytesReader 把整个 request 体卡死，避免恶意客户端流式发巨量数据。
func (h *ArtifactHandler) Upload(c *gin.Context) {
	ct := c.Request.Header.Get("Content-Type")

	// 1. 整体请求大小硬上限：multipart 头开销 + 文件 + 一点冗余
	if h.maxUploadBytes > 0 {
		// 给 multipart 边界 + 字段头加 1MB 缓冲
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.maxUploadBytes+(1<<20))
	}

	// 2. 显式解 multipart，把错误 log 出来——比 c.PostForm 静默失败可观测得多
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
		slog.Warn("upload: parse multipart failed",
			"content_type", ct, "err", err.Error())
		// 区分超大体与 boundary 错配
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			apperr.Respond(c, apperr.New("REQUEST_TOO_LARGE",
				"上传体超出服务器限制", http.StatusRequestEntityTooLarge))
			return
		}
		apperr.Respond(c, apperr.New("BAD_REQUEST",
			"multipart 解析失败："+err.Error(), http.StatusBadRequest))
		return
	}

	// 3. 解析 form 字段
	appIDStr := c.PostForm("app_id")
	versionTag := c.PostForm("version_tag")
	if appIDStr == "" || versionTag == "" {
		slog.Warn("upload: missing form fields",
			"content_type", ct, "has_app_id", appIDStr != "", "has_version_tag", versionTag != "")
		apperr.Respond(c, apperr.New("BAD_REQUEST", "app_id / version_tag 必填", http.StatusBadRequest))
		return
	}
	appID, err := strconv.ParseUint(appIDStr, 10, 32)
	if err != nil {
		apperr.Respond(c, apperr.New("BAD_REQUEST", "app_id 必须是整数", http.StatusBadRequest))
		return
	}

	// 4. 取文件
	fh, err := c.FormFile("file")
	if err != nil {
		// 区分超大体（MaxBytesReader 返回 *http.MaxBytesError）与缺字段
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			apperr.Respond(c, apperr.New("REQUEST_TOO_LARGE",
				"上传体超出服务器限制", http.StatusRequestEntityTooLarge))
			return
		}
		apperr.Respond(c, apperr.Wrap(err, "BAD_REQUEST", "file 字段缺失或不可读", http.StatusBadRequest))
		return
	}
	src, err := fh.Open()
	if err != nil {
		apperr.Respond(c, apperr.Wrap(err, "INTERNAL", "open uploaded file", http.StatusInternalServerError))
		return
	}
	defer src.Close()

	// 5. 落库
	out, err := h.svc.Upload(service.ArtifactUploadInput{
		AppID:      uint(appID),
		VersionTag: versionTag,
		FileName:   fh.Filename,
	}, src)
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusCreated, out)
}

// List GET /artifacts?app_id=N
func (h *ArtifactHandler) List(c *gin.Context) {
	var appID uint
	if s := c.Query("app_id"); s != "" {
		n, err := strconv.ParseUint(s, 10, 32)
		if err != nil {
			apperr.Respond(c, apperr.New("BAD_REQUEST", "app_id 必须是整数", http.StatusBadRequest))
			return
		}
		appID = uint(n)
	}
	out, err := h.svc.ListByApp(appID)
	if err != nil {
		apperr.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

// Get GET /artifacts/:id
func (h *ArtifactHandler) Get(c *gin.Context) {
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

// Delete DELETE /artifacts/:id
func (h *ArtifactHandler) Delete(c *gin.Context) {
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
