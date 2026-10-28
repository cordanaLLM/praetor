package agenthook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/router"
	"github.com/cordanaLLM/praetor/internal/state"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

func claudeOutcomeRepository(t *testing.T) string {
	t.Helper()
	root := claudeRouteRepository(t)
	if err := os.MkdirAll(filepath.Join(root, state.WorkingDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

func claudeAsyncLaunchPayload(t *testing.T, resolvedModel string) []byte {
	t.Helper()
	resp := map[string]any{"status": "async_launched", "agentId": "agent"}
	if resolvedModel != "" {
		resp["resolvedModel"] = resolvedModel
	}
	return commandPayload(t, map[string]any{
		"hook_event_name": "PostToolUse", "session_id": "s", "tool_name": "Agent", "tool_use_id": "t",
		"tool_input": map[string]any{"prompt": validBrief, "run_in_background": true}, "tool_response": resp,
	})
}

func claudeSubagentStopPayload(t *testing.T, cwd, transcriptPath, message string) []byte {
	t.Helper()
	fields := map[string]any{
		"hook_event_name": "SubagentStop", "session_id": "s", "agent_id": "agent",
		"agent_type": "general-purpose", "last_assistant_message": message,
	}
	if cwd != "" {
		fields["cwd"] = cwd
	}
	if transcriptPath != "" {
		fields["agent_transcript_path"] = transcriptPath
	}
	return commandPayload(t, fields)
}

func TestClaudeBackgroundReceiptLifecycle(t *testing.T) {
	for _, word := range []string{"pass", "fail", "partial", "unknown"} {
		t.Run(word, func(t *testing.T) {
			root, storeDir := claudeOutcomeRepository(t), t.TempDir()
			transcript := plantClaudeTranscript(t, plantedClaudeUsage)
			pre := claudeDispatch(t, "s", "t", validBrief, nil)
			requireOutcome(t, "pre-dispatch", runAgentHook(t, root, storeDir, "claude", EventPreDispatch, pre), 0, "")
			launch := claudeAsyncLaunchPayload(t, "claude-haiku-4-5")
			requireOutcome(t, "launch", runAgentHook(t, root, storeDir, "claude", EventDispatchReceipt, launch), 0, "")
			if _, err := os.Stat(filepath.Join(root, router.DefaultOutcomeLogPath)); !os.IsNotExist(err) {
				t.Fatalf("launch receipt wrote outcome early: %v", err)
			}
			report := strings.Replace(validReturn, "verdict: pass", "verdict: "+word, 1)
			requireOutcome(t, "pre-handback", runAgentHook(t, root, storeDir, "claude", EventPreHandback, handbackEventPayload(t, "PreToolUse", "s", "agent", report)), 0, "")
			requireOutcome(t, "handback-receipt", runAgentHook(t, root, storeDir, "claude", EventHandbackReceipt, handbackEventPayload(t, "PostToolUse", "s", "agent", report)), 0, "")
			stop := claudeSubagentStopPayload(t, root, transcript, "Report delivered through SubagentHandback.")
			requireOutcome(t, "subagent-stop", runAgentHook(t, root, storeDir, "claude", EventPostReturn, stop), 0, "")
			requireStoreRows(t, storeDir, 0)
			// A replay after SubagentStop released the correlation is unowned and writes no second row.
			requireOutcome(t, "replay", runAgentHook(t, root, storeDir, "claude", EventPostReturn, stop), 0, "no Praetor-owned dispatch")
			assertClaudeReceiptRow(t, root, word)
		})
	}
}

func assertClaudeReceiptRow(t *testing.T, root, word string) {
	t.Helper()
	rows, err := router.ReadOutcomes(t.Context(), filepath.Join(root, router.DefaultOutcomeLogPath))
	if err != nil || len(rows) != 1 {
		t.Fatalf("receipt count: %+v %v", rows, err)
	}
	want := router.OutcomeFail
	if word == "pass" {
		want = router.OutcomeOK
	}
	row := rows[0]
	if row.Result != want || row.Tokens != 98 || row.Target != "haiku" || row.Effort != "low" || row.ResolvedModel != "claude-haiku-4-5" {
		t.Fatalf("receipt row: %+v", row)
	}
}

func TestClaudeOutcomeNoHandbackAndUnlabeled(t *testing.T) {
	root, storeDir := claudeOutcomeRepository(t), t.TempDir()
	transcript := plantClaudeTranscript(t, plantedClaudeUsage)
	requireOutcome(t, "pre", runAgentHook(t, root, storeDir, "claude", EventPreDispatch, claudeDispatch(t, "s", "t", validBrief, nil)), 0, "")
	requireOutcome(t, "launch", runAgentHook(t, root, storeDir, "claude", EventDispatchReceipt, claudeAsyncLaunchPayload(t, "")), 0, "")
	stop := claudeSubagentStopPayload(t, root, transcript, validReturn)
	requireOutcome(t, "no-handback stop", runAgentHook(t, root, storeDir, "claude", EventPostReturn, stop), 0, "")
	assertNoHandbackOutcomeRow(t, root)
	plantUnlabeledActiveAgent(t, root, storeDir)
	requireOutcome(t, "unlabeled stop", runAgentHook(t, root, storeDir, "claude", EventPostReturn, stop), 0, "")
	if rows, err := router.ReadOutcomes(t.Context(), filepath.Join(root, router.DefaultOutcomeLogPath)); err != nil || len(rows) != 1 {
		t.Fatalf("unlabeled wrote row: %+v %v", rows, err)
	}
}

func assertNoHandbackOutcomeRow(t *testing.T, root string) {
	t.Helper()
	rows, err := router.ReadOutcomes(t.Context(), filepath.Join(root, router.DefaultOutcomeLogPath))
	if err != nil || len(rows) != 1 {
		t.Fatalf("no-handback count: %+v %v", rows, err)
	}
	// A return without SubagentHandback is validated at SubagentStop; its verdict field counts.
	row := rows[0]
	if row.Result != router.OutcomeOK || row.Note != "" || row.ResolvedModel != "none" || row.Tokens != 98 {
		t.Fatalf("no-handback row: %+v", row)
	}
}

func plantUnlabeledActiveAgent(t *testing.T, root, storeDir string) {
	t.Helper()
	store, err := newCorrelationStore(t.Context(), root, storeDir)
	if err != nil {
		t.Fatal(err)
	}
	res := config.Resolution{Register: config.TextRegisterInternal, Source: "surfaces.agent", ManifestSHA256: digestOf("m")}
	if err := store.reserve(t.Context(), "claude", "s", "unlabeled", res, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.promote(t.Context(), "claude", "s", "unlabeled", "agent"); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeOutcomeRejectedTranscript(t *testing.T) {
	transcript := plantClaudeTranscript(t, plantedClaudeUsage)
	outside := filepath.Join(filepath.Dir(filepath.Dir(transcript)), "agent-agent.jsonl")
	if err := os.WriteFile(outside, []byte(plantedClaudeUsage), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{filepath.Join(filepath.Dir(transcript), "agent-other.jsonl"), outside} {
		root, storeDir := claudeOutcomeRepository(t), t.TempDir()
		requireOutcome(t, "pre", runAgentHook(t, root, storeDir, "claude", EventPreDispatch, claudeDispatch(t, "s", "t", validBrief, nil)), 0, "")
		requireOutcome(t, "launch", runAgentHook(t, root, storeDir, "claude", EventDispatchReceipt, claudeAsyncLaunchPayload(t, "claude-haiku-4-5")), 0, "")
		requireOutcome(t, "rejected stop", runAgentHook(t, root, storeDir, "claude", EventPostReturn, claudeSubagentStopPayload(t, root, bad, validReturn)), 0, "")
		rows, err := router.ReadOutcomes(t.Context(), filepath.Join(root, router.DefaultOutcomeLogPath))
		if err != nil || len(rows) != 1 || rows[0].Tokens != 0 || !strings.Contains(rows[0].Note, "tokens unread") {
			t.Fatalf("rejected transcript (%s) row: %+v %v", bad, rows, err)
		}
	}
}

func createLinkedWorktree(t *testing.T, root string) string {
	t.Helper()
	ctx, err := util.WithCommandEnvironment(t.Context(), testsupport.HermeticGitEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := util.RunGit(ctx, root, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := util.RunGit(ctx, root, "commit", "-q", "-m", "fixture"); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(t.TempDir(), "linked-worktree")
	if _, err := util.RunGit(ctx, root, "worktree", "add", "--detach", child, "HEAD"); err != nil {
		t.Fatal(err)
	}
	return child
}

func TestClaudeOutcomeLinkedWorktreeAndResolverError(t *testing.T) {
	root := claudeOutcomeRepository(t)
	child := createLinkedWorktree(t, root)
	storeDir, transcript := t.TempDir(), plantClaudeTranscript(t, plantedClaudeUsage)
	requireOutcome(t, "pre", runAgentHook(t, child, storeDir, "claude", EventPreDispatch, claudeDispatch(t, "s", "t", validBrief, nil)), 0, "")
	requireOutcome(t, "launch", runAgentHook(t, child, storeDir, "claude", EventDispatchReceipt, claudeAsyncLaunchPayload(t, "claude-haiku-4-5")), 0, "")
	requireOutcome(t, "stop", runAgentHook(t, child, storeDir, "claude", EventPostReturn, claudeSubagentStopPayload(t, child, transcript, validReturn)), 0, "")
	if _, err := os.Stat(filepath.Join(child, state.WorkingDirName)); !os.IsNotExist(err) {
		t.Fatalf("linked worktree created own state folder: %v", err)
	}
	if rows, err := router.ReadOutcomes(t.Context(), filepath.Join(root, router.DefaultOutcomeLogPath)); err != nil || len(rows) != 1 {
		t.Fatalf("main worktree outcome missing: %+v %v", rows, err)
	}

	// Resolver error (no state folder) leaves SubagentStop verdict Allow and reports Notice on stderr.
	noStateRoot := claudeRouteRepository(t)
	requireOutcome(t, "pre-nostate", runAgentHook(t, noStateRoot, storeDir, "claude", EventPreDispatch, claudeDispatch(t, "s", "t", validBrief, nil)), 0, "")
	requireOutcome(t, "launch-nostate", runAgentHook(t, noStateRoot, storeDir, "claude", EventDispatchReceipt, claudeAsyncLaunchPayload(t, "claude-haiku-4-5")), 0, "")
	requireOutcome(t, "stop-nostate", runAgentHook(t, noStateRoot, storeDir, "claude", EventPostReturn, claudeSubagentStopPayload(t, noStateRoot, transcript, validReturn)), 0, "Claude outcome append failed: resolve outcome state directory")
	if _, err := os.Stat(filepath.Join(noStateRoot, state.WorkingDirName)); !os.IsNotExist(err) {
		t.Fatalf("resolver error created state folder: %v", err)
	}
	requireStoreRows(t, storeDir, 0)
}

func TestClaudeForegroundRefusedRegression(t *testing.T) {
	root, storeDir := claudeOutcomeRepository(t), t.TempDir()
	fg := claudeDispatch(t, "s", "t", validBrief, map[string]any{"run_in_background": false})
	requireOutcome(t, "foreground dispatch", runAgentHook(t, root, storeDir, "claude", EventPreDispatch, fg), 2,
		"tool_input.run_in_background must not be false: foreground return precedes correlation")
	completed := commandPayload(t, map[string]any{
		"hook_event_name": "PostToolUse", "session_id": "s", "tool_name": "Agent", "tool_use_id": "t",
		"tool_input":    map[string]any{"prompt": validBrief, "run_in_background": true},
		"tool_response": map[string]any{"status": "completed", "agentId": "agent"},
	})
	requireOutcome(t, "completed receipt", runAgentHook(t, root, storeDir, "claude", EventDispatchReceipt, completed), 2,
		"tool_response.status must be async_launched")
}
