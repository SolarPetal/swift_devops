package service

import (
	"strings"
	"testing"

	"swift-devops/internal/model"
)

func TestNormalizeFrontendGatewayInputDefaults(t *testing.T) {
	got, err := normalizeFrontendGatewayInput(FrontendGatewayEnsureInput{})
	if err != nil {
		t.Fatalf("normalize defaults: %v", err)
	}
	if got.ContainerName != defaultFrontendGatewayContainer {
		t.Fatalf("container=%q", got.ContainerName)
	}
	if got.NetworkName != defaultFrontendGatewayNetwork {
		t.Fatalf("network=%q", got.NetworkName)
	}
	if got.Image != defaultFrontendGatewayImage {
		t.Fatalf("image=%q", got.Image)
	}
	if got.HTTPPort != 80 || got.HTTPSPort != 443 {
		t.Fatalf("ports=%d/%d", got.HTTPPort, got.HTTPSPort)
	}
	if got.ConfigDir != "/opt/swift-devops/gateway/nginx/conf.d" {
		t.Fatalf("config_dir=%q", got.ConfigDir)
	}
}

func TestNormalizeFrontendDomain(t *testing.T) {
	got, err := normalizeFrontendDomain("WWW.Example.COM.")
	if err != nil {
		t.Fatalf("normalize domain: %v", err)
	}
	if got != "www.example.com" {
		t.Fatalf("domain=%q", got)
	}
	bad := []string{"http://www.example.com", "www.example.com:8080", "localhost", "bad_domain.example.com"}
	for _, raw := range bad {
		t.Run(raw, func(t *testing.T) {
			if _, err := normalizeFrontendDomain(raw); err == nil {
				t.Fatalf("expected invalid domain for %q", raw)
			}
		})
	}
}

func TestRenderFrontendGatewayRouteConfigHTTP(t *testing.T) {
	route := &model.FrontendGatewayRoute{
		ID:            12,
		Domain:        "www.xxx.top",
		ServiceCode:   "web",
		ContainerName: "sd-fe-www-web",
		TargetPort:    80,
		Enabled:       true,
	}
	got, err := renderFrontendGatewayRouteConfig(route)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	needles := []string{
		"upstream sd_fe_route_12",
		"server sd-fe-www-web:80;",
		"server_name www.xxx.top;",
		"proxy_pass http://sd_fe_route_12;",
		"proxy_set_header X-Forwarded-Proto $scheme;",
	}
	for _, needle := range needles {
		if !strings.Contains(got, needle) {
			t.Fatalf("config missing %q:\n%s", needle, got)
		}
	}
	if strings.Contains(got, "listen 443") {
		t.Fatalf("http config should not include 443:\n%s", got)
	}
}

func TestRenderFrontendGatewayRouteConfigHTTPS(t *testing.T) {
	route := &model.FrontendGatewayRoute{
		ID:            13,
		Domain:        "h5.xxx.top",
		ServiceCode:   "web",
		ContainerName: "sd-fe-h5-web",
		TargetPort:    80,
		HTTPS:         true,
		CertPath:      "/etc/nginx/certs/h5/fullchain.pem",
		KeyPath:       "/etc/nginx/certs/h5/privkey.pem",
		Enabled:       true,
	}
	got, err := renderFrontendGatewayRouteConfig(route)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	needles := []string{
		"listen 80;",
		"return 301 https://$host$request_uri;",
		"listen 443 ssl http2;",
		"ssl_certificate /etc/nginx/certs/h5/fullchain.pem;",
		"ssl_certificate_key /etc/nginx/certs/h5/privkey.pem;",
	}
	for _, needle := range needles {
		if !strings.Contains(got, needle) {
			t.Fatalf("config missing %q:\n%s", needle, got)
		}
	}
}

func TestBuildFrontendGatewayConfigFilesSkipsDisabledRoutes(t *testing.T) {
	files, err := buildFrontendGatewayConfigFiles([]model.FrontendGatewayRoute{
		{ID: 1, Domain: "www.xxx.top", ServiceCode: "web", ContainerName: "sd-fe-www-web", TargetPort: 80, Enabled: true},
		{ID: 2, Domain: "old.xxx.top", ServiceCode: "web", ContainerName: "sd-fe-old-web", TargetPort: 80, Enabled: false},
	})
	if err != nil {
		t.Fatalf("build files: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("files len=%d, want default + one enabled", len(files))
	}
	joined := files[0].Content + files[1].Content
	if strings.Contains(joined, "old.xxx.top") {
		t.Fatalf("disabled route should not be rendered:\n%s", joined)
	}
}

func testBoolPtr(v bool) *bool { return &v }

func TestEffectiveRouteHTTPS(t *testing.T) {
	current := &model.FrontendGatewayRoute{HTTPS: true}
	if !effectiveRouteHTTPS(nil, current) {
		t.Fatal("nil input should preserve current https=true")
	}
	if effectiveRouteHTTPS(testBoolPtr(false), current) {
		t.Fatal("explicit false should override current https=true")
	}
}
