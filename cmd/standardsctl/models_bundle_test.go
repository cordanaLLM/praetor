// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/reviewbundle"
	"github.com/cordanaLLM/praetor/internal/util"
)

// modelBundleRepo plants a real conflicting merge under isolated fixture Git settings.
func modelBundleRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	env := util.FilterEnvironment(os.Environ(), func(name string) bool { return strings.HasPrefix(strings.ToUpper(name), "GIT_") })
	env = append(env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	git := func(args ...string) {
		t.Helper()
		argv := append([]string{"-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=" + os.DevNull, "-c", "maintenance.auto=false", "-c", "gc.auto=0"}, args...)
		fixtureGit(t, dir, env, argv...)
	}
	git("init", "-q", "-b", "target")
	writeFixtureFile(t, dir, "file.txt", "base\n")
	git("add", ".")
	git("commit", "-qm", "base")
	git("checkout", "-qb", "head")
	writeFixtureFile(t, dir, "file.txt", "head\n")
	git("commit", "-qam", "head")
	git("checkout", "-q", "target")
	writeFixtureFile(t, dir, "file.txt", "target\n")
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
