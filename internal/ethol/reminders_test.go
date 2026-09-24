package ethol

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReminderEngineCheckOnce(t *testing.T) {
	now := NowWIB()
	start := now.Add(10 * time.Minute).Format("15:04")
	end := now.Add(70 * time.Minute).Format("15:04")
	deadline := now.Add(2 * time.Hour).Format("02-01-2006 15:04")

	dayNames := []string{"Minggu", "Senin", "Selasa", "Rabu", "Kamis", "Jumat", "Sabtu"}
	hari := dayNames[int(now.Weekday())]

	var (
		mu       sync.Mutex
		messages []string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/auth/cas-redirect":
			http.Redirect(w, r, "/cas/login?service=test", http.StatusFound)
		case "/cas/login":
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, `<form id="fm1" action="/cas/login" method="post"><input name="username"/><input name="password"/></form>`)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "ETHOL_SESS", Value: "ok", Path: "/"})
			http.Redirect(w, r, "/api/auth/validasi-token", http.StatusFound)
		case "/api/auth/validasi-token":
			w.Write([]byte(`{"nomor":1001,"nama":"Budi","nipnrp":"3120600001"}`))
		case "/api/auth/config":
			json.NewEncoder(w).Encode(map[string]any{"tahun_aktif": 2024, "semester_aktif": 1})
		case "/api/kuliah":
			json.NewEncoder(w).Encode([]map[string]any{{"nomor": 501, "jenisSchema": 0, "matakuliah": "Algoritma", "dosen": "Dr. Tech"}})
		case "/api/jadwal/jadwal-online":
			json.NewEncoder(w).Encode([]map[string]any{{
				"hari": hari, "jam_awal": start, "jam_akhir": end,
				"matakuliah": "Algoritma", "dosen": "Dr. Tech", "ruang": "A1", "kuliah": 501,
			}})
		case "/api/tugas":
			json.NewEncoder(w).Encode([]map[string]any{{
				"title": "Tugas 1", "deadline": deadline, "matkul": "Algoritma", "kuliah_id": 501,
			}})
		case "/bottoken/sendMessage":
			var payload tgSendMessagePayload
			_ = json.NewDecoder(r.Body).Decode(&payload)
			mu.Lock()
			messages = append(messages, payload.Text)
			mu.Unlock()
			w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	auth := NewAuthManager(client, server.URL, "user", "pass")
	if _, err := auth.Login(ctx); err != nil {
		t.Fatal(err)
	}
	courses := NewCourseManager(client, server.URL, 10*time.Minute)
	academic := NewAcademicManager(client, server.URL, 10*time.Minute)
	notifier := NewTelegramNotifier(client, server.URL, "token", "123")
	re := NewReminderEngine(auth, courses, academic, notifier)

	if got := re.CheckOnce(ctx); got != 2 {
		t.Fatalf("CheckOnce sent %d reminders, want 2", got)
	}
	if got := re.CheckOnce(ctx); got != 0 {
		t.Fatalf("second CheckOnce sent %d reminders, want 0 (deduped)", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(messages))
	}
	joined := messages[0] + messages[1]
	if !strings.Contains(joined, "KELAS SEBENTAR LAGI") || !strings.Contains(joined, "DEADLINE TUGAS") {
		t.Errorf("unexpected reminder messages: %q", messages)
	}
}
