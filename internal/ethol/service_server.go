package ethol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

type MultiTenantService struct {
	cfg     *ServiceConfig
	store   *TenantStore
	audit   *AuditLogger
	limiter *IPRateLimiter
	workers *TenantWorkerManager
}

func StartMultiTenantService(ctx context.Context, cfg *ServiceConfig) error {
	if cfg == nil {
		return errors.New("service config is required")
	}
	store, err := NewTenantStore(cfg.StatePath, cfg.EncryptionKey)
	if err != nil {
		return err
	}

	svc := &MultiTenantService{
		cfg:     cfg,
		store:   store,
		audit:   NewAuditLogger(cfg.StatePath + ".audit.log"),
		limiter: NewIPRateLimiter(10, time.Minute),
		workers: NewTenantWorkerManager(cfg.BaseURL, cfg.StatePath, cfg.WorkerConcurrency),
	}
	go svc.runOrchestrator(ctx)

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           svc.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", cfg.Addr)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		svc.workers.Shutdown()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	slog.Info("Multi-tenant service started", "addr", cfg.Addr, "state_path", cfg.StatePath)
	return server.Serve(listener)
}

func (s *MultiTenantService) runOrchestrator(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	s.syncWorkers()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.syncWorkers()
		}
	}
}

func (s *MultiTenantService) syncWorkers() {
	creds, err := s.store.ActiveCredentials(time.Now().UTC())
	if err != nil {
		slog.Error("Failed to sync worker credentials", "error", err)
		return
	}
	s.workers.Sync(context.Background(), creds)
}

func (s *MultiTenantService) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleHome)
	mux.HandleFunc("/admin", s.handleAdminPage)
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/readyz", s.handleReady)
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/api/request-account", s.handleRequestAccount)
	mux.HandleFunc("/api/admin/accounts", s.handleAdminAccounts)
	mux.HandleFunc("/api/admin/approve", s.handleAdminApprove)
	mux.HandleFunc("/api/admin/suspend", s.handleAdminSuspend)
	return mux
}

func (s *MultiTenantService) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (s *MultiTenantService) handleReady(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ready": true})
}

func (s *MultiTenantService) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	accounts := s.store.ListAccounts()
	active := 0
	for _, acc := range accounts {
		if acc.Status == AccountActive && acc.SubscriptionEndsAt.After(time.Now().UTC()) {
			active++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total_accounts":   len(accounts),
		"active_accounts":  active,
		"active_workers":   s.workers.ActiveWorkerCount(),
		"service_state_dir": filepath.Dir(s.cfg.StatePath),
	})
}

type requestAccountPayload struct {
	Username string           `json:"username"`
	Password string           `json:"password"`
	Plan     SubscriptionPlan `json:"plan"`
}

func (s *MultiTenantService) handleRequestAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	ip := clientIP(r.RemoteAddr)
	if !s.limiter.Allow(ip, time.Now()) {
		_ = s.audit.Log(AuditEvent{Actor: "public", Action: "request_rate_limited", IP: ip})
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
		return
	}
	var payload requestAccountPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON payload"})
		return
	}
	acc, err := s.store.CreateAccountRequest(payload.Username, payload.Password, payload.Plan)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	_ = s.audit.Log(AuditEvent{Actor: "public", Action: "request_account", AccountID: acc.ID, IP: ip, Detail: string(acc.Plan)})
	writeJSON(w, http.StatusCreated, map[string]any{"account": acc})
}

func (s *MultiTenantService) handleAdminAccounts(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": s.store.ListAccounts()})
}

type accountActionPayload struct {
	AccountID string `json:"account_id"`
}

func (s *MultiTenantService) handleAdminApprove(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var payload accountActionPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON payload"})
		return
	}
	acc, err := s.store.ApproveAccount(payload.AccountID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.syncWorkers()
	_ = s.audit.Log(AuditEvent{Actor: "admin", Action: "approve_account", AccountID: acc.ID, IP: clientIP(r.RemoteAddr)})
	writeJSON(w, http.StatusOK, map[string]any{"account": acc})
}

func (s *MultiTenantService) handleAdminSuspend(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var payload accountActionPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON payload"})
		return
	}
	acc, err := s.store.SuspendAccount(payload.AccountID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.syncWorkers()
	_ = s.audit.Log(AuditEvent{Actor: "admin", Action: "suspend_account", AccountID: acc.ID, IP: clientIP(r.RemoteAddr)})
	writeJSON(w, http.StatusOK, map[string]any{"account": acc})
}

func (s *MultiTenantService) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	want := "Bearer " + s.cfg.AdminToken
	if auth != want {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func clientIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

var homeTemplate = template.Must(template.New("home").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>ETHOL Service</title></head>
<body>
<h1>Request ETHOL Service Account</h1>
<p>Isi form, admin akan approve setelah pembayaran.</p>
<form id="req">
  <label>Username ETHOL <input name="username" required></label><br>
  <label>Password ETHOL <input name="password" type="password" required></label><br>
  <label>Plan
    <select name="plan">
      <option value="daily">Daily</option>
      <option value="weekly">Weekly</option>
      <option value="monthly">Monthly</option>
    </select>
  </label><br>
  <button type="submit">Submit Request</button>
</form>
<pre id="out"></pre>
<script>
document.getElementById('req').addEventListener('submit', async (e) => {
  e.preventDefault();
  const f = new FormData(e.target);
  const body = Object.fromEntries(f.entries());
  const res = await fetch('/api/request-account', {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
  const data = await res.json();
  document.getElementById('out').textContent = JSON.stringify(data, null, 2);
});
</script>
<p><a href="/admin">Admin Page</a></p>
</body></html>`))

func (s *MultiTenantService) handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if err := homeTemplate.Execute(w, nil); err != nil {
		http.Error(w, fmt.Sprintf("render home: %v", err), http.StatusInternalServerError)
	}
}

var adminTemplate = template.Must(template.New("admin").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>ETHOL Admin</title></head>
<body>
<h1>Admin Dashboard</h1>
<label>Admin Token <input id="token" type="password"></label>
<button id="load">Load Accounts</button>
<pre id="list"></pre>
<h2>Approve</h2>
<input id="approveID" placeholder="Account ID">
<button id="approve">Approve + Extend by Plan</button>
<h2>Suspend</h2>
<input id="suspendID" placeholder="Account ID">
<button id="suspend">Suspend</button>
<script>
const listEl = document.getElementById('list');
function hdrs(){ return {'Authorization':'Bearer '+document.getElementById('token').value,'Content-Type':'application/json'}; }
async function load(){
  const res = await fetch('/api/admin/accounts',{headers:hdrs()});
  const data = await res.json();
  listEl.textContent = JSON.stringify(data, null, 2);
}
document.getElementById('load').onclick = load;
document.getElementById('approve').onclick = async () => {
  await fetch('/api/admin/approve',{method:'POST',headers:hdrs(),body:JSON.stringify({account_id:document.getElementById('approveID').value})});
  await load();
};
document.getElementById('suspend').onclick = async () => {
  await fetch('/api/admin/suspend',{method:'POST',headers:hdrs(),body:JSON.stringify({account_id:document.getElementById('suspendID').value})});
  await load();
};
</script>
</body></html>`))

func (s *MultiTenantService) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin" {
		http.NotFound(w, r)
		return
	}
	if err := adminTemplate.Execute(w, nil); err != nil {
		http.Error(w, fmt.Sprintf("render admin: %v", err), http.StatusInternalServerError)
	}
}
