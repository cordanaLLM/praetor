package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// tagNonSemverNoise points count lightweight tags at HEAD in one git process. Each name
// matches the "refs/tags/v*" glob, sorts before any "v<digit>" tag, and does not parse as
// SemVer, so the noise fills the candidate scan without ever being a resolution winner.
func tagNonSemverNoise(t *testing.T, dir string, env []string, count int) {
	t.Helper()
	if count < 1 || count > maxSemverTagCandidates+1 {
		t.Fatal("noise count outside the bounded test range")
	}
	head := fixtureGit(t, dir, env, "rev-parse", "HEAD")
	var input strings.Builder
	for i := 1; i <= count; i++ {
		fmt.Fprintf(&input, "create refs/tags/v-nonsemver-%05d %s\n", i, head)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "update-ref", "--stdin")
	cmd.Dir, cmd.Env, cmd.Stdin = dir, env, strings.NewReader(input.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create tag noise: %v (%s)", err, out)
	}
}

// Exactly maxSemverTagCandidates matching tags, the stable one sorting last, is inside the
// bound: the scan sees every candidate and resolves the winner.
func TestResolveFlavorRef_Boundary_StableTagAtTheBoundResolves(t *testing.T) {
	f := newTagFixture(t)
	tagNonSemverNoise(t, f.dir, f.env, maxSemverTagCandidates-1)
	wantCommit := f.commitAndTag(t, "v7.0.0")

	got, ok, err := resolveFlavorRef(context.Background(), f.dir, "refs/tags/v*")
	if err != nil || !ok {
		t.Fatalf("%d candidates must resolve: ok=%v err=%v", maxSemverTagCandidates, ok, err)
	}
	if got != wantCommit {
		t.Errorf("resolved %q, want v7.0.0's commit %q", got, wantCommit)
	}
}

// One matching tag past the bound is an overflow error, never ok=false: a pending flavor
// means no stable tag exists, which a truncated scan cannot claim (#389).
func TestResolveFlavorRef_Negative_StableTagPastTheBoundIsAnOverflow(t *testing.T) {
	f := newTagFixture(t)
	tagNonSemverNoise(t, f.dir, f.env, maxSemverTagCandidates)
	wantCommit := f.commitAndTag(t, "v7.0.0")

	got, ok, err := resolveFlavorRef(context.Background(), f.dir, "refs/tags/v*")
	if err == nil || ok || got != "" {
		t.Fatalf("%d candidates must fail as an overflow: got=%q ok=%v err=%v", maxSemverTagCandidates+1, got, ok, err)
	}
	mustContain(t, err.Error(), `"refs/tags/v*"`, fmt.Sprintf("more than %d tags", maxSemverTagCandidates), "narrow source_ref")

	// The remedy the error names: a narrowed pattern inside the bound resolves again.
	if got, ok, err := resolveFlavorRef(context.Background(), f.dir, "refs/tags/v7.*"); !ok || err != nil || got != wantCommit {
		t.Errorf("narrowed source_ref = %q ok=%v err=%v, want %q", got, ok, err, wantCommit)
	}

	// A branch glob never takes the SemVer scan, so the tag bound does not apply to it.
	if _, ok, err := resolveFlavorRef(context.Background(), f.dir, "refs/heads/ma*"); !ok || err != nil {
		t.Errorf("a branch glob must still resolve beside an overflowing tag set: ok=%v err=%v", ok, err)
	}
}

// A stable tag that sorts after maxSemverTagCandidates non-SemVer tags must never leave
// latest silently unresolved (#389): plan and sync both fail, and no tag moves.
func TestRunFlavors_Negative_TagCandidatesPastTheBoundFailPlanAndSync(t *testing.T) {
	f := newFlavorFixture(t)
	tagNonSemverNoise(t, f.repo, f.env, maxSemverTagCandidates)
	fixtureGit(t, f.repo, f.env, "tag", "v7.0.0")

	out, err := captureStdout(t, func() error {
		return runFlavors([]string{"plan", "--config=" + f.config, "--dir=" + f.repo})
	})
	if err == nil {
		t.Fatalf("plan must fail when refs/tags/v* matches more than %d tags:\n%s", maxSemverTagCandidates, out)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("more than %d", maxSemverTagCandidates)) {
		t.Errorf("plan error must name the bound, got %v", err)
	}
	if strings.Contains(out, "<unresolved>") {
		t.Errorf("a truncated scan must not print a plan that reads as pending:\n%s", out)
	}

	if out, err := f.sync(t, "--push"); err == nil {
		t.Fatalf("sync must fail when refs/tags/v* overflows the bound:\n%s", out)
	}
	for _, dir := range []string{f.repo, f.remote} {
		if got := f.tagAt(t, dir, "bleeding"); got != "" {
			t.Errorf("an overflowing sync moved bleeding in %s to %q", dir, got)
		}
	}
}
