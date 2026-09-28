package funding

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Positive: a first rendering creates .github/FUNDING.yml below the root.
func TestApplyConfinedWrite_Positive_CreatesGitHubDirectory(t *testing.T) {
	root := fixtureRoot(t)
	if _, err := Apply(t.Context(), root, mustParse(t, fullConfig), true); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.Contains(readFixture(t, root, fundingFile), "github: [exampleOrg]") {
		t.Fatal("FUNDING.yml was not rendered")
	}
}

// Negative: a surface planted as a link inside the repository is refused instead of written
// through, and the link target keeps its content (BUG-826).
func TestApplyConfinedWrite_Negative_LinkedSurface(t *testing.T) {
	root := fixtureRoot(t)
	writeFixture(t, root, "notes/readme-source.md", fixtureReadme)
	if err := os.Remove(filepath.Join(root, "README.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("notes", "readme-source.md"), filepath.Join(root, "README.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Apply(t.Context(), root, mustParse(t, fullConfig), true); !errors.Is(err, util.ErrSymlinkDestination) {
		t.Fatalf("linked README.md = %v, want ErrSymlinkDestination", err)
	}
	if got := readFixture(t, root, "notes/readme-source.md"); got != fixtureReadme {
		t.Fatalf("link target changed:\n%s", got)
	}
}

// Boundary: a docs directory that is a relative link staying inside the repository is
// followed; the confinement edge is the repository root.
func TestApplyConfinedWrite_Boundary_InRootLinkedDirectory(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "README.md", fixtureReadme)
	writeFixture(t, root, "mkdocs.yml", fixtureMkdocs)
	writeFixture(t, root, "site/docs/sponsoring.md", fixtureSponsoring)
	writeFixture(t, root, "site/docs/monetization.md", fixtureMonetization)
	if err := os.Symlink(filepath.Join("site", "docs"), filepath.Join(root, "docs")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Apply(t.Context(), root, mustParse(t, fullConfig), true); err != nil {
		t.Fatalf("in-repository linked docs = %v, want the write to follow it", err)
	}
	if !strings.Contains(readFixture(t, root, "site/docs/monetization.md"), "ko-fi.com/example") {
		t.Fatal("monetization page was not rendered through the in-repository link")
	}
}
