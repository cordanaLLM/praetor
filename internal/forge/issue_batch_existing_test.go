package forge

import (
	"context"
	"testing"
)

// Positive: Existing returns the listed issue with its body, and a created issue with the
// body it was created with.
func TestIssueBatch_Existing_Positive(t *testing.T) {
	ctx := context.Background()
	fake := &recordingForge{existing: []IssueSpec{{ID: 7, Title: "listed", Body: "- [ ] #8", State: "open"}}}
	planned := []IssueSpec{{Title: "listed"}, {Title: "created", Body: "fresh body"}}
	batch, err := PrepareIssueBatch(ctx, fake, planned)
	if err != nil {
		t.Fatal(err)
	}
	listed, found, err := batch.Existing(planned[0])
	if err != nil || !found || listed.ID != 7 || listed.Body != "- [ ] #8" {
		t.Fatalf("listed issue: %+v found=%t err=%v", listed, found, err)
	}
	if _, err := batch.Ensure(ctx, planned[1]); err != nil {
		t.Fatal(err)
	}
	created, found, err := batch.Existing(planned[1])
	if err != nil || !found || created.Body != "fresh body" || created.ID != 1 {
		t.Fatalf("created issue: %+v found=%t err=%v", created, found, err)
	}
}

// Negative: a spec outside the prepared batch is refused.
func TestIssueBatch_Existing_Negative(t *testing.T) {
	batch, err := PrepareIssueBatch(context.Background(), &recordingForge{}, []IssueSpec{{Title: "planned"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := batch.Existing(IssueSpec{Title: "other"}); err == nil {
		t.Fatal("a spec outside the batch must be refused")
	}
}

// Boundary: a planned title neither listed nor created yet is not found, without error,
// and surrounding whitespace does not change the identity.
func TestIssueBatch_Existing_Boundary(t *testing.T) {
	fake := &recordingForge{existing: []IssueSpec{{ID: 3, Title: "padded", Body: "b"}}}
	batch, err := PrepareIssueBatch(context.Background(), fake, []IssueSpec{{Title: "missing"}, {Title: " padded "}})
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := batch.Existing(IssueSpec{Title: "missing"}); err != nil || found {
		t.Fatalf("unresolved title: found=%t err=%v", found, err)
	}
	if got, found, err := batch.Existing(IssueSpec{Title: "padded"}); err != nil || !found || got.ID != 3 {
		t.Fatalf("trimmed identity: %+v found=%t err=%v", got, found, err)
	}
}
