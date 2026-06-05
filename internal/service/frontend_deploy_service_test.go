package service

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"swift-devops/internal/model"
)

func TestNormalizeFrontendAppConfigDefaults(t *testing.T) {
	app := &model.Application{ID: 7, AppCode: "portal"}
	got, err := normalizeFrontendAppConfig(app, FrontendAppConfigInput{})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got.ServiceCode != "web" || got.PackageManager != "auto" || got.BuildCommand != "npm run build" {
		t.Fatalf("unexpected defaults: %+v", got)
	}
	if got.ContainerName != "sd-fe-portal-web" {
		t.Fatalf("container=%q", got.ContainerName)
	}
	if !got.SPAFallback {
		t.Fatal("spa_fallback should default true")
	}
}

func TestRenderFrontendDockerfileAutoPackageManager(t *testing.T) {
	app := &model.Application{AppCode: "portal"}
	cfg, err := normalizeFrontendAppConfig(app, FrontendAppConfigInput{DistDir: "build"})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	got := renderFrontendDockerfile(app, cfg)
	needles := []string{
		"FROM node:20-alpine AS builder",
		"pnpm-lock.yaml",
		"yarn.lock",
		"RUN npm run build",
		"COPY --from=builder /app/build /usr/share/nginx/html",
		"FROM nginx:1.27-alpine",
	}
	for _, needle := range needles {
		if !strings.Contains(got, needle) {
			t.Fatalf("Dockerfile missing %q:\n%s", needle, got)
		}
	}
}

func TestRenderFrontendRuntimeNginxConfigSPAFallback(t *testing.T) {
	cfg := &model.FrontendAppConfig{SPAFallback: true}
	got := renderFrontendRuntimeNginxConfig(cfg)
	if !strings.Contains(got, "try_files $uri $uri/ /index.html;") {
		t.Fatalf("expected SPA fallback:\n%s", got)
	}
	cfg.SPAFallback = false
	got = renderFrontendRuntimeNginxConfig(cfg)
	if !strings.Contains(got, "try_files $uri $uri/ =404;") {
		t.Fatalf("expected no SPA fallback:\n%s", got)
	}
}

func TestCreateTarGzSkipsHeavyDirs(t *testing.T) {
	dir := t.TempDir()
	mustWrite := func(name, content string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("package.json", "{}")
	mustWrite("src/main.ts", "console.log(1)")
	mustWrite("node_modules/pkg/index.js", "skip")
	mustWrite("dist/index.html", "skip")
	archive := filepath.Join(t.TempDir(), "frontend.tar.gz")
	if err := createTarGz(dir, archive); err != nil {
		t.Fatalf("create tar: %v", err)
	}
	got := tarNames(t, archive)
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "package.json") || !strings.Contains(joined, "src/main.ts") {
		t.Fatalf("archive missing expected files: %v", got)
	}
	if strings.Contains(joined, "node_modules") || strings.Contains(joined, "dist/index.html") {
		t.Fatalf("archive should skip heavy dirs: %v", got)
	}
}

func tarNames(t *testing.T, archive string) []string {
	t.Helper()
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var out []string
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		out = append(out, hdr.Name)
	}
	return out
}
