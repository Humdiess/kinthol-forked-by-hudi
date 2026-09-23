package ethol

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const presenceSubmitRetries = 2

type PresenceEngine struct {
	client  *http.Client
	baseURL string
}

func NewPresenceEngine(client *http.Client, baseURL string) *PresenceEngine {
	if baseURL == "" {
		baseURL = "https://ethol.pens.ac.id"
	}
	return &PresenceEngine{
		client:  client,
		baseURL: strings.TrimRight(baseURL, "/"),
	}
}

func (pe *PresenceEngine) CheckCourse(ctx context.Context, c Course) (string, bool, error) {
	url := fmt.Sprintf("%s/api/presensi/aktif-kuliah?kuliah=%d&jenis_schema=%d", pe.baseURL, c.Nomor, c.JenisSchema)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", false, fmt.Errorf("create presence check req: %w", err)
	}

	resp, err := pe.client.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("check presence request: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusUnauthorized {
		return "", false, ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("check presence HTTP %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	if err != nil {
		return "", false, fmt.Errorf("read presence check: %w", err)
	}

	key := extractPresenceKey(raw)
	devLog("Presence checked", "course", c.CourseName(), "raw_len", len(raw), "key_found", key != "")
	if key != "" {
		return key, true, nil
	}
	return "", false, nil
}

type presenceSubmitPayload struct {
	Kuliah      int    `json:"kuliah"`
	JenisSchema int    `json:"jenis_schema"`
	Mahasiswa   int    `json:"mahasiswa"`
	Key         string `json:"key"`
	KuliahAsal  int    `json:"kuliah_asal"`
}

type presenceSubmitResponse struct {
	Sukses  any `json:"sukses"`
	Success any `json:"success"`
	Status  any `json:"status"`
	Pesan   any `json:"pesan"`
	Message any `json:"message"`
}

func (pe *PresenceEngine) Submit(ctx context.Context, c Course, key string, studentID int) (string, bool, error) {
	payload := presenceSubmitPayload{
		Kuliah:      c.Nomor,
		JenisSchema: c.JenisSchema,
		Mahasiswa:   studentID,
		Key:         key,
		KuliahAsal:  c.KuliahAsal,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return "", false, fmt.Errorf("marshal presence payload: %w", err)
	}
	devLog("Submitting presence payload", "course", c.CourseName())

	url := pe.baseURL + "/api/presensi/mahasiswa"
	resp, err := pe.submitWithRetry(ctx, url, data)
	if err != nil {
		return "", false, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusUnauthorized {
		return "", false, ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("submit presence HTTP %d", resp.StatusCode)
	}

	var res presenceSubmitResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&res); err != nil {
		return "", false, fmt.Errorf("decode submit response: %w", err)
	}

	msg := "Berhasil"
	if res.Pesan != nil {
		msg = fmt.Sprintf("%v", res.Pesan)
	} else if res.Message != nil {
		msg = fmt.Sprintf("%v", res.Message)
	}

	isSuccess := classifyPresenceSuccess(res, msg)
	devLog("Presence submitted", "course", c.CourseName(), "is_success", isSuccess, "msg", msg)

	return msg, isSuccess, nil
}

// presenceFailureWords mark a response as failed even when it mentions "sudah"
// (e.g. "kelas sudah ditutup"), preventing false success records.
var presenceFailureWords = []string{
	"gagal", "tidak", "belum", "bukan", "error", "invalid",
	"ditutup", "kadaluarsa", "expired", "salah", "ditolak", "dibatalkan", "batal",
}

// presenceRecordedWords qualify "sudah" as an idempotent already-recorded result.
var presenceRecordedWords = []string{
	"presensi", "absen", "hadir", "dilakukan", "tercatat", "melakukan", "disimpan",
}

func containsAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// classifyPresenceSuccess trusts explicit API status fields, then falls back to
// message text. The text fallback requires a success or already-recorded phrase
// and rejects failure indicators, so an error mentioning "sudah" is not success.
func classifyPresenceSuccess(res presenceSubmitResponse, msg string) bool {
	if b, ok := res.Sukses.(bool); ok && b {
		return true
	}
	if b, ok := res.Success.(bool); ok && b {
		return true
	}
	if s := fmt.Sprintf("%v", res.Status); s == "200" || s == "true" {
		return true
	}

	lower := strings.ToLower(msg)
	if strings.Contains(lower, "berhasil") {
		return true
	}
	if containsAny(lower, presenceFailureWords) {
		return false
	}
	return strings.Contains(lower, "sudah") && containsAny(lower, presenceRecordedWords)
}

// submitWithRetry posts the presence payload, retrying transient gateway errors
// and transport failures. Retry is safe: the server dedupes by key/student.
func (pe *PresenceEngine) submitWithRetry(ctx context.Context, url string, data []byte) (*http.Response, error) {
	var lastStatus int
	for attempt := 0; attempt <= presenceSubmitRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("create presence submit req: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := pe.client.Do(req)
		if err != nil {
			if ctx.Err() != nil || attempt == presenceSubmitRetries {
				return nil, fmt.Errorf("submit presence request: %w", err)
			}
		} else if !retryablePresenceStatus(resp.StatusCode) || attempt == presenceSubmitRetries {
			return resp, nil
		} else {
			lastStatus = resp.StatusCode
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
		}

		timer := time.NewTimer(transportRetryBase * time.Duration(1<<attempt))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, fmt.Errorf("submit presence HTTP %d", lastStatus)
}

func retryablePresenceStatus(code int) bool {
	return code == http.StatusBadGateway || code == http.StatusServiceUnavailable || code == http.StatusGatewayTimeout
}

func extractPresenceKey(raw []byte) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) <= 2 {
		return ""
	}

	type keyItem struct {
		Key string `json:"key"`
	}

	switch trimmed[0] {
	case '[':
		var list []keyItem
		if err := json.Unmarshal(trimmed, &list); err == nil {
			if len(list) > 0 && list[0].Key != "" {
				return list[0].Key
			}
			for _, item := range list {
				if item.Key != "" {
					return item.Key
				}
			}
		}
	case '{':
		var single keyItem
		if err := json.Unmarshal(trimmed, &single); err == nil {
			return single.Key
		}
	}

	return ""
}
