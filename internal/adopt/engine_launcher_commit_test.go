// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"strings"
	"testing"
)

// A repository may pin its engine as PRAETOR_COMMIT, the full commit its CI builds the gate
// engine from, instead of PRAETOR_REF (#1366). Without the launcher reading it, CI and hooks
// ran different engines.

const engineCommit = "492a00f930e1aa7b3c4d5e6f708192a3b4c5d6e7"

// Positive: a workflow that declares only PRAETOR_COMMIT runs the engine of that commit, not the
// one on PATH, and installs it into the cache of that commit on first use.
func TestEngineLauncher_Positive_PraetorCommitPinsEngine(t *testing.T) {
	f := newEngineFixture(t)
	f.pathEngine()
	f.cached(engineCommit)
	f.declare("ci.yml", "env:\n  PRAETOR_COMMIT: \""+engineCommit+"\"\n")
	if out, code := f.run(); code != 0 || strings.TrimSpace(out) != "pinned-engine audit --offline" {
		t.Fatalf("cached: exit %d, output %q, want the engine PRAETOR_COMMIT pins", code, out)
	}

	f = newEngineFixture(t)
	f.pathEngine()
	f.goStub(installingGo)
	f.declare("ci.yml", "env:\n  PRAETOR_COMMIT: "+engineCommit+"\n")
	out, code := f.run()
	if code != 0 || !strings.Contains(out, "installed-engine audit --offline") || !strings.Contains(out, "PRAETOR_COMMIT="+engineCommit) || strings.Contains(out, "path-engine") {
		t.Fatalf("cold: exit %d, output %q, want the PRAETOR_COMMIT engine installed and run", code, out)
	}
}

// Negative: PRAETOR_COMMIT names a commit, so a value that is not 40 hexadecimal digits is no
// pin. The hook says so and fails closed rather than running the engine on PATH.
func TestEngineLauncher_Negative_PraetorCommitNotFullCommitFailsClosed(t *testing.T) {
	for _, bad := range []string{"main", enginePin, "v1.2.3", strings.Repeat("a", 41), engineCommit[:39] + "g"} {
		f := newEngineFixture(t)
		f.pathEngine()
		f.declare("ci.yml", "env:\n  PRAETOR_COMMIT: "+bad+"\n")
		out, code := f.run()
		if code != 1 || !strings.Contains(out, "PRAETOR_COMMIT='"+bad+"'") || !strings.Contains(out, "no usable pin") || !strings.Contains(out, "ci.yml") || strings.Contains(out, "path-engine") {
			t.Errorf("PRAETOR_COMMIT %q: exit %d, output %q", bad, code, out)
		}
	}
}

// Negative: PRAETOR_REF and PRAETOR_COMMIT with different values pin no engine; the hook refuses
// to choose and names both declarations.
func TestEngineLauncher_Negative_PraetorRefAndCommitDisagreeFailClosed(t *testing.T) {
	f := newEngineFixture(t)
	f.pathEngine()
	f.cached(engineCommit)
	f.declare("gate.yml", "env:\n  PRAETOR_REF: "+enginePin+"\n")
	f.declare("ci.yml", "env:\n  PRAETOR_COMMIT: "+engineCommit+"\n")
	out, code := f.run()
	if code != 1 || !strings.Contains(out, "several engine pins") || !strings.Contains(out, "PRAETOR_REF") || !strings.Contains(out, "PRAETOR_COMMIT") ||
		strings.Contains(out, "path-engine") || strings.Contains(out, "pinned-engine") {
		t.Fatalf("exit %d, output %q", code, out)
	}
}

// Boundary: both names with the same value are one pin.
func TestEngineLauncher_Boundary_PraetorRefAndCommitSameValueIsOnePin(t *testing.T) {
	f := newEngineFixture(t)
	f.pathEngine()
	f.cached(engineCommit)
	f.declare("ci.yml", "env:\n  PRAETOR_REF: "+engineCommit+"\n  PRAETOR_COMMIT: '"+engineCommit+"'\n")
	if out, code := f.run(); code != 0 || strings.TrimSpace(out) != "pinned-engine audit --offline" {
		t.Fatalf("exit %d, output %q", code, out)
	}
}

// Boundary: with neither name declared the hook still runs the engine on PATH, and never
// silently: standard error names both pin variables it looked for.
func TestEngineLauncher_Boundary_NoPinNamesBothVariables(t *testing.T) {
	f := newEngineFixture(t)
	f.pathEngine()
	f.declare("ci.yml", "env:\n  PRAETOR_COMMIT_SHA: "+engineCommit+"\n  # PRAETOR_COMMIT: "+engineCommit+"\n")
	out, code := f.run()
	if code != 0 || !strings.Contains(out, "path-engine audit --offline") || !strings.Contains(out, "no PRAETOR_REF or PRAETOR_COMMIT pin") {
		t.Fatalf("exit %d, output %q", code, out)
	}
}
