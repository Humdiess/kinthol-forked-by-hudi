package ethol

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestHealthServerReportsNotReadyBeforeAuthentication(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	_ = probe.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	scanner := NewScanner(nil, nil, nil, nil, nil, nil, 1)
	if err := StartHealthServer(ctx, addr, scanner); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: time.Second}
	var resp *http.Response
	for range 20 {
		resp, err = client.Get("http://" + addr + "/readyz")
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("ready status = %d, want %d", resp.StatusCode, http.StatusServiceUnavailable)
	}
}
