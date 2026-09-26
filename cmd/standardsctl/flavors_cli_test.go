// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavors"
)

func TestApplyFlavorTransitions_Negative_StrictRefusesUnresolvedSourceRef(t *testing.T) {
	transitions := []flavors.TagTransition{
		// A resolvable transition listed first: under --strict the plan must be rejected
		// whole, so this tag is never moved even though it precedes the unresolvable one.
		{FlavorName: "bleeding", TargetRef: "refs/heads/main", TargetCommit: "abc", Action: flavors.ActionUpdate},
		{
			FlavorName: "latest",
			CurrentRef: "8feca96",
			TargetRef:  "refs/tags/v9.9.9",
			Action:     flavors.ActionUnresolved,
		},
	}

	if err := validateFlavorPlan(transitions, true); err == nil {
		t.Fatal("expected strict plan validation to reject the unresolvable transition")
	}
	if err := validateFlavorPlan(transitions, false); err != nil {
		t.Fatalf("without --strict an unresolved flavor is pending, not an error: %v", err)
	}

	// The target directory does not matter: the refusal happens before any git call, so
	// under --strict nothing moves when one flavor cannot resolve.
	moved, err := applyFlavorTransitions(context.Background(), t.TempDir(), transitions, true)
	if err == nil {
		t.Fatal("expected an unresolved transition to abort the strict sync")
	}
	if len(moved) != 0 || !strings.Contains(err.Error(), "resolves to no commit") {
		t.Errorf("unexpected result: moved=%v err=%v", moved, err)
	}
}

func TestApplyFlavorTransitions_Negative_RejectsOptionShapedTagName(t *testing.T) {
	transitions := []flavors.TagTransition{
		{FlavorName: "--delete", TargetRef: "refs/heads/main", TargetCommit: "abc", Action: flavors.ActionCreate},
	}
	if _, err := applyFlavorTransitions(context.Background(), t.TempDir(), transitions, false); err == nil {
		t.Fatal("a flavor name that git would parse as an option must be refused")
	}
}

func TestApplyFlavorTransitions_Positive_NoopsAndPendingAreNotRetagged(t *testing.T) {
	transitions := []flavors.TagTransition{
		{FlavorName: "latest", CurrentRef: "abc", TargetRef: "refs/tags/v1.0.0", TargetCommit: "abc", Action: flavors.ActionNoop},
		{FlavorName: "lts", TargetRef: "refs/heads/lts-*", Action: flavors.ActionUnresolved},
	}

	// No git repository exists here, so any git call would fail: success proves that
	// neither the noop nor the pending flavor reached git.
	moved, err := applyFlavorTransitions(context.Background(), t.TempDir(), transitions, false)
	if err != nil {
		t.Fatalf("a plan of noops and pending flavors must not fail: %v", err)
	}
	if len(moved) != 0 {
		t.Errorf("nothing should have moved, got %v", moved)
	}
}

func TestApplyFlavorTransitions_Boundary_EmptyPlan(t *testing.T) {
	moved, err := applyFlavorTransitions(context.Background(), t.TempDir(), nil, true)
	if err != nil || len(moved) != 0 {
		t.Fatalf("an empty plan must succeed and move nothing: moved=%v err=%v", moved, err)
	}
}

func TestFirstOutputLine_3D(t *testing.T) {
	if got := firstOutputLine("  abc \n def\n"); got != "abc" {
		t.Errorf("expected the first trimmed line, got %q", got)
	}
	if got := firstOutputLine("only"); got != "only" {
		t.Errorf("expected the single line, got %q", got)
	}
	if got := firstOutputLine("   \n\n"); got != "" {
		t.Errorf("expected an empty result for blank output, got %q", got)
	}
}

func TestResolveFlavorRef_Negative_RejectsShellMetacharacters(t *testing.T) {
	if _, ok := resolveFlavorRef(context.Background(), t.TempDir(), "refs/tags/$(touch pwned)"); ok {
		t.Error("a ref carrying shell metacharacters must not resolve")
	}
	if _, ok := resolveFlavorRef(context.Background(), t.TempDir(), ""); ok {
		t.Error("an empty ref must not resolve")
	}
}

// tagFixture is a repository where each call to commitAndTag adds a new commit and tags it,
// so distinct tags resolve to distinct, individually identifiable commits.
type tagFixture struct {
	dir string
	env []string
}

func newTagFixture(t *testing.T) tagFixture {
	t.Helper()
	dir := t.TempDir()
	writeFixtureFile(t, dir, "README.md", "zero\n")
	env := initGitFixture(t, dir)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(dir, "no-such-gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(dir, "no-such-gitconfig"))
	return tagFixture{dir: dir, env: env}
}

// commitAndTag adds a commit and tags it, returning the commit it tagged.
func (f tagFixture) commitAndTag(t *testing.T, tag string) string {
	t.Helper()
	writeFixtureFile(t, f.dir, "VERSION", tag+"\n")
	gitCommitAll(t, f.dir, f.env, "tag "+tag)
	commit := fixtureGit(t, f.dir, f.env, "rev-parse", "HEAD")
	fixtureGit(t, f.dir, f.env, "tag", tag)
	return commit
}

func TestResolveFlavorRef_Positive_HighestStableTagWins(t *testing.T) {
	f := newTagFixture(t)
	f.commitAndTag(t, "v0.1.0")
	wantCommit := f.commitAndTag(t, "v0.2.0")

	got, ok := resolveFlavorRef(context.Background(), f.dir, "refs/tags/v*")
	if !ok {
		t.Fatal("expected refs/tags/v* to resolve with two stable tags present")
	}
	if got != wantCommit {
		t.Errorf("resolved %q, want v0.2.0's commit %q", got, wantCommit)
	}
}

func TestResolveFlavorRef_Negative_OnlyPrereleasesLeaveLatestUnresolved(t *testing.T) {
	f := newTagFixture(t)
	f.commitAndTag(t, "v0.2.0-rc.1")

	if got, ok := resolveFlavorRef(context.Background(), f.dir, "refs/tags/v*"); ok {
		t.Errorf("expected an rc-only repository to leave latest unresolved (pending), got %q", got)
	}
}

func TestResolveFlavorRef_Boundary_PrereleaseBuildMetadataAndNonSemverTagsIgnored(t *testing.T) {
	f := newTagFixture(t)
	f.commitAndTag(t, "v0.1.0")
	// A release candidate above v0.1.0 by major.minor.patch must still lose to it: a
	// prerelease never outranks a stable release for "latest" (#245).
	f.commitAndTag(t, "v0.2.0-rc.1")
	// A tag that matches the "v*" glob syntactically but is not SemVer must be ignored
	// rather than aborting resolution.
	f.commitAndTag(t, "v-nightly")
	// The true winner: highest stable version, carrying build metadata that must not
	// affect precedence or prevent selection.
	wantCommit := f.commitAndTag(t, "v0.3.0+build.7")

	got, ok := resolveFlavorRef(context.Background(), f.dir, "refs/tags/v*")
	if !ok {
		t.Fatal("expected refs/tags/v* to resolve")
	}
	if got != wantCommit {
		t.Errorf("resolved %q, want v0.3.0+build.7's commit %q", got, wantCommit)
	}
}

// flavorFixture is a repository shaped like this one before its first release: main has
// moved past an old `latest` tag, no v* tag and no lts-* branch exist, and a bare
// repository stands in for the forge.
type flavorFixture struct {
	repo, remote, config string
	env                  []string
	oldCommit, head      string
}

const flavorFixtureConfig = `version: 1
flavors:
  bleeding: {source_ref: "refs/heads/main"}
  edge:     {source_ref: "refs/heads/main"}
  latest:   {source_ref: "refs/tags/v*"}
  lts:      {source_ref: "refs/heads/lts-*"}
`

func newFlavorFixture(t *testing.T) flavorFixture {
	t.Helper()
	root := t.TempDir()
	f := flavorFixture{repo: filepath.Join(root, "repo"), remote: filepath.Join(root, "remote.git")}
	f.config = writeFixtureFile(t, root, "flavors.yaml", flavorFixtureConfig)
	writeFixtureFile(t, f.repo, "README.md", "one\n")
	f.env = initGitFixture(t, f.repo)
	// The code under test inherits the process environment; keep it off the developer's
	// git configuration the way the fixture's own git calls are.
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "no-such-gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(root, "no-such-gitconfig"))

	f.oldCommit = fixtureGit(t, f.repo, f.env, "rev-parse", "HEAD")
	fixtureGit(t, f.repo, f.env, "tag", "latest")
	writeFixtureFile(t, f.repo, "README.md", "two\n")
	gitCommitAll(t, f.repo, f.env, "second")
	f.head = fixtureGit(t, f.repo, f.env, "rev-parse", "HEAD")

	fixtureGit(t, root, f.env, "init", "-q", "--bare", f.remote)
	fixtureGit(t, f.repo, f.env, "remote", "add", "origin", f.remote)
	fixtureGit(t, f.repo, f.env, "push", "-q", "origin", "main", "refs/tags/latest")
	return f
}

func fixtureGit(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	out, err := runFixtureGit(t, dir, env, args...)
	if err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, out)
	}
	return strings.TrimSpace(out)
}

// tagAt returns the commit a tag points at in dir, or "" when the tag is absent.
func (f flavorFixture) tagAt(t *testing.T, dir, name string) string {
	t.Helper()
	out, err := runFixtureGit(t, dir, f.env, "rev-parse", "--verify", "--quiet", "refs/tags/"+name+"^{commit}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func (f flavorFixture) sync(t *testing.T, extra ...string) (string, error) {
	t.Helper()
	args := append([]string{"sync", "--config=" + f.config, "--dir=" + f.repo}, extra...)
	return captureStdout(t, func() error { return runFlavors(args) })
}

func TestRunFlavorsSync_Positive_MovesResolvableFlavorsAndPublishesThem(t *testing.T) {
	f := newFlavorFixture(t)

	out, err := f.sync(t, "--push")
	if err != nil {
		t.Fatalf("sync must succeed while latest and lts are pending: %v\n%s", err, out)
	}
	for _, dir := range []string{f.repo, f.remote} {
		for _, name := range []string{"bleeding", "edge"} {
			if got := f.tagAt(t, dir, name); got != f.head {
				t.Errorf("%s in %s = %q, want main %q", name, filepath.Base(dir), got, f.head)
			}
		}
		// The stale stable pointer is left where it was, never aliased to main.
		if got := f.tagAt(t, dir, "latest"); got != f.oldCommit {
			t.Errorf("latest in %s = %q, want untouched %q", filepath.Base(dir), got, f.oldCommit)
		}
		if got := f.tagAt(t, dir, "lts"); got != "" {
			t.Errorf("lts must stay absent in %s, got %q", filepath.Base(dir), got)
		}
	}
	mustContain(t, out, "Pending latest", "Pending lts", "2 tag(s) moved, 0 already current, 2 pending",
		"Published 2 flavor tag(s) to origin: bleeding, edge")
}

func TestRunFlavorsSync_Negative_StrictRefusesBeforeAnyTagMoves(t *testing.T) {
	f := newFlavorFixture(t)

	if out, err := f.sync(t, "--strict", "--push"); err == nil {
		t.Fatalf("--strict must fail while latest cannot resolve:\n%s", out)
	}
	for _, dir := range []string{f.repo, f.remote} {
		if got := f.tagAt(t, dir, "bleeding"); got != "" {
			t.Errorf("a refused strict sync moved bleeding in %s to %q", filepath.Base(dir), got)
		}
	}
}

func TestRunFlavorsSync_Boundary_SigningConfigAndNothingToPublish(t *testing.T) {
	f := newFlavorFixture(t)
	// A workstation that signs every tag. Without --no-sign, `git tag -f` fails here
	// with "no tag message" and the sync never gets past its first flavor.
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "tag.gpgSign")
	t.Setenv("GIT_CONFIG_VALUE_0", "true")

	if out, err := f.sync(t); err != nil {
		t.Fatalf("sync under tag.gpgSign=true failed: %v\n%s", err, out)
	}
	if kind := fixtureGit(t, f.repo, f.env, "cat-file", "-t", "refs/tags/bleeding"); kind != "commit" {
		t.Errorf("moving tag should be lightweight, got a %s object", kind)
	}

	// Second run: every resolvable flavor is current, so --push must not contact the
	// remote at all. The remote named here does not exist and would fail a push.
	out, err := f.sync(t, "--push", "--remote=no-such-remote")
	if err != nil {
		t.Fatalf("a sync with nothing to publish must not push: %v\n%s", err, out)
	}
	mustContain(t, out, "0 tag(s) moved, 2 already current, 2 pending", "Nothing to publish")
}

func TestPushFlavorTags_Negative_UnusableOrMissingRemote(t *testing.T) {
	f := newFlavorFixture(t)
	ctx := context.Background()

	if err := pushFlavorTags(ctx, f.repo, "--upload-pack=touch", []string{"latest"}); err == nil {
		t.Error("an option-shaped remote must be refused before git runs")
	}
	if err := pushFlavorTags(ctx, f.repo, "no-such-remote", []string{"latest"}); err == nil {
		t.Error("a push to a remote that does not exist must fail")
	}
}

// The reconciler heading names the tool, never this product's own repository (#361).
func TestPrintFlavorPlan_HeadingIsRepositoryNeutral(t *testing.T) {
	for _, transitions := range [][]flavors.TagTransition{
		nil,
		{{FlavorName: "stable", TargetRef: "v1.*", Action: flavors.ActionUnresolved}},
	} {
		out, err := captureStdout(t, func() error { printFlavorPlan(transitions); return nil })
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(out, "=== Praetor Release Flavor Reconciler ===\n") || strings.Contains(out, "cordanaLLM/praetor") {
			t.Errorf("heading = %q", out)
		}
		if len(transitions) > 0 && !strings.Contains(out, "<absent> -> <unresolved>") {
			t.Errorf("transition line missing: %q", out)
		}
	}
}
