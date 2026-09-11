package container

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestHealthServer_Positive_Probes(t *testing.T) {
	hs := NewHealthServer("127.0.0.1:0")
	if err := hs.Start(); err != nil {
		t.Fatalf("failed to start health server: %v", err)
	}
	defer func() {
		_ = hs.Shutdown(context.Background())
	}()

	// Test request handlers directly via http.Handler
	handler := hs.server.Handler

	testCases := []struct {
		path         string
		expectedCode int
	}{
		{"/healthz", http.StatusOK},
		{"/livez", http.StatusOK},
		{"/readyz", http.StatusOK},
	}

	for _, tc := range testCases {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, tc.path, nil)
		rec := &mockResponseWriter{header: make(http.Header)}
		handler.ServeHTTP(rec, req)

		if rec.statusCode != tc.expectedCode {
			t.Errorf("path %s expected code %d, got %d", tc.path, tc.expectedCode, rec.statusCode)
		}
	}
}

func TestHealthServer_Negative_Readiness(t *testing.T) {
	hs := NewHealthServer("127.0.0.1:0")
	hs.SetReady(false)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/readyz", nil)
	rec := &mockResponseWriter{header: make(http.Header)}
	hs.server.Handler.ServeHTTP(rec, req)

	if rec.statusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for unready state, got %d", rec.statusCode)
	}
}

func TestHealthServer_Boundary_NilServerAndContext(t *testing.T) {
	var nilHs *HealthServer
	if err := nilHs.Shutdown(context.Background()); err != nil {
		t.Errorf("expected nil error on nil server shutdown, got %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := WaitForGracefulDrain(ctx, nil, 10*time.Millisecond)
	if err == nil {
		t.Error("expected error when context is already cancelled, got nil")
	}
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
