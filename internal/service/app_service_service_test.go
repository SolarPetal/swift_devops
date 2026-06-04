package service_test

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"swift-devops/internal/model"
	"swift-devops/internal/pkg/deploy"
	"swift-devops/internal/service"
)

func setupAppServiceService(t *testing.T) (*service.AppServiceService, *gorm.DB, model.Application) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Application{},
		&model.AppService{},
		&model.Deployment{},
		&model.DockerfileTemplate{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	app := model.Application{
		AppCode:        "demo-cloud",
		Name:           "Demo Cloud",
		AppType:        "spring-cloud",
		DeployPath:     "/opt/demo-cloud",
		Port:           8080,
		HealthCheckURL: "/actuator/health",
		BuildMode:      "remote-docker",
		DeployMode:     deploy.DeployModeDocker,
	}
	if err := db.Create(&app).Error; err != nil {
		t.Fatalf("create app: %v", err)
	}
	return service.NewAppServiceService(db), db, app
}

func TestAppServiceBatchImportKeepsEmptyDeployModeForInheritance(t *testing.T) {
	svc, db, app := setupAppServiceService(t)

	out, err := svc.BatchImport(app.ID, service.AppServiceBatchImportInput{
		Items: []service.AppServiceInput{{
			ServiceCode:         "demo-admin",
			Name:                "demo-admin",
			BuildModule:         "demo-admin",
			BuildJarPattern:     "demo-admin/target/*.jar",
			Port:                8081,
			HealthCheckURL:      "/service-health",
			JvmArgs:             "-Xmx128m",
			EnvVars:             `{"FROM":"service"}`,
			DeployMode:          deploy.DeployModeSystemd,
			DockerRegistry:      "registry.service.example.com",
			DockerImageName:     "legacy/{{SERVICE_CODE}}",
			DockerImageTag:      "legacy",
			DockerBuildArgs:     "--build-arg LEGACY=1",
			DockerContainerName: "legacy-{{SERVICE_CODE}}",
			DockerRunArgs:       "--restart=no",
		}},
		Upsert: true,
	})
	if err != nil {
		t.Fatalf("batch import: %v", err)
	}
	if len(out.Items) != 1 {
		t.Fatalf("items len = %d, want 1", len(out.Items))
	}
	if out.Items[0].DeployMode != "" {
		t.Fatalf("view deploy_mode = %q, want empty inheritance", out.Items[0].DeployMode)
	}

	var row model.AppService
	if err := db.Where("app_id = ? AND service_code = ?", app.ID, "demo-admin").First(&row).Error; err != nil {
		t.Fatalf("find imported service: %v", err)
	}
	if row.DeployMode != "" {
		t.Fatalf("stored deploy_mode = %q, want empty inheritance", row.DeployMode)
	}
	if row.HealthCheckURL != "" || row.JvmArgs != "" || row.EnvVars != "" {
		t.Fatalf(
			"stored runtime fields should be app-managed, got health=%q jvm=%q env=%q",
			row.HealthCheckURL,
			row.JvmArgs,
			row.EnvVars,
		)
	}
	if row.DockerRegistry != "" || row.DockerImageName != "" || row.DockerImageTag != "" ||
		row.Dockerfile != "" || row.DockerBuildArgs != "" || row.DockerContainerName != "" ||
		row.DockerRunArgs != "" {
		t.Fatalf("stored service docker overrides should be empty, got %+v", row)
	}
}

func TestAppServiceBatchImportCanClearExistingDeployModeOnUpsert(t *testing.T) {
	svc, db, app := setupAppServiceService(t)
	existing := model.AppService{
		AppID:               app.ID,
		ServiceCode:         "demo-admin",
		Name:                "demo-admin",
		BuildModule:         "demo-admin",
		BuildJarPattern:     "demo-admin/target/*.jar",
		Port:                8081,
		HealthCheckURL:      "/actuator/health",
		DeployMode:          deploy.DeployModeSystemd,
		DockerRegistry:      "registry.service.example.com",
		DockerImageName:     "legacy/{{SERVICE_CODE}}",
		DockerImageTag:      "legacy",
		Dockerfile:          "FROM legacy",
		DockerBuildArgs:     "--build-arg LEGACY=1",
		DockerContainerName: "legacy-{{SERVICE_CODE}}",
		DockerRunArgs:       "--restart=no",
		Enabled:             true,
	}
	if err := db.Create(&existing).Error; err != nil {
		t.Fatalf("create existing service: %v", err)
	}

	_, err := svc.BatchImport(app.ID, service.AppServiceBatchImportInput{
		Items: []service.AppServiceInput{{
			ServiceCode:     "demo-admin",
			Name:            "demo-admin",
			BuildModule:     "demo-admin",
			BuildJarPattern: "demo-admin/target/*.jar",
			Port:            8081,
		}},
		Upsert: true,
	})
	if err != nil {
		t.Fatalf("batch import upsert: %v", err)
	}

	var row model.AppService
	if err := db.Where("app_id = ? AND service_code = ?", app.ID, "demo-admin").First(&row).Error; err != nil {
		t.Fatalf("find upserted service: %v", err)
	}
	if row.DeployMode != "" {
		t.Fatalf("stored deploy_mode after upsert = %q, want empty inheritance", row.DeployMode)
	}
	if row.DockerRegistry != "" || row.DockerImageName != "" || row.DockerImageTag != "" ||
		row.Dockerfile != "" || row.DockerBuildArgs != "" || row.DockerContainerName != "" ||
		row.DockerRunArgs != "" {
		t.Fatalf("stored service docker overrides after upsert should be empty, got %+v", row)
	}
}

func TestAppServiceCreateClearsAppManagedRuntimeFields(t *testing.T) {
	svc, db, app := setupAppServiceService(t)

	out, err := svc.Create(app.ID, service.AppServiceInput{
		ServiceCode:         "demo-worker",
		Name:                "demo-worker",
		BuildModule:         "demo-worker",
		BuildJarPattern:     "demo-worker/target/*.jar",
		Port:                8082,
		HealthCheckURL:      "/service-health",
		JvmArgs:             "-Xmx128m",
		EnvVars:             `{"FROM":"service"}`,
		DeployMode:          deploy.DeployModeSystemd,
		DockerRegistry:      "registry.service.example.com",
		DockerImageName:     "legacy/{{SERVICE_CODE}}",
		DockerImageTag:      "legacy",
		DockerBuildArgs:     "--build-arg LEGACY=1",
		DockerContainerName: "legacy-{{SERVICE_CODE}}",
		DockerRunArgs:       "--restart=no",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if out.HealthCheckURL != "" || out.JvmArgs != "" || out.EnvVars != "" || out.DeployMode != "" {
		t.Fatalf("view runtime fields should be empty, got %+v", out)
	}
	if out.DockerRegistry != "" || out.DockerImageName != "" || out.DockerImageTag != "" ||
		out.Dockerfile != "" || out.DockerBuildArgs != "" || out.DockerContainerName != "" ||
		out.DockerRunArgs != "" {
		t.Fatalf("view service docker overrides should be empty, got %+v", out)
	}

	var row model.AppService
	if err := db.Where("app_id = ? AND service_code = ?", app.ID, "demo-worker").First(&row).Error; err != nil {
		t.Fatalf("find created service: %v", err)
	}
	if row.HealthCheckURL != "" || row.JvmArgs != "" || row.EnvVars != "" || row.DeployMode != "" {
		t.Fatalf(
			"stored runtime fields should be empty, got health=%q jvm=%q env=%q mode=%q",
			row.HealthCheckURL,
			row.JvmArgs,
			row.EnvVars,
			row.DeployMode,
		)
	}
	if row.DockerRegistry != "" || row.DockerImageName != "" || row.DockerImageTag != "" ||
		row.Dockerfile != "" || row.DockerBuildArgs != "" || row.DockerContainerName != "" ||
		row.DockerRunArgs != "" {
		t.Fatalf("stored service docker overrides should be empty, got %+v", row)
	}
}

func TestAppServiceListDoesNotEnsureDefaultService(t *testing.T) {
	svc, _, app := setupAppServiceService(t)

	items, err := svc.List(app.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("空 service 列表不应自动补 default，got %d", len(items))
	}
}
