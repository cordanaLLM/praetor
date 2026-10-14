// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package readmegovernance

import (
	"strings"
	"testing"
)

var placementState = State{DocumentationEnabled: true, RepositoryOwner: "acme", RepositoryName: "widgets"}

// placed reconciles input, checks that the result verifies and that a second run changes nothing,
// and returns the text before the block's start marker and after its end marker.
func placed(t *testing.T, input string) (before, after string) {
	t.Helper()
	out, _, err := Reconcile(input, placementState)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(out, placementState); err != nil {
		t.Fatalf("verify %q: %v", out, err)
	}
	if again, changed, err := Reconcile(out, placementState); err != nil || changed || again != out {
		t.Fatalf("second reconcile changed the README: changed=%v err=%v", changed, err)
	}
	start, end := strings.Index(out, Start), strings.Index(out, End)
	if start < 0 || end < start {
		t.Fatalf("no block in %q", out)
	}
	return out[:start], out[end+len(End):]
}

// Positive (#1262): the block goes after the description, the first paragraph below the title,
// also when badges, a reference-style badge or an HTML logo sit between them.
func TestInsertedBlockFollowsTheDescription(t *testing.T) {
	description := "Widgets measures the widgets of a factory floor.\nIt runs on the line controller.\n"
	for name, input := range map[string]string{
		"plain":     "# Widgets\n\n" + description + "\n## Install\n\nRun make.\n",
		"badges":    "# Widgets\n\n[![CI](https://ci.example/badge.svg)](https://ci.example) ![size](s.svg)\n\n" + description + "\n## Install\n",
		"reference": "# Widgets\n\n[![CI][ci-badge]][ci]\n\n" + description + "\n## Install\n\n[ci-badge]: b.svg\n[ci]: https://ci.example\n",
		"html logo": "# Widgets\n\n<p align=\"center\">\n  <img src=\"logo.svg\" alt=\"\">\n</p>\n\n" + description + "\n## Install\n",
		"last":      "# Widgets\n\n" + description,
	} {
		before, after := placed(t, input)
		if !strings.HasSuffix(before, description+"\n") {
			t.Errorf("%s: block does not follow the description:\n%s", name, before)
		}
		if strings.Contains(after, "Widgets measures") {
			t.Errorf("%s: description below the block", name)
		}
	}
}

// Negative: with no description yet (a heading, a code fence or only badges below the title, or
// the title alone) the block stays right under the title, and without a title at the end.
func TestInsertedBlockWithoutADescriptionStaysUnderTheTitle(t *testing.T) {
	for name, input := range map[string]string{
		"heading":     "# Widgets\n\n## Install\n\nRun make.\n",
		"fence":       "# Widgets\n\n```sh\nmake\n```\n\nText after.\n",
		"badges only": "# Widgets\n\n[![CI](https://ci.example/badge.svg)](https://ci.example)\n\n## Install\n",
		"title only":  "# Widgets\n",
	} {
		before, _ := placed(t, input)
		if before != "# Widgets\n\n" {
			t.Errorf("%s: text before the block %q", name, before)
		}
	}
	untitled := "Widgets without a title.\n\n## Install\n\nRun make.\n"
	before, after := placed(t, untitled)
	if before != untitled+"\n" || after != "\n" {
		t.Errorf("untitled README: before %q after %q", before, after)
	}
}

// Boundary: a block already in the README stays where it sits, also above the description, and
// a marker pair the adopter placed is filled in place; the content after a block keeps one blank
// line before it.
func TestExistingBlockAndPlacedMarkersStayPut(t *testing.T) {
	top, _, err := Reconcile("# Widgets\n\n## Install\n", placementState)
	if err != nil {
		t.Fatal(err)
	}
	moved := strings.Replace(top, "## Install\n", "Widgets measures the floor.\n\n## Install\n", 1)
	before, _ := placed(t, moved)
	if before != "# Widgets\n\n" {
		t.Errorf("an existing block moved: before %q", before)
	}
	anchored := "# Widgets\n\nWidgets measures the floor.\n\n## Governance\n\n" + Start + "\n" + End + "\n\n## More\n"
	before, after := placed(t, anchored)
	if !strings.HasSuffix(before, "## Governance\n\n") || after != "\n\n## More\n" {
		t.Errorf("placed markers not filled in place: before %q after %q", before, after)
	}
	_, after = placed(t, "# Widgets\n\nWidgets measures the floor.\n\n\n\n## Install\n")
	if after != "\n\n## Install\n" {
		t.Errorf("content after the block: %q", after)
	}
}
