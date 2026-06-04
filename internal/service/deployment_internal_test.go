package service

import (
	"testing"

	"swift-devops/internal/model"
	"swift-devops/internal/pkg/deploy"
)

func TestEffectiveDeploymentModeUsesApplicationMode(t *testing.T) {
	app := &model.Application{DeployMode: deploy.DeployModeDocker}
	svc := &model.AppService{ServiceCode: "admin-server", DeployMode: deploy.DeployModeSystemd}

	if got := effectiveDeploymentMode(app, svc, svc.ServiceCode); got != deploy.DeployModeDocker {
		t.Fatalf("effectiveDeploymentMode = %q, want app docker", got)
	}
}

func TestEffectiveDeploymentModeDockerBuildModeForcesDocker(t *testing.T) {
	app := &model.Application{BuildMode: "remote-docker", DeployMode: deploy.DeployModeSystemd}

	if got := effectiveDeploymentMode(app, nil, "admin-server"); got != deploy.DeployModeDocker {
		t.Fatalf("effectiveDeploymentMode = %q, want docker for docker build mode", got)
	}
}
