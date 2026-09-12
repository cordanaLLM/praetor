package forge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestSyncIssuesRejectsIncompleteGitHubListingBeforeMutation(t *testing.T) {
	gh, fake := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, index int) {
		if r.Method != http.MethodGet {
			writeJSON(t, w, http.StatusCreated, map[string]any{"number": 2001})
			return
		}
		writeJSON(t, w, http.StatusOK, issuePage(index*100+1, 100, false))
	})

	report, err := SyncIssues(context.Background(), gh, []IssueSpec{{Title: "new finding"}})
	if err == nil {
		t.Fatalf("incomplete listing authorized mutation: report=%+v", report)
	}
	if report != nil || len(fake.requests) != 20 {
		t.Fatalf("want no report and exactly 20 listing requests; report=%+v requests=%d", report, len(fake.requests))
	}
	for _, request := range fake.requests {
		if request.Method != http.MethodGet {
			t.Fatalf("incomplete listing caused %s", request.Method)
		}
	}
}

func TestSyncIssuesUsesCompleteInventoryBeyondBatchLimit(t *testing.T) {
	for _, count := range []int{1001, 2000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			fake := &recordingForge{existing: inventoryIssues(count)}
			report, err := SyncIssues(context.Background(), fake, []IssueSpec{{Title: fmt.Sprintf("finding %d", count), State: "closed"}})
			if err != nil {
				t.Fatal(err)
			}
			if report.Created != 0 || report.Updated != 1 || len(fake.created) != 0 || len(fake.updated) != 1 {
				t.Fatalf("existing issue recreated: report=%+v created=%d updated=%d", report, len(fake.created), len(fake.updated))
			}
			if fake.updated[0].Number != count {
				t.Fatalf("updated issue %d, want %d", fake.updated[0].Number, count)
			}
		})
	}
}

func TestSyncIssuesRejectsOverBoundInventoryBeforeMutation(t *testing.T) {
	fake := &recordingForge{existing: inventoryIssues(2001)}
	report, err := SyncIssues(context.Background(), fake, []IssueSpec{{Title: "new finding"}})
	if err == nil || report != nil || len(fake.created) != 0 || len(fake.updated) != 0 {
		t.Fatalf("oversized inventory authorized a write: report=%+v err=%v created=%d", report, err, len(fake.created))
	}
}

func inventoryIssues(count int) []IssueSpec {
	issues := make([]IssueSpec, count)
	for i := 0; i < count; i++ {
		issues[i] = IssueSpec{ID: i + 1, Title: fmt.Sprintf("finding %d", i+1)}
	}
	return issues
}

func TestSyncIssuesRejectsDuplicatePlannedTitlesBeforeAllMutations(t *testing.T) {
	fake := &recordingForge{}
	batch := []IssueSpec{{Title: "first unique"}, {Title: " same finding "}, {Title: "same finding", State: "closed"}}
	report, err := SyncIssues(context.Background(), fake, batch)
	if err == nil || report != nil || len(fake.created) != 0 || len(fake.updated) != 0 {
		t.Fatalf("duplicate batch partially mutated: report=%+v err=%v created=%d", report, err, len(fake.created))
	}
	var conflict *IssueTitleConflictError
	if !errors.As(err, &conflict) || conflict.Title != "same finding" || conflict.Source != "planned" {
		t.Fatalf("expected typed planned identity conflict, got %v", err)
	}
}

func TestSyncIssuesRejectsAmbiguousSelectedTitleBeforeAllMutations(t *testing.T) {
	fake := &recordingForge{existing: []IssueSpec{{ID: 1, Title: " finding "}, {ID: 2, Title: "finding"}}}
	report, err := SyncIssues(context.Background(), fake, []IssueSpec{{Title: "new unique"}, {Title: "finding", State: "closed"}})
	if err == nil || report != nil || len(fake.created) != 0 || len(fake.updated) != 0 {
		t.Fatalf("ambiguous title partially mutated: report=%+v err=%v created=%d updated=%d", report, err, len(fake.created), len(fake.updated))
	}
	var conflict *IssueTitleConflictError
	if !errors.As(err, &conflict) || conflict.Title != "finding" || conflict.Source != "existing" {
		t.Fatalf("expected typed existing identity conflict, got %v", err)
	}
}

func TestSyncIssuesAllowsUnrelatedDuplicateTitles(t *testing.T) {
	fake := &recordingForge{existing: []IssueSpec{{ID: 1, Title: "finding"}, {ID: 2, Title: "finding"}}}
	report, err := SyncIssues(context.Background(), fake, []IssueSpec{{Title: "different finding"}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Created != 1 || len(fake.created) != 1 || len(fake.updated) != 0 {
		t.Fatalf("unexpected unrelated duplicate handling: report=%+v", report)
	}
}

func TestGitHubIssueInventoryCompletenessBoundaries(t *testing.T) {
	cases := []struct {
		name                                string
		fullPages, lastRows, want, requests int
		incomplete                          bool
	}{
		{"empty", 0, 0, 0, 1, false},
		{"full then empty", 1, 0, 100, 2, false},
		{"last partial", 19, 99, 1999, 20, false},
		{"full limit", 20, 0, 0, 20, true},
		{"oversized page", 0, 101, 0, 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gh, fake := newFakeForge(t, func(w http.ResponseWriter, _ *http.Request, index int) {
				rows := 0
				if index < tc.fullPages {
					rows = 100
				} else if index == tc.fullPages {
					rows = tc.lastRows
				}
				writeJSON(t, w, http.StatusOK, issuePage(index*100+1, rows, false))
			})
			issues, err := gh.ListIssues(context.Background(), "all")
			var incomplete *IssueListIncompleteError
			if errors.As(err, &incomplete) != tc.incomplete || (!tc.incomplete && err != nil) {
				t.Fatalf("unexpected completeness result: %v", err)
			}
			if len(issues) != tc.want || len(fake.requests) != tc.requests {
				t.Fatalf("issues=%d requests=%d; want %d/%d", len(issues), len(fake.requests), tc.want, tc.requests)
			}
		})
	}
}

func TestGitHubIssueInventoryRejectsMalformedRowsBeforeSync(t *testing.T) {
	for _, body := range []string{"null", "[null]", `[ {"number":1,"title":"  "} ]`, `[ {"number":0,"title":"finding"} ]`} {
		t.Run(body, func(t *testing.T) {
			gh, fake := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, _ int) {
				if r.Method != http.MethodGet {
					writeJSON(t, w, http.StatusCreated, map[string]any{"number": 2})
					return
				}
				if _, err := w.Write([]byte(body)); err != nil {
					t.Errorf("write malformed fixture: %v", err)
				}
			})
			report, err := SyncIssues(context.Background(), gh, []IssueSpec{{Title: "finding"}})
			if err == nil || report != nil || len(fake.requests) != 1 {
				t.Fatalf("malformed listing allowed mutation: report=%+v err=%v requests=%d", report, err, len(fake.requests))
			}
		})
	}
}
