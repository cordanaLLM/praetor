// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavors"
	"github.com/cordanaLLM/praetor/internal/testsupport"
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
	if _, ok, err := resolveFlavorRef(context.Background(), t.TempDir(), "refs/tags/$(touch pwned)"); ok || err != nil {
		t.Errorf("a ref carrying shell metacharacters must not resolve: ok=%v err=%v", ok, err)
	}
	if _, ok, err := resolveFlavorRef(context.Background(), t.TempDir(), ""); ok || err != nil {
		t.Errorf("an empty ref must not resolve: ok=%v err=%v", ok, err)
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
	env := initGitFixture(t, dir, filesRefStoreArgs(t)...)
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

// filesRefStoreArgs returns the git init arguments that keep a fixture's refs in the files
// backend, so corruptPackedRefs breaks its ref store whatever backend the installed git
// defaults to: the reftable backend, the planned git 3.0 default, ignores packed-refs. A
// git that rejects --ref-format predates the reftable backend (git 2.45) and keeps refs as
// files already, so it gets no argument.
func filesRefStoreArgs(t *testing.T) []string {
	t.Helper()
	if _, err := runFixtureGit(t, t.TempDir(), testsupport.HermeticGitEnv(t), "init", "-q", "--ref-format=files"); err != nil {
		return nil
	}
	return []string{"--ref-format=files"}
}

// corruptPackedRefs writes garbage into the packed-refs file of the repository dir, which
// git refuses to read (exit status 128) under the files ref backend, while the context
// stays live.
func corruptPackedRefs(t *testing.T, dir string, env []string) {
	t.Helper()
	if format, err := runFixtureGit(t, dir, env, "rev-parse", "--show-ref-format"); err == nil && strings.TrimSpace(format) == "reftable" {
		t.Fatalf("fixture %s uses the reftable backend, which ignores packed-refs", dir)
	}
	writeFixtureFile(t, dir, filepath.Join(".git", "packed-refs"), "corrupt refs garbage\n")
}

func TestResolveFlavorRef_Positive_HighestStableTagWins(t *testing.T) {
	f := newTagFixture(t)
	f.commitAndTag(t, "v0.1.0")
	wantCommit := f.commitAndTag(t, "v0.2.0")

	got, ok, err := resolveFlavorRef(context.Background(), f.dir, "refs/tags/v*")
	if !ok || err != nil {
		t.Fatalf("expected refs/tags/v* to resolve with two stable tags present: %v", err)
	}
	if got != wantCommit {
		t.Errorf("resolved %q, want v0.2.0's commit %q", got, wantCommit)
	}
}

func TestResolveFlavorRef_Negative_OnlyPrereleasesLeaveLatestUnresolved(t *testing.T) {
	f := newTagFixture(t)
	f.commitAndTag(t, "v0.2.0-rc.1")

	// Pending, not an error: "no stable tag exists" stays distinct from an overflow (#389).
	if got, ok, err := resolveFlavorRef(context.Background(), f.dir, "refs/tags/v*"); ok || err != nil {
		t.Errorf("expected an rc-only repository to leave latest unresolved (pending), got %q, %v", got, err)
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

	got, ok, err := resolveFlavorRef(context.Background(), f.dir, "refs/tags/v*")
	if !ok || err != nil {
		t.Fatalf("expected refs/tags/v* to resolve: %v", err)
	}
	if got != wantCommit {
		t.Errorf("resolved %q, want v0.3.0+build.7's commit %q", got, wantCommit)
	}
}

func TestResolveFlavorRef_Boundary_UnmatchedPatternResolvesToPendingWithoutError(t *testing.T) {
	f := newTagFixture(t)
	// A pattern that matches nothing returns ok=false, err=nil: pending flavor, not an error (#671).
	if got, ok, err := resolveFlavorRef(context.Background(), f.dir, "refs/tags/v*"); ok || err != nil || got != "" {
		t.Fatalf("unmatched tag pattern must resolve to pending: got=%q ok=%v err=%v", got, ok, err)
	}
	if got, ok, err := resolveFlavorRef(context.Background(), f.dir, "refs/heads/lts-*"); ok || err != nil || got != "" {
		t.Fatalf("unmatched branch pattern must resolve to pending: got=%q ok=%v err=%v", got, ok, err)
	}
}

func TestResolveFlavorRef_Negative_FailedGitReadReturnsErrorNamingPatternAndCause(t *testing.T) {
	f := newTagFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// A failed git read for a tag pattern must return an error naming the pattern and the cause (#671).
	got, ok, err := resolveFlavorRef(ctx, f.dir, "refs/tags/v*")
	if err == nil || ok || got != "" {
		t.Fatalf("cancelled tag read must fail with error: got=%q ok=%v err=%v", got, ok, err)
	}
	mustContain(t, err.Error(), `"refs/tags/v*"`, "context canceled")

	// A failed git read for a branch pattern must also name the pattern and the cause.
	got, ok, err = resolveFlavorRef(ctx, f.dir, "refs/heads/lts-*")
	if err == nil || ok || got != "" {
		t.Fatalf("cancelled branch read must fail with error: got=%q ok=%v err=%v", got, ok, err)
	}
	mustContain(t, err.Error(), `"refs/heads/lts-*"`, "context canceled")

	// A failed git read for a concrete ref must also name the ref and the cause (#671).
	got, ok, err = resolveFlavorRef(ctx, f.dir, "refs/heads/main")
	if err == nil || ok || got != "" {
		t.Fatalf("cancelled concrete ref read must fail with error: got=%q ok=%v err=%v", got, ok, err)
	}
	mustContain(t, err.Error(), `"refs/heads/main"`, "context canceled")

	// A failed git read during fetchCurrentTags under a cancelled context must record an error (#671).
	cfg := &flavors.Config{
		Flavors: map[string]flavors.Flavor{
			"bleeding": {SourceRef: "refs/heads/main"},
		},
	}
	resolver := &flavorRefResolver{ctx: ctx, dir: f.dir}
	tags := fetchCurrentTags(cfg, resolver.resolve)
	if len(tags) != 0 || resolver.err == nil {
		t.Fatalf("cancelled current-tag read must record error: tags=%v err=%v", tags, resolver.err)
	}
	mustContain(t, resolver.err.Error(), `"refs/tags/bleeding"`, "context canceled")
}

func TestResolveFlavorRef_Negative_CorruptPackedRefsFailsConcreteAndTagReads(t *testing.T) {
	f := newTagFixture(t)
	// Corrupt packed-refs so git rev-parse fails on concrete refs and tags.
	corruptPackedRefs(t, f.dir, f.env)

	// Concrete ref read must return a wrapped error, not ok=false, err=nil (#671).
	got, ok, err := resolveFlavorRef(context.Background(), f.dir, "refs/heads/main")
	if err == nil || ok || got != "" {
		t.Fatalf("corrupt ref store concrete read must fail with error: got=%q ok=%v err=%v", got, ok, err)
	}
	mustContain(t, err.Error(), `"refs/heads/main"`, "failed to resolve ref")

	// Current tag read via fetchCurrentTags must also record the error.
	cfg := &flavors.Config{
		Flavors: map[string]flavors.Flavor{
			"bleeding": {SourceRef: "refs/heads/main"},
		},
	}
	resolver := &flavorRefResolver{ctx: context.Background(), dir: f.dir}
	tags := fetchCurrentTags(cfg, resolver.resolve)
	if len(tags) != 0 || resolver.err == nil {
		t.Fatalf("corrupt ref store current-tag read must record error: tags=%v err=%v", tags, resolver.err)
	}
	mustContain(t, resolver.err.Error(), `"refs/tags/bleeding"`, "failed to resolve ref")
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
	f.env = initGitFixture(t, f.repo, filesRefStoreArgs(t)...)
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

func TestRunFlavors_Negative_FailedGitReadFailsPlanAndSync(t *testing.T) {
	f := newFlavorFixture(t)
	// Corrupt packed-refs so git for-each-ref fails with an error on pattern reads.
	corruptPackedRefs(t, f.repo, f.env)

	out, err := captureStdout(t, func() error {
		return runFlavors([]string{"plan", "--config=" + f.config, "--dir=" + f.repo})
	})
	if err == nil {
		t.Fatalf("plan must fail when git read fails:\n%s", out)
	}
	mustContain(t, err.Error(), "failed to resolve flavor refs", "failed to resolve ref")
	if strings.Contains(out, "<unresolved>") {
		t.Errorf("a failed git read must not print a plan that reads as pending:\n%s", out)
	}

	if out, err := f.sync(t, "--push"); err == nil {
		t.Fatalf("sync must fail when git read fails:\n%s", out)
	}
	for _, dir := range []string{f.repo, f.remote} {
		if got := f.tagAt(t, dir, "bleeding"); got != "" {
			t.Errorf("a failed git read sync moved bleeding in %s to %q", dir, got)
		}
	}
}

func TestRunFlavors_Negative_ConcreteOnlyWithCorruptPackedRefsFails(t *testing.T) {
	f := newFlavorFixture(t)
	// Config with only a concrete ref (no glob patterns). Under a corrupt ref store,
	// resolveRefCommit must not map rev-parse failure to pending (#671).
	concreteConfig := filepath.Join(f.repo, ".config", "flavors-concrete.yaml")
	writeFixtureFile(t, f.repo, filepath.Join(".config", "flavors-concrete.yaml"), `version: 1
flavors:
  bleeding: {source_ref: "refs/heads/main"}
`)
	corruptPackedRefs(t, f.repo, f.env)

	out, err := captureStdout(t, func() error {
		return runFlavors([]string{"plan", "--config=" + concreteConfig, "--dir=" + f.repo})
	})
	if err == nil {
		t.Fatalf("plan must fail on concrete ref under corrupt ref store, got nil err and output:\n%s", out)
	}
	mustContain(t, err.Error(), "failed to resolve flavor refs", "failed to resolve ref")
	if strings.Contains(out, "[UNRESOLVED]") || strings.Contains(out, "<unresolved>") {
		t.Errorf("corrupt ref store must not produce unresolved/pending output:\n%s", out)
	}

	if outSync, errSync := captureStdout(t, func() error {
		return runFlavors([]string{"sync", "--config=" + concreteConfig, "--dir=" + f.repo})
	}); errSync == nil {
		t.Fatalf("sync must fail on concrete ref under corrupt ref store, got nil err and output:\n%s", outSync)
	}
}
