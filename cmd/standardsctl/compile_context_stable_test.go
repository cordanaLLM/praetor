package main

import (
	"strings"
	"testing"
)

func TestCompileContextVerifyStable_Positive_WarnsOnUnmarkedSource(t *testing.T) {
	dir := newContextFixture(t, false)
	out, err := runCompileContextCmd(t, dir, "--verify-stable")
	if err != nil {
		t.Fatalf("verify-stable: %v\n%s", err, out)
	}
	mustContain(t, out, "no cache band markers", "<!-- praetor:head -->")
}

func TestCompileContextVerifyStable_Negative_RefusesVolatileHead(t *testing.T) {
	dir := newContextFixture(t, false)
	source := readFixtureFile(t, dir, "AGENTS.md")
	writeFixtureFile(t, dir, "AGENTS.md", "<!-- praetor:head -->\nBuilt 2026-10-07T12:30:00Z.\n\n<!-- praetor:tail -->\n"+source)
	out, err := runCompileContextCmd(t, dir, "--verify-stable")
	if err == nil || !strings.Contains(err.Error(), "volatile") {
		t.Fatalf("planted timestamp accepted: %v\n%s", err, out)
	}
}
