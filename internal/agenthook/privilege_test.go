// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

func privilegePayload(t *testing.T, client, root, command string) []byte {
	t.Helper()
	if client == "agy" {
		return commandPayload(t, map[string]any{
			"workspacePaths": []string{root}, "stepIdx": 1,
			"toolCall": map[string]any{"name": "run_command", "args": map[string]string{"CommandLine": command}},
		})
	}
	tool, event := "Bash", "PreToolUse"
	if client == "gemini" {
		tool, event = "run_shell_command", "BeforeTool"
	}
	return commandPayload(t, map[string]any{
		"cwd": root, "tool_name": tool, "hook_event_name": event,
		"tool_input": map[string]string{"command": command},
	})
}

// Replay the same positive, negative and boundary fixtures through each client's
// registered Go entry point. Antigravity denies in stdout, with exit zero.
func TestPolicyPrivilegePerClientHook(t *testing.T) {
	root := repository(t, true)
	for _, client := range []string{"claude", "gemini", "codex", "agy"} {
		t.Run(client, func(t *testing.T) {
			replayPrivilegeClient(t, client, root)
		})
	}
}

func replayPrivilegeClient(t *testing.T, client, root string) {
	t.Helper()
	if _, err := ParseArguments(client, "pre-tool"); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range loadCases(t) {
		if !strings.HasPrefix(fixture.Name, "privilege-") {
			continue
		}
		var input struct {
			ToolInput struct{ Command string } `json:"tool_input"`
		}
		if err := json.Unmarshal(fixture.Payload, &input); err != nil {
			t.Fatal(err)
		}
		response := serve(t, client, "pre-tool", root, privilegePayload(t, client, root, input.ToolInput.Command))
		checkPrivilegeResponse(t, client, fixture, response)
	}
}

func checkPrivilegeResponse(t *testing.T, client string, fixture fixtureCase, response Response) {
	t.Helper()
	denied := response.ExitCode == 2
	if client == "agy" {
		denied = bytes.Contains(response.Stdout, []byte(`"decision":"deny"`))
	}
	if denied == fixture.Allow || (fixture.Allow && response.ExitCode != 0) ||
		(!fixture.Allow && !bytes.Contains(append(response.Stdout, response.Stderr...), []byte(evasionMessage))) {
		t.Errorf("%s: allow %v, response %+v", fixture.Name, fixture.Allow, response)
	}
}

// Exercise the actual compatibility adapters and their real shared Lefthook job,
// rather than a stub that could refuse without ever reading the rule.
func TestPolicyPrivilegeNativePythonHooks(t *testing.T) {
	if _, err := exec.LookPath("lefthook"); err != nil {
		t.Skip("the native Python guards run the shared Lefthook job and lefthook is not on PATH; CI installs it (HISS-21)")
	}
	python := testsupport.PythonInterpreter(t)
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, client := range []string{"claude", "gemini", "codex"} {
		script := "command_guard.py"
		if client == "codex" {
			script = "codex_pre_tool.py"
		}
		for _, command := range []string{"sudo -n true", "grep -n sudo file"} {
			result := runGuardScript(t, python, filepath.Join(root, ".config", "agent", "hooks", script),
				privilegePayload(t, client, root, command), nil, guardEnvironment())
			wantDeny := command == "sudo -n true"
			if (result.exit == 2) != wantDeny || (!wantDeny && result.exit != 0) ||
				(wantDeny && !bytes.Contains(result.stderr, []byte(evasionMessage))) {
				t.Errorf("%s %q: exit %d, stderr %s", client, command, result.exit, result.stderr)
			}
		}
	}
}

func TestRegistrationPrivilegeAgyPlugin(t *testing.T) {
	var plugins map[string]map[string][]nativeGroup
	readTrackedJSON(t, ".agents/plugins/praetor/hooks.json", &plugins)
	row, err := ParseArguments("agy", "pre-tool")
	if err != nil || !groupsHold(plugins["praetor-command-policy"]["PreToolUse"], row, time.Second) {
		t.Fatalf("Antigravity command policy not registered: row %+v, error %v", row, err)
	}
}

// The act judge must refuse escalators itself, even if the text rule is absent.
// Light escalated commands would pass if escalators were transparent wrappers.
func TestAdmissionPrivilegePrograms(t *testing.T) {
	check := admissionPolicy(t).admission
	for command, deny := range map[string]bool{
		"sudo -n true": true, "doas true": true, "pkexec true": true, "run0 true": true,
		"su -c true": true, "su - root -c id": true, "su root --command id": true,
		"env A=1 timeout 5 /usr/bin/sudo true": true, "builtin doas true": true,
		"echo x && sudo true": true, "echo $(pkexec true)": true,
		"grep -n sudo file": false, `echo "sudo is refused"`: false,
		"su": false, "su - root": false, "sudoers true": false,
	} {
		got := check.judge(command)
		if (got.Outcome == Deny) != deny || (deny && !strings.Contains(got.Reason, evasionMessage+"; privilege escalation prohibited")) {
			t.Errorf("%q: %+v, want deny %v", command, got, deny)
		}
	}
}

func TestRegistrationPrivilegeOpenCodeGap(t *testing.T) {
	if _, err := ParseArguments("opencode", "pre-tool"); err == nil {
		t.Fatal("OpenCode now serves pre-tool: extend privilege replay and update the documented gap")
	}
}
