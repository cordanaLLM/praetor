package schemacheck

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func writeBody(t *testing.T, w http.ResponseWriter, body []byte) {
	t.Helper()
	if _, err := w.Write(body); err != nil {
		t.Errorf("write response: %v", err)
	}
}

func TestFetchPositive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeBody(t, w, []byte(`{"type":"string"}`))
	}))
	defer server.Close()
	data, err := Fetch(context.Background(), server.URL)
	if err != nil || string(data) != `{"type":"string"}` {
		t.Fatalf("Fetch = %q, %v", data, err)
	}
	// A fetched copy validates like a vendored one and is never written anywhere.
	schema, err := Compile("fetched.json", data, nil)
	if err != nil || schema.Validate([]byte(`"x"`)) != nil {
		t.Fatalf("fetched schema unusable: %v", err)
	}
}

func TestFetchNegative(t *testing.T) {
	notFound := httptest.NewServer(http.NotFoundHandler())
	defer notFound.Close()
	if _, err := Fetch(context.Background(), notFound.URL); err == nil || !strings.Contains(err.Error(), "HTTP 404") || errors.Is(err, ErrOffline) {
		t.Errorf("404 = %v, want an HTTP error that is not an offline skip", err)
	}
	for _, bad := range []string{"", "ftp://example.invalid/x", "https://", "::"} {
		if _, err := Fetch(context.Background(), bad); err == nil {
			t.Errorf("Fetch(%q) accepted", bad)
		}
	}
}

func TestFetchBoundary(t *testing.T) {
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeBody(t, w, []byte(strings.Repeat("a", MaxFetchBytes+1)))
	}))
	defer big.Close()
	if _, err := Fetch(context.Background(), big.URL); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Errorf("oversize = %v", err)
	}
	exact := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeBody(t, w, []byte(strings.Repeat("a", MaxFetchBytes)))
	}))
	defer exact.Close()
	if data, err := Fetch(context.Background(), exact.URL); err != nil || len(data) != MaxFetchBytes {
		t.Errorf("exact bound = %d bytes, %v", len(data), err)
	}
}

func TestUnreachableHostIsOffline(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL
	server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := Fetch(ctx, url); !errors.Is(err, ErrOffline) {
		t.Errorf("closed server = %v, want ErrOffline", err)
	}
}

// recorder captures what SkipOffline does without ending the real test.
type recorder struct {
	testing.TB
	skipped, failed string
}

func (r *recorder) Helper()                  {}
func (r *recorder) Skipf(f string, a ...any) { r.skipped = f }
func (r *recorder) Fatal(a ...any)           { r.failed = "fatal" }

func TestSkipOffline(t *testing.T) {
	r := &recorder{TB: t}
	SkipOffline(r, nil)
	SkipOffline(r, ErrOffline)
	if r.skipped == "" || !strings.Contains(r.skipped, "offline") || r.failed != "" {
		t.Fatalf("skipped %q failed %q", r.skipped, r.failed)
	}
	SkipOffline(r, errors.New("HTTP 500"))
	if r.failed == "" {
		t.Error("a non-network error did not fail")
	}
}
