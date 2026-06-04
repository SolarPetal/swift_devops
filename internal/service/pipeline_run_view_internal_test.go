package service

import (
	"testing"
	"time"

	"swift-devops/internal/model"
)

func TestToRunViewIncludesBundleIDs(t *testing.T) {
	now := time.Date(2026, 6, 2, 14, 50, 0, 0, time.UTC)
	run := &model.PipelineRun{
		ID: 9, AppID: 2,
		ArtifactID:       0,
		BundleID:         3,
		PreviousBundleID: 1,
		Strategy:         "single",
		Status:           "success",
		StateSnapshot:    `{"steps":[]}`,
		TriggeredBy:      "admin",
		StartedAt:        &now,
		FinishedAt:       &now,
		CreatedAt:        now,
	}

	view := toRunView(run)
	if view.BundleID != 3 {
		t.Fatalf("BundleID=%d, want 3", view.BundleID)
	}
	if view.PreviousBundleID != 1 {
		t.Fatalf("PreviousBundleID=%d, want 1", view.PreviousBundleID)
	}
}
