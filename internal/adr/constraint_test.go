package adr

import (
	"strings"
	"testing"
)

const universalScopeRecord = "# ADR-1142\n\nThere is no upstream-code tier.\n\n" +
	"```adr-constraint\n" +
	"id: no-source-tiers\n" +
	"kind: universal-scope\n" +
	"gate: hiss-scan\n" +
	"forbids: [\"vendor/\", \"third_party/\"]\n" +
	"rationale: >-\n" +
	"  Scope is decided by the file being source, not by who wrote it.\n" +
	"```\n"

// Positive: a well-formed record yields the clause it declares.
func TestParse_Positive_ReadsADeclaredConstraint(t *testing.T) {
	constraints, err := Parse("docs/adr/1142.md", []byte(universalScopeRecord))
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if len(constraints) != 1 {
		t.Fatalf("expected one constraint, got %d", len(constraints))
	}
	got := constraints[0]
	if got.ID != "no-source-tiers" || got.Kind != KindUniversalScope || got.Gate != "hiss-scan" {
		t.Errorf("unexpected constraint: %+v", got)
	}
	if got.Record != "docs/adr/1142.md" {
		t.Errorf("constraint does not carry its record: %q", got.Record)
	}
}

// Negative, and the property that makes this worth having: a record cannot declare a check
// the engine does not implement. Accepting it would report the decision as enforced while
// nothing measured it -- the exact failure a prose-only ADR already has.
func TestParse_Negative_RejectsAKindTheEngineCannotCheck(t *testing.T) {
	record := strings.Replace(universalScopeRecord, "kind: universal-scope", "kind: vibes-based", 1)
	_, err := Parse("docs/adr/1142.md", []byte(record))
	if err == nil {
		t.Fatal("an unimplementable constraint kind was accepted")
	}
	if !strings.Contains(err.Error(), "vibes-based") || !strings.Contains(err.Error(), "nothing measures it") {
		t.Errorf("error does not explain why an unknown kind is refused: %v", err)
	}
}

// Negative: a constraint that forbids nothing can never fail, so it is a decoration.
func TestParse_Negative_RejectsAConstraintThatCannotFail(t *testing.T) {
	record := strings.Replace(universalScopeRecord, "forbids: [\"vendor/\", \"third_party/\"]", "forbids: []", 1)
	if _, err := Parse("docs/adr/1142.md", []byte(record)); err == nil ||
		!strings.Contains(err.Error(), "can never fail") {
		t.Fatalf("a vacuous constraint was accepted: %v", err)
	}
}

func TestParse_Negative_RequiresAnIDAndARationale(t *testing.T) {
	for _, removal := range []string{"id: no-source-tiers\n", "rationale: >-\n  Scope is decided by the file being source, not by who wrote it.\n"} {
		record := strings.Replace(universalScopeRecord, removal, "", 1)
		if _, err := Parse("docs/adr/1142.md", []byte(record)); err == nil {
			t.Errorf("a constraint missing %q was accepted", strings.SplitN(removal, ":", 2)[0])
		}
	}
}

// Boundary: a record with no constraint block declares nothing, which is not an error --
// most records are prose and must stay that way.
func TestParse_Boundary_ARecordWithoutConstraintsIsNotAnError(t *testing.T) {
	constraints, err := Parse("docs/adr/0001.md", []byte("# ADR-0001\n\nProse only.\n"))
	if err != nil || len(constraints) != 0 {
		t.Fatalf("prose-only record: %v %v", constraints, err)
	}
}

// Boundary: the fence is labelled, so a constraint shown inside a fenced example in the
// documentation is not read as a live declaration.
func TestParse_Boundary_IgnoresAnUnlabelledFence(t *testing.T) {
	record := strings.Replace(universalScopeRecord, "```adr-constraint", "```yaml", 1)
	constraints, err := Parse("docs/adr/1142.md", []byte(record))
	if err != nil || len(constraints) != 0 {
		t.Fatalf("an unlabelled fence was read as a declaration: %v %v", constraints, err)
	}
}

func TestParse_Boundary_RejectsAnOversizedRecord(t *testing.T) {
	if _, err := Parse("docs/adr/big.md", make([]byte, maxRecordBytes+1)); err == nil {
		t.Fatal("an oversized record was accepted")
	}
}

// Positive: the check fires on the shape that motivated it -- an exclusion file reintroducing
// a tier the record abolished.
func TestCheckConstraint_Positive_ReportsAReintroducedTier(t *testing.T) {
	constraints, err := Parse("docs/adr/1142.md", []byte(universalScopeRecord))
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	tracked := map[string]string{".semgrepignore": "# generated\nvendor/\nbuild/\n"}
	findings := checkConstraint(constraints[0], tracked)
	if len(findings) != 1 {
		t.Fatalf("expected one finding, got %d: %v", len(findings), findings)
	}
	if !strings.Contains(findings[0].Detail, "vendor/") || !strings.Contains(findings[0].Detail, "hiss-scan") {
		t.Errorf("finding names neither the exclusion nor the gate: %s", findings[0])
	}
	if !strings.Contains(findings[0].String(), "because:") {
		t.Error("a finding must carry the record's rationale; the reason is what a reader needs")
	}
}

// Negative: a comment mentioning the term is not an exclusion.
func TestCheckConstraint_Negative_DoesNotFireOnAComment(t *testing.T) {
	constraints, err := Parse("docs/adr/1142.md", []byte(universalScopeRecord))
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	tracked := map[string]string{".semgrepignore": "# vendor/ is deliberately NOT excluded\nbuild/\n"}
	if findings := checkConstraint(constraints[0], tracked); len(findings) != 0 {
		t.Fatalf("a comment was read as an exclusion: %v", findings)
	}
}

// Boundary: no exclusion file at all, and an empty one.
func TestCheckConstraint_Boundary_NoExclusionSources(t *testing.T) {
	constraints, err := Parse("docs/adr/1142.md", []byte(universalScopeRecord))
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	for _, tracked := range []map[string]string{{}, {".semgrepignore": ""}, {".gitignore": "\n\n"}} {
		if findings := checkConstraint(constraints[0], tracked); len(findings) != 0 {
			t.Errorf("clean repository reported %v", findings)
		}
	}
}
