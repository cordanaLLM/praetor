package docdistill

import (
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// TestDocCache_Positive_CreatedOwnerOnly pins BUG-839: the documentation cache lives in the
// private working directory, so its directories and files are created owner-only.
func TestDocCache_Positive_CreatedOwnerOnly(t *testing.T) {
	repo := t.TempDir()
	if err := SaveCatalog(repo, &DocCatalog{Packages: map[string]DistilledDoc{}}); err != nil {
		t.Fatalf("SaveCatalog: %v", err)
	}
	if err := writeDistilledDoc(repo, &DistilledDoc{PackageName: "example", Version: "v1.0.0", RawMarkdown: "# example\n"}); err != nil {
		t.Fatalf("writeDistilledDoc: %v", err)
	}
	for _, rel := range []string{".workingdir", DocsDirRel, DistilledDirRel} {
		testsupport.RequireCreatedMode(t, filepath.Join(repo, filepath.FromSlash(rel)), 0o700)
	}
	testsupport.RequireCreatedMode(t, filepath.Join(repo, filepath.FromSlash(CatalogFileRel)), 0o600)
	distilled := filepath.Join(repo, filepath.FromSlash(DistilledDirRel), sanitizeDocFilename("example", "v1.0.0"))
	testsupport.RequireCreatedMode(t, distilled, 0o600)
}
