package forge

import (
	"context"
	"reflect"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// #1285: metadata the repository holds and the manifest leaves unset is reported as undeclared
// with the repository's values, and still never written or cleared.

func liveWithEverything() map[string]any {
	return map[string]any{
		"description": "Widget engine", "homepage": "http://widgets.example", "visibility": "public",
		"topics": []any{"legacy", "governance"},
	}
}

// Positive: a manifest equal to the repository leaves nothing undeclared.
func TestReconcileRepositoryMetadata_Positive_MatchingManifestLeavesNothingUndeclared(t *testing.T) {
	gh, fake, _ := newMetadataForge(t, liveWithEverything(), nil)
	report, err := gh.ReconcileRepositoryMetadata(context.Background(), config.RepositoryMetadata{
		Description: "Widget engine", Homepage: "http://widgets.example", Topics: []string{"governance", "legacy"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.HasUndeclared() || len(writes(fake)) != 0 {
		t.Fatalf("report %+v, writes %v; want nothing undeclared and nothing written", report, writes(fake))
	}
}

// Negative: an empty manifest against a repository holding all three reports all three, with
// the repository's values, and writes nothing.
func TestReconcileRepositoryMetadata_Negative_EmptyManifestReportsEveryUndeclaredField(t *testing.T) {
	gh, fake, forge := newMetadataForge(t, liveWithEverything(), nil)
	report, err := gh.ReconcileRepositoryMetadata(context.Background(), config.RepositoryMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	if report.UndeclaredDescription != "Widget engine" || report.UndeclaredHomepage != "http://widgets.example" ||
		!reflect.DeepEqual(report.UndeclaredTopics, []string{"legacy", "governance"}) || !report.HasUndeclared() {
		t.Fatalf("undeclared = %q, %q, %v; want the repository's three values", report.UndeclaredDescription, report.UndeclaredHomepage, report.UndeclaredTopics)
	}
	if got := writes(fake); len(got) != 0 || !reflect.DeepEqual(forge.live, liveWithEverything()) {
		t.Fatalf("writes %v, live %v; nothing may be written", got, forge.live)
	}
}

// Boundary: a topic the manifest does not name is undeclared and kept; a blank-only description
// counts as unset; a declared, differing homepage is written and not undeclared; a declared topic
// matches whatever its case; an empty repository field is not undeclared.
func TestReconcileRepositoryMetadata_Boundary_PartlyDeclaredManifest(t *testing.T) {
	live := liveWithEverything()
	gh, fake, forge := newMetadataForge(t, live, nil)
	report, err := gh.ReconcileRepositoryMetadata(context.Background(), config.RepositoryMetadata{
		Description: "   ", Homepage: "https://widgets.example", Topics: []string{"GOVERNANCE"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.UndeclaredDescription != "Widget engine" || report.UndeclaredHomepage != "" ||
		!reflect.DeepEqual(report.UndeclaredTopics, []string{"legacy"}) {
		t.Fatalf("undeclared = %q, %q, %v; want the description and legacy only", report.UndeclaredDescription, report.UndeclaredHomepage, report.UndeclaredTopics)
	}
	if !reflect.DeepEqual(report.Updated, []string{"homepage"}) || forge.live["homepage"] != "https://widgets.example" {
		t.Fatalf("updated %v, live homepage %v; the declared homepage must be written", report.Updated, forge.live["homepage"])
	}
	if topics, _ := forge.live["topics"].([]any); len(topics) != 2 {
		t.Fatalf("an undeclared topic was removed: %v (writes %v)", forge.live["topics"], writes(fake))
	}

	bare, _, _ := newMetadataForge(t, map[string]any{"description": "", "homepage": "", "visibility": "public", "topics": []any{}}, nil)
	empty, err := bare.ReconcileRepositoryMetadata(context.Background(), config.RepositoryMetadata{})
	if err != nil || empty.HasUndeclared() {
		t.Fatalf("an empty repository reads as undeclared metadata: %+v (%v)", empty, err)
	}
}
