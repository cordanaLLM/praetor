package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
)

const volatileHeadAgents = "# Harness\n\n<!-- praetor:head -->\nBuilt 2026-10-07T12:30:00Z.\n\n<!-- praetor:tail -->\nCommands.\n"

// compiledFixture writes source as AGENTS.md of a fixture repository and compiles it, so the
// targets are in sync and only the cache stability gate can still refuse the source.
func compiledFixture(t *testing.T, source string) string {
	t.Helper()
	dir := newContextFixture(t, false)
	writeFixtureFile(t, dir, "AGENTS.md", source)
	if out, err := runCompileContextCmd(t, dir); err != nil {
		t.Fatalf("compile-context: %v\n%s", err, out)
	}
	return dir
}

// Negative (rule 13): compile-context --verify refuses a timestamp in the head band; the
// VerifyStableContext call in VerifyCompiledContext is the only check that can.
func TestCompileContextVerify_Negative_RefusesVolatileHeadBand(t *testing.T) {
	dir := compiledFixture(t, volatileHeadAgents)
	out, err := runCompileContextCmd(t, dir, "--verify")
	if err == nil || !strings.Contains(err.Error(), "head line") || !strings.Contains(err.Error(), "timestamp") {
		t.Fatalf("--verify accepted a planted head timestamp: %v\n%s", err, out)
	}
}

// Positive: the same source without the planted token passes --verify.
func TestCompileContextVerify_Positive_StableHeadBandPasses(t *testing.T) {
	dir := compiledFixture(t, strings.Replace(volatileHeadAgents, "Built 2026-10-07T12:30:00Z.", "Static rule.", 1))
	if out, err := runCompileContextCmd(t, dir, "--verify"); err != nil {
		t.Fatalf("--verify refused a stable head: %v\n%s", err, out)
	}
}

// Boundary: --verify on an unmarked source passes and prints the unlayered warning.
func TestCompileContextVerify_Boundary_UnmarkedSourceWarns(t *testing.T) {
	dir := compiledFixture(t, fixtureAgentsMD)
	out, err := runCompileContextCmd(t, dir, "--verify")
	if err != nil {
		t.Fatalf("--verify refused an unmarked source: %v\n%s", err, out)
	}
	mustContain(t, out, compiler.UnlayeredWarning)
}

func auditContextOptions(dir string) *auditOptions {
	return &auditOptions{rootDir: dir, agentsPath: filepath.Join(dir, "AGENTS.md")}
}

// Negative (rule 13): the audit agent-context check refuses a volatile head band.
func TestAuditAgentContext_Negative_RefusesVolatileHeadBand(t *testing.T) {
	dir := compiledFixture(t, volatileHeadAgents)
	var err error
	out, captureErr := captureStdout(t, func() error {
		err = auditAgentContext(t.Context(), &config.Manifest{}, auditContextOptions(dir))
		return nil
	})
	if captureErr != nil {
		t.Fatal(captureErr)
	}
	if err == nil || !strings.Contains(err.Error(), "cache stability") || !strings.Contains(err.Error(), "timestamp") {
		t.Fatalf("audit accepted a planted head timestamp: %v\n%s", err, out)
	}
}

// Boundary: the audit prints the unlayered warning for an unmarked source and does not fail on it.
func TestAuditAgentContext_Boundary_UnmarkedSourceWarns(t *testing.T) {
	dir := compiledFixture(t, fixtureAgentsMD)
	var err error
	out, captureErr := captureStdout(t, func() error {
		err = auditAgentContext(t.Context(), &config.Manifest{}, auditContextOptions(dir))
		return nil
	})
	if captureErr != nil {
		t.Fatal(captureErr)
	}
	if err != nil && strings.Contains(err.Error(), "cache stability") {
		t.Fatalf("unmarked source failed the stability gate: %v", err)
	}
	mustContain(t, out, compiler.UnlayeredWarning)
}
