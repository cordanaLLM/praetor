package p

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"time"
)

// The artifact is named by an exact version and verified against a pinned digest
// before it is returned, so the build is hermetic.
const toolchainDigest = "3b7a1c0e5f2d4a6b8c9d0e1f2a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d"

func FetchPinnedToolchain(ctx context.Context) (data []byte, err error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://downloads.example.com/toolchain/1.27.1/toolchain.tar.gz", nil)
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
	data, err = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != toolchainDigest {
		return nil, errors.New("toolchain digest mismatch")
	}
	return data, nil
}
