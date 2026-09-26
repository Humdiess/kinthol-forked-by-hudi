package ethol

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type AuditEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Actor     string    `json:"actor"`
	Action    string    `json:"action"`
	AccountID string    `json:"account_id,omitempty"`
	IP        string    `json:"ip,omitempty"`
	Detail    string    `json:"detail,omitempty"`
}

type AuditLogger struct {
	mu   sync.Mutex
	path string
}

func NewAuditLogger(path string) *AuditLogger {
	return &AuditLogger{path: path}
}

func (a *AuditLogger) Log(event AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(a.path), 0o755); err != nil {
		return fmt.Errorf("create audit directory: %w", err)
	}
	f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open audit file: %w", err)
	}
	defer func() { _ = f.Close() }()

	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal audit event: %w", err)
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write audit event: %w", err)
	}
	return nil
}
