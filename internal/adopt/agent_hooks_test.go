package adopt

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/classify"
)

func TestReconcileAgentHooks(t *testing.T) {
	t.Run("NotApplicableClients", testAgentHooksNotApplicable)
	t.Run("RegistersSupportedClients", testAgentHooksRegistersSupported)
	t.Run("IdempotentRerun", testAgentHooksIdempotentRerun)
	t.Run("MergeAndBackup", testAgentHooksMergeAndBackup)
}

func testAgentHooksNotApplicable(t *testing.T) {
	repoDir := t.TempDir()
	opts := AdoptOptions{Path: repoDir, SkipGitValidation: true, DryRun: false}
	s := &adoptSession{
		repoPath: repoDir,
		report:   newAdoptionReport(repoDir, opts, classify.Result{Archetype: "app-service"}),
		opts:     opts,
	}

	err := reconcileAgentHooks(context.Background(), s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedNotApplicable := []string{"cursor", "windsurf", "copilot"}
	for _, expected := range expectedNotApplicable {
		found := false
		for _, action := range s.report.ActionDetails {
			if action.Path == expected && action.Action == "skip" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected client %q to be marked not applicable", expected)
		}
	}
}

func testAgentHooksRegistersSupported(t *testing.T) {
	repoDir := t.TempDir()
	opts := AdoptOptions{Path: repoDir, SkipGitValidation: true, DryRun: false}
	s := &adoptSession{
		repoPath: repoDir,
		report:   newAdoptionReport(repoDir, opts, classify.Result{Archetype: "app-service"}),
		opts:     opts,
	}

	configs := map[string]string{
		".claude/settings.json": `{"hooks": {"PreToolUse": [{"matcher": "^Bash$", "hooks": []}]}}`,
		".codex/hooks.json":     `{"hooks": {"PreToolUse": [{"matcher": "^Bash$", "hooks": []}]}}`,
		".gemini/settings.json": `{"hooks": {"BeforeTool": [{"matcher": "run_shell_command", "hooks": []}]}}`,
	}

	for path, content := range configs {
		full := filepath.Join(repoDir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	err := reconcileAgentHooks(context.Background(), s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	claudeData, err := os.ReadFile(filepath.Join(repoDir, filepath.FromSlash(".claude/settings.json")))
	if err != nil {
		t.Fatal(err)
	}
	if !bytesContains(claudeData, `"command": "praetorctl hook claude pre-tool"`) {
		t.Errorf("claude hook not registered. got: %s", string(claudeData))
	}
	if !bytesContains(claudeData, `"timeout": 15`) {
		t.Errorf("claude timeout not registered correctly. got: %s", string(claudeData))
	}

	codexData, err := os.ReadFile(filepath.Join(repoDir, filepath.FromSlash(".codex/hooks.json")))
	if err != nil {
		t.Fatal(err)
	}
	if !bytesContains(codexData, `"command": "praetorctl hook codex pre-tool"`) {
		t.Errorf("codex hook not registered. got: %s", string(codexData))
	}

	geminiData, err := os.ReadFile(filepath.Join(repoDir, filepath.FromSlash(".gemini/settings.json")))
	if err != nil {
		t.Fatal(err)
	}
	if !bytesContains(geminiData, `"command": "praetorctl hook gemini pre-tool"`) {
		t.Errorf("gemini hook not registered. got: %s", string(geminiData))
	}
	if !bytesContains(geminiData, `"timeout": 15000`) {
		t.Errorf("gemini timeout not registered correctly. got: %s", string(geminiData))
	}
}

func testAgentHooksIdempotentRerun(t *testing.T) {
	repoDir := t.TempDir()
	opts := AdoptOptions{Path: repoDir, SkipGitValidation: true, DryRun: false}
	s := &adoptSession{
		repoPath: repoDir,
		report:   newAdoptionReport(repoDir, opts, classify.Result{Archetype: "app-service"}),
		opts:     opts,
	}

	claudePath := filepath.Join(repoDir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(claudePath), 0755); err != nil {
		t.Fatal(err)
	}

	initialJSON := `{"hooks": {"PreToolUse": [{"matcher": "^Bash$", "hooks": [{"type": "command", "command": "praetorctl hook claude pre-tool", "timeout": 15}]}]}}`
	if err := os.WriteFile(claudePath, []byte(initialJSON), 0644); err != nil {
		t.Fatal(err)
	}

	err := reconcileAgentHooks(context.Background(), s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}

	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		t.Fatal("hooks field is not a map")
	}
	preToolUseList, ok := hooks["PreToolUse"].([]any)
	if !ok || len(preToolUseList) == 0 {
		t.Fatal("PreToolUse field is missing or empty")
	}
	preToolUse, ok := preToolUseList[0].(map[string]any)
	if !ok {
		t.Fatal("PreToolUse[0] is not a map")
	}
	commandHooks, ok := preToolUse["hooks"].([]any)
	if !ok {
		t.Fatal("hooks field is not a list")
	}

	if len(commandHooks) != 1 {
		t.Errorf("idempotent rerun failed: expected 1 hook, got %d", len(commandHooks))
	}
}

func testAgentHooksMergeAndBackup(t *testing.T) {
	repoDir := t.TempDir()
	opts := AdoptOptions{Path: repoDir, SkipGitValidation: true, DryRun: false}
	s := &adoptSession{
		repoPath: repoDir,
		report:   newAdoptionReport(repoDir, opts, classify.Result{Archetype: "app-service"}),
		opts:     opts,
	}

	claudePath := filepath.Join(repoDir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(claudePath), 0755); err != nil {
		t.Fatal(err)
	}

	initialJSON := `{"hooks": {"PreToolUse": [{"matcher": "^Bash$", "hooks": [{"type": "command", "command": "echo test"}]}]}}`
	if err := os.WriteFile(claudePath, []byte(initialJSON), 0644); err != nil {
		t.Fatal(err)
	}

	err := reconcileAgentHooks(context.Background(), s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	bakPath := claudePath + ".bak"
	if _, err := os.Stat(bakPath); os.IsNotExist(err) {
		t.Errorf("backup file %s was not created", bakPath)
	}

	bakData, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(bakData) != initialJSON {
		t.Errorf("backup file content mismatch")
	}

	data, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}

	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		t.Fatal("hooks field is not a map")
	}
	preToolUseList, ok := hooks["PreToolUse"].([]any)
	if !ok || len(preToolUseList) == 0 {
		t.Fatal("PreToolUse field is missing or empty")
	}
	preToolUse, ok := preToolUseList[0].(map[string]any)
	if !ok {
		t.Fatal("PreToolUse[0] is not a map")
	}
	commandHooks, ok := preToolUse["hooks"].([]any)
	if !ok {
		t.Fatal("hooks field is not a list")
	}

	if len(commandHooks) != 2 {
		t.Fatalf("merge failed: expected 2 hooks, got %d", len(commandHooks))
	}

	firstHook, ok := commandHooks[0].(map[string]any)
	if !ok {
		t.Fatal("commandHooks[0] is not a map")
	}
	if firstCmd := stringField(firstHook, "command"); firstCmd != "echo test" {
		t.Errorf("existing hook was corrupted: expected 'echo test', got %s", firstCmd)
	}

	secondHook, ok := commandHooks[1].(map[string]any)
	if !ok {
		t.Fatal("commandHooks[1] is not a map")
	}
	if secondCmd := stringField(secondHook, "command"); secondCmd != "praetorctl hook claude pre-tool" {
		t.Errorf("new hook was not appended correctly")
	}
}

func bytesContains(b []byte, s string) bool {
	return bytes.Contains(b, []byte(s))
}
