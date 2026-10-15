// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/enginepin"
)

// The pin rule has two texts: the launcher, which resolves it before any engine exists, and
// enginepin, which agent hooks use on every tool call without a shell (#1173). These cases run
// both over the same workflow fixtures and fail where they disagree.

// pinCase is one fixture: workflow files by name, and what both resolutions must agree on.
type pinCase struct {
	name      string
	workflows map[string]string
}

var pinCases = []pinCase{
	{"no workflows", nil},
	{"declared colon", map[string]string{"gate.yml": "env:\n  PRAETOR_REF: 492a00f930e1\n"}},
	{"declared equals", map[string]string{"gate.yml": "run: PRAETOR_REF=492a00f echo\n"}},
	{"declared quoted tag", map[string]string{"gate.yml": "env:\n  PRAETOR_REF: \"v1.2.3\"\n"}},
	{"declared single quoted with comment", map[string]string{"gate.yml": "env:\n  PRAETOR_REF: 'v2.0.0-rc.1' # pinned\n"}},
	{"declared build metadata", map[string]string{"gate.yaml": "env:\n  PRAETOR_REF: v1.2.3+build.5\n"}},
	{"declared CRLF", map[string]string{"gate.yml": "env:\r\n  PRAETOR_REF: 492a00f930e1\r\n"}},
	{"declared branch is a fault", map[string]string{"gate.yml": "env:\n  PRAETOR_REF: main\n"}},
	{"declared empty is a fault", map[string]string{"gate.yml": "env:\n  PRAETOR_REF:\n"}},
	{"declared short hex is a fault", map[string]string{"gate.yml": "env:\n  PRAETOR_REF: 492a00\n"}},
	{"declared long hex is a fault", map[string]string{"gate.yml": "env:\n  PRAETOR_REF: " + strings.Repeat("a", 41) + "\n"}},
	{"declared two-part tag is a fault", map[string]string{"gate.yml": "env:\n  PRAETOR_REF: v1.2\n"}},
	{"declared bad suffix is a fault", map[string]string{"gate.yml": "env:\n  PRAETOR_REF: v1.2.3_x\n"}},
	{"declared twice the same", map[string]string{"a.yml": "env:\n  PRAETOR_REF: 492a00f930e1\n", "b.yml": "env:\n  PRAETOR_REF: 492a00f930e1\n"}},
	{"declared twice differing", map[string]string{"a.yml": "env:\n  PRAETOR_REF: 492a00f930e1\n", "b.yml": "env:\n  PRAETOR_REF: v1.2.3\n"}},
	{"uses adopt action", map[string]string{"adopt.yml": "steps:\n  - uses: cordanaLLM/praetor/.github/actions/praetor-adopt@492a00f930e1\n"}},
	{"uses reusable workflow mixed case", map[string]string{"gate.yml": "jobs:\n  gate:\n    uses: CordanaLLM/Praetor/.github/workflows/gate.yml@v3.1.4 # tag\n"}},
	{"uses branch is unpinned", map[string]string{"gate.yml": "jobs:\n  gate:\n    uses: cordanaLLM/praetor/.github/workflows/gate.yml@main\n"}},
	{"uses quoted", map[string]string{"adopt.yml": "steps:\n  - uses: 'cordanallm/praetor/.github/actions/praetor-adopt@v1.0.0'\n"}},
	{"declared wins over uses", map[string]string{"a.yml": "env:\n  PRAETOR_REF: 492a00f930e1\n", "b.yml": "steps:\n  - uses: cordanaLLM/praetor/.github/actions/praetor-adopt@v9.9.9\n"}},
	{"uses two pins differing", map[string]string{"a.yml": "steps:\n  - uses: cordanaLLM/praetor/.github/actions/praetor-adopt@v1.0.0\n", "b.yml": "steps:\n  - uses: cordanaLLM/praetor/.github/actions/praetor-adopt@v1.0.1\n"}},
	{"other action ignored", map[string]string{"ci.yml": "steps:\n  - uses: actions/checkout@v4\n"}},
	{"not a workflow file", map[string]string{"notes.txt": "PRAETOR_REF: 492a00f930e1\n"}},
}

// launcherVerdict is what the launcher prints for one fixture: "pin <ref>", "unpinned" or
// "fault".
func launcherVerdict(t *testing.T, sh, repo, cache, stubs string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), sh, filepath.FromSlash(engineLauncherFile), "--print-path")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cache, "PATH="+stubs+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.Output()
	if err != nil {
		return "fault"
	}
	printed := filepath.ToSlash(strings.TrimSpace(string(out)))
	if dir := filepath.ToSlash(filepath.Join(cache, "praetor", "engine")); strings.HasPrefix(printed, dir+"/") {
		return "pin " + strings.SplitN(strings.TrimPrefix(printed, dir+"/"), "/", 2)[0]
	}
	return "unpinned"
}

func resolverVerdict(t *testing.T, repo string) string {
	t.Helper()
	resolution, err := enginepin.Resolve(repo)
	switch {
	case err != nil, resolution.Fault != "":
		return "fault"
	case resolution.Pin != "":
		return "pin " + resolution.Pin
	}
	return "unpinned"
}

// TestEnginePin_Differential_ResolverAgreesWithTheLauncher runs both texts of the pin rule.
// Every ref a fixture names is cached, so the launcher never installs; a go stub that fails
// stands in for an install the launcher would start on a ref the resolver did not predict.
func TestEnginePin_Differential_ResolverAgreesWithTheLauncher(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("the launcher is a POSIX shell script and no sh is on PATH: %v", err)
	}
	stubs := t.TempDir()
	writeStub(t, stubs, "go", "exit 3\n")
	writeStub(t, stubs, "praetorctl", "echo path-engine\n")
	for _, tc := range pinCases {
		t.Run(tc.name, func(t *testing.T) {
			repo, cache := t.TempDir(), t.TempDir()
			mustWrite(t, filepath.Join(repo, filepath.FromSlash(engineLauncherFile)), engineLauncherScript)
			for name, text := range tc.workflows {
				mustWrite(t, filepath.Join(repo, ".github", "workflows", name), text)
				for _, ref := range strings.FieldsFunc(text, func(r rune) bool { return strings.ContainsRune(" \t\r\n:=@'\"#", r) }) {
					if dir := filepath.Join(cache, "praetor", "engine", ref); filepath.Base(dir) == ref && ref != "" {
						if err := os.MkdirAll(dir, 0o755); err != nil {
							t.Fatal(err)
						}
						writeStub(t, dir, "standardsctl", "echo pinned\n")
					}
				}
			}
			launcher, resolver := launcherVerdict(t, sh, repo, cache, stubs), resolverVerdict(t, repo)
			if launcher != resolver {
				t.Fatalf("launcher %q, resolver %q", launcher, resolver)
			}
		})
	}
}
