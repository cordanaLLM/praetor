package harvester

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInventoryIgnoresSubmoduleFiltersAndDeclaresDirtyScope(t *testing.T) {
	fixture := newSubmoduleFilterFixture(t)

	first, err := ScanLocalWorkstation(t.Context(), fixture.devRoot)
	if err != nil {
		t.Fatal(err)
	}
	observation := requireSingleRepositoryObservation(t, first)
	if _, err := os.Stat(fixture.marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("submodule clean filter executed during inventory: %v", err)
	}
	if !first.RepositoryInventoryComplete || observation.DirtyState != "known" || observation.DirtyScope != "checkout-excluding-submodules" || observation.DirtyEntries != 0 {
		t.Fatalf("submodule-only changes were not scoped truthfully: complete=%v observation=%+v", first.RepositoryInventoryComplete, observation)
	}

	if err := os.WriteFile(filepath.Join(fixture.superproject, "README"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := ScanLocalWorkstation(t.Context(), fixture.devRoot)
	if err != nil {
		t.Fatal(err)
	}
	observation = requireSingleRepositoryObservation(t, second)
	if _, err := os.Stat(fixture.marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("submodule clean filter executed during dirty-root inventory: %v", err)
	}
	if observation.DirtyState != "known" || observation.DirtyScope != "checkout-excluding-submodules" || observation.DirtyEntries != 1 {
		t.Fatalf("root dirty entry was not counted in declared scope: %+v", observation)
	}
}

type submoduleFilterFixture struct {
	devRoot      string
	superproject string
	marker       string
}

func newSubmoduleFilterFixture(t *testing.T) submoduleFilterFixture {
	t.Helper()
	fixture := t.TempDir()
	devRoot := filepath.Join(fixture, "dev")
	superproject := filepath.Join(devRoot, "superproject")
	submoduleSource := filepath.Join(fixture, "submodule-source")
	marker := filepath.Join(fixture, "submodule-filter-ran")
	if err := os.MkdirAll(devRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	initTestRepository(t, submoduleSource)
	if err := os.WriteFile(filepath.Join(submoduleSource, ".gitattributes"), []byte("README filter=inventory-probe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runTestGit(submoduleSource, "add", ".gitattributes"); err != nil {
		t.Fatalf("add submodule attributes: %v (%s)", err, out)
	}
	if out, err := runTestGit(submoduleSource, "commit", "-m", "configure filter"); err != nil {
		t.Fatalf("commit submodule attributes: %v (%s)", err, out)
	}

	initTestRepository(t, superproject)
	if out, err := runTestGit(superproject, "-c", "protocol.file.allow=always", "submodule", "add", "-q", submoduleSource, "sub"); err != nil {
		t.Fatalf("add submodule: %v (%s)", err, out)
	}
	if out, err := runTestGit(superproject, "commit", "-m", "add submodule"); err != nil {
		t.Fatalf("commit submodule: %v (%s)", err, out)
	}

	submodule := filepath.Join(superproject, "sub")
	gitDirOutput, err := runTestGit(submodule, "rev-parse", "--git-dir")
	if err != nil {
		t.Fatalf("locate submodule git directory: %v (%s)", err, gitDirOutput)
	}
	submoduleGitDir := strings.TrimSpace(gitDirOutput)
	if !filepath.IsAbs(submoduleGitDir) {
		submoduleGitDir = filepath.Join(submodule, submoduleGitDir)
	}
	submoduleGitDir = filepath.Clean(submoduleGitDir)
	filter := filepath.Join(submoduleGitDir, "inventory-probe")
	quotedMarker := "'" + strings.ReplaceAll(marker, "'", "'\\''") + "'"
	if err := os.WriteFile(filter, []byte("#!/bin/sh\n: > "+quotedMarker+"\ncat\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := runTestGit(submodule, "config", "filter.inventory-probe.clean", filter); err != nil {
		t.Fatalf("configure submodule filter: %v (%s)", err, out)
	}
	if out, err := runTestGit(submodule, "config", "filter.inventory-probe.required", "true"); err != nil {
		t.Fatalf("require submodule filter: %v (%s)", err, out)
	}
	if err := os.WriteFile(filepath.Join(submodule, "README"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return submoduleFilterFixture{devRoot: devRoot, superproject: superproject, marker: marker}
}

func requireSingleRepositoryObservation(t *testing.T, report *WorkstationReport) RepositoryObservation {
	t.Helper()
	if len(report.RepositoryObservations) != 1 {
		t.Fatalf("expected one repository observation, got %d: %+v", len(report.RepositoryObservations), report.RepositoryObservations)
	}
	return report.RepositoryObservations[0]
}
