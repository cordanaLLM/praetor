// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestGitHubDriver_ReusableWorkflowAt verifies ReusableWorkflowAt via an httptest server:
// request path, ref query and Accept header are asserted; 200 returns body; 404 wraps
// ErrRepositoryNotVisible; 500 returns an error; short SHA, path outside workflows dir,
// and bad owner are refused with zero requests; bodies exceeding 256 KiB are refused.
// Order: engine-hiss18-cross-repo-reusable-20261027.
func TestGitHubDriver_ReusableWorkflowAt(t *testing.T) {
	validSHA := "2bf15d49f0eca695707a999808bccd988e2160e1"
	validPath := ".github/workflows/reusable.yml"
	var requestsCount int32
	var responseCode int = http.StatusOK
	var responseBody string = "on: workflow_call\njobs: {}\n"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestsCount, 1)
		wantPath := "/repos/acme/widgets/contents/.github/workflows/reusable.yml"
		if r.URL.Path != wantPath {
			t.Errorf("request path: got %q, want %q", r.URL.Path, wantPath)
		}
		if ref := r.URL.Query().Get("ref"); ref != validSHA {
			t.Errorf("ref query: got %q, want %q", ref, validSHA)
		}
		if accept := r.Header.Get("Accept"); accept != "application/vnd.github.raw+json" {
			t.Errorf("Accept header: got %q, want application/vnd.github.raw+json", accept)
		}
		w.WriteHeader(responseCode)
		_, _ = w.Write([]byte(responseBody))
	}))
	defer server.Close()

	driver := NewGitHubDriver("test-token", server.URL)

	// Refused with zero requests: short SHA, path outside workflows, bad owner.
	for name, tc := range map[string]struct {
		owner, repo, path, sha string
	}{
		"short SHA":              {"acme", "widgets", validPath, "abc"},
		"path outside workflows": {"acme", "widgets", "not-workflows/call.yml", validSHA},
		"bad owner":              {"bad owner!", "widgets", validPath, validSHA},
	} {
		t.Run(name, func(t *testing.T) {
			atomic.StoreInt32(&requestsCount, 0)
			_, err := driver.ReusableWorkflowAt(t.Context(), tc.owner, tc.repo, tc.path, tc.sha)
			if err == nil {
				t.Fatalf("%s: expected error, got nil", name)
			}
			if count := atomic.LoadInt32(&requestsCount); count != 0 {
				t.Fatalf("%s: sent %d requests, want 0", name, count)
			}
		})
	}

	// 200 body returned.
	t.Run("status 200", func(t *testing.T) {
		atomic.StoreInt32(&requestsCount, 0)
		responseCode = http.StatusOK
		responseBody = "on: workflow_call\njobs:\n  gate:\n    runs-on: ubuntu-latest\n"
		got, err := driver.ReusableWorkflowAt(t.Context(), "acme", "widgets", validPath, validSHA)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(got) != responseBody {
			t.Fatalf("got body %q, want %q", string(got), responseBody)
		}
	})

	// 404 wraps ErrRepositoryNotVisible.
	t.Run("status 404", func(t *testing.T) {
		responseCode = http.StatusNotFound
		responseBody = "Not Found"
		_, err := driver.ReusableWorkflowAt(t.Context(), "acme", "widgets", validPath, validSHA)
		if err == nil || !errors.Is(err, ErrRepositoryNotVisible) {
			t.Fatalf("status 404: expected error wrapping ErrRepositoryNotVisible, got %v", err)
		}
	})

	// 500 returns error naming status and preview.
	t.Run("status 500", func(t *testing.T) {
		responseCode = http.StatusInternalServerError
		responseBody = "internal server fault"
		_, err := driver.ReusableWorkflowAt(t.Context(), "acme", "widgets", validPath, validSHA)
		if err == nil || !strings.Contains(err.Error(), "HTTP 500") || !strings.Contains(err.Error(), "internal server fault") {
			t.Fatalf("status 500: expected error naming HTTP 500 and preview, got %v", err)
		}
	})

	// Body above 256 KiB bound refused.
	t.Run("body above bound", func(t *testing.T) {
		responseCode = http.StatusOK
		responseBody = strings.Repeat("x", maxReusableWorkflowBytes+1)
		_, err := driver.ReusableWorkflowAt(t.Context(), "acme", "widgets", validPath, validSHA)
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("body above bound: expected error naming exceeded bound, got %v", err)
		}
	})
}
