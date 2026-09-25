package forge

import (
	"context"
	"strings"
	"testing"
)

// nilCreateForge is a recordingForge whose driver reports success without an issue.
type nilCreateForge struct{ recordingForge }

func (n *nilCreateForge) CreateIssue(context.Context, IssueSpec) (*IssueResponse, error) {
	return nil, nil
}

func TestIssueBatch_Positive_EnsureResolvesAndUpsertConverges(t *testing.T) {
	ctx := context.Background()
	fake := &recordingForge{existing: []IssueSpec{{ID: 7, Title: "existing", State: "closed"}}}
	planned := []IssueSpec{{Title: "existing", State: "open", Labels: []string{"epic"}}, {Title: "missing"}}

	batch, err := PrepareIssueBatch(ctx, fake, planned)
	if err != nil {
		t.Fatalf("prepare failed: %v", err)
	}
	found, err := batch.Ensure(ctx, planned[0])
	if err != nil {
		t.Fatalf("ensure existing failed: %v", err)
	}
	if found.Number != 7 || found.Outcome != IssueUnchanged || found.State != "closed" || len(fake.updated) != 0 {
		t.Fatalf("ensure must resolve the existing issue untouched: %+v updates=%+v", found, fake.updated)
	}
	created, err := batch.Ensure(ctx, planned[1])
	if err != nil {
		t.Fatalf("ensure missing failed: %v", err)
	}
	if created.Outcome != IssueCreated || created.Number != 1 || len(fake.created) != 1 {
		t.Fatalf("ensure must create the missing issue: %+v created=%d", created, len(fake.created))
	}

	upserted, err := batch.Upsert(ctx, planned[0])
	if err != nil {
		t.Fatalf("upsert failed: %v", err)
	}
	if upserted.Outcome != IssueUpdated || upserted.State != "open" || len(fake.updated) != 1 || fake.updated[0].Number != 7 {
		t.Fatalf("upsert must converge the existing issue: %+v updates=%+v", upserted, fake.updated)
	}
}

func TestIssueBatch_Negative_RefusesUnpreparedSpecsAndEmptyCreates(t *testing.T) {
	ctx := context.Background()
	fake := &recordingForge{}
	batch, err := PrepareIssueBatch(ctx, fake, []IssueSpec{{Title: "planned"}})
	if err != nil {
		t.Fatalf("prepare failed: %v", err)
	}
	if _, err := batch.Ensure(ctx, IssueSpec{Title: "never checked"}); err == nil || len(fake.created) != 0 {
		t.Fatalf("a title outside the batch must be refused before any write: err=%v created=%d", err, len(fake.created))
	}
	var unprepared *IssueBatch
	if _, err := unprepared.Upsert(ctx, IssueSpec{Title: "planned"}); err == nil {
		t.Fatal("a nil batch must refuse to upsert")
	}

	empty := &nilCreateForge{}
	emptyBatch, err := PrepareIssueBatch(ctx, empty, []IssueSpec{{Title: "planned"}})
	if err != nil {
		t.Fatalf("prepare failed: %v", err)
	}
	if _, err := emptyBatch.Ensure(ctx, IssueSpec{Title: "planned"}); err == nil || !strings.Contains(err.Error(), "returned no issue") {
		t.Fatalf("a create without an issue must fail, got %v", err)
	}

	if _, err := PrepareIssueBatch(ctx, nil, []IssueSpec{{Title: "planned"}}); err == nil {
		t.Fatal("a nil forge must be refused")
	}
}

func TestIssueBatch_Boundary_CreatedTitleJoinsTheIndex(t *testing.T) {
	ctx := context.Background()
	fake := &recordingForge{}
	batch, err := PrepareIssueBatch(ctx, fake, []IssueSpec{{Title: " padded "}})
	if err != nil {
		t.Fatalf("prepare failed: %v", err)
	}
	first, err := batch.Ensure(ctx, IssueSpec{Title: " padded "})
	if err != nil {
		t.Fatalf("first ensure failed: %v", err)
	}
	// The trimmed title is the identity, and a created issue resolves from then on.
	again, err := batch.Ensure(ctx, IssueSpec{Title: "padded"})
	if err != nil {
		t.Fatalf("second ensure failed: %v", err)
	}
	if again.Number != first.Number || again.Outcome != IssueUnchanged || len(fake.created) != 1 {
		t.Fatalf("ensuring a created title again duplicated it: first=%+v again=%+v created=%d", first, again, len(fake.created))
	}
	if _, err := batch.Upsert(ctx, IssueSpec{Title: "padded"}); err != nil || len(fake.updated) != 0 {
		t.Fatalf("an upsert with nothing to converge must not update: err=%v updates=%+v", err, fake.updated)
	}
}
