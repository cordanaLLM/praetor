package supplychain

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// fixtureGitTimeout bounds one fixture git command (HISS-02).
const fixtureGitTimeout = 30 * time.Second

// requireGit skips a test that needs a real checkout on a host without Git (HISS-21).
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
}

// fixtureGit runs git in dir under a hermetic environment and fails the test on error.
func fixtureGit(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), fixtureGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// writeGoMod writes a go.mod into dir, creating dir first.
func writeGoMod(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// committedCheckout creates a Git checkout holding one commit of everything in root and
// tags that commit with each of tags.
func committedCheckout(t *testing.T, root string, tags ...string) {
	t.Helper()
	env := testsupport.HermeticGitEnv(t)
	fixtureGit(t, root, env, "init", "-q")
	fixtureGit(t, root, env, "add", "-A")
	fixtureGit(t, root, env, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "fixture")
	for _, tag := range tags {
		fixtureGit(t, root, env, "tag", tag)
	}
}

func componentsFor(t *testing.T, goMod string) []Component {
	t.Helper()
	dir := t.TempDir()
	writeGoMod(t, dir, goMod)
	bom, err := GenerateCycloneDX(t.Context(), dir, SBOMOptions{ModuleVersion: "v0.0.1"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	return bom.Components
}

func TestApplyReplacements_Positive_UnversionedAndMatchingVersion(t *testing.T) {
	components := componentsFor(t, "module example.com/app\n\ngo 1.27\n\nrequire (\n"+
		"\told.example.com/a v1.0.0\n\told.example.com/b v1.4.0\n)\n\nreplace (\n"+
		"\told.example.com/a => new.example.com/a v2.0.0\n"+
		"\told.example.com/b v1.4.0 => new.example.com/b v1.4.1\n)\n")
	want := []Component{
		{Type: "library", Name: "new.example.com/a", Version: "v2.0.0", PURL: "pkg:golang/new.example.com/a@v2.0.0"},
		{Type: "library", Name: "new.example.com/b", Version: "v1.4.1", PURL: "pkg:golang/new.example.com/b@v1.4.1"},
	}
	if len(components) != len(want) || components[0] != want[0] || components[1] != want[1] {
		t.Fatalf("got %+v, want %+v", components, want)
	}
}

func TestApplyReplacements_Negative_OtherVersionIsNotReplaced(t *testing.T) {
	components := componentsFor(t, "module example.com/app\n\ngo 1.27\n\n"+
		"require old.example.com/a v1.1.0\n\nreplace old.example.com/a v1.0.0 => new.example.com/a v2.0.0\n")
	want := Component{Type: "library", Name: "old.example.com/a", Version: "v1.1.0", PURL: "pkg:golang/old.example.com/a@v1.1.0"}
	if len(components) != 1 || components[0] != want {
		t.Fatalf("a replace scoped to v1.0.0 rewrote the v1.1.0 requirement: %+v", components)
	}
}

func TestApplyReplacements_Boundary_ExactVersionWinsAndLocalPathClearsPURL(t *testing.T) {
	components := componentsFor(t, "module example.com/app\n\ngo 1.27\n\nrequire (\n"+
		"\told.example.com/a v1.0.0\n\told.example.com/local v0.1.0\n)\n\nreplace (\n"+
		"\told.example.com/a => wildcard.example.com/a v9.0.0\n"+
		"\told.example.com/a v1.0.0 => exact.example.com/a v1.0.1\n"+
		"\told.example.com/local => ../local\n)\n")
	if len(components) != 2 {
		t.Fatalf("expected 2 components, got %+v", components)
	}
	if components[0].Name != "exact.example.com/a" || components[0].Version != "v1.0.1" {
		t.Errorf("the version-specific replace must win over the module-wide one: %+v", components[0])
	}
	if components[1] != (Component{Type: "library", Name: "../local"}) {
		t.Errorf("a local replacement must carry no version or PURL: %+v", components[1])
	}
}

func TestGenerateCycloneDX_Positive_ModuleVersionFromOptionOrReleaseTag(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir, "module example.com/app\n\ngo 1.27\n")
	bom, err := GenerateCycloneDX(t.Context(), dir, SBOMOptions{ModuleVersion: " v3.1.4 "})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if got := bom.Metadata.Component.Version; got != "v3.1.4" {
		t.Errorf("explicit module version: got %q", got)
	}

	requireGit(t)
	committedCheckout(t, dir, "v1.2.3")
	tagged, err := GenerateCycloneDX(t.Context(), dir, SBOMOptions{})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if got := tagged.Metadata.Component.Version; got != "v1.2.3" {
		t.Errorf("release tag on HEAD: got %q, want v1.2.3", got)
	}
}

func TestGenerateCycloneDX_Negative_UnknownVersionIsOmitted(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	writeGoMod(t, root, "module example.com/app\n\ngo 1.27\n")
	committedCheckout(t, root, "nightly", "v1-beta")

	bom, err := GenerateCycloneDX(t.Context(), root, SBOMOptions{})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if got := bom.Metadata.Component.Version; got != "" {
		t.Fatalf("a HEAD without a SemVer release tag must leave the version unset, got %q", got)
	}
	data, err := json.Marshal(bom.Metadata.Component)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"version"`) {
		t.Errorf("an unknown version must be omitted from the BOM: %s", data)
	}

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := GenerateCycloneDX(cancelled, root, SBOMOptions{}); err == nil {
		t.Error("a cancelled context must fail the generation")
	}
}

func TestGenerateCycloneDX_Boundary_NestedModuleUsesItsOwnTagPrefix(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	writeGoMod(t, root, "module example.com/root\n\ngo 1.27\n")
	nested := filepath.Join(root, "tools", "gen")
	writeGoMod(t, nested, "module example.com/root/tools/gen\n\ngo 1.27\n")
	committedCheckout(t, root, "v9.9.9", "tools/gen/v0.3.0")

	bom, err := GenerateCycloneDX(t.Context(), nested, SBOMOptions{})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if got := bom.Metadata.Component.Version; got != "v0.3.0" {
		t.Errorf("nested module: got %q, want v0.3.0 from its own tag", got)
	}

	plain := t.TempDir()
	writeGoMod(t, plain, "module example.com/plain\n\ngo 1.27\n")
	outside, err := releaseTag(t.Context(), plain)
	if err != nil || outside != "" {
		t.Errorf("a directory outside any checkout must yield no tag: %q, %v", outside, err)
	}
}
