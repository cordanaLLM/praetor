// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package managedasset

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// actionlintFixture is fixtureFamily with a workflow running one job per runner and labels
// declared to actionlint.
func actionlintFixture(runners, labels []string) Family {
	family := fixtureFamily()
	family.Workflow = "name: Fixture Gate\njobs:\n"
	for index, runner := range runners {
		family.Workflow += fmt.Sprintf("  job%d:\n    runs-on: %s\n", index, runner)
	}
	family.ActionlintLabels = labels
	return family
}

// Positive (#593): the Markdown family declares its runner, a runs-on value of its workflow, to
// actionlint and the figure engine declares none; ActionlintLabelsOf reads each label once, in
// family order, and a fixture family declaring its own runners validates, a trailing comment
// on a runs-on line aside.
func TestActionlintLabelsPositive(t *testing.T) {
	families := ForFacet(DocumentationFacet)
	if got := families[0].ActionlintLabels; !slices.Equal(got, []string{"ubuntu-26.04"}) || len(families[1].ActionlintLabels) != 0 {
		t.Fatalf("actionlint labels = %v and %v, want [ubuntu-26.04] and none", got, families[1].ActionlintLabels)
	}
	if !slices.Contains(workflowRunners(families[0].Workflow), "ubuntu-26.04") {
		t.Fatal("the Markdown workflow does not run on the label it declares")
	}
	fixture := actionlintFixture([]string{"ubuntu-26.04", "gpu-runner  # ours"}, []string{"gpu-runner", "ubuntu-26.04"})
	if err := fixture.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := ActionlintLabelsOf([]Family{families[0], fixture, families[1]}); !slices.Equal(got, []string{"ubuntu-26.04", "gpu-runner"}) {
		t.Fatalf("ActionlintLabelsOf = %v", got)
	}
}

// Negative: a label its workflow does not run on, a repeated label, a flow-sequence runner, and
// a label YAML would read as a number, a boolean, a null or anything but that plain string fail
// validation.
func TestActionlintLabelsNegative(t *testing.T) {
	cases := map[string]Family{
		"not a runner": actionlintFixture([]string{"ubuntu-26.04"}, []string{"ubuntu-24.04"}),
		"repeated":     actionlintFixture([]string{"ubuntu-26.04"}, []string{"ubuntu-26.04", "ubuntu-26.04"}),
		"flow runner":  actionlintFixture([]string{"[ubuntu-26.04]"}, []string{"ubuntu-26.04"}),
	}
	for _, label := range []string{"26.04", "true", "null", "~", "0x1f", "ubuntu 26.04", "[a]", "'a'", "a#b", "*a", ""} {
		cases["label "+label] = actionlintFixture([]string{label}, []string{label})
	}
	for name, family := range cases {
		if err := family.Validate(); err == nil || !strings.Contains(err.Error(), "actionlint label") {
			t.Errorf("%s: validation passed or failed for another reason: %v", name, err)
		}
	}
}

// Boundary: MaxActionlintLabels labels validate and one more fails, and ActionlintLabelsOf
// returns at most MaxActionlintLabels labels however many families declare, none for none.
func TestActionlintLabelsBoundary(t *testing.T) {
	runners := make([]string, 0, MaxActionlintLabels+1)
	for index := 0; index <= MaxActionlintLabels; index++ {
		runners = append(runners, fmt.Sprintf("runner-%d", index))
	}
	full := actionlintFixture(runners, runners[:MaxActionlintLabels])
	if err := full.Validate(); err != nil {
		t.Fatalf("%d labels: %v", MaxActionlintLabels, err)
	}
	over := actionlintFixture(runners, runners)
	if err := over.Validate(); err == nil {
		t.Fatalf("%d labels validated", MaxActionlintLabels+1)
	}
	other := actionlintFixture(runners, runners[1:])
	if got := ActionlintLabelsOf([]Family{full, other}); !slices.Equal(got, runners[:MaxActionlintLabels]) {
		t.Fatalf("ActionlintLabelsOf = %v, want the first %d", got, MaxActionlintLabels)
	}
	if got := ActionlintLabelsOf(nil); len(got) != 0 {
		t.Fatalf("no family declared labels %v", got)
	}
}
