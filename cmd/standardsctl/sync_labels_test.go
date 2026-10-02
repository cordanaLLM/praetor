package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
)

// staleLabelDescription is one managed label as an adopter's labels.yaml may still carry it:
// earlier wording sync has since revised.
type staleLabelDescription struct {
	issue, name, color, stale, canonical string
}

// staleLabelDescriptions holds every revision of a managed description sync migrates.
var staleLabelDescriptions = []staleLabelDescription{
	{"#266", "hiss-violation", "d73a4a", "Code introduces a regression against HISS-16 invariants", "Code introduces a regression against HISS invariants"},
	{"#393", "hiss-waiver", "fbca04", "Requires cryptographically signed waiver approval", "Architectural exception to HISS (waivers are not signed)"},
}

// customLabelTaxonomy is a labels.yaml holding label first and then a label of the adopter's
// own, under a comment of the file's own.
func customLabelTaxonomy(name, color, description string) string {
	return fmt.Sprintf(`# Canonical Repository Label Taxonomy
version: 1
labels:
  - name: %q
    color: %q
    description: %q

  - name: "team:widgets"
    color: "00ff00"
    description: "Owned by the widgets team"
`, name, color, description)
}

// TestSync_Positive_LabelDescriptionReconciledInPlace covers every managed description
// revision: a labels.yaml an adopter already has, carrying the earlier wording (the
// pre-rename "HISS-16 invariants", #266; the signed-waiver claim, #393), is rewritten to the
// canonical text in place. Every other byte -- an adopter's own label, its comment, its
// color -- survives untouched, and a second run writes nothing.
func TestSync_Positive_LabelDescriptionReconciledInPlace(t *testing.T) {
	for _, tc := range staleLabelDescriptions {
		t.Run(tc.issue+" "+tc.name, func(t *testing.T) {
			f := newSyncValidationFixture(t)
			writeFixtureFile(t, f.dir, ".config/labels.yaml", customLabelTaxonomy(tc.name, tc.color, tc.stale))
			out, err := runSyncCmd(t, "--config="+f.manifestPath)
			if err != nil {
				t.Fatalf("sync: %v\n%s", err, out)
			}
			mustContain(t, out, "[FIX] Updated managed label description(s) in .config/labels.yaml", "[OK] Labels verified")
			want := customLabelTaxonomy(tc.name, tc.color, tc.canonical)
			if got := readFixtureFile(t, f.dir, ".config/labels.yaml"); got != want {
				t.Fatalf("only the managed description may change:\ngot:\n%s\nwant:\n%s", got, want)
			}
			out, err = runSyncCmd(t, "--config="+f.manifestPath)
			if err != nil {
				t.Fatalf("second sync: %v\n%s", err, out)
			}
			if strings.Contains(out, "[FIX] Updated managed label description") {
				t.Errorf("reconciliation is not idempotent:\n%s", out)
			}
			if got := readFixtureFile(t, f.dir, ".config/labels.yaml"); got != want {
				t.Errorf("the second run changed the file:\n%s", got)
			}
		})
	}
}

// TestSync_Negative_UnmanagedLabelDescriptionKept: a label sync does not author keeps its
// description, even one carrying the earlier hiss-waiver wording, and the file is not written.
func TestSync_Negative_UnmanagedLabelDescriptionKept(t *testing.T) {
	f := newSyncValidationFixture(t)
	taxonomy := customLabelTaxonomy("team:waivers", "fbca04", staleLabelDescriptions[1].stale)
	writeFixtureFile(t, f.dir, ".config/labels.yaml", taxonomy)
	out, err := runSyncCmd(t, "--config="+f.manifestPath)
	if err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}
	if strings.Contains(out, "[FIX] Updated managed label description") {
		t.Errorf("an unmanaged label was reconciled:\n%s", out)
	}
	if got := readFixtureFile(t, f.dir, ".config/labels.yaml"); got != taxonomy {
		t.Errorf("an unmanaged label's description changed:\n%s", got)
	}
}

// TestManagedLabelDescriptions_Boundary_MatchDefaultTaxonomy keeps the two places a managed
// description lives in step: sync rewrites a drifted description to exactly the text
// adoption scaffolds, so an adopted file and a synced one cannot disagree. Every revision
// sync migrates is managed, and its earlier wording is no longer canonical.
func TestManagedLabelDescriptions_Boundary_MatchDefaultTaxonomy(t *testing.T) {
	labels, err := forge.ParseLabelTaxonomy(forge.DefaultLabelTaxonomy())
	if err != nil {
		t.Fatal(err)
	}
	canonical := make(map[string]string, len(labels))
	for _, label := range labels {
		canonical[label.Name] = label.Description
	}
	for name, description := range managedLabelDescriptions {
		if got, ok := canonical[name]; !ok || got != description {
			t.Errorf("managed %s description %q, default taxonomy %q (present %v)", name, description, got, ok)
		}
	}
	for _, tc := range staleLabelDescriptions {
		if managedLabelDescriptions[tc.name] != tc.canonical || tc.stale == tc.canonical {
			t.Errorf("%s (%s): sync does not migrate %q to %q", tc.name, tc.issue, tc.stale, tc.canonical)
		}
	}
}
