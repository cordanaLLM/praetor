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
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Positive: the inventory is the gate program alone and reads its exact embedded bytes.
// Negative: an unknown name is refused. Boundary: the returned inventory is a private copy.
func TestLockedAssetInventory(t *testing.T) {
	names := Names()
	if !slices.Equal(names, []string{GateFile, PlaceholderFile}) {
		t.Fatalf("asset names = %v, want [%s %s]", names, GateFile, PlaceholderFile)
	}
	for _, name := range []string{GateFile, PlaceholderFile} {
		if _, err := Read(name); err != nil {
			t.Fatal(err)
		}
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

// Positive: this repository's own hosted gate is the locked text, in the hosted gate shape
// (ghworkflow.HostedGateFault), and its last step runs the gate. Negative: the text carries no
// expression in its run line, where the shell would execute it. Boundary: the base reaches the
// gate through env, the step names the gate program by its asset path, and the only other
// command is the draft step's.
func TestWorkflowRunsTheGate(t *testing.T) {
	copyBytes, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(WorkflowFile)))
	if err != nil {
		t.Fatal(err)
	}
	if string(copyBytes) != Workflow {
		t.Fatalf("%s differs from the locked workflow in %s", WorkflowFile, SourceFile)
	}
	spec, err := ghworkflow.Parse([]byte(Workflow))
	if err != nil {
		t.Fatal(err)
	}
	if err := ghworkflow.HostedGateFault(&spec, "api-compatibility", ghworkflow.HostedGateDefaultBranch); err != nil {
		t.Fatalf("the workflow departs from the hosted gate shape: %v", err)
	}
	runs, err := forge.WorkflowRuns([]byte(Workflow))
	if err != nil {
		t.Fatal(err)
	}
	want := "go run " + Directory + "/" + GateFile + " -base=\"$BASE\""
	if len(runs) != 2 || runs[0].Name != ghworkflow.HostedGateDraftStepName || runs[1].Script != want {
		t.Fatalf("workflow runs %+v, want the draft step and the one gate step %q", runs, want)
	}
	if !strings.Contains(Workflow, "BASE: ${{ github.event.pull_request.base.sha }}") {
		t.Fatal("the pull request base does not reach the gate through env")
	}
}

// Positive: the job has no condition and reports StatusContext on every pull request, so the
// rendered ruleset requires it; on a draft it reports a failure by design. Negative: a job
// condition, the draft skip included, removes it from the required set, since GitHub reports the
// skipped job as successful. Boundary: an advisory job (continue-on-error) is not required
// either, and dropping ready_for_review keeps the job required while the shape check refuses it.
func TestWorkflowIsARequiredCheck(t *testing.T) {
	contexts, err := forge.RequiredStatusContextsPlanned(t.Context(), t.TempDir(), map[string][]byte{WorkflowFile: []byte(Workflow)})
	if err != nil || !slices.Equal(contexts, []string{StatusContext}) {
		t.Fatalf("required status contexts = %v, %v; want [%s]", contexts, err, StatusContext)
	}
	job := "    name: " + StatusContext + "\n"
	for name, mutated := range map[string]string{
		"conditional": strings.Replace(Workflow, job, job+"    if: github.actor != 'bot'\n", 1),
		"draft skip":  strings.Replace(Workflow, job, job+"    if: "+ghworkflow.HostedGateNotDraft+"\n", 1),
		"advisory":    strings.Replace(Workflow, job, job+"    continue-on-error: true\n", 1),
	} {
		if mutated == Workflow {
			t.Fatalf("%s: the mutation did not change the workflow", name)
		}
		contexts, err := forge.RequiredStatusContextsPlanned(t.Context(), t.TempDir(), map[string][]byte{WorkflowFile: []byte(mutated)})
		if err != nil || len(contexts) != 0 {
			t.Fatalf("%s job: required status contexts = %v, %v; want none", name, contexts, err)
		}
	}
	noReady := strings.Replace(Workflow, ", ready_for_review]", "]", 1)
	spec, err := ghworkflow.Parse([]byte(noReady))
	if err != nil || ghworkflow.HostedGateFault(&spec, "api-compatibility", ghworkflow.HostedGateDefaultBranch) == nil {
		t.Fatalf("a gate that never reruns on ready_for_review holds the hosted gate shape (%v)", err)
	}
}

// PriorDigests. Positive: every file under testdata/prior reproduces one digest, each digest is
// reproduced by one file, and the file maps to the path it was shipped at: the workflow, or the
// gate program (api-gate-main*.go.txt). Negative: the current texts are no Prior text, and the
// returned map is a private copy, so a caller cannot add a digest the family then accepts.
// Boundary: a CRLF checkout of a prior text reproduces the same digest.
func TestPriorDigests(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("testdata", "prior"))
	if err != nil {
		t.Fatal(err)
	}
	digests := PriorDigests()
	if len(entries) != len(digests) {
		t.Fatalf("testdata/prior holds %d texts for %d digests", len(entries), len(digests))
	}
	total := len(digests)
	reproduced := map[string]bool{}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join("testdata", "prior", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		want := WorkflowFile
		if strings.HasPrefix(entry.Name(), "api-gate-main") {
			want = Directory + "/" + GateFile
		}
		for _, text := range [][]byte{data, bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n"))} {
			digest, _, err := util.CanonicalTextDigest(text)
			if err != nil || digests[digest] != want {
				t.Fatalf("%s: digest %s maps to %q (%v), want %s", entry.Name(), digest, digests[digest], err, want)
			}
			reproduced[digest] = true
		}
	}
	if len(reproduced) != total {
		t.Fatalf("testdata/prior reproduces %d of %d digests", len(reproduced), total)
	}
	current, _, err := util.CanonicalTextDigest([]byte(Workflow))
	if err != nil || digests[current] != "" {
		t.Fatalf("the current workflow is listed as a Prior text (%v)", err)
	}
	digests["x"] = WorkflowFile
	if len(PriorDigests()) != total {
		t.Fatal("PriorDigests exposed the map for mutation")
	}
}

// Positive: the placeholder holds exactly the negation of the gate's constraint, so with the
// tag off the gate directory has one buildable file, which a directory-level vet or lint finds,
// and with it on only the gate is built. Negative: it does not hold with the tag, and it is not
// the gate. Boundary: it holds without any tag, and it exits nonzero, so a run of the directory
// is never a silent success.
func TestPlaceholderStandsInOnlyWithoutTheGateTag(t *testing.T) {
	source, err := Read(PlaceholderFile)
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := bytes.Cut(source, []byte("\n"))
	expr, err := constraint.Parse(string(first))
	if err != nil {
		t.Fatalf("the placeholder does not open with a build constraint: %v", err)
	}
	if !expr.Eval(func(string) bool { return false }) {
		t.Fatalf("constraint %q does not hold without a tag", first)
	}
	if expr.Eval(func(tag string) bool { return tag == BuildTag }) {
		t.Fatalf("constraint %q holds with %s, so the directory would hold two main functions", first, BuildTag)
	}
	if !bytes.Contains(source, []byte("os.Exit(1)")) || !bytes.Contains(source, []byte(Directory+"/"+GateFile)) {
		t.Fatal("the placeholder neither fails nor names the file that runs the gate")
	}
	gate, err := Read(GateFile)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(gate, source) {
		t.Fatal("the placeholder is the gate")
	}
}
