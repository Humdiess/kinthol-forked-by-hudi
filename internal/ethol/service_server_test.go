package ethol

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func newTestService(t *testing.T) *MultiTenantService {
	t.Helper()
	cfg := &ServiceConfig{
		Addr:              ":0",
		StatePath:         filepath.Join(t.TempDir(), "service_state.json"),
		AdminToken:        "token123",
		EncryptionKey:     "encryption-key",
		BaseURL:           "http://127.0.0.1:1",
		WorkerConcurrency: 1,
	}
	store, err := NewTenantStore(cfg.StatePath, cfg.EncryptionKey)
	if err != nil {
		t.Fatalf("NewTenantStore: %v", err)
	}
	return &MultiTenantService{
		cfg:     cfg,
		store:   store,
		audit:   NewAuditLogger(cfg.StatePath + ".audit.log"),
		limiter: NewIPRateLimiter(10, 0),
		workers: NewTenantWorkerManager(cfg.BaseURL, cfg.StatePath, cfg.WorkerConcurrency),
	}
}

func TestService_RequestAccount(t *testing.T) {
	svc := newTestService(t)
	h := svc.routes()
	body, _ := json.Marshal(map[string]any{
		"username": "3120600010",
		"password": "secret",
		"plan":     "weekly",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/request-account", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusCreated, rec.Body.String())
	}
}

func TestService_AdminAccountsRequiresAuth(t *testing.T) {
	svc := newTestService(t)
	h := svc.routes()

	req := httptest.NewRequest(http.MethodGet, "/api/admin/accounts", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/admin/accounts", nil)
	req2.Header.Set("Authorization", "Bearer "+svc.cfg.AdminToken)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec2.Code, http.StatusOK, rec2.Body.String())
	}
}
