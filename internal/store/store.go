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
	return db.AutoMigrate(
		&model.Host{},
		&model.Application{},
		&model.Artifact{},
		&model.Deployment{},
		&model.PipelineRun{},
		&model.PipelineRunHost{},
		&model.GitCredential{},
		&model.BuildRun{},
		&model.BuilderEnv{},
		&model.AuditLog{},
	)
}
