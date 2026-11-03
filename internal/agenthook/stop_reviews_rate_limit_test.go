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
	ownErr error
	owns   int
}

// OwnOpen counts inventory attempts and optionally plants a discovery failure.
func (f *rateLimitedStopReviews) OwnOpen(ctx context.Context, repo string) ([]int, error) {
	f.owns++
	if f.ownErr != nil {
		return nil, f.ownErr
	}
	return f.stopReviewFixture.OwnOpen(ctx, repo)
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
		if verdict.Outcome != Allow || verdict.Notice != "review inventory deferred: graphql 37 of 100, reset 22:45" {
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
