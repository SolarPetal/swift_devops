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
	"swift-devops/internal/service"
)

func NewRouter(cfg *config.Config, db *gorm.DB, aes *crypto.AESGCM, distFS fs.FS) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.Logger())

	// 健康检查（无需鉴权）
	r.GET("/api/v1/health", handler.Health)

	// 认证（登录限流：1 分钟最多 10 次 / IP）
	authH := handler.NewAuthHandler(cfg)
	loginRL := middleware.NewRateLimiter(time.Minute, 10)
	r.POST("/api/v1/auth/login", middleware.LoginRateLimit(loginRL), authH.Login)

	// 受保护 API（写操作自动审计）
	v1 := r.Group("/api/v1")
	v1.Use(middleware.JWT(cfg))
	v1.Use(middleware.Audit(db))
	{
		v1.GET("/me", authH.Me)

		// 主机管理
		hostSvc := service.NewHostService(db, aes)
		hostH := handler.NewHostHandler(hostSvc)
		v1.POST("/hosts", hostH.Create)
		v1.GET("/hosts", hostH.List)
		v1.GET("/hosts/:id", hostH.Get)
		v1.PUT("/hosts/:id", hostH.Update)
		v1.DELETE("/hosts/:id", hostH.Delete)
		v1.POST("/hosts/:id/test", hostH.TestConnect)

		// 后续业务模块挂这里：apps / artifacts / pipelines / monitor
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

	return r
}
