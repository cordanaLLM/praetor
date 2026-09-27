package adopt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/classify"
)

const (
	geminiHookFile = ".gemini/settings.json"
	// commentedGeminiSettings is JSONC Gemini CLI reads (its settings loader strips comments
	// before JSON.parse) and a strict merge cannot rewrite without dropping the comments.
	commentedGeminiSettings = "{\n  // project settings\n  \"theme\": \"x\" /* dark */\n}\n"
)

// linkInRootFile makes rel below repoPath a relative symlink to a regular file holding content,
// also below repoPath, and returns the file. util.ConfinePath accepts such a link.
func linkInRootFile(t *testing.T, repoPath, rel, target, content string) string {
	t.Helper()
	real := filepath.Join(repoPath, filepath.FromSlash(target))
	mustWrite(t, real, content)
	link := filepath.Join(repoPath, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	relTarget, err := filepath.Rel(filepath.Dir(link), real)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(relTarget, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return real
}

// Positive: a commented .gemini/settings.json is left byte for byte alone, without a backup,
// and reported as a skip with a warning naming the command to add by hand; the other selected
// clients are still registered, the preflight accepts the file, and a rerun changes nothing.
func TestReconcileAgentHooks_Positive_CommentedGeminiLeftUntouchedAndReported(t *testing.T) {
	s := hookSession(t, false)
	mustWrite(t, hookPath(s, geminiHookFile), commentedGeminiSettings)
	if err := preflightAgentHooks(context.Background(), s.repoPath); err != nil {
		t.Fatalf("preflight refused a JSONC file Gemini CLI reads: %v", err)
	}
	for run := 0; run < 2; run++ {
		s.report = newAdoptionReport(s.repoPath, s.opts, classify.Result{Archetype: "app-service"})
		if err := reconcileAgentHooks(context.Background(), s); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if got := mustRead(t, hookPath(s, geminiHookFile)); got != commentedGeminiSettings {
			t.Fatalf("run %d: commented settings rewritten:\n%s", run, got)
		}
		if fileExists(hookPath(s, geminiHookFile+hookBackupExt)) {
			t.Fatalf("run %d: backup taken of a file left untouched", run)
		}
		action, ok := actionOf(s.report, geminiHookFile)
		if !ok || action.Action != actionSkip || !strings.Contains(action.Details, "Not strict JSON") ||
			!strings.Contains(action.Details, `"praetorctl hook gemini pre-tool" under hooks.BeforeTool`) {
			t.Fatalf("run %d: gemini report = %+v", run, action)
		}
		if len(s.report.Warnings) != 1 || !strings.HasPrefix(s.report.Warnings[0], geminiHookFile+": ") {
			t.Fatalf("run %d: warnings = %v", run, s.report.Warnings)
		}
		requireHandler(t, []byte(mustRead(t, hookPath(s, claudeHookFile))), "PreToolUse", "^Bash$", "praetorctl hook claude pre-tool", 15)
	}
}

// Negative: a commented file of a client that parses strict JSON, and a Gemini file whose
// hooks section has the wrong shape, are refused rather than skipped: the leniency covers only
// the comments Gemini CLI strips, not a document neither side can read.
func TestPlanHookTarget_Negative_OnlyGeminiCommentsAreSkipped(t *testing.T) {
	for _, tc := range []struct{ name, rel, content string }{
		{"strict client comments", ".codex/hooks.json", "{ // note\n\"hooks\": {}}"},
		{"gemini wrong shape", geminiHookFile, `{"hooks": []}`},
	} {
		repoPath := t.TempDir()
		mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(tc.rel)), tc.content)
		if err := preflightAgentHooks(context.Background(), repoPath); err == nil {
			t.Errorf("%s: preflight accepted", tc.name)
		}
	}
}

// Negative: a hook file the step would refuse fails adoption before its first write, where it
// used to fail at the last step after the manifest, the vendor files and every earlier client's
// hook file were written: a symlinked hook file, a symlinked backup path, and a hook file that
// cannot be merged. A dry run is refused the same way.
func TestAdopt_Negative_HookFileRefusedBeforeAnyWrite(t *testing.T) {
	for _, tc := range []struct {
		name   string
		plant  func(t *testing.T, repoPath string) string
		dryRun bool
	}{
		{"symlinked hook file", func(t *testing.T, repoPath string) string {
			return linkInRootFile(t, repoPath, claudeHookFile, "shared/settings.json", "{}\n")
		}, false},
		{"symlinked backup", func(t *testing.T, repoPath string) string {
			mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(geminiHookFile)), "{}\n")
			return linkInRootFile(t, repoPath, geminiHookFile+hookBackupExt, "shared/backup.json", "outside\n")
		}, false},
		{"malformed hook file", func(t *testing.T, repoPath string) string {
			path := filepath.Join(repoPath, ".codex", "hooks.json")
			mustWrite(t, path, `{"hooks": []}`)
			return path
		}, false},
		{"symlinked hook file dry run", func(t *testing.T, repoPath string) string {
			return linkInRootFile(t, repoPath, claudeHookFile, "shared/settings.json", "{}\n")
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repoPath := newTestRepo(t, "hook-preflight")
			planted := tc.plant(t, repoPath)
			plantedBytes := mustRead(t, planted)
			before := snapshotTree(t, repoPath)
			rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, DryRun: tc.dryRun})
			if err == nil || !strings.Contains(err.Error(), "agent-hooks preflight") {
				t.Fatalf("err = %v, want an agent-hooks preflight refusal", err)
			}
			if rep == nil || len(rep.CreatedFiles) != 0 || len(rep.ReconciledFiles) != 0 {
				t.Fatalf("adoption reported writes before the refusal: %+v", rep)
			}
			assertTreeUnchanged(t, before, snapshotTree(t, repoPath))
			if got := mustRead(t, planted); got != plantedBytes {
				t.Fatalf("written behind the refusal: %q", got)
			}
			for _, rel := range []string{manifestFile, agentsFile, "CLAUDE.md", claudeHookFile + hookBackupExt} {
				if _, err := os.Lstat(filepath.Join(repoPath, filepath.FromSlash(rel))); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("%s written before the refusal (lstat err=%v)", rel, err)
				}
			}
		})
	}
}

// Positive: a repository whose Gemini settings carry comments adopts; the settings stay as they
// were, and every other surface, the Claude hook file included, is written.
func TestAdopt_Positive_CommentedGeminiSettingsAdopt(t *testing.T) {
	repoPath := newTestRepo(t, "commented-gemini")
	settings := filepath.Join(repoPath, filepath.FromSlash(geminiHookFile))
	mustWrite(t, settings, commentedGeminiSettings)
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	assertNoIssues(t, rep)
	if got := mustRead(t, settings); got != commentedGeminiSettings {
		t.Fatalf("commented settings rewritten:\n%s", got)
	}
	if fileExists(settings + hookBackupExt) {
		t.Fatal("backup taken of a file left untouched")
	}
	if !hasAction(rep, geminiHookFile, actionSkip) || !contains(rep.CreatedFiles, claudeHookFile) {
		t.Fatalf("report: created %v, actions %+v", rep.CreatedFiles, rep.ActionDetails)
	}
}

// Boundary: the preflight writes nothing on a plain repository, and a declined agent-hooks step
// is not preflighted, so a symlinked hook file it would refuse does not stop adoption and nothing
// is written behind it.
func TestAdopt_Boundary_HookPreflightWritesNothingAndSkipsDeclinedStep(t *testing.T) {
	plain := t.TempDir()
	if err := preflightAgentHooks(context.Background(), plain); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(plain); err != nil || len(entries) != 0 {
		t.Fatalf("preflight wrote %v (err %v)", entries, err)
	}

	repoPath := newTestRepo(t, "declined-agent-hooks")
	mustWrite(t, filepath.Join(repoPath, manifestFile), "version: 1\nadoption:\n  decline: [agent-hooks]\n")
	real := linkInRootFile(t, repoPath, claudeHookFile, "shared/settings.json", "{}\n")
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	assertNoIssues(t, rep)
	if got := mustRead(t, real); got != "{}\n" {
		t.Fatalf("written behind the declined step's link: %q", got)
	}
}
