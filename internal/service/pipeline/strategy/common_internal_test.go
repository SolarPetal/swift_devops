package strategy

import (
	"testing"

	"swift-devops/internal/model"
)

func TestHealthProbeDisabled(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"/actuator/health", false},
		{"-", true},
		{"none", true},
		{"OFF", true},
		{" disabled ", true},
		{"skip", true},
	}
	for _, c := range cases {
		if got := healthProbeDisabled(c.in); got != c.want {
			t.Fatalf("healthProbeDisabled(%q)=%v, want %v", c.in, got, c.want)
		}
	}
}

func TestEffectiveDockerContainerName(t *testing.T) {
	app := &model.Application{AppCode: "shop", DockerContainerName: "{{APP_CODE}}-{service}"}
	got, err := effectiveDockerContainerName(&Plan{
		Service: &model.AppService{ServiceCode: "default"},
	}, app)
	if err != nil {
		t.Fatalf("effectiveDockerContainerName: %v", err)
	}
	if got != "shop-default" {
		t.Fatalf("got %q", got)
	}

	got, err = effectiveDockerContainerName(&Plan{
		Service: &model.AppService{ServiceCode: "order", DockerContainerName: "svc-{{SERVICE_CODE}}"},
	}, app)
	if err != nil {
		t.Fatalf("effectiveDockerContainerName service: %v", err)
	}
	if got != "shop-order" {
		t.Fatalf("got service %q", got)
	}
}

func TestResolveServiceConfigUsesAppManagedRuntimeFields(t *testing.T) {
	app := &model.Application{
		AppCode:        "suite",
		BuildMode:      "local-jar",
		DeployMode:     "docker",
		Port:           8080,
		HealthCheckURL: "/app-health",
		JvmArgs:        "-Xmx1024m",
		EnvVars:        `{"FROM":"app"}`,
		SystemdUser:    "appuser",
		JavaPath:       "/opt/jdk/bin/java",
	}
	plan := &Plan{
		App:    app,
		EnvMap: map[string]string{"FROM": "app"},
		Service: &model.AppService{
			ServiceCode:    "admin-server",
			Port:           18080,
			HealthCheckURL: "/service-health",
			JvmArgs:        "-Xmx128m",
			EnvVars:        `{"FROM":"service"}`,
			DeployMode:     "systemd",
		},
	}

	svcCode, port, health, jvm, envMap, systemdUser, javaPath, deployMode := resolveServiceConfig(
		plan,
		app,
		&model.Deployment{},
	)

	if svcCode != "admin-server" || port != 18080 {
		t.Fatalf("service identity mismatch: code=%q port=%d", svcCode, port)
	}
	if health != "/app-health" {
		t.Fatalf("health = %q, want app health", health)
	}
	if jvm != "-Xmx1024m" {
		t.Fatalf("jvm = %q, want app jvm", jvm)
	}
	if envMap["FROM"] != "app" {
		t.Fatalf("env FROM = %q, want app", envMap["FROM"])
	}
	if deployMode != "docker" {
		t.Fatalf("deployMode = %q, want app docker", deployMode)
	}
	if systemdUser != "appuser" || javaPath != "/opt/jdk/bin/java" {
		t.Fatalf("app systemd/java mismatch: user=%q java=%q", systemdUser, javaPath)
	}
}

func TestResolveServiceConfigDockerBuildModeForcesDockerRuntime(t *testing.T) {
	app := &model.Application{
		AppCode:        "suite",
		BuildMode:      "remote-docker",
		DeployMode:     "systemd",
		Port:           8080,
		HealthCheckURL: "/actuator/health",
	}
	plan := &Plan{App: app, Service: &model.AppService{ServiceCode: "admin-server", Port: 18080}}

	_, _, _, _, _, _, _, deployMode := resolveServiceConfig(plan, app, &model.Deployment{})
	if deployMode != "docker" {
		t.Fatalf("deployMode = %q, want docker for docker build mode", deployMode)
	}
}
