package forge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// A forced adoption rebuilds .standards.lock from the lock source, and the DevContainer bootstrap
// inventory of that rebuild runs git ls-files there (internal/devcontainer/bootstrap_source.go).
// Loaded remotely, praetor-adopt is a tree the runner unpacked from an archive, with no .git, so on
// a forced adopt run the build step checks out the action's own repository at the action's own ref
// with git and builds from that checkout, which the run step then passes as --lock-source-root.
// These tests execute the shipped build step with a stub go and the real git against a repository
// served over file://, standing in for GITHUB_SERVER_URL.

const (
	// adoptRemoteRepository is github.action_repository for the served repository.
	adoptRemoteRepository = "acme/praetor"
	// adoptRemoteMarker is a file whose text tells the served commits apart.
	adoptRemoteMarker = "release.txt"
	// adoptRemoteTag names the older of the two served commits.
	adoptRemoteTag = "v1.2.3"
	// adoptSourcePrefix is the name the build step gives its checkout under the runner's
	// temporary directory.
	adoptSourcePrefix = "praetor-source."
)

// adoptRemote is a praetor repository served the way GitHub serves the action's own: the build step
// fetches GITHUB_SERVER_URL/<github.action_repository>. tagged is the commit adoptRemoteTag names
// and tip the newer commit on main; adoptRemoteMarker holds "tagged" or "tip" in their trees.
type adoptRemote struct {
	serverURL, tagged, tip string
}

// writeAdoptFixtureFile writes content at path, creating the directories above it.
func writeAdoptFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// serveAdoptRemote commits a repository declaring module, with the cmd/standardsctl the build
// step requires, at adoptRemoteRepository below a fresh server directory.
func serveAdoptRemote(t *testing.T, module string) adoptRemote {
	t.Helper()
	server := t.TempDir()
	repo := filepath.Join(server, filepath.FromSlash(adoptRemoteRepository))
	files := map[string]string{
		"go.mod":                   "module " + module + "\n\ngo 1.27\n",
		"cmd/standardsctl/main.go": "package main\n\nfunc main() {}\n",
		adoptRemoteMarker:          "tagged\n",
	}
	for rel, content := range files {
		writeAdoptFixtureFile(t, filepath.Join(repo, filepath.FromSlash(rel)), content)
	}
	testsupport.InitGitRepoWithOrigin(t, repo, "")
	tagged := testsupport.RunFixtureGit(t, repo, []string{"symbolic-ref", "HEAD", "refs/heads/main"},
		[]string{"add", "-A"}, []string{"commit", "--quiet", "-m", "tagged"},
		[]string{"tag", adoptRemoteTag}, []string{"rev-parse", "HEAD"})
	writeAdoptFixtureFile(t, filepath.Join(repo, adoptRemoteMarker), "tip\n")
	tip := testsupport.RunFixtureGit(t, repo, []string{"commit", "--quiet", "-am", "tip"}, []string{"rev-parse", "HEAD"})
	return adoptRemote{serverURL: "file://" + filepath.ToSlash(server), tagged: tagged, tip: tip}
}

// runForcedBuild executes the build step for a forced adopt run of the action at actionPath,
// loaded from source, with serverURL as GITHUB_SERVER_URL and git reading no configuration outside
// the fixture. runner overrides any of it, PRAETOR_FORCE and PRAETOR_MODE included.
func runForcedBuild(t *testing.T, actionPath, serverURL string, source actionSource, runner ...string) stepOutcome {
	t.Helper()
	env := append([]string{
		"PRAETOR_FORCE=true", "GITHUB_SERVER_URL=" + serverURL,
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_TERMINAL_PROMPT=0",
	}, runner...)
	return runAdoptBuildStep(t, actionPath, source, env...)
}

// assertBuiltFrom checks that the build step succeeded, ran one go build in root and published root
// as the lock source.
func assertBuiltFrom(t *testing.T, got stepOutcome, root string) {
	t.Helper()
	if got.exitCode != 0 {
		t.Fatalf("build step exited %d:\n%s", got.exitCode, got.combined)
	}
	if len(got.invocations) != 1 || len(got.invocations[0]) < 3 || filepath.Clean(got.invocations[0][2]) != filepath.Clean(root) {
		t.Fatalf("build step ran go %v, want one build in %s", got.invocations, root)
	}
	if source := got.outputs["source_root"]; filepath.Clean(source) != filepath.Clean(root) {
		t.Fatalf("source_root output is %q, want %q", source, root)
	}
}

// sourceCheckouts lists the checkouts the build step made under the runner's temporary directory.
func sourceCheckouts(t *testing.T, got stepOutcome) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(got.runnerTemp, adoptSourcePrefix+"*"))
	if err != nil {
		t.Fatalf("list checkouts: %v", err)
	}
	return matches
}

// Positive: a forced adopt run of an action tree with no .git builds from, and publishes as the lock
// source, a git checkout of the action's own repository at the action's own ref, made in a fresh
// directory under the runner's temporary directory. Boundary: each form a `uses:` ref takes, a tag,
// a commit id and a branch, checks out the commit it names, so the branch case gets main's newer
// tip and the other two the tagged commit.
func TestPraetorAdoptBuild_Positive_ForcedRunInAnArchiveTreeChecksOutTheActionRef(t *testing.T) {
	remote := serveAdoptRemote(t, adoptModule)
	cases := map[string]struct{ ref, commit, marker string }{
		"tag":    {adoptRemoteTag, remote.tagged, "tagged\n"},
		"commit": {remote.tagged, remote.tagged, "tagged\n"},
		"branch": {"main", remote.tip, "tip\n"},
	}
	for name := range cases {
		t.Run(name, func(t *testing.T) {
			actionPath, root := adoptCheckoutFixture(t, adoptModule)
			got := runForcedBuild(t, actionPath, remote.serverURL, actionSource{repository: adoptRemoteRepository, ref: cases[name].ref})
			checkouts := sourceCheckouts(t, got)
			if len(checkouts) != 1 {
				t.Fatalf("build step made checkouts %v, want one:\n%s", checkouts, got.combined)
			}
			assertBuiltFrom(t, got, checkouts[0])
			if filepath.Clean(checkouts[0]) == filepath.Clean(root) {
				t.Fatal("the build step kept the archive tree")
			}
			head := testsupport.RunFixtureGit(t, checkouts[0], []string{"rev-parse", "HEAD"})
			marker, err := os.ReadFile(filepath.Join(checkouts[0], adoptRemoteMarker)) //nolint:gosec // the test's own checkout
			if err != nil || head != cases[name].commit || string(marker) != cases[name].marker {
				t.Fatalf("checkout holds %s with marker %q (err %v), want %s with %q", head, marker, err, cases[name].commit, cases[name].marker)
			}
		})
	}
}

// Negative: a tree that already is a git checkout, a .git directory or the .git file of a worktree,
// stays the source of a forced run; an unforced run and a forced dogfood run, which rebuild no lock,
// keep the archive tree too. The server holds no repository, so any fetch would fail the step.
func TestPraetorAdoptBuild_Negative_GitCheckoutOrUnforcedRunKeepsTheActionTree(t *testing.T) {
	cases := map[string]struct {
		dotGit string // "dir", "file" or "" for none
		runner []string
	}{
		"git checkout, forced":             {dotGit: "dir"},
		"worktree .git file, forced":       {dotGit: "file"},
		"archive tree, not forced":         {runner: []string{"PRAETOR_FORCE=false"}},
		"archive tree, forced dogfood run": {runner: []string{"PRAETOR_MODE=dogfood"}},
	}
	for name := range cases {
		t.Run(name, func(t *testing.T) {
			actionPath, root := adoptCheckoutFixture(t, adoptModule)
			switch cases[name].dotGit {
			case "dir":
				if err := os.Mkdir(filepath.Join(root, ".git"), 0o750); err != nil {
					t.Fatal(err)
				}
			case "file":
				writeAdoptFixtureFile(t, filepath.Join(root, ".git"), "gitdir: /elsewhere/.git/worktrees/praetor\n")
			}
			source := actionSource{repository: adoptRemoteRepository, ref: adoptRemoteTag}
			got := runForcedBuild(t, actionPath, "file://"+filepath.ToSlash(t.TempDir()), source, cases[name].runner...)
			assertBuiltFrom(t, got, root)
			if checkouts := sourceCheckouts(t, got); len(checkouts) != 0 {
				t.Fatalf("the build step checked out %v", checkouts)
			}
		})
	}
}

// Negative: a forced run in an archive tree that cannot get a praetor checkout builds nothing,
// publishes no binary or lock source and says why: GitHub named no repository and ref (a local
// action with no git checkout), a ref git would read as an option or a refspec with a destination
// (refused before git runs), a repository the server does not hold, and a checkout that is not
// praetor. Boundary: the option-like ref carries a command, and it never runs.
func TestPraetorAdoptBuild_Negative_ForcedRunWithoutAPraetorCheckoutBuildsNothing(t *testing.T) {
	remote := serveAdoptRemote(t, adoptModule)
	other := serveAdoptRemote(t, "example.test/other")
	absent := "file://" + filepath.ToSlash(t.TempDir())
	cases := map[string]struct {
		serverURL string
		source    actionSource
		want      string
	}{
		"no repository or ref":  {remote.serverURL, actionSource{}, "GitHub named no repository ('') and ref ('')"},
		"option-like ref":       {remote.serverURL, actionSource{adoptRemoteRepository, "--upload-pack=touch pwned"}, "GitHub named no repository"},
		"refspec with a target": {remote.serverURL, actionSource{adoptRemoteRepository, "main:refs/heads/x"}, "GitHub named no repository"},
		"repository not served": {absent, actionSource{adoptRemoteRepository, adoptRemoteTag}, "checking out " + absent + "/" + adoptRemoteRepository + " at " + adoptRemoteTag + " failed"},
		"checkout not praetor":  {other.serverURL, actionSource{adoptRemoteRepository, adoptRemoteTag}, "the checkout of " + adoptRemoteRepository + " at " + adoptRemoteTag + " is not a praetor checkout"},
	}
	for name := range cases {
		t.Run(name, func(t *testing.T) {
			actionPath, _ := adoptCheckoutFixture(t, adoptModule)
			got := runForcedBuild(t, actionPath, cases[name].serverURL, cases[name].source)
			assertBuildRefused(t, got, cases[name].want)
			if _, err := os.Stat(filepath.Join(got.workDir, "pwned")); err == nil {
				t.Fatal("the ref ran a command of its own")
			}
			if strings.Contains(got.combined, "forced adoption; building") {
				t.Errorf("a refused run still announced a build:\n%s", got.combined)
			}
		})
	}
}
