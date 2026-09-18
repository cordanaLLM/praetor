package agenthook

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var preToolClients = []string{"claude", "codex", "gemini", "lefthook"}

func denyExit(client string) int {
	if client == "lefthook" {
		return 1
	}
	return 2
}

func TestRunGoldensPerDialect(t *testing.T) {
	root := repository(t, true)
	allowed := []byte(`{"tool_input":{"command":"git status"}}`)
	denied := []byte(`{"tool_input":{"command":"git commit --no-verify"}}`)
	for _, client := range preToolClients {
		allow := serve(t, client, "pre-tool", root, allowed)
		wantStdout := ""
		if client == "lefthook" {
			wantStdout = CommandPolicyMarker + "\n"
		}
		if allow.ExitCode != 0 || string(allow.Stdout) != wantStdout || len(allow.Stderr) != 0 {
			t.Errorf("%s allow golden: %+v", client, allow)
		}
		deny := serve(t, client, "pre-tool", root, denied)
		if deny.ExitCode != denyExit(client) || len(deny.Stdout) != 0 ||
			!strings.HasPrefix(string(deny.Stderr), "[BLOCKED BY HISS-16] ") || !bytes.HasSuffix(deny.Stderr, []byte("\n")) {
			t.Errorf("%s deny golden: %+v", client, deny)
		}
		invalid := serve(t, client, "pre-tool", root, []byte("{}"))
		if invalid.ExitCode != denyExit(client) || !strings.Contains(string(invalid.Stderr), "Invalid hook input") {
			t.Errorf("%s invalid golden: %+v", client, invalid)
		}
	}
}

func TestRunReplaysThePythonSuitePayloads(t *testing.T) {
	root := repository(t, true)
	withOperator := policy(t, organisationContainerPattern)
	for _, client := range preToolClients {
		for _, fixture := range loadCases(t) {
			if client == "gemini" && bytes.Contains(fixture.Payload, []byte("hook_event_name")) {
				continue // a Claude-shaped event name is a contradiction for Gemini, asserted separately
			}
			response := Run(context.Background(), Invocation{
				Client: client, Event: "pre-tool", Stdin: bytes.NewReader(fixture.Payload),
				Getenv: noEnvironment, WorkDir: root, Policy: withOperator,
			})
			if (response.ExitCode == 0) != fixture.Allow {
				t.Errorf("%s %s: allow=%v, got %+v", client, fixture.Name, fixture.Allow, response)
			}
			if !fixture.Allow && response.ExitCode != denyExit(client) {
				t.Errorf("%s %s: deny exit %d", client, fixture.Name, response.ExitCode)
			}
		}
	}
}

func TestRunRawPayloadsAndTheSizeBoundary(t *testing.T) {
	root := repository(t, true)
	for _, client := range preToolClients {
		for _, raw := range rawCases() {
			response := serve(t, client, "pre-tool", root, raw.payload)
			if (response.ExitCode == 0) != raw.allow {
				t.Errorf("%s %s: allow=%v, got exit %d %q", client, raw.name, raw.allow, response.ExitCode, response.Stderr)
			}
		}
	}
}

func TestRunOrganisationContainersAreOperatorPolicy(t *testing.T) {
	root := repository(t, true)
	for _, fixture := range loadCases(t) {
		if !fixture.Operator {
			continue
		}
		if response := serve(t, "claude", "pre-tool", root, fixture.Payload); response.ExitCode != 0 {
			t.Errorf("%s: the built-in policy names an organisation container: %+v", fixture.Name, response)
		}
	}
}

func TestRunPayloadEventMustAgreeWithTheArgument(t *testing.T) {
	root := repository(t, true)
	for _, tc := range []struct {
		client, named string
		allow         bool
	}{
		{"claude", "PreToolUse", true}, {"claude", "PostToolUse", false}, {"claude", "BeforeTool", false},
		{"codex", "PreToolUse", true}, {"codex", "Stop", false},
		{"gemini", "BeforeTool", true}, {"gemini", "PreToolUse", false},
		{"lefthook", "PreToolUse", true}, {"lefthook", "agent-pre-tool", false},
	} {
		payload := commandPayload(t, map[string]any{"hook_event_name": tc.named, "tool_input": map[string]any{"command": "git status"}})
		if response := serve(t, tc.client, "pre-tool", root, payload); (response.ExitCode == 0) != tc.allow {
			t.Errorf("%s %s: %+v", tc.client, tc.named, response)
		}
	}
	wrongType := []byte(`{"hook_event_name":7,"tool_input":{"command":"git status"}}`)
	if response := serve(t, "claude", "pre-tool", root, wrongType); response.ExitCode != 2 {
		t.Errorf("non-text event name accepted: %+v", response)
	}
}

func TestRunClassifiesByToolName(t *testing.T) {
	root := repository(t, true)
	for _, tc := range []struct {
		client, payload string
		exit            int
	}{
		{"claude", `{"tool_name":"Read","tool_input":{"file_path":"x"}}`, 0},
		{"claude", `{"tool_name":"Read"}`, 0},
		{"claude", `{"tool_name":"Bash","tool_input":{"file_path":"x"}}`, 2},
		{"claude", `{"tool_name":null,"tool_input":{}}`, 2},
		{"claude", `{"tool_name":["Bash"],"tool_input":{"command":"git status"}}`, 2},
		{"gemini", `{"tool_name":"run_shell_command","tool_input":{}}`, 2},
		{"gemini", `{"tool_name":"read_file"}`, 0},
		{"lefthook", `{"tool_name":"Read"}`, 1},
		{"lefthook", `{"tool_name":"run_shell_command","tool_input":{"command":"rm -rf .git/hooks"}}`, 1},
	} {
		if response := serve(t, tc.client, "pre-tool", root, []byte(tc.payload)); response.ExitCode != tc.exit {
			t.Errorf("%s %s: %+v", tc.client, tc.payload, response)
		}
	}
}

func TestRunSkipsUngovernedWorkspacesWithAReason(t *testing.T) {
	plain := repository(t, false)
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	denied := []byte(`{"tool_input":{"command":"rm -rf .git/hooks"}}`)
	for _, client := range preToolClients {
		response := serve(t, client, "pre-tool", plain, denied)
		if response.ExitCode != 0 || len(response.Stdout) != 0 ||
			string(response.Stderr) != "praetor hook: workspace not governed, skipped\n" {
			t.Errorf("%s ungoverned: %+v", client, response)
		}
		response = serve(t, client, "pre-tool", outside, denied)
		if response.ExitCode != 0 || string(response.Stderr) != "praetor hook: no repository, skipped\n" {
			t.Errorf("%s no repository: %+v", client, response)
		}
		if invalid := serve(t, client, "pre-tool", plain, []byte("not json")); invalid.ExitCode == 0 {
			t.Errorf("%s: undecodable input was skipped instead of denied", client)
		}
	}
}

func TestRunTakesTheWorkspaceFromThePayload(t *testing.T) {
	governed, plain := repository(t, true), repository(t, false)
	command := map[string]any{"command": "rm -rf .git/hooks"}
	inGoverned := commandPayload(t, map[string]any{"cwd": governed, "tool_input": command})
	if response := serve(t, "claude", "pre-tool", plain, inGoverned); response.ExitCode != 2 {
		t.Errorf("payload workspace lost to the process directory: %+v", response)
	}
	inPlain := commandPayload(t, map[string]any{"cwd": plain, "tool_input": command})
	if response := serve(t, "claude", "pre-tool", governed, inPlain); response.ExitCode != 0 {
		t.Errorf("process directory overrode the payload workspace: %+v", response)
	}
	t.Setenv("GIT_DIR", filepath.Join(plain, ".git"))
	if response := serve(t, "claude", "pre-tool", plain, inGoverned); response.ExitCode != 2 {
		t.Errorf("ambient GIT_DIR redirected the workspace: %+v", response)
	}
}

func TestRunFailsClosedOnAnUnresolvableWorkspace(t *testing.T) {
	governed := repository(t, true)
	for name, cwd := range map[string]any{
		"relative": "relative/dir", "missing": filepath.Join(governed, "does-not-exist"), "not-text": 7,
	} {
		payload := commandPayload(t, map[string]any{"cwd": cwd, "tool_input": map[string]any{"command": "git status"}})
		if response := serve(t, "claude", "pre-tool", governed, payload); response.ExitCode != 2 {
			t.Errorf("%s workspace allowed: %+v", name, response)
		}
	}
	if response := serve(t, "claude", "pre-tool", "", []byte(`{"tool_input":{"command":"git status"}}`)); response.ExitCode != 2 {
		t.Errorf("no workspace at all allowed: %+v", response)
	}
}

func TestRunEnvironmentEvent(t *testing.T) {
	root := repository(t, true)
	for _, tc := range []struct {
		name, key, value string
		exit             int
	}{
		{"clean", "", "", 0}, {"lefthook-enabled", "LEFTHOOK", "1", 0}, {"lefthook-disabled", "LEFTHOOK", "0", 1},
		{"exclude", "LEFTHOOK_EXCLUDE", "lint", 1}, {"skip", "LEFTHOOK_SKIP", "pre-commit", 1},
	} {
		getenv := func(key string) string {
			if key == tc.key {
				return tc.value
			}
			return ""
		}
		response := Run(context.Background(), Invocation{
			Client: "lefthook", Event: "environment", Getenv: getenv, WorkDir: root, Policy: policy(t),
		})
		if response.ExitCode != tc.exit || len(response.Stdout) != 0 {
			t.Errorf("%s: %+v", tc.name, response)
		}
		allowed := []byte(`{"tool_input":{"command":"git status"}}`)
		preTool := Run(context.Background(), Invocation{
			Client: "claude", Event: "pre-tool", Stdin: bytes.NewReader(allowed), Getenv: getenv, WorkDir: root, Policy: policy(t),
		})
		if (preTool.ExitCode == 0) != (tc.exit == 0) {
			t.Errorf("%s: pre-tool ignores the environment check: %+v", tc.name, preTool)
		}
	}
}

func TestRunRejectsUnsupportedArguments(t *testing.T) {
	root := repository(t, true)
	for _, pair := range [][2]string{
		{"", ""}, {"claude", ""}, {"Claude", "pre-tool"}, {"claude", "pre_tool"}, {"claude", "pre-tool "},
		{"agy", "pre-tool"}, {"claude", "stop"}, {"claude", "environment"}, {"lefthook", "post-tool"}, {"-h", "--help"},
	} {
		response := serve(t, pair[0], pair[1], root, []byte(`{"tool_input":{"command":"git status"}}`))
		if response.ExitCode != 2 || !strings.Contains(string(response.Stderr), "usage: praetorctl hook <client> <event>") ||
			!strings.Contains(string(response.Stderr), "  praetorctl hook lefthook environment\n") {
			t.Errorf("%q %q: %+v", pair[0], pair[1], response)
		}
	}
}

func TestRunFailsClosedOnMissingCollaborators(t *testing.T) {
	root := repository(t, true)
	allowed := []byte(`{"tool_input":{"command":"git status"}}`)
	base := Invocation{Client: "claude", Event: "pre-tool", Stdin: bytes.NewReader(allowed), Getenv: noEnvironment, WorkDir: root, Policy: policy(t)}
	noStdin, noGetenv, noPolicy := base, base, base
	noStdin.Stdin, noGetenv.Getenv, noPolicy.Policy = nil, nil, nil
	noGetenv.Stdin, noPolicy.Stdin = bytes.NewReader(allowed), bytes.NewReader(allowed)
	for name, invocation := range map[string]Invocation{"stdin": noStdin, "getenv": noGetenv, "policy": noPolicy} {
		if response := Run(context.Background(), invocation); response.ExitCode != 2 {
			t.Errorf("missing %s allowed: %+v", name, response)
		}
	}
}

func TestRunDeniesWhenStdinNeverCloses(t *testing.T) {
	root := repository(t, true)
	reader, writer := io.Pipe()
	t.Cleanup(func() {
		if err := writer.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	response := Run(ctx, Invocation{Client: "claude", Event: "pre-tool", Stdin: reader, Getenv: noEnvironment, WorkDir: root, Policy: policy(t)})
	if response.ExitCode != 2 || !strings.Contains(string(response.Stderr), "not delivered in time") || time.Since(start) > 5*time.Second {
		t.Errorf("open stdin held the hook: %+v after %s", response, time.Since(start))
	}
}
