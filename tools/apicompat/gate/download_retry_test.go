//go:build apicompatgate

// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"fmt"
	"go/build"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

type mockTracker struct {
	downloads int
	lists     int
	failCount int
	failErr   error
}

func (m *mockTracker) run(ctx context.Context, dir string, stdout, errOut io.Writer, name string, args ...string) error {
	if len(args) >= 2 && args[0] == "mod" && args[1] == "download" {
		m.downloads++
		if m.downloads <= m.failCount {
			return m.failErr
		}
		return nil
	}
	if len(args) >= 1 && args[0] == "list" {
		m.lists++
		return nil
	}
	return nil
}

// Positive: first download fails then succeeds on attempt 2, list runs once, one wait of 5 s.
func TestBuildModule_Positive_FirstDownloadFailsThenSucceeds(t *testing.T) {
	t.Parallel()
	tracker := &mockTracker{
		failCount: 1,
		failErr:   errors.New("simulated proxy stream INTERNAL_ERROR"),
	}
	var (
		waits  []time.Duration
		stderr strings.Builder
	)
	wait := func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}

	err := buildModule(t.Context(), retryFixtureRoot(t, "my/mod"), "my/mod", &stderr, tracker.run, wait)
	if err != nil {
		t.Fatalf("buildModule failed: %v", err)
	}
	if tracker.downloads != 2 || tracker.lists != 1 {
		t.Errorf("downloads = %d, lists = %d, want 2 and 1", tracker.downloads, tracker.lists)
	}
	if !slices.Equal(waits, []time.Duration{5 * time.Second}) {
		t.Errorf("waits = %v, want [5s]", waits)
	}
	wantMsg := "go mod download failed for my/mod (attempt 1 of 3): simulated proxy stream INTERNAL_ERROR"
	if !strings.Contains(stderr.String(), wantMsg) {
		t.Errorf("stderr missing expected attempt 1 line:\n%s", stderr.String())
	}
}

// Negative: every download fails, error after 3 attempts naming module, list never runs.
func TestBuildModule_Negative_EveryDownloadFails(t *testing.T) {
	t.Parallel()
	tracker := &mockTracker{
		failCount: 3,
		failErr:   errors.New("persistent connection refused"),
	}
	var (
		waits  []time.Duration
		stderr strings.Builder
	)
	wait := func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}

	err := buildModule(t.Context(), retryFixtureRoot(t, "pkg/badmod"), "pkg/badmod", &stderr, tracker.run, wait)
	if err == nil {
		t.Fatal("buildModule succeeded, want persistent download error")
	}
	if tracker.downloads != 3 || tracker.lists != 0 {
		t.Errorf("downloads = %d, lists = %d, want 3 and 0", tracker.downloads, tracker.lists)
	}
	wantWaits := []time.Duration{5 * time.Second, 15 * time.Second}
	if !slices.Equal(waits, wantWaits) {
		t.Errorf("waits = %v, want %v", waits, wantWaits)
	}
	if !strings.Contains(err.Error(), "pkg/badmod") || !strings.Contains(err.Error(), "3 attempts") {
		t.Errorf("error %q does not name module and 3 attempts", err.Error())
	}
	for _, attempt := range []string{"attempt 1 of 3", "attempt 2 of 3", "attempt 3 of 3"} {
		if !strings.Contains(stderr.String(), attempt) {
			t.Errorf("stderr missing %q:\n%s", attempt, stderr.String())
		}
	}
}

// Boundary: cancel during wait returns at once.
func TestBuildModule_Boundary_CancelDuringWaitReturnsAtOnce(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	runner := func(runCtx context.Context, dir string, stdout, errOut io.Writer, name string, args ...string) error {
		if len(args) >= 2 && args[0] == "mod" && args[1] == "download" {
			cancel()
			return errors.New("initial failure")
		}
		return nil
	}

	start := time.Now()
	err := buildModule(ctx, retryFixtureRoot(t, "cancelmod"), "cancelmod", io.Discard, runner, defaultWait)
	elapsed := time.Since(start)

	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if elapsed >= 2*time.Second {
		t.Errorf("cancel took %v, expected immediate return without waiting full backoff", elapsed)
	}
}

// Negative: exception for module plus download failure still fails gate.
func TestGate_Negative_ExceptionForModulePlusDownloadFailureStillFailsGate(t *testing.T) {
	t.Parallel()
	repo := repository{
		root:  retryFixtureRoot(t, "excepted"),
		today: time.Now().UTC(),
		exceptions: []exception{
			{Path: "excepted/go.mod", Reason: "needs libfoo", Expires: "2099-12-31"},
		},
		runner: func(ctx context.Context, dir string, stdout, errOut io.Writer, name string, args ...string) error {
			if len(args) >= 2 && args[0] == "mod" && args[1] == "download" {
				return errors.New("proxy error")
			}
			return nil
		},
		wait: func(ctx context.Context, d time.Duration) error {
			return nil
		},
	}
	plan := modulePlan{
		compared: []module{
			{dir: "excepted", path: "example.com/excepted"},
		},
	}
	stdout := &printer{w: io.Discard}
	stderr := &printer{w: io.Discard}

	results, err := compareEach(t.Context(), repo, plan, "checker", checkerSites{}, nil, stdout, stderr)
	if err == nil {
		t.Fatalf("compareEach succeeded (results = %+v), want failure when download fails even with live exception", results)
	}
	if !strings.Contains(err.Error(), "excepted") || !strings.Contains(err.Error(), "3 attempts") {
		t.Errorf("error %q does not name excepted module and attempts", err.Error())
	}
}

// Boundary: base code without download is red against download retry requirement.
func TestBuildModule_Boundary_BaseCodeWithoutDownloadRed(t *testing.T) {
	t.Parallel()
	priorPath := filepath.Join("..", "testdata", "prior", "api-gate-main-before-modfetch-retry.go.txt")
	priorData, err := os.ReadFile(priorPath)
	if err != nil {
		t.Fatalf("read prior gate text: %v", err)
	}
	if strings.Contains(string(priorData), "go mod download") {
		t.Fatal("prior gate text unexpectedly contains go mod download")
	}

	currentData, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read current main.go: %v", err)
	}
	if !strings.Contains(string(currentData), "go mod download") {
		t.Fatal("current main.go must contain go mod download retry")
	}

	baseCodeBuildModule := func(ctx context.Context, root, dir string, stderr io.Writer, run toolRunner) error {
		moduleDir := filepath.Join(root, filepath.FromSlash(dir))
		return run(ctx, moduleDir, io.Discard, stderr, "go", "list", "-export", "./...")
	}

	var downloadRan bool
	mockRunner := func(ctx context.Context, dir string, stdout, errOut io.Writer, name string, args ...string) error {
		if len(args) >= 2 && args[0] == "mod" && args[1] == "download" {
			downloadRan = true
		}
		return nil
	}

	if err := baseCodeBuildModule(t.Context(), "/mock/root", "mod", io.Discard, mockRunner); err != nil {
		t.Fatalf("baseCodeBuildModule failed: %v", err)
	}
	if downloadRan {
		t.Fatal("base code unexpectedly ran download")
	}
}

// writeTestModuleFiles writes go.mod and Go source fixtures into a temporary test directory.
func writeTestModuleFiles(t *testing.T, dir, gomod, src, testSrc string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"go.mod": gomod, "snap.go": src, "snap_test.go": testSrc} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Positive: test-only go.mod requirements are never prefetched before go list -export.
func TestBuildModule_Positive_SkipsTestOnlyRequirements(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	fssnapDir := filepath.Join(root, "hw", "fssnap")
	gomod := "module example.com/hw/fssnap\n\ngo 1.26.0\n\nrequire github.com/stretchr/testify v1.9.0\n"
	src := "package fssnap\n\nimport \"fmt\"\n\nfunc Name() string { return fmt.Sprint(\"ok\") }\n"
	testSrc := "package fssnap\n\nimport _ \"github.com/stretchr/testify/assert\"\n"
	writeTestModuleFiles(t, fssnapDir, gomod, src, testSrc)

	var downloadCalls [][]string
	var lists int
	failDownload := func(ctx context.Context, dir string, stdout, errOut io.Writer, name string, args ...string) error {
		if slices.Equal(args[:min(len(args), 2)], []string{"mod", "download"}) {
			downloadCalls = append(downloadCalls, slices.Clone(args))
			return errors.New("testify proxy unavailable")
		}
		lists++
		return nil
	}
	if err := buildModule(t.Context(), root, "hw/fssnap", io.Discard, failDownload, defaultWait); err != nil {
		t.Fatalf("buildModule failed on test-only requirement: %v", err)
	}
	if len(downloadCalls) != 0 || lists != 1 {
		t.Fatalf("downloadCalls = %v, lists = %d; want no download calls and 1 list", downloadCalls, lists)
	}

	mixedGomod := "module example.com/hw/fssnap\n\ngo 1.26.0\n\nrequire (\n\texample.com/needed v1.2.3\n\tgithub.com/stretchr/testify v1.9.0\n)\n"
	writeTestModuleFiles(t, fssnapDir, mixedGomod, "package fssnap\n\nimport _ \"example.com/needed/sub\"\n", testSrc)
	recordDownload := func(ctx context.Context, dir string, stdout, errOut io.Writer, name string, args ...string) error {
		if slices.Equal(args[:min(len(args), 2)], []string{"mod", "download"}) {
			downloadCalls = append(downloadCalls, slices.Clone(args))
		}
		return nil
	}
	if err := buildModule(t.Context(), root, "hw/fssnap", io.Discard, recordDownload, defaultWait); err != nil {
		t.Fatalf("buildModule failed on mixed requirements: %v", err)
	}
	wantArgs := []string{"mod", "download", "example.com/needed@v1.2.3"}
	if len(downloadCalls) != 1 || !slices.Equal(downloadCalls[0], wantArgs) {
		t.Fatalf("downloadCalls = %v, want [%v]", downloadCalls, wantArgs)
	}
}

// Boundary: download retries stay within the 60-minute CI job and divide remaining context budget.
func TestRetryModuleGo_Boundary_RetryLoopBoundedByJobAndContextDeadline(t *testing.T) {
	t.Parallel()
	const ciJobTimeout = 60 * time.Minute
	if runTimeout >= ciJobTimeout {
		t.Errorf("runTimeout = %v, must be less than %v CI job limit", runTimeout, ciJobTimeout)
	}
	maxRetrySpan := downloadAttempts*downloadTimeout + firstBackoff + secondBackoff
	if maxRetrySpan >= ciJobTimeout {
		t.Errorf("three stalled download attempts can take %v, exceeding %v CI job limit", maxRetrySpan, ciJobTimeout)
	}

	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		ctx, cancel := context.WithTimeout(t.Context(), 450*time.Millisecond)
		defer cancel()
		var attempts int
		stalledRunner := func(runCtx context.Context, _ string, _, _ io.Writer, _ string, _ ...string) error {
			attempts++
			<-runCtx.Done()
			return runCtx.Err()
		}
		count, err := retryModuleGo(ctx, "unused", "stalled/mod", "go mod download", []string{"mod", "download"}, downloadTimeout, io.Discard, stalledRunner, defaultWait)
		if !errors.Is(err, context.DeadlineExceeded) || count != downloadAttempts || attempts != downloadAttempts {
			t.Fatalf("err = %v, count = %d, attempts = %d; want all 3 timed-out attempts before job deadline", err, count, attempts)
		}
		if elapsed := time.Since(start); elapsed > 450*time.Millisecond {
			t.Fatalf("retry loop took %v, exceeding the 450ms parent budget", elapsed)
		}
	})
}

// TestRetryModuleGo_ParentDeadlineStopsFirstAttempt verifies parent deadline stops the first retry attempt.
func TestRetryModuleGo_ParentDeadlineStopsFirstAttempt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		parentCtx, cancelParent := context.WithTimeout(t.Context(), 40*time.Millisecond)
		defer cancelParent()
		var parentAttempts int
		expireParentRunner := func(_ context.Context, _ string, _, _ io.Writer, _ string, _ ...string) error {
			parentAttempts++
			<-parentCtx.Done()
			return parentCtx.Err()
		}
		count, parentErr := retryModuleGo(parentCtx, "unused", "expired/mod", "go mod download", []string{"mod", "download"}, downloadTimeout, io.Discard, expireParentRunner, defaultWait)
		if !errors.Is(parentErr, context.DeadlineExceeded) {
			t.Fatalf("parentErr = %v, want context.DeadlineExceeded", parentErr)
		}
		if parentAttempts != 1 || count != 1 {
			t.Fatalf("parentAttempts = %d, count = %d; want 1 when parent context expires during attempt 1", parentAttempts, count)
		}
	})
}

// TestBuildModule_LocalReplaceVersionSelection verifies local replacement version selection during module build.
func TestBuildModule_LocalReplaceVersionSelection(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	modDir := filepath.Join(root, "mod")
	gomod := "module example.com/mod\n\ngo 1.26.0\n\nrequire (\n" +
		"\texample.com/mismatch v1.2.0\n" +
		"\texample.com/matched v1.0.0\n" +
		"\texample.com/unversioned v1.5.0\n" +
		"\texample.com/testonly v1.0.0\n" +
		")\n\nreplace (\n" +
		"\texample.com/mismatch v1.0.0 => ../mismatch\n" +
		"\texample.com/matched v1.0.0 => ../matched\n" +
		"\texample.com/unversioned => ../unversioned\n" +
		")\n"
	src := "package mod\n\nimport (\n\t_ \"example.com/mismatch\"\n\t_ \"example.com/matched\"\n\t_ \"example.com/unversioned\"\n)\n"
	writeTestModuleFiles(t, modDir, gomod, src, "package mod\n\nimport _ \"example.com/testonly\"\n")
	var gotArgs []string
	rec := func(ctx context.Context, dir string, stdout, errOut io.Writer, name string, args ...string) error {
		if slices.Equal(args[:min(len(args), 2)], []string{"mod", "download"}) {
			gotArgs = slices.Clone(args)
		}
		return nil
	}
	if err := buildModule(t.Context(), root, "mod", io.Discard, rec, defaultWait); err != nil {
		t.Fatal(err)
	}
	want := []string{"mod", "download", "example.com/mismatch@v1.2.0"}
	if !slices.Equal(gotArgs, want) {
		t.Fatalf("download args = %v, want %v", gotArgs, want)
	}

}

// TestGoModRequirements_ReadsPastOldCapAndLateReplace pins requirement parsing beyond old limits with late replacements.
func TestGoModRequirements_ReadsPastOldCapAndLateReplace(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	b.WriteString("module example.com/mod\nrequire (\nexample.com/testonly v1.0.0\n")
	for index := range maxImports + 5 {
		fmt.Fprintf(&b, "example.com/dep%d v1.0.0 // indirect\n", index)
	}
	b.WriteString("example.com/late v1.2.3\n)\nreplace example.com/dep0 => ../dep0\n")
	reqs, ok := parseGoModRequirements(b.String())
	if !ok || len(reqs) != maxImports+6 || reqs[len(reqs)-1].path != "example.com/late" {
		t.Fatalf("requirements past 256: count = %d, complete = %v; want %d with late replacement", len(reqs), ok, maxImports+6)
	}
	if slices.ContainsFunc(reqs, func(req modRequirement) bool { return req.path == "example.com/dep0" }) {
		t.Fatal("replacement after old cap was ignored")
	}
	if _, ok := parseGoModRequirements(strings.Repeat("\n", maxGoModLines-1)); !ok {
		t.Fatal("exact line limit rejected")
	}
	if _, ok := parseGoModRequirements(strings.Repeat("\n", maxGoModLines)); ok {
		t.Fatal("over-limit go.mod silently accepted")
	}
}

// retryFixtureRoot sets up a temporary module directory with valid go.mod and Go sources.
func retryFixtureRoot(t *testing.T, dir string) string {
	t.Helper()
	root := t.TempDir()
	writeTestModuleFiles(t, filepath.Join(root, dir),
		"module example.com/mod\nrequire example.com/needed v1.0.0\n",
		"package mod\nimport _ \"example.com/needed\"\n", "package mod\n")
	return root
}

// TestBuildModule_RejectsIncompleteScan pins build rejection when source or go.mod files cannot be scanned completely.
func TestBuildModule_RejectsIncompleteScan(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"symlink", "parse-error", "oversized-source", "oversized-go-mod", "missing-go-mod", "too-many-lines"} {
		t.Run(kind, func(t *testing.T) {
			root := retryFixtureRoot(t, "mod")
			modDir := filepath.Join(root, "mod")
			plantIncompleteScan(t, modDir, kind)
			calls := 0
			runner := func(context.Context, string, io.Writer, io.Writer, string, ...string) error {
				calls++
				return nil
			}
			err := buildModule(t.Context(), root, "mod", io.Discard, runner, defaultWait)
			if err == nil || calls != 0 {
				t.Fatalf("%s: error = %v, calls = %d; want scan error before any Go command", kind, err, calls)
			}
		})
	}
}

// plantIncompleteScan creates broken, oversized, or missing module fixtures for scan testing.
func plantIncompleteScan(t *testing.T, dir, kind string) {
	t.Helper()
	file, body := "broken.go", "package mod\nimport (\n"
	switch kind {
	case "symlink":
		if err := os.Symlink(filepath.Join(dir, "snap.go"), filepath.Join(dir, "link.go")); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		return
	case "oversized-source":
		body = strings.Repeat(" ", maxDeclBytes+1)
	case "oversized-go-mod":
		file, body = "go.mod", strings.Repeat(" ", maxGoModBytes+1)
	case "too-many-lines":
		file, body = "go.mod", strings.Repeat("\n", maxGoModLines+1)
	case "missing-go-mod":
		if err := os.Remove(filepath.Join(dir, "go.mod")); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCollectNonTestImports_CancellationAndDirectoryBounds pins context cancellation and per-directory entry bounds.
func TestCollectNonTestImports_CancellationAndDirectoryBounds(t *testing.T) {
	t.Parallel()
	root := retryFixtureRoot(t, "mod")
	modDir := filepath.Join(root, "mod")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := collectNonTestImports(ctx, t.TempDir(), build.Default); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled scan = %v, want context.Canceled", err)
	}
	for index := range maxGoFiles - 3 {
		if err := os.Mkdir(filepath.Join(modDir, fmt.Sprintf("dir%04d", index)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := collectNonTestImports(t.Context(), modDir, build.Default); err != nil {
		t.Fatalf("exact directory entry limit: %v", err)
	}
	if err := os.Mkdir(filepath.Join(modDir, "overflow"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := collectNonTestImports(t.Context(), modDir, build.Default); err == nil {
		t.Fatal("over-limit directory scan silently accepted")
	}
}

// TestCollectNonTestImports_NodeModules excludes only exact dependency-directory names at any depth.
func TestCollectNonTestImports_NodeModules(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		keep bool
	}{
		{"node_modules", false},
		{"web/node_modules", false},
		{"node_modules_extra", true},
		{"other_node_modules", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestModuleFiles(t, root, "module example.com/mod\n", "package mod\nimport _ \"example.com/keep\"\n", "package mod\n")
			dir := filepath.Join(root, filepath.FromSlash(tc.name))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "dependency.go"), []byte("package dependency\nimport _ \"example.com/dependency\"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			imports, err := collectNonTestImports(t.Context(), root, build.Default)
			if err != nil || !imports["example.com/keep"] || imports["example.com/dependency"] != tc.keep {
				t.Fatalf("imports = %v, err = %v; want module import retained, dependency retained = %v", imports, err, tc.keep)
			}
		})
	}
	if skippedDirectory("node_modules") {
		t.Fatal("import-only exclusion changed tracked-path handling")
	}
}

// TestCollectNonTestImports_NodeModulesOverLimit skips oversized dependency trees before inventory.
func TestCollectNonTestImports_NodeModulesOverLimit(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "web", "node_modules")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for index := range maxGoFiles + 1 {
		if err := os.Mkdir(filepath.Join(dir, fmt.Sprintf("package%04d", index)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := collectNonTestImports(t.Context(), root, build.Default); err != nil {
		t.Fatalf("oversized node_modules must not consume the import scan budget: %v", err)
	}
}

// TestBuildModule_RetriesGoListExport pins backoff and retries on transient go list export errors.
func TestBuildModule_RetriesGoListExport(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	modDir := filepath.Join(root, "mod")
	writeTestModuleFiles(t, modDir, "module example.com/mod\n\ngo 1.26.0\n", "package mod\n", "package mod\n")
	var lists int
	var waits []time.Duration
	flakyList := func(ctx context.Context, dir string, stdout, errOut io.Writer, name string, args ...string) error {
		if len(args) >= 1 && args[0] == "list" {
			lists++
			if lists == 1 {
				return errors.New("transient indirect module proxy timeout")
			}
		}
		return nil
	}
	wait := func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}
	if err := buildModule(t.Context(), root, "mod", io.Discard, flakyList, wait); err != nil {
		t.Fatalf("buildModule failed on transient go list error: %v", err)
	}
	if lists != 2 || !slices.Equal(waits, []time.Duration{5 * time.Second}) {
		t.Fatalf("lists = %d, waits = %v; want 2 and [5s]", lists, waits)
	}
	alwaysFailList := func(ctx context.Context, dir string, stdout, errOut io.Writer, name string, args ...string) error {
		if len(args) >= 1 && args[0] == "list" {
			return errors.New("compile error")
		}
		return nil
	}
	if err := buildModule(t.Context(), root, "mod", io.Discard, alwaysFailList, wait); err == nil || !strings.Contains(err.Error(), "its packages do not build at HEAD") {
		t.Fatalf("buildModule error = %v, want build error after retry limit", err)
	}
}

// TestCollectNonTestImports_TotalDirectoryLimit pins refusal when directory entries exceed the global limit.
func TestCollectNonTestImports_TotalDirectoryLimit(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for index := range maxGoFiles/2 - 1 {
		if err := os.MkdirAll(filepath.Join(root, fmt.Sprintf("dir%04d", index), "child"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "last"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := collectNonTestImports(t.Context(), root, build.Default); err != nil {
		t.Fatalf("exact total directory limit: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "last", "overflow"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := collectNonTestImports(t.Context(), root, build.Default); err == nil {
		t.Fatal("directory limit silently returned partial imports")
	}
}

// TestBuildModule_SkipsUnimportedIndirectRequirements verifies unimported indirect requirements are skipped during download.
func TestBuildModule_SkipsUnimportedIndirectRequirements(t *testing.T) {
	t.Parallel()
	root := retryFixtureRoot(t, "mod")
	gomod := "module example.com/mod\nrequire (\nexample.com/needed v1.0.0\nexample.com/testonly v1.0.0 // indirect\n)\n"
	if err := os.WriteFile(filepath.Join(root, "mod", "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	var downloads [][]string
	runner := func(ctx context.Context, dir string, stdout, stderr io.Writer, name string, args ...string) error {
		if args[0] == "mod" {
			downloads = append(downloads, slices.Clone(args))
		}
		return nil
	}
	if err := buildModule(t.Context(), root, "mod", io.Discard, runner, defaultWait); err != nil {
		t.Fatal(err)
	}
	want := []string{"mod", "download", "example.com/needed@v1.0.0"}
	if len(downloads) != 1 || !slices.Equal(downloads[0], want) {
		t.Fatalf("download args = %v, want only %v", downloads, want)
	}
}

// TestReadBoundedRegularFile_SourceBoundaryAndCancellation pins size limit enforcement and cancellation during regular file reads.
func TestReadBoundedRegularFile_SourceBoundaryAndCancellation(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "source.go")
	if err := os.WriteFile(file, []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, size := range []int64{10, 11} {
		if _, err := readBoundedRegularFile(t.Context(), file, size); err != nil {
			t.Fatalf("size bound %d: %v", size, err)
		}
	}
	if _, err := readBoundedRegularFile(t.Context(), file, 9); err == nil {
		t.Fatal("source exceeding byte limit accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := readBoundedRegularFile(ctx, file, 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read = %v, want context.Canceled", err)
	}
}

// TestParseLocalReplace_DirectoryBoundaries pins local directory replace target syntax across Unix and Windows path forms.
func TestParseLocalReplace_DirectoryBoundaries(t *testing.T) {
	t.Parallel()
	for _, target := range []string{
		".", "..", "./sibling", "../sibling", "/sibling", "/",
		`.\sibling`, `..\sibling`, `\sibling`, `\`, `\\server\share`,
		`C:\sibling`, `z:/sibling`, "A:relative", "z:",
	} {
		modPath, version, ok := parseLocalReplace("replace example.com/local v1.2.3 => " + target)
		if !ok || modPath != "example.com/local" || version != "v1.2.3" {
			t.Errorf("replace target %q = (%q, %q, %v), want local module and version", target, modPath, version, ok)
		}
	}
	for _, target := range []string{"", "sibling", ".hidden", "..hidden", "example.com/remote", "1:relative", ":relative", "c"} {
		if _, _, ok := parseLocalReplace("replace example.com/local => " + target); ok {
			t.Errorf("non-directory %q treated as a local directory", target)
		}
	}
	if _, _, ok := parseLocalReplace("replace example.com/local => example.com/remote v1.2.3"); ok {
		t.Fatal("remote module treated as a local directory")
	}
}

// TestBuildModule_RejectsSymlinkWithoutRequirements pins refusal of symlinked Go sources even when requirements are empty.
func TestBuildModule_RejectsSymlinkWithoutRequirements(t *testing.T) {
	t.Parallel()
	root := retryFixtureRoot(t, "mod")
	dir := filepath.Join(root, "mod")
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/mod\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plantIncompleteScan(t, dir, "symlink")
	if _, _, err := moduleDownloadArgs(t.Context(), dir); err == nil {
		t.Fatal("non-regular Go source accepted when requirements are empty")
	}
}
