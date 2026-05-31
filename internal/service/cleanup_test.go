package service_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"swift-devops/internal/model"
	"swift-devops/internal/service"
)

// setupBundleSvc 起一个带 Bundle/Item/Deployment/PipelineRun 全表的内存库（Sprint 5.7 清理测试用）。
func setupBundleSvc(t *testing.T) (*service.ArtifactService, *gorm.DB, string) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.Application{}, &model.Artifact{},
		&model.ArtifactBundle{}, &model.ArtifactItem{},
		&model.Deployment{}, &model.PipelineRun{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	root := t.TempDir()
	return service.NewArtifactService(db, root, 1<<20), db, root
}

// ingestN 造 n 个单 service Bundle，返回它们的 bundle id（创建顺序，id 递增）。
func ingestN(t *testing.T, svc *service.ArtifactService, appID uint, n int) []uint {
	t.Helper()
	srcDir := t.TempDir()
	var ids []uint
	for i := 0; i < n; i++ {
		src := filepath.Join(srcDir, fmt.Sprintf("svc-%d.jar", i))
		if err := os.WriteFile(src, []byte(fmt.Sprintf("jar-content-%d", i)), 0o644); err != nil {
			t.Fatalf("write src %d: %v", i, err)
		}
		bv, err := svc.IngestBundle(appID, fmt.Sprintf("v%d", i), fmt.Sprintf("sha%d", i), "tester", "",
			[]service.BundleItemInput{{ServiceCode: "default", LocalPath: src}})
		if err != nil {
			t.Fatalf("ingest %d: %v", i, err)
		}
		ids = append(ids, bv.ID)
	}
	return ids
}

func bundleCount(db *gorm.DB, appID uint) int64 {
	var n int64
	db.Model(&model.ArtifactBundle{}).Where("app_id = ?", appID).Count(&n)
	return n
}

// 滚动删除：keep=3，5 个 Bundle 应删最旧 2 个，文件释放 > 0。
func TestCleanupBundleHistory_Roll(t *testing.T) {
	svc, db, _ := setupBundleSvc(t)
	appID := seedApp(t, db)
	ids := ingestN(t, svc, appID, 5)

	// 删除前先记下最旧两个 bundle 的 item 文件路径，验证文件真的没了
	var oldItems []model.ArtifactItem
	db.Where("bundle_id IN ?", []uint{ids[0], ids[1]}).Find(&oldItems)
	if len(oldItems) != 2 {
		t.Fatalf("setup: expect 2 old items, got %d", len(oldItems))
	}

	res, err := svc.CleanupBundleHistory(appID, 3)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if len(res.DeletedBundles) != 2 {
		t.Fatalf("expect delete 2, got %d (%v)", len(res.DeletedBundles), res.DeletedBundles)
	}
	if got := bundleCount(db, appID); got != 3 {
		t.Fatalf("expect 3 bundles left, got %d", got)
	}
	if res.FreedBytes <= 0 {
		t.Fatalf("expect freed bytes > 0, got %d", res.FreedBytes)
	}
	for _, it := range oldItems {
		if _, err := os.Stat(it.FilePath); !os.IsNotExist(err) {
			t.Fatalf("expect file gone: %s (err=%v)", it.FilePath, err)
		}
	}
}

// 引用保护：被 Deployment 引用的旧 Bundle 必须跳过，不能删（否则回滚断链）。
func TestCleanupBundleHistory_SkipInUse(t *testing.T) {
	svc, db, _ := setupBundleSvc(t)
	appID := seedApp(t, db)
	ids := ingestN(t, svc, appID, 3) // ids[0] 最旧

	// 让最旧 Bundle 的 item 被一个 Deployment 当前版引用
	var item model.ArtifactItem
	if err := db.Where("bundle_id = ?", ids[0]).First(&item).Error; err != nil {
		t.Fatalf("load item: %v", err)
	}
	if err := db.Create(&model.Deployment{
		AppID: appID, HostID: 1, ServiceCode: "default",
		CurrentArtifactItemID: item.ID, Status: "running",
	}).Error; err != nil {
		t.Fatalf("create deployment: %v", err)
	}

	// keep=1：保留 ids[2]，候选 ids[1]、ids[0]；ids[0] 被引用 → 跳过，只删 ids[1]
	res, err := svc.CleanupBundleHistory(appID, 1)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if len(res.SkippedInUse) != 1 || res.SkippedInUse[0] != ids[0] {
		t.Fatalf("expect skip bundle %d, got %v", ids[0], res.SkippedInUse)
	}
	if len(res.DeletedBundles) != 1 || res.DeletedBundles[0] != ids[1] {
		t.Fatalf("expect delete bundle %d, got %v", ids[1], res.DeletedBundles)
	}
	// 被引用的 Bundle 仍在
	var n int64
	db.Model(&model.ArtifactBundle{}).Where("id = ?", ids[0]).Count(&n)
	if n != 1 {
		t.Fatalf("in-use bundle %d should remain", ids[0])
	}
}

// PipelineRun 回滚指针引用也要保护。
func TestCleanupBundleHistory_SkipPipelineRef(t *testing.T) {
	svc, db, _ := setupBundleSvc(t)
	appID := seedApp(t, db)
	ids := ingestN(t, svc, appID, 3)

	// previous_bundle_id 指向最旧 Bundle（回滚指针）
	if err := db.Create(&model.PipelineRun{
		AppID: appID, PreviousBundleID: ids[0], Strategy: "rollback", Status: "success",
	}).Error; err != nil {
		t.Fatalf("create run: %v", err)
	}

	res, err := svc.CleanupBundleHistory(appID, 1)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	for _, d := range res.DeletedBundles {
		if d == ids[0] {
			t.Fatalf("bundle %d referenced by pipeline run must not be deleted", ids[0])
		}
	}
}

// keep<1 兜底成 1，绝不清空一个 app 的全部制品。
func TestCleanupBundleHistory_KeepFloor(t *testing.T) {
	svc, db, _ := setupBundleSvc(t)
	appID := seedApp(t, db)
	ingestN(t, svc, appID, 2)

	res, err := svc.CleanupBundleHistory(appID, 0)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if res.Keep != 1 {
		t.Fatalf("expect keep floored to 1, got %d", res.Keep)
	}
	if got := bundleCount(db, appID); got != 1 {
		t.Fatalf("expect 1 bundle left (never empty), got %d", got)
	}
}

// 总数不超过 keep 时啥也不删。
func TestCleanupBundleHistory_Noop(t *testing.T) {
	svc, db, _ := setupBundleSvc(t)
	appID := seedApp(t, db)
	ingestN(t, svc, appID, 2)

	res, err := svc.CleanupBundleHistory(appID, 5)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if len(res.DeletedBundles) != 0 {
		t.Fatalf("expect no deletion, got %v", res.DeletedBundles)
	}
	if got := bundleCount(db, appID); got != 2 {
		t.Fatalf("expect 2 bundles intact, got %d", got)
	}
}
