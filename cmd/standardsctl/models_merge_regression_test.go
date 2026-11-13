package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/reviewmerge"
	"github.com/cordanaLLM/praetor/internal/util"
)

// mergeReviewFile writes a bounded review fixture without accessing routing or credentials.
func mergeReviewFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestModelsMergeHelpAndUsage pins help success and merge-only usage errors.
func TestModelsMergeHelpAndUsage(t *testing.T) {
	cases := []struct {
		args []string
		code int
		help bool
	}{
		{[]string{"merge-check", "--help"}, 0, true},
		{[]string{"--draft", "missing"}, 2, false},
		{[]string{"list", "--final", "missing"}, 2, false},
		{[]string{"route", "--json"}, 2, false},
		{[]string{"merge-check", "--unknown"}, 2, false},
		{[]string{"--config", "merge-check", "list", "--unknown"}, 1, false},
		{[]string{"list", "--config=merge-check", "--unknown"}, 1, false},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.args), func(t *testing.T) {
			_, _, err := runOutcomeCLI(t, tc.args)
			if errors.Is(err, flag.ErrHelp) != tc.help {
				t.Fatalf("help error: %v", err)
			}
			if !tc.help && exitCode(err) != tc.code {
				t.Fatalf("exit %d want %d: %v", exitCode(err), tc.code, err)
			}
		})
	}
}

// TestModelsMergeCanceledContext refuses reads after invocation cancellation.
func TestModelsMergeCanceledContext(t *testing.T) {
	draft := mergeReviewFile(t, "draft.json", `{"model":"m","kind":"code","findings":[]}`)
	final := mergeReviewFile(t, "final.json", `{"combiner":"c","verdict":"approve"}`)
	previous := rootCtx
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rootCtx = ctx
	t.Cleanup(func() { rootCtx = previous })
	_, _, err := runOutcomeCLI(t, []string{"merge-check", "--draft", draft, "--final", final})
	if exitCode(err) != 2 {
		t.Fatalf("canceled input: %v", err)
	}
}

// TestModelsMergeDraftCountBoundary covers the CLI's 64-draft and 65-draft boundary.
func TestModelsMergeDraftCountBoundary(t *testing.T) {
	draft := mergeReviewFile(t, "draft.json", `{"model":"m","kind":"code","findings":[]}`)
	final := mergeReviewFile(t, "final.json", `{"combiner":"c","verdict":"approve"}`)
	for _, count := range []int{64, 65} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			args := []string{"merge-check", "--final", final}
			for i := 0; i < count; i++ {
				args = append(args, "--draft", draft)
			}
			_, _, err := runOutcomeCLI(t, args)
			want := 0
			if count == 65 {
				want = 2
			}
			if exitCode(err) != want {
				t.Fatalf("%d drafts: %v", count, err)
			}
		})
	}
}

// TestModelsMergeOffline validates two supplied reviews without a catalog or reviewer dispatch.
func TestModelsMergeOffline(t *testing.T) {
	one := mergeReviewFile(t, "one.json", `{"model":"one","kind":"code","findings":[]}`)
	two := mergeReviewFile(t, "two.json", `{"model":"two","kind":"design","findings":[]}`)
	final := mergeReviewFile(t, "final.json", `{"combiner":"c","verdict":"approve"}`)
	_, _, err := runOutcomeCLI(t, []string{"merge-check", "--draft", one, "--draft", two, "--final", final, "--config", filepath.Join(t.TempDir(), "absent.yaml")})
	if err != nil {
		t.Fatal(err)
	}
}

// TestModelsMergeAggregateBudget counts both drafts and the final at the exact byte boundary.
func TestModelsMergeAggregateBudget(t *testing.T) {
	draftBody := `{"model":"m","kind":"code","findings":[]}`
	finalBody := `{"combiner":"c","verdict":"approve"}`
	draft := mergeReviewFile(t, "draft.json", draftBody)
	final := mergeReviewFile(t, "final.json", finalBody)
	f := &modelMergeFlags{drafts: repeatedStringFlag{draft, draft}, final: &final}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	total := 2*len(draftBody) + len(finalBody)
	for _, budget := range []int{0, total - 1, total} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			_, _, err := loadMergeInputs(ctx, f, budget)
			if (err == nil) != (budget == total) {
				t.Fatalf("budget %d: %v", budget, err)
			}
		})
	}
}

// TestModelsMergeAggregateCLI enforces the shared byte budget at its CLI boundary.
func TestModelsMergeAggregateCLI(t *testing.T) {
	draftBody := `{"model":"m","kind":"code","findings":[]}`
	finalBody := `{"combiner":"c","verdict":"approve"}`
	draft := mergeReviewFile(t, "draft.json", draftBody+strings.Repeat(" ", mergeFileLimit-len(draftBody)))
	final := mergeReviewFile(t, "final.json", finalBody+strings.Repeat(" ", mergeFileLimit-len(finalBody)))
	for _, count := range []int{3, 4} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			args := []string{"merge-check", "--final", final}
			for i := 0; i < count; i++ {
				args = append(args, "--draft", draft)
			}
			_, _, err := runOutcomeCLI(t, args)
			want := 0
			if count == 4 {
				want = 2
			}
			if exitCode(err) != want {
				t.Fatalf("%d input bytes: %v", mergeFileLimit*(count+1), err)
			}
		})
	}
}

// TestModelsMergeReadBounds checks snapshot type, per-file bytes and canceled reads.
func TestModelsMergeReadBounds(t *testing.T) {
	body := `{}` + strings.Repeat(" ", mergeFileLimit-2)
	path := mergeReviewFile(t, "draft.json", body)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var target map[string]any
	remaining := mergeInputLimit
	if err := readMergeJSON(ctx, path, &target, &remaining); err != nil {
		t.Fatal(err)
	}
	if target == nil || remaining != mergeInputLimit-len(body) {
		t.Fatalf("boundary decode %v, remaining %d", target, remaining)
	}
	if err := readMergeJSON(ctx, filepath.Dir(path), &target, &remaining); err == nil || !strings.Contains(err.Error(), "source must be regular") {
		t.Fatalf("directory: %v", err)
	}
	cancel()
	if err := readMergeJSON(ctx, path, &target, &remaining); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

// TestModelsMergeOversize preserves the snapshot reader's named size error before decoding.
func TestModelsMergeOversize(t *testing.T) {
	path := mergeReviewFile(t, "draft.json", `{}`+strings.Repeat(" ", mergeFileLimit-1))
	var target map[string]any
	remaining := mergeInputLimit
	err := readMergeJSON(t.Context(), path, &target, &remaining)
	if !errors.Is(err, util.ErrFileTooLarge) || !strings.Contains(err.Error(), fmt.Sprintf("source exceeds %d bytes", mergeFileLimit)) {
		t.Fatalf("snapshot byte limit: %v", err)
	}
	if target != nil || remaining != mergeInputLimit {
		t.Fatalf("oversized input retained: %v, remaining %d", target, remaining)
	}
}

// TestModelsMergeLeafLink refuses a linked review without decoding or charging its target.
func TestModelsMergeLeafLink(t *testing.T) {
	path := mergeReviewFile(t, "draft.json", `{}`)
	link := filepath.Join(filepath.Dir(path), "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	var target map[string]any
	remaining := mergeInputLimit
	err := readMergeJSON(t.Context(), link, &target, &remaining)
	if err == nil || !strings.Contains(err.Error(), "source must be regular") {
		t.Fatalf("leaf link: %v", err)
	}
	if target != nil || remaining != mergeInputLimit {
		t.Fatalf("linked input retained: %v, remaining %d", target, remaining)
	}
}

// TestModelsMergeTerminalOutput escapes human diagnostics while retaining JSON data.
func TestModelsMergeTerminalOutput(t *testing.T) {
	draft := mergeReviewFile(t, "draft.json", `{"model":"m\u001b[2J\nforged","kind":"code"}`)
	final := mergeReviewFile(t, "final.json", `{"combiner":"c","verdict":"approve"}`)
	args := []string{"merge-check", "--draft", draft, "--final", final}
	out, _, err := runOutcomeCLI(t, args)
	if exitCode(err) != 1 || strings.ContainsAny(out, "\x1b\r") || strings.Count(out, "\n") != 1 {
		t.Fatalf("terminal output %q: %v", out, err)
	}
	out, _, err = runOutcomeCLI(t, append(args, "--json"))
	if exitCode(err) != 1 || !strings.Contains(out, `\u001b`) {
		t.Fatalf("JSON output %q: %v", out, err)
	}
}

// TestModelsMergeDecodeLimit refuses oversized finding arrays during loading, before validation.
func TestModelsMergeDecodeLimit(t *testing.T) {
	items := strings.Repeat(`{"id":"x"},`, reviewmerge.MaxFindings) + `{"id":"last"}`
	draft := mergeReviewFile(t, "draft.json", `{"model":"m","kind":"code","findings":[`+items+`]}`)
	final := mergeReviewFile(t, "final.json", `{"combiner":"c","verdict":"approve"}`)
	_, _, err := runOutcomeCLI(t, []string{"merge-check", "--draft", draft, "--final", final})
	if exitCode(err) != 2 {
		t.Fatalf("oversized decoded draft must be input error: %v", err)
	}
}
