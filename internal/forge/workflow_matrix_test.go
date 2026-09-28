package forge

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// matrixOSLegs returns the os value of every leg of job's matrix in leg order, or nil when the
// matrix does not expand; the guard tests identify a leg by its runner image.
func matrixOSLegs(job workflowJob) []string {
	legs, err := expandMatrix(&job.Strategy.Matrix)
	if err != nil {
		return nil
	}
	var images []string
	for i := 0; i < len(legs) && i < maxMatrixLegs; i++ {
		if value, ok := legs[i].lookup("os"); ok {
			images = append(images, value.text)
		}
	}
	return images
}

// includeOSMatrix is a strategy.matrix holding one include entry per image.
func includeOSMatrix(images ...string) yaml.Node {
	scalar := func(value string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value} }
	entries := &yaml.Node{Kind: yaml.SequenceNode}
	for _, image := range images {
		entries.Content = append(entries.Content, &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{scalar("os"), scalar(image)}})
	}
	return yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{scalar(matrixIncludeKey), entries}}
}

// matrixJob returns the contexts of a pull_request workflow whose one job, test, has body: the
// job's lines indented under it.
func matrixJob(body string) ([]string, error) {
	return workflowPullRequestContexts([]byte("on: pull_request\njobs:\n  test:\n" + body))
}

// Positive: every literal matrix shape reports the contexts GitHub reports. The constant-name
// shape is #324's reproduction; the literal-axis shapes are its follow-up comments; the other
// names follow the jobs GitHub lists for matrices of these shapes: axis values appended in
// declaration order, values an include entry merges into a combination not appended, an include
// leg's own values appended with empty ones left out.
func TestMatrixContexts_Positive_NamesEveryLegAsGitHubReportsIt(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"constant name gets its axis values",
			"    name: Test (race + coverage)\n    strategy:\n      matrix:\n        os: [self-hosted-linux]\n        toolchain: [module]\n        include:\n          - os: self-hosted-linux\n            toolchain: module\n",
			"Test (race + coverage) (self-hosted-linux, module)"},
		{"literal axis evaluates an expression name",
			"    name: Build and smoke (${{ matrix.variant }})\n    strategy:\n      matrix:\n        variant: [alpha, beta]\n",
			"Build and smoke (alpha)|Build and smoke (beta)"},
		{"unnamed job appends in declaration order less excluded legs",
			"    strategy:\n      matrix:\n        python-version: [\"3.10\", \"3.11\"]\n        os: [ubuntu-latest, windows-latest]\n        exclude:\n          - os: windows-latest\n            python-version: \"3.10\"\n",
			"test (3.10, ubuntu-latest)|test (3.11, ubuntu-latest)|test (3.11, windows-latest)"},
		{"include merged into a combination is not appended",
			"    strategy:\n      matrix:\n        suite: [windows-unit, ubuntu-unit]\n        include:\n          - suite: windows-unit\n            os: windows-latest\n            coverage: true\n",
			"test (windows-unit)|test (ubuntu-unit)"},
		{"include legs append their values without empty ones",
			"    name: Test full\n    strategy:\n      matrix:\n        include:\n          - { os: windows-latest, features: \"\" }\n          - { os: ubuntu-latest, features: io-uring }\n",
			"Test full (windows-latest)|Test full (ubuntu-latest, io-uring)"},
		{"include adding a combination appends its own values",
			"    name: Node\n    strategy:\n      matrix:\n        os: [macos-latest, windows-latest]\n        version: [12, 14]\n        include:\n          - os: windows-latest\n            version: 17\n",
			"Node (macos-latest, 12)|Node (macos-latest, 14)|Node (windows-latest, 12)|Node (windows-latest, 14)|Node (windows-latest, 17)"},
		{"expression reads included and case-folded variables",
			"    name: ${{ matrix.OS }} npm ${{matrix.npm}}\n    strategy:\n      matrix:\n        os: [windows-latest]\n        node: [16]\n        include:\n          - os: windows-latest\n            node: 16\n            npm: 6\n",
			"windows-latest npm 6"},
		{"a mapping value no name reads",
			"    name: Deploy (${{ matrix.site }})\n    strategy:\n      matrix:\n        include:\n          - site: production\n            env: {REGION: eu}\n",
			"Deploy (production)"},
		{"canonical numbers and booleans",
			"    strategy:\n      matrix:\n        node: [20, -1]\n        experimental: [false]\n        ratio: [1.5]\n",
			"test (20, false, 1.5)|test (-1, false, 1.5)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := matrixJob(tc.body)
			if err != nil {
				t.Fatalf("expand: %v", err)
			}
			if want := strings.Split(tc.want, "|"); !slices.Equal(got, want) {
				t.Fatalf("contexts = %q, want %q", got, want)
			}
		})
	}
}

// Positive: the include example of the workflow syntax guide (run-job-variations, "Expanding or
// adding matrix configurations") yields its six combinations with exactly the documented
// variables: added values overwritten by a later entry, an entry that fits no combination added
// as its own, and an entry never merged into a combination an earlier entry added.
func TestExpandMatrix_Positive_ReplaysTheReferenceIncludeExample(t *testing.T) {
	var matrix yaml.Node
	document := "fruit: [apple, pear]\nanimal: [cat, dog]\ninclude:\n  - color: green\n  - color: pink\n    animal: cat\n" +
		"  - fruit: apple\n    shape: circle\n  - fruit: banana\n  - fruit: banana\n    animal: cat\n"
	if err := yaml.Unmarshal([]byte(document), &matrix); err != nil {
		t.Fatal(err)
	}
	legs, err := expandMatrix(matrix.Content[0])
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	want := []string{
		"fruit=apple animal=cat color=pink shape=circle",
		"fruit=apple animal=dog color=green shape=circle",
		"fruit=pear animal=cat color=pink",
		"fruit=pear animal=dog color=green",
		"fruit=banana",
		"fruit=banana animal=cat",
	}
	got := make([]string, 0, len(legs))
	for _, leg := range legs {
		var cells []string
		for _, cell := range append(slices.Clone(leg.base), leg.added...) {
			cells = append(cells, cell.key+"="+cell.value.text)
		}
		got = append(got, strings.Join(cells, " "))
	}
	if !slices.Equal(got, want) {
		t.Fatalf("legs = %q, want %q", got, want)
	}
}

// Negative: a shape whose legs or names only a workflow run knows is refused with that shape
// named, never emitted as a context no run reports.
func TestMatrixContexts_Negative_RefusesWhatTheFileCannotShow(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"dynamic matrix", "    strategy:\n      matrix: ${{ fromJSON(needs.plan.outputs.matrix) }}\n", "strategy.matrix is the expression"},
		{"dynamic axis", "    strategy:\n      matrix:\n        os: ${{ fromJSON(vars.IMAGES) }}\n", `matrix axis "os" is the expression`},
		{"dynamic include", "    strategy:\n      matrix:\n        include: ${{ fromJSON(vars.LEGS) }}\n", "strategy.matrix.include is the expression"},
		{"expression include entry", "    strategy:\n      matrix:\n        include:\n          - ${{ vars.LEG }}\n", "entry 0 is the expression"},
		{"expression axis value appended", "    name: Gate\n    strategy:\n      matrix:\n        os: [ubuntu-latest, \"${{ vars.EXTRA }}\"]\n", "is the expression"},
		{"number spelled otherwise", "    strategy:\n      matrix:\n        python: [3.10]\n", "quote it"},
		{"mapping axis value appended", "    strategy:\n      matrix:\n        target: [{name: x86_64}]\n", "is a mapping"},
		{"null axis value appended", "    strategy:\n      matrix:\n        os: [~]\n", "is null"},
		{"alias axis", "    env:\n      IMAGES: &images [a]\n    strategy:\n      matrix:\n        os: *images\n", "YAML alias"},
		{"exclude naming no axis", "    strategy:\n      matrix:\n        os: [a]\n        exclude:\n          - build: false\n", "which no axis declares"},
		{"values differing only in case", "    strategy:\n      matrix:\n        os: [Ubuntu]\n        exclude:\n          - os: ubuntu\n", "differ only in case"},
		{"empty axis", "    strategy:\n      matrix:\n        os: []\n", "has no values"},
		{"expression without a matrix", "    name: Build ${{ github.ref }}\n", "declares no strategy.matrix"},
		{"operator in a matrix name", "    name: ${{ matrix.os || 'none' }}\n    strategy:\n      matrix:\n        os: [a]\n", "not a bare matrix variable"},
		{"other context in a matrix name", "    name: ${{ github.ref }}\n    strategy:\n      matrix:\n        os: [a]\n", "is not a matrix variable"},
		{"unclosed expression", "    name: \"Gate (${{ matrix.os )\"\n    strategy:\n      matrix:\n        os: [a]\n", "not closed"},
		{"scalar matrix", "    strategy:\n      matrix: linux\n", `the scalar "linux"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := matrixJob(tc.body)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("contexts %q, error %v; want an error containing %q", got, err, tc.want)
			}
		})
	}
}

// Boundary: the leg, product and expression bounds hold at their limit and refuse one past it;
// a leg of empty values has no name the file shows; one leg still carries its suffix; and a
// matrix whose legs no file shows does not fail a workflow whose job is no required check.
func TestMatrixContexts_Boundary_Limits(t *testing.T) {
	values := func(n int) string {
		items := make([]string, n)
		for i := range items {
			items[i] = fmt.Sprintf("v%d", i)
		}
		return "[" + strings.Join(items, ", ") + "]"
	}
	full := "    strategy:\n      matrix:\n        a: " + values(16) + "\n        b: " + values(16) + "\n"
	if got, err := matrixJob(full); err != nil || len(got) != maxMatrixLegs {
		t.Fatalf("a matrix of exactly %d legs: %d contexts, %v", maxMatrixLegs, len(got), err)
	}
	for name, tc := range map[string]struct{ body, want string }{
		"one leg past the limit": {full + "        include:\n          - a: extra\n", "exceeds 256 legs"},
		"product past its bound": {"    strategy:\n      matrix:\n        a: " + values(65) + "\n        b: " + values(64) + "\n", "more than 4096"},
		"every value empty":      {"    strategy:\n      matrix:\n        include:\n          - features: \"\"\n", "every matrix value"},
		"one expression too many": {"    name: " + strings.Repeat("${{ matrix.a }}", maxNameExpressions+1) + "\n    strategy:\n      matrix:\n        a: [x]\n",
			"more than 16 expressions"},
	} {
		if got, err := matrixJob(tc.body); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: contexts %d, error %v; want %q", name, len(got), err, tc.want)
		}
	}
	atLimit := "    name: " + strings.Repeat("${{ matrix.a }}", maxNameExpressions) + "\n    strategy:\n      matrix:\n        a: [x]\n"
	if got, err := matrixJob(atLimit); err != nil || len(got) != 1 || got[0] != strings.Repeat("x", maxNameExpressions) {
		t.Errorf("a name of exactly %d expressions: %q, %v", maxNameExpressions, got, err)
	}
	conditional := "    if: needs.plan.outputs.run == 'true'\n    strategy:\n      matrix: ${{ fromJSON(needs.plan.outputs.matrix) }}\n  gate:\n    name: CI success\n"
	if got, err := matrixJob(conditional); err != nil || !slices.Equal(got, []string{"CI success"}) {
		t.Errorf("a dynamic matrix on a conditional job: %q, %v; want only the gate", got, err)
	}
}

// Positive: a selection reads only the named workflows, so a workflow elsewhere whose contexts no
// file shows fails the full inventory but not the selection.
func TestRequiredStatusContextsOf_Positive_ReadsOnlyTheNamedWorkflows(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFixture(t, root, "docs.yml", "on: pull_request\njobs:\n  docs:\n    name: Documentation Governance\n")
	writeWorkflowFixture(t, root, "ci.yml", "on: pull_request\njobs:\n  test:\n    strategy:\n      matrix: ${{ fromJSON(vars.LEGS) }}\n")
	if _, err := RequiredStatusContexts(t.Context(), root); err == nil {
		t.Fatal("the full inventory accepted a matrix no file shows")
	}
	got, err := RequiredStatusContextsOf(t.Context(), root, []string{".github/workflows/docs.yml"})
	if err != nil || !slices.Equal(got, []string{"Documentation Governance"}) {
		t.Fatalf("selection = %q, %v; want the documentation context alone", got, err)
	}
}

// Negative: a selection names workflow documents directly under .github/workflows, within the
// inventory bound, and a nil context still fails.
func TestRequiredStatusContextsOf_Negative_RefusesPathsOutsideTheWorkflowDirectory(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"docs/ci.yml", ".github/workflows/nested/ci.yml", ".github/workflows/ci.txt", ".github/workflows/"} {
		if _, err := RequiredStatusContextsOf(t.Context(), root, []string{path}); err == nil {
			t.Errorf("selection %q accepted", path)
		}
	}
	tooMany := make([]string, maxWorkflowFiles+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf(".github/workflows/%02d.yml", i)
	}
	if _, err := RequiredStatusContextsOf(t.Context(), root, tooMany); err == nil {
		t.Error("a selection past the inventory bound was accepted")
	}
	//nolint:staticcheck // SA1012: the nil context is the refusal under test.
	if _, err := RequiredStatusContextsOf(nil, root, []string{".github/workflows/ci.yml"}); err == nil {
		t.Error("a nil context was accepted")
	}
}

// Boundary: a named workflow the repository lacks and an empty selection contribute nothing, and
// the whole inventory stays bounded whatever the selection names.
func TestRequiredStatusContextsOf_Boundary_AbsentAndBoundedInventory(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFixture(t, root, "ci.yml", "on: pull_request\njobs:\n  verify: {}\n")
	for _, selection := range [][]string{{".github/workflows/absent.yml"}, {}} {
		if got, err := RequiredStatusContextsOf(t.Context(), root, selection); err != nil || len(got) != 0 {
			t.Errorf("selection %q = %q, %v; want no contexts", selection, got, err)
		}
	}
	for i := 0; i < maxWorkflowFiles; i++ {
		writeWorkflowFixture(t, root, fmt.Sprintf("%02d.yml", i), "on: push\njobs: {}\n")
	}
	if _, err := RequiredStatusContextsOf(t.Context(), root, []string{".github/workflows/ci.yml"}); err == nil {
		t.Error("an inventory past its bound was read for a selection")
	}
}
