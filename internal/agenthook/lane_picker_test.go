package agenthook

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/router"
)

func pickerScript(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fixture; executable picker integration requires a Windows fixture")
	}
	path := filepath.Join(t.TempDir(), "picker")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func pickerRouting(t *testing.T, root string) {
	t.Helper()
	writeBriefFixture(t, root, ".config/models/routing.yaml",
		strings.Replace(briefRoutingTemplate, "%s", `"feature_implementation", "ci_debugging"`, 1)+
			"offload:\n  labels: [feature_implementation]\n")
}

func pickerHook(t *testing.T, root, picker, brief string) Response {
	t.Helper()
	payload := nativePayload(t, "PreToolUse", "session", "Agent", "tool",
		map[string]any{"prompt": brief, "run_in_background": true}, nil)
	getenv := func(key string) string {
		if key == "PRAETOR_LANE_PICKER" {
			return picker
		}
		return ""
	}
	return Run(t.Context(), Invocation{Client: "claude", Event: string(EventPreDispatch), Stdin: bytes.NewReader(payload),
		Getenv: getenv, WorkDir: root, Policy: policy(t), CorrelationDir: t.TempDir()})
}

func TestClaudeLanePickerDecisions(t *testing.T) {
	root := repository(t, true)
	pickerRouting(t, root)
	for _, tc := range []struct {
		name, script, brief string
		wantExit            int
		wantText            string
	}{
		{"route", "printf 'agy use agy-run.sh\\n'; exit 0", validBrief, 2, "[ROUTED] task feature_implementation runs on agy, not the Claude lane: use agy-run.sh"},
		{"crlf", "printf 'local use runner\\r\\n'; exit 0", validBrief, 2, "runs on local, not the Claude lane: use runner"},
		{"keep", "exit 3", validBrief, 0, ""},
		{"failure", "exit 1", validBrief, 0, "lane picker unavailable:"},
		{"malformed", "printf 'claude stay here\\n'", validBrief, 0, "lane picker unavailable:"},
		{"other label", "touch \"$0.ran\"; exit 0", briefFor("ci_debugging", "internal/agenthook"), 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := pickerScript(t, tc.script)
			got := pickerHook(t, root, path, tc.brief)
			if got.ExitCode != tc.wantExit || !strings.Contains(string(got.Stderr), tc.wantText) {
				t.Fatalf("response = %+v; want exit %d and %q", got, tc.wantExit, tc.wantText)
			}
			if tc.name == "other label" {
				if _, err := os.Stat(path + ".ran"); !os.IsNotExist(err) {
					t.Fatalf("unlisted label ran picker: %v", err)
				}
			}
		})
	}
}

func TestClaudeLanePickerUnavailable(t *testing.T) {
	root := repository(t, true)
	pickerRouting(t, root)
	missing := filepath.Join(t.TempDir(), "missing")
	got := pickerHook(t, root, missing, validBrief)
	if got.ExitCode != 0 || !strings.Contains(string(got.Stderr), "dispatch stays on the Claude lane") {
		t.Fatalf("missing picker was silent: %+v", got)
	}
	path := pickerScript(t, "sleep 2")
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	verdict := evaluateLaneOffload(ctx, &router.OffloadConfig{Labels: []string{"feature_implementation"}}, validBrief,
		func(key string) string {
			if key == "PRAETOR_LANE_PICKER" {
				return path
			}
			return ""
		})
	if verdict.Outcome != Allow || !strings.Contains(verdict.Notice, "picker timed out") ||
		!strings.Contains(verdict.Notice, "dispatch stays on the Claude lane") {
		t.Fatalf("picker timeout = %+v", verdict)
	}
	tooMuch := pickerScript(t, "printf '"+strings.Repeat("x", pickerOutputLimit+1)+"'")
	got = pickerHook(t, root, tooMuch, validBrief)
	if got.ExitCode != 0 || !strings.Contains(string(got.Stderr), "exceeds 65536 bytes") {
		t.Fatalf("oversized picker output = %+v", got)
	}
}

func TestLanePickerConfigurationPrecedence(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "praetor", "lane-picker")
	writeBriefFixture(t, root, "praetor/lane-picker", "/file/picker\nignored\n")
	getenv := func(key string) string {
		switch key {
		case "XDG_CONFIG_HOME":
			return root
		case "PRAETOR_LANE_PICKER":
			return "/env/picker"
		}
		return ""
	}
	if path, configured, err := lanePickerPath(getenv); err != nil || !configured || path != "/env/picker" {
		t.Fatalf("env precedence = %q, %v, %v", path, configured, err)
	}
	getenv = func(key string) string {
		if key == "XDG_CONFIG_HOME" {
			return root
		}
		return ""
	}
	if path, configured, err := lanePickerPath(getenv); err != nil || !configured || path != "/file/picker" {
		t.Fatalf("config first line = %q, %v, %v at %s", path, configured, err, configPath)
	}
}
