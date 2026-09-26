package harvester

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

func TestInventoryRelativePathAndSymlinkMetadata(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	initTestRepository(t, repo)
	abs := inspectRepository(t.Context(), repo)
	// The relative spelling is produced by moving the process next to the repository rather
	// than by relating the repository to the package directory. Windows paths carry a drive
	// letter and no ".." sequence crosses volumes, so filepath.Rel refuses outright when the
	// runner's TEMP is on C: and the checkout on D: -- "can't make C:\...\repo relative to
	// D:\a\praetor\praetor\internal\harvester" ended this test before the symlink half ever
	// ran (#135). A base on the target's own volume is relatable on every platform. The test
	// is not parallel, so t.Chdir is safe here, and this package already uses it.
	t.Chdir(filepath.Dir(repo))
	rel := inspectRepository(t.Context(), "repo")
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
		"https://user:private-password@example.invalid:badport/org/repo",
		"user:private-password@example.invalid:org/repo",
	} {
		var failures []string
		got, _ := parseRemoteURLs("origin\t"+input+" (fetch)", &failures)
		if strings.Contains(strings.Join(got, " "), "private-") || strings.Contains(strings.Join(failures, " "), "private-") {
			t.Errorf("remote secret retained: urls=%q errors=%q", got, failures)
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

// TestInventoryRemoteForms_SharedParser pins the inventory to util.ParseGitRemoteURL, the
// one network-remote parser: every accepted remote is recorded exactly as that parser
// renders it, and every remote it rejects marks the inventory incomplete.
func TestInventoryRemoteForms_SharedParser(t *testing.T) {
	for _, tt := range []struct{ input, want string }{
		{"https://token@example.invalid/org/repo.git", "https://example.invalid/org/repo.git"},
		{"git@example.invalid:org/repo.git", "ssh://example.invalid/org/repo.git"},
		{"ssh://git@example.invalid:2222/org/repo", "ssh://example.invalid:2222/org/repo"},
		// git:// and git+ssh:// are network transports git itself speaks.
		{"git://example.invalid/org/repo", "git://example.invalid/org/repo"},
		{"git+ssh://example.invalid/org/repo", "git+ssh://example.invalid/org/repo"},
	} {
		var failures []string
		urls, valid := parseRemoteURLs("origin\t"+tt.input+" (fetch)", &failures)
		if !valid || len(urls) != 1 || urls[0] != tt.want {
			t.Errorf("remote %q: urls=%v valid=%t errors=%v, want %q", tt.input, urls, valid, failures, tt.want)
			continue
		}
		parsed, err := util.ParseGitRemoteURL(tt.input)
		if err != nil || parsed.String() != urls[0] {
			t.Errorf("remote %q: inventory recorded %q, util.ParseGitRemoteURL says %v, %v", tt.input, urls[0], parsed, err)
		}
	}
}

func TestInventoryRemoteForms_Negative_LocalAndHelperRemotes(t *testing.T) {
	for _, input := range []string{
		"/srv/git/org/repo",
		"file:///srv/git/org/repo",
		// A Windows drive path is local, not an ssh host named "C".
		"C:/repos/org/repo",
		"ext::ssh-helper/org/repo",
		"ftp://example.invalid/org/repo",
	} {
		var failures []string
		urls, valid := parseRemoteURLs("origin\t"+input+" (fetch)", &failures)
		if valid || len(urls) != 0 || len(failures) == 0 {
			t.Errorf("remote %q: urls=%v valid=%t errors=%v; want rejected", input, urls, valid, failures)
		}
	}
}

func TestInventoryRemoteForms_Boundary(t *testing.T) {
	// One accepted and one rejected remote: the good one is kept, the inventory is
	// marked incomplete, and the same remote twice is recorded once.
	var failures []string
	urls, valid := parseRemoteURLs("origin\tgit@example.invalid:org/repo (fetch)\n"+
		"origin\tgit@example.invalid:org/repo (push)\nlocal\tC:/repos/org/repo (fetch)", &failures)
	if valid || len(urls) != 1 || urls[0] != "ssh://example.invalid/org/repo" || len(failures) != 1 {
		t.Fatalf("mixed remotes: urls=%v valid=%t errors=%v", urls, valid, failures)
	}
}
