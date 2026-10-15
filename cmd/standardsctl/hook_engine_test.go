// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/agenthook"
	"github.com/cordanaLLM/praetor/internal/enginepin"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// pinnedEngineSource is a stand-in engine: `hook` lists the claude pre-tool pair, and a call of
// that pair prints the marker and the payload it read to stderr and allows.
const pinnedEngineSource = `package main

import (
	"io"
	"os"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "hook" {
		os.Stdout.WriteString("usage:\n  praetorctl hook claude pre-tool\n")
		os.Exit(2)
	}
	payload, _ := io.ReadAll(os.Stdin)
	os.Stderr.WriteString("pinned-engine " + os.Getenv("PRAETOR_HOOK_PINNED_ENGINE") + " " + string(payload))
}
`

const fixturePin = "492a00f930e1"

var preToolRow = agenthook.Registration{Client: "claude", Event: "pre-tool"}

// pinnedRepository writes a repository whose workflow pins fixturePin, and a cache holding that
// engine when installed is true. It returns the root and the environment the handoff reads.
func pinnedRepository(t *testing.T, workflow string, installed bool) (string, map[string]string) {
	t.Helper()
	root, cache := t.TempDir(), t.TempDir()
	if workflow != "" {
		dir := filepath.Join(root, ".github", "workflows")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "gate.yml"), []byte(workflow), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if installed {
		dir := filepath.Join(cache, "praetor", "engine", fixturePin)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		testsupport.BuildExecutable(t, dir, "standardsctl", pinnedEngineSource)
	}
	return root, map[string]string{"XDG_CACHE_HOME": cache}
}

func handoffWith(env map[string]string) pinnedEngine {
	return pinnedEngine{
		getenv:     func(key string) string { return env[key] },
		executable: func() (string, error) { return "/path/to/praetorctl", nil },
		resolve:    enginepin.Resolve,
	}
}

// Positive (#1173): with a pin whose engine is installed, the pinned engine judges the call: it
// reads the payload, is marked as the handed-off engine, and its answer is returned as it is.
func TestPinnedEngine_Positive_PinnedEngineJudges(t *testing.T) {
	root, env := pinnedRepository(t, "env:\n  PRAETOR_REF: "+fixturePin+"\n", true)
	result := handoffWith(env).handoff(t.Context(), root, preToolRow, []byte(`{"tool_name":"Bash"}`))
	if !result.Handled || result.Response.ExitCode != 0 ||
		string(result.Response.Stderr) != "pinned-engine "+fixturePin+` {"tool_name":"Bash"}` {
		t.Fatalf("handoff = %+v (stderr %q); want the pinned engine's answer", result, result.Response.Stderr)
	}
}

// Negative: a pin whose engine is not installed is judged by this engine, never skipped, and
// the note names the pin, the substitute and the install command; nothing is installed.
func TestPinnedEngine_Negative_CacheMissIsJudgedHereAndSaysSo(t *testing.T) {
	root, env := pinnedRepository(t, "env:\n  PRAETOR_REF: "+fixturePin+"\n", false)
	result := handoffWith(env).handoff(t.Context(), root, preToolRow, nil)
	if result.Handled || result.Deny != "" || result.Skip != "" {
		t.Fatalf("cache miss = %+v; want this engine to judge", result)
	}
	for _, want := range []string{"PRAETOR_REF=" + fixturePin, "declared in .github/workflows/gate.yml", "not installed",
		"judged by /path/to/praetorctl", "go install " + enginepin.Module + "/" + enginepin.Package + "@" + fixturePin} {
		if !strings.Contains(result.Note, want) {
			t.Errorf("note %q lacks %q", result.Note, want)
		}
	}
}

// Boundary: without a pin this engine judges and the note names it; a pin that cannot be used
// refuses the call; inside the handed-off engine nothing is handed on again; an installed pinned
// engine that serves no such row skips with the reason instead of blocking.
func TestPinnedEngine_Boundary_NoPinFaultLoopAndUnservedRow(t *testing.T) {
	root, env := pinnedRepository(t, "", false)
	if result := handoffWith(env).handoff(t.Context(), root, preToolRow, nil); result.Handled ||
		!strings.Contains(result.Note, "pins no engine") || !strings.Contains(result.Note, "/path/to/praetorctl") {
		t.Errorf("no pin = %+v", result)
	}
	root, env = pinnedRepository(t, "env:\n  PRAETOR_REF: main\n", false)
	if result := handoffWith(env).handoff(t.Context(), root, preToolRow, nil); !strings.Contains(result.Deny, "no usable pin") {
		t.Errorf("unusable pin = %+v; want a refusal", result)
	}
	root, env = pinnedRepository(t, "env:\n  PRAETOR_REF: "+fixturePin+"\n", true)
	env[pinnedEngineEnv] = fixturePin
	if result := handoffWith(env).handoff(t.Context(), root, preToolRow, nil); result.Handled || result.Deny != "" ||
		result.Skip != "" || result.Note != "" {
		t.Errorf("inside the handed-off engine = %+v; want no handoff", result)
	}
	delete(env, pinnedEngineEnv)
	row := agenthook.Registration{Client: "gemini", Event: "pre-tool"}
	if result := handoffWith(env).handoff(t.Context(), root, row, nil); !strings.Contains(result.Skip, "serves no gemini pre-tool row") {
		t.Errorf("unserved row = %+v; want a stated skip", result)
	}
}
