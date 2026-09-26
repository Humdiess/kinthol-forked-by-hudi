package ethol

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type tenantStoreDisk struct {
	NextID   int64           `json:"next_id"`
	Accounts []TenantAccount `json:"accounts"`
}

type TenantStore struct {
	mu       sync.RWMutex
	path     string
	nextID   int64
	accounts map[string]TenantAccount
	encKey   [32]byte
}

func NewTenantStore(path, encryptionKey string) (*TenantStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("service state path is required")
	}
	if strings.TrimSpace(encryptionKey) == "" {
		return nil, errors.New("service encryption key is required")
	}

	s := &TenantStore{
		path:     path,
		nextID:   1,
		accounts: make(map[string]TenantAccount),
		encKey:   sha256.Sum256([]byte(encryptionKey)),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *TenantStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read tenant store: %w", err)
	}
	var disk tenantStoreDisk
	if err := json.Unmarshal(data, &disk); err != nil {
		return fmt.Errorf("decode tenant store: %w", err)
	}
	if disk.NextID > 0 {
		s.nextID = disk.NextID
	}
	for _, acc := range disk.Accounts {
		s.accounts[acc.ID] = acc
	}
	return nil
}

func (s *TenantStore) saveLocked() error {
	accounts := make([]TenantAccount, 0, len(s.accounts))
	for _, acc := range s.accounts {
		accounts = append(accounts, acc)
	}
	disk := tenantStoreDisk{NextID: s.nextID, Accounts: accounts}
	data, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tenant store: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create tenant store directory: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write tenant temp store: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("rename tenant store: %w", err)
	}
	return nil
}

func (s *TenantStore) CreateAccountRequest(username, password string, plan SubscriptionPlan) (TenantAccountView, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return TenantAccountView{}, errors.New("username is required")
	}
	if strings.TrimSpace(password) == "" {
		return TenantAccountView{}, errors.New("password is required")
	}
	if !validPlan(plan) {
		return TenantAccountView{}, errors.New("invalid plan")
	}
	ciphertext, err := s.encrypt(password)
	if err != nil {
		return TenantAccountView{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	id := strconv.FormatInt(s.nextID, 10)
	s.nextID++
	now := time.Now().UTC()
	acc := TenantAccount{
		ID:                 id,
		Username:           username,
		PasswordCiphertext: ciphertext,
		Plan:               plan,
		Status:             AccountPending,
		RequestedAt:        now,
		Revision:           1,
	}
	s.accounts[id] = acc
	if err := s.saveLocked(); err != nil {
		return TenantAccountView{}, err
	}
	return accountView(acc), nil
}

func (s *TenantStore) ApproveAccount(accountID string) (TenantAccountView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	acc, ok := s.accounts[accountID]
	if !ok {
		return TenantAccountView{}, errors.New("account not found")
	}
	now := time.Now().UTC()
	dur := planDuration(acc.Plan)
	if dur <= 0 {
		return TenantAccountView{}, errors.New("account has invalid plan")
	}
	start := now
	if acc.SubscriptionEndsAt.After(now) {
		start = acc.SubscriptionEndsAt
	}
	acc.Status = AccountActive
	if acc.ApprovedAt.IsZero() {
		acc.ApprovedAt = now
	}
	acc.SubscriptionEndsAt = start.Add(dur)
	acc.Revision++
	s.accounts[accountID] = acc
	if err := s.saveLocked(); err != nil {
		return TenantAccountView{}, err
	}
	return accountView(acc), nil
}

func (s *TenantStore) SuspendAccount(accountID string) (TenantAccountView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	acc, ok := s.accounts[accountID]
	if !ok {
		return TenantAccountView{}, errors.New("account not found")
	}
	acc.Status = AccountSuspend
	acc.Revision++
	s.accounts[accountID] = acc
	if err := s.saveLocked(); err != nil {
		return TenantAccountView{}, err
	}
	return accountView(acc), nil
}

func (s *TenantStore) ListAccounts() []TenantAccountView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]TenantAccountView, 0, len(s.accounts))
	for _, acc := range s.accounts {
		out = append(out, accountView(acc))
	}
	return out
}

func (s *TenantStore) ActiveCredentials(now time.Time) ([]TenantCredential, error) {
	now = now.UTC()
	s.mu.RLock()
	accounts := make([]TenantAccount, 0, len(s.accounts))
	for _, acc := range s.accounts {
		if acc.Status == AccountActive && acc.SubscriptionEndsAt.After(now) {
			accounts = append(accounts, acc)
		}
	}
	s.mu.RUnlock()

	creds := make([]TenantCredential, 0, len(accounts))
	for _, acc := range accounts {
		password, err := s.decrypt(acc.PasswordCiphertext)
		if err != nil {
			return nil, fmt.Errorf("decrypt account %s: %w", acc.ID, err)
		}
		creds = append(creds, TenantCredential{
			AccountID: acc.ID,
			Username:  acc.Username,
			Password:  password,
			Revision:  acc.Revision,
		})
	}
	return creds, nil
}

func (s *TenantStore) encrypt(plaintext string) (string, error) {
	block, err := aes.NewCipher(s.encKey[:])
	if err != nil {
		return "", fmt.Errorf("init encryption cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("init encryption mode: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	payload := append(nonce, ciphertext...)
	return base64.StdEncoding.EncodeToString(payload), nil
}

func (s *TenantStore) decrypt(ciphertextB64 string) (string, error) {
	payload, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return "", fmt.Errorf("decode encrypted payload: %w", err)
	}
	block, err := aes.NewCipher(s.encKey[:])
	if err != nil {
		return "", fmt.Errorf("init decryption cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("init decryption mode: %w", err)
	}
	n := gcm.NonceSize()
	if len(payload) < n {
		return "", errors.New("encrypted payload too short")
	}
	plaintext, err := gcm.Open(nil, payload[:n], payload[n:], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt payload: %w", err)
	}
	return string(plaintext), nil
}
