// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// frameworkComplexity is the framework catalog body setting the HISS-04 limits the shipped
// framework profile sets (.config/archetypes/framework.yaml).
var frameworkComplexity = map[string]string{"framework": "id: \"framework\"\nname: \"Adoption fixture framework\"\n" +
	"complexity:\n  max_cyclomatic: 10\n  max_cognitive: 15\n  max_func_loc: 75\n  max_statements: 50\n"}

// complexityOverrideManifest declares acme/widget with complexity, a repository override block.
func complexityOverrideManifest(complexity string) string {
	return "version: 1\nrepository:\n  owner: acme\n  name: widget\nprofiles:\n  - framework\n" +
		"overrides:\n  complexity:\n" + complexity
}

// TestAdoptHarnessesStateEveryComplexityOverride (#321): both harnesses and rules.md state every
// HISS-04 limit the audit enforces, not the function length alone, and `praetorctl paperclip
// harness` (RepositoryHISSFacts) synthesizes the bytes adoption wrote. Positive: the issue's
// stricter override of all four limits under the framework profile. Negative: a looser
// cyclomatic override cannot loosen the profile's limit, so the profile's stays. Boundary: one
// limit overridden beside the profile's other limits.
func TestAdoptHarnessesStateEveryComplexityOverride(t *testing.T) {
	facets := []string{"security:high", "api:public-contract", "docs:seo-portal", "agent:sandboxed", "custom:facet"}
	cases := map[string]struct {
		override, want string
	}{
		"stricter override": {"    max_cyclomatic: 8\n    max_cognitive: 12\n    max_func_loc: 45\n    max_statements: 40\n",
			"McCabe cyclomatic <= 8, cognitive <= 12, statements <= 40; func LOC <= 45"},
		"looser cyclomatic": {"    max_cyclomatic: 20\n",
			"McCabe cyclomatic <= 10, cognitive <= 15, statements <= 50; func LOC <= 60 (audit ceiling)"},
		"statements only": {"    max_statements: 49\n",
			"McCabe cyclomatic <= 10, cognitive <= 15, statements <= 49; func LOC <= 60 (audit ceiling)"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			source := newCatalogLockSource(t, &config.Manifest{Version: 1, Profiles: []string{"framework"}, Facets: facets}, frameworkComplexity)
			repo := newTestRepo(t, "widget")
			mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/widget\n\ngo 1.27\n")
			mustWrite(t, filepath.Join(repo, manifestFile), complexityOverrideManifest(tc.override))
			adoptFrom(t, repo, "framework", source)
			agents, paperclipRule := adoptedRules(t, repo, "HISS-04")
			if agents != tc.want || paperclipRule != tc.want {
				t.Fatalf("HISS-04: AGENTS.md %q, Paperclip %q; want %q", agents, paperclipRule, tc.want)
			}
			rules := strings.ReplaceAll(mustRead(t, filepath.Join(repo, ".paperclip", "rules.md")), "\n  ", " ")
			if !strings.Contains(rules, "- HISS-04: "+tc.want+"\n") {
				t.Fatalf("rules.md lacks the HISS-04 row %q:\n%s", tc.want, rules)
			}
			assertCLIHarnessMatches(t, repo)
		})
	}
}
