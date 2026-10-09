package main

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/cavemansource"
	"github.com/cordanaLLM/praetor/internal/config"
)

func TestMCPRuntimeDescriptionsMatchSourceCensus(t *testing.T) {
	root := sourceCheckoutRoot(t)
	input := config.RegisterSourceInput{Path: "cmd/standards-mcp", Surface: config.SurfaceMCP,
		Kind: "message", Format: config.SourceFormatGo, Selector: "mcp.descriptions"}
	result, err := cavemansource.ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(root, "test")
	if err != nil {
		t.Fatal(err)
	}
	want := make(map[string]int, len(result.Sources))
	for _, source := range result.Sources {
		want[source.Text]++
	}
	got := make(map[string]int, len(result.Sources))
	for _, tool := range server.tools {
		got[tool.Description]++
		for _, property := range tool.InputSchema.Properties {
			got[property.Description]++
		}
	}
	if len(result.Sources) != 102 || !equalTextCensus(want, got) {
		t.Fatalf("runtime description census differs: extracted=%d runtime=%d", len(result.Sources), censusSize(got))
	}
}

func TestMCPRuntimeOutputsHaveNoUnclassifiedCallsites(t *testing.T) {
	root := sourceCheckoutRoot(t)
	callsites, inventory, err := independentMCPOutputCallsites(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, callsite := range callsites {
		kinds[callsite.kind]++
	}
	wantKinds := map[string]int{"builder-append": 1, "builder-external": 12, "builder-template": 50,
		"classified-result": 79, "governed-result": 17, "http-error": 14, "result": 42,
		"template": 1, "wire-format": 4}
	if len(callsites) != 220 || !equalTextCensus(kinds, wantKinds) {
		t.Fatalf("independent MCP output inventory drift: records=%d kinds=%v", len(callsites), kinds)
	}
	productionKinds, productionCount := productionOutputCensus(t, root)
	if productionCount != len(callsites) || !equalTextCensus(productionKinds, kinds) {
		t.Fatalf("production MCP output extraction differs: production=%d kinds=%v independent=%d kinds=%v",
			productionCount, productionKinds, len(callsites), kinds)
	}
	digest := outputCallsiteDigest(callsites, inventory)
	if digest != expectedMCPOutputCallsiteDigest {
		t.Fatalf("independent MCP output identities drift: got %s want %s", digest, expectedMCPOutputCallsiteDigest)
	}
	t.Logf("independent MCP output census: %d records kinds=%v identities=%s", len(callsites), kinds, digest)
}

func productionOutputCensus(t *testing.T, root string) (map[string]int, int) {
	t.Helper()
	input := config.RegisterSourceInput{Path: "cmd/standards-mcp", Surface: config.SurfaceMCP,
		Kind: "message", Format: config.SourceFormatGo, Selector: "mcp.outputs"}
	result, err := cavemansource.ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input})
	if err != nil {
		t.Fatal(err)
	}
	kinds := make(map[string]int)
	for _, source := range result.Sources {
		parts := strings.Split(source.Selector, ":")
		if len(parts) != 3 || parts[0] != "mcp" {
			t.Fatalf("malformed production output selector %q", source.Selector)
		}
		kinds[parts[1]]++
	}
	return kinds, len(result.Sources)
}

func TestRuntimeSourceInventoryIsBoundedAndComplete(t *testing.T) {
	root := sourceCheckoutRoot(t)
	inputs := []config.RegisterSourceInput{
		{Path: ".paperclip/harness.json", Surface: config.SurfacePrompts, Kind: "message", Format: config.SourceFormatJSON, Selector: "operating_contract.*"},
		{Path: ".paperclip/harness.json", Surface: config.SurfacePrompts, Kind: "message", Format: config.SourceFormatJSON, Selector: "invariants.*"},
		{Path: ".config/semgrep/hiss-invariants.yml", Surface: config.SurfaceHooks, Kind: "message", Format: config.SourceFormatYAML, Selector: "rules.*.message"},
		{Path: ".config/agent/hooks", Surface: config.SurfaceHooks, Kind: "message", Format: config.SourceFormatPython},
		{Path: ".config/lefthook/scripts", Surface: config.SurfaceHooks, Kind: "message", Format: config.SourceFormatPython},
		{Path: "cmd/standards-mcp", Surface: config.SurfaceMCP, Kind: "message", Format: config.SourceFormatGo, Selector: "mcp.descriptions"},
		{Path: "cmd/standards-mcp", Surface: config.SurfaceMCP, Kind: "message", Format: config.SourceFormatGo, Selector: "mcp.outputs"},
	}
	all, applicable, excluded := 0, 0, 0
	for _, input := range inputs {
		part, partErr := cavemansource.ExtractInputs(t.Context(), root, []config.RegisterSourceInput{input})
		if partErr != nil {
			t.Fatal(partErr)
		}
		partApplicable := 0
		for _, source := range part.Sources {
			if source.NotApplicable == "" {
				partApplicable++
			}
		}
		t.Logf("source scope %s %s: all=%d applicable=%d", input.Path, input.Selector, len(part.Sources), partApplicable)
		all += len(part.Sources)
		applicable += partApplicable
		excluded += len(part.Sources) - partApplicable
	}
	t.Logf("source totals: all=%d applicable=%d excluded=%d", all, applicable, excluded)
	result, err := cavemansource.ExtractInputs(t.Context(), root, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if result.Applicable > config.MaxRegisterSourceOutputs || result.NotApplicable > config.MaxRegisterSourceOutputs {
		t.Fatalf("runtime source inventory exceeds bounds: applicable=%d not_applicable=%d",
			result.Applicable, result.NotApplicable)
	}
	violations := 0
	for _, source := range result.Sources {
		if source.NotApplicable != "" {
			continue
		}
		report := caveman.Check(source.Text, caveman.Options{Kind: source.Kind})
		if report.Passed() {
			continue
		}
		violations++
		if violations <= 20 {
			t.Errorf("%s %s text=%q: %v", source.Path, source.Selector, source.Text, report.Findings)
		}
	}
	if violations > 20 {
		t.Errorf("plus %d additional Caveman source failures", violations-20)
	}
	t.Logf("runtime source inventory: applicable=%d not_applicable=%d records=%d digest=%s",
		result.Applicable, result.NotApplicable, len(result.Sources), result.SHA256)
}

func sourceCheckoutRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve source checkout")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func equalTextCensus(left, right map[string]int) bool {
	if len(left) != len(right) {
		return false
	}
	for text, count := range left {
		if right[text] != count {
			return false
		}
	}
	return true
}

func censusSize(values map[string]int) int {
	total := 0
	for _, count := range values {
		total += count
	}
	return total
}
