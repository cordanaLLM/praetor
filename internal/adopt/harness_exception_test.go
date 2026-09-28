// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"github.com/cordanaLLM/praetor/internal/paperclip"
)

// cleanupGotoClause is HISS-01's exception clause for a C repository declaring it.
const cleanupGotoClause = "C/C++: `goto` only single-level forward jump to function cleanup label (declared exception); HISS-01 audit ignores exception: each new `goto` still fails gate"

// adoptedRules returns the Rule cell of id in the AGENTS.md harness of repo and the Paperclip
// invariant of id adoption wrote there, without its "id: " prefix.
func adoptedRules(t *testing.T, repo, id string) (agents, paperclipRule string) {
	t.Helper()
	rows, err := hisscatalog.ParseGatedInvariants(agentsHarness(t, repo))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == id {
			agents = row.Rule
		}
	}
	harness, err := paperclip.LoadHarness(filepath.Join(repo, filepath.FromSlash(paperclipFile)))
	if err != nil {
		t.Fatal(err)
	}
	for _, invariant := range harness.Invariants {
		if rule, ok := strings.CutPrefix(invariant, id+": "); ok {
			paperclipRule = rule
		}
	}
	if agents == "" || paperclipRule == "" {
		t.Fatalf("%s missing: AGENTS.md %q, Paperclip %q", id, agents, paperclipRule)
	}
	return agents, paperclipRule
}

// TestAdoptHonoursDocumentedCleanupGotoException: Positive: a C repository whose manifest
// declares hiss.exceptions.c_goto_cleanup and carries the document it names reads the exception
// in both harnesses instead of the zero-goto ban (#68). Negative: the same declaration without
// its document keeps the ban and warns, naming the document; a Go repository declaring it keeps
// Go's own ban and reads no C clause. Boundary: a Go + C repository keeps Go's ban beside C's
// exception.
func TestAdoptHonoursDocumentedCleanupGotoException(t *testing.T) {
	goMarkers := map[string]string{"go.mod": "module example.com/widget\n\ngo 1.27\n"}
	mixed := map[string]string{"go.mod": goMarkers["go.mod"], "meson.build": cMarkers["meson.build"]}
	cases := map[string]struct {
		markers    map[string]string
		documented bool
		want       string
		absent     []string
	}{
		"documented C":   {cMarkers, true, "recursion prohibited; call graph = DAG; " + cleanupGotoClause, []string{"zero `goto`"}},
		"undocumented C": {cMarkers, false, "recursion prohibited; call graph = DAG; C/C++: zero `goto`", []string{"declared exception"}},
		"documented Go":  {goMarkers, true, "recursion prohibited; call graph = DAG; Go: zero `goto`", []string{"C/C++", "declared exception"}},
		"Go and C":       {mixed, true, "recursion prohibited; call graph = DAG; Go: zero `goto`; " + cleanupGotoClause, nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			repo := cleanupGotoRepo(t, newTestRepo(t, "widget"), tc.markers, tc.documented)
			report := adoptFrom(t, repo, "native-gpu-systems", newAdoptLockSource(t))
			agents, paperclipRule := adoptedRules(t, repo, "HISS-01")
			if agents != tc.want || paperclipRule != tc.want {
				t.Fatalf("HISS-01: AGENTS.md %q, Paperclip %q; want %q", agents, paperclipRule, tc.want)
			}
			for _, word := range tc.absent {
				if strings.Contains(agents+paperclipRule, word) {
					t.Errorf("HISS-01 carries %q: %q", word, agents)
				}
			}
			warned := strings.Contains(strings.Join(report.Warnings, "\n"), "hiss.exceptions.c_goto_cleanup names docs/cleanup-goto.md")
			if warned == tc.documented {
				t.Errorf("undocumented-exception warning = %v, want %v: %q", warned, !tc.documented, report.Warnings)
			}
		})
	}
}

// funcLOCProfile is a framework catalog body setting only the function length, or none for "".
func funcLOCProfile(limit string) map[string]string {
	if limit == "" {
		return nil
	}
	return map[string]string{"framework": "id: \"framework\"\nname: \"Adoption fixture framework\"\ncomplexity:\n  max_func_loc: " + limit + "\n"}
}

// TestAdoptHarnessesStateTheAuditFunctionLength: both harnesses state the function length
// `praetorctl audit` enforces, resolved by the loader and audit layer the audit uses. Positive:
// a stricter pinned profile (50) and a stricter repository override (45) are stated alone.
// Negative: a pinned profile allowing 75 is not restated; the harnesses state the 60-line audit
// ceiling and that it caps the profile value (#68). Boundary: the default profile resolves to the
// ceiling itself. The Paperclip harness adoption bound before the policy resolved is the one
// `praetorctl paperclip harness` synthesizes afterwards, byte for byte.
func TestAdoptHarnessesStateTheAuditFunctionLength(t *testing.T) {
	facets := []string{"security:high", "api:public-contract", "docs:seo-portal", "agent:sandboxed", "custom:facet"}
	cases := map[string]struct {
		profileLimit, override string
		want                   int
	}{
		"default profile":     {"", "", config.AuditMaxFuncLOC},
		"looser profile":      {"75", "", config.AuditMaxFuncLOC},
		"stricter profile":    {"50", "", 50},
		"repository override": {"", "45", 45},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			source := newCatalogLockSource(t, &config.Manifest{Version: 1, Profiles: []string{"framework"}, Facets: facets}, funcLOCProfile(tc.profileLimit))
			repo := newTestRepo(t, "widget")
			mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/widget\n\ngo 1.27\n")
			if tc.override != "" {
				mustWrite(t, filepath.Join(repo, manifestFile), "version: 1\nrepository:\n  owner: acme\n  name: widget\nprofiles:\n  - framework\n"+
					"overrides:\n  complexity:\n    max_func_loc: "+tc.override+"\n")
			}
			adoptFrom(t, repo, "framework", source)
			audit, err := config.LoadEffectivePolicyContext(t.Context(), config.EffectiveOptions{Root: repo, Audit: true})
			if err != nil || audit.Policy.Complexity.MaxFuncLOC != tc.want {
				t.Fatalf("audit policy: %v, %v; want function length %d", audit, err, tc.want)
			}
			want := fmt.Sprintf("; func LOC <= %d", tc.want)
			if tc.want == config.AuditMaxFuncLOC {
				want += " (audit ceiling; caps profile value)"
			}
			agents, paperclipRule := adoptedRules(t, repo, "HISS-04")
			if !strings.HasSuffix(agents, want) || !strings.HasSuffix(paperclipRule, want) {
				t.Fatalf("HISS-04: AGENTS.md %q, Paperclip %q; want suffix %q", agents, paperclipRule, want)
			}
			assertCLIHarnessMatches(t, repo)
		})
	}
}

// assertCLIHarnessMatches synthesizes repo's Paperclip harness from the facts `praetorctl
// paperclip harness` reads and compares it with the one adoption wrote.
func assertCLIHarnessMatches(t *testing.T, repo string) {
	t.Helper()
	facts, warnings, err := RepositoryHISSFacts(t.Context(), repo)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("RepositoryHISSFacts: %v %q", err, warnings)
	}
	synthesized, err := paperclip.SynthesizeHarness(t.Context(), repo, facts)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := paperclip.MarshalHarness(synthesized)
	if err != nil {
		t.Fatal(err)
	}
	if written := mustRead(t, filepath.Join(repo, filepath.FromSlash(paperclipFile))); written != string(fresh) {
		t.Fatalf("CLI synthesis differs from adoption:\n%s\nwant\n%s", fresh, written)
	}
}
