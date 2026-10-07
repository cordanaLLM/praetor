// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/managedasset"
)

// hostedGatePriors maps each hosted gate adoption writes to the committed text the earlier
// Praetor shipped there, the one that ran on every branch, tag and draft (#815).
var hostedGatePriors = map[string]string{
	APICompatibilityWorkflowFile: "../../tools/apicompat/testdata/prior/praetor-api.every-branch-and-draft.yml",
	DocumentationWorkflowFile:    "../../tools/markdownlint/testdata/prior/praetor-docs.every-branch-and-draft.yml",
}

// adoptHostedGates adopts the framework profile, which enables both hosted gates, into a fresh
// repository whose git tracks a go.mod; head, when not empty, is the origin HEAD the checkout
// records.
func adoptHostedGates(t *testing.T, name, head string) (string, AdoptOptions) {
	t.Helper()
	root := newTestRepo(t, name)
	if head != "" {
		recordOriginHead(t, root, head)
	}
	trackGoModule(t, root, "go.mod")
	opts := AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	return root, opts
}

// hostedGateRenderings returns each hosted gate's workflow rendered for branch.
func hostedGateRenderings(t *testing.T, branch string) map[string]string {
	t.Helper()
	renderings := map[string]string{}
	for _, family := range managedasset.Families() {
		if family.WorkflowFile == "" {
			continue
		}
		rendered, err := family.ForBranch(branch)
		if err != nil {
			t.Fatal(err)
		}
		renderings[family.WorkflowFile] = rendered.Workflow
	}
	if len(renderings) != len(hostedGatePriors) {
		t.Fatalf("hosted gates = %d, want %d", len(renderings), len(hostedGatePriors))
	}
	return renderings
}

// assertHostedGates fails unless every hosted gate in root holds want's text for its path.
func assertHostedGates(t *testing.T, root string, want map[string]string) {
	t.Helper()
	for rel, text := range want {
		if got := mustRead(t, filepath.Join(root, filepath.FromSlash(rel))); got != text {
			t.Fatalf("%s is not the expected rendering:\n%s", rel, got)
		}
	}
}

// Positive: a repository whose default branch is master gets both hosted gates rendered for
// master, and a rerun changes nothing. Boundary: without the origin HEAD, as CI checks out, the
// manifest adoption created still renders master, and the main rendering refreshes to it.
func TestAdoptRendersHostedGatesForTheDefaultBranch(t *testing.T) {
	root, opts := adoptHostedGates(t, "gates-master", "master")
	master := hostedGateRenderings(t, "master")
	assertHostedGates(t, root, master)
	for rel, text := range master {
		if !strings.Contains(text, "branches: ['master']") {
			t.Fatalf("%s rendering names no master push branch", rel)
		}
	}
	before := snapshotTree(t, root)
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	assertTreeUnchanged(t, before, snapshotTree(t, root))
	removeOriginHead(t, root)
	for rel, text := range hostedGateRenderings(t, managedasset.WorkflowBranch) {
		mustWrite(t, filepath.Join(root, filepath.FromSlash(rel)), text)
	}
	report, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	assertHostedGates(t, root, master)
	assertRefreshed(t, report, master)
}

// Positive: the earlier gates that ran on every branch, tag and draft, LF or CRLF, refresh to
// the current rendering without --force. Negative: a hand-edited gate is kept without --force
// and replaced with it.
func TestAdoptRefreshesPriorHostedGatesAndKeepsEditedOnes(t *testing.T) {
	root, opts := adoptHostedGates(t, "gates-prior", "")
	current := hostedGateRenderings(t, managedasset.WorkflowBranch)
	for name, convert := range map[string]func(string) string{
		"LF":   func(text string) string { return text },
		"CRLF": func(text string) string { return strings.ReplaceAll(text, "\n", "\r\n") },
	} {
		for rel, prior := range hostedGatePriors {
			data, err := os.ReadFile(prior)
			if err != nil {
				t.Fatal(err)
			}
			mustWrite(t, filepath.Join(root, filepath.FromSlash(rel)), convert(string(data)))
		}
		report, err := Adopt(t.Context(), opts)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		assertRefreshed(t, report, current)
		for rel, text := range current {
			if got := mustRead(t, filepath.Join(root, filepath.FromSlash(rel))); got != convert(text) {
				t.Fatalf("%s: %s did not refresh in its line-ending style:\n%s", name, rel, got)
			}
		}
	}
	edited := map[string]string{}
	for rel, text := range current {
		edited[rel] = strings.Replace(text, "branches: ['main']", "branches: ['main', 'release/**']", 1)
		mustWrite(t, filepath.Join(root, filepath.FromSlash(rel)), edited[rel])
	}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	assertHostedGates(t, root, edited)
	opts.Force = true
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	assertHostedGates(t, root, current)
}

// assertRefreshed fails unless report refreshed every path of want as an earlier Praetor text.
func assertRefreshed(t *testing.T, report *AdoptReport, want map[string]string) {
	t.Helper()
	for rel := range want {
		if !slices.ContainsFunc(report.ActionDetails, func(action ActionDetail) bool {
			return action.Path == rel && strings.Contains(action.Details, refreshedFamilyDetail)
		}) {
			t.Fatalf("%s was not refreshed as an earlier Praetor text: %+v", rel, report.ActionDetails)
		}
	}
}
