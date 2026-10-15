// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/clientjson"
)

// codexCall runs one codex row from cwd with the session id and a user cache at cache.
func codexCall(t *testing.T, event, cwd, cache, session string, fields map[string]any) Response {
	t.Helper()
	payload := map[string]any{"session_id": session, "cwd": cwd}
	for key, value := range fields {
		payload[key] = value
	}
	env := map[string]string{"XDG_CACHE_HOME": cache}
	return Run(context.Background(), Invocation{
		Client: "codex", Event: event, Stdin: bytes.NewReader(commandPayload(t, payload)),
		Getenv: func(key string) string { return env[key] }, WorkDir: cwd, Policy: policy(t),
	})
}

func codexBash(command string) map[string]any {
	return map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]any{"command": command}}
}

// #1347. SessionStart in the adopted repository records its root, and a pre-tool call is then
// judged against that root wherever Codex runs it: in the repository, in a foreign repository
// (Codex's own memory folder is one) and outside any repository. Without a record a call is
// judged against its working directory's governed repository with a notice, and refused
// where that directory is no governed repository.
func TestCodexSession_3D(t *testing.T) {
	adopted, foreign := repository(t, true), repository(t, false)
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	start := codexCall(t, "session-start", adopted, cache, "recorded-session", map[string]any{"hook_event_name": "SessionStart", "source": "startup"})
	if start.ExitCode != 0 {
		t.Fatalf("session start: %s", start)
	}
	record, err := os.ReadFile(filepath.Join(cache, filepath.FromSlash(codexSessionDir), "recorded-session"))
	if err != nil {
		t.Fatalf("no session record: %v", err)
	}
	if got, _ := filepath.EvalSymlinks(strings.TrimSpace(string(record))); got == "" {
		t.Fatalf("record %q names no directory", record)
	}
	for name, cwd := range map[string]string{"adopted": adopted, "foreign": foreign, "outside": outside} {
		allowed := codexCall(t, "pre-tool", cwd, cache, "recorded-session", codexBash("git status"))
		denied := codexCall(t, "pre-tool", cwd, cache, "recorded-session", codexBash("git commit --no-"+"verify"))
		if allowed.ExitCode != 0 || len(allowed.Stderr) != 0 || denied.ExitCode != 2 {
			t.Errorf("recorded session, cwd %s: allow %s; deny %s", name, allowed, denied)
		}
	}
	noticed := codexCall(t, "pre-tool", adopted, cache, "unrecorded-session", codexBash("git status"))
	if noticed.ExitCode != 0 || !strings.Contains(string(noticed.Stderr), "no codex session record unrecorded-session") ||
		!strings.Contains(string(noticed.Stderr), "praetorctl adopt") {
		t.Errorf("unrecorded session in the adopted repository: %s; want an allow naming the missing record and praetorctl adopt", noticed)
	}
	for name, cwd := range map[string]string{"foreign": foreign, "outside": outside} {
		refused := codexCall(t, "pre-tool", cwd, cache, "unrecorded-session", codexBash("git status"))
		if refused.ExitCode != 2 || !strings.Contains(string(refused.Stderr), "no codex session record") {
			t.Errorf("unrecorded session, cwd %s: %s; want a refusal", name, refused)
		}
	}
}

// Boundary: a session that starts outside an adopted repository records nothing and never
// blocks the start; a session id that cannot name a file is no record.
func TestCodexSession_Boundary_NoRecordOutsideAnAdoptedRepository(t *testing.T) {
	foreign, cache := repository(t, false), t.TempDir()
	start := codexCall(t, "session-start", foreign, cache, "foreign-session", map[string]any{"hook_event_name": "SessionStart"})
	if start.ExitCode != 0 {
		t.Fatalf("session start outside an adopted repository: %s", start)
	}
	if _, err := os.Stat(filepath.Join(cache, filepath.FromSlash(codexSessionDir), "foreign-session")); !os.IsNotExist(err) {
		t.Fatalf("a record was kept for an unadopted repository: %v", err)
	}
	if _, err := codexSessionRecord("../escape", func(string) string { return cache }); err == nil {
		t.Fatal("a session id with a path separator named a record file")
	}
}

// The drift check (#1347): a Codex rendering with the pre-tool row and without SessionStart is
// reported; one with both, one with neither and no file at all are not.
func TestCodexSessionStartMissing_3D(t *testing.T) {
	write := func(t *testing.T, events ...Event) string {
		t.Helper()
		root := t.TempDir()
		if len(events) == 0 {
			return root
		}
		file, _ := NativeHookFile("codex")
		var hooks []clientjson.Hook
		for _, event := range events {
			hooks = append(hooks, NativeHooks("codex", file, event)...)
		}
		plan, err := clientjson.PlanHooks(context.Background(), nil, hooks)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, plan.Content, 0o644); err != nil {
			t.Fatal(err)
		}
		return root
	}
	for name, tc := range map[string]struct {
		events []Event
		want   bool
	}{
		"pre-tool without session-start": {[]Event{EventPreTool}, true},
		"pre-tool with session-start":    {[]Event{EventPreTool, EventSessionStart}, false},
		"no codex rows":                  {[]Event{EventPostReturn}, false},
		"no file":                        {nil, false},
	} {
		got, err := CodexSessionStartMissing(context.Background(), write(t, tc.events...))
		if err != nil || got != tc.want {
			t.Errorf("%s: missing=%v err=%v, want %v", name, got, err, tc.want)
		}
	}
}
