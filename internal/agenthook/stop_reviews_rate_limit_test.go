package agenthook

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
)

type rateLimitedStopReviews struct {
	*stopReviewFixture
	ownErr         error
	owns           int
	batchSnapshots map[int]forge.ReviewSnapshot
}

// OwnOpen counts inventory attempts and optionally plants a discovery failure.
func (f *rateLimitedStopReviews) OwnOpen(ctx context.Context, repo string) ([]int, error) {
	f.owns++
	if f.ownErr != nil {
		return nil, f.ownErr
	}
	return f.stopReviewFixture.OwnOpen(ctx, repo)
}

// ReadBatch returns planted partial results or delegates complete reads to the shared fixture.
func (f *rateLimitedStopReviews) ReadBatch(ctx context.Context, repo string, numbers []int) (map[int]forge.ReviewSnapshot, error) {
	if f.batchSnapshots != nil {
		return f.batchSnapshots, f.err
	}
	return f.stopReviewFixture.ReadBatch(ctx, repo, numbers)
}

// installRateLimitedStopReviews installs a reader and identity without invoking git or gh.
func installRateLimitedStopReviews(t *testing.T, reader *rateLimitedStopReviews) (string, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "praetor-stop-reviews.json")
	previousReader, previousIdentity := stopReviewReader, stopReviewIdentity
	stopReviewReader = func(string) forge.ReviewReader { return reader }
	stopReviewIdentity = func(context.Context, string) (string, string, string, error) {
		return "example/widget", "same-head", path, nil
	}
	t.Cleanup(func() { stopReviewReader, stopReviewIdentity = previousReader, previousIdentity })
	return root, path
}

// TestStopReviewRateLimitedDeferral plants discovery and batch failures and requires a fresh retry.
func TestStopReviewRateLimitedDeferral(t *testing.T) {
	for _, phase := range []string{"inventory", "batch"} {
		t.Run(phase, func(t *testing.T) { testStopReviewRateLimitedDeferral(t, phase) })
	}
}

// testStopReviewRateLimitedDeferral verifies uncached retries and recovery to an unresolved-thread denial.
func testStopReviewRateLimitedDeferral(t *testing.T, phase string) {
	t.Helper()
	limited := &forge.ErrRateLimited{Bucket: "graphql", Remaining: "37", Limit: "100", Reset: "22:45",
		Cause: errors.New("gh_guard: review paced (graphql 37 of 100 remaining), reset 22:45")}
	fixture := &rateLimitedStopReviews{stopReviewFixture: reviewFixture()}
	failure := fmt.Errorf("read reviews: %w", limited)
	if phase == "inventory" {
		fixture.ownErr = failure
	} else {
		fixture.err = failure
	}
	root, path := installRateLimitedStopReviews(t, fixture)
	for range 2 {
		verdict := evaluateStopReviewThreads(t.Context(), root)
		if verdict.Outcome != Allow || verdict.Reason != "" || verdict.Notice != "review inventory deferred: graphql 37 of 100, reset 22:45" {
			t.Fatalf("paced %s: %+v", phase, verdict)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("deferral wrote cache: %v", err)
		}
	}
	if fixture.owns != 2 {
		t.Fatalf("same-head deferral avoided retry: %d", fixture.owns)
	}
	fixture.ownErr, fixture.err = nil, nil
	if verdict := evaluateStopReviewThreads(t.Context(), root); verdict.Outcome != Deny {
		t.Fatalf("unpaced open thread passed: %+v", verdict)
	}
}

// TestStopReviewPartialRateLimit preserves known denials, defers unread reviews, and never caches either verdict.
func TestStopReviewPartialRateLimit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		resolved bool
		clean    bool
		outcome  Outcome
	}{
		{"unresolved", false, false, Deny},
		{"resolved", true, false, Allow},
		{"clean", false, true, Allow},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := &rateLimitedStopReviews{stopReviewFixture: reviewFixture()}
			fixture.numbers = []int{7, 8}
			fixture.snapshot.Threads[0].Resolved = tc.resolved
			if tc.clean {
				fixture.snapshot.Threads = nil
			}
			fixture.batchSnapshots = map[int]forge.ReviewSnapshot{7: fixture.snapshot}
			fixture.err = fmt.Errorf("partial read: %w", &forge.ErrRateLimited{
				Bucket: "graphql", Remaining: "0", Limit: "100", Reset: "22:45", Cause: errors.New("rate limited")})
			root, path := installRateLimitedStopReviews(t, fixture)
			for range 2 {
				verdict := evaluateStopReviewThreads(t.Context(), root)
				assertPartialRateLimitedStopVerdict(t, verdict, tc.outcome)
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("partial read wrote cache: %v", err)
				}
			}
			if fixture.owns != 2 {
				t.Fatalf("partial read avoided retry: %d", fixture.owns)
			}
		})
	}
}

// assertPartialRateLimitedStopVerdict requires known findings and the deferral notice without unread denials.
func assertPartialRateLimitedStopVerdict(t *testing.T, verdict Verdict, outcome Outcome) {
	t.Helper()
	if verdict.Outcome != outcome || verdict.Notice != "review inventory deferred: graphql 0 of 100, reset 22:45" {
		t.Fatalf("partial read: %+v", verdict)
	}
	if outcome == Deny && (!strings.Contains(verdict.Reason, "example/widget#7 has 1 unresolved") ||
		!strings.Contains(verdict.Reason, "main.go:9 by review-bot")) {
		t.Fatalf("known thread lost: %+v", verdict)
	}
	if strings.Contains(verdict.Reason, "not read") || (outcome == Allow && verdict.Reason != "") {
		t.Fatalf("unread review not deferred: %+v", verdict)
	}
}

// TestStopReviewJoinedRateLimits preserves ordinary failures and known denials beside deferred reads.
func TestStopReviewJoinedRateLimits(t *testing.T) {
	limited := fmt.Errorf("read example/widget#8: %w", &forge.ErrRateLimited{Cause: errors.New("paced read")})
	ordinary := fmt.Errorf("read example/widget#9: %w", errors.New("permission denied"))
	for _, tc := range []struct {
		name       string
		failure    error
		unresolved bool
		outcome    Outcome
		want       string
	}{
		{"mixed", errors.Join(limited, ordinary), false, Deny, ordinary.Error()},
		{"mixed-reversed", errors.Join(ordinary, limited), false, Deny, ordinary.Error()},
		{"nested-mixed", errors.Join(limited, errors.Join(ordinary)), false, Deny, ordinary.Error()},
		{"wrapped-mixed", fmt.Errorf("batch context: %w", errors.Join(limited, ordinary)), false, Deny, ordinary.Error()},
		{"all-limited", errors.Join(limited, limited), false, Allow, ""},
		{"known-denial", errors.Join(limited, ordinary), true, Deny, "example/widget#7 has 1 unresolved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := &rateLimitedStopReviews{stopReviewFixture: reviewFixture()}
			fixture.numbers = []int{7, 8, 9}
			fixture.snapshot.Threads[0].Resolved = !tc.unresolved
			fixture.batchSnapshots = map[int]forge.ReviewSnapshot{7: fixture.snapshot}
			fixture.err = tc.failure
			verdict := readStopReviews(t.Context(), fixture, "example/widget", time.Now())
			if verdict.Outcome != tc.outcome || !strings.Contains(verdict.Notice, "review inventory deferred: GitHub rate limited: paced read") ||
				!strings.Contains(verdict.Reason, tc.want) {
				t.Fatalf("joined failures lost: %+v", verdict)
			}
			if tc.outcome == Allow && verdict.Reason != "" {
				t.Fatalf("rate-limited batch denied: %+v", verdict)
			}
		})
	}
}

// TestStopReviewErrorWalkBound refuses oversized joined errors instead of hiding unread failures.
func TestStopReviewErrorWalkBound(t *testing.T) {
	failures := make([]error, 4*forge.MaxReviewPullRequests+1)
	for i := range failures {
		failures[i] = &forge.ErrRateLimited{Cause: errors.New("paced read")}
	}
	fixture := &rateLimitedStopReviews{stopReviewFixture: reviewFixture()}
	fixture.batchSnapshots = map[int]forge.ReviewSnapshot{}
	fixture.err = errors.Join(failures...)
	verdict := readStopReviews(t.Context(), fixture, "example/widget", time.Now())
	if verdict.Outcome != Deny || !strings.Contains(verdict.Reason, "review error walk exceeds") {
		t.Fatalf("unclassified failures passed: %+v", verdict)
	}
}

// TestStopReviewRateLimitReset distinguishes a known reset from missing reset metadata.
func TestStopReviewRateLimitReset(t *testing.T) {
	for _, tc := range []struct{ name, reset, want string }{
		{"known", "22:45", "reset 22:45"},
		{"unknown", "", "reset unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := &rateLimitedStopReviews{stopReviewFixture: reviewFixture(), ownErr: &forge.ErrRateLimited{
				Bucket: "graphql", Remaining: "37", Limit: "100", Reset: tc.reset, Cause: errors.New("rate limited")}}
			verdict := readStopReviews(t.Context(), fixture, "example/widget", time.Now())
			if verdict.Outcome != Allow || verdict.Reason != "" ||
				verdict.Notice != "review inventory deferred: graphql 37 of 100, "+tc.want {
				t.Fatalf("reset metadata: %+v", verdict)
			}
		})
	}
}

// TestStopReviewRateLimitWithoutNumbers preserves the GitHub cause at either read boundary.
func TestStopReviewRateLimitWithoutNumbers(t *testing.T) {
	for _, message := range []string{"API rate limit already exceeded", "secondary rate limit"} {
		for _, phase := range []string{"inventory", "batch"} {
			fixture := &rateLimitedStopReviews{stopReviewFixture: reviewFixture()}
			limited := &forge.ErrRateLimited{Cause: errors.New(message)}
			if phase == "inventory" {
				fixture.ownErr = limited
			} else {
				fixture.err = limited
			}
			verdict := readStopReviews(t.Context(), fixture, "example/widget", time.Now())
			if verdict.Outcome != Allow || verdict.Notice != "review inventory deferred: GitHub rate limited: "+message {
				t.Fatalf("%s %s: %+v", phase, message, verdict)
			}
		}
	}
}

// TestStopReviewOtherErrorsDeny excludes ordinary failures and deadline exhaustion from deferral.
func TestStopReviewOtherErrorsDeny(t *testing.T) {
	for _, failure := range []error{errors.New("read refused"), context.DeadlineExceeded,
		errors.New("secondary rate limit mentioned in an untyped reader error")} {
		for _, phase := range []string{"inventory", "batch"} {
			fixture := &rateLimitedStopReviews{stopReviewFixture: reviewFixture()}
			if phase == "inventory" {
				fixture.ownErr = failure
			} else {
				fixture.err = failure
			}
			verdict := readStopReviews(t.Context(), fixture, "example/widget", time.Now())
			if verdict.Outcome != Deny || verdict.Notice != "" || !strings.Contains(verdict.Reason, failure.Error()) {
				t.Fatalf("ordinary %s failure passed: %+v", phase, verdict)
			}
		}
	}
}

// TestStopReviewDeferralNoticeSurvivesMerge preserves the notice beside allow, skip, and deny checks.
func TestStopReviewDeferralNoticeSurvivesMerge(t *testing.T) {
	for _, outcome := range []Outcome{Allow, Skip, Deny} {
		verdict := mergeStopChecks(Verdict{Outcome: outcome, Reason: "other check", Notice: "other notice"},
			Verdict{Outcome: Allow, Notice: "review inventory deferred: graphql 0 of 100, reset 00:00"})
		if verdict.Outcome != outcome || verdict.Notice != "other notice; review inventory deferred: graphql 0 of 100, reset 00:00" {
			t.Fatalf("merge discarded deferral: %+v", verdict)
		}
	}
}

// TestStopReviewDeferralCacheRejected refuses a previously serialized deferral even within its TTL.
func TestStopReviewDeferralCacheRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "praetor-stop-reviews.json")
	now := time.Now()
	cache := stopReviewCache{Repository: "example/widget", Head: "same-head", CheckedAt: now,
		Verdict: Verdict{Outcome: Allow, Notice: "review inventory deferred: graphql 0 of 100, reset 00:00"}}
	writeStopReviewCache(t, path, cache)
	if _, hit := loadStopReviewCache(path, cache.Repository, cache.Head, now); hit {
		t.Fatal("deferral cache suppressed a fresh inventory read")
	}
}
