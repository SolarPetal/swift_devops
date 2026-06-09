package store

import (
	"fmt"
	"strings"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"swift-devops/internal/model"
)

const (
	DriverSQLite   = "sqlite"
	DriverMySQL    = "mysql"
	DriverPostgres = "postgres"
)

// NormalizeDriver 归一化 database.driver。
// 支持别名：sqlite3 -> sqlite, pg/postgresql -> postgres。
func NormalizeDriver(driver string) string {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "", "sqlite", "sqlite3":
		return DriverSQLite
	case "mysql":
		return DriverMySQL
	case "postgres", "postgresql", "pg":
		return DriverPostgres
	default:
		return strings.ToLower(strings.TrimSpace(driver))
	}
}

// Open 按 database.driver 打开数据库连接。
//
// 支持：
//   - sqlite:    纯 Go SQLite，自动开启 WAL / foreign_keys / busy_timeout
//   - mysql:     github.com/go-sql-driver/mysql DSN
//   - postgres:  pgx / PostgreSQL DSN
func Open(driver, dsn string) (*gorm.DB, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("database.dsn is required")
	}

	cfg := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	}

	switch NormalizeDriver(driver) {
	case DriverSQLite:
		return gorm.Open(sqlite.Open(sqliteDSN(dsn)), cfg)
	case DriverMySQL:
		return gorm.Open(mysql.Open(dsn), cfg)
	case DriverPostgres:
		return gorm.Open(postgres.Open(dsn), cfg)
	default:
		return nil, fmt.Errorf("unsupported database.driver %q (supported: sqlite, mysql, postgres)", driver)
	}
}

func sqliteDSN(dsn string) string {
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
}

func AutoMigrate(db *gorm.DB, driver string) error {
	// Sprint X.1 之前 deployments / pipeline_run_hosts 只有 host 维度唯一索引。
	// 多 service 发布需要升级为 (app_id, host_id, service_code) /
	// (run_id, host_id, service_code)。GORM AutoMigrate 不会删除旧索引，
	// 这里显式清理，避免老库阻止同一主机绑定多个 service。
	dropLegacyIndexes(db, driver)
	return db.AutoMigrate(
		&model.Host{},
		&model.Application{},
		&model.AppService{}, // Sprint X.1：可部署服务层
		&model.DockerfileTemplate{},
		&model.Artifact{},
		&model.ArtifactBundle{}, // Sprint X.1：版本一致性层
		&model.ArtifactItem{},   // Sprint X.1：产物明细
		&model.Deployment{},
		&model.FrontendGatewayInstance{},
		&model.FrontendGatewayRoute{},
		&model.FrontendAppConfig{},
		&model.FrontendDeploymentState{},
		&model.PipelineRun{},
		&model.PipelineRunHost{},
		&model.GitCredential{},
		&model.BuildRun{},
		&model.BuilderEnv{},
		&model.AuditLog{},
	)
}

func dropLegacyIndexes(db *gorm.DB, driver string) {
	switch NormalizeDriver(driver) {
	case DriverMySQL:
		dropMySQLIndexIfExists(db, &model.Deployment{}, "idx_app_host")
		dropMySQLIndexIfExists(db, &model.PipelineRunHost{}, "idx_run_host")
	default:
		_ = db.Exec("DROP INDEX IF EXISTS idx_app_host").Error
		_ = db.Exec("DROP INDEX IF EXISTS idx_run_host").Error
	}
}

func dropMySQLIndexIfExists(db *gorm.DB, dst any, indexName string) {
	if db.Migrator().HasIndex(dst, indexName) {
		_ = db.Migrator().DropIndex(dst, indexName)
	}
}
