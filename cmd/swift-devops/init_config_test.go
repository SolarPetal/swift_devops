package main

import (
	"os"
	"path/filepath"
	"testing"

	"swift-devops/internal/config"
)

func TestWriteInitialConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	generatedPassword, err := writeInitialConfig(initConfigOptions{
		ConfigPath: cfgPath,
		DataDir:    filepath.Join(dir, "data"),
		LogDir:     filepath.Join(dir, "logs"),
		Port:       18088,
		AdminUser:  "admin",
	})
	if err != nil {
		t.Fatalf("writeInitialConfig: %v", err)
	}
	if generatedPassword == "" {
		t.Fatal("expected generated password")
	}
	if _, err := os.Stat(filepath.Join(dir, "initial-admin-password.txt")); err != nil {
		t.Fatalf("password file missing: %v", err)
	}
	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load generated config: %v", err)
	}
	if loaded.Server.Port != 18088 {
		t.Fatalf("unexpected port: %d", loaded.Server.Port)
	}
	if loaded.Admin.Username != "admin" || loaded.Admin.PasswordBcrypt == "" {
		t.Fatalf("admin config not generated: %+v", loaded.Admin)
	}
	if loaded.Storage.MaxUploadMB != 256 {
		t.Fatalf("max_upload_mb default not written: %d", loaded.Storage.MaxUploadMB)
	}
}

func TestWriteInitialConfigRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("exists"), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	_, err := writeInitialConfig(initConfigOptions{
		ConfigPath: cfgPath,
		DataDir:    filepath.Join(dir, "data"),
		LogDir:     filepath.Join(dir, "logs"),
		Port:       18088,
		AdminUser:  "admin",
	})
	if err == nil {
		t.Fatal("expected overwrite refusal")
	}
}
