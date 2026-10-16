package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// cliTwoAliasFixture declares two gateway aliases (light, heavy) next to one pinned model.
const cliTwoAliasFixture = cliLaneFixture +
	"      - {id: gw-heavy, family: openai, provider: gw, alias: heavy, alias_status: answers, cost_per_m_in: 2, cost_per_m_out: 2}\n"

// outcomeArgsWith returns the valid outcome arguments with every flag named in drop removed
// and the extra arguments appended.
func outcomeArgsWith(log string, drop []string, extra ...string) []string {
	args := make([]string, 0, len(extra)+20)
	for _, arg := range validOutcomeCLIArgs(log) {
		if !slices.ContainsFunc(drop, func(name string) bool { return strings.HasPrefix(arg, "--"+name+"=") }) {
			args = append(args, arg)
		}
	}
	return append(args, extra...)
}

// runOutcomeCLI runs models with args and returns stdout, stderr and the command error.
func runOutcomeCLI(t *testing.T, args []string) (string, string, error) {
	t.Helper()
	var stdout string
	stderr, err := captureStderr(t, func() error {
		out, runErr := captureStdout(t, func() error { return runModels(args) })
		stdout = out
		return runErr
	})
	return stdout, stderr, err
}

// readOutcomeLogLines returns the raw lines of the outcome log at path.
func readOutcomeLogLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func TestModelsOutcomeCLIUndeclaredAliasWithoutPhysicalModelFails(t *testing.T) {
	dir := t.TempDir()
	cfg := writeRouteCLIInput(t, cliRouteFixture)
	for name, extra := range map[string][]string{
		"no catalog loaded":         nil,
		"catalog without the alias": {"--config=" + cfg},
	} {
		log := filepath.Join(dir, name, "outcomes.jsonl")
		args := outcomeArgsWith(log, []string{"physical-model", "target"}, append([]string{"--target=cordana-coding"}, extra...)...)
		_, _, err := runOutcomeCLI(t, args)
		if err == nil || !strings.Contains(err.Error(), "--physical-model") {
			t.Errorf("%s: alias target without --physical-model must fail naming the flag, got %v", name, err)
		}
		if _, statErr := os.Stat(log); statErr == nil {
			t.Errorf("%s: a refused outcome created the log", name)
		}
	}
}

func TestModelsOutcomeCLIPhysicalModelRefusesEveryCatalogAlias(t *testing.T) {
	dir := t.TempDir()
	cfg := writeRouteCLIInput(t, cliTwoAliasFixture)
	refused := map[string][2]string{
		"other alias name":        {"light", "heavy"},
		"other alias catalog id":  {"light", "gw-heavy"},
		"own alias name":          {"light", "light"},
		"own alias catalog id":    {"light", "gw-light"},
		"pinned target and alias": {"pinned-cheap", "light"},
		"undeclared target alias": {"cordana-coding", "gw-heavy"},
	}
	for name, pair := range refused {
		log := filepath.Join(dir, "refused", "outcomes.jsonl")
		args := outcomeArgsWith(log, []string{"physical-model", "target"}, "--config="+cfg, "--target="+pair[0], "--physical-model="+pair[1])
		if _, _, err := runOutcomeCLI(t, args); err == nil {
			t.Errorf("%s: --target=%s --physical-model=%s accepted", name, pair[0], pair[1])
		}
	}
	accepted := map[string][2]string{
		"alias resolved":        {"light", "claude-3-5-sonnet"},
		"pinned target as-is":   {"pinned-cheap", "pinned-cheap"},
		"pinned provider alias": {"pinned-cheap", "pinned-cheap-001"},
	}
	for name, pair := range accepted {
		log := filepath.Join(dir, "accepted", "outcomes.jsonl")
		args := outcomeArgsWith(log, []string{"physical-model", "target"}, "--config="+cfg, "--target="+pair[0], "--physical-model="+pair[1])
		if _, _, err := runOutcomeCLI(t, args); err != nil {
			t.Errorf("%s: --target=%s --physical-model=%s refused: %v", name, pair[0], pair[1], err)
		}
	}
}

func TestModelsOutcomeCLIMissingEstimateStaysAbsent(t *testing.T) {
	log := filepath.Join(t.TempDir(), "outcomes.jsonl")
	_, stderr, err := runOutcomeCLI(t, outcomeArgsWith(log, []string{"cost-estimate", "actual-cost"}, "--actual-cost=0.5"))
	if err != nil {
		t.Fatal(err)
	}
	lines := readOutcomeLogLines(t, log)
	if len(lines) != 1 || strings.Contains(lines[0], `"cost_estimate"`) || strings.Contains(lines[0], `"estimate_error"`) {
		t.Fatalf("a missing estimate must stay absent, never zero: %v", lines)
	}
	if !strings.Contains(stderr, "not measured") || strings.Contains(stderr, "+0.5000") {
		t.Fatalf("estimate-error without an estimate must read not measured: %q", stderr)
	}
	reconciled, _, err := runOutcomeCLI(t, []string{"outcome", "--reconcile", "--outcome-log=" + log})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reconciled, "not measured") || strings.Contains(reconciled, "$0.0000") {
		t.Fatalf("reconciliation must not count a missing estimate as zero: %q", reconciled)
	}
}

func TestModelsOutcomeCLIZeroEstimateIsMeasured(t *testing.T) {
	log := filepath.Join(t.TempDir(), "outcomes.jsonl")
	_, stderr, err := runOutcomeCLI(t, outcomeArgsWith(log, []string{"cost-estimate", "actual-cost"}, "--cost-estimate=0", "--actual-cost=0.5"))
	if err != nil {
		t.Fatal(err)
	}
	lines := readOutcomeLogLines(t, log)
	if len(lines) != 1 || !strings.Contains(lines[0], `"cost_estimate":0`) || !strings.Contains(lines[0], `"estimate_error":0.5`) {
		t.Fatalf("an explicit zero estimate must be recorded: %v", lines)
	}
	if !strings.Contains(stderr, "estimate-error [gateway-coding]: +0.5000\n") {
		t.Fatalf("zero estimate renders the signed error without a percentage: %q", stderr)
	}
}

// realMCPToolNames returns n distinct tool names at the length MCP servers produce, for
// example mcp__plugin_cloudflare_cloudflare-observability__query_worker_observability.
func realMCPToolNames(n int) []string {
	prefixes := []string{
		"mcp__plugin_cloudflare_cloudflare-observability__query_worker_observability",
		"mcp__cordana-mcp__openalex-analyze_geographic_distribution",
		"mcp__plugin_cloudflare_cloudflare-bindings__hyperdrive_config_delete",
		"mcp__cordana-mcp__wikipedia-wikipedia_summarize_article_for_query",
	}
	names := make([]string, n)
	for i := 0; i < n; i++ {
		names[i] = fmt.Sprintf("%s_%03d", prefixes[i%len(prefixes)], i)
	}
	return names
}

func TestModelsOutcomeCLILargeToolSetStaysUnderTheRecordBound(t *testing.T) {
	log := filepath.Join(t.TempDir(), "outcomes.jsonl")
	tools := realMCPToolNames(200)
	if _, _, err := runOutcomeCLI(t, outcomeArgsWith(log, []string{"tools"}, "--tools="+strings.Join(tools, ","))); err != nil {
		t.Fatalf("a session with 200 real-length tool names must be recordable: %v", err)
	}
	slices.Reverse(tools)
	if _, _, err := runOutcomeCLI(t, outcomeArgsWith(log, []string{"tools"}, "--tools="+strings.Join(tools, ","))); err != nil {
		t.Fatal(err)
	}
	lines := readOutcomeLogLines(t, log)
	if len(lines) != 2 {
		t.Fatalf("expected two records, got %d", len(lines))
	}
	for _, line := range lines {
		if len(line) >= 4096 || !strings.Contains(line, `"tool_count":200`) || !strings.Contains(line, `"tool_set_digest":"sha256:`) || strings.Contains(line, tools[0]) {
			t.Fatalf("record must hold the tool-set digest and count, not the list, under 4096 B: %d B %s", len(line), line)
		}
	}
	if digestOf(lines[0]) != digestOf(lines[1]) {
		t.Fatal("the tool-set digest must not depend on the order the tools were listed in")
	}
}

// digestOf extracts the tool_set_digest value of one raw outcome line.
func digestOf(line string) string {
	_, rest, _ := strings.Cut(line, `"tool_set_digest":"`)
	digest, _, _ := strings.Cut(rest, `"`)
	return digest
}

func TestModelsOutcomeCLIRecordToolListKeepsTheListOrRefuses(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "outcomes.jsonl")
	if _, _, err := runOutcomeCLI(t, outcomeArgsWith(log, nil, "--record-tool-list")); err != nil {
		t.Fatal(err)
	}
	lines := readOutcomeLogLines(t, log)
	if len(lines) != 1 || !strings.Contains(lines[0], `"tool_set":["read","write"]`) || !strings.Contains(lines[0], `"tool_count":2`) {
		t.Fatalf("--record-tool-list must keep the list next to digest and count: %v", lines)
	}
	tooLong := outcomeArgsWith(log, []string{"tools"}, "--record-tool-list", "--tools="+strings.Join(realMCPToolNames(200), ","))
	if _, _, err := runOutcomeCLI(t, tooLong); err == nil || !strings.Contains(err.Error(), "exceeds its bound") {
		t.Fatalf("a kept tool list past the record bound must be refused with its reason, got %v", err)
	}
	if again := readOutcomeLogLines(t, log); len(again) != 1 {
		t.Fatalf("a refused record changed the log: %d lines", len(again))
	}
}

func TestModelsOutcomeCLIMissingCatalogIsReported(t *testing.T) {
	log := filepath.Join(t.TempDir(), "outcomes.jsonl")
	_, stderr, err := runOutcomeCLI(t, outcomeArgsWith(log, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "not checked against catalog aliases") {
		t.Fatalf("an unchecked --physical-model must be reported: %q", stderr)
	}
}
