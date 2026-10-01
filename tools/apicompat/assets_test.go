// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package apicompat

import (
	"bytes"
	"go/build/constraint"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
)

// Positive: the inventory is the gate program alone and reads its exact embedded bytes.
// Negative: an unknown name is refused. Boundary: the returned inventory is a private copy.
func TestLockedAssetInventory(t *testing.T) {
	names := Names()
	if !slices.Equal(names, []string{GateFile}) {
		t.Fatalf("asset names = %v, want [%s]", names, GateFile)
	}
	if _, err := Read(GateFile); err != nil {
		t.Fatal(err)
	}
	if _, err := Read("gate/other.go"); err == nil {
		t.Fatal("unknown asset accepted")
	}
	names[0] = "mutated"
	if Names()[0] != GateFile {
		t.Fatal("Names exposed the inventory for mutation")
	}
}

// Positive: the gate's build constraint is BuildTag alone, so no ./... pattern of an adopting
// module builds it, and the program is gofmt-formatted with LF endings. Negative: the
// constraint does not hold without the tag. Boundary: it holds with exactly that tag.
func TestGateSourceStaysOutOfPackagePatterns(t *testing.T) {
	source, err := Read(GateFile)
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := bytes.Cut(source, []byte("\n"))
	expr, err := constraint.Parse(string(first))
	if err != nil {
		t.Fatalf("the gate does not open with a build constraint: %v", err)
	}
	if expr.Eval(func(string) bool { return false }) {
		t.Fatalf("constraint %q holds without any tag", first)
	}
	if !expr.Eval(func(tag string) bool { return tag == BuildTag }) {
		t.Fatalf("constraint %q does not hold with %s", first, BuildTag)
	}
	formatted, err := format.Source(source)
	if err != nil || !bytes.Equal(formatted, source) || bytes.Contains(source, []byte("\r")) {
		t.Fatalf("the gate is not gofmt-formatted LF text (format error %v)", err)
	}
}

// Positive: the checker is pinned to one exact module version. Negative: no moving query
// (latest, a branch) and no unpinned install appear. Boundary: the pin is one well-formed
// module@version token.
func TestGatePinsItsChecker(t *testing.T) {
	source, err := Read(GateFile)
	if err != nil {
		t.Fatal(err)
	}
	pin := regexp.MustCompile(`(?m)^\s*checkerModule = "(github\.com/joelanford/go-apidiff@v\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?)"$`)
	if !pin.Match(source) {
		t.Fatal("checkerModule is not github.com/joelanford/go-apidiff at an exact version")
	}
	for _, moving := range []string{"@latest", "@main", "@master", "go-apidiff@v0\""} {
		if bytes.Contains(source, []byte(moving)) {
			t.Fatalf("the gate names a moving checker version %q", moving)
		}
	}
}

// Positive: this repository's own hosted gate is the locked text. Negative: the text carries
// no expression in its run line, where the shell would execute it. Boundary: the base reaches
// the gate through env, and the step names the gate program by its asset path.
func TestWorkflowRunsTheGate(t *testing.T) {
	copyBytes, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(WorkflowFile)))
	if err != nil {
		t.Fatal(err)
	}
	if string(copyBytes) != Workflow {
		t.Fatalf("%s differs from the locked workflow in %s", WorkflowFile, SourceFile)
	}
	runs, err := forge.WorkflowRuns([]byte(Workflow))
	if err != nil {
		t.Fatal(err)
	}
	want := "go run " + Directory + "/" + GateFile + " -base=\"$BASE\""
	if len(runs) != 1 || runs[0].Script != want {
		t.Fatalf("workflow runs %+v, want the one step %q", runs, want)
	}
	if !strings.Contains(Workflow, "BASE: ${{ github.event.pull_request.base.sha }}") {
		t.Fatal("the pull request base does not reach the gate through env")
	}
}

// Positive: the job reports StatusContext on every pull request, so the rendered ruleset
// requires it. Negative: a condition on the job removes it from the required set. Boundary: an
// advisory job (continue-on-error) is not required either.
func TestWorkflowIsARequiredCheck(t *testing.T) {
	contexts, err := forge.RequiredStatusContextsPlanned(t.Context(), t.TempDir(), map[string][]byte{WorkflowFile: []byte(Workflow)})
	if err != nil || !slices.Equal(contexts, []string{StatusContext}) {
		t.Fatalf("required status contexts = %v, %v; want [%s]", contexts, err, StatusContext)
	}
	job := "    name: " + StatusContext + "\n"
	for name, mutated := range map[string]string{
		"conditional": strings.Replace(Workflow, job, job+"    if: github.actor != 'bot'\n", 1),
		"advisory":    strings.Replace(Workflow, job, job+"    continue-on-error: true\n", 1),
	} {
		contexts, err := forge.RequiredStatusContextsPlanned(t.Context(), t.TempDir(), map[string][]byte{WorkflowFile: []byte(mutated)})
		if err != nil || len(contexts) != 0 {
			t.Fatalf("%s job: required status contexts = %v, %v; want none", name, contexts, err)
		}
	}
}

// PriorDigests. Positive and boundary: no Prior text exists yet. Negative: the returned map is
// a private copy, so a caller cannot add a digest the family then accepts.
func TestPriorDigests(t *testing.T) {
	if len(PriorDigests()) != 0 {
		t.Fatal("a Prior text is recorded although no earlier gate text shipped")
	}
	prior := PriorDigests()
	prior["x"] = WorkflowFile
	if len(PriorDigests()) != 0 {
		t.Fatal("PriorDigests exposed the map for mutation")
	}
}
