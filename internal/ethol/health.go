package ethol

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"time"
)

func StartHealthServer(ctx context.Context, addr string, scanner *Scanner) error {
	if addr == "" {
		return nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		status := scanner.Status()
		ready := !status.StartTime.IsZero() && status.User != nil
		if ready && status.ScanPlan.Interval != 0 && !status.LastScanTime.IsZero() {
			ready = status.LastScanErr == nil && time.Since(status.LastScanTime) < 30*time.Minute
		}
		if !ready {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ready": ready, "last_scan": status.LastScanTime,
			"successful_scans": status.SuccessfulScans, "failed_scans": status.FailedScans,
			"successful_submits": status.SuccessfulSubmit, "failed_submits": status.FailedSubmit,
		})
	})
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	go func() { _ = server.Serve(listener) }()
	return nil
}
