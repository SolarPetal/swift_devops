package service

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"

	"swift-devops/internal/model"
	"swift-devops/internal/pkg/deploy"
	apperr "swift-devops/internal/pkg/errors"
)

// ArtifactInput 注册制品入参（按已有本地路径）。
// 仍保留兼容老流程，但路径必须落在 cfg.Storage.ArtifactDir 之下。
type ArtifactInput struct {
	AppID      uint   `json:"app_id" binding:"required"`
	VersionTag string `json:"version_tag" binding:"required"`
	FilePath   string `json:"file_path" binding:"required"` // 绝对路径
	FileName   string `json:"file_name,omitempty"`          // 留空则取 FilePath basename
}

// ArtifactUploadInput multipart 上传入参（不含文件流，流走 reader 参数）。
type ArtifactUploadInput struct {
	AppID      uint
	VersionTag string
	FileName   string // 客户端原始文件名
}

// ArtifactView 响应视图
type ArtifactView struct {
	ID          uint   `json:"id"`
	AppID       uint   `json:"app_id"`
	VersionTag  string `json:"version_tag"`
	FileName    string `json:"file_name"`
	FilePath    string `json:"file_path"`
	FileMD5     string `json:"file_md5"`
	FileSize    int64  `json:"file_size"`
	BuildStatus string `json:"build_status"`
	CreatedAt   string `json:"created_at"`
}

func toArtifactView(a *model.Artifact) ArtifactView {
	return ArtifactView{
		ID: a.ID, AppID: a.AppID, VersionTag: a.VersionTag,
		FileName: a.FileName, FilePath: a.FilePath,
		FileMD5: a.FileMD5, FileSize: a.FileSize,
		BuildStatus: a.BuildStatus,
		CreatedAt:   a.CreatedAt.Format(time.RFC3339),
	}
}

// ArtifactService 制品注册 + 上传
type ArtifactService struct {
	db             *gorm.DB
	artifactDir    string // 绝对、规范化后的根目录
	maxUploadBytes int64  // <=0 表示不限
}

// NewArtifactService 构造。
// artifactDir 必须非空；构造函数会 Abs+Clean，确保后续白名单比较稳。
// maxUploadBytes <=0 表示不限制单文件大小。
func NewArtifactService(db *gorm.DB, artifactDir string, maxUploadBytes int64) *ArtifactService {
	root := artifactDir
	if root != "" {
		if abs, err := filepath.Abs(root); err == nil {
			root = filepath.Clean(abs)
		}
	}
	return &ArtifactService{db: db, artifactDir: root, maxUploadBytes: maxUploadBytes}
}

// Register 注册一个已存在的本地文件为制品。
// 校验：app 存在 / 文件是绝对路径且必须落在 artifactDir 之下且是普通文件 / 计算 MD5+size。
func (s *ArtifactService) Register(in ArtifactInput) (ArtifactView, error) {
	// 1. app 存在
	var app model.Application
	if err := s.db.First(&app, in.AppID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ArtifactView{}, apperr.New("NOT_FOUND", fmt.Sprintf("应用 %d 不存在", in.AppID), 404)
		}
		return ArtifactView{}, apperr.Wrap(err, "INTERNAL", "find app", 500)
	}
	// 2. 路径白名单
	abs, err := s.ensureUnderArtifactDir(in.FilePath)
	if err != nil {
		return ArtifactView{}, err
	}
	// 3. 是普通文件
	st, err := os.Stat(abs)
	if err != nil {
		return ArtifactView{}, apperr.New("BAD_REQUEST", fmt.Sprintf("file_path 不可读：%v", err), 400)
	}
	if !st.Mode().IsRegular() {
		return ArtifactView{}, apperr.New("BAD_REQUEST", "file_path 必须是普通文件", 400)
	}
	// 4. MD5
	md5sum, err := deploy.LocalFileMD5(abs)
	if err != nil {
		return ArtifactView{}, apperr.Wrap(err, "INTERNAL", "md5", 500)
	}
	// 5. 写库
	name := in.FileName
	if name == "" {
		name = filepath.Base(abs)
	}
	a := &model.Artifact{
		AppID: in.AppID, VersionTag: in.VersionTag,
		FileName: name, FilePath: abs,
		FileMD5: md5sum, FileSize: st.Size(),
		BuildStatus: "success",
	}
	if err := s.db.Create(a).Error; err != nil {
		return ArtifactView{}, apperr.Wrap(err, "INTERNAL", "create artifact", 500)
	}
	return toArtifactView(a), nil
}

// ensureUnderArtifactDir 把入参路径规范化后断言落在 artifactDir 之下。
// artifactDir 为空时退化为"必须绝对路径"老校验，不做白名单。
func (s *ArtifactService) ensureUnderArtifactDir(p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", apperr.New("BAD_REQUEST", "file_path 必须是绝对路径", 400)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", apperr.New("BAD_REQUEST", "file_path 不可解析", 400)
	}
	abs = filepath.Clean(abs)
	if s.artifactDir == "" {
		return abs, nil // 未配置白名单（极少见，仅旧测试兼容）
	}
	rel, err := filepath.Rel(s.artifactDir, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", apperr.New("BAD_REQUEST",
			fmt.Sprintf("file_path 必须位于 %s 之下", s.artifactDir), 400)
	}
	return abs, nil
}

// safeVersionTag 把 version_tag 收成文件名安全：只留 [A-Za-z0-9._-]，其它换 _；
// 顺手折叠连续 `.`（避免 `..` 残留）。
var versionSanitizer = regexp.MustCompile(`[^A-Za-z0-9._-]`)
var collapseDots = regexp.MustCompile(`\.{2,}`)

func sanitizeVersion(v string) string {
	v = versionSanitizer.ReplaceAllString(v, "_")
	v = collapseDots.ReplaceAllString(v, "_")
	return v
}

// Upload 接收 multipart 文件流，落地到 {artifactDir}/{app_code}/{version}-{ts}{ext}。
// 边写边算 MD5 + 累计大小；超过 maxUploadBytes 立即中断并清理落地文件。
// 落库后返回 view。reader 调用方负责关闭。
func (s *ArtifactService) Upload(in ArtifactUploadInput, src io.Reader) (ArtifactView, error) {
	if s.artifactDir == "" {
		return ArtifactView{}, apperr.New("INTERNAL", "storage.artifact_dir 未配置", 500)
	}
	if in.VersionTag == "" {
		return ArtifactView{}, apperr.New("BAD_REQUEST", "version_tag 必填", 400)
	}
	if in.FileName == "" {
		return ArtifactView{}, apperr.New("BAD_REQUEST", "file 不能为空", 400)
	}

	// 1. app 存在 + 取 AppCode 作为子目录
	var app model.Application
	if err := s.db.First(&app, in.AppID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ArtifactView{}, apperr.New("NOT_FOUND", fmt.Sprintf("应用 %d 不存在", in.AppID), 404)
		}
		return ArtifactView{}, apperr.Wrap(err, "INTERNAL", "find app", 500)
	}

	// 2. 组装落地路径：{root}/{app_code}/{sanitized_version}-{unixnano}{ext}
	subDir := filepath.Join(s.artifactDir, app.AppCode)
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		return ArtifactView{}, apperr.Wrap(err, "INTERNAL", "mkdir artifact subdir", 500)
	}
	ext := filepath.Ext(in.FileName)
	if ext == "" {
		ext = ".jar"
	}
	ver := sanitizeVersion(in.VersionTag)
	fname := fmt.Sprintf("%s-%d%s", ver, time.Now().UnixNano(), ext)
	abs := filepath.Join(subDir, fname)

	// 3. 流式写入 + MD5 + 大小限制
	f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return ArtifactView{}, apperr.Wrap(err, "INTERNAL", "create file", 500)
	}
	closed := false
	cleanup := func() {
		if !closed {
			_ = f.Close()
			closed = true
		}
		_ = os.Remove(abs)
	}

	h := md5.New()
	mw := io.MultiWriter(f, h)
	var reader io.Reader = src
	if s.maxUploadBytes > 0 {
		// 多读 1 字节，命中说明超限
		reader = io.LimitReader(src, s.maxUploadBytes+1)
	}
	written, err := io.Copy(mw, reader)
	if err != nil {
		cleanup()
		return ArtifactView{}, apperr.Wrap(err, "INTERNAL", "copy upload", 500)
	}
	if s.maxUploadBytes > 0 && written > s.maxUploadBytes {
		cleanup()
		return ArtifactView{}, apperr.New("REQUEST_TOO_LARGE",
			fmt.Sprintf("文件超出 %d MB 限制", s.maxUploadBytes>>20), 413)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(abs)
		return ArtifactView{}, apperr.Wrap(err, "INTERNAL", "close file", 500)
	}
	closed = true

	// 4. 落库；失败回滚文件
	a := &model.Artifact{
		AppID: in.AppID, VersionTag: in.VersionTag,
		FileName: filepath.Base(in.FileName), FilePath: abs,
		FileMD5: hex.EncodeToString(h.Sum(nil)), FileSize: written,
		BuildStatus: "success",
	}
	if err := s.db.Create(a).Error; err != nil {
		_ = os.Remove(abs)
		return ArtifactView{}, apperr.Wrap(err, "INTERNAL", "create artifact", 500)
	}
	return toArtifactView(a), nil
}

// IngestLocalJar 把构建产出的 jar 拷贝到 artifactDir 并注册成 Artifact。
// Sprint 5.3 给 BuildService 调：
//   - localPath 通常在 build_workspace 内（构建工作区），不一定在 artifactDir 白名单
//   - 拷贝到 {artifactDir}/{app_code}/{version_tag}{ext}
//   - 计算 MD5 + size，落 Artifact 表
//   - 不删 localPath（调用方决定是否清理 build workspace）
//
// Sprint X.2 注意：这是单 jar 旧链路，多 service 走 [IngestBundle]。
func (s *ArtifactService) IngestLocalJar(appID uint, versionTag, localPath string) (ArtifactView, error) {
	if s.artifactDir == "" {
		return ArtifactView{}, apperr.New("INTERNAL", "storage.artifact_dir 未配置", 500)
	}
	if strings.TrimSpace(versionTag) == "" {
		return ArtifactView{}, apperr.New("BAD_REQUEST", "version_tag 必填", 400)
	}

	// 1. 校验 app
	var app model.Application
	if err := s.db.First(&app, appID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ArtifactView{}, apperr.New("NOT_FOUND", fmt.Sprintf("应用 %d 不存在", appID), 404)
		}
		return ArtifactView{}, apperr.Wrap(err, "INTERNAL", "find app", 500)
	}

	// 2. 源文件检查
	src, err := os.Open(localPath)
	if err != nil {
		return ArtifactView{}, apperr.Wrap(err, "INTERNAL", "open source jar", 500)
	}
	defer src.Close()
	st, err := src.Stat()
	if err != nil {
		return ArtifactView{}, apperr.Wrap(err, "INTERNAL", "stat source jar", 500)
	}

	// 3. 组装落地路径：{root}/{app_code}/{sanitized_version}-{unixnano}{ext}
	subDir := filepath.Join(s.artifactDir, app.AppCode)
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		return ArtifactView{}, apperr.Wrap(err, "INTERNAL", "mkdir artifact subdir", 500)
	}
	ext := filepath.Ext(localPath)
	if ext == "" {
		ext = ".jar"
	}
	ver := sanitizeVersion(versionTag)
	fname := fmt.Sprintf("%s-%d%s", ver, time.Now().UnixNano(), ext)
	dst := filepath.Join(subDir, fname)

	// 4. 拷贝 + MD5
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return ArtifactView{}, apperr.Wrap(err, "INTERNAL", "create dst", 500)
	}
	h := md5.New()
	mw := io.MultiWriter(f, h)
	written, err := io.Copy(mw, src)
	if err != nil {
		_ = f.Close()
		_ = os.Remove(dst)
		return ArtifactView{}, apperr.Wrap(err, "INTERNAL", "copy jar", 500)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(dst)
		return ArtifactView{}, apperr.Wrap(err, "INTERNAL", "close dst", 500)
	}
	if written != st.Size() {
		_ = os.Remove(dst)
		return ArtifactView{}, apperr.New("INTERNAL",
			fmt.Sprintf("copy size mismatch: src=%d written=%d", st.Size(), written), 500)
	}

	// 5. 落库
	a := &model.Artifact{
		AppID: appID, VersionTag: versionTag,
		FileName: filepath.Base(localPath), FilePath: dst,
		FileMD5: hex.EncodeToString(h.Sum(nil)), FileSize: written,
		BuildStatus: "success",
	}
	if err := s.db.Create(a).Error; err != nil {
		_ = os.Remove(dst)
		return ArtifactView{}, apperr.Wrap(err, "INTERNAL", "create artifact", 500)
	}
	return toArtifactView(a), nil
}

// ListByApp 列出应用制品（最新优先）
func (s *ArtifactService) ListByApp(appID uint) ([]ArtifactView, error) {
	var as []model.Artifact
	q := s.db.Order("id DESC")
	if appID > 0 {
		q = q.Where("app_id = ?", appID)
	}
	if err := q.Find(&as).Error; err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "list artifacts", 500)
	}
	vs := make([]ArtifactView, len(as))
	for i := range as {
		vs[i] = toArtifactView(&as[i])
	}
	return vs, nil
}

// Get 取一个
func (s *ArtifactService) Get(id uint) (ArtifactView, error) {
	a, err := s.findByID(id)
	if err != nil {
		return ArtifactView{}, err
	}
	return toArtifactView(a), nil
}

// GetModel 取原始模型，供 pipeline 使用 FilePath / FileMD5。
func (s *ArtifactService) GetModel(id uint) (*model.Artifact, error) {
	return s.findByID(id)
}

// Delete 删除制品记录（不动本地文件）。
// 若仍被 Deployment.current_artifact_id 引用则拒绝，避免回滚链断裂。
func (s *ArtifactService) Delete(id uint) error {
	var n int64
	if err := s.db.Model(&model.Deployment{}).
		Where("current_artifact_id = ? OR previous_artifact_id = ?", id, id).
		Count(&n).Error; err != nil {
		return apperr.Wrap(err, "INTERNAL", "count deployments", 500)
	}
	if n > 0 {
		return apperr.New("CONFLICT",
			fmt.Sprintf("制品仍被 %d 个部署引用，请先发布其他版本", n), 409)
	}
	res := s.db.Delete(&model.Artifact{}, id)
	if res.Error != nil {
		return apperr.Wrap(res.Error, "INTERNAL", "delete artifact", 500)
	}
	if res.RowsAffected == 0 {
		return apperr.ErrNotFound
	}
	return nil
}

func (s *ArtifactService) findByID(id uint) (*model.Artifact, error) {
	var a model.Artifact
	if err := s.db.First(&a, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.ErrNotFound
		}
		return nil, apperr.Wrap(err, "INTERNAL", "get artifact", 500)
	}
	return &a, nil
}

// =============================================================================
// Sprint X.2：ArtifactBundle + ArtifactItem 多 service 链路
// =============================================================================

// BundleItemInput IngestBundle 入参的单条 service 产物。
type BundleItemInput struct {
	ServiceCode string // 对应 AppService.ServiceCode；多 service 时必填，单 service 兜底 "default"
	LocalPath   string // 构建产物绝对路径（一般在 build_workspace 内）
}

// ArtifactBundleView 整组制品视图。
type ArtifactBundleView struct {
	ID           uint                `json:"id"`
	AppID        uint                `json:"app_id"`
	VersionTag   string              `json:"version_tag"`
	GitCommitSHA string              `json:"git_commit_sha"`
	BuildStatus  string              `json:"build_status"`
	BuildLogPath string              `json:"build_log_path"`
	TriggeredBy  string              `json:"triggered_by"`
	CreatedAt    string              `json:"created_at"`
	Items        []ArtifactItemView  `json:"items"`
}

// ArtifactItemView 单 service 产物明细。
type ArtifactItemView struct {
	ID          uint   `json:"id"`
	BundleID    uint   `json:"bundle_id"`
	ServiceCode string `json:"service_code"`
	FileName    string `json:"file_name"`
	FilePath    string `json:"file_path"`
	FileMD5     string `json:"file_md5"`
	FileSize    int64  `json:"file_size"`
	CreatedAt   string `json:"created_at"`
}

func toBundleView(b *model.ArtifactBundle, items []model.ArtifactItem) ArtifactBundleView {
	v := ArtifactBundleView{
		ID: b.ID, AppID: b.AppID, VersionTag: b.VersionTag,
		GitCommitSHA: b.GitCommitSHA, BuildStatus: b.BuildStatus,
		BuildLogPath: b.BuildLogPath, TriggeredBy: b.TriggeredBy,
		CreatedAt: b.CreatedAt.Format(time.RFC3339),
	}
	for i := range items {
		it := &items[i]
		v.Items = append(v.Items, ArtifactItemView{
			ID: it.ID, BundleID: it.BundleID, ServiceCode: it.ServiceCode,
			FileName: it.FileName, FilePath: it.FilePath,
			FileMD5: it.FileMD5, FileSize: it.FileSize,
			CreatedAt: it.CreatedAt.Format(time.RFC3339),
		})
	}
	return v
}

// IngestBundle 接收一组 service jar，拷到 artifactDir，落 ArtifactBundle + N 条 ArtifactItem。
// 整个过程事务化：任一 item 失败回滚整组（已拷出来的文件清理掉）。
//
// 落地路径：{artifactDir}/{app_code}/bundles/{sanitized_version}/{service_code}-{unixnano}{ext}
func (s *ArtifactService) IngestBundle(appID uint, versionTag, commitSHA, triggeredBy, logPath string, items []BundleItemInput) (ArtifactBundleView, error) {
	if s.artifactDir == "" {
		return ArtifactBundleView{}, apperr.New("INTERNAL", "storage.artifact_dir 未配置", 500)
	}
	if strings.TrimSpace(versionTag) == "" {
		return ArtifactBundleView{}, apperr.New("BAD_REQUEST", "version_tag 必填", 400)
	}
	if len(items) == 0 {
		return ArtifactBundleView{}, apperr.New("BAD_REQUEST", "至少一个 service 产物", 400)
	}

	var app model.Application
	if err := s.db.First(&app, appID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ArtifactBundleView{}, apperr.New("NOT_FOUND", fmt.Sprintf("应用 %d 不存在", appID), 404)
		}
		return ArtifactBundleView{}, apperr.Wrap(err, "INTERNAL", "find app", 500)
	}

	// 1. 准备目录
	ver := sanitizeVersion(versionTag)
	bundleDir := filepath.Join(s.artifactDir, app.AppCode, "bundles", ver)
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		return ArtifactBundleView{}, apperr.Wrap(err, "INTERNAL", "mkdir bundle dir", 500)
	}

	// 2. 拷贝所有 jar + 计算 MD5；失败立刻清理已成功的文件
	type stagedItem struct {
		service string
		dst     string
		size    int64
		md5     string
		srcName string
	}
	var staged []stagedItem
	rollback := func() {
		for _, st := range staged {
			_ = os.Remove(st.dst)
		}
	}

	for _, it := range items {
		svc := strings.TrimSpace(it.ServiceCode)
		if svc == "" {
			svc = "default"
		}
		src, err := os.Open(it.LocalPath)
		if err != nil {
			rollback()
			return ArtifactBundleView{}, apperr.Wrap(err, "INTERNAL", "open src "+svc, 500)
		}
		st, err := src.Stat()
		if err != nil {
			src.Close()
			rollback()
			return ArtifactBundleView{}, apperr.Wrap(err, "INTERNAL", "stat src "+svc, 500)
		}
		ext := filepath.Ext(it.LocalPath)
		if ext == "" {
			ext = ".jar"
		}
		dst := filepath.Join(bundleDir, fmt.Sprintf("%s-%d%s", svc, time.Now().UnixNano(), ext))
		f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			src.Close()
			rollback()
			return ArtifactBundleView{}, apperr.Wrap(err, "INTERNAL", "create dst "+svc, 500)
		}
		h := md5.New()
		mw := io.MultiWriter(f, h)
		written, err := io.Copy(mw, src)
		src.Close()
		if err != nil {
			f.Close()
			_ = os.Remove(dst)
			rollback()
			return ArtifactBundleView{}, apperr.Wrap(err, "INTERNAL", "copy jar "+svc, 500)
		}
		if err := f.Close(); err != nil {
			_ = os.Remove(dst)
			rollback()
			return ArtifactBundleView{}, apperr.Wrap(err, "INTERNAL", "close dst "+svc, 500)
		}
		if written != st.Size() {
			_ = os.Remove(dst)
			rollback()
			return ArtifactBundleView{}, apperr.New("INTERNAL",
				fmt.Sprintf("size mismatch service=%s src=%d written=%d", svc, st.Size(), written), 500)
		}
		staged = append(staged, stagedItem{
			service: svc, dst: dst, size: written,
			md5:     hex.EncodeToString(h.Sum(nil)),
			srcName: filepath.Base(it.LocalPath),
		})
	}

	// 3. 事务落库 Bundle + Items
	var bundle model.ArtifactBundle
	err := s.db.Transaction(func(tx *gorm.DB) error {
		bundle = model.ArtifactBundle{
			AppID: appID, VersionTag: versionTag,
			GitCommitSHA: commitSHA, BuildStatus: "success",
			BuildLogPath: logPath, TriggeredBy: triggeredBy,
		}
		if err := tx.Create(&bundle).Error; err != nil {
			return err
		}
		for _, st := range staged {
			it := model.ArtifactItem{
				BundleID: bundle.ID, ServiceCode: st.service,
				FileName: st.srcName, FilePath: st.dst,
				FileMD5: st.md5, FileSize: st.size,
			}
			if err := tx.Create(&it).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		rollback()
		return ArtifactBundleView{}, apperr.Wrap(err, "INTERNAL", "create bundle tx", 500)
	}

	// 4. 重新加载 items 拼 view
	var dbItems []model.ArtifactItem
	if err := s.db.Where("bundle_id = ?", bundle.ID).Order("service_code ASC").Find(&dbItems).Error; err != nil {
		return ArtifactBundleView{}, apperr.Wrap(err, "INTERNAL", "load items", 500)
	}
	return toBundleView(&bundle, dbItems), nil
}

// GetBundle 加载一个 Bundle 含 items（部署链用）。
func (s *ArtifactService) GetBundle(id uint) (*model.ArtifactBundle, []model.ArtifactItem, error) {
	var b model.ArtifactBundle
	if err := s.db.First(&b, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, apperr.ErrNotFound
		}
		return nil, nil, apperr.Wrap(err, "INTERNAL", "get bundle", 500)
	}
	var items []model.ArtifactItem
	if err := s.db.Where("bundle_id = ?", id).Order("service_code ASC").Find(&items).Error; err != nil {
		return nil, nil, apperr.Wrap(err, "INTERNAL", "list items", 500)
	}
	return &b, items, nil
}

// ListBundles 列应用的整组制品（最新优先）。
func (s *ArtifactService) ListBundles(appID uint) ([]ArtifactBundleView, error) {
	var bs []model.ArtifactBundle
	q := s.db.Order("id DESC").Limit(200)
	if appID > 0 {
		q = q.Where("app_id = ?", appID)
	}
	if err := q.Find(&bs).Error; err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "list bundles", 500)
	}
	if len(bs) == 0 {
		return []ArtifactBundleView{}, nil
	}
	ids := make([]uint, len(bs))
	for i := range bs {
		ids[i] = bs[i].ID
	}
	var items []model.ArtifactItem
	if err := s.db.Where("bundle_id IN ?", ids).Find(&items).Error; err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "list items", 500)
	}
	byBundle := map[uint][]model.ArtifactItem{}
	for _, it := range items {
		byBundle[it.BundleID] = append(byBundle[it.BundleID], it)
	}
	out := make([]ArtifactBundleView, len(bs))
	for i := range bs {
		out[i] = toBundleView(&bs[i], byBundle[bs[i].ID])
	}
	return out, nil
}
