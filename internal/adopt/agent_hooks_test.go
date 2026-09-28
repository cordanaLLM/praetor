package adopt

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/classify"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

const claudeHookFile = ".claude/settings.json"

// hookSession is an adoption session over an empty directory: no manifest, so agent_clients is
// absent and every client is selected.
func hookSession(t *testing.T, dryRun bool) *adoptSession {
	t.Helper()
	repoDir := t.TempDir()
	opts := AdoptOptions{Path: repoDir, SkipGitValidation: true, DryRun: dryRun}
	return &adoptSession{
		repoPath:    repoDir,
		report:      newAdoptionReport(repoDir, opts, classify.Result{Archetype: "app-service"}),
		opts:        opts,
		backupStamp: testBackupStamp,
	}
}

func hookPath(s *adoptSession, rel string) string {
	return filepath.Join(s.repoPath, filepath.FromSlash(rel))
}

func actionOf(rep *AdoptReport, path string) (ActionDetail, bool) {
	for _, action := range rep.ActionDetails {
		if action.Path == path {
			return action, true
		}
	}
	return ActionDetail{}, false
}

// registeredHandlers returns every handler registered under event and matcher in a hook file.
func registeredHandlers(t *testing.T, raw []byte, event, matcher string) []map[string]any {
	t.Helper()
	var doc struct {
		Hooks map[string][]struct {
			Matcher string           `json:"matcher"`
			Hooks   []map[string]any `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("hook file does not parse: %v\n%s", err, raw)
	}
	var handlers []map[string]any
	for _, group := range doc.Hooks[event] {
		if group.Matcher == matcher {
			handlers = append(handlers, group.Hooks...)
		}
	}
	return handlers
}

func requireHandler(t *testing.T, raw []byte, event, matcher, command string, timeout float64) {
	t.Helper()
	for _, handler := range registeredHandlers(t, raw, event, matcher) {
		if handler["command"] == command && handler["type"] == "command" && handler["timeout"] == timeout {
			return
		}
	}
	t.Fatalf("no %q handler (timeout %v) under %s/%s:\n%s", command, timeout, event, matcher, raw)
}

// Positive: a repository without any hook file gets each native client's file created, holding
// the engine's pre-tool call under the registration row's event, matcher and timeout unit; the
// context-only clients and AGY are not applicable, and nothing warns.
func TestReconcileAgentHooks_Positive_CreatesMissingHookFiles(t *testing.T) {
	s := hookSession(t, false)
	if err := reconcileAgentHooks(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct{ rel, event, matcher, command string }{
		{claudeHookFile, "PreToolUse", "^Bash$", "praetorctl hook claude pre-tool"},
		{".codex/hooks.json", "PreToolUse", "^Bash$", "praetorctl hook codex pre-tool"},
	} {
		requireHandler(t, []byte(mustRead(t, hookPath(s, want.rel))), want.event, want.matcher, want.command, 15)
		if action, ok := actionOf(s.report, want.rel); !ok || action.Action != actionCreate {
			t.Errorf("%s: report %+v", want.rel, action)
		}
	}
	gemini := []byte(mustRead(t, hookPath(s, ".gemini/settings.json")))
	requireHandler(t, gemini, "BeforeTool", "run_shell_command", "praetorctl hook gemini pre-tool", 15000)
	for _, client := range []string{"cursor", "copilot", "windsurf", "agy"} {
		if action, ok := actionOf(s.report, client); !ok || action.Action != actionSkip {
			t.Errorf("%s not reported as not applicable: %+v", client, s.report.ActionDetails)
		}
	}
	if len(s.report.Warnings) != 0 || len(s.report.Errors) != 0 {
		t.Fatalf("warnings %v, errors %v", s.report.Warnings, s.report.Errors)
	}
}

// Positive: an existing file is merged, not rewritten: members keep their order and literals
// (a 20-digit integer, 1.50), a group whose matcher only resembles the row's (^Bash, every tool
// whose name starts with Bash) is left alone, and the row's own group is created. No copy lands
// beside the file (BUG-1026); outside a git work tree nothing confirms the backup root is
// ignored, so the report says there is no backup.
func TestReconcileAgentHooks_Positive_MergesPreservingForeignContent(t *testing.T) {
	s := hookSession(t, false)
	original := `{"zeta": 12345678901234567890, "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "stop.sh"}]}],` +
		` "PreToolUse": [{"matcher": "^Bash", "hooks": [{"type": "command", "command": "lint.sh"}]}]}, "alpha": {"ratio": 1.50}}` + "\n"
	mustWrite(t, hookPath(s, claudeHookFile), original)
	if err := reconcileAgentHooks(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	merged := mustRead(t, hookPath(s, claudeHookFile))
	requireHandler(t, []byte(merged), "PreToolUse", "^Bash$", "praetorctl hook claude pre-tool", 15)
	if got := registeredHandlers(t, []byte(merged), "PreToolUse", "^Bash"); len(got) != 1 || got[0]["command"] != "lint.sh" {
		t.Errorf("foreign matcher group changed: %v", got)
	}
	order := []string{`"zeta": 12345678901234567890`, `"hooks"`, `"Stop"`, `"PreToolUse"`, `"alpha"`, `"ratio": 1.50`}
	for i := 1; i < len(order); i++ {
		if !strings.Contains(merged, order[i-1]) || strings.Index(merged, order[i-1]) > strings.Index(merged, order[i]) {
			t.Fatalf("member order or literal lost (%s before %s):\n%s", order[i-1], order[i], merged)
		}
	}
	if fileExists(hookPath(s, claudeHookFile+hookBackupExt)) {
		t.Fatal("backup written beside the hook file")
	}
	if action, ok := actionOf(s.report, claudeHookFile); !ok || action.Action != actionMerge || !strings.Contains(action.Details, "; no backup: ") {
		t.Errorf("merge not reported: %+v", s.report.ActionDetails)
	}
	if len(s.report.CreatedFiles) != 2 || contains(s.report.CreatedFiles, claudeHookFile+hookBackupExt) {
		t.Errorf("created = %v, want the two absent client files alone", s.report.CreatedFiles)
	}
}

// Positive: the handler joins an existing group whose matcher equals the row's, next to the
// handlers already there, instead of opening a second group for the same tool.
func TestReconcileAgentHooks_Positive_JoinsExactMatcherGroup(t *testing.T) {
	s := hookSession(t, false)
	mustWrite(t, hookPath(s, claudeHookFile), `{"hooks": {"PreToolUse": [{"matcher": "^Bash$", "hooks": [{"type": "command", "command": "audit.sh"}]}]}}`)
	if err := reconcileAgentHooks(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	handlers := registeredHandlers(t, []byte(mustRead(t, hookPath(s, claudeHookFile))), "PreToolUse", "^Bash$")
	if len(handlers) != 2 || handlers[0]["command"] != "audit.sh" || handlers[1]["command"] != "praetorctl hook claude pre-tool" {
		t.Fatalf("handlers = %v", handlers)
	}
}

// Positive and negative, per client: Claude Code and Codex read the literal Bash as the exact
// tool ^Bash$ selects, so an adopter group Bash that runs the adopted interceptor serves the
// row and the file stays byte for byte as it was, and a Bash group without it takes the
// engine call instead of a second group. Gemini CLI tests every matcher as an unanchored
// regular expression, so its ^run_shell_command$ group selects less than the row's
// run_shell_command and the row still gets its own group.
func TestReconcileAgentHooks_LiteralMatcherPerClient(t *testing.T) {
	adapter := `{"type": "command", "command": "python3", "args": [".config/agent/hooks/block_evasion.py"]}`
	s := hookSession(t, false)
	served := `{"hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [` + adapter + `]}]}}`
	joined := `{"hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "lint.sh"}]}]}}`
	gemini := `{"hooks": {"BeforeTool": [{"matcher": "^run_shell_command$", "hooks": [` + adapter + `]}]}}`
	mustWrite(t, hookPath(s, claudeHookFile), served)
	mustWrite(t, hookPath(s, ".codex/hooks.json"), joined)
	mustWrite(t, hookPath(s, ".gemini/settings.json"), gemini)
	if err := reconcileAgentHooks(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, hookPath(s, claudeHookFile)); got != served {
		t.Fatalf("served Claude Code file rewritten:\n%s", got)
	}
	if action, ok := actionOf(s.report, claudeHookFile); !ok || action.Action != actionReconcile || !strings.Contains(action.Details, "block_evasion.py") {
		t.Fatalf("claude report = %+v", action)
	}
	codex := []byte(mustRead(t, hookPath(s, ".codex/hooks.json")))
	if handlers := registeredHandlers(t, codex, "PreToolUse", "Bash"); len(handlers) != 2 || handlers[1]["command"] != "praetorctl hook codex pre-tool" {
		t.Fatalf("codex handler not joined to the Bash group:\n%s", codex)
	}
	if got := registeredHandlers(t, codex, "PreToolUse", "^Bash$"); len(got) != 0 {
		t.Fatalf("codex got a second group for Bash:\n%s", codex)
	}
	requireHandler(t, []byte(mustRead(t, hookPath(s, ".gemini/settings.json"))), "BeforeTool", "run_shell_command", "praetorctl hook gemini pre-tool", 15000)
}

// Positive: a repository that already runs the evaluator, through the engine call, the skew
// guard or a replaced Python adapter, is left byte for byte alone and reported as registered.
func TestReconcileAgentHooks_Positive_ExistingEvaluatorIsNotDuplicated(t *testing.T) {
	s := hookSession(t, false)
	existing := `{"hooks": {"PreToolUse": [{"matcher": "^Bash$", "hooks": [{"type": "command", "command": "python3",` +
		` "args": ["-B", "${CLAUDE_PROJECT_DIR}/.config/agent/hooks/command_guard.py"], "timeout": 15}]}]}}`
	mustWrite(t, hookPath(s, claudeHookFile), existing)
	if err := reconcileAgentHooks(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, hookPath(s, claudeHookFile)); got != existing {
		t.Fatalf("served file rewritten:\n%s", got)
	}
	if fileExists(hookPath(s, claudeHookFile+hookBackupExt)) {
		t.Fatal("unchanged file was backed up")
	}
	action, ok := actionOf(s.report, claudeHookFile)
	if !ok || action.Action != actionReconcile || !strings.Contains(action.Details, "command_guard.py") {
		t.Fatalf("report = %+v", action)
	}
}

// Positive: agent_clients limits registration to the selected clients; unselected native
// clients get no file and a not-applicable entry under their hook file, and a selected
// context-only client is reported as having no hook file.
func TestReconcileAgentHooks_Positive_HonoursAgentClientsSelection(t *testing.T) {
	s := hookSession(t, false)
	mustWrite(t, filepath.Join(s.repoPath, manifestFile), "version: 1\nagent_clients: [claude, cursor]\n")
	if err := reconcileAgentHooks(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if !fileExists(hookPath(s, claudeHookFile)) {
		t.Fatal("selected client not registered")
	}
	for _, rel := range []string{".codex/hooks.json", ".gemini/settings.json"} {
		if fileExists(hookPath(s, rel)) {
			t.Errorf("unselected %s written", rel)
		}
		if action, ok := actionOf(s.report, rel); !ok || !strings.Contains(action.Details, "Not selected") {
			t.Errorf("unselected %s not reported: %+v", rel, action)
		}
	}
	if action, ok := actionOf(s.report, "cursor"); !ok || !strings.Contains(action.Details, "No pre-tool row") {
		t.Errorf("selected cursor = %+v", action)
	}
}

// Negative: a hook file that is not one JSON object, or whose hooks section or event list has
// the wrong type, fails the step and is left untouched without a backup.
func TestReconcileAgentHooks_Negative_MalformedFilesFailUntouched(t *testing.T) {
	for name, content := range map[string]string{
		"not json":       `{"hooks": `,
		"array root":     `[]`,
		"hooks array":    `{"hooks": []}`,
		"event object":   `{"hooks": {"PreToolUse": {}}}`,
		"event null":     `{"hooks": {"PreToolUse": null}}`,
		"duplicate name": `{"hooks": {}, "hooks": {}}`,
	} {
		s := hookSession(t, false)
		mustWrite(t, hookPath(s, claudeHookFile), content)
		if err := reconcileAgentHooks(context.Background(), s); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if got := mustRead(t, hookPath(s, claudeHookFile)); got != content {
			t.Errorf("%s: file changed to %q", name, got)
		}
		if fileExists(hookPath(s, claudeHookFile+hookBackupExt)) {
			t.Errorf("%s: backup written", name)
		}
	}
}

// Negative: an unknown agent_clients id fails the step before any file is written.
func TestReconcileAgentHooks_Negative_UnknownClientSelection(t *testing.T) {
	s := hookSession(t, false)
	mustWrite(t, filepath.Join(s.repoPath, manifestFile), "version: 1\nagent_clients: [claude, vim]\n")
	if err := reconcileAgentHooks(context.Background(), s); err == nil || !strings.Contains(err.Error(), "vim") {
		t.Fatalf("err = %v, want one naming vim", err)
	}
	if fileExists(hookPath(s, claudeHookFile)) {
		t.Fatal("hook file written despite the invalid selection")
	}
}

// Negative: the former backup path, <file>.bak, is never written again: a symlink planted
// there out of the repository keeps its target's bytes while the merge goes ahead, and the
// report names the leftover.
func TestReconcileAgentHooks_Negative_FormerBackupPathNotWrittenThrough(t *testing.T) {
	s := hookSession(t, false)
	mustWrite(t, hookPath(s, claudeHookFile), `{"hooks": {}}`)
	victim := filepath.Join(t.TempDir(), "victim.json")
	mustWrite(t, victim, "outside\n")
	if err := os.Symlink(victim, hookPath(s, claudeHookFile+hookBackupExt)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := reconcileAgentHooks(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, victim); got != "outside\n" {
		t.Fatalf("symlink target written: %q", got)
	}
	requireHandler(t, []byte(mustRead(t, hookPath(s, claudeHookFile))), "PreToolUse", "^Bash$", "praetorctl hook claude pre-tool", 15)
	if !strings.HasPrefix(strings.Join(s.report.Warnings, "\n"), claudeHookFile+hookBackupExt+": ") {
		t.Fatalf("leftover not reported: %v", s.report.Warnings)
	}
}

// Negative: a FIFO planted at a hook file is refused within the deadline instead of blocking
// adoption in open(2) (BUG-822): the confined read checks the file type before it opens it.
func TestReconcileAgentHooks_Negative_FIFORefused(t *testing.T) {
	s := hookSession(t, true)
	if err := os.MkdirAll(filepath.Dir(hookPath(s, claudeHookFile)), 0o755); err != nil {
		t.Fatal(err)
	}
	testsupport.MakeFIFO(t, hookPath(s, claudeHookFile))
	err := testsupport.RunWithin(t, 10*time.Second, func() error {
		return reconcileAgentHooks(context.Background(), s)
	})
	if err == nil || !strings.Contains(err.Error(), "must be regular") {
		t.Fatalf("FIFO hook file = %v, want a refusal of the non-regular file", err)
	}
}

// Boundary: a second run over the first run's output changes nothing, byte for byte, takes no
// backup, and reports each client as already registered.
func TestReconcileAgentHooks_Boundary_RerunIsIdempotent(t *testing.T) {
	s := hookSession(t, false)
	if err := reconcileAgentHooks(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	first := mustRead(t, hookPath(s, claudeHookFile))
	rerun := &adoptSession{repoPath: s.repoPath, opts: s.opts,
		report: newAdoptionReport(s.repoPath, s.opts, classify.Result{Archetype: "app-service"})}
	if err := reconcileAgentHooks(context.Background(), rerun); err != nil {
		t.Fatal(err)
	}
	if second := mustRead(t, hookPath(s, claudeHookFile)); second != first {
		t.Fatalf("rerun changed the file:\n%s\n---\n%s", first, second)
	}
	if fileExists(hookPath(s, claudeHookFile+hookBackupExt)) {
		t.Fatal("rerun took a backup")
	}
	action, ok := actionOf(rerun.report, claudeHookFile)
	if !ok || action.Action != actionReconcile || !strings.Contains(action.Details, "praetorctl hook claude pre-tool") {
		t.Fatalf("rerun report = %+v", action)
	}
}

// Boundary: a dry run writes nothing and reports exactly what the run it previews records.
func TestReconcileAgentHooks_Boundary_DryRunReportsTheApplyPlan(t *testing.T) {
	existing := `{"hooks": {"Stop": []}}`
	preview, apply := hookSession(t, true), hookSession(t, false)
	for _, s := range []*adoptSession{preview, apply} {
		mustWrite(t, hookPath(s, claudeHookFile), existing)
		if err := reconcileAgentHooks(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(preview.report.ActionDetails, apply.report.ActionDetails) {
		t.Fatalf("dry run %+v\napply   %+v", preview.report.ActionDetails, apply.report.ActionDetails)
	}
	if got := mustRead(t, hookPath(preview, claudeHookFile)); got != existing {
		t.Fatalf("dry run wrote %q", got)
	}
	for _, rel := range []string{claudeHookFile + hookBackupExt, ".codex/hooks.json", ".gemini/settings.json"} {
		if fileExists(hookPath(preview, rel)) {
			t.Errorf("dry run created %s", rel)
		}
	}
}

// Boundary: an empty agent_clients selection registers nothing and reports every client, and an
// existing empty hook file is a document to fill, not a malformed one.
func TestReconcileAgentHooks_Boundary_EmptySelectionAndEmptyFile(t *testing.T) {
	none := hookSession(t, false)
	mustWrite(t, filepath.Join(none.repoPath, manifestFile), "version: 1\nagent_clients: []\n")
	if err := reconcileAgentHooks(context.Background(), none); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{claudeHookFile, ".codex/hooks.json", ".gemini/settings.json"} {
		if fileExists(hookPath(none, rel)) {
			t.Errorf("empty selection wrote %s", rel)
		}
	}
	if got, want := len(none.report.ActionDetails), 7; got != want {
		t.Errorf("empty selection recorded %d entries, want %d: %+v", got, want, none.report.ActionDetails)
	}

	empty := hookSession(t, false)
	mustWrite(t, hookPath(empty, claudeHookFile), "")
	if err := reconcileAgentHooks(context.Background(), empty); err != nil {
		t.Fatal(err)
	}
	requireHandler(t, []byte(mustRead(t, hookPath(empty, claudeHookFile))), "PreToolUse", "^Bash$", "praetorctl hook claude pre-tool", 15)
}
