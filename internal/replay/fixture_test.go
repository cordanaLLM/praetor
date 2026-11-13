// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package replay_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/replay"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// fixtureRepo wraps a temporary hermetic git repository with deterministic timestamps.
type fixtureRepo struct {
	t   *testing.T
	dir string
	env []string
}

// newFixtureRepo creates a fresh git repository on branch main in t.TempDir().
func newFixtureRepo(t *testing.T) *fixtureRepo {
	t.Helper()
	dir := t.TempDir()
	env := testsupport.HermeticGitEnv(t)
	r := &fixtureRepo{t: t, dir: dir, env: env}
	r.git(time.Time{}, "init", "--quiet", "-b", "main")
	return r
}

// git runs one git command in the fixture repository with fixed author and committer dates.
func (r *fixtureRepo) git(when time.Time, args ...string) string {
	r.t.Helper()
	env := r.env
	if !when.IsZero() {
		stamp := when.UTC().Format(time.RFC3339)
		env = append(append([]string(nil), r.env...),
			"GIT_AUTHOR_DATE="+stamp,
			"GIT_COMMITTER_DATE="+stamp,
		)
	}
	ctx, err := util.WithCommandEnvironment(r.t.Context(), env)
	if err != nil {
		r.t.Fatalf("fixture env: %v", err)
	}
	runCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := util.RunGit(runCtx, r.dir, args...)
	if err != nil {
		r.t.Fatalf("git %v: %v (%s)", args, err, out)
	}
	return strings.TrimSpace(out)
}

// writeFile writes rel inside the fixture repository.
func (r *fixtureRepo) writeFile(rel, content string) {
	r.t.Helper()
	full := filepath.Join(r.dir, filepath.FromSlash(rel))
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a fixture directory needs the owner search bit to hold files
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		r.t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := contextopt.WriteSnapshot(r.t.Context(), full, []byte(content), 0o600); err != nil {
		r.t.Fatalf("write %s: %v", rel, err)
	}
}

// commitFiles writes files, stages them, commits with msg at when, and returns the full SHA.
func (r *fixtureRepo) commitFiles(when time.Time, msg string, files map[string]string) string {
	r.t.Helper()
	for rel, content := range files {
		r.writeFile(rel, content)
	}
	r.git(when, "add", "-A")
	r.git(when, "commit", "--quiet", "-m", msg)
	return r.git(when, "rev-parse", "HEAD")
}

// fastImport pipes a git fast-import stream into the fixture repository.
func (r *fixtureRepo) fastImport(stream string) {
	r.t.Helper()
	ctx, err := util.WithCommandEnvironment(r.t.Context(), r.env)
	if err != nil {
		r.t.Fatalf("fast-import env: %v", err)
	}
	ctx, err = util.WithCommandStdin(ctx, []byte(stream))
	if err != nil {
		r.t.Fatalf("fast-import stdin: %v", err)
	}
	runCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	if out, err := util.RunGit(runCtx, r.dir, "fast-import", "--quiet"); err != nil {
		r.t.Fatalf("fast-import: %v (%s)", err, out)
	}
	r.git(time.Time{}, "checkout", "--quiet", "-f", "main")
}

// armCommand builds an arm command string invoking TestHelperReplayArm in this test binary.
func armCommand(t *testing.T, mode, findingFile string, findingLine int) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return fmt.Sprintf("%q -test.run=^TestHelperReplayArm$ -- %s %s %d {tree} {drafts}",
		exe, mode, findingFile, findingLine)
}

// TestHelperReplayArm acts as a deterministic cross-platform review arm subprocess when invoked
// with "-- <mode> <file> <line> <tree> <drafts...>".
func TestHelperReplayArm(t *testing.T) {
	sep := slices.Index(os.Args[:min(len(os.Args), 256)], "--")
	if sep < 0 || sep+5 > len(os.Args) {
		return
	}
	args := os.Args[sep+1:]
	mode, file := args[0], args[1]
	var line int
	if _, err := fmt.Sscanf(args[2], "%d", &line); err != nil {
		os.Exit(2)
	}
	treeDir := args[3]
	drafts := args[4:]
	if info, err := os.Stat(treeDir); err != nil || !info.IsDir() {
		os.Exit(3)
	}
	if mode == "isolation" {
		checkReviewArmIsolation(t, treeDir)
		mode = "stable-reject"
	}
	emitHelperVerdict(mode, file, line, drafts)
	os.Exit(0)
}

// emitHelperVerdict writes the deterministic JSON verdict for TestHelperReplayArm.
func emitHelperVerdict(mode, file string, line int, drafts []string) {
	verdict := replay.VerdictChangesRequested
	findings := []replay.Finding{{File: file, Line: line, Summary: "defect retained"}}
	switch mode {
	case "stable-reject", "trailing", "fail":
	case "require-patch":
		requireDraftPatch(drafts)
	case "wait":
		time.Sleep(2 * time.Second)
	case "stable-approve":
		verdict = replay.VerdictApprove
	case "flip-on-reverse":
		if len(drafts) > 0 && strings.Contains(filepath.Base(drafts[0]), "draft-b") {
			verdict = replay.VerdictApprove
		}
	default:
		os.Exit(4)
	}
	writeHelperVerdict(mode, verdict, findings)
}

// writeHelperVerdict emits valid output or a planted execution/decoding failure.
func writeHelperVerdict(mode, verdict string, findings []replay.Finding) {
	if mode == "fail" {
		os.Exit(7)
	}
	data, err := json.Marshal(replay.ArmVerdict{Verdict: verdict, Findings: findings})
	if err != nil {
		os.Exit(5)
	}
	if mode == "trailing" {
		data = append(data, []byte(" {}")...)
	}
	if _, err := os.Stdout.Write(append(data, '\n')); err != nil {
		os.Exit(6)
	}
}

// checkReviewArmIsolation refuses previous writes and future refs before mutating this run's tree.
func checkReviewArmIsolation(t *testing.T, treeDir string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	res, err := util.RunGitProbe(ctx, treeDir, 4096, "log", "--all", "--format=%s")
	if err != nil || strings.Contains(string(res.Stdout), "Fixes") {
		os.Exit(8)
	}
	if _, err := os.Stat(filepath.Join(treeDir, "arm-marker")); !os.IsNotExist(err) {
		os.Exit(9)
	}
	if err := contextopt.WriteSnapshot(ctx, filepath.Join(treeDir, "arm-marker"), []byte("changed"), 0o600); err != nil {
		os.Exit(10)
	}
	if err := contextopt.WriteSnapshot(ctx, filepath.Join(treeDir, "f.txt"), []byte("changed"), 0o600); err != nil {
		os.Exit(11)
	}
}

// requireDraftPatch accepts either draft order while requiring the complete PR patch.
func requireDraftPatch(drafts []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i := 0; i < len(drafts) && i < replay.MaxDrafts; i++ {
		data, err := contextopt.ReadSnapshot(ctx, drafts[i])
		if err == nil && strings.Contains(string(data), "+merged feature") {
			return
		}
	}
	os.Exit(13)
}
