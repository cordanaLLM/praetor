// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hisscatalog"
)

// updateHarnessGolden rewrites the harness goldens from the current rendering:
// go test ./internal/adopt -run TestAdoptedHarnessGolden -update-harness-golden
var updateHarnessGolden = flag.Bool("update-harness-golden", false, "rewrite internal/adopt/testdata/harness-*.golden")

// harnessProfile is one repository shape the golden harness is rendered for.
type harnessProfile struct {
	name, profile string
	markers       map[string]string
	// present and absent are wording the profile's harness must and must not carry (#68).
	present, absent []string
}

// adoptedHarness adopts a fresh acme/widget checkout carrying markers under profile and
// returns the harness part of the AGENTS.md adoption wrote.
func adoptedHarness(t *testing.T, profile string, markers map[string]string) string {
	t.Helper()
	repo := newTestRepo(t, "widget")
	for rel, body := range markers {
		mustWrite(t, filepath.Join(repo, filepath.FromSlash(rel)), body)
	}
	adoptFrom(t, repo, profile, newAdoptLockSource(t))
	return agentsHarness(t, repo)
}

// adoptFrom adopts repo under profile with the pinned catalog at lockSource.
func adoptFrom(t *testing.T, repo, profile, lockSource string) *AdoptReport {
	t.Helper()
	report, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: lockSource, Path: repo, Profile: profile})
	if err != nil {
		t.Fatalf("Adopt %s: %v", profile, err)
	}
	return report
}

// agentsHarness returns the harness part of repo's AGENTS.md, through its end marker.
func agentsHarness(t *testing.T, repo string) string {
	t.Helper()
	content := mustRead(t, filepath.Join(repo, agentsFile))
	end := strings.Index(content, harnessEndMarker)
	if end < 0 {
		t.Fatalf("adopted AGENTS.md has no harness end marker:\n%s", content)
	}
	return content[:end+len(harnessEndMarker)] + "\n"
}

// TestAdoptedHarnessGolden renders the adopted harness for a Go framework, a Rust crate and a
// native C engine and compares each with its golden. Positive: each names the constructs of
// its own language (Go's context.Context, Rust's unwrap, C's goto and libc). Negative: none
// names another language's construct, a review bot, a pull-request re-check or a verify-all
// receipt. Boundary: the function-length limit is the one each profile's audit enforces, and
// the dispatch hook sentence is absent, since adoption registers no pre-dispatch hook.
func TestAdoptedHarnessGolden(t *testing.T) {
	common := []string{"[bot]", "re-checks every pull request", "All pass -> Ed25519", "registered dispatch hook denies"}
	profiles := []harnessProfile{
		{
			name: "go", profile: "framework",
			markers: map[string]string{"go.mod": "module example.com/widget\n\ngo 1.27\n", "internal/widget.go": "package widget\n"},
			present: []string{"Go: I/O takes `context.Context` deadline", "Go: zero unchecked `error` return", "Go: zero `goto`", "func LOC <= 60"},
			absent:  []string{".unwrap()", "Rust:", "libc", "C/C++:"},
		},
		{
			name: "rust", profile: "native-gpu-systems",
			markers: map[string]string{"Cargo.toml": "[package]\nname = \"widget\"\nversion = \"0.1.0\"\n"},
			present: []string{"Rust: zero `.unwrap()` / `.expect()` outside tests", "Rust: `// SAFETY:` proof before every `unsafe` block", "func LOC <= 60"},
			absent:  []string{"context.Context", "Go:", "`goto`", "libc"},
		},
		{
			name: "native-c", profile: "native-gpu-systems",
			markers: map[string]string{"meson.build": "project('widget', 'c')\n"},
			present: []string{"C/C++: zero `goto`", "C/C++: zero banned libc (`gets` / `strcpy` / `sprintf`)", "func LOC <= 60"},
			absent:  []string{"context.Context", "Go:", ".unwrap()", "Rust:", "`unsafe`", "declared exception"},
		},
		{
			name: "native-c-exception", profile: "native-gpu-systems",
			markers: map[string]string{"meson.build": "project('widget', 'c')\n", manifestFile: cleanupGotoManifest,
				"docs/cleanup-goto.md": cleanupGotoDocument},
			present: []string{"C/C++: `goto` only single-level forward jump to function cleanup label (declared exception); audit still reports each `goto`",
				"C/C++: zero banned libc (`gets` / `strcpy` / `sprintf`)", "func LOC <= 60"},
			absent: []string{"zero `goto`", "context.Context", "Go:", ".unwrap()", "Rust:", "`unsafe`"},
		},
	}
	for _, p := range profiles {
		t.Run(p.name, func(t *testing.T) {
			harness := adoptedHarness(t, p.profile, p.markers)
			directives := directiveCells(t, harness)
			for _, want := range p.present {
				if !strings.Contains(directives, want) {
					t.Errorf("rule cells lack %q:\n%s", want, directives)
				}
			}
			for _, word := range p.absent {
				if strings.Contains(directives, word) {
					t.Errorf("rule cells carry %q:\n%s", word, directives)
				}
			}
			for _, word := range common {
				if strings.Contains(harness, word) {
					t.Errorf("harness carries %q", word)
				}
			}
			compareHarnessGolden(t, filepath.Join("testdata", "harness-"+p.name+".golden"), harness)
		})
	}
}

// directiveCells joins the Rule cell of every invariant row, one per line. The Adopted check
// cell describes what the audit scanner decides in each language, so it is not a directive.
func directiveCells(t *testing.T, harness string) string {
	t.Helper()
	rows, err := hisscatalog.ParseGatedInvariants(harness)
	if err != nil {
		t.Fatal(err)
	}
	cells := make([]string, 0, len(rows))
	for _, row := range rows {
		cells = append(cells, row.ID+": "+row.Rule)
	}
	return strings.Join(cells, "\n")
}

// compareHarnessGolden compares got with the golden at path, or rewrites it under
// -update-harness-golden.
func compareHarnessGolden(t *testing.T, path, got string) {
	t.Helper()
	if *updateHarnessGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (regenerate with -update-harness-golden): %v", err)
	}
	if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Errorf("harness differs from %s (regenerate with -update-harness-golden after review):\n%s", path, got)
	}
}
