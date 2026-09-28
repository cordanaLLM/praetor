// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
)

// rulesetOnDisk reports whether dir carries the branch ruleset.
func rulesetOnDisk(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".github", "rulesets", "main.json"))
	return err == nil
}

// Positive: flavor apply writes the branch ruleset and lists every required setting, naming the
// command that writes each one it does not.
func TestFlavorApply_Positive_WritesTheRulesetAndListsSettings(t *testing.T) {
	dir := t.TempDir()
	out, err := captureStdout(t, func() error {
		return dispatchCommand("flavor", []string{"apply", dir, "--flavor=go-service"})
	})
	if err != nil {
		t.Fatalf("flavor apply: %v\n%s", err, out)
	}
	mustContain(t, out, "Settings (3):", "lefthook.yml: deferred (praetorctl adopt)",
		".vscode/settings.json: deferred (praetorctl editors generate)", ".github/rulesets/main.json: created (")
	if !rulesetOnDisk(dir) {
		t.Fatal("flavor apply did not write the ruleset")
	}
}

// Negative: a ruleset adoption.decline refuses is not written by flavor apply either, and a
// manifest the decline cannot be read from fails the apply without writing the ruleset.
func TestFlavorApply_Negative_HonoursAdoptionDecline(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".standards.yaml"), []byte("version: 1\nadoption:\n  decline:\n    - branch-ruleset\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, func() error {
		return dispatchCommand("flavor", []string{"apply", dir, "--flavor=native-gpu-systems"})
	})
	if err != nil {
		t.Fatalf("flavor apply: %v\n%s", err, out)
	}
	mustContain(t, out, ".github/rulesets/main.json: declined (adoption.decline names branch-ruleset)")
	if rulesetOnDisk(dir) {
		t.Fatal("a declined ruleset was written")
	}

	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, ".standards.yaml"), []byte("version: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err = captureStdout(t, func() error {
		return dispatchCommand("flavor", []string{"apply", broken, "--flavor=native-gpu-systems"})
	})
	if err == nil || !strings.Contains(out, "resolve adoption.decline for branch-ruleset") || rulesetOnDisk(broken) {
		t.Fatalf("an unreadable manifest must fail the ruleset: %v\n%s", err, out)
	}
}

// Boundary: the usage line says what apply writes.
func TestFlavorUsage_Boundary_DescribesSettingsApplyWrites(t *testing.T) {
	usage, err := captureStdout(t, func() error { printFlavorUsage(); return nil })
	if err != nil || !strings.Contains(usage, "Scaffold a flavor's templates and branch ruleset; list settings other commands write") {
		t.Fatalf("usage does not describe apply: %v\n%s", err, usage)
	}
}

// printAdoptPreviews prints the rendered file for a create, the diff for an update and nothing
// beyond the heading for an unchanged file.
func TestPrintAdoptPreviews_3D(t *testing.T) {
	out, err := captureStdout(t, func() error {
		printAdoptPreviews([]adopt.FilePreview{
			{Path: "r.json", Action: adopt.PreviewCreate, Content: "{\"a\": 1}\n", Note: "0 required status checks"},
			{Path: "r.json", Action: adopt.PreviewUpdate, Diff: "--- a/r.json\n+++ b/r.json\n"},
			{Path: "r.json", Action: adopt.PreviewUnchanged},
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "--- Preview: r.json (create) ---\n  0 required status checks\n{\"a\": 1}\n",
		"--- Preview: r.json (update) ---\n--- a/r.json\n+++ b/r.json\n", "--- Preview: r.json (unchanged) ---\n")
	if empty, err := captureStdout(t, func() error { printAdoptPreviews(nil); return nil }); err != nil || empty != "" {
		t.Fatalf("no previews must print nothing, got %q, %v", empty, err)
	}
}
