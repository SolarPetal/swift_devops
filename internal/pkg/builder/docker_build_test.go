package builder

import "testing"

func TestResolveDockerImageNameDefaultFallback(t *testing.T) {
	got := ResolveDockerImageName(Plan{AppCode: "test"}, ServiceBuildSpec{}, "default", "abcdef123456")
	if got != "test/default:latest" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveDockerImageNameConfiguredTemplate(t *testing.T) {
	plan := Plan{
		AppCode:         "car",
		BuildID:         42,
		DockerRegistry:  "registry.example.com",
		DockerImageName: "team/{{APP_CODE}}-{{SERVICE_CODE}}",
		DockerImageTag:  "git-{sha}-{build_id}",
	}
	got := ResolveDockerImageName(plan, ServiceBuildSpec{}, "admin", "abcdef123456")
	want := "registry.example.com/team/car-admin:git-abcdef1-42"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestResolveDockerImageNameIgnoresServiceDockerOverride(t *testing.T) {
	plan := Plan{
		AppCode:         "car",
		BuildID:         42,
		DockerRegistry:  "registry.app.example.com",
		DockerImageName: "team/{{APP_CODE}}-{{SERVICE_CODE}}",
		DockerImageTag:  "git-{sha}-{build_id}",
	}
	sp := ServiceBuildSpec{
		ServiceCode:     "admin",
		DockerRegistry:  "registry.service.example.com",
		DockerImageName: "legacy/{{SERVICE_CODE}}",
		DockerImageTag:  "legacy",
	}
	got := ResolveDockerImageName(plan, sp, "admin", "abcdef123456")
	want := "registry.app.example.com/team/car-admin:git-abcdef1-42"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
