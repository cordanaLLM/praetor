package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// anonymousRepo returns a git repository with no manifest and no origin remote, so no
// resolution step names an owner. It is a repository, not a bare directory, so git discovery
// stops there even when the temporary directory sits inside another checkout.
func anonymousRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, dir, "")
	return dir
}

// identityRepo returns anonymousRepo with a manifest declaring owner and name; an empty value
// leaves that field out.
func identityRepo(t *testing.T, owner, name string) string {
	t.Helper()
	dir := anonymousRepo(t)
	body := "version: 1\nrepository:\n"
	if owner != "" {
		body += "  owner: " + owner + "\n"
	}
	if name != "" {
		body += "  name: " + name + "\n"
	}
	writeFixtureFile(t, dir, config.ManifestFileName, body)
	return dir
}

// remoteRepo returns a repository whose origin remote names owner/name.
func remoteRepo(t *testing.T, owner, name string) string {
	t.Helper()
	dir := t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, dir, "https://github.com/"+owner+"/"+name+".git")
	return dir
}

// forgeWorkstation writes a workstation settings document with forgeYAML as its forge
// section and returns the --workstation-config flag selecting it.
func forgeWorkstation(t *testing.T, forgeYAML string) string {
	t.Helper()
	dir := t.TempDir()
	return "--workstation-config=" + writeFixtureFile(t, dir, "workstation.yaml", "forge:\n"+forgeYAML)
}

// gitOnlyPath leaves git as the only executable on PATH, so no test reaches a gh session and
// its token: project commands without credentials then stay on the local cache.
func gitOnlyPath(t *testing.T) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	bin := t.TempDir()
	if err := os.Symlink(git, filepath.Join(bin, filepath.Base(git))); err != nil {
		t.Skipf("symlinks unavailable, cannot isolate PATH: %v", err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
}

func TestResolveForgeOwner_Positive(t *testing.T) {
	cases := []struct {
		name, dir, explicit, fallback, want string
	}{
		{"flag", anonymousRepo(t), "acme", "", "acme"},
		{"flag outranks manifest", identityRepo(t, "acme", "kit"), "flagged", "other", "flagged"},
		{"manifest", identityRepo(t, "acme", "kit"), "", "other", "acme"},
		{"manifest owner without name or remote", identityRepo(t, "acme", ""), "", "other", "acme"},
		{"origin remote", remoteRepo(t, "acme-remote", "kit"), "", "other", "acme-remote"},
		{"forge.default_owner", anonymousRepo(t), "", "acme-default", "acme-default"},
	}
	for _, tc := range cases {
		got, err := resolveForgeOwner(t.Context(), tc.dir, tc.explicit, tc.fallback)
		if err != nil || got != tc.want {
			t.Errorf("%s: resolveForgeOwner = (%q, %v), want %q", tc.name, got, err, tc.want)
		}
	}
}

func TestResolveForgeOwner_Negative(t *testing.T) {
	if got, err := resolveForgeOwner(t.Context(), anonymousRepo(t), "", ""); !errors.Is(err, config.ErrOwnerUnknown) || got != "" {
		t.Errorf("no step names an owner: (%q, %v), want ErrOwnerUnknown", got, err)
	}
	for _, owner := range []string{" acme ", "\tacme", "-acme", "acme-", "ac--me", "acme/kit", "ac.me"} {
		if got, err := resolveForgeOwner(t.Context(), anonymousRepo(t), owner, ""); err == nil || errors.Is(err, config.ErrOwnerUnknown) {
			t.Errorf("--owner %q = (%q, %v), want a grammar error", owner, got, err)
		}
	}
	broken := anonymousRepo(t)
	writeFixtureFile(t, broken, config.ManifestFileName, "repository: [\n")
	if _, err := resolveForgeOwner(t.Context(), broken, "", "acme"); err == nil || errors.Is(err, config.ErrOwnerUnknown) {
		t.Errorf("an unreadable manifest fell back to the default owner: %v", err)
	}
	if _, err := resolveForgeOwner(t.Context(), filepath.Join(t.TempDir(), "missing"), "", "acme"); err == nil {
		t.Error("a missing directory resolved an owner")
	}
	if _, err := resolveForgeOwner(t.Context(), identityRepo(t, "-bad", ""), "", ""); err == nil {
		t.Error("a manifest owner GitHub rejects was accepted")
	}
}

func TestResolveForgeOwner_Boundary(t *testing.T) {
	longest := strings.Repeat("a", 39)
	if got, err := resolveForgeOwner(t.Context(), anonymousRepo(t), longest, ""); err != nil || got != longest {
		t.Errorf("39-byte --owner = (%q, %v), want accepted", got, err)
	}
	if got, err := resolveForgeOwner(t.Context(), anonymousRepo(t), "", longest); err != nil || got != longest {
		t.Errorf("39-byte forge.default_owner = (%q, %v), want accepted", got, err)
	}
	if got, err := resolveForgeOwner(t.Context(), anonymousRepo(t), longest+"a", ""); err == nil {
		t.Errorf("40-byte --owner = %q, want refused", got)
	}
	if got, err := resolveForgeOwner(t.Context(), anonymousRepo(t), "a", ""); err != nil || got != "a" {
		t.Errorf("one-byte --owner = (%q, %v), want accepted", got, err)
	}
}

func TestResolveForgeRepository_3D(t *testing.T) {
	// Positive: the manifest names both; --repo overrides the name; --owner the owner.
	for _, tc := range []struct{ owner, repo, want string }{
		{"", "", "acme/kit"}, {"", "app", "acme/app"}, {"flagged", "", "flagged/kit"},
	} {
		owner, name, err := resolveForgeRepository(t.Context(), identityRepo(t, "acme", "kit"), tc.owner, tc.repo, "")
		if err != nil || owner+"/"+name != tc.want {
			t.Errorf("owner=%q repo=%q: (%s/%s, %v), want %s", tc.owner, tc.repo, owner, name, err, tc.want)
		}
	}
	// Negative: no identity, no default: nothing is invented; a --repo GitHub rejects fails.
	if _, _, err := resolveForgeRepository(t.Context(), anonymousRepo(t), "", "", ""); !errors.Is(err, config.ErrOwnerUnknown) {
		t.Errorf("no identity = %v, want ErrOwnerUnknown", err)
	}
	if _, _, err := resolveForgeRepository(t.Context(), anonymousRepo(t), "", "kit", ""); !errors.Is(err, config.ErrOwnerUnknown) {
		t.Errorf("--repo without any owner = %v, want ErrOwnerUnknown", err)
	}
	if _, _, err := resolveForgeRepository(t.Context(), anonymousRepo(t), "acme", "..", ""); err == nil {
		t.Error("--repo=.. was accepted")
	}
	// Boundary: forge.default_owner completes a manifest naming only the repository, but a
	// default owner alone names no repository.
	if owner, name, err := resolveForgeRepository(t.Context(), identityRepo(t, "", "kit"), "", "", "acme"); err != nil || owner+"/"+name != "acme/kit" {
		t.Errorf("default owner with a manifest name = (%s/%s, %v), want acme/kit", owner, name, err)
	}
	if _, _, err := resolveForgeRepository(t.Context(), anonymousRepo(t), "", "", "acme"); !errors.Is(err, config.ErrRepositoryNameUnknown) {
		t.Errorf("default owner alone = %v, want ErrRepositoryNameUnknown", err)
	}
}

// issueListingServer answers every issue listing with an empty page and records the paths it
// was asked for.
func issueListingServer(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("dry-run reconcile sent %s %s", r.Method, r.URL.Path)
		}
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		writeInventoryResponse(t, w, []map[string]any{})
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(paths)
	}
}

func runReconcileIn(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	t.Chdir(dir)
	return captureStdout(t, func() error { return dispatchCommand("issue", append([]string{"reconcile"}, args...)) })
}

func TestIssueReconcile_Positive_ScopeChain(t *testing.T) {
	cases := []struct {
		name string
		dir  string
		args []string
		want []string
	}{
		{"bare --repos with forge.default_owner", anonymousRepo(t),
			[]string{"--repos=kit", forgeWorkstation(t, "  default_owner: acme\n")}, []string{"/repos/acme/kit/issues"}},
		{"forge.reconcile_repos", anonymousRepo(t),
			[]string{forgeWorkstation(t, "  reconcile_repos: [acme/kit, acme-labs/app]\n")},
			[]string{"/repos/acme/kit/issues", "/repos/acme-labs/app/issues"}},
		{"current repository", identityRepo(t, "acme", "kit"), nil, []string{"/repos/acme/kit/issues"}},
		{"current repository from the remote", remoteRepo(t, "acme", "app"), nil, []string{"/repos/acme/app/issues"}},
	}
	for _, tc := range cases {
		srv, paths := issueListingServer(t)
		args := append([]string{"--token=fixture", "--endpoint=" + srv.URL}, tc.args...)
		if out, err := runReconcileIn(t, tc.dir, args...); err != nil {
			t.Fatalf("%s: %v\n%s", tc.name, err, out)
		}
		if got := paths(); !slices.Equal(got, tc.want) {
			t.Errorf("%s: read %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestIssueReconcile_Negative_NoInventedOwnerOrScope(t *testing.T) {
	srv, paths := issueListingServer(t)
	dir := anonymousRepo(t)
	_, err := runReconcileIn(t, dir, "--repos=kit", "--token=fixture", "--endpoint="+srv.URL)
	if !errors.Is(err, config.ErrOwnerUnknown) || !strings.Contains(err.Error(), `"kit"`) {
		t.Errorf("bare --repos without an owner = %v, want ErrOwnerUnknown naming kit", err)
	}
	_, err = runReconcileIn(t, dir, "--token=fixture", "--endpoint="+srv.URL)
	if !errors.Is(err, config.ErrOwnerUnknown) || !strings.Contains(err.Error(), "needs at least one repository") {
		t.Errorf("no scope anywhere = %v, want the refusal wrapping ErrOwnerUnknown", err)
	}
	invalid := "--workstation-config=" + writeFixtureFile(t, t.TempDir(), "w.yaml", "forge: {reconcile_repos: [not-a-coordinate]}\n")
	if _, err := runReconcileIn(t, dir, "--token=fixture", "--endpoint="+srv.URL, invalid); err == nil {
		t.Error("an invalid forge.reconcile_repos was accepted")
	}
	if got := paths(); len(got) != 0 {
		t.Errorf("refused selections reached the forge: %v", got)
	}
}

func TestIssueReconcile_Boundary_ExplicitReposWinAndOwnerLine(t *testing.T) {
	srv, paths := issueListingServer(t)
	settings := forgeWorkstation(t, "  default_owner: acme\n  reconcile_repos: [acme/kit]\n")
	out, err := runReconcileIn(t, anonymousRepo(t), "--repos=acme-labs/one", "--token=fixture", "--endpoint="+srv.URL, settings)
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(); !slices.Equal(got, []string{"/repos/acme-labs/one/issues"}) {
		t.Errorf("explicit --repos read %v, want only acme-labs/one", got)
	}
	mustContain(t, out, "Reconciliation: 1 repositories", "Owner of bare repository names: acme")
	// Qualified names need no owner, and none is printed when nothing names one.
	out, err = runReconcileIn(t, anonymousRepo(t), "--repos=acme/kit", "--token=fixture", "--endpoint="+srv.URL)
	if err != nil || strings.Contains(out, "Owner of bare repository names") {
		t.Errorf("qualified --repos without an owner = %v\n%s", err, out)
	}
}

func TestProjectList_3D_OwnerResolution(t *testing.T) {
	gitOnlyPath(t)
	// Negative: no owner anywhere is an error, not the boards of a built-in organisation.
	_, err := captureStdout(t, func() error { return dispatchCommand("project", []string{"list", "--dir=" + anonymousRepo(t)}) })
	if !errors.Is(err, config.ErrOwnerUnknown) {
		t.Errorf("project list without an owner = %v, want ErrOwnerUnknown", err)
	}
	_, err = captureStdout(t, func() error {
		return dispatchCommand("project", []string{"add", "1", "https://github.com/acme/kit/issues/1", "--dir=" + anonymousRepo(t)})
	})
	if !errors.Is(err, config.ErrOwnerUnknown) {
		t.Errorf("project add without an owner = %v, want ErrOwnerUnknown", err)
	}
	// Positive: manifest, then forge.default_owner.
	for want, args := range map[string][]string{
		"acme-org": {"list", "--dir=" + identityRepo(t, "acme-org", "kit")},
		"acme":     {"list", "--dir=" + anonymousRepo(t), forgeWorkstation(t, "  default_owner: acme\n")},
	} {
		out, err := captureStdout(t, func() error { return dispatchCommand("project", args) })
		if err != nil || !strings.Contains(out, "Projects (v2) for "+want+" ") {
			t.Errorf("project %v = %v, want boards of %s:\n%s", args, err, want, out)
		}
	}
	// Boundary: a whitespace --owner is refused instead of trimmed into someone's login.
	if _, err := captureStdout(t, func() error {
		return dispatchCommand("project", []string{"list", "--owner= ", "--dir=" + anonymousRepo(t)})
	}); err == nil || errors.Is(err, config.ErrOwnerUnknown) {
		t.Errorf("--owner=' ' = %v, want a grammar error", err)
	}
}

func TestMilestoneSync_3D_RepositoryResolution(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		writeInventoryResponse(t, w, []map[string]any{})
	}))
	t.Cleanup(srv.Close)
	syncRun := func(dir string, extra ...string) error {
		args := append([]string{"sync", "--dir=" + dir, "--token=fixture", "--endpoint=" + srv.URL}, extra...)
		_, err := captureStdout(t, func() error { return dispatchCommand("milestone", args) })
		return err
	}
	// Negative: no identity: nothing is published to a guessed repository.
	if err := syncRun(anonymousRepo(t)); !errors.Is(err, config.ErrOwnerUnknown) {
		t.Errorf("milestone sync without an identity = %v, want ErrOwnerUnknown", err)
	}
	if err := syncRun(anonymousRepo(t), forgeWorkstation(t, "  default_owner: acme\n")); !errors.Is(err, config.ErrRepositoryNameUnknown) {
		t.Errorf("milestone sync with only a default owner = %v, want ErrRepositoryNameUnknown", err)
	}
	mu.Lock()
	if len(paths) != 0 {
		t.Errorf("refused syncs reached the forge: %v", paths)
	}
	mu.Unlock()
	// Positive: the manifest names the repository; Boundary: explicit flags override it.
	if err := syncRun(identityRepo(t, "acme", "kit")); err != nil {
		t.Fatalf("milestone sync from the manifest: %v", err)
	}
	if err := syncRun(identityRepo(t, "acme", "kit"), "--owner=acme-labs", "--repo=app"); err != nil {
		t.Fatalf("milestone sync with flags: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 2 || !strings.HasPrefix(paths[0], "/repos/acme/kit/") || !strings.HasPrefix(paths[1], "/repos/acme-labs/app/") {
		t.Errorf("milestone sync read %v, want acme/kit then acme-labs/app", paths)
	}
}

func TestInit_3D_RepositoryIdentity(t *testing.T) {
	cases := []struct {
		name, dir, settings string
		owner, repo, warn   string
	}{
		{"origin remote", remoteRepo(t, "acme", "kit"), "", "acme", "kit", ""},
		{"forge.default_owner", anonymousRepo(t), forgeWorkstation(t, "  default_owner: acme\n"), "acme", "", "repository.name not detected"},
		{"nothing", anonymousRepo(t), "", "", "", "repository.owner not detected; set it in .standards.yaml"},
	}
	for _, tc := range cases {
		args := []string{"--output=" + filepath.Join(tc.dir, config.ManifestFileName)}
		if tc.settings != "" {
			args = append(args, tc.settings)
		}
		out, err := runInitCmd(t, args...)
		if err != nil {
			t.Fatalf("%s: init: %v\n%s", tc.name, err, out)
		}
		manifest, err := config.LoadManifest(filepath.Join(tc.dir, config.ManifestFileName))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if manifest.Repository.Owner != tc.owner || manifest.Repository.Name != tc.repo {
			t.Errorf("%s: wrote %s/%s, want %s/%s", tc.name, manifest.Repository.Owner, manifest.Repository.Name, tc.owner, tc.repo)
		}
		if tc.warn != "" && !strings.Contains(out, tc.warn) {
			t.Errorf("%s: output lacks %q:\n%s", tc.name, tc.warn, out)
		}
		if tc.warn == "" && strings.Contains(out, "not detected") {
			t.Errorf("%s: a resolved identity printed a warning:\n%s", tc.name, out)
		}
	}
}

func TestForgeSyncWiki_3D_PortalOwner(t *testing.T) {
	agents, err := os.ReadFile(filepath.Join("..", "..", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	run := func(dir string, extra ...string) (string, error) {
		t.Helper()
		writeFixtureFile(t, dir, "AGENTS.md", string(agents))
		t.Chdir(dir)
		return captureStdout(t, func() error {
			return dispatchCommand("forge", append([]string{"sync-wiki", "--output=wiki"}, extra...))
		})
	}
	// Positive: the manifest names the portal.
	dir := identityRepo(t, "acme", "kit")
	if out, err := run(dir); err != nil {
		t.Fatalf("sync-wiki: %v\n%s", err, out)
	}
	mustContain(t, readFixtureFile(t, dir, "wiki/Home.md"), "acme/kit Wiki Portal")
	// Boundary: forge.default_owner completes a manifest that names only the repository.
	named := identityRepo(t, "", "kit")
	if out, err := run(named, forgeWorkstation(t, "  default_owner: acme-labs\n")); err != nil {
		t.Fatalf("sync-wiki with a default owner: %v\n%s", err, out)
	}
	mustContain(t, readFixtureFile(t, named, "wiki/Home.md"), "acme-labs/kit Wiki Portal")
	// Negative: no identity writes no portal named after the directory layout.
	anonymous := anonymousRepo(t)
	if _, err := run(anonymous); !errors.Is(err, config.ErrOwnerUnknown) {
		t.Errorf("sync-wiki without an identity = %v, want ErrOwnerUnknown", err)
	}
	if _, err := os.Stat(filepath.Join(anonymous, "wiki")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("sync-wiki without an identity wrote pages: %v", err)
	}
}
