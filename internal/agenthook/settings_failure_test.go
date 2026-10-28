// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"errors"
	"strings"
	"testing"
)

// Order: engine-hook-digest-mismatch-fail-closed-20261028.
func TestSettingsRepairCommand(t *testing.T) {
	for _, command := range []string{
		"praetorctl workstation install --source /x --fleet-config /y",
		"standardsctl workstation install --source '/engine checkout'",
		`"praetorctl" 'workstation' "install" --source /x`,
	} {
		if !settingsRepairCommand(command) {
			t.Errorf("repair refused: %s", command)
		}
	}
	for _, command := range []string{
		"", "praetorctl workstation", "praetorctl workstation installer", "praetorctl workstation update",
		"echo praetorctl workstation install", "praetorctl workstation install; make test",
		"praetorctl workstation install && make test", "praetorctl workstation install\nmake test",
		"praetorctl workstation install --source $(make test)", "praetorctl workstation install --source `make test`",
		"praetorctl workstation install --source /x > /etc/file", "praetorctl workstation install --source 'unclosed",
	} {
		if settingsRepairCommand(command) {
			t.Errorf("non-repair allowed: %s", command)
		}
	}
}

func TestSettingsUnavailableVerdicts(t *testing.T) {
	cause := errors.New("broken settings; repair: praetorctl workstation install --source /x")
	for _, event := range []Event{EventPreTool, EventPreEdit, EventPreDispatch, EventPreHandback, EventPostReturn, EventStop, EventEnvironment} {
		verdict := settingsUnavailable(Canonical{Event: event}, cause, nil)
		if verdict.Outcome != Deny || !strings.HasPrefix(verdict.Reason, "[BLOCKED BY HISS] operator settings unavailable:") {
			t.Errorf("%s: %+v", event, verdict)
		}
	}
	for _, event := range []Event{EventPostTool, EventPrompt, EventSubagentStart, EventDispatchReceipt, EventDispatchAbort, EventHandbackReceipt, EventHandbackAbort} {
		verdict := settingsUnavailable(Canonical{Event: event}, cause, nil)
		if verdict.Outcome != Skip || !strings.Contains(verdict.Reason, cause.Error()) {
			t.Errorf("%s: %+v", event, verdict)
		}
	}
	repair := Canonical{Event: EventPreTool, Command: "praetorctl workstation install --source /x"}
	if verdict := settingsUnavailable(repair, cause, nil); verdict.Outcome != Allow || !strings.Contains(verdict.Notice, cause.Error()) {
		t.Errorf("repair: %+v", verdict)
	}
	if verdict := settingsUnavailable(repair, cause, errors.New("bad payload")); verdict.Outcome != Deny {
		t.Errorf("invalid repair payload: %+v", verdict)
	}
}

func TestSettingsFailureUsesEveryDialect(t *testing.T) {
	for _, client := range []string{"claude", "codex", "gemini", "lefthook", "agy", "opencode"} {
		t.Run(client, func(t *testing.T) {
			dialect, ok := DialectFor(client)
			if !ok {
				t.Fatal("missing dialect")
			}
			verdict := settingsUnavailable(Canonical{Event: EventPrompt}, errors.New("settings cause"), nil)
			response := dialect.Encode(Canonical{Event: EventPrompt}, verdict)
			if response.ExitCode != 0 || !strings.Contains(string(response.Stderr), "settings cause, skipped") {
				t.Fatalf("prompt: %s", response)
			}
		})
	}
}
