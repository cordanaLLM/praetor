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
// but never inspected. #1277: the list offered the persona-only helpers as runnable; it now
// reads the registry 'agent run' dispatches from and names persona-only files apart.

// agentFixture is a working directory whose .agents/agents holds a persona file per name.
func agentFixture(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, ".agents", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(agentsDir, name+".md"), []byte("# fixture\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
}

// listSections runs 'agent list' and returns the names it offers as runnable and the names it
// marks as persona only.
func listSections(t *testing.T) (runnable, personaOnly []string, out string) {
	t.Helper()
	out, err := captureStdout(t, func() error { return dispatchCommand("agent", []string{"list"}) })
	if err != nil {
		t.Fatalf("agent list: %v", err)
	}
	section := &runnable
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Persona only, not runnable") {
			section = &personaOnly
			continue
		}
		if fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "•")); strings.HasPrefix(strings.TrimSpace(line), "•") && len(fields) > 0 {
			*section = append(*section, fields[0])
		}
	}
	return runnable, personaOnly, out
}

// Positive (#1277): the list offers exactly the registry's helpers as runnable, naming the
// persona file where one exists, and every name it offers resolves to an implementation.
func TestListAgents_Positive_OffersOnlyRunnableHelpers(t *testing.T) {
	agentFixture(t, "praetor-auditor", "praetor-fuzzer", "praetor-packager", "repo-auditor")
	runnable, _, out := listSections(t)
	if len(runnable) != len(agentHelpers) {
		t.Fatalf("runnable = %v, want the %d registry helpers\n%s", runnable, len(agentHelpers), out)
	}
	for _, name := range runnable {
		if _, ok := findAgentHelper(name); !ok {
			t.Errorf("'agent list' offers %s as runnable, but 'agent run' has no implementation for it", name)
		}
	}
	mustContain(t, out, "praetor-auditor           (.agents/agents/praetor-auditor.md)",
		"praetor-gatekeeper        (built in, no persona file here)")
}

// Negative (#1277): a persona file without an implementation is named apart as not runnable,
// and 'agent run' refuses each such name, in either form, by name and never as unknown.
func TestDispatchAgentTask_Negative_PersonaOnlyIsListedApartAndRefused(t *testing.T) {
	agentFixture(t, "praetor-auditor", "praetor-fuzzer", "praetor-packager", "repo-auditor")
	runnable, personaOnly, out := listSections(t)
	want := []string{"praetor-fuzzer", "praetor-packager", "repo-auditor"}
	if strings.Join(personaOnly, ",") != strings.Join(want, ",") {
		t.Fatalf("persona only = %v, want %v\n%s", personaOnly, want, out)
	}
	for _, name := range append(personaOnly, "praetor_fuzzer", "praetor_packager") {
		for _, offered := range runnable {
			if offered == canonicalAgentName(name) {
				t.Fatalf("%s is offered as runnable and as persona only", name)
			}
		}
		err := dispatchAgentTask(name, nil)
		mustErrContain(t, err, name+" has a persona file but no implementation 'agent run' can run")
		if strings.Contains(err.Error(), "unknown agent persona") {
			t.Fatalf("%s has a persona file; it must not be reported as unknown: %v", name, err)
		}
	}
}

func TestDispatchAgentTask_Positive_AuditorStillDispatches(t *testing.T) {
	// A hermetic, empty scan target: dispatch routing is under test, not the HISS
	// scanner itself.
	t.Chdir(t.TempDir())
	for _, name := range []string{"praetor-auditor", "praetor_auditor"} {
		out, err := captureStdout(t, func() error { return dispatchAgentTask(name, nil) })
		if err != nil {
			t.Fatalf("%s must still dispatch to the HISS scan: %v", name, err)
		}
		// The helper's own line shows the registry entry ran, not only that nothing failed.
		mustContain(t, out, "[praetor-auditor] AST sweep complete")
	}
}

func TestDispatchAgentTask_Negative_GenuinelyUnknownPersonaNameIsRejected(t *testing.T) {
	agentFixture(t, "praetor-fuzzer")
	err := dispatchAgentTask("not-a-real-persona", nil)
	mustErrContain(t, err, "unknown agent persona: not-a-real-persona")
}

// Boundary (#1277): without .agents/agents the list still offers every runnable helper and no
// persona-only section, and a persona-only name there is unknown, since no file names it.
func TestListAgents_Boundary_NoPersonaDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	runnable, personaOnly, out := listSections(t)
	if len(runnable) != len(agentHelpers) || len(personaOnly) != 0 || strings.Contains(out, "Persona only") {
		t.Fatalf("runnable %v, persona only %v\n%s", runnable, personaOnly, out)
	}
	mustErrContain(t, dispatchAgentTask("praetor-fuzzer", nil), "unknown agent persona: praetor-fuzzer")
}

func TestDispatchAgentTask_Boundary_ExtraArgumentsAreRefused(t *testing.T) {
	err := dispatchAgentTask("praetor-auditor", []string{"--scope=foo"})
	mustErrContain(t, err, "unexpected extra argument")
}
