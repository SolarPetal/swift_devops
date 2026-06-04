package store

import (
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"swift-devops/internal/model"
)

// Open 打开 SQLite，WAL 模式，5 秒 busy timeout，开启外键。
func Open(dsn string) (*gorm.DB, error) {
	conn := dsn + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	return gorm.Open(sqlite.Open(conn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
}

func AutoMigrate(db *gorm.DB) error {
	// Sprint X.1 之前 deployments / pipeline_run_hosts 只有 host 维度唯一索引。
	// 多 service 发布需要升级为 (app_id, host_id, service_code) /
	// (run_id, host_id, service_code)。GORM AutoMigrate 不会删除旧索引，
	// 这里显式清理，避免老库阻止同一主机绑定多个 service。
	_ = db.Exec("DROP INDEX IF EXISTS idx_app_host").Error
	_ = db.Exec("DROP INDEX IF EXISTS idx_run_host").Error
	return db.AutoMigrate(
		&model.Host{},
		&model.Application{},
		&model.AppService{}, // Sprint X.1：可部署服务层
		&model.DockerfileTemplate{},
		&model.Artifact{},
		&model.ArtifactBundle{}, // Sprint X.1：版本一致性层
		&model.ArtifactItem{},   // Sprint X.1：产物明细
		&model.Deployment{},
		&model.PipelineRun{},
		&model.PipelineRunHost{},
		&model.GitCredential{},
		&model.BuildRun{},
		&model.BuilderEnv{},
		&model.AuditLog{},
	)
}
