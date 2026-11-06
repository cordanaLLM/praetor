//go:build apicompatgate

// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// TestBuildModule_EffectiveBuildSelection verifies build tags, GOOS, and GOARCH filter excluded source files.
func TestBuildModule_EffectiveBuildSelection(t *testing.T) {
	t.Setenv("GOOS", "windows")
	t.Setenv("GOARCH", "arm64")
	t.Setenv("CGO_ENABLED", "0")
	t.Setenv("GOFLAGS", `"-tags=reviewtag secondtag"`)
	for _, tc := range []struct{ name, source string }{
		{"excluded_linux.go", "package mod\nimport (\n"},
		{"excluded_amd64.go", "package mod\nimport (\n"},
		{"excluded.go", "//go:build !reviewtag\n\npackage mod\nimport (\n"},
		{"excluded_second.go", "//go:build !secondtag\n\npackage mod\nimport (\n"},
		{"_ignored.go", "package mod\nimport (\n"},
		{".ignored.go", "package mod\nimport (\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := retryFixtureRoot(t, "mod")
			if err := os.WriteFile(filepath.Join(root, "mod", tc.name), []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			tracker := &mockTracker{}
			if err := buildModule(t.Context(), root, "mod", io.Discard, tracker.run, defaultWait); err != nil {
				t.Fatalf("excluded source failed the selected build: %v", err)
			}
			if tracker.downloads != 1 || tracker.lists != 1 {
				t.Fatalf("downloads = %d, lists = %d; want 1 each", tracker.downloads, tracker.lists)
			}
		})
	}
}

// TestBuildModule_SelectedTaggedImports pins prefetching and build errors for matching build-tagged sources.
func TestBuildModule_SelectedTaggedImports(t *testing.T) {
	t.Setenv("GOOS", "windows")
	t.Setenv("GOARCH", "arm64")
	t.Setenv("GOFLAGS", `"-tags=reviewtag secondtag"`)
	root := retryFixtureRoot(t, "mod")
	dir := filepath.Join(root, "mod")
	writeTestModuleFiles(t, dir, "module example.com/mod\nrequire example.com/needed v1.0.0\n",
		"package mod\n", "package mod\n")
	source := "//go:build windows && arm64 && reviewtag && secondtag\n\npackage mod\nimport _ \"example.com/needed\"\n"
	if err := os.WriteFile(filepath.Join(dir, "selected_windows_arm64.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	var downloads [][]string
	runner := func(_ context.Context, _ string, _, _ io.Writer, _ string, args ...string) error {
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
		t.Fatalf("selected import downloads = %v, want [%v]", downloads, want)
	}
	if err := os.WriteFile(filepath.Join(dir, "selected_windows_arm64.go"), []byte("//go:build reviewtag\n\npackage mod\nimport (\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := buildModule(t.Context(), root, "mod", io.Discard, runner, defaultWait); err == nil {
		t.Fatal("invalid selected source accepted")
	}
}

// TestBuildModule_RejectsInvalidOrOversizedBuildContext pins refusal of invalid or oversized GOFLAGS build configurations.
func TestBuildModule_RejectsInvalidOrOversizedBuildContext(t *testing.T) {
	for _, tc := range []struct{ name, flags, diagnostic string }{
		{"invalid-flags", "-not-a-go-build-flag", "read Go build context"},
		{"oversized-context", "-tags=" + strings.Repeat("reviewtag,", maxRecordBytes/9), "go build context exceeds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			skipOversizedGoFlags(t, runtime.GOOS, tc.flags)
			t.Setenv("GOFLAGS", tc.flags)
			tracker := &mockTracker{}
			err := buildModule(t.Context(), retryFixtureRoot(t, "mod"), "mod", io.Discard, tracker.run, defaultWait)
			if err == nil || !strings.Contains(err.Error(), tc.diagnostic) {
				t.Fatalf("build configuration must fail closed with %q: %v", tc.diagnostic, err)
			}
			if tracker.downloads != 0 || tracker.lists != 0 {
				t.Fatalf("invalid configuration ran downloads = %d, lists = %d", tracker.downloads, tracker.lists)
			}
		})
	}
}

// skipOversizedGoFlags skips tests when environment variable limits prevent setting large GOFLAGS values.
func skipOversizedGoFlags(t *testing.T, platform, flags string) {
	t.Helper()
	if platform == "windows" && len(flags) > 32767 {
		t.Skip("Windows limits a single environment variable to 32,767 characters; oversized GOFLAGS cannot reach the Go build-context check")
	}
}

// TestOversizedGoFlagsPlatformSkip verifies skip behavior across platform-specific environment variable size limits.
func TestOversizedGoFlagsPlatformSkip(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, platform string
		size           int
		skipped        bool
	}{
		{"windows-empty", "windows", 0, false},
		{"windows-limit", "windows", 32767, false},
		{"windows-over-limit", "windows", 32768, true},
		{"windows-oversized-context", "windows", maxRecordBytes * 2, true},
		{"linux-oversized-context", "linux", maxRecordBytes * 2, false},
		{"darwin-oversized-context", "darwin", maxRecordBytes * 2, false},
	} {
		var child *testing.T
		t.Run(tc.name, func(t *testing.T) {
			child = t
			skipOversizedGoFlags(t, tc.platform, strings.Repeat("x", tc.size))
		})
		if child.Skipped() != tc.skipped {
			t.Errorf("%s: skipped = %v, want %v", tc.name, child.Skipped(), tc.skipped)
		}
	}
}

// TestEffectiveBuildContext_PreservesContextAndToolErrors pins error wrapping when build context discovery fails.
func TestEffectiveBuildContext_PreservesContextAndToolErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		want error
	}{
		{"active-parent", nil},
		{"canceled-parent", context.Canceled},
		{"deadline-parent", context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				assertBuildContextErrors(t, tc.want)
			})
		})
	}
}

// assertBuildContextErrors verifies tool errors and parent context states are preserved in return values.
func assertBuildContextErrors(t *testing.T, want error) {
	t.Helper()
	toolErr := errors.New("simulated killed process")
	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	runner := func(_ context.Context, _ string, _, _ io.Writer, _ string, _ ...string) error {
		if errors.Is(want, context.Canceled) {
			cancel()
		}
		if want != nil {
			<-ctx.Done()
		}
		return toolErr
	}
	_, err := effectiveBuildContext(ctx, t.TempDir(), runner)
	if !errors.Is(err, toolErr) || (want != nil && !errors.Is(err, want)) {
		t.Fatalf("build context error = %v, want tool error and %v", err, want)
	}
	if want == nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		t.Fatalf("active parent misclassified: %v", err)
	}
}

// TestBuildModule_WorkspaceBuildContext verifies module builds succeed when running inside a multi-module go.work workspace.
func TestBuildModule_WorkspaceBuildContext(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"first", "second"} {
		writeTestModuleFiles(t, filepath.Join(root, name), "module example.com/"+name+"\n\ngo 1.27\n", "package mod\n", "package mod\n")
	}
	workspace := filepath.Join(root, "go.work")
	if err := os.WriteFile(workspace, []byte("go 1.27\nuse (\n./first\n./second\n)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOWORK", workspace)
	tracker := &mockTracker{}
	if err := buildModule(t.Context(), root, "second", io.Discard, tracker.run, defaultWait); err != nil {
		t.Fatalf("multiple main-module contexts failed: %v", err)
	}
	if tracker.downloads != 0 || tracker.lists != 1 {
		t.Fatalf("workspace ran downloads = %d, lists = %d; want 0 and 1", tracker.downloads, tracker.lists)
	}
}

// TestBuildModule_ScanErrorsAreBuildErrors pins scan failures returning build errors without running download retries.
func TestBuildModule_ScanErrorsAreBuildErrors(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"parse-error", "missing-go-mod", "too-many-lines"} {
		t.Run(kind, func(t *testing.T) {
			root := retryFixtureRoot(t, "mod")
			plantIncompleteScan(t, filepath.Join(root, "mod"), kind)
			tracker := &mockTracker{}
			err := buildModule(t.Context(), root, "mod", io.Discard, tracker.run, defaultWait)
			var downloadErr *downloadError
			if err == nil || errors.As(err, &downloadErr) || strings.Contains(err.Error(), "after 0 attempts") {
				t.Fatalf("scan must return a build error without download attempts: %v", err)
			}
			if tracker.downloads != 0 || tracker.lists != 0 {
				t.Fatalf("scan failure ran downloads = %d, lists = %d", tracker.downloads, tracker.lists)
			}
		})
	}
}

// TestCompareEach_ScanFailureExceptions verifies active exceptions excuse scan failures while expired ones fail.
func TestCompareEach_ScanFailureExceptions(t *testing.T) {
	t.Parallel()
	today := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, expiry string
		excused      bool
	}{
		{"live", today.AddDate(0, 0, 1).Format(time.DateOnly), true},
		{"expires-today", today.Format(time.DateOnly), true},
		{"expired", today.AddDate(0, 0, -1).Format(time.DateOnly), false},
		{"absent", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := repository{root: retryFixtureRoot(t, "mod"), today: today}
			plantIncompleteScan(t, filepath.Join(repo.root, "mod"), "parse-error")
			if tc.expiry != "" {
				repo.exceptions = []exception{{Path: "mod/go.mod", Reason: "known broken build", Expires: tc.expiry}}
			}
			plan := modulePlan{compared: []module{{dir: "mod", path: "example.com/mod"}}}
			results, err := compareEach(t.Context(), repo, plan, "unused", checkerSites{}, nil, &printer{w: io.Discard}, &printer{w: io.Discard})
			if tc.excused {
				if err != nil || len(results) != 1 || results[0].notCompared == "" {
					t.Fatalf("live exception did not excuse source failure: results = %+v, err = %v", results, err)
				}
			} else if err == nil {
				t.Fatal("source failure passed without a live exception")
			}
		})
	}
}

// TestBuildModule_OperationTimeoutCaps pins per-attempt timeouts across varying parent context deadlines.
func TestBuildModule_OperationTimeoutCaps(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		budget time.Duration
	}{
		{"no-parent-deadline", 0},
		{"long-parent-deadline", 2 * time.Hour},
		{"shared-parent-deadline", 10 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			if tc.budget > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tc.budget)
				defer cancel()
			}
			root := retryFixtureRoot(t, "mod")
			runner := func(attemptCtx context.Context, _ string, _, _ io.Writer, _ string, args ...string) error {
				assertOperationTimeout(t, attemptCtx, args[0], tc.budget)
				return nil
			}
			if err := buildModule(ctx, root, "mod", io.Discard, runner, defaultWait); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestRetryModuleGo_PreparationDelay keeps timeout assertions stable after slow dependency discovery.
func TestRetryModuleGo_PreparationDelay(t *testing.T) {
	t.Parallel()
	for _, delay := range []time.Duration{0, 30 * time.Second, 90 * time.Second} {
		t.Run(delay.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				const budget = 10 * time.Minute
				ctx, cancel := context.WithTimeout(t.Context(), budget)
				defer cancel()
				time.Sleep(delay)
				for _, operation := range []string{"mod", "list"} {
					cap := downloadTimeout
					if operation == "list" {
						cap = moduleTimeout
					}
					runner := func(attemptCtx context.Context, _ string, _, _ io.Writer, _ string, args ...string) error {
						assertOperationTimeout(t, attemptCtx, args[0], budget)
						return nil
					}
					if _, err := retryModuleGo(ctx, "unused", "mod", operation, []string{operation}, cap, io.Discard, runner, defaultWait); err != nil {
						t.Fatal(err)
					}
				}
			})
		})
	}
}

// assertOperationTimeout checks that attempt context deadlines match operation budgets.
func assertOperationTimeout(t *testing.T, ctx context.Context, operation string, budget time.Duration) {
	t.Helper()
	want := downloadTimeout
	if operation == "list" {
		want = moduleTimeout
	}
	tolerance := time.Second
	if budget == 10*time.Minute {
		want = (budget - firstBackoff - secondBackoff) / downloadAttempts
		// Dependency discovery consumes the parent deadline before attempts start.
		// Keep the wider window below either operation cap (5m and 30m).
		tolerance = 30 * time.Second
	}
	deadline, ok := ctx.Deadline()
	remaining := time.Until(deadline)
	if !ok || remaining > want || remaining < want-tolerance {
		t.Errorf("%s timeout = %v, want [%v, %v]", operation, remaining, want-tolerance, want)
	}
}
