// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// netnsModule is the fixture module a go-test-netns entry names, the shape of an adopter module
// whose tests exercise a root-ownership check.
const netnsModule = "tools/needs-root/go.mod"

// netnsManifest renders a .standards.yaml holding one go-test-netns entry for module, entered
// 120 days (category D) before expires.
func netnsManifest(module string, expires time.Time) string {
	return fmt.Sprintf("exceptions:\n  - rule: go-test-netns\n    path: %s\n    category: D\n    entered: %s\n    expires: %s\n    reason: tests run the production ssh-keygen owner check, which needs host root mapped\n",
		module, expires.AddDate(0, 0, -120).Format("2006-01-02"), expires.Format("2006-01-02"))
}

// netnsRepo writes a repository root with the module go.mod files and, when manifest is not
// empty, a .standards.yaml, and returns the root.
func netnsRepo(t *testing.T, manifest string, modules ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, module := range modules {
		path := filepath.Join(root, filepath.FromSlash(module))
		// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a directory the test writes files into needs the owner execute bit; 0o700 is least privilege
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("module example.test/"+filepath.Base(filepath.Dir(path))+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if manifest != "" {
		if err := os.WriteFile(filepath.Join(root, ".standards.yaml"), []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// raceModule runs one module's race command against the fixture root with a recording runner on
// local Linux, and returns the message, the recorded go test invocations and the error.
func raceModule(t *testing.T, root, module string) (string, []recordedCommand, error) {
	t.Helper()
	t.Setenv("CI", "")
	t.Setenv("GITHUB_ACTIONS", "")
	cfg, recorded := newTestConfig(t, root, false)
	cfg.goos = "linux"
	dir := filepath.Join(root, filepath.Dir(filepath.FromSlash(module)))
	msg, err := runGoRaceCommand(t.Context(), cfg, goModule{file: module, dir: dir}, dir, time.Minute, raceScopeConfig{})
	return msg, testInvocations(*recorded), err
}

// TestGoRaceDeclaredModuleRunsUnwrapped pins that a live go-test-netns entry runs its module's
// test binaries without the namespace wrapper and names the entry, while the undeclared sibling
// module of the same repository keeps the wrapper.
func TestGoRaceDeclaredModuleRunsUnwrapped(t *testing.T) {
	const sibling = "tools/sibling/go.mod"
	root := netnsRepo(t, netnsManifest(netnsModule, time.Now().AddDate(0, 0, 30)), netnsModule, sibling)
	msg, runs, err := raceModule(t, root, netnsModule)
	if err != nil || len(runs) != 1 || slices.Contains(runs[0].args, "-exec") {
		t.Fatalf("declared module still wrapped: runs=%+v error=%v", runs, err)
	}
	if !strings.Contains(msg, "exception go-test-netns") || !strings.Contains(msg, "category D") {
		t.Fatalf("message does not name the exception: %q", msg)
	}
	msg, runs, err = raceModule(t, root, sibling)
	if err != nil || len(runs) != 1 || !slices.Contains(runs[0].args, "-exec") || !strings.Contains(msg, "loopback-only network namespace") {
		t.Fatalf("undeclared sibling lost the wrapper: runs=%+v message=%q error=%v", runs, msg, err)
	}
}

// TestGoRaceExpiredDeclarationFails pins that an entry expired yesterday fails its module before
// any test binary runs, naming the entry, instead of falling back to either the wrapped or the
// unwrapped path; an entry expiring today still holds (boundary).
func TestGoRaceExpiredDeclarationFails(t *testing.T) {
	expired := netnsRepo(t, netnsManifest(netnsModule, time.Now().AddDate(0, 0, -1)), netnsModule)
	_, runs, err := raceModule(t, expired, netnsModule)
	if err == nil || len(runs) != 0 || !strings.Contains(err.Error(), "expired") || !strings.Contains(err.Error(), netnsModule) {
		t.Fatalf("expired entry did not fail its module: runs=%+v error=%v", runs, err)
	}
	today := netnsRepo(t, netnsManifest(netnsModule, time.Now()), netnsModule)
	if _, runs, err := raceModule(t, today, netnsModule); err != nil || len(runs) != 1 || slices.Contains(runs[0].args, "-exec") {
		t.Fatalf("entry expiring today does not hold: runs=%+v error=%v", runs, err)
	}
}

// TestGoRaceDeclarationWithoutWrapper pins the paths where goTestExec wraps nothing (CI on Linux,
// and a host without Linux namespaces): a live entry leaves the execution and its note unchanged,
// and an expired entry still fails the module before any test binary runs.
func TestGoRaceDeclarationWithoutWrapper(t *testing.T) {
	live := netnsRepo(t, netnsManifest(netnsModule, time.Now().AddDate(0, 0, 30)), netnsModule)
	expired := netnsRepo(t, netnsManifest(netnsModule, time.Now().AddDate(0, 0, -1)), netnsModule)
	for _, host := range []struct{ goos, ci, note string }{{"linux", "true", "CI execution unchanged"}, {"darwin", "", "has no Linux network namespaces"}} {
		t.Setenv("CI", host.ci)
		t.Setenv("GITHUB_ACTIONS", "")
		for _, root := range []string{live, expired} {
			cfg, recorded := newTestConfig(t, root, false)
			cfg.goos = host.goos
			dir := filepath.Join(root, "tools", "needs-root")
			msg, err := runGoRaceCommand(t.Context(), cfg, goModule{file: netnsModule, dir: dir}, dir, time.Minute, raceScopeConfig{})
			runs := testInvocations(*recorded)
			if root == expired && (err == nil || !strings.Contains(err.Error(), "expired") || len(runs) != 0) {
				t.Fatalf("%s: expired entry did not fail without a wrapper: runs=%+v error=%v", host.goos, runs, err)
			}
			if root == live && (err != nil || len(runs) != 1 || slices.Contains(runs[0].args, "-exec") || !strings.Contains(msg, host.note)) {
				t.Fatalf("%s: live entry changed the wrapper-free path: runs=%+v message=%q error=%v", host.goos, runs, msg, err)
			}
		}
	}
}

// TestGoRaceExpiredDeclarationFailsEmptyModule pins that an expired entry fails its module even
// when the module holds no Go packages, instead of the stage reporting the module as empty.
func TestGoRaceExpiredDeclarationFailsEmptyModule(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("GITHUB_ACTIONS", "")
	root := netnsRepo(t, netnsManifest(netnsModule, time.Now().AddDate(0, 0, -1)), netnsModule)
	cfg, recorded := newTestConfig(t, root, false)
	cfg.goos = "linux"
	base := cfg.run
	cfg.run = func(ctx context.Context, dir, name string, args ...string) (string, error) {
		if name == "go" && len(args) > 0 && args[0] == "list" {
			return "", nil
		}
		return base(ctx, dir, name, args...)
	}
	dir := filepath.Join(root, "tools", "needs-root")
	msg, err := runGoRaceCommand(t.Context(), cfg, goModule{file: netnsModule, dir: dir}, dir, time.Minute, raceScopeConfig{})
	if err == nil || !strings.Contains(err.Error(), "expired") || len(testInvocations(*recorded)) != 0 {
		t.Fatalf("expired entry on an empty module passed: message=%q error=%v", msg, err)
	}
}

// TestGoRaceNetnsEntriesReadOncePerStage pins that one stage judges every module against the
// entries it read first: a manifest rewritten after the stale check changes no module's decision.
func TestGoRaceNetnsEntriesReadOncePerStage(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("GITHUB_ACTIONS", "")
	root := netnsRepo(t, netnsManifest(netnsModule, time.Now().AddDate(0, 0, 30)), netnsModule)
	cfg, _ := newTestConfig(t, root, false)
	cfg.goos = "linux"
	cfg.moduleDiscovery = func(context.Context, string) ([]string, error) { return []string{netnsModule}, nil }
	if err := staleNetnsEntries(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, ".standards.yaml"), netnsManifest(netnsModule, time.Now().AddDate(0, 0, -1)))
	if execFlag, _, err := cfg.moduleTestExec(t.Context(), goModule{file: netnsModule}); err != nil || execFlag != "" {
		t.Fatalf("module judged against a manifest read after the stale check: exec=%q error=%v", execFlag, err)
	}
}

// TestGoRaceStaleDeclarationFails pins that an entry naming no tracked Go module fails the race
// stage before any module runs, as a stale gosec entry fails the security stage, while an entry
// naming a tracked module passes the same check.
func TestGoRaceStaleDeclarationFails(t *testing.T) {
	repo, cfg, _ := trackedModuleFixture(t, true)
	writeFile(t, filepath.Join(repo, ".standards.yaml"), netnsManifest("tooling/go.mod", time.Now().AddDate(0, 0, 30)))
	if _, err := runGoTests(t.Context(), cfg); err != nil {
		t.Fatalf("entry naming a tracked module refused: %v", err)
	}
	// A stage configuration reads the manifest once, so the stale case is its own stage.
	repo, cfg, recorded := trackedModuleFixture(t, true)
	writeFile(t, filepath.Join(repo, ".standards.yaml"), netnsManifest("gone/go.mod", time.Now().AddDate(0, 0, 30)))
	_, err := runGoTests(t.Context(), cfg)
	if err == nil || !strings.Contains(err.Error(), "gone/go.mod") || !strings.Contains(err.Error(), "no tracked Go module") || len(testInvocations(*recorded)) != 0 {
		t.Fatalf("stale entry passed: error=%v runs=%+v", err, testInvocations(*recorded))
	}
}
