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
	depSvc := service.NewDeploymentService(db)
	artSvc := service.NewArtifactService(db, cfg.Storage.ArtifactDir, int64(cfg.Storage.MaxUploadMB)<<20)
	pipeSvc := service.NewPipelineService(db, hostSvc, artSvc, cfg.SSH.ConnectTimeout)
	pipeSvc.SetPublisher(wsHub) // 异步推送 step/status 到 hub

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
		v1.GET("/hosts/:id", hostH.Get)
		v1.PUT("/hosts/:id", hostH.Update)
		v1.DELETE("/hosts/:id", hostH.Delete)
		v1.POST("/hosts/:id/test", hostH.TestConnect)

		// 应用管理
		appH := handler.NewAppHandler(appSvc)
		v1.POST("/apps", appH.Create)
		v1.GET("/apps", appH.List)
		v1.GET("/apps/:id", appH.Get)
		v1.PUT("/apps/:id", appH.Update)
		v1.DELETE("/apps/:id", appH.Delete)

		// 应用 × 主机绑定（Deployment）
		depH := handler.NewDeploymentHandler(depSvc)
		v1.POST("/apps/:id/hosts", depH.Bind)
		v1.GET("/apps/:id/hosts", depH.ListByApp)
		v1.DELETE("/deployments/:id", depH.Unbind)
		v1.PATCH("/deployments/:id", depH.UpdateGroup)

		// 制品（注册已有路径 + multipart 上传）
		maxUp := int64(cfg.Storage.MaxUploadMB) << 20
		artH := handler.NewArtifactHandler(artSvc, maxUp)
		v1.POST("/artifacts", artH.Create)
		v1.POST("/artifacts/upload", artH.Upload)
		v1.GET("/artifacts", artH.List)
		v1.GET("/artifacts/:id", artH.Get)
		v1.DELETE("/artifacts/:id", artH.Delete)

		// 流水线（Sprint 2.4 单主机部署）
		pipeH := handler.NewPipelineHandler(pipeSvc)
		v1.POST("/apps/:id/deploy", pipeH.Deploy)
		v1.GET("/pipelines", pipeH.List)
		v1.GET("/pipelines/:id", pipeH.Get)

		// 后续业务模块挂这里：monitor
	}

	// --- WebSocket 流（独立 group，绕开 JWT/Audit；走一次性 ticket 鉴权）---
	wsGroup := r.Group("/api/v1/ws")
	{
		pipeWSH := handler.NewPipelineWSHandler(pipeSvc, wsTickets, wsHub, wsUpgrader)
		wsGroup.GET("/pipelines/:id", pipeWSH.Stream)
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
