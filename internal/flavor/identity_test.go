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

// applyAndRead applies go-service to dir and returns the named scaffold bodies.
func applyAndRead(t *testing.T, dir string, rels ...string) map[string]string {
	t.Helper()
	report, err := ApplyFlavor(t.Context(), dir, "go-service", false)
	if err != nil {
		t.Fatalf("apply go-service: %v", err)
	}
	if len(report.Errors) > 0 {
		t.Fatalf("apply go-service recorded errors: %s", strings.Join(report.Errors, "; "))
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
	bodies := applyAndRead(t, dir, ".github/workflows/ci.yml", "Dockerfile")
	if got := bodies[".github/workflows/ci.yml"]; got != "# ci.yml configuration for acme/widget\n" {
		t.Fatalf("stub = %q, want the origin remote's acme/widget", got)
	}
	if !strings.Contains(bodies["Dockerfile"], "COPY widget /") {
		t.Fatalf("Dockerfile does not name the remote's repository: %q", bodies["Dockerfile"])
	}
}

// Negative (BUG-852): without an origin remote the checkout's parent directory is not an
// owner. The stub names the checkout directory alone.
func TestApplyFlavor_Negative_CheckoutLayoutIsNotOwner(t *testing.T) {
	dir := checkoutAt(t, "acme", "widget", "")
	bodies := applyAndRead(t, dir, ".github/workflows/ci.yml")
	if got := bodies[".github/workflows/ci.yml"]; got != "# ci.yml configuration for widget\n" {
		t.Fatalf("stub = %q, want the bare checkout name with no layout owner", got)
	}
}

// Boundary: a checkout under a directory named dev, which util.ResolveRepoIdentity refused
// and so failed the whole flavor, now scaffolds with the checkout name; a cancelled context
// is a failed read, never an empty identity.
func TestFlavorIdentity_Boundary_DevParentAndCancelledRead(t *testing.T) {
	dir := checkoutAt(t, "dev", "orphan", "")
	if got := applyAndRead(t, dir, ".github/workflows/ci.yml")[".github/workflows/ci.yml"]; got != "# ci.yml configuration for orphan\n" {
		t.Fatalf("stub = %q, want the checkout name", got)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	owner, name, err := flavorIdentity(ctx, dir)
	if err == nil || errors.Is(err, util.ErrRepoIdentityUnresolved) || owner != "" || name != "" {
		t.Fatalf("flavorIdentity on a cancelled read = (%q, %q, %v), want a read error", owner, name, err)
	}
}
