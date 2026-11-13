// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
