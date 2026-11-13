package reviewmerge

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/router"
)

// draftArbiter supplies fresh observed capacity for the real orthogonal selector.
func draftArbiter() *router.ModelCapacityArbiter {
	cfg := &router.RoutingConfig{
		Governance: router.GovernancePolicy{QuotaReadingMaxAge: router.DefaultQuotaReadingMaxAge, ExhaustionThresholdPercent: 80},
		Tiers: map[string]router.Tier{"review": {Models: []router.ModelDescriptor{
			{ID: "one", Family: router.FamilyAnthropic}, {ID: "two", Family: router.FamilyGoogle},
		}}},
	}
	a := router.NewModelCapacityArbiter(cfg, nil)
	a.Tracker.RecordUsage("one", 0, 0)
	a.Tracker.RecordUsage("two", 0, 0)
	return a
}

// TestReviewOrthogonalConsumer obtains the second selected draft and feeds both into validation.
func TestReviewOrthogonalConsumer(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	d, f := completeReview()
	calls := 0
	review := func(got context.Context, model router.ModelDescriptor) (Draft, error) {
		calls++
		if got != ctx || model.ID != "two" || model.Family == router.FamilyAnthropic {
			t.Fatalf("selection/context: %+v", model)
		}
		return Draft{Model: model.ID, Kind: "code", Findings: []Finding{}}, nil
	}
	drafts, err := CollectOrthogonalDraft(ctx, draftArbiter(), router.FamilyAnthropic, "review", d[0], review)
	if err != nil || len(drafts) != 2 || calls != 1 {
		t.Fatalf("second consumer: drafts=%v calls=%d err=%v", drafts, calls, err)
	}
	if p := Validate(drafts, f); len(p) != 0 {
		t.Fatal(p)
	}
}

// collectorErrorCase constructs one invalid consumer boundary without external I/O.
func collectorErrorCase(t *testing.T, mode string) (context.Context, *router.ModelCapacityArbiter, Draft, DraftReviewer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	d, _ := completeReview()
	a := draftArbiter()
	review := collectorErrorReviewer(mode)
	input := ctx
	switch mode {
	case "unavailable":
		a.Config.Tiers = map[string]router.Tier{}
	case "canceled":
		cancel()
	case "deadline":
		input = t.Context()
	case "nil context":
		input = nil
	case "nil arbiter":
		a = nil
	case "nil reviewer":
		review = nil
	case "malformed":
		d[0].Findings = nil
	}
	return input, a, d[0], review
}

// collectorErrorReviewer supplies either a provider failure or a mismatched model identity.
func collectorErrorReviewer(mode string) DraftReviewer {
	return func(_ context.Context, model router.ModelDescriptor) (Draft, error) {
		if mode == "provider" {
			return Draft{}, errors.New("provider failed")
		}
		if mode == "identity" {
			model.ID = "unselected"
		}
		return Draft{Model: model.ID, Kind: "code", Findings: []Finding{}}, nil
	}
}

// TestReviewOrthogonalConsumerErrors fails closed on selection, provider and identity failures.
func TestReviewOrthogonalConsumerErrors(t *testing.T) {
	for _, mode := range []string{"unavailable", "provider", "identity", "canceled", "deadline", "nil context", "nil arbiter", "nil reviewer", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			ctx, arbiter, first, review := collectorErrorCase(t, mode)
			if _, err := CollectOrthogonalDraft(ctx, arbiter, router.FamilyAnthropic, "review", first, review); err == nil {
				t.Fatal("invalid collection accepted")
			}
		})
	}
}

// TestReviewOrthogonalConsumerProviderEvidence preserves the provider's failure as an error cause.
func TestReviewOrthogonalConsumerProviderEvidence(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	d, _ := completeReview()
	want := errors.New("review unavailable")
	review := func(context.Context, router.ModelDescriptor) (Draft, error) { return Draft{}, want }
	_, err := CollectOrthogonalDraft(ctx, draftArbiter(), router.FamilyAnthropic, "review", d[0], review)
	if !errors.Is(err, want) || !strings.Contains(err.Error(), "two") {
		t.Fatalf("provider cause and selected model missing: %v", err)
	}
}
