package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

const (
	cavemanProse = "Search for an existing implementation before adding one. Grep the repository for the " +
		"capability and extend the code that is already there. Two implementations of one behavior are a " +
		"defect: they drift, and the second one stops matching the first.\n"
	cavemanTerse = "verdict: pass. changed: internal/caveman. ran: go test ./internal/caveman/. open: none.\n"
)

// runCavemanCLI runs the command against an in-memory stdin and returns what it printed.
func runCavemanCLI(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := cavemanCommand(context.Background(), args, strings.NewReader(stdin), &out)
	return out.String(), err
}

func TestCavemanCheckPositive(t *testing.T) {
	dir := t.TempDir()
	terse := writeFixtureFile(t, dir, "terse.md", cavemanTerse)
	out, err := runCavemanCLI(t, "", "check", terse)
	if err != nil || !strings.Contains(out, ": PASS prose_words=") {
		t.Fatalf("terse file: err=%v\n%s", err, out)
	}
	if out, err = runCavemanCLI(t, cavemanTerse, "check", "-"); err != nil || !strings.HasPrefix(out, "-: PASS") {
		t.Fatalf("stdin: err=%v\n%s", err, out)
	}
	// A directory expands to the Markdown files below it, and nothing else.
	writeFixtureFile(t, dir, "nested/deeper.md", cavemanTerse)
	writeFixtureFile(t, dir, "nested/notes.txt", cavemanProse)
	if out, err = runCavemanCLI(t, "", "check", dir); err != nil || strings.Count(out, ": PASS") != 2 || strings.Contains(out, "notes.txt") {
		t.Fatalf("directory: err=%v\n%s", err, out)
	}
}

func TestCavemanCheckNegative(t *testing.T) {
	dir := t.TempDir()
	prose := writeFixtureFile(t, dir, "prose.md", cavemanProse)
	terse := writeFixtureFile(t, dir, "terse.md", cavemanTerse)
	out, err := runCavemanCLI(t, "", "check", terse, prose)
	if err == nil || !strings.Contains(err.Error(), "1 of 2 input(s) failed") {
		t.Fatalf("prose must fail the check: err=%v", err)
	}
	if !strings.Contains(out, "prose.md: FAIL") || !strings.Contains(out, "prose.md:0 C1 article-density:") {
		t.Fatalf("failure output:\n%s", out)
	}
	for name, args := range map[string][]string{
		"no subcommand":      nil,
		"unknown subcommand": {"lint", terse},
		"no inputs":          {"check"},
		"missing file":       {"check", filepath.Join(dir, "absent.md")},
		"unknown flag":       {"check", "--strict", terse},
		"unknown surface":    {"check", "--surface=slack", "--root=" + dir, terse},
	} {
		if _, err := runCavemanCLI(t, "", args...); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestCavemanCheckBoundary(t *testing.T) {
	dir := t.TempDir()
	prose := writeFixtureFile(t, dir, "prose.md", cavemanProse)
	empty := writeFixtureFile(t, dir, "empty.md", "")
	if out, err := runCavemanCLI(t, "", "check", empty); err != nil || !strings.Contains(out, "PASS prose_words=0") {
		t.Fatalf("empty file: err=%v\n%s", err, out)
	}
	// Without a manifest the mcp surface falls back to agent = internal: the lint applies.
	if _, err := runCavemanCLI(t, "", "check", "--surface=mcp", "--root="+dir, prose); err == nil {
		t.Fatal("mcp defaults to internal; prose must fail")
	}
	// A manifest that opts the surface out skips the lint and says which row decided it.
	writeFixtureFile(t, dir, ".standards.yaml", "version: 1\nregister:\n  surfaces:\n    mcp: docs\n")
	out, err := runCavemanCLI(t, "", "check", "--surface=mcp", "--root="+dir, prose)
	if err != nil || !strings.Contains(out, "skip, surfaces.mcp = docs") {
		t.Fatalf("opted-out surface: err=%v\n%s", err, out)
	}
	// Printed findings are bounded; the rest are counted.
	many := strings.Repeat("please.\n", maxPrintedFindings+5)
	if out, err = runCavemanCLI(t, many, "check", "-"); err == nil || !strings.Contains(out, "(+5 more findings)") {
		t.Fatalf("finding bound: err=%v\n%s", err, out)
	}
	if _, err = runCavemanCLI(t, strings.Repeat("a", 1<<20+1), "check", "-"); err == nil {
		t.Fatal("stdin above 1 MiB must be refused")
	}
}

func TestCavemanEstimate(t *testing.T) {
	dir := t.TempDir()
	path := writeFixtureFile(t, dir, "ten.md", "one two three four five six seven eight nine ten\n")
	out, err := runCavemanCLI(t, "", "estimate", path, "-")
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	if !strings.Contains(out, "ten.md: bytes=49 lines=1 tokens_est=13") || !strings.Contains(out, "-: bytes=0 lines=0 tokens_est=0") {
		t.Fatalf("per-input lines:\n%s", out)
	}
	if !strings.Contains(out, "total: inputs=2 bytes=49 tokens_est=13") {
		t.Fatalf("total line:\n%s", out)
	}
	if _, err := runCavemanCLI(t, "", "estimate"); err == nil {
		t.Fatal("estimate without inputs must print usage")
	}
	if _, err := runCavemanCLI(t, "", "estimate", filepath.Join(dir, "absent")); err == nil {
		t.Fatal("a missing input must fail")
	}
}
