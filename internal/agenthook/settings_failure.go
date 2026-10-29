// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"regexp"

	"github.com/cordanaLLM/praetor/internal/forge"
)

// Only literal argument characters are admitted after the bounded shell reader succeeds.
var settingsRepairWord = regexp.MustCompile(`^[\pL\pN_./:=+@,%\\ '"-]+$`)

// settingsUnavailable fails closed before policy evaluation or recording can allow a call.
// Order: engine-hook-digest-mismatch-fail-closed-20261028.
func settingsUnavailable(canonical Canonical, cause, decodeErr error) Verdict {
	reason := "operator settings unavailable: " + cause.Error()
	switch canonical.Event {
	case EventPostTool, EventPrompt, EventSubagentStart, EventDispatchReceipt, EventDispatchAbort,
		EventHandbackReceipt, EventHandbackAbort:
		return Verdict{Outcome: Skip, Reason: reason}
	}
	if decodeErr == nil && canonical.Event == EventPreTool && settingsRepairCommand(canonical.Command) {
		return Verdict{Outcome: Allow, Notice: "operator settings repair allowed; " + reason}
	}
	return Verdict{Outcome: Deny, Reason: "[BLOCKED BY HISS] " + reason}
}

// settingsRepairCommand admits one literal install invocation, with no shell composition.
// Reuse the bounded shell reader; a prefix cannot exempt a second guarded command.
func settingsRepairCommand(command string) bool {
	words, err := forge.ShellFields(command)
	if err != nil || len(words) < 3 {
		return false
	}
	if binary := unquoteTarget(words[0]); binary != "praetorctl" && binary != "standardsctl" {
		return false
	}
	if unquoteTarget(words[1]) != "workstation" || unquoteTarget(words[2]) != "install" {
		return false
	}
	for _, word := range words {
		if !settingsRepairWord.MatchString(word) {
			return false
		}
	}
	return true
}
