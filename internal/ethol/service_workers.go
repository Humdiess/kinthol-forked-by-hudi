package ethol

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type workerState struct {
	cancel   context.CancelFunc
	revision int64
}

type TenantWorkerManager struct {
	baseURL     string
	stateRoot   string
	concurrency int

	mu      sync.Mutex
	workers map[string]workerState
}

func NewTenantWorkerManager(baseURL, statePath string, concurrency int) *TenantWorkerManager {
	return &TenantWorkerManager{
		baseURL:     baseURL,
		stateRoot:   statePath + ".tenants",
		concurrency: concurrency,
		workers:     make(map[string]workerState),
	}
}

func (m *TenantWorkerManager) ActiveWorkerCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.workers)
}

func (m *TenantWorkerManager) Sync(ctx context.Context, creds []TenantCredential) {
	desired := make(map[string]TenantCredential, len(creds))
	for _, cred := range creds {
		desired[cred.AccountID] = cred
	}

	m.mu.Lock()
	for id, w := range m.workers {
		cred, ok := desired[id]
		if !ok || cred.Revision != w.revision {
			w.cancel()
			delete(m.workers, id)
		}
	}
	for id, cred := range desired {
		if _, ok := m.workers[id]; ok {
			continue
		}
		tenantCtx, cancel := context.WithCancel(ctx)
		m.workers[id] = workerState{cancel: cancel, revision: cred.Revision}
		go m.runTenantWorker(tenantCtx, cred)
	}
	m.mu.Unlock()
}

func (m *TenantWorkerManager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, w := range m.workers {
		w.cancel()
		delete(m.workers, id)
	}
}

func (m *TenantWorkerManager) runTenantWorker(ctx context.Context, cred TenantCredential) {
	tenantDir := filepath.Join(m.stateRoot, cred.AccountID)
	if err := os.MkdirAll(tenantDir, 0o755); err != nil {
		slog.Error("Failed to create tenant state directory", "account_id", cred.AccountID, "error", err)
		return
	}

	statePath := filepath.Join(tenantDir, "attended_keys.json")
	client, err := NewHTTPClient()
	if err != nil {
		slog.Error("Failed to create tenant HTTP client", "account_id", cred.AccountID, "error", err)
		return
	}
	auth := NewAuthManager(client, m.baseURL, cred.Username, cred.Password)
	courses := NewCourseManager(client, m.baseURL, 10*time.Minute)
	academic := NewAcademicManager(client, m.baseURL, 10*time.Minute)
	presence := NewPresenceEngine(client, m.baseURL)
	state, err := NewExclusiveStateManager(statePath)
	if err != nil {
		slog.Error("Failed to initialize tenant state", "account_id", cred.AccountID, "error", err)
		return
	}
	defer func() { _ = state.Close() }()

	sessionPath := statePath + ".session"
	sessionURLs := []string{m.baseURL, m.baseURL + "/api"}
	if err := LoadCookies(client.Jar, sessionPath, sessionURLs); err != nil {
		slog.Debug("No tenant persisted session restored", "account_id", cred.AccountID, "error", err)
	}
	if _, err := auth.RestoreSession(ctx); err != nil {
		if _, err := auth.Login(ctx); err != nil {
			slog.Error("Tenant login failed", "account_id", cred.AccountID, "error", err)
			return
		}
	}
	if err := SaveCookies(client.Jar, sessionPath, sessionURLs); err != nil {
		slog.Warn("Failed to persist tenant session cookies", "account_id", cred.AccountID, "error", err)
	}

	scanner := NewScanner(auth, courses, presence, academic, state, nil, m.concurrency)
	slog.Info("Tenant worker started", "account_id", cred.AccountID, "username", cred.Username)
	if err := scanner.Run(ctx); err != nil {
		slog.Error("Tenant worker stopped with error", "account_id", cred.AccountID, "error", err)
		return
	}
	slog.Info("Tenant worker stopped", "account_id", cred.AccountID)
}
