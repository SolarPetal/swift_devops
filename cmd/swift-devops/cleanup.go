package main

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"gorm.io/gorm"

	"swift-devops/internal/config"
	"swift-devops/internal/store"
)

// runCleanupApps 清空应用链相关表，保留主机 / 凭证 / 用户 / 构建环境 / 审计。
//
// Sprint X.1：数据模型从"单体 jar"切换到"App + AppService"前的一次性清理。
//   - DELETE：applications / artifacts / builds / deployments / pipeline_runs / pipeline_run_hosts
//   - DROP INDEX：deployments.idx_app_host、pipeline_run_hosts.idx_run_host
//     （旧 (app,host) 与 (run,host) 唯一索引会拦下新 (app,host,service) / (run,host,service) 组合写入）
//   - 保留：hosts / git_credentials / builder_env / audit_logs
//
// 用法：swift-devops cleanup-apps --config <path>
func runCleanupApps(args []string) {
	cfgPath := "/etc/swift-devops/config.yaml"
	for i := 0; i < len(args); i++ {
		if args[i] == "--config" && i+1 < len(args) {
			cfgPath = args[i+1]
			i++
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config failed: %v\n", err)
		os.Exit(1)
	}
	setupLogger(cfg)

	db, err := store.Open(cfg.Database.DSN)
	if err != nil {
		slog.Error("open db failed", "err", err)
		os.Exit(1)
	}

	if err := cleanupApps(db); err != nil {
		slog.Error("cleanup-apps failed", "err", err)
		os.Exit(1)
	}
	slog.Info("cleanup-apps done")
}

// cleanupApps 在一个事务里完成 DELETE + DROP INDEX。
// 顺序按外键依赖：子表先清，父表后清。
func cleanupApps(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		// 1. 子表先清
		stmts := []string{
			"DELETE FROM pipeline_run_hosts",
			"DELETE FROM pipeline_runs",
			"DELETE FROM deployments",
			"DELETE FROM build_runs",
			"DELETE FROM artifacts",
			// X.1 新表 —— 即使为空也走一次，确保幂等
			"DELETE FROM artifact_items",
			"DELETE FROM artifact_bundles",
			"DELETE FROM app_services",
			// 父表最后
			"DELETE FROM applications",
		}
		for _, s := range stmts {
			if err := tx.Exec(s).Error; err != nil {
				// 新表可能尚未建好（首次启动），忽略 no such table
				if isNoSuchTable(err) {
					slog.Warn("skip (table not exist yet)", "stmt", s, "err", err)
					continue
				}
				return fmt.Errorf("%s: %w", s, err)
			}
			slog.Info("cleaned", "stmt", s)
		}

		// 2. DROP 旧唯一索引：让 AutoMigrate 重启时建新索引时不会冲突
		drops := []string{
			"DROP INDEX IF EXISTS idx_app_host",
			"DROP INDEX IF EXISTS idx_run_host",
		}
		for _, s := range drops {
			if err := tx.Exec(s).Error; err != nil {
				return fmt.Errorf("%s: %w", s, err)
			}
			slog.Info("dropped index", "stmt", s)
		}
		return nil
	})
}

// isNoSuchTable 识别 SQLite "no such table" 错误，便于首次清理时忽略未建的表。
func isNoSuchTable(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "no such table")
}
