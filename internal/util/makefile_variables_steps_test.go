package util

import (
	"fmt"
	"strings"
	"testing"
)

// Boundary: a Makefile near the line bound, 3900 variables bound once plus 120 computed binding
// names of about 8 KB, each holding two literal runs, splits every bound name once and compares
// each variable it may fix with the 240 runs once. Splitting each computed name again for every
// variable compared with it made the work variables times bytes: 4.73 s for MakefileHasTarget on
// such a file, against 20.7 ms before the resolver. The test counts the work
// (makefileVariables.steps) instead of timing it, so the bound is the same on every machine: the
// bytes of every bound name, at most the file's length, plus one step per run per variable.
func TestMakefileReadVariablesSplitsComputedNamesOnce(t *testing.T) {
	const plain, computed = 3900, 120
	var makefile strings.Builder
	for i := range plain {
		fmt.Fprintf(&makefile, "V%d := a\n", i)
	}
	long := strings.Repeat("x", 8000)
	for i := range computed {
		fmt.Fprintf(&makefile, "$(P)%s%d := 1\n", long, i)
	}
	makefile.WriteString("GATE := verify-all\n$(GATE): dep\n\t@echo custom\n")
	data := makefile.String()
	lines, whole := makefileLogicalLines(data)
	if !whole {
		t.Fatalf("the reader stopped before the end of the %d-line file", len(lines))
	}
	variables := makefileReadVariables(lines)
	if value, ok := variables.values["GATE"]; !ok || value.text != "verify-all" {
		t.Fatalf("GATE = %q, %v; the file fixes it to verify-all", value.text, ok)
	}
	bound := len(data) + (plain+1)*computed*2
	if variables.steps == 0 || variables.steps > bound {
		t.Fatalf("fixing the variables took %d steps, bound %d: each name is split once and each variable compared with each run once",
			variables.steps, bound)
	}
	if !MakefileHasTarget(data, "verify-all") || MakefileMayDefineTarget(data, "docs-lint") {
		t.Fatal("the large file's computed verify-all rule was not resolved")
	}
}

// Negative: past MaxMakefileComputedRuns the reader fixes no variable and so compares none, which
// keeps the work of a file with many computed binding names within the bound.
func TestMakefileReadVariablesStopsPastComputedRunBound(t *testing.T) {
	var makefile strings.Builder
	for i := range MaxMakefileComputedRuns/2 + 1 {
		fmt.Fprintf(&makefile, "$(P)q%d := 1\n", i)
	}
	makefile.WriteString("GATE := verify-all\n$(GATE): dep\n")
	data := makefile.String()
	lines, _ := makefileLogicalLines(data)
	variables := makefileReadVariables(lines)
	if len(variables.values) != 0 || variables.steps > len(data) {
		t.Fatalf("past the run bound the reader fixed %d values in %d steps, file %d bytes",
			len(variables.values), variables.steps, len(data))
	}
	if MakefileHasTarget(data, "verify-all") || !MakefileMayDefineTarget(data, "verify-all") {
		t.Fatal("past the run bound $(GATE) was resolved")
	}
}
