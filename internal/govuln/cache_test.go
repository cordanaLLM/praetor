// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package govuln

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

const cacheMissExample = "could not import fmt (open /home/test/.cache/go-build/ab/entry-d: no such file or directory)"

func TestRetryCacheMiss(t *testing.T) {
	cases := []struct {
		name, command, diagnostic string
		wantCalls                 int
	}{
		{"import archive", "go", cacheMissExample, 2},
		{"absolute executable", filepath.Join(t.TempDir(), "go"), cacheMissExample, 2},
		{"standalone scanner", DefaultScanner, cacheMissExample, 2},
		{"Windows executable", "go.exe", `open C:\Users\test\go-build\ab\entry-a: no such file or directory`, 2},
		{"scanner executable", "govulncheck.exe", cacheMissExample, 2},
		{"network", "go", "network unavailable", 1},
		{"permission", "go", "open /cache/go-build/ab/entry-a: permission denied", 1},
		{"other directory", "go", "open /cache/not-go-build/ab/entry-a: no such file or directory", 1},
		{"other command", "git", cacheMissExample, 1},
		{"custom scanner", "scanner", cacheMissExample, 1},
		{"separate lines", "go", "cache /cache/go-build/ab/entry-a\nopen source.go: no such file or directory", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, inError := range []bool{false, true} {
				checkCacheMissResult(t, tc.command, tc.diagnostic, inError, tc.wantCalls)
			}
		})
	}
}

func checkCacheMissResult(t *testing.T, name, diagnostic string, inError bool, wantCalls int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	dir := t.TempDir()
	calls, notes := 0, 0
	failure := errors.New("exit status 1")
	firstOut := diagnostic
	if inError {
		failure, firstOut = errors.New(diagnostic), "first output"
	}
	base := cacheResultRunner(t, ctx, dir, name, firstOut, failure, &calls)
	run := RetryCacheMiss(base, func(gotDir string) error {
		notes++
		if gotDir != dir {
			t.Fatalf("note dir=%q", gotDir)
		}
		return nil
	})
	out, err := run(ctx, dir, name, "tool", "govulncheck")
	if calls != wantCalls || notes != wantCalls-1 {
		t.Fatalf("calls=%d notes=%d; want %d/%d", calls, notes, wantCalls, wantCalls-1)
	}
	if wantCalls == 2 {
		firstOut, failure = "second output", nil
	}
	if out != firstOut || !errors.Is(err, failure) {
		t.Fatalf("result changed: %q %v", out, err)
	}
}

func cacheResultRunner(t *testing.T, ctx context.Context, dir, name, firstOut string, failure error, calls *int) Runner {
	t.Helper()
	return func(gotCtx context.Context, gotDir, gotName string, args ...string) (string, error) {
		*calls++
		if gotCtx != ctx || gotDir != dir || gotName != name || !slices.Equal(args, []string{"tool", "govulncheck"}) {
			t.Fatalf("command changed: %s %s %v", gotDir, gotName, args)
		}
		if *calls == 1 {
			return firstOut, failure
		}
		return "second output", nil
	}
}

func TestRetryCacheMissBoundary(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "second failure", true: "cancelled"}[cancelled], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			failure := errors.New(cacheMissExample)
			calls, notes := 0, 0
			run := RetryCacheMiss(func(context.Context, string, string, ...string) (string, error) {
				calls++
				if cancelled {
					cancel()
				}
				return "partial stream", failure
			}, func(string) error { notes++; return nil })
			out, err := run(ctx, t.TempDir(), "go", "list", "./...")
			want := 2
			if cancelled {
				want = 1
			}
			if calls != want || notes != want-1 || !errors.Is(err, failure) || out != "partial stream" {
				t.Fatalf("calls=%d notes=%d out=%q err=%v", calls, notes, out, err)
			}
		})
	}
}

func TestRetryCacheMissNoteFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	noteErr := errors.New("report writer failed")
	calls := 0
	run := RetryCacheMiss(func(context.Context, string, string, ...string) (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New(cacheMissExample)
		}
		return "complete stream", nil
	}, func(string) error { return noteErr })
	out, err := run(ctx, t.TempDir(), "go", "list", "./...")
	if calls != 2 || out != "complete stream" || !errors.Is(err, noteErr) {
		t.Fatalf("calls=%d out=%q err=%v", calls, out, err)
	}
}

func TestRetryCacheMissSuccessfulDiagnostic(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	calls := 0
	run := RetryCacheMiss(func(context.Context, string, string, ...string) (string, error) {
		calls++
		return cacheMissExample, nil
	}, func(string) error { t.Fatal("successful command retried"); return nil })
	if out, err := run(ctx, t.TempDir(), "go", "list", "./..."); calls != 1 || err != nil || out != cacheMissExample {
		t.Fatalf("calls=%d out=%q err=%v", calls, out, err)
	}
}
