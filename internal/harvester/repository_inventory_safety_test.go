package harvester

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInventoryRelativePathAndSymlinkMetadata(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	initTestRepository(t, repo)
	abs := inspectRepository(t.Context(), repo)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, repo)
	if err != nil {
		t.Fatal(err)
	}
	rel := inspectRepository(t.Context(), relative)
	if rel.GitCommonDir != abs.GitCommonDir || rel.Classification != abs.Classification {
		t.Fatalf("relative path changed local identity: %+v %+v", abs, rel)
	}
	root := t.TempDir()
	linked := filepath.Join(root, "linked")
	if err := os.Mkdir(linked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(repo, ".git"), filepath.Join(linked, ".git")); err != nil {
		t.Fatal(err)
	}
	report, err := ScanLocalWorkstation(t.Context(), root)
	if err != nil || report.RepositoryInventoryComplete || len(report.RepositoryObservations) != 1 {
		t.Fatalf("symlinked metadata must remain an incomplete observation: %+v %v", report, err)
	}
	got := report.RepositoryObservations[0]
	if got.GitCommonDir != "" || got.RemoteState != "unknown" {
		t.Fatalf("external metadata was observed: %+v", got)
	}
}

func TestInventoryNilContext(t *testing.T) {
	var ctx context.Context
	if _, err := ScanLocalWorkstation(ctx, t.TempDir()); err == nil {
		t.Fatal("missing context must be rejected")
	}
}

func TestInventoryDoesNotExecuteRepositoryCommands(t *testing.T) {
	for _, kind := range []string{"fsmonitor", "filter"} {
		t.Run(kind, func(t *testing.T) {
			repo := filepath.Join(t.TempDir(), "repo")
			initTestRepository(t, repo)
			script := filepath.Join(repo, ".git", "inventory-probe")
			if err := os.WriteFile(script, []byte("#!/bin/sh\n: > .git/inventory-probe-ran\ncat\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			key := "core.fsmonitor"
			if kind == "filter" {
				key = "filter.inventory-probe.clean"
				if err := os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte("README filter=inventory-probe\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if out, err := runTestGit(repo, "config", key, script); err != nil {
				t.Fatalf("configure fixture: %v %s", err, out)
			}
			if err := os.WriteFile(filepath.Join(repo, "README"), []byte("changed\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			inspectRepository(t.Context(), repo)
			if _, err := os.Stat(filepath.Join(repo, ".git", "inventory-probe-ran")); !os.IsNotExist(err) {
				t.Fatalf("repository %s command executed during metadata inventory", kind)
			}
		})
	}
}

func TestInventoryRemotePrivacyAndMalformedState(t *testing.T) {
	for _, input := range []string{
		"git@example.invalid:org/repo#private-fragment",
		"git@example.invalid:org/repo?private-query#private-fragment",
		"ssh://user:private-password@example.invalid/org/repo?private-query#private-fragment",
	} {
		got, err := sanitizeRemote(input)
		if err == nil && strings.Contains(got, "private-") {
			t.Errorf("remote secret retained: %q", got)
		}
	}
	var failures []string
	_, valid := parseRemoteURLs("origin", &failures)
	if valid || len(failures) == 0 {
		t.Fatalf("malformed remote must mark inventory incomplete: valid=%t errors=%v", valid, failures)
	}
}

func TestInventoryPartialCloneRemoteAnnotation(t *testing.T) {
	var failures []string
	urls, valid := parseRemoteURLs("origin\thttps://example.invalid/org/repo.git (fetch) [blob:none]\norigin\thttps://example.invalid/org/repo.git (push)", &failures)
	if !valid || len(failures) != 0 || len(urls) != 1 || urls[0] != "https://example.invalid/org/repo.git" {
		t.Fatalf("partial clone annotation changed remote identity: urls=%v valid=%t failures=%v", urls, valid, failures)
	}
}
