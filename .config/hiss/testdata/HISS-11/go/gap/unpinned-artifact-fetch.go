package p

import (
	"context"
	"io"
	"net/http"
	"time"
)

// HISS-11: the artifact is fetched from a floating "latest" URL and is never checked
// against a pinned digest, so the bytes that enter the build are whatever the remote
// served at that moment. Every other invariant is satisfied deliberately: the request
// is context-bounded, the client carries an explicit timeout, the read is byte-bounded
// and the close error is handled, so a finding here could only be HISS-11.
func FetchToolchain(ctx context.Context) (data []byte, err error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://downloads.example.com/toolchain/latest/toolchain.tar.gz", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}
