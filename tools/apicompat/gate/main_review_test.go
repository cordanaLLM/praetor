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
	"slices"
	"strings"
	"testing"
	"time"
)

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

func TestBuildModule_RejectsInvalidOrOversizedBuildContext(t *testing.T) {
	for _, tc := range []struct{ name, flags, diagnostic string }{
		{"invalid-flags", "-not-a-go-build-flag", "read Go build context"},
		{"oversized-context", "-tags=" + strings.Repeat("reviewtag,", maxRecordBytes/9), "go build context exceeds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
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

func TestCompareEach_ScanFailureExceptions(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, expiry string
		excused      bool
	}{
		{"live", "2026-11-03", true},
		{"expires-today", "2026-11-02", true},
		{"expired", "2026-11-01", false},
		{"absent", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := repository{root: retryFixtureRoot(t, "mod"), today: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)}
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

func assertOperationTimeout(t *testing.T, ctx context.Context, operation string, budget time.Duration) {
	t.Helper()
	want := downloadTimeout
	if operation == "list" {
		want = moduleTimeout
	}
	if budget == 10*time.Minute {
		want = (budget - firstBackoff - secondBackoff) / downloadAttempts
	}
	deadline, ok := ctx.Deadline()
	remaining := time.Until(deadline)
	if !ok || remaining > want || remaining < want-time.Second {
		t.Errorf("%s timeout = %v, want (%v, %v]", operation, remaining, want-time.Second, want)
	}
}
