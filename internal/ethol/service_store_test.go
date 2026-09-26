package ethol

import (
	"path/filepath"
	"testing"
	"time"
)

func TestTenantStore_CreateAndApprove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service_state.json")
	store, err := NewTenantStore(path, "very-secret-key")
	if err != nil {
		t.Fatalf("NewTenantStore: %v", err)
	}

	acc, err := store.CreateAccountRequest("3120600001", "password123", PlanWeekly)
	if err != nil {
		t.Fatalf("CreateAccountRequest: %v", err)
	}
	if acc.Status != AccountPending {
		t.Fatalf("status = %q, want %q", acc.Status, AccountPending)
	}

	all := store.ListAccounts()
	if len(all) != 1 {
		t.Fatalf("len(accounts) = %d, want 1", len(all))
	}

	approved, err := store.ApproveAccount(acc.ID)
	if err != nil {
		t.Fatalf("ApproveAccount: %v", err)
	}
	if approved.Status != AccountActive {
		t.Fatalf("status = %q, want %q", approved.Status, AccountActive)
	}
	if !approved.SubscriptionEndsAt.After(time.Now().UTC()) {
		t.Fatalf("subscription end %v must be in the future", approved.SubscriptionEndsAt)
	}

	creds, err := store.ActiveCredentials(time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveCredentials: %v", err)
	}
	if len(creds) != 1 {
		t.Fatalf("len(creds) = %d, want 1", len(creds))
	}
	if creds[0].Password != "password123" {
		t.Fatalf("decrypted password = %q, want password123", creds[0].Password)
	}
}

func TestTenantStore_Suspend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service_state.json")
	store, err := NewTenantStore(path, "very-secret-key")
	if err != nil {
		t.Fatalf("NewTenantStore: %v", err)
	}
	acc, err := store.CreateAccountRequest("3120600002", "password321", PlanDaily)
	if err != nil {
		t.Fatalf("CreateAccountRequest: %v", err)
	}
	if _, err := store.ApproveAccount(acc.ID); err != nil {
		t.Fatalf("ApproveAccount: %v", err)
	}
	if _, err := store.SuspendAccount(acc.ID); err != nil {
		t.Fatalf("SuspendAccount: %v", err)
	}
	creds, err := store.ActiveCredentials(time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveCredentials: %v", err)
	}
	if len(creds) != 0 {
		t.Fatalf("len(creds) = %d, want 0", len(creds))
	}
}
