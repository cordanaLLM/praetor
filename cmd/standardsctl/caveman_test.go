package main

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
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
	// Without a manifest the mcp surface is internal by default: the lint applies.
	if _, err := runCavemanCLI(t, "", "check", "--surface=mcp", "--root="+dir, prose); err == nil {
		t.Fatal("mcp defaults to internal; prose must fail")
	}
	// surfaces.agent does not reach an emission surface, so agent = docs keeps the lint on.
	writeFixtureFile(t, dir, ".standards.yaml", "version: 1\nregister:\n  surfaces:\n    agent: docs\n")
	if _, err := runCavemanCLI(t, "", "check", "--surface=mcp", "--root="+dir, prose); err == nil {
		t.Fatal("agent = docs must not switch the mcp lint off; prose must fail")
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
	// The rendered register block is masked exactly as the context gate masks it.
	block := config.RegisterBlockStart + "\n" + strings.TrimSuffix(cavemanProse, "\n") + "\n" + config.RegisterBlockEnd + "\n"
	if out, err = runCavemanCLI(t, cavemanTerse+block, "check", "-"); err != nil || !strings.Contains(out, "PASS") || !strings.Contains(out, "register_block_lines=3") {
		t.Fatalf("register block: err=%v\n%s", err, out)
	}
}

// TestCavemanCheckCeilingFlags covers --max-words/--max-tokens: positive (terse text still
// passing prose rules fails once it crosses either ceiling), negative (the default, no
// flags, never fires C7/C8) and boundary (0 means no ceiling; exactly at a ceiling passes).
func TestCavemanCheckCeilingFlags(t *testing.T) {
	dir := t.TempDir()
	terse := writeFixtureFile(t, dir, "terse.md", cavemanTerse)

	out, err := runCavemanCLI(t, "", "check", "--max-words=1", terse)
	if err == nil || !strings.Contains(out, "C7 word-ceiling") {
		t.Fatalf("--max-words=1 must fail terse text on the word ceiling alone: err=%v\n%s", err, out)
	}
	out, err = runCavemanCLI(t, "", "check", "--max-tokens=1", terse)
	if err == nil || !strings.Contains(out, "C8 token-ceiling") {
		t.Fatalf("--max-tokens=1 must fail terse text on the token ceiling alone: err=%v\n%s", err, out)
	}

	if out, err = runCavemanCLI(t, "", "check", terse); err != nil || strings.Contains(out, "ceiling") {
		t.Fatalf("no flags set (the default) must never fire a ceiling rule: err=%v\n%s", err, out)
	}
	if out, err = runCavemanCLI(t, "", "check", "--max-words=0", "--max-tokens=0", terse); err != nil || strings.Contains(out, "ceiling") {
		t.Fatalf("--max-words=0 --max-tokens=0 must behave like unset: err=%v\n%s", err, out)
	}

	words := strings.Fields(cavemanTerse)
	if out, err = runCavemanCLI(t, "", "check", fmt.Sprintf("--max-words=%d", len(words)), terse); err != nil || strings.Contains(out, "ceiling") {
		t.Fatalf("exactly at the word ceiling must pass: err=%v\n%s", err, out)
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

const (
	floorBefore = "1. **Never** push to `main`; run `make verify-all` first (HISS-16).\n" +
		"You MUST read [the guide](docs/guides/text-register.md).\n"
	floorAfter = "1. **Never** push `main`. First: `make verify-all` (HISS-16).\n" +
		"MUST read [guide](docs/guides/text-register.md).\n"
)

func TestCavemanFloorPositive(t *testing.T) {
	dir := t.TempDir()
	before := writeFixtureFile(t, dir, "before.md", floorBefore)
	after := writeFixtureFile(t, dir, "after.md", floorAfter)
	out, err := runCavemanCLI(t, "", "floor", before, after)
	if err != nil || !strings.Contains(out, "after.md: PASS findings=0") {
		t.Fatalf("a rewrite that keeps every fact must pass: err=%v\n%s", err, out)
	}
	// The rewrite may arrive on standard input.
	if out, err = runCavemanCLI(t, floorAfter, "floor", before, "-"); err != nil || !strings.Contains(out, "-> -: PASS") {
		t.Fatalf("stdin rewrite: err=%v\n%s", err, out)
	}
}

func TestCavemanFloorNegative(t *testing.T) {
	dir := t.TempDir()
	before := writeFixtureFile(t, dir, "before.md", floorBefore)
	lossy := writeFixtureFile(t, dir, "lossy.md", "1. **Never** push main.\nRead the guide.\n")
	out, err := runCavemanCLI(t, "", "floor", before, lossy)
	if err == nil || !strings.Contains(err.Error(), "lossy.md lost") {
		t.Fatalf("a lossy rewrite must fail: err=%v\n%s", err, out)
	}
	for _, want := range []string{"F1 code-span-lost", "F3 id-lost: HISS-16", "F4 link-lost", "F6 must-dropped: 1 -> 0"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing finding %q in:\n%s", want, out)
		}
	}
	for name, args := range map[string][]string{
		"one input":    {"floor", before},
		"three inputs": {"floor", before, before, before},
		"both stdin":   {"floor", "-", "-"},
		"missing file": {"floor", before, filepath.Join(dir, "absent.md")},
	} {
		if _, err := runCavemanCLI(t, "", args...); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestCavemanFloorBoundary(t *testing.T) {
	dir := t.TempDir()
	empty := writeFixtureFile(t, dir, "empty.md", "")
	before := writeFixtureFile(t, dir, "before.md", floorBefore)
	// Nothing to lose: an empty original passes against anything, including empty.
	if out, err := runCavemanCLI(t, "", "floor", empty, empty); err != nil || !strings.Contains(out, "PASS findings=0") {
		t.Fatalf("empty -> empty: err=%v\n%s", err, out)
	}
	// Moving a fact is fine; the floor checks presence, not position.
	moved := writeFixtureFile(t, dir, "moved.md", "MUST read [guide](docs/guides/text-register.md).\n"+
		"1. **Never** push `main`. First: `make verify-all` (HISS-16).\n")
	if out, err := runCavemanCLI(t, "", "floor", before, moved); err != nil {
		t.Fatalf("reordered facts must pass: err=%v\n%s", err, out)
	}
	// Findings are bounded like check's; the rest are counted.
	var many strings.Builder
	for i := 0; i < maxPrintedFindings+5; i++ {
		fmt.Fprintf(&many, "see ID-%d\n", i+1)
	}
	ids := writeFixtureFile(t, dir, "ids.md", many.String())
	if out, err := runCavemanCLI(t, "", "floor", ids, empty); err == nil || !strings.Contains(out, "(+5 more findings)") {
		t.Fatalf("finding bound: err=%v\n%s", err, out)
	}
}
