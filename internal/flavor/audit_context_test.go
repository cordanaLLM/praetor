package flavor_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/flavor"
)

// AuditFlavorContext under a live context returns the same verdict as AuditFlavor.
func TestAuditFlavorContext_Positive_LiveContextMatchesAuditFlavor(t *testing.T) {
	emptyPATH(t)
	repo := conformingGoLibrary(t, nil)
	want, err := flavor.AuditFlavor(repo, "go-library")
	if err != nil {
		t.Fatalf("AuditFlavor: %v", err)
	}
	got, err := flavor.AuditFlavorContext(t.Context(), repo, "go-library")
	if err != nil {
		t.Fatalf("AuditFlavorContext: %v", err)
	}
	if got.Flavor != want.Flavor || got.Score != want.Score || got.Passed != want.Passed {
		t.Errorf("context audit = %s %.1f %v, want %s %.1f %v",
			got.Flavor, got.Score, got.Passed, want.Flavor, want.Score, want.Passed)
	}
}

// A cancelled context stops the audit with the context's error and no verdict, whether the
// flavor is detected or named.
func TestAuditFlavorContext_Negative_CancelledContextStopsTheAudit(t *testing.T) {
	repo := conformingGoLibrary(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, target := range []string{"auto", "go-library"} {
		rep, err := flavor.AuditFlavorContext(ctx, repo, target)
		if !errors.Is(err, context.Canceled) || rep != nil {
			t.Errorf("%s: got %+v, %v; want nil report and context.Canceled", target, rep, err)
		}
	}
}

// countdownCtx is live for its first `left` Err calls and cancelled afterwards, which lets a
// test cancel an audit part-way through without racing a timer.
type countdownCtx struct {
	context.Context
	left int
}

func (c *countdownCtx) Err() error {
	if c.left <= 0 {
		return context.Canceled
	}
	c.left--
	return nil
}

// Boundary: a context cancelled after the audit started stops it at the next file, rather
// than letting it finish; the gate stage used to check only before and after the audit.
func TestAuditFlavorContext_Boundary_CancelledMidAuditStopsAtTheNextItem(t *testing.T) {
	repo := conformingGoLibrary(t, nil)
	ctx := &countdownCtx{Context: context.Background(), left: 2}
	rep, err := flavor.AuditFlavorContext(ctx, repo, "go-library")
	if !errors.Is(err, context.Canceled) || rep != nil {
		t.Fatalf("got %+v, %v; want nil report and context.Canceled", rep, err)
	}
	if !strings.Contains(err.Error(), "before template") {
		t.Errorf("the error must name the item the audit stopped at, got %v", err)
	}
}

// Boundary: an expired deadline is reported as such, and a nil context is refused.
func TestAuditFlavorContext_Boundary_ExpiredDeadlineAndNilContext(t *testing.T) {
	repo := conformingGoLibrary(t, nil)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if _, err := flavor.AuditFlavorContext(ctx, repo, "go-library"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expired deadline: got %v, want context.DeadlineExceeded", err)
	}
	//nolint:staticcheck // exercising the documented nil-context contract
	if _, err := flavor.AuditFlavorContext(nil, repo, "go-library"); err == nil {
		t.Error("a nil context must be refused")
	}
}
