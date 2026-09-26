package ethol

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadServiceConfig(t *testing.T) {
	t.Setenv("ETHOL_SERVICE_ADDR", ":9999")
	t.Setenv("ETHOL_SERVICE_ADMIN_TOKEN", "admintoken")
	t.Setenv("ETHOL_SERVICE_ENCRYPTION_KEY", "encryption-key")

	cfg, err := LoadServiceConfig("", ServiceConfig{})
	if err != nil {
		t.Fatalf("LoadServiceConfig returned error: %v", err)
	}
	if cfg.Addr != ":9999" {
		t.Fatalf("addr = %q, want %q", cfg.Addr, ":9999")
	}
	if cfg.WorkerConcurrency != 4 {
		t.Fatalf("worker concurrency = %d, want 4", cfg.WorkerConcurrency)
	}
	if cfg.StatePath == "" {
		t.Fatal("state path must be defaulted")
	}
}

func TestLoadServiceConfigFromFileAndOverride(t *testing.T) {
	tmp := t.TempDir()
	envPath := filepath.Join(tmp, ".env")
	data := []byte("ETHOL_SERVICE_ADDR=:8089\nETHOL_SERVICE_ADMIN_TOKEN=fromfile\nETHOL_SERVICE_ENCRYPTION_KEY=filekey\nETHOL_SERVICE_WORKER_CONCURRENCY=3\n")
	if err := os.WriteFile(envPath, data, 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	cfg, err := LoadServiceConfig(envPath, ServiceConfig{AdminToken: "override"})
	if err != nil {
		t.Fatalf("LoadServiceConfig returned error: %v", err)
	}
	if cfg.AdminToken != "override" {
		t.Fatalf("admin token = %q, want override", cfg.AdminToken)
	}
	if cfg.WorkerConcurrency != 3 {
		t.Fatalf("worker concurrency = %d, want 3", cfg.WorkerConcurrency)
	}
}
