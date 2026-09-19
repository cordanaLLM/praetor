// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// BUG-726: `agent list` enumerated every *.md persona under .agents/agents while
// dispatchAgentTask's switch only recognized four of them, and extraArgs was accepted
// but never inspected.

func TestListAgents_Positive_EnumeratesPersonaFiles(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, ".agents", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"praetor-auditor.md", "praetor-fuzzer.md", "praetor-packager.md"} {
		if err := os.WriteFile(filepath.Join(agentsDir, name), []byte("# fixture\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)

	out, err := captureStdout(t, func() error { return dispatchCommand("agent", []string{"list"}) })
	if err != nil {
		t.Fatalf("agent list: %v", err)
	}
	mustContain(t, out, "praetor-auditor", "praetor-fuzzer", "praetor-packager")
}

func TestDispatchAgentTask_Positive_AuditorStillDispatches(t *testing.T) {
	// A hermetic, empty scan target: dispatch routing is under test, not the HISS
	// scanner itself.
	t.Chdir(t.TempDir())
	if err := dispatchAgentTask("praetor-auditor", nil); err != nil {
		t.Fatalf("praetor-auditor must still dispatch to the HISS scan: %v", err)
	}
}

func TestDispatchAgentTask_Negative_ListedButUndispatchablePersonaIsRefusedByName(t *testing.T) {
	for _, name := range []string{"praetor-fuzzer", "praetor_fuzzer", "praetor-packager", "praetor_packager"} {
		err := dispatchAgentTask(name, nil)
		mustErrContain(t, err, name)
		if err != nil && strings.Contains(err.Error(), "unknown agent persona") {
			t.Fatalf("%s is advertised by 'agent list', not a typo; it must not be reported as unknown: %v", name, err)
		}
	}
}

func TestDispatchAgentTask_Negative_GenuinelyUnknownPersonaNameIsRejected(t *testing.T) {
	err := dispatchAgentTask("not-a-real-persona", nil)
	mustErrContain(t, err, "unknown agent persona: not-a-real-persona")
}

func TestDispatchAgentTask_Boundary_ExtraArgumentsAreRefused(t *testing.T) {
	err := dispatchAgentTask("praetor-auditor", []string{"--scope=foo"})
	mustErrContain(t, err, "unexpected extra argument")
}
