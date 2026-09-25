package config

import (
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// goOnlyTools names linters and generators that only read Go source. A facet is a
// cross-cutting modifier any profile may select, whatever its runtime, so a facet naming one
// of these puts a Go tool into, for example, a Rust repository's resolved policy. Language
// tooling belongs to the profile, which declares a runtime.
var goOnlyTools = map[string]bool{
	"benchstat": true, "errcheck": true, "gocognit": true, "gocyclo": true,
	"golangci-lint": true, "gosec": true, "govet": true, "oapi-codegen": true,
	"revive": true, "staticcheck": true,
}

// canonicalHISSRow matches one invariant row of AGENTS.md's "Core Directives & Invariants"
// table, the set the repository gates.
var canonicalHISSRow = regexp.MustCompile(`(?m)^\| \*\*(HISS-\d{2})\*\*`)

var hissCitation = regexp.MustCompile(`HISS-\d{2}`)

// extendedSpecPath is where HISS invariants outside the gated table are defined.
const extendedSpecPath = "docs/standards/hiss-spec.md"

// shippedFacetIndex indexes the repository's own facets exactly as the lockfile audit does,
// keyed by declared id.
func shippedFacetIndex(t *testing.T) map[string]string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	_, facets, err := archetypeSources(t.Context(), root)
	if err != nil {
		t.Fatalf("index shipped catalog: %v", err)
	}
	if len(facets) == 0 {
		t.Fatal("no shipped facets indexed; the catalog path is wrong")
	}
	return facets
}

// facetMember returns one top-level field of a facet, read with the policy decoder: a scalar
// as its value, a sequence as its string items.
func facetMember(t *testing.T, path, key string) (string, []string) {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- path comes from the shipped catalog index.
	if err != nil {
		t.Fatal(err)
	}
	doc, err := decodePolicyDocument(t.Context(), data)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	node := policyMember(doc, key)
	if node == nil {
		return "", nil
	}
	if node.Kind != yaml.SequenceNode {
		return node.Value, nil
	}
	var list []string
	if err := node.Decode(&list); err != nil {
		t.Fatalf("decode %s %s: %v", path, key, err)
	}
	return "", list
}

// TestShippedFacets_Positive_EveryFacetDeclaresItsID pins the identity rule the authoring
// guide states: a facet is named by its id field, never by its file name, so every shipped
// facet carries one and the index key equals it.
func TestShippedFacets_Positive_EveryFacetDeclaresItsID(t *testing.T) {
	for id, path := range shippedFacetIndex(t) {
		data, err := os.ReadFile(path) // #nosec G304 -- path comes from the shipped catalog index.
		if err != nil {
			t.Fatal(err)
		}
		if declared := archetypeID(string(data)); declared != id {
			t.Errorf("%s is indexed as %q but declares id %q", filepath.Base(path), id, declared)
		}
	}
}

// TestShippedFacets_Negative_NoGoOnlyLinter keeps language tooling out of cross-cutting
// facets. agent:sandboxed once listed gocyclo, perf:hotpath benchstat and
// api:public-contract oapi-codegen.
func TestShippedFacets_Negative_NoGoOnlyLinter(t *testing.T) {
	facets := shippedFacetIndex(t)
	if _, ok := facets["agent:sandboxed"]; !ok {
		t.Fatal("agent:sandboxed is not indexed; the guard would pass vacuously")
	}
	for _, id := range slices.Sorted(maps.Keys(facets)) {
		_, linters := facetMember(t, facets[id], "linters")
		for _, linter := range linters {
			if goOnlyTools[linter] {
				t.Errorf("facet %s lists Go-only tool %q", id, linter)
			}
		}
	}
}

// TestShippedFacets_Negative_UngatedHISSCitesTheExtendedSpec rejects a facet description that
// cites a HISS invariant absent from AGENTS.md's gated table as though it were gated. Such a
// citation must name the extended spec that defines it.
func TestShippedFacets_Negative_UngatedHISSCitesTheExtendedSpec(t *testing.T) {
	agents, err := os.ReadFile(filepath.Join("..", "..", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	gated := make(map[string]bool)
	for _, match := range canonicalHISSRow.FindAllStringSubmatch(string(agents), -1) {
		gated[match[1]] = true
	}
	if len(gated) == 0 || gated["HISS-03"] || gated["HISS-14"] {
		t.Fatalf("AGENTS.md invariant table parsed as %v; the fixture assumption is stale", gated)
	}
	facets := shippedFacetIndex(t)
	for _, id := range slices.Sorted(maps.Keys(facets)) {
		description, _ := facetMember(t, facets[id], "description")
		for _, cited := range hissCitation.FindAllString(description, -1) {
			if !gated[cited] && !strings.Contains(description, extendedSpecPath) {
				t.Errorf("facet %s cites %s, which AGENTS.md does not gate, without naming %s", id, cited, extendedSpecPath)
			}
		}
	}
}

// TestFacetIndex_Boundary_ResolvesByDeclaredIDNotFileName covers both sides of the naming
// rule: a declared id wins over the file name, and only a file without one falls back to its
// name minus ".yaml".
func TestFacetIndex_Boundary_ResolvesByDeclaredIDNotFileName(t *testing.T) {
	root := t.TempDir()
	named := writePolicyFile(t, root, ".config/archetypes/facets/short.yaml", "id: \"team:long-identity\"\n")
	bare := writePolicyFile(t, root, ".config/archetypes/facets/no-id.yaml", "name: \"No id\"\n")
	_, facets, err := archetypeSources(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if got := facets["team:long-identity"]; got != named {
		t.Errorf("declared id resolved to %q, want %q", got, named)
	}
	if _, ok := facets["short"]; ok {
		t.Error("file name of a facet with a declared id became an identity")
	}
	if got := facets["no-id"]; got != bare {
		t.Errorf("facet without id resolved to %q, want %q", got, bare)
	}
	if len(facets) != 2 {
		t.Errorf("index = %v, want exactly two entries", facets)
	}
}
