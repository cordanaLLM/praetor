package baseline

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// sampleInfractions returns n distinct fingerprinted infractions.
func sampleInfractions(n int) []Infraction {
	out := make([]Infraction, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, Infraction{
			RuleID: "HISS-07", FilePath: "a.go", LineNumber: i + 1,
			Fingerprint: fmt.Sprintf("a.go:%d:HISS-07", i+1),
		})
	}
	return out
}

func TestRecord_Positive(t *testing.T) {
	prev := &Baseline{
		Version: 1, Repository: "acme/widgets", CommitSHA: "abc",
		Infractions: sampleInfractions(3), TotalInfractions: 3, IncreaseRationale: "old",
	}

	// Debt reduction is recorded and drops the stale rationale.
	next, err := Record(prev, sampleInfractions(2), RecordOptions{})
	if err != nil {
		t.Fatalf("Record decrease: %v", err)
	}
	if next.TotalInfractions != 2 || len(next.Infractions) != 2 {
		t.Fatalf("expected 2 infractions, got total=%d len=%d", next.TotalInfractions, len(next.Infractions))
	}
	if next.IncreaseRationale != "" {
		t.Fatalf("rationale must be cleared when debt does not grow, got %q", next.IncreaseRationale)
	}
	if next.Repository != "acme/widgets" || next.CommitSHA != "abc" || next.Version != 1 {
		t.Fatalf("identity fields not carried over: %+v", next)
	}

	// A deliberate increase carries its trimmed rationale.
	grown, err := Record(prev, sampleInfractions(4), RecordOptions{AllowIncrease: true, Rationale: "  scanner rule HISS-42 added  "})
	if err != nil {
		t.Fatalf("Record allowed increase: %v", err)
	}
	if grown.TotalInfractions != 4 || grown.IncreaseRationale != "scanner rule HISS-42 added" {
		t.Fatalf("unexpected grown snapshot: %+v", grown)
	}
}

func TestRecord_Negative(t *testing.T) {
	prev := &Baseline{Version: 1, Infractions: sampleInfractions(1), TotalInfractions: 1}

	if _, err := Record(prev, sampleInfractions(2), RecordOptions{}); !errors.Is(err, ErrDebtIncrease) {
		t.Fatalf("expected ErrDebtIncrease, got %v", err)
	}
	_, err := Record(prev, sampleInfractions(2), RecordOptions{AllowIncrease: true, Rationale: "   "})
	if !errors.Is(err, ErrIncreaseRationaleRequired) || !errors.Is(err, ErrDebtIncrease) {
		t.Fatalf("expected rationale-required error wrapping ErrDebtIncrease, got %v", err)
	}

	if err := CheckMonotonic(nil, prev); !errors.Is(err, ErrNilSnapshot) {
		t.Fatalf("expected ErrNilSnapshot for nil previous, got %v", err)
	}
	if err := CheckMonotonic(prev, nil); !errors.Is(err, ErrNilSnapshot) {
		t.Fatalf("expected ErrNilSnapshot for nil next, got %v", err)
	}
	if err := SaveBaseline(filepath.Join(t.TempDir(), "b.json"), nil); !errors.Is(err, ErrNilSnapshot) {
		t.Fatalf("expected ErrNilSnapshot from SaveBaseline, got %v", err)
	}
	if _, err := ParseBaseline([]byte("{not json")); err == nil {
		t.Fatal("expected ParseBaseline to reject malformed JSON")
	}
	// A directory is not a baseline: the read error is surfaced, not treated as empty.
	if _, err := LoadBaseline(t.TempDir()); err == nil {
		t.Fatal("expected LoadBaseline to fail on a directory")
	}
}

func TestRecord_Boundary(t *testing.T) {
	// A nil previous snapshot behaves like an empty baseline.
	next, err := Record(nil, nil, RecordOptions{})
	if err != nil {
		t.Fatalf("Record from nil: %v", err)
	}
	if next.Version != 1 || next.TotalInfractions != 0 || next.Infractions == nil {
		t.Fatalf("unexpected empty snapshot: %+v", next)
	}

	// Equal counts never need a rationale, even when an increase would be allowed.
	prev := &Baseline{Version: 2, Infractions: sampleInfractions(2), TotalInfractions: 2}
	same, err := Record(prev, sampleInfractions(2), RecordOptions{AllowIncrease: true, Rationale: "unused"})
	if err != nil {
		t.Fatalf("Record equal: %v", err)
	}
	if same.IncreaseRationale != "" || same.Version != 2 {
		t.Fatalf("equal snapshot must carry no rationale and keep the version: %+v", same)
	}

	// Count trusts the entries over a hand-edited total, and tolerates nil.
	edited := &Baseline{TotalInfractions: 0, Infractions: sampleInfractions(3)}
	if edited.Count() != 3 {
		t.Fatalf("Count() = %d, want 3", edited.Count())
	}
	var nilBaseline *Baseline
	if nilBaseline.Count() != 0 {
		t.Fatalf("nil Count() = %d, want 0", nilBaseline.Count())
	}
	if err := CheckMonotonic(&Baseline{TotalInfractions: 3}, edited); err != nil {
		t.Fatalf("equal counts must pass: %v", err)
	}

	// ParseBaseline normalises a missing infractions array.
	parsed, err := ParseBaseline([]byte(`{"version":1,"total_infractions":0}`))
	if err != nil || parsed.Infractions == nil {
		t.Fatalf("ParseBaseline minimal: err=%v infractions=%v", err, parsed)
	}

	// SaveBaseline round-trips the rationale with the tracked-artifact mode.
	path := filepath.Join(t.TempDir(), "b.json")
	if err := SaveBaseline(path, &Baseline{Version: 1, Infractions: sampleInfractions(1), IncreaseRationale: "why"}); err != nil {
		t.Fatalf("SaveBaseline: %v", err)
	}
	loaded, err := LoadBaseline(path)
	if err != nil || loaded.IncreaseRationale != "why" || loaded.TotalInfractions != 1 {
		t.Fatalf("round trip: err=%v loaded=%+v", err, loaded)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if util.ModeIsProtection() && info.Mode().Perm() != FilePerm {
		t.Fatalf("baseline mode = %v, want %v", info.Mode().Perm(), FilePerm)
	}
}
