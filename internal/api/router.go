package api

import (
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"swift-devops/internal/api/handler"
	"swift-devops/internal/api/middleware"
	"swift-devops/internal/config"
	"swift-devops/internal/pkg/crypto"
	wspkg "swift-devops/internal/pkg/ws"
	"swift-devops/internal/service"
)

// NewRouter 装配 gin 路由。
// 返回 Cleanup，main 应 defer 调一次（关 WS ticket GC 等）。
func NewRouter(cfg *config.Config, db *gorm.DB, aes *crypto.AESGCM, distFS fs.FS) (*gin.Engine, func()) {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.Logger())

	// 健康检查（无需鉴权）
	r.GET("/api/v1/health", handler.Health)

	// 认证（登录限流：1 分钟最多 10 次 / IP）
	authH := handler.NewAuthHandler(cfg)
	loginRL := middleware.NewRateLimiter(time.Minute, 10)
	r.POST("/api/v1/auth/login", middleware.LoginRateLimit(loginRL), authH.Login)

	// --- 服务装配（提到 group 外，便于 ws group 复用 pipeSvc 这种共享单例）---
	wsTickets := wspkg.NewTicketStore(wspkg.DefaultTicketTTL)
	wsHub := wspkg.NewHub()
	wsUpgrader := wspkg.NewUpgrader(nil) // 允许任意 origin（内网部署够用）

	hostSvc := service.NewHostService(db, aes)
	appSvc := service.NewAppService(db)
	dockerfileTplSvc := service.NewDockerfileTemplateService(db)
	depSvc := service.NewDeploymentService(db, service.WithDeploymentRuntimeLogs(hostSvc, cfg.SSH.ConnectTimeout))
	frontendGatewaySvc := service.NewFrontendGatewayService(db, hostSvc, cfg.SSH.ConnectTimeout)
	artSvc := service.NewArtifactService(db, cfg.Storage.ArtifactDir, int64(cfg.Storage.MaxUploadMB)<<20)
	gitCredSvc := service.NewGitCredentialService(db, aes)
	builderEnvSvc := service.NewBuilderEnvService(db)
	appServiceSvc := service.NewAppServiceService(db,
		service.WithAppServiceDiscovery(gitCredSvc, builderEnvSvc, cfg.Storage.BuildWorkspace)) // Sprint X.4：可部署服务 CRUD + Maven discover
	buildSvc := service.NewBuildService(db, artSvc, gitCredSvc, builderEnvSvc, cfg.Storage.BuildWorkspace, cfg.Storage.MaxHistory, cfg.Builder.DockerEnabled)
	pipeSvc := service.NewPipelineService(db, hostSvc, artSvc, cfg.SSH.ConnectTimeout)
	frontendDeploySvc := service.NewFrontendDeployService(db, hostSvc, gitCredSvc, builderEnvSvc, frontendGatewaySvc, cfg.Storage.BuildWorkspace, cfg.SSH.ConnectTimeout)
	pipeSvc.SetPublisher(wsHub)           // 异步推送 step/status 到 hub
	buildSvc.SetPublisher(wsHub)          // Sprint 5.5：构建日志实时推 hub
	frontendDeploySvc.SetPublisher(wsHub) // 前端源码部署也走 pipeline:<id> WS 主题
	frontendDeploySvc.SetPipelineCoordinator(pipeSvc)

	// --- 受保护 API（JWT + 审计）---
	v1 := r.Group("/api/v1")
	v1.Use(middleware.JWT(cfg))
	v1.Use(middleware.Audit(db))
	{
		v1.GET("/me", authH.Me)

		// WebSocket ticket 颁发
		wsTH := handler.NewWSTicketHandler(wsTickets)
		v1.POST("/ws-tickets", wsTH.Issue)

		// 主机管理
		hostH := handler.NewHostHandler(hostSvc)
		v1.POST("/hosts", hostH.Create)
		v1.GET("/hosts", hostH.List)
		v1.GET("/hosts/metrics", hostH.Metrics)
		v1.GET("/hosts/:id", hostH.Get)
		v1.PUT("/hosts/:id", hostH.Update)
		v1.DELETE("/hosts/:id", hostH.Delete)
		v1.POST("/hosts/:id/test", hostH.TestConnect)
		v1.GET("/hosts/:id/docker/containers", hostH.DockerContainers)
		v1.GET("/hosts/:id/docker/logs", hostH.DockerLogs)

		// 前端统一入口网关（gateway container + domain routes）
		fgH := handler.NewFrontendGatewayHandler(frontendGatewaySvc)
		v1.PUT("/hosts/:id/frontend-gateway", fgH.EnsureGateway)
		v1.GET("/hosts/:id/frontend-gateway", fgH.GetGateway)
		v1.POST("/hosts/:id/frontend-gateway/apply", fgH.ApplyGateway)
		v1.GET("/hosts/:id/frontend-gateway/routes", fgH.ListRoutes)
		v1.POST("/hosts/:id/frontend-gateway/routes", fgH.CreateRoute)
		v1.GET("/frontend-gateway/routes/:id", fgH.GetRoute)
		v1.PUT("/frontend-gateway/routes/:id", fgH.UpdateRoute)
		v1.DELETE("/frontend-gateway/routes/:id", fgH.DeleteRoute)
		v1.GET("/frontend-gateway/routes/:id/preview", fgH.PreviewRoute)

		// 应用管理
		appH := handler.NewAppHandler(appSvc)
		v1.POST("/apps", appH.Create)
		v1.GET("/apps", appH.List)
		v1.GET("/apps/:id", appH.Get)
		v1.PUT("/apps/:id", appH.Update)
		v1.DELETE("/apps/:id", appH.Delete)

		// 前端项目 Docker 部署配置与触发
		fdH := handler.NewFrontendDeployHandler(frontendDeploySvc)
		v1.GET("/apps/:id/frontend-config", fdH.GetConfig)
		v1.PUT("/apps/:id/frontend-config", fdH.SaveConfig)
		v1.GET("/apps/:id/frontend-config/preview", fdH.Preview)
		v1.GET("/apps/:id/frontend-states", fdH.ListStates)
		v1.POST("/apps/:id/frontend-deploy", fdH.Deploy)
		v1.POST("/apps/:id/frontend-rollback", fdH.Rollback)

		// 可部署服务 AppService（Sprint X.4）
		asH := handler.NewAppServiceHandler(appServiceSvc)
		v1.POST("/apps/:id/services", asH.Create)
		v1.GET("/apps/:id/services", asH.List)
		v1.POST("/apps/:id/services/discover", asH.Discover)
		v1.POST("/apps/:id/services/batch", asH.BatchImport)
		v1.GET("/app-services/:id", asH.Get)
		v1.PUT("/app-services/:id", asH.Update)
		v1.DELETE("/app-services/:id", asH.Delete)

		// Dockerfile 模板（应用级，多模板 + service 绑定）
		dfH := handler.NewDockerfileTemplateHandler(dockerfileTplSvc)
		v1.GET("/apps/:id/dockerfiles", dfH.List)
		v1.POST("/apps/:id/dockerfiles", dfH.Create)
		v1.POST("/apps/:id/dockerfiles/default", dfH.CreateDefault)
		v1.GET("/dockerfiles/:id", dfH.Get)
		v1.PUT("/dockerfiles/:id", dfH.Update)
		v1.DELETE("/dockerfiles/:id", dfH.Delete)

		// 应用 × 主机绑定（Deployment）
		depH := handler.NewDeploymentHandler(depSvc)
		v1.POST("/apps/:id/hosts", depH.Bind)
		v1.GET("/apps/:id/hosts", depH.ListByApp)
		v1.POST("/apps/:id/hosts/runtime-check", depH.CheckRuntimeByApp)
		v1.DELETE("/deployments/:id", depH.Unbind)
		v1.GET("/deployments/:id/runtime-logs", depH.RuntimeLogs)
		v1.POST("/deployments/:id/runtime/stop", depH.StopRuntime)
		v1.POST("/deployments/:id/runtime/restart", depH.RestartRuntime)

		// 制品（注册已有路径 + multipart 上传）
		maxUp := int64(cfg.Storage.MaxUploadMB) << 20
		artH := handler.NewArtifactHandler(artSvc, maxUp, cfg.Storage.MaxHistory)
		v1.POST("/artifacts", artH.Create)
		v1.POST("/artifacts/upload", artH.Upload)
		v1.GET("/artifacts", artH.List)
		v1.GET("/artifacts/:id", artH.Get)
		v1.DELETE("/artifacts/:id", artH.Delete)
		// Sprint X.6：多 service 制品组（Bundle）
		v1.GET("/bundles", artH.ListBundles)
		// Sprint 5.7：手动滚动清理历史 Bundle（自动清理在构建成功后触发）
		v1.POST("/apps/:id/artifacts/cleanup", artH.Cleanup)

		// 流水线（Sprint 2.4 single / Sprint 3.1 Cancel / 3.2 rolling / 3.3 rollback）
		pipeH := handler.NewPipelineHandler(pipeSvc)
		v1.POST("/apps/:id/deploy", pipeH.Deploy)
		v1.POST("/apps/:id/rollback", pipeH.Rollback)
		v1.GET("/pipelines", pipeH.List)
		v1.GET("/pipelines/:id", pipeH.Get)
		v1.POST("/pipelines/:id/rollback", pipeH.RollbackRun)
		v1.POST("/pipelines/:id/cancel", pipeH.Cancel)

		// Git 凭证管理（Sprint 5.1）
		gcH := handler.NewGitCredentialHandler(gitCredSvc)
		v1.POST("/git-creds", gcH.Create)
		v1.GET("/git-creds", gcH.List)
		v1.GET("/git-creds/:id", gcH.Get)
		v1.PUT("/git-creds/:id", gcH.Update)
		v1.DELETE("/git-creds/:id", gcH.Delete)

		// 构建（Sprint 5.3）：git clone + mvn package → 注册成 Artifact
		buildH := handler.NewBuildHandler(buildSvc)
		v1.POST("/apps/:id/build", buildH.Trigger)
		v1.GET("/builds", buildH.List)
		v1.GET("/builds/:id", buildH.Get)
		v1.GET("/builds/:id/log", buildH.GetLog)
		v1.GET("/apps/:id/branches", buildH.ListBranches) // Sprint X.11：获取远程分支列表

		// 构建环境配置（Sprint 5.4）：单例配置 + 检测
		beH := handler.NewBuilderEnvHandler(builderEnvSvc)
		v1.GET("/builder-env", beH.Get)
		v1.PUT("/builder-env", beH.Update)
		v1.POST("/builder-env/detect", beH.Detect)

		// 后续业务模块挂这里：monitor
	}

	// --- WebSocket 流（独立 group，绕开 JWT/Audit；走一次性 ticket 鉴权）---
	wsGroup := r.Group("/api/v1/ws")
	{
		pipeWSH := handler.NewPipelineWSHandler(pipeSvc, wsTickets, wsHub, wsUpgrader)
		wsGroup.GET("/pipelines/:id", pipeWSH.Stream)
		// 构建日志流（Sprint 5.5）：复用同一 hub / ticket / upgrader
		buildWSH := handler.NewBuildWSHandler(buildSvc, wsTickets, wsHub, wsUpgrader)
		wsGroup.GET("/builds/:id", buildWSH.Stream)
	}

	// SPA 静态资源 + history fallback
	fileServer := http.FileServer(http.FS(distFS))
	r.NoRoute(func(c *gin.Context) {
		p := c.Request.URL.Path
		if strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/ws/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		// 静态资源命中
		if p != "/" {
			if _, err := fs.Stat(distFS, strings.TrimPrefix(p, "/")); err == nil {
				fileServer.ServeHTTP(c.Writer, c.Request)
				return
			}
		}
		// 否则回退到 index.html
		c.Request.URL.Path = "/"
		fileServer.ServeHTTP(c.Writer, c.Request)
	})

	cleanup := func() {
		wsTickets.Close()
	}
	return r, cleanup
}
