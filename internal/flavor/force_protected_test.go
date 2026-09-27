package flavor

import (
	"os"
	"path/filepath"
	"testing"
)

// forceBody is what every ContentFunc template below renders, so a refreshed file is told
// apart from the operator's by content alone.
const forceBody = "# rendered by a ContentFunc flavor\n"

// contentFuncTemplate is a template a flavor registered from outside this package could
// declare: a ContentFunc body and no Source or Producer. The built-in flavors defer the
// manifest and lock to adoption, so a ContentFunc template is the remaining path on which
// --force reaches those files, and forceProtected is what stops it there.
func contentFuncTemplate(rel string) TemplateItem {
	return TemplateItem{Path: rel, ContentFunc: func(string, string) string { return forceBody }}
}

// seedRepo writes each file into a fresh directory and returns it.
func seedRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func readRepoFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

// Positive: --force refreshes a file the flavor's ContentFunc owns, so the guard is narrow.
func TestScaffoldTemplate_Positive_ForceRefreshesContentFuncScaffold(t *testing.T) {
	root := seedRepo(t, map[string]string{"lint.yml": "operator: tuned\n"})
	outcome, _, err := scaffoldTemplate(t.Context(), root, contentFuncTemplate("lint.yml"), "widget", "", true)
	if err != nil || outcome != templateCreated {
		t.Fatalf("want the scaffold refreshed under --force, got outcome %d err %v", outcome, err)
	}
	if got := readRepoFile(t, root, "lint.yml"); got != forceBody {
		t.Fatalf("--force left the scaffold unrefreshed: %q", got)
	}
}

// Negative: --force never rewrites the manifest, the lock or the session ledger, even when a
// flavor supplies a body for them through ContentFunc (forceProtected).
func TestScaffoldTemplate_Negative_ForceNeverRewritesDeclarations(t *testing.T) {
	declared := map[string]string{
		".standards.yaml":      "version: 1\nprofiles: [framework]\n",
		".standards.lock":      "{\"version\":1,\"digest\":\"sha256:0000\"}\n",
		".workingdir/STATE.md": "# Retained history\n",
	}
	root := seedRepo(t, declared)
	for rel, want := range declared {
		outcome, _, err := scaffoldTemplate(t.Context(), root, contentFuncTemplate(rel), "widget", "", true)
		if err != nil || outcome != templateSkipped {
			t.Errorf("%s: want skipped under --force, got outcome %d err %v", rel, outcome, err)
		}
		if got := readRepoFile(t, root, rel); got != want {
			t.Errorf("--force rewrote %s: %q", rel, got)
		}
	}
}

// Boundary: the guard protects what a repository declared, not the path itself. An absent
// lock is written like any other missing template; only an existing one survives --force.
func TestScaffoldTemplate_Boundary_ForceWritesAbsentProtectedPath(t *testing.T) {
	root := t.TempDir()
	outcome, _, err := scaffoldTemplate(t.Context(), root, contentFuncTemplate(".standards.lock"), "widget", "", true)
	if err != nil || outcome != templateCreated {
		t.Fatalf("want an absent lock written, got outcome %d err %v", outcome, err)
	}
	if got := readRepoFile(t, root, ".standards.lock"); got != forceBody {
		t.Fatalf("absent lock scaffolded as %q", got)
	}
}
