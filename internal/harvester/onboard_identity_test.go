package harvester

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// onboardCheckout creates <tmp>/<parent>/<name> as a repository, with an origin remote
// when url is set.
func onboardCheckout(t *testing.T, parent, name, url string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	testsupport.InitGitRepoWithOrigin(t, dir, url)
	return dir
}

// Positive: onboarding reads the origin remote through util.ResolveRemoteIdentity.
func TestResolveGitIdentity_Positive_OriginRemote(t *testing.T) {
	dir := onboardCheckout(t, "parent-dir", "checkout-dir", "git@github.com:acme/widget.git")
	owner, name, err := resolveGitIdentity(t.Context(), dir)
	if err != nil || owner != "acme" || name != "widget" {
		t.Fatalf("resolveGitIdentity = %q/%q (err %v), want acme/widget", owner, name, err)
	}
}

// Negative: without a remote the identity is empty, not the checkout layout and not an error.
func TestResolveGitIdentity_Negative_NoRemoteIsEmpty(t *testing.T) {
	dir := onboardCheckout(t, "acme", "widget", "")
	owner, name, err := resolveGitIdentity(t.Context(), dir)
	if err != nil || owner != "" || name != "" {
		t.Fatalf("resolveGitIdentity = %q/%q (err %v), want an empty identity", owner, name, err)
	}
}

// Boundary: a cancelled read is an error, never an empty identity a manifest could record.
func TestResolveGitIdentity_Boundary_CancelledReadFails(t *testing.T) {
	dir := onboardCheckout(t, "acme", "widget", "https://github.com/acme/widget.git")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if owner, name, err := resolveGitIdentity(ctx, dir); err == nil || owner != "" || name != "" {
		t.Fatalf("resolveGitIdentity on a cancelled read = %q/%q (err %v), want an error", owner, name, err)
	}
}

// onboardManifest writes the onboarding manifest of dir and returns its text.
func onboardManifest(t *testing.T, dir string) string {
	t.Helper()
	if err := ensureOnboardingManifest(t.Context(), dir, "widget", "framework", nil); err != nil {
		t.Fatalf("ensureOnboardingManifest: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, config.ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Positive: a master checkout's onboarding manifest declares master, so a checkout without the
// origin HEAD renders the same ruleset.
func TestEnsureOnboardingManifest_Positive_DeclaresAMasterOriginHead(t *testing.T) {
	dir := onboardCheckout(t, "acme", "widget", "https://github.com/acme/widget.git")
	testsupport.RecordOriginHead(t, dir, "master")
	onboardManifest(t, dir)
	manifest, err := config.LoadManifest(filepath.Join(dir, config.ManifestFileName))
	if err != nil || manifest.Repository.DefaultBranch != "master" {
		t.Fatalf("onboarding a master checkout must declare master: %+v, %v", manifest, err)
	}
}

// Negative: an origin HEAD the ruleset cannot carry fails onboarding and writes no manifest.
func TestEnsureOnboardingManifest_Negative_UnusableOriginHeadFails(t *testing.T) {
	dir := onboardCheckout(t, "acme", "widget", "https://github.com/acme/widget.git")
	testsupport.RecordOriginHead(t, dir, "_private")
	err := ensureOnboardingManifest(t.Context(), dir, "widget", "framework", nil)
	if err == nil || !strings.Contains(err.Error(), "repository.default_branch") {
		t.Fatalf("an origin HEAD the ruleset cannot carry must fail onboarding, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, config.ManifestFileName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("a failed default-branch read must leave no manifest: %v", statErr)
	}
}

// Boundary: an origin HEAD at main, or none, declares no default branch.
func TestEnsureOnboardingManifest_Boundary_MainOrNoOriginHeadDeclaresNothing(t *testing.T) {
	for _, head := range []string{"main", ""} {
		dir := onboardCheckout(t, "acme", "widget-"+head, "https://github.com/acme/widget.git")
		if head != "" {
			testsupport.RecordOriginHead(t, dir, head)
		}
		if text := onboardManifest(t, dir); strings.Contains(text, "default_branch") {
			t.Fatalf("origin HEAD %q must declare nothing:\n%s", head, text)
		}
	}
}
