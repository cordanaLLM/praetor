package harvester

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
