package service

import (
	"strings"
	"testing"
)

func TestDeploymentStatusFromDockerContainer(t *testing.T) {
	tests := []struct {
		name     string
		exists   bool
		state    string
		want     string
		contains string
	}{
		{name: "missing", exists: false, want: "stopped", contains: "不存在"},
		{name: "running", exists: true, state: "running", want: "running"},
		{name: "exited", exists: true, state: "exited", want: "stopped"},
		{name: "dead", exists: true, state: "dead", want: "failed"},
		{name: "restarting", exists: true, state: "restarting", want: "failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, detail := deploymentStatusFromDockerContainer("app", HostDockerContainerView{
				Name:  "app",
				State: tt.state,
			}, tt.exists)
			if got != tt.want {
				t.Fatalf("status=%s, want %s (detail=%q)", got, tt.want, detail)
			}
			if tt.contains != "" && !strings.Contains(detail, tt.contains) {
				t.Fatalf("detail=%q should contain %q", detail, tt.contains)
			}
		})
	}
}
