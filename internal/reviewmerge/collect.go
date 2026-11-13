package reviewmerge

import (
	"context"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/router"
)

// DraftReviewer obtains an ungraded draft from the selected model under the invocation deadline.
type DraftReviewer func(context.Context, router.ModelDescriptor) (Draft, error)

// CollectOrthogonalDraft pairs an existing draft with a selected, independently produced draft.
func CollectOrthogonalDraft(ctx context.Context, arbiter *router.ModelCapacityArbiter, family router.ModelFamily, tier string, first Draft, review DraftReviewer) ([]Draft, error) {
	if err := validateCollectorInputs(ctx, arbiter, family, review); err != nil {
		return nil, err
	}
	if err := validateCollectedDrafts([]Draft{first}); err != nil {
		return nil, err
	}
	model, err := arbiter.SelectOrthogonalAuditor(family, tier)
	if err != nil {
		return nil, fmt.Errorf("select orthogonal reviewer: %w", err)
	}
	second, err := review(ctx, *model)
	if err != nil {
		return nil, fmt.Errorf("review from %s: %w", model.ID, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("review canceled: %w", err)
	}
	if second.Model != model.ID {
		return nil, fmt.Errorf("review model %q differs from selected model %q", second.Model, model.ID)
	}
	drafts := []Draft{first, second}
	if err := validateCollectedDrafts(drafts); err != nil {
		return nil, err
	}
	return drafts, nil
}

// validateCollectorInputs requires a bounded invocation and an explicit selector and reviewer.
func validateCollectorInputs(ctx context.Context, arbiter *router.ModelCapacityArbiter, family router.ModelFamily, review DraftReviewer) error {
	if ctx == nil {
		return fmt.Errorf("review collection requires a context")
	}
	if _, ok := ctx.Deadline(); !ok {
		return fmt.Errorf("review collection requires a context deadline")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("review collection canceled: %w", err)
	}
	if arbiter == nil || review == nil || family == "" {
		return fmt.Errorf("review collection requires an arbiter, reviewer and author family")
	}
	return nil
}

// validateCollectedDrafts reuses the merge validator before retaining callback-produced drafts.
func validateCollectedDrafts(drafts []Draft) error {
	v := newValidator()
	if v.checkBounds(drafts, Final{}) {
		v.checkDrafts(drafts)
	}
	if len(v.problems) != 0 {
		return fmt.Errorf("invalid collected drafts: %v", v.problems)
	}
	return nil
}
