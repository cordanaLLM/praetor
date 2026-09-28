package needs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// symlinkOrSkip plants a link at link pointing to target, skipping where links are unavailable.
func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}
}

// assertFileBody fails unless path still holds body.
func assertFileBody(t *testing.T, path, body string) {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- test-local path from t.TempDir
	if err != nil || string(data) != body {
		t.Fatalf("%s = %q, %v; want %q untouched", path, data, err, body)
	}
}

// Positive: the needs manifest, the demand files and the migration guide are written below
// their root through the confined writer.
func TestNeedsConfinedWrites_Positive(t *testing.T) {
	repo := t.TempDir()
	if err := WriteNeedsManifest(repo, &RepoNeeds{Repository: "acme/widget"}); err != nil {
		t.Fatalf("WriteNeedsManifest: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(repo, NeedsManifestName)); err != nil || !strings.Contains(string(data), "acme/widget") {
		t.Fatalf("manifest = %q, %v", data, err)
	}
	if _, err := applyPlannedRewrites(context.Background(), repo, &MigrationPlan{GuideMarkdown: "# guide\n"}, nil, true); err != nil {
		t.Fatalf("guide write: %v", err)
	}
	assertFileBody(t, filepath.Join(repo, migrationGuideName), "# guide\n")
}

// Negative: a link planted at a written file is refused instead of written through: one that
// leaves the root (the needs manifest, the demand manifest) as an escape, one that stays
// inside it (the migration guide, a demand request file) as a link destination. Every link
// target keeps its content (BUG-826).
func TestNeedsConfinedWrites_Negative_LinkedDestinations(t *testing.T) {
	outside := writeFixture(t, t.TempDir(), "victim.txt", "outside\n")

	repo := t.TempDir()
	symlinkOrSkip(t, outside, filepath.Join(repo, NeedsManifestName))
	if err := WriteNeedsManifest(repo, &RepoNeeds{Repository: "acme/widget"}); !errors.Is(err, util.ErrPathEscapesRoot) {
		t.Errorf("escaping .needs.yaml = %v, want ErrPathEscapesRoot", err)
	}

	guideRepo := t.TempDir()
	inRoot := writeFixture(t, guideRepo, "docs/guide.md", "kept\n")
	symlinkOrSkip(t, filepath.Join("docs", "guide.md"), filepath.Join(guideRepo, migrationGuideName))
	if _, err := applyPlannedRewrites(context.Background(), guideRepo, &MigrationPlan{GuideMarkdown: "# guide\n"}, nil, true); !errors.Is(err, util.ErrSymlinkDestination) {
		t.Errorf("linked MIGRATION.md = %v, want ErrSymlinkDestination", err)
	}

	demands := t.TempDir()
	symlinkOrSkip(t, outside, filepath.Join(demands, demandManifestName))
	if err := EmitDemandRequests(context.Background(), demandFixture(), demands); !errors.Is(err, util.ErrPathEscapesRoot) {
		t.Errorf("escaping demand manifest = %v, want ErrPathEscapesRoot", err)
	}
	requestDir := t.TempDir()
	sibling := writeFixture(t, requestDir, "sibling.md", "kept\n")
	symlinkOrSkip(t, "sibling.md", filepath.Join(requestDir, demandFixture()[0].RequestID+".md"))
	if err := EmitDemandRequests(context.Background(), demandFixture(), requestDir); !errors.Is(err, util.ErrSymlinkDestination) {
		t.Errorf("linked request file = %v, want ErrSymlinkDestination", err)
	}

	assertFileBody(t, outside, "outside\n")
	assertFileBody(t, inRoot, "kept\n")
	assertFileBody(t, sibling, "kept\n")
}

// Boundary: an ancestor that is a relative link staying inside the repository is still
// followed, so the confinement edge is the root itself, not every link below it.
func TestNeedsConfinedWrites_Boundary_InRootLinkedAncestor(t *testing.T) {
	repo := t.TempDir()
	real := writeFixture(t, repo, "internal/pkg/main.go", "package pkg\n\nimport \"github.com/a/b\"\n")
	symlinkOrSkip(t, filepath.Join("internal", "pkg"), filepath.Join(repo, "pkg"))
	if err := applyFileImportReplacement(repo, filepath.Join(repo, "pkg", "main.go"), "github.com/a/b", "example.com/acme/kit/b"); err != nil {
		t.Fatalf("rewrite through an in-root linked ancestor: %v", err)
	}
	assertFileBody(t, real, "package pkg\n\nimport \"example.com/acme/kit/b\"\n")
}

// Negative: an epic output path that is a link is refused instead of written through, so the
// operator's link target keeps its content (BUG-826).
func TestWriteEpicMarkdown_Negative_LinkedOutputRefused(t *testing.T) {
	dir := t.TempDir()
	target := writeFixture(t, dir, "notes.md", "kept\n")
	symlinkOrSkip(t, "notes.md", filepath.Join(dir, "epic.md"))
	if err := WriteEpicMarkdown(context.Background(), &PreMigrationEpic{}, filepath.Join(dir, "epic.md")); !errors.Is(err, util.ErrSymlinkDestination) {
		t.Fatalf("linked epic output = %v, want ErrSymlinkDestination", err)
	}
	assertFileBody(t, target, "kept\n")
}
