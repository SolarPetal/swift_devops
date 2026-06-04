package service

import (
	"testing"

	"swift-devops/internal/model"
	"swift-devops/internal/service/pipeline/strategy"
)

func TestAdaptLegacyBundlePlanForwardDefaultItem(t *testing.T) {
	item := &model.ArtifactItem{
		ID:          11,
		BundleID:    7,
		ServiceCode: "default",
		FileName:    "app.jar",
		FilePath:    "/tmp/app.jar",
		FileMD5:     "abc",
		FileSize:    123,
		DockerImage: "demo/default:latest",
	}
	plan := &strategy.Plan{
		App:    &model.Application{ID: 3},
		Bundle: &model.ArtifactBundle{ID: 7},
		ItemByServiceCode: map[string]*model.ArtifactItem{
			"default": item,
		},
	}

	if err := adaptLegacyBundlePlan(plan); err != nil {
		t.Fatalf("adapt: %v", err)
	}
	if plan.Item != item {
		t.Fatalf("plan.Item 未指向 default item")
	}
	if plan.Artifact == nil {
		t.Fatal("plan.Artifact 应被包装出来")
	}
	if plan.Artifact.AppID != 3 || plan.Artifact.FileName != "app.jar" ||
		plan.Artifact.FilePath != "/tmp/app.jar" || plan.Artifact.FileMD5 != "abc" ||
		plan.Artifact.FileSize != 123 {
		t.Fatalf("artifact 包装不正确：%+v", plan.Artifact)
	}
}

func TestAdaptLegacyBundlePlanForwardSingleItemFallback(t *testing.T) {
	item := &model.ArtifactItem{
		ID:          12,
		BundleID:    8,
		ServiceCode: "only-service",
		FileName:    "only.jar",
		FilePath:    "/tmp/only.jar",
		FileMD5:     "def",
		FileSize:    456,
	}
	plan := &strategy.Plan{
		App:    &model.Application{ID: 4},
		Bundle: &model.ArtifactBundle{ID: 8},
		ItemByServiceCode: map[string]*model.ArtifactItem{
			"only-service": item,
		},
	}

	if err := adaptLegacyBundlePlan(plan); err != nil {
		t.Fatalf("single item 应可兜底适配：%v", err)
	}
	if plan.Item != item {
		t.Fatalf("plan.Item 未使用唯一 item 兜底")
	}
	if plan.Artifact == nil || plan.Artifact.FileName != "only.jar" {
		t.Fatalf("artifact 包装不正确：%+v", plan.Artifact)
	}
}

func TestAdaptLegacyBundlePlanRollbackItems(t *testing.T) {
	item := &model.ArtifactItem{
		ID:          21,
		BundleID:    9,
		ServiceCode: "default",
		FileName:    "prev.jar",
		FilePath:    "/tmp/prev.jar",
		FileMD5:     "ghi",
		FileSize:    789,
		DockerImage: "demo/default:prev",
	}
	plan := &strategy.Plan{
		App: &model.Application{ID: 5},
		ItemByDepID: map[uint]*model.ArtifactItem{
			99: item,
		},
	}

	if err := adaptLegacyBundlePlan(plan); err != nil {
		t.Fatalf("adapt rollback: %v", err)
	}
	art := plan.ArtifactByDepID[99]
	if art == nil {
		t.Fatal("rollback ArtifactByDepID 应由 ItemByDepID 包装出来")
	}
	if art.AppID != 5 || art.FileName != "prev.jar" || art.FilePath != "/tmp/prev.jar" ||
		art.FileMD5 != "ghi" || art.FileSize != 789 {
		t.Fatalf("rollback artifact 包装不正确：%+v", art)
	}
	if plan.ItemByDepID[99] != item {
		t.Fatalf("ItemByDepID 应保留，供 Docker 元数据读取")
	}
}
