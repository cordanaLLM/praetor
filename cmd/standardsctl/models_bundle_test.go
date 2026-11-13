// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/reviewbundle"
	"github.com/cordanaLLM/praetor/internal/util"
)

// modelBundleRepo plants a real conflicting merge under isolated fixture Git settings.
func modelBundleRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	env := util.FilterEnvironment(os.Environ(), func(name string) bool { return strings.HasPrefix(strings.ToUpper(name), "GIT_") })
	env = append(env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	ctx, err := util.WithCommandEnvironment(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		argv := append([]string{"-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=" + os.DevNull, "-c", "maintenance.auto=false", "-c", "gc.auto=0"}, args...)
		if out, err := util.RunGitProbe(ctx, dir, 1<<20, argv...); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out.Stderr)
		}
	}
	git("init", "-q", "-b", "target")
	if err := contextopt.WriteSnapshot(t.Context(), filepath.Join(dir, "file.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-qm", "base")
	git("checkout", "-qb", "head")
	if err := contextopt.WriteSnapshot(t.Context(), filepath.Join(dir, "file.txt"), []byte("head\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("commit", "-qam", "head")
	git("checkout", "-q", "target")
	if err := contextopt.WriteSnapshot(t.Context(), filepath.Join(dir, "file.txt"), []byte("target\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("commit", "-qam", "target")
	return dir
}

// TestModelsBundleOutputTimeout refuses a stalled pipe within its own two-second deadline.
func TestModelsBundleOutputTimeout(t *testing.T) {
	cases := []struct {
		name   string
		asJSON bool
	}{{"text", false}, {"JSON", true}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { checkModelBundleOutputTimeout(t, tc.name, tc.asJSON) })
	}
}

// checkModelBundleOutputTimeout writes into a pipe nobody reads and expects the named deadline error.
func checkModelBundleOutputTimeout(t *testing.T, name string, asJSON bool) {
	t.Helper()
	reader, writer := io.Pipe()
	t.Cleanup(func() {
		if err := errors.Join(reader.Close(), writer.Close()); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- writeModelBundleOutput(ctx, writer, reviewbundle.Bundle{}, asJSON) }()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "bundle "+name) {
			t.Fatalf("want named output timeout, got %v", err)
		}
	case <-timer.C:
		t.Fatal("bundle output blocked past two-second test deadline")
	}
}

// TestModelsBundleOutputCanceled refuses output after the shared build budget expires.
func TestModelsBundleOutputCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, asJSON := range []bool{false, true} {
		var out bytes.Buffer
		err := writeModelBundleOutput(ctx, &out, reviewbundle.Bundle{}, asJSON)
		if !errors.Is(err, context.Canceled) || out.Len() != 0 {
			t.Fatalf("JSON=%t: canceled output wrote %d bytes: %v", asJSON, out.Len(), err)
		}
	}
}

type modelBundleRefusingWriter struct{ err error }

// Write plants an output failure without touching a real descriptor.
func (w modelBundleRefusingWriter) Write([]byte) (int, error) { return 0, w.err }

// TestModelsBundleOutputFailure preserves writer errors through the context wrapper.
func TestModelsBundleOutputFailure(t *testing.T) {
	want := errors.New("output refused")
	for _, asJSON := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		err := writeModelBundleOutput(ctx, modelBundleRefusingWriter{want}, reviewbundle.Bundle{}, asJSON)
		cancel()
		if !errors.Is(err, want) {
			t.Fatalf("JSON=%t: lost writer error: %v", asJSON, err)
		}
	}
}

// TestModelsBundleOutputPositive preserves every output byte in both formats.
func TestModelsBundleOutputPositive(t *testing.T) {
	b := reviewbundle.Bundle{Files: []reviewbundle.File{{Path: "full", Content: "first\nlast"}}}
	wantText := "Target: \nHead: \nMerge base: \nPredicted tree: \n\"full\" TRUNCATED: no (omitted bytes: 0; deleted: false; binary: false)\nfirst\nlast\n"
	var wantJSON bytes.Buffer
	if err := json.NewEncoder(&wantJSON).Encode(b); err != nil {
		t.Fatal(err)
	}
	for _, asJSON := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		var out bytes.Buffer
		err := writeModelBundleOutput(ctx, &out, b, asJSON)
		cancel()
		want := wantText
		if asJSON {
			want = wantJSON.String()
		}
		if err != nil || out.String() != want {
			t.Fatalf("JSON=%t: output %q, error %v", asJSON, out.String(), err)
		}
	}
}

// TestModelsBundleOutputContentBoundary retains exactly the default two-MiB content cap.
func TestModelsBundleOutputContentBoundary(t *testing.T) {
	content := strings.Repeat("x", 2<<20)
	b := reviewbundle.Bundle{
		Files:  []reviewbundle.File{{Path: "cap", Content: content}},
		Limits: reviewbundle.Options{MaxFileBytes: len(content)},
	}
	for _, asJSON := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		var out bytes.Buffer
		err := writeModelBundleOutput(ctx, &out, b, asJSON)
		cancel()
		if err != nil || strings.Count(out.String(), content) != 1 {
			t.Fatalf("JSON=%t: content at two-MiB cap lost: %v", asJSON, err)
		}
	}
}

type modelBundleCappedWriter struct{ remaining int }

// Write accepts exactly the configured byte budget and refuses any excess.
func (w *modelBundleCappedWriter) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		return 0, io.ErrShortWrite
	}
	w.remaining -= len(p)
	return len(p), nil
}

// TestModelsBundleOutputWriterBoundary completes when a writer accepts exactly two MiB.
func TestModelsBundleOutputWriterBoundary(t *testing.T) {
	const capBytes = 2 << 20
	for _, asJSON := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		b := reviewbundle.Bundle{
			Files:  []reviewbundle.File{{Path: "cap"}},
			Limits: reviewbundle.Options{MaxFileBytes: capBytes},
		}
		var metadata bytes.Buffer
		if err := writeModelBundleOutput(ctx, &metadata, b, asJSON); err != nil {
			cancel()
			t.Fatal(err)
		}
		b.Files[0].Content = strings.Repeat("x", capBytes-metadata.Len())
		out := modelBundleCappedWriter{remaining: capBytes}
		err := writeModelBundleOutput(ctx, &out, b, asJSON)
		cancel()
		if err != nil || out.remaining != 0 {
			t.Fatalf("JSON=%t: exact-cap writer has %d bytes left: %v", asJSON, out.remaining, err)
		}
	}
}

// TestModelsBundleConflictAndJSON checks successful conflict findings and visible truncation.
func TestModelsBundleConflictAndJSON(t *testing.T) {
	dir := modelBundleRepo(t)
	args := []string{"bundle", "--repo=" + dir, "--target=target", "--head=head", "--max-file-bytes=4"}
	out, err := captureStdout(t, func() error { return runModels(args) })
	if err != nil {
		t.Fatalf("conflict must exit zero: %v", err)
	}
	wants := []string{"CONFLICT", "file.txt", "TRUNCATED: yes"}
	for i := 0; i < len(wants); i++ {
		want := wants[i]
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q: %s", want, out)
		}
	}
	out, err = captureStdout(t, func() error { return runModels(append(args, "--json")) })
	var b reviewbundle.Bundle
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatal(err)
	}
	if len(b.Conflicts) != 1 || len(b.Files) != 1 || !b.Files[0].Truncated {
		t.Fatalf("json: %+v", b)
	}
}

// TestModelsBundleUsage checks error status two, reasons and the non-truncated text boundary.
func TestModelsBundleUsage(t *testing.T) {
	dir := modelBundleRepo(t)
	cases := [][]string{{"--head=missing-ref"}, {"--max-files=-1"}, {"--unknown"}, {"extra"}}
	for i := 0; i < len(cases); i++ {
		extra := cases[i]
		args := append([]string{"bundle", "--repo=" + dir, "--target=target", "--head=head"}, extra...)
		var diagnostic bytes.Buffer
		_, stderr, err := runOutcomeCLI(t, args)
		if err == nil || commandExitCode(&diagnostic, err) != 2 || stderr == "" {
			t.Fatalf("usage %v: %v, %s, %s", extra, err, stderr, diagnostic.String())
		}
	}
	out, err := captureStdout(t, func() error { return runModels([]string{"bundle", "--repo=" + dir, "--target=head", "--head=head"}) })
	if err != nil || strings.Contains(out, "CONFLICT") {
		t.Fatalf("empty bundle: %s, %v", out, err)
	}
	out, err = captureStdout(t, func() error { return runModels([]string{"bundle", "--repo=" + dir, "--target=target", "--head=head"}) })
	if err != nil || !strings.Contains(out, "TRUNCATED: no") {
		t.Fatalf("uncut bundle: %s, %v", out, err)
	}
}

// TestModelsBundleReviewTextContent exposes complete and truncated source bytes in text output.
func TestModelsBundleReviewTextContent(t *testing.T) {
	cases := []reviewbundle.File{
		{Path: "full", Content: "first\nlast"},
		{Path: "cut", Content: "prefix", Truncated: true, TruncatedBytes: 3},
		{Path: "bytes", Content: "raw\xff"},
	}
	for i := 0; i < len(cases); i++ {
		f := cases[i]
		b := reviewbundle.Bundle{Files: []reviewbundle.File{f}}
		if f.Truncated {
			b.Cut = []string{f.Path}
		}
		var out bytes.Buffer
		if err := writeModelBundle(&out, b); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "\n"+f.Content+"\n") {
			t.Errorf("content missing: %q", out.String())
		}
	}
	if err := writeModelBundle(io.Discard, reviewbundle.Bundle{Cut: make([]string, 4097)}); err == nil {
		t.Fatal("oversized text inventory accepted")
	}
}
