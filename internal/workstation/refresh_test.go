// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package workstation

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

// installedEngine is an engine checkout with a fake install of its first commit recorded in a
// temp manifest, the state a workstation is in after `workstation install` and a later pull.
type installedEngine struct {
	*engineCheckout
	opts      Options
	installed string
	branch    string
}

func newInstalledEngine(t *testing.T) *installedEngine {
	t.Helper()
	c := newEngineCheckout(t)
	dir := t.TempDir()
	opts := Options{
		Checkout: c.root, BinDir: filepath.Join(dir, "bin"), ManifestPath: filepath.Join(dir, "config", "install.json"),
		Build: fakeBuild("v1"), GOOS: "linux",
		Now: func() time.Time { return time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC) },
	}
	if _, err := Install(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	return &installedEngine{engineCheckout: c, opts: opts,
		installed: c.git("rev-parse", "HEAD"), branch: c.git("symbolic-ref", "--short", "HEAD")}
}

// refreshOptions refreshes with a v2 build from the manifest's own bin directory, on branch.
func (e *installedEngine) refreshOptions(branch string) RefreshOptions {
	install := e.opts
	install.BinDir = ""
	install.Build = fakeBuild("v2")
	return RefreshOptions{Install: install, Module: engineModule,
		Select: func(context.Context, string) (RefreshSettings, error) { return RefreshSettings{Branch: branch}, nil }}
}

// rewriteHead replaces the HEAD commit with one of different content, so the old HEAD stays in
// the repository but is no longer an ancestor of the new one.
func (c *engineCheckout) rewriteHead(message string) string {
	c.t.Helper()
	c.write("cmd/engine/"+message+".go", "package main\n")
	c.git("add", "-A")
	c.git("-c", "user.email=test@example.com", "-c", "user.name=test", "commit", "--amend", "-q", "-m", message)
	return c.git("rev-parse", "HEAD")
}

func requireSkipped(t *testing.T, result RefreshResult, err error, fragment string) {
	t.Helper()
	if err != nil {
		t.Fatalf("a skipped refresh is not an error: %v", err)
	}
	if result.Refreshed || result.Install != nil || !strings.Contains(result.Reason, fragment) {
		t.Fatalf("want a skip naming %q, got %+v", fragment, result)
	}
}

func (e *installedEngine) requireBinary(t *testing.T, want string) {
	t.Helper()
	assertContent(t, filepath.Join(e.opts.BinDir, "praetorctl"), want)
}

// Positive: after a merge on the update branch the lagging install is rebuilt in the bin
// directory the manifest recorded, and the manifest records the checkout HEAD.
func TestRefresh_Positive_LaggingInstallIsRebuilt(t *testing.T) {
	e := newInstalledEngine(t)
	e.write("internal/render/render.go", "package render\n")
	head := e.commit("new renderer")
	result, err := Refresh(context.Background(), e.refreshOptions(e.branch))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Refreshed || result.CommitsBehind != 1 || result.Install == nil {
		t.Fatalf("want a refresh one commit behind, got %+v", result)
	}
	e.requireBinary(t, "v2:praetorctl")
	manifest, err := config.ReadInstallManifest(context.Background(), e.opts.ManifestPath)
	if err != nil || manifest.EngineCommit != head || manifest.Previous == nil || manifest.Previous.EngineCommit != e.installed {
		t.Fatalf("manifest after refresh: %+v, %v", manifest, err)
	}
}

// Positive: an untracked file (a receipt, a scratch note) does not block a refresh.
func TestRefresh_Positive_UntrackedFilesDoNotBlock(t *testing.T) {
	e := newInstalledEngine(t)
	e.write("internal/render/render.go", "package render\n")
	e.commit("new renderer")
	e.write("scratch.txt", "local note\n")
	if result, err := Refresh(context.Background(), e.refreshOptions(e.branch)); err != nil || !result.Refreshed {
		t.Fatalf("untracked file blocked the refresh: %+v, %v", result, err)
	}
}

// Negative: nothing installed, another branch, a modified tree, and an install that is not
// behind the checkout all install nothing and say why.
func TestRefresh_Negative_Skips(t *testing.T) {
	e := newInstalledEngine(t)
	e.write("internal/render/render.go", "package render\n")
	e.commit("new renderer")

	missing := e.refreshOptions(e.branch)
	missing.Install.ManifestPath = filepath.Join(t.TempDir(), "install.json")
	result, err := Refresh(context.Background(), missing)
	requireSkipped(t, result, err, "no install manifest")

	result, err = Refresh(context.Background(), e.refreshOptions("release"))
	requireSkipped(t, result, err, `not the update branch "release"`)

	e.write("cmd/engine/main.go", "package main\n\nfunc main() { println() }\n")
	result, err = Refresh(context.Background(), e.refreshOptions(e.branch))
	requireSkipped(t, result, err, "modified tracked files")
	e.git("checkout", "-q", "--", "cmd/engine/main.go")

	e.git("reset", "-q", "--hard", e.installed)
	e.rewriteHead("sideways")
	result, err = Refresh(context.Background(), e.refreshOptions(e.branch))
	requireSkipped(t, result, err, "is not behind the checkout HEAD")
	e.requireBinary(t, "v1:praetorctl")
}

// Negative: a checkout of another module never reaches the manifest or the settings, and a
// failing settings selection or a missing context is an error.
func TestRefresh_Negative_ForeignCheckoutAndErrors(t *testing.T) {
	e := newInstalledEngine(t)
	foreign := e.refreshOptions(e.branch)
	foreign.Module = "example.com/other"
	foreign.Select = func(context.Context, string) (RefreshSettings, error) {
		t.Fatal("a foreign checkout must not select operator settings")
		return RefreshSettings{}, nil
	}
	result, err := Refresh(context.Background(), foreign)
	requireSkipped(t, result, err, "not a checkout of this engine's module")

	failing := e.refreshOptions(e.branch)
	failing.Select = func(context.Context, string) (RefreshSettings, error) {
		return RefreshSettings{}, errors.New("settings changed since the install manifest recorded them")
	}
	if _, err := Refresh(context.Background(), failing); err == nil || !strings.Contains(err.Error(), "select settings") {
		t.Fatalf("a failing selection must be an error, got %v", err)
	}
	var noContext context.Context
	if _, err := Refresh(noContext, e.refreshOptions(e.branch)); err == nil {
		t.Fatal("nil context accepted")
	}
	noSelector := e.refreshOptions(e.branch)
	noSelector.Select = nil
	if _, err := Refresh(context.Background(), noSelector); err == nil {
		t.Fatal("nil settings selector accepted")
	}
}

// Boundary: an install exactly at the checkout HEAD, a detached HEAD and an empty update
// branch are not refreshed.
func TestRefresh_Boundary_NothingToCatchUp(t *testing.T) {
	e := newInstalledEngine(t)
	result, err := Refresh(context.Background(), e.refreshOptions(e.branch))
	requireSkipped(t, result, err, "is not behind the checkout HEAD")

	e.write("internal/render/render.go", "package render\n")
	head := e.commit("new renderer")
	result, err = Refresh(context.Background(), e.refreshOptions(""))
	requireSkipped(t, result, err, `not the update branch ""`)

	e.git("checkout", "-q", "--detach", head)
	result, err = Refresh(context.Background(), e.refreshOptions(e.branch))
	requireSkipped(t, result, err, `checkout is on ""`)
	e.requireBinary(t, "v1:praetorctl")
}

// Positive: InstallLag counts the commits the checkout HEAD is ahead of the install.
func TestInstallLag_Positive_CountsCommitsBehind(t *testing.T) {
	c := newEngineCheckout(t)
	installed := c.git("rev-parse", "HEAD")
	c.write("a.go", "package engine\n")
	c.commit("one")
	c.write("b.go", "package engine\n")
	c.commit("two")
	behind, ancestor, err := InstallLag(context.Background(), c.root, installed)
	if err != nil || !ancestor || behind != 2 {
		t.Fatalf("InstallLag = %d, %v, %v; want 2, true, nil", behind, ancestor, err)
	}
}

// Negative: a commit the checkout does not hold, one that is not an ancestor, and a malformed
// id report no lag and no error.
func TestInstallLag_Negative_NoLineOfDescent(t *testing.T) {
	c := newEngineCheckout(t)
	c.write("a.go", "package engine\n")
	ahead := c.commit("ahead")
	c.git("reset", "-q", "--hard", "HEAD~1")
	for _, installed := range []string{strings.Repeat("ab", 20), ahead, "HEAD", "--all"} {
		behind, ancestor, err := InstallLag(context.Background(), c.root, installed)
		if err != nil || ancestor || behind != 0 {
			t.Fatalf("InstallLag(%q) = %d, %v, %v; want 0, false, nil", installed, behind, ancestor, err)
		}
	}
}

// Boundary: an install at the checkout HEAD is an ancestor zero commits behind.
func TestInstallLag_Boundary_AtHead(t *testing.T) {
	c := newEngineCheckout(t)
	behind, ancestor, err := InstallLag(context.Background(), c.root, c.git("rev-parse", "HEAD"))
	if err != nil || !ancestor || behind != 0 {
		t.Fatalf("InstallLag at HEAD = %d, %v, %v; want 0, true, nil", behind, ancestor, err)
	}
}

// Positive and boundary: status reports how far the install lags, zero at HEAD, and omits
// the count when the installed commit is not in the checkout's line of descent.
func TestStatusReportsCommitsBehind(t *testing.T) {
	e := newInstalledEngine(t)
	report, err := Status(context.Background(), StatusOptions{Checkout: e.root, ManifestPath: e.opts.ManifestPath})
	if err != nil || report.CommitsBehind == nil || *report.CommitsBehind != 0 || !report.UpToDate {
		t.Fatalf("status at HEAD: %+v, %v", report, err)
	}
	e.write("internal/render/render.go", "package render\n")
	e.commit("new renderer")
	report, err = Status(context.Background(), StatusOptions{Checkout: e.root, ManifestPath: e.opts.ManifestPath})
	if err != nil || report.CommitsBehind == nil || *report.CommitsBehind != 1 || report.UpToDate {
		t.Fatalf("status one commit behind: %+v, %v", report, err)
	}
	other := newEngineCheckout(t)
	other.rewriteHead("unrelated")
	report, err = Status(context.Background(), StatusOptions{Checkout: other.root, ManifestPath: e.opts.ManifestPath})
	if err != nil || report.CommitsBehind != nil {
		t.Fatalf("status against an unrelated checkout must omit the count: %+v, %v", report, err)
	}
}
