package notebook

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func fixture(t *testing.T) (Bundle, []byte) {
	t.Helper()
	b := Bundle{Format: Format, NotebookID: "fixture", Title: "Plan", Connector: "test",
		CapturedAt: "2026-09-12T00:00:00Z", Coverage: []string{"Synthetic source and note only"},
		Sources: []Source{{ID: "s1", Title: "Scope", Role: "source", Locator: "fixture:source",
			Content: "Keep original records. Treat proposals as drafts."}}}
	b.Sources[0].SHA256 = Digest([]byte(b.Sources[0].Content))
	return b, encode(t, b)
}

func encode(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestPreparePreservesDataAndReportsOnlyMetadata(t *testing.T) {
	b, raw := fixture(t)
	p, err := Prepare(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(p.Files["sources.json"]) != string(raw) || p.SourceCount != 1 || !p.ReviewRequired {
		t.Fatal("snapshot or metadata changed")
	}
	metadata := string(encode(t, p))
	if strings.Contains(metadata, b.Sources[0].Content) {
		t.Fatal("source content leaked in metadata")
	}
	if !strings.Contains(string(p.Files["prompt.md"]), p.BundleSHA256) {
		t.Fatal("prompt not bound to snapshot")
	}
}

func TestPrepareRejectsMalformedSnapshots(t *testing.T) {
	b, _ := fixture(t)
	for _, mutate := range []func(*Bundle){
		func(b *Bundle) { b.Format = "unknown" },
		func(b *Bundle) { b.Coverage = nil },
		func(b *Bundle) { b.Sources[0].SHA256 = "wrong" },
		func(b *Bundle) { b.Sources = append(b.Sources, b.Sources[0]) },
		func(b *Bundle) { b.Sources[0].Content = "" },
	} {
		copy := b
		copy.Sources = append([]Source(nil), b.Sources...)
		mutate(&copy)
		if _, err := Prepare(context.Background(), encode(t, copy)); err == nil {
			t.Fatal("invalid bundle accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Prepare(ctx, encode(t, b)); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestPrepareBoundsAndDuplicateKeys(t *testing.T) {
	b, _ := fixture(t)
	b.Sources[0].Content = strings.Repeat("x", MaxContentBytes)
	b.Sources[0].SHA256 = Digest([]byte(b.Sources[0].Content))
	if _, err := Prepare(context.Background(), encode(t, b)); err != nil {
		t.Fatal(err)
	}
	b.Sources[0].Content += "x"
	b.Sources[0].SHA256 = Digest([]byte(b.Sources[0].Content))
	if _, err := Prepare(context.Background(), encode(t, b)); err == nil {
		t.Fatal("oversized source accepted")
	}
	for _, raw := range []string{`{"format":"x","format":"y"}`, `{"a":{"k":1,"k":2}}`, `{}`, `null`, `[]`, `{} {}`, strings.Repeat("[", 33) + strings.Repeat("]", 33)} {
		if _, err := Prepare(context.Background(), []byte(raw)); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	var value any
	if err := Decode([]byte(`{"a":{"k":1},"b":[{"k":2}]}`), &value); err != nil {
		t.Fatal(err)
	}
}

func TestValidateGenerationCitations(t *testing.T) {
	b, raw := fixture(t)
	g := Generation{BundleSHA256: Digest(raw), Requirements: []Requirement{{ID: "R1", Text: "Preserve originals",
		SourceID: "s1", SourceSHA256: b.Sources[0].SHA256, Quote: "Keep original records."}},
		Documents: map[string]string{"project_plan": "Draft R1", "specification": "Draft R1", "tasks": "Draft R1"}}
	if _, err := ValidateGeneration(context.Background(), raw, encode(t, g)); err != nil {
		t.Fatal(err)
	}
	g.Requirements[0].Quote = "A fact that is not present."
	if _, err := ValidateGeneration(context.Background(), raw, encode(t, g)); err == nil {
		t.Fatal("invented citation accepted")
	}
	g.Requirements[0].Quote = "Keep original records."
	delete(g.Documents, "tasks")
	if _, err := ValidateGeneration(context.Background(), raw, encode(t, g)); err == nil {
		t.Fatal("missing document accepted")
	}
}
