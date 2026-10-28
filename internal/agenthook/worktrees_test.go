// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/topology"
)

type worktreeFixture struct {
	devRoot string
	repoDir string
}

func newWorktreeFixture(t *testing.T) worktreeFixture {
	t.Helper()
	base := t.TempDir()
	resolvedBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	devRoot := filepath.Join(resolvedBase, "dev-root")
	repoDir := filepath.Join(devRoot, "acme", "praetor")
	initWorktreeTestRepo(t, repoDir)
	if err := os.MkdirAll(filepath.Join(devRoot, topology.WorktreesContainer), 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv(topology.DevRootEnv, devRoot)
	t.Setenv(topology.LegacyDevRootEnv, "")
	return worktreeFixture{devRoot: devRoot, repoDir: repoDir}
}

func initWorktreeTestRepo(t *testing.T, repoDir string) {
	t.Helper()
	gitDir := filepath.Join(repoDir, ".git")
	if err := os.MkdirAll(gitDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func enforcingWorktreePolicy(t *testing.T, workDir string) *Policy {
	t.Helper()
	policy, err := BuildPolicy(config.HookSettings{Worktrees: config.WorktreeSettings{Enforce: true}})
	if err != nil {
		t.Fatalf("BuildPolicy: %v", err)
	}
	return policy.WithWorkDir(workDir)
}

func TestWorktrees_Boundary_EnforceOffAllowsAnyPath(t *testing.T) {
	fx := newWorktreeFixture(t)
	policy, err := BuildPolicy(config.HookSettings{})
	if err != nil {
		t.Fatalf("BuildPolicy: %v", err)
	}
	policy.WithWorkDir(fx.repoDir)
	for _, cmd := range []string{
		"git worktree add /tmp/x",
		"git worktree add ../x",
		"git worktree add",
		"git worktree add --unknown-flag /tmp/x",
	} {
		if verdict := policy.Command(cmd); verdict.Outcome != Allow {
			t.Fatalf("enforce off refused %q: %+v", cmd, verdict)
		}
	}
}

func TestWorktrees_Positive_DeclaredDevRootWorktreeAllowed(t *testing.T) {
	fx := newWorktreeFixture(t)
	policy := enforcingWorktreePolicy(t, fx.repoDir)
	target := filepath.Join(fx.devRoot, topology.WorktreesContainer, "praetor-topic")
	cmd := "git worktree add " + target + " -b x"
	if verdict := policy.Command(cmd); verdict.Outcome != Allow {
		t.Fatalf("expected allow for %q, got %+v", cmd, verdict)
	}

	// Linked worktree cwd resolves back to the main repository folder name ("praetor")
	// through GitCommonDir parent.
	linkedWT := filepath.Join(fx.devRoot, topology.WorktreesContainer, "praetor-existing")
	linkedGit := filepath.Join(fx.repoDir, ".git", topology.WorktreesContainer, "existing")
	if err := os.MkdirAll(linkedGit, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linkedGit, "HEAD"), []byte("ref: refs/heads/existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linkedGit, "commondir"), []byte("../..\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(linkedWT, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linkedWT, ".git"), []byte("gitdir: "+linkedGit+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if verdict := policy.CommandIn(context.Background(), cmd, linkedWT); verdict.Outcome != Allow {
		t.Fatalf("expected allow from linked worktree cwd, got %+v", verdict)
	}
}

func TestWorktrees_Negative_OtherRepoPrefixRefused(t *testing.T) {
	fx := newWorktreeFixture(t)
	policy := enforcingWorktreePolicy(t, fx.repoDir)
	badTarget := filepath.Join(fx.devRoot, topology.WorktreesContainer, "other-topic")
	verdict := policy.Command("git worktree add " + badTarget)
	if verdict.Outcome != Deny {
		t.Fatalf("expected deny for %q, got %+v", badTarget, verdict)
	}
	wantWorktreeForm := filepath.Join(fx.devRoot, topology.WorktreesContainer, "praetor-<topic>")
	wantClaudeForm := filepath.Join(fx.repoDir, ".claude", topology.WorktreesContainer, "<name>")
	for _, want := range []string{badTarget, wantWorktreeForm, wantClaudeForm} {
		if !strings.Contains(verdict.Reason, want) {
			t.Errorf("refusal %q missing %q", verdict.Reason, want)
		}
	}
}

func TestWorktrees_Boundary_EmptyTopicRefused(t *testing.T) {
	fx := newWorktreeFixture(t)
	policy := enforcingWorktreePolicy(t, fx.repoDir)
	emptyTopic := filepath.Join(fx.devRoot, topology.WorktreesContainer, "praetor-")
	if verdict := policy.Command("git worktree add " + emptyTopic); verdict.Outcome != Deny {
		t.Fatalf("expected deny for empty topic %q, got %+v", emptyTopic, verdict)
	}
}

func TestWorktrees_Negative_OutsideWorktreesRefused(t *testing.T) {
	fx := newWorktreeFixture(t)
	policy := enforcingWorktreePolicy(t, fx.repoDir)
	for _, bad := range []string{
		"/tmp/x",
		filepath.Join(fx.devRoot, "praetor-topic"),
		filepath.Join(fx.devRoot, topology.WorktreesContainer, "praetor-topic", "nested"),
	} {
		if verdict := policy.Command("git worktree add " + bad); verdict.Outcome != Deny {
			t.Fatalf("expected deny for %q, got %+v", bad, verdict)
		}
	}
}

func TestWorktrees_Negative_RelativePathResolvedAndRefused(t *testing.T) {
	fx := newWorktreeFixture(t)
	policy := enforcingWorktreePolicy(t, fx.repoDir)
	verdict := policy.Command("git worktree add ../x")
	if verdict.Outcome != Deny {
		t.Fatalf("expected deny for ../x, got %+v", verdict)
	}
	wantResolved := filepath.Join(filepath.Dir(fx.repoDir), "x")
	if !strings.Contains(verdict.Reason, wantResolved) {
		t.Fatalf("refusal %q did not name resolved relative path %q", verdict.Reason, wantResolved)
	}
	traversal := policy.Command("git worktree add .claude/worktrees/../../x")
	if traversal.Outcome != Deny || !strings.Contains(traversal.Reason, filepath.Join(fx.repoDir, "x")) {
		t.Fatalf("expected resolved deny for traversal, got %+v", traversal)
	}
}

func TestWorktrees_Positive_ClaudeWorktreesAllowed(t *testing.T) {
	fx := newWorktreeFixture(t)
	policy := enforcingWorktreePolicy(t, fx.repoDir)
	for _, pathArg := range []string{
		".claude/worktrees/x",
		filepath.Join(fx.repoDir, ".claude", topology.WorktreesContainer, "x"),
	} {
		if verdict := policy.Command("git worktree add " + pathArg); verdict.Outcome != Allow {
			t.Fatalf("expected allow for %q, got %+v", pathArg, verdict)
		}
	}
	bareClaudeDir := filepath.Join(fx.repoDir, ".claude", topology.WorktreesContainer)
	if verdict := policy.Command("git worktree add " + bareClaudeDir); verdict.Outcome != Deny {
		t.Fatalf("expected deny for bare .claude/worktrees dir, got %+v", verdict)
	}
}

func TestWorktrees_Positive_GitDashCResolvesAgainstTargetRepo(t *testing.T) {
	fx := newWorktreeFixture(t)
	outsideDir := t.TempDir()
	policy := enforcingWorktreePolicy(t, outsideDir)

	allowedCmd := "git -C " + fx.repoDir + " worktree add .claude/worktrees/agent-1 -b feat/x"
	if verdict := policy.Command(allowedCmd); verdict.Outcome != Allow {
		t.Fatalf("expected allow for %q, got %+v", allowedCmd, verdict)
	}
	refusedCmd := "git -C " + fx.repoDir + " worktree add ../escaped"
	verdict := policy.Command(refusedCmd)
	if verdict.Outcome != Deny || !strings.Contains(verdict.Reason, filepath.Join(filepath.Dir(fx.repoDir), "escaped")) {
		t.Fatalf("expected resolved deny for %q, got %+v", refusedCmd, verdict)
	}

	otherRepo := filepath.Join(fx.devRoot, "acme", "other")
	initWorktreeTestRepo(t, otherRepo)
	mismatchCmd := "git -C " + otherRepo + " worktree add " + filepath.Join(fx.devRoot, topology.WorktreesContainer, "praetor-topic")
	if verdict := policy.Command(mismatchCmd); verdict.Outcome != Deny {
		t.Fatalf("expected deny when -C repo is other, got %+v", verdict)
	}
}

func TestWorktrees_Positive_OptionsBeforeAndAfterPathParsed(t *testing.T) {
	fx := newWorktreeFixture(t)
	policy := enforcingWorktreePolicy(t, fx.repoDir)
	goodTarget := filepath.Join(fx.devRoot, topology.WorktreesContainer, "praetor-topic")
	for _, cmd := range []string{
		"git worktree add -f --detach --no-checkout --lock --reason why -q " + goodTarget + " HEAD",
		"git worktree add --force --checkout --track --guess-remote --reason=why -b feat/topic " + goodTarget + " origin/main",
		"git worktree add -B feat/reset --no-track --orphan -fq " + goodTarget,
		"git worktree add -bfeat/inline " + goodTarget + " --",
		"git worktree add -- " + goodTarget + " main",
	} {
		if verdict := policy.Command(cmd); verdict.Outcome != Allow {
			t.Fatalf("expected allow for %q, got %+v", cmd, verdict)
		}
	}
	badTarget := filepath.Join(fx.devRoot, topology.WorktreesContainer, "other-topic")
	if verdict := policy.Command("git worktree add -f -b feat/x --lock --reason test " + badTarget + " main"); verdict.Outcome != Deny {
		t.Fatalf("expected deny when options precede invalid path, got %+v", verdict)
	}
}

func TestWorktrees_Boundary_NonAddWorktreeSubcommandsUntouched(t *testing.T) {
	fx := newWorktreeFixture(t)
	policy := enforcingWorktreePolicy(t, fx.repoDir)
	for _, cmd := range []string{
		"git worktree list",
		"git worktree list --porcelain",
		"git worktree remove /tmp/x",
		"git worktree remove --force /tmp/x",
		"git worktree prune",
		"git status",
	} {
		if verdict := policy.Command(cmd); verdict.Outcome != Allow {
			t.Fatalf("expected non-add command %q untouched, got %+v", cmd, verdict)
		}
	}
}

func TestWorktrees_Negative_UnparsableWorktreeAddFailsClosed(t *testing.T) {
	fx := newWorktreeFixture(t)
	policy := enforcingWorktreePolicy(t, fx.repoDir)
	goodTarget := filepath.Join(fx.devRoot, topology.WorktreesContainer, "praetor-topic")
	cases := []struct {
		cmd  string
		want string
	}{
		{cmd: "git worktree add", want: "missing <path>"},
		{cmd: "git worktree add -f -b topic", want: "missing <path>"},
		{cmd: "git worktree add --unknown " + goodTarget, want: "unknown option --unknown"},
		{cmd: "git worktree add -x " + goodTarget, want: "unknown option -x"},
		{cmd: "git worktree add -b", want: "missing branch name for -b"},
		{cmd: "git worktree add --reason", want: "missing argument for --reason"},
		{cmd: "git worktree add --reason= " + goodTarget, want: "missing argument for --reason"},
		{cmd: "git worktree add " + goodTarget + " main extra", want: "unexpected positional argument extra"},
		{cmd: "git worktree add \"/unclosed", want: "unclosed quote"},
		{cmd: "git -C " + filepath.Join(fx.devRoot, "missing") + " worktree add " + goodTarget, want: "cannot resolve -C directory"},
	}
	for _, tc := range cases {
		verdict := policy.Command(tc.cmd)
		if verdict.Outcome != Deny || !strings.Contains(verdict.Reason, tc.want) {
			t.Fatalf("command %q: got %+v, want deny containing %q", tc.cmd, verdict, tc.want)
		}
	}
}
