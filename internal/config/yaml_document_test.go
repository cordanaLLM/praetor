package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// multiDocManifest is a manifest whose second document would change what a first-document
// reader sees; every reader must refuse it (BUG-857).
const multiDocManifest = "version: 1\nrepository:\n  owner: first\n  name: demo\n---\nversion: 1\nrepository:\n  owner: second\n  name: demo\n"

func writeConfigFixture(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

type repositorySection struct {
	Repository struct {
		Owner string `yaml:"owner"`
	} `yaml:"repository"`
}

// Positive: a single-document file decodes; a section reader tolerates the file's other keys
// and the policy loader accepts a complete manifest.
func TestReadYAMLDocument_Positive_SingleDocument(t *testing.T) {
	dir := t.TempDir()
	path := writeConfigFixture(t, dir, ".standards.yaml", "version: 1\nrepository:\n  owner: acme\n  name: demo\n")
	var section repositorySection
	if err := ReadYAMLDocument(t.Context(), path, &section, util.YAMLDocumentOptions{}); err != nil {
		t.Fatalf("section read: %v", err)
	}
	if section.Repository.Owner != "acme" {
		t.Fatalf("section = %+v, want owner acme", section)
	}
	m, err := LoadManifest(path)
	if err != nil || m.Repository.Owner != "acme" {
		t.Fatalf("LoadManifest = %+v, %v", m, err)
	}
}

// Negative: a second document is refused by the section reader, the policy loader and the
// lock builder's manifest read; an unknown key is refused by the lock builder as by the policy
// loader; an oversize file and a nil or cancelled context are refused.
func TestReadYAMLDocument_Negative_MultiDocOversizeContext(t *testing.T) {
	dir := t.TempDir()
	path := writeConfigFixture(t, dir, ".standards.yaml", multiDocManifest)
	var section repositorySection
	if err := ReadYAMLDocument(t.Context(), path, &section, util.YAMLDocumentOptions{}); !errors.Is(err, util.ErrYAMLNotSingleDocument) {
		t.Errorf("section read of a multi-document file = %v", err)
	}
	if _, err := LoadManifest(path); !errors.Is(err, util.ErrYAMLNotSingleDocument) {
		t.Errorf("LoadManifest of a multi-document file = %v", err)
	}
	if _, err := readLockManifest(t.Context(), dir); !errors.Is(err, util.ErrYAMLNotSingleDocument) {
		t.Errorf("lock builder manifest read of a multi-document file = %v", err)
	}
	writeConfigFixture(t, dir, ".standards.yaml", "version: 1\nprofiels: [framework]\n")
	if _, err := readLockManifest(t.Context(), dir); err == nil || !strings.Contains(err.Error(), "profiels") {
		t.Errorf("lock builder accepted a key the policy loader refuses: %v", err)
	}

	big := writeConfigFixture(t, t.TempDir(), "big.yaml", "k: "+strings.Repeat("x", contextopt.MaxSourceBytes)+"\n")
	if err := ReadYAMLDocument(t.Context(), big, &section, util.YAMLDocumentOptions{}); !errors.Is(err, util.ErrFileTooLarge) {
		t.Errorf("oversize read = %v, want ErrFileTooLarge", err)
	}
	if err := ReadYAMLDocument(nil, path, &section, util.YAMLDocumentOptions{}); err == nil { //nolint:staticcheck // exercising the nil-context contract
		t.Error("nil context accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := ReadYAMLDocument(ctx, path, &section, util.YAMLDocumentOptions{}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled read = %v", err)
	}
}

// Negative: a FIFO at the manifest path is refused within the deadline by the policy loader and
// the section reader instead of blocking in open(2) (BUG-822).
func TestReadYAMLDocument_Negative_FIFORefusedWithinDeadline(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), ".standards.yaml")
	testsupport.MakeFIFO(t, fifo)
	err := testsupport.RunWithin(t, 10*time.Second, func() error {
		_, loadErr := LoadManifest(fifo)
		return loadErr
	})
	if !errors.Is(err, util.ErrNotRegularFile) {
		t.Errorf("LoadManifest of a FIFO = %v, want ErrNotRegularFile", err)
	}
	var section repositorySection
	err = testsupport.RunWithin(t, 10*time.Second, func() error {
		return ReadYAMLDocument(t.Context(), fifo, &section, util.YAMLDocumentOptions{})
	})
	if !errors.Is(err, util.ErrNotRegularFile) {
		t.Errorf("section read of a FIFO = %v, want ErrNotRegularFile", err)
	}
}

// Boundary: an empty manifest still loads as the zero manifest, a missing file reports
// os.ErrNotExist, and the runner-config section reader tolerates unrelated keys while
// refusing a second document.
func TestReadYAMLDocument_Boundary_EmptyMissingAndSections(t *testing.T) {
	dir := t.TempDir()
	empty := writeConfigFixture(t, dir, "empty.yaml", "")
	if m, err := LoadManifest(empty); err != nil || m.Version != 0 {
		t.Errorf("empty manifest = %+v, %v; want the zero manifest", m, err)
	}
	var section repositorySection
	if err := ReadYAMLDocument(t.Context(), filepath.Join(dir, "absent.yaml"), &section, util.YAMLDocumentOptions{}); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file = %v, want os.ErrNotExist", err)
	}

	repo := t.TempDir()
	writeConfigFixture(t, repo, ".standards.yaml", "version: 1\nunrelated: kept\nrunners:\n  default: custom-runner\n")
	policy, err := LoadCascadingRunnerConfigContext(t.Context(), repo, "")
	if err != nil || policy.Default != "custom-runner" {
		t.Errorf("runner section = %+v, %v; want default custom-runner", policy, err)
	}
	writeConfigFixture(t, repo, ".standards.yaml", "runners:\n  default: first\n---\nrunners:\n  default: second\n")
	if _, err := LoadCascadingRunnerConfigContext(t.Context(), repo, ""); !errors.Is(err, util.ErrYAMLNotSingleDocument) {
		t.Errorf("multi-document runner config = %v, want ErrYAMLNotSingleDocument", err)
	}
}
