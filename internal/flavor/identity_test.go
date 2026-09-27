package flavor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// checkoutAt creates <tmp>/<parent>/<name>, the layout util.ResolveRepoIdentity's directory
// fallback reads as parent/name, as a repository with an origin remote when url is set.
func checkoutAt(t *testing.T, parent, name, url string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if url != "" {
		testsupport.InitGitRepoWithOrigin(t, dir, url)
	}
	return dir
}

// identityFlavor is the flavor whose scaffold renders the repository identity: its
// .gitleaks.toml body titles the policy for the repository (templates/native/.gitleaks.toml.tmpl).
// Every other embedded body is identity-free.
const identityFlavor = "native-gpu-systems"

// gitleaksTitle returns the title line the scaffolded .gitleaks.toml carries for subject.
func gitleaksTitle(subject string) string {
	return "title = \"gitleaks configuration for " + subject + "\"\n"
}

// applyAndRead applies identityFlavor to dir and returns the named scaffold bodies.
func applyAndRead(t *testing.T, dir string, rels ...string) map[string]string {
	t.Helper()
	report, err := ApplyFlavor(t.Context(), dir, identityFlavor, false)
	if err != nil {
		t.Fatalf("apply %s: %v", identityFlavor, err)
	}
	if len(report.Errors) > 0 {
		t.Fatalf("apply %s recorded errors: %s", identityFlavor, strings.Join(report.Errors, "; "))
	}
	bodies := make(map[string]string, len(rels))
	for _, rel := range rels {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		bodies[rel] = string(data)
	}
	return bodies
}

// Positive: the origin remote names the rendered owner and name, not the checkout path.
func TestApplyFlavor_Positive_IdentityFromOriginRemote(t *testing.T) {
	dir := checkoutAt(t, "parent-dir", "checkout-dir", "https://github.com/acme/widget.git")
	body := applyAndRead(t, dir, ".gitleaks.toml")[".gitleaks.toml"]
	if !strings.Contains(body, gitleaksTitle("acme/widget")) {
		t.Fatalf("gitleaks policy does not name the origin remote's acme/widget:\n%s", body)
	}
	if strings.Contains(body, "checkout-dir") || strings.Contains(body, "parent-dir") {
		t.Fatalf("gitleaks policy names the checkout path instead of the remote:\n%s", body)
	}
}

// Negative (BUG-852): without an origin remote the checkout's parent directory is not an
// owner. The policy names the checkout directory alone.
func TestApplyFlavor_Negative_CheckoutLayoutIsNotOwner(t *testing.T) {
	dir := checkoutAt(t, "acme", "widget", "")
	body := applyAndRead(t, dir, ".gitleaks.toml")[".gitleaks.toml"]
	if !strings.Contains(body, gitleaksTitle("widget")) || strings.Contains(body, "acme") {
		t.Fatalf("gitleaks policy must name the bare checkout with no layout owner:\n%s", body)
	}
}

// Boundary: a checkout under a directory named dev, which util.ResolveRepoIdentity refused
// and so failed the whole flavor, now scaffolds with the checkout name; a cancelled context
// is a failed read, never an empty identity.
func TestFlavorIdentity_Boundary_DevParentAndCancelledRead(t *testing.T) {
	dir := checkoutAt(t, "dev", "orphan", "")
	if body := applyAndRead(t, dir, ".gitleaks.toml")[".gitleaks.toml"]; !strings.Contains(body, gitleaksTitle("orphan")) {
		t.Fatalf("gitleaks policy must name the checkout:\n%s", body)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	owner, name, err := flavorIdentity(ctx, dir)
	if err == nil || errors.Is(err, util.ErrRepoIdentityUnresolved) || owner != "" || name != "" {
		t.Fatalf("flavorIdentity on a cancelled read = (%q, %q, %v), want a read error", owner, name, err)
	}
}
