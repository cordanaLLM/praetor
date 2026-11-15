// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package sandbox

import (
	"context"
	"encoding/base32"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// testExecSession requires exactly one supported, random session override in bubblewrap's argv.
func testExecSession(t *testing.T, args []string) string {
	t.Helper()
	index := slices.Index(args, "TESTCONTAINERS_SESSION_ID")
	if index < 1 || index+1 >= len(args) || args[index-1] != "--setenv" {
		t.Fatalf("missing testcontainers session override: %q", args)
	}
	identity := args[index+1]
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(identity)
	if err != nil || len(decoded) < 16 {
		t.Fatalf("session must carry at least 128 random bits: %q error=%v", identity, err)
	}
	if slices.Contains(args[index+2:], "TESTCONTAINERS_SESSION_ID") {
		t.Fatalf("multiple testcontainers session overrides: %q", args)
	}
	return identity
}

// TestSandboxReaperSessionArguments requires unique identities in both namespace modes.
func TestSandboxReaperSessionArguments(t *testing.T) {
	t.Setenv("TESTCONTAINERS_SESSION_ID", "planted-shared-run-session")
	seen := map[string]bool{}
	for run := 0; run < 2; run++ {
		for _, runner := range []Runner{stubRunner(""), publishRunner("")} {
			invocation, err := runner.LoopbackCommand(t.Context(), []string{"/test"})
			if err != nil {
				t.Fatal(err)
			}
			identity := testExecSession(t, invocation.Args)
			if seen[identity] {
				t.Fatalf("test-exec invocations share session %q", identity)
			}
			seen[identity] = true
		}
	}
}

// reaperEngine models Docker's globally unique names and simultaneous list misses.
type reaperEngine struct {
	mu        sync.Mutex
	names     map[string]bool
	listed    int
	ready     chan struct{}
	conflicts int
}

// ServeHTTP forces both packages past lookup before either can create its reaper.
func (e *reaperEngine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == "/containers/json" {
		e.listPair(w, r)
		return
	}
	if r.Method != http.MethodPost || r.URL.Path != "/containers/create" {
		http.Error(w, "unexpected engine request", http.StatusBadRequest)
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	name := r.URL.Query().Get("name")
	if e.names[name] {
		e.conflicts++
		http.Error(w, "Conflict: container name already in use", http.StatusConflict)
		return
	}
	e.names[name] = true
	w.WriteHeader(http.StatusCreated)
}

// listPair holds each pair of lookups until both test binaries reach the engine.
func (e *reaperEngine) listPair(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	ready := e.ready
	e.listed++
	if e.listed%2 == 0 {
		close(ready)
		e.ready = make(chan struct{})
	}
	e.mu.Unlock()
	select {
	case <-ready:
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, "[]"); err != nil {
			return
		}
	case <-r.Context().Done():
		http.Error(w, "second package did not arrive", http.StatusRequestTimeout)
	}
}

// startReaperEngine serves the named stub Unix socket without constructing a Docker client.
func startReaperEngine(t *testing.T) (*reaperEngine, string) {
	t.Helper()
	socket := t.TempDir() + "/reaper-engine.sock"
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	engine := &reaperEngine{names: map[string]bool{}, ready: make(chan struct{})}
	server := &http.Server{Handler: engine, ReadHeaderTimeout: 5 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("stub reaper engine: %v", err)
		}
	})
	return engine, socket
}

// TestSandboxReaperExecProcess is go test's -exec adapter for the two fixture packages.
func TestSandboxReaperExecProcess(t *testing.T) {
	index := slices.Index(os.Args, "sandbox-reaper-exec")
	if index < 0 {
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := (Runner{ContainerSocket: noContainer}).RunLoopback(ctx, os.Args[index+1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		t.Fatal(err)
	}
}

// TestSandboxReaperPackages requires distinct reapers for concurrent packages and subsequent runs.
func TestSandboxReaperPackages(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("test-exec namespaces require Linux")
	}
	if _, err := (Runner{ContainerSocket: noContainer}).LoopbackCommand(t.Context(), []string{"/bin/true"}); err != nil {
		t.Skipf("test-exec namespaces unavailable: %v", err)
	}
	for _, session := range []string{"", "planted-shared-run-session"} {
		name := "default"
		if session != "" {
			name = "inherited"
		}
		t.Run(name, func(t *testing.T) { checkReaperPackages(t, session) })
	}
}

// checkReaperPackages checks two runs against one engine with or without an inherited session.
func checkReaperPackages(t *testing.T, session string) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	engine, socket := startReaperEngine(t)
	t.Setenv("PRAETOR_REAPER_SOCKET", socket)
	// A launcher-supplied session bypasses bootstrap and the start-tick reservation.
	// Every binary must override it rather than sharing the launcher's Ryuk name.
	t.Setenv("TESTCONTAINERS_SESSION_ID", session)
	for run := 0; run < 2; run++ {
		runReaperPackages(t, run)
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.conflicts != 0 || len(engine.names) != 4 {
		t.Fatalf("two runs of two packages: names=%v conflicts=%d", engine.names, engine.conflicts)
	}
}

// runReaperPackages runs both packages concurrently through the real sandbox adapter.
func runReaperPackages(t *testing.T, run int) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	wrapper := "'" + executable + "' -test.run=^TestSandboxReaperExecProcess$ -- sandbox-reaper-exec"
	result, err := util.RunCommandBytes(ctx, "testdata/reaper", "go", 65536,
		"test", "-count=1", "-v", "-p=2", "-timeout=30s", "-exec", wrapper, "./first", "./second")
	t.Logf("run=%d stdout=%s stderr=%s", run, result.Stdout, result.Stderr)
	if err != nil || strings.Contains(string(result.Stdout), "Conflict") {
		t.Fatalf("concurrent reaper fixture: %v", err)
	}
}
