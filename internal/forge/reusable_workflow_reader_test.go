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

const (
	rawForgeSHA  = "2bf15d49f0eca695707a999808bccd988e2160e1"
	rawForgePath = ".github/workflows/reusable.yml"
)

// rawWorkflowForge is a stand-in forge serving one workflow file: it answers every request with
// code and body, counts the requests and reports a request whose path, ref query or media type
// differs from what ReusableWorkflowAt must send.
type rawWorkflowForge struct {
	t        *testing.T
	code     int
	body     string
	requests atomic.Int32
}

func (f *rawWorkflowForge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests.Add(1)
	if want := "/repos/acme/widgets/contents/" + rawForgePath; r.URL.Path != want {
		f.t.Errorf("request path: got %q, want %q", r.URL.Path, want)
	}
	if ref := r.URL.Query().Get("ref"); ref != rawForgeSHA {
		f.t.Errorf("ref query: got %q, want %q", ref, rawForgeSHA)
	}
	if accept := r.Header.Get("Accept"); accept != "application/vnd.github.raw+json" {
		f.t.Errorf("Accept header: got %q, want application/vnd.github.raw+json", accept)
	}
	w.WriteHeader(f.code)
	if _, err := w.Write([]byte(f.body)); err != nil {
		f.t.Errorf("write response: %v", err)
	}
}

// TestGitHubDriver_ReusableWorkflowAt verifies ReusableWorkflowAt against a stand-in forge (order
// engine-hiss18-cross-repo-reusable-20261027). Negative: a short SHA, a path outside the workflows
// directory and an invalid owner are refused with zero requests. Positive: 200 returns the body.
// Negative: 404 wraps ErrRepositoryNotVisible and 500 names the status and the body preview.
// Boundary: a body above 256 KiB is refused.
func TestGitHubDriver_ReusableWorkflowAt(t *testing.T) {
	forge := &rawWorkflowForge{t: t}
	server := httptest.NewServer(forge)
	defer server.Close()
	driver := NewGitHubDriver("test-token", server.URL)

	refused := map[string][4]string{
		"short SHA":              {"acme", "widgets", rawForgePath, "abc"},
		"path outside workflows": {"acme", "widgets", "not-workflows/call.yml", rawForgeSHA},
		"bad owner":              {"bad owner!", "widgets", rawForgePath, rawForgeSHA},
	}
	for name, c := range refused {
		if _, err := driver.ReusableWorkflowAt(t.Context(), c[0], c[1], c[2], c[3]); err == nil {
			t.Errorf("%s: expected an error, got nil", name)
		}
	}
	if count := forge.requests.Load(); count != 0 {
		t.Fatalf("refused coordinates sent %d requests, want 0", count)
	}

	answered := []struct {
		name string
		code int
		body string
		ok   func(got []byte, err error) bool
	}{
		{"status 200", http.StatusOK, "on: workflow_call\njobs: {}\n",
			func(got []byte, err error) bool { return err == nil && string(got) == "on: workflow_call\njobs: {}\n" }},
		{"status 404", http.StatusNotFound, "Not Found",
			func(_ []byte, err error) bool { return errors.Is(err, ErrRepositoryNotVisible) }},
		{"status 500", http.StatusInternalServerError, "internal server fault",
			func(_ []byte, err error) bool {
				return err != nil && strings.Contains(err.Error(), "HTTP 500") && strings.Contains(err.Error(), "internal server fault")
			}},
		{"body above bound", http.StatusOK, strings.Repeat("x", maxReusableWorkflowBytes+1),
			func(_ []byte, err error) bool { return err != nil && strings.Contains(err.Error(), "exceeds") }},
	}
	for _, c := range answered {
		forge.code, forge.body = c.code, c.body
		got, err := driver.ReusableWorkflowAt(t.Context(), "acme", "widgets", rawForgePath, rawForgeSHA)
		if !c.ok(got, err) {
			t.Errorf("%s: unexpected result: body %d bytes, error %v", c.name, len(got), err)
		}
	}
}
