package needs

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Positive: the needs section of .standards.yaml is read among the manifest's other sections.
func TestLoadExistingDeclarations_Positive_StandardsSectionAmongOthers(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, ".standards.yaml", "version: 1\nrepository:\n  owner: acme\nneeds:\n  required:\n    - http.client\n")
	repoNeeds := &RepoNeeds{}
	if err := loadExistingDeclarations(t.Context(), dir, repoNeeds); err != nil {
		t.Fatalf("section read: %v", err)
	}
	if len(repoNeeds.Capabilities.Required) != 1 || repoNeeds.Capabilities.Required[0] != "http.client" {
		t.Fatalf("declared capabilities = %v, want [http.client]", repoNeeds.Capabilities.Required)
	}
}

// Negative: a multi-document .needs.yaml or .standards.yaml is refused rather than merged from
// its first document (BUG-857), and a FIFO .needs.yaml fails within the deadline instead of
// hanging the scan (BUG-822).
func TestLoadExistingDeclarations_Negative_MultiDocumentAndFIFO(t *testing.T) {
	for name, body := range map[string]string{
		".needs.yaml":     "capabilities:\n  required: [ui.framework]\n---\ncapabilities:\n  required: [other]\n",
		".standards.yaml": "needs:\n  required: [ui.framework]\n---\nneeds:\n  required: [other]\n",
	} {
		dir := t.TempDir()
		writeFixture(t, dir, name, body)
		if err := loadExistingDeclarations(t.Context(), dir, &RepoNeeds{}); !errors.Is(err, util.ErrYAMLNotSingleDocument) {
			t.Errorf("%s: multi-document declaration = %v, want ErrYAMLNotSingleDocument", name, err)
		}
	}

	dir := t.TempDir()
	testsupport.MakeFIFO(t, filepath.Join(dir, ".needs.yaml"))
	err := testsupport.RunWithin(t, 10*time.Second, func() error {
		return loadExistingDeclarations(t.Context(), dir, &RepoNeeds{})
	})
	if !errors.Is(err, util.ErrNotRegularFile) {
		t.Errorf("FIFO .needs.yaml = %v, want ErrNotRegularFile", err)
	}
}

// Boundary: an empty .needs.yaml declares nothing and leaves the computed set as it was.
func TestLoadExistingDeclarations_Boundary_EmptyDeclaresNothing(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, ".needs.yaml", "")
	repoNeeds := &RepoNeeds{Capabilities: CapabilityDeclaration{Required: []CapabilityKey{"http.client"}}}
	if err := loadExistingDeclarations(t.Context(), dir, repoNeeds); err != nil {
		t.Fatalf("empty declaration: %v", err)
	}
	if len(repoNeeds.Capabilities.Required) != 1 {
		t.Fatalf("empty declaration changed the computed set: %v", repoNeeds.Capabilities.Required)
	}
}
