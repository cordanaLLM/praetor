package container

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"
)

func probe(t *testing.T, hs *HealthServer, path string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	if err != nil {
		t.Fatalf("build probe request: %v", err)
	}
	rec := &mockResponseWriter{header: make(http.Header)}
	hs.server.Handler.ServeHTTP(rec, req)
	return rec.statusCode, string(rec.body)
}

func TestHealthServer_Positive_ProbesOverBoundListener(t *testing.T) {
	hs := NewHealthServer("127.0.0.1:0")
	if err := hs.Start(context.Background()); err != nil {
		t.Fatalf("failed to start health server: %v", err)
	}
	defer func() {
		if err := hs.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	}()

	hs.mu.Lock()
	addr := hs.listener.Addr().String()
	hs.mu.Unlock()

	client := &http.Client{Timeout: 5 * time.Second}
	for _, tc := range []struct {
		path string
		body string
	}{
		{"/healthz", "OK\n"},
		{"/livez", "LIVE\n"},
		{"/readyz", "READY\n"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+tc.path, nil)
		if err != nil {
			cancel()
			t.Fatalf("build request for %s: %v", tc.path, err)
		}
		resp, err := client.Do(req)
		if err != nil {
			cancel()
			t.Fatalf("probe %s over the bound listener: %v", tc.path, err)
		}
		body, readErr := io.ReadAll(resp.Body)
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("close body: %v", closeErr)
		}
		cancel()
		if readErr != nil {
			t.Fatalf("read %s body: %v", tc.path, readErr)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("path %s expected 200, got %d", tc.path, resp.StatusCode)
		}
		if string(body) != tc.body {
			t.Errorf("path %s expected body %q, got %q", tc.path, tc.body, string(body))
		}
	}

	if err := hs.ServeErr(); err != nil {
		t.Errorf("expected no serve error, got %v", err)
	}
}

func TestHealthServer_Negative_LivezBeforeStartAndAfterShutdown(t *testing.T) {
	hs := NewHealthServer("127.0.0.1:0")

	if code, body := probe(t, hs, "/livez"); code != http.StatusServiceUnavailable || body != "NOT_STARTED\n" {
		t.Fatalf("before Start expected 503/NOT_STARTED, got %d/%q", code, body)
	}
	if code, _ := probe(t, hs, "/readyz"); code != http.StatusServiceUnavailable {
		t.Fatalf("before Start expected 503 on /readyz, got %d", code)
	}

	if err := hs.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if code, body := probe(t, hs, "/livez"); code != http.StatusOK || body != "LIVE\n" {
		t.Fatalf("after Start expected 200/LIVE, got %d/%q", code, body)
	}

	if err := hs.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if code, body := probe(t, hs, "/livez"); code != http.StatusServiceUnavailable || body != "NOT_STARTED\n" {
		t.Fatalf("after Shutdown expected 503/NOT_STARTED, got %d/%q", code, body)
	}
}

func TestHealthServer_Negative_Readiness(t *testing.T) {
	hs := NewHealthServer("127.0.0.1:0")
	hs.SetReady(false)

	if code, body := probe(t, hs, "/readyz"); code != http.StatusServiceUnavailable || body != "NOT_READY\n" {
		t.Errorf("expected 503/NOT_READY for unready state, got %d/%q", code, body)
	}
}

func TestHealthServer_Negative_StartReportsBindFailure(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve an address: %v", err)
	}
	defer func() {
		if closeErr := occupied.Close(); closeErr != nil {
			t.Errorf("close reserved listener: %v", closeErr)
		}
	}()

	hs := NewHealthServer(occupied.Addr().String())
	startErr := hs.Start(context.Background())
	if startErr == nil {
		if shutdownErr := hs.Shutdown(context.Background()); shutdownErr != nil {
			t.Errorf("shutdown: %v", shutdownErr)
		}
		t.Fatal("expected Start to report the bind failure on an occupied address")
	}
	if !strings.Contains(startErr.Error(), "failed to bind health probes") {
		t.Errorf("unexpected bind error text: %v", startErr)
	}
	if code, _ := probe(t, hs, "/livez"); code != http.StatusServiceUnavailable {
		t.Errorf("a server that never bound must not report LIVE, got %d", code)
	}
}

func TestHealthServer_Negative_DoubleStart(t *testing.T) {
	hs := NewHealthServer("127.0.0.1:0")
	if err := hs.Start(context.Background()); err != nil {
		t.Fatalf("first start: %v", err)
	}
	defer func() {
		if err := hs.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	}()

	if err := hs.Start(context.Background()); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("expected ErrAlreadyStarted on a second Start, got %v", err)
	}
}

func TestHealthServer_Boundary_NilReceivers(t *testing.T) {
	var nilHs *HealthServer

	if err := nilHs.Start(context.Background()); !errors.Is(err, ErrServerNotInitialized) {
		t.Errorf("expected ErrServerNotInitialized from a nil receiver, got %v", err)
	}
	if err := (&HealthServer{}).Start(context.Background()); !errors.Is(err, ErrServerNotInitialized) {
		t.Errorf("expected ErrServerNotInitialized for a zero server, got %v", err)
	}
	if err := nilHs.Shutdown(context.Background()); err != nil {
		t.Errorf("expected nil error on nil server shutdown, got %v", err)
	}
	if err := nilHs.ServeErr(); err != nil {
		t.Errorf("expected nil serve error from a nil receiver, got %v", err)
	}
	nilHs.SetReady(true) // must not panic
}

func TestWaitForGracefulDrain_Boundary_NonPositiveDuration(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Second} {
		err := WaitForGracefulDrain(context.Background(), nil, d)
		if !errors.Is(err, ErrNonPositiveDrain) {
			t.Errorf("drain duration %s: expected ErrNonPositiveDrain, got %v", d, err)
		}
	}
}

func TestWaitForGracefulDrain_Negative_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := WaitForGracefulDrain(ctx, nil, 10*time.Millisecond)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestWaitForGracefulDrain_Positive_SignalDrainsServer(t *testing.T) {
	// Register a guard first so a SIGTERM raised before WaitForGracefulDrain installs its
	// own handler cannot fall through to the default disposition and kill the test binary.
	guard := make(chan os.Signal, 1)
	signal.Notify(guard, syscall.SIGTERM)
	defer signal.Stop(guard)

	hs := NewHealthServer("127.0.0.1:0")
	if err := hs.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- WaitForGracefulDrain(context.Background(), hs, 5*time.Second)
	}()

	// Give the goroutine a moment to register its signal handler before raising SIGTERM.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := raiseTermination(); err != nil {
			t.Skipf("cannot exercise the drain path here: %v", err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("graceful drain: %v", err)
			}
			if code, _ := probe(t, hs, "/readyz"); code != http.StatusServiceUnavailable {
				t.Errorf("after a drain the server must report NOT_READY, got %d", code)
			}
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatal("WaitForGracefulDrain did not return after SIGTERM")
}

type mockResponseWriter struct {
	header     http.Header
	statusCode int
	body       []byte
}

func (m *mockResponseWriter) Header() http.Header {
	return m.header
}

func (m *mockResponseWriter) Write(b []byte) (int, error) {
	m.body = append(m.body, b...)
	return len(b), nil
}

func (m *mockResponseWriter) WriteHeader(statusCode int) {
	m.statusCode = statusCode
}

var _ http.ResponseWriter = (*mockResponseWriter)(nil)
var _ io.Writer = (*mockResponseWriter)(nil)
