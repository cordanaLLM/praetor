// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package apicompat

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

const stubGoSource = `package main

import (
	"fmt"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) >= 3 && os.Args[1] == "mod" && os.Args[2] == "download" {
		counterFile := os.Getenv("STUB_GO_COUNTER")
		mode := os.Getenv("STUB_GO_MODE")
		attempts := 0
		if data, err := os.ReadFile(counterFile); err == nil {
			attempts, _ = strconv.Atoi(string(data))
		}
		attempts++
		_ = os.WriteFile(counterFile, []byte(strconv.Itoa(attempts)), 0o600)
		if mode == "retry_success" && attempts >= 2 {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, "go: example.com/pkg: proxy.golang.org stream INTERNAL_ERROR")
		os.Exit(1)
	}
	os.Exit(0)
}
`

const stubSleepSource = `package main

import (
	"os"
)

func main() {
	if len(os.Args) > 1 {
		logFile := os.Getenv("STUB_SLEEP_LOG")
		if logFile != "" {
			f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err == nil {
				_, _ = f.WriteString(os.Args[1] + "\n")
				_ = f.Close()
			}
		}
	}
	os.Exit(0)
}
`

func downloadRetryScript(t *testing.T, workflowText string) string {
	t.Helper()
	runs, err := forge.WorkflowRuns([]byte(workflowText))
	if err != nil {
		t.Fatalf("WorkflowRuns: %v", err)
	}
	for i := 0; i < len(runs); i++ {
		if runs[i].Name == "Download Go modules with retry" {
			return runs[i].Script
		}
	}
	return ""
}

func setupTestRepo(t *testing.T, modPath string) string {
	t.Helper()
	repo := t.TempDir()
	if _, err := util.RunGit(t.Context(), repo, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if _, err := util.RunGit(t.Context(), repo, "config", "user.email", "test@example.com"); err != nil {
		t.Fatalf("git config email: %v", err)
	}
	if _, err := util.RunGit(t.Context(), repo, "config", "user.name", "Test User"); err != nil {
		t.Fatalf("git config name: %v", err)
	}
	goModPath := filepath.Join(repo, filepath.FromSlash(modPath))
	if err := os.MkdirAll(filepath.Dir(goModPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goModPath, []byte("module example.com/test\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := util.RunGit(t.Context(), repo, "add", "."); err != nil {
		t.Fatalf("git add: %v", err)
	}
	return repo
}

func setupStubs(t *testing.T) (binDir, counterFile, sleepLog string) {
	t.Helper()
	binDir = t.TempDir()
	counterFile = filepath.Join(binDir, "attempts.txt")
	sleepLog = filepath.Join(binDir, "sleep.log")
	testsupport.BuildExecutable(t, binDir, "go", stubGoSource)
	testsupport.BuildExecutable(t, binDir, "sleep", stubSleepSource)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("STUB_GO_COUNTER", counterFile)
	t.Setenv("STUB_SLEEP_LOG", sleepLog)
	return binDir, counterFile, sleepLog
}

func runBashScript(ctx context.Context, repoDir, script string) (string, string, int, error) {
	res, err := util.RunCommandBytes(ctx, repoDir, "bash", 1<<20, "-c", script)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return string(res.Stdout), string(res.Stderr), exitErr.ExitCode(), nil
		}
		return string(res.Stdout), string(res.Stderr), -1, err
	}
	return string(res.Stdout), string(res.Stderr), 0, nil
}

func readAttempts(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	val, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return val
}

// Positive: stub go binary fails the first download with proxy stream INTERNAL_ERROR, then
// succeeds on attempt 2. The retry step succeeds (exit 0) after recording attempt 1 and sleeping.
func TestDownloadRetry_Positive_TransientProxyStreamErrorRetriesAndSucceeds(t *testing.T) {
	_, counterFile, sleepLog := setupStubs(t)
	t.Setenv("STUB_GO_MODE", "retry_success")

	repo := setupTestRepo(t, "go.mod")
	script := downloadRetryScript(t, Workflow)
	if script == "" {
		t.Fatal("workflow lacks Download Go modules with retry step")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, stderr, rc, err := runBashScript(ctx, repo, script)
	if err != nil {
		t.Fatalf("runBashScript failed: %v", err)
	}
	if rc != 0 {
		t.Fatalf("expected step to succeed (rc=0), got rc=%d; stderr:\n%s", rc, stderr)
	}
	if attempts := readAttempts(counterFile); attempts != 2 {
		t.Fatalf("expected 2 download attempts, got %d", attempts)
	}
	sleeps, err := os.ReadFile(sleepLog)
	if err != nil {
		t.Fatalf("read sleep log: %v", err)
	}
	if string(sleeps) != "5\n" {
		t.Fatalf("expected sleep log %q, got %q", "5\n", string(sleeps))
	}
	if !strings.Contains(stderr, "proxy.golang.org stream INTERNAL_ERROR") {
		t.Errorf("stderr lacks proxy stream error:\n%s", stderr)
	}
	if !strings.Contains(stderr, "attempt 1 of 3") {
		t.Errorf("stderr lacks attempt 1 annotation:\n%s", stderr)
	}
}

// Negative: stub go binary fails every attempt. The step retries up to 3 attempts with 5s and 15s
// backoff, then fails with exit 1 naming the module on stderr.
func TestDownloadRetry_Negative_PersistentFailureFailsAfterThreeAttemptsNamingModule(t *testing.T) {
	_, counterFile, sleepLog := setupStubs(t)
	t.Setenv("STUB_GO_MODE", "always_fail")

	repo := setupTestRepo(t, "go.mod")
	script := downloadRetryScript(t, Workflow)
	if script == "" {
		t.Fatal("workflow lacks Download Go modules with retry step")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, stderr, rc, err := runBashScript(ctx, repo, script)
	if err != nil {
		t.Fatalf("runBashScript failed: %v", err)
	}
	if rc != 1 {
		t.Fatalf("expected step to fail with rc=1, got rc=%d; stderr:\n%s", rc, stderr)
	}
	if attempts := readAttempts(counterFile); attempts != 3 {
		t.Fatalf("expected 3 download attempts, got %d", attempts)
	}
	sleeps, err := os.ReadFile(sleepLog)
	if err != nil {
		t.Fatalf("read sleep log: %v", err)
	}
	if string(sleeps) != "5\n15\n" {
		t.Fatalf("expected backoffs 5s and 15s, got %q", string(sleeps))
	}
	for _, want := range []string{"attempt 1 of 3", "attempt 2 of 3", "attempt 3 of 3", "go.mod"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
}

// Boundary: the old workflow template before modfetch retry lacked the retry step. An unretried
// download under proxy stream error fails immediately on the first attempt without backoff.
func TestDownloadRetry_Boundary_OldTemplateLackedRetryStep(t *testing.T) {
	priorData, err := os.ReadFile(filepath.Join("testdata", "prior", "praetor-api.before-modfetch-retry.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if script := downloadRetryScript(t, string(priorData)); script != "" {
		t.Fatalf("prior workflow unexpectedly contains retry step: %s", script)
	}

	_, counterFile, sleepLog := setupStubs(t)
	t.Setenv("STUB_GO_MODE", "retry_success")

	repo := setupTestRepo(t, "go.mod")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, stderr, rc, err := runBashScript(ctx, repo, "cd . && go mod download")
	if err != nil {
		t.Fatalf("runBashScript failed: %v", err)
	}
	if rc != 1 {
		t.Fatalf("unretried download expected to fail with rc=1, got rc=%d", rc)
	}
	if attempts := readAttempts(counterFile); attempts != 1 {
		t.Fatalf("expected single unretried attempt, got %d", attempts)
	}
	if _, err := os.ReadFile(sleepLog); !os.IsNotExist(err) {
		t.Fatalf("unretried download should not produce sleep log, err = %v", err)
	}
	if !strings.Contains(stderr, "proxy.golang.org stream INTERNAL_ERROR") {
		t.Errorf("stderr lacks stream error:\n%s", stderr)
	}
}
