package schemacheck

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// Bounds of one fetch (HISS-02: every I/O has a context deadline).
const (
	// MaxFetchBytes bounds one fetched schema.
	MaxFetchBytes = 8 << 20
	// FetchTimeout is the deadline Fetch adds when the caller's context has none.
	FetchTimeout = 30 * time.Second
)

// ErrOffline marks a fetch that failed because the network is unreachable, not because the
// source answered badly. SkipOffline turns it into a stated skip (HISS-21).
var ErrOffline = errors.New("network unreachable")

// Fetch reads one URL over HTTP(S). It is the fetch-only path for a schema hosted in a copyleft
// repository: the caller validates against the fetched bytes and never commits them. A schema
// published under a permissive licence is vendored instead (internal/clientschema).
func Fetch(ctx context.Context, rawURL string) ([]byte, error) {
	return fetchWith(ctx, &http.Client{Timeout: FetchTimeout}, rawURL)
}

func fetchWith(ctx context.Context, client *http.Client, rawURL string) (data []byte, err error) {
	if err = checkURL(rawURL); err != nil {
		return nil, err
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, FetchTimeout)
		defer cancel()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, classify(rawURL, err)
	}
	defer func() { err = errors.Join(err, response.Body.Close()) }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %d", rawURL, response.StatusCode)
	}
	data, err = io.ReadAll(io.LimitReader(response.Body, MaxFetchBytes+1))
	if err != nil {
		return nil, classify(rawURL, err)
	}
	if len(data) > MaxFetchBytes {
		return nil, fmt.Errorf("fetch %s: more than %d bytes", rawURL, MaxFetchBytes)
	}
	return data, nil
}

func checkURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return fmt.Errorf("fetch %q: an http(s) URL with a host is required", rawURL)
	}
	return nil
}

func classify(rawURL string, err error) error {
	var dns *net.DNSError
	var op *net.OpError
	if errors.As(err, &dns) || errors.As(err, &op) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("fetch %s: %w: %w", rawURL, ErrOffline, err)
	}
	return fmt.Errorf("fetch %s: %w", rawURL, err)
}

// SkipOffline skips the test with a stated reason when err is ErrOffline, and fails it on any
// other error. A nil err returns.
func SkipOffline(t testing.TB, err error) {
	t.Helper()
	switch {
	case err == nil:
	case errors.Is(err, ErrOffline):
		t.Skipf("offline: %v (the vendored copies are still checked against their pins; this check needs the network)", err)
	default:
		t.Fatal(err)
	}
}
