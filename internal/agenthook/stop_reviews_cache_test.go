package agenthook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// TestStopReviewCacheKeyAndTTL pins cache key matching, expiration TTL, and outcome invalidation rules.
func TestStopReviewCacheKeyAndTTL(t *testing.T) {
	root := reviewRepositoryFixture(t)
	fixture := reviewFixture()
	fixture.snapshot.Threads[0].Resolved = true
	installReviewFixture(t, fixture)
	if verdict := evaluateStopReviewThreads(t.Context(), root); verdict.Outcome != Allow {
		t.Fatal(verdict)
	}
	repo, head, path, err := stopReviewIdentity(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	data, err := contextopt.ReadSnapshot(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	var cache stopReviewCache
	if err := json.Unmarshal(data, &cache); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		repo, head string
		when       time.Time
		outcome    Outcome
		hit        bool
	}{
		{repo, head, cache.CheckedAt.Add(stopReviewCacheTTL - time.Nanosecond), Allow, true},
		{repo, head, cache.CheckedAt.Add(stopReviewCacheTTL - time.Nanosecond), Skip, false},
		{repo, head, cache.CheckedAt.Add(stopReviewCacheTTL - time.Nanosecond), Deny, false},
		{repo, head, cache.CheckedAt.Add(stopReviewCacheTTL), Allow, false},
		{repo, "different-head", cache.CheckedAt, Allow, false},
		{"different/repository", head, cache.CheckedAt, Allow, false},
		{repo, head, cache.CheckedAt.Add(-time.Nanosecond), Allow, false},
	} {
		cache.Verdict.Outcome = scenario.outcome
		writeStopReviewCache(t, path, cache)
		if _, hit := loadStopReviewCache(t.Context(), path, scenario.repo, scenario.head, scenario.when); hit != scenario.hit {
			t.Fatalf("cache scenario=%+v hit=%t", scenario, hit)
		}
	}
	cache.Verdict.Outcome = Allow
	cache.CheckedAt = cache.CheckedAt.Add(-stopReviewCacheTTL)
	writeStopReviewCache(t, path, cache)
	evaluateStopReviewThreads(t.Context(), root)
	if fixture.reads.Load() != 2 {
		t.Fatalf("expired cache avoided fresh read: %d", fixture.reads.Load())
	}
}

// writeStopReviewCache serializes and atomically writes a test stopReviewCache entry to disk.
func writeStopReviewCache(t *testing.T, path string, cache stopReviewCache) {
	t.Helper()
	cache.Complete = true
	data, err := json.Marshal(cache)
	if err != nil {
		t.Fatal(err)
	}
	if err := contextopt.WriteSnapshot(t.Context(), path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestStopReviewCacheWriteFailureKeepsDeny pins that a cache write failure does not suppress an active deny verdict.
func TestStopReviewCacheWriteFailureKeepsDeny(t *testing.T) {
	root := reviewRepositoryFixture(t)
	installReviewFixture(t, reviewFixture())
	_, _, path, err := stopReviewIdentity(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	verdict := evaluateStopReviewThreads(t.Context(), root)
	if verdict.Outcome != Deny || !strings.Contains(verdict.Reason, "write review cache") {
		t.Fatalf("cache failure hid known finding: %+v", verdict)
	}
}

// TestStopReviewMalformedCacheForcesRead pins that corrupted cache files are discarded in favor of a fresh read.
func TestStopReviewMalformedCacheForcesRead(t *testing.T) {
	root := reviewRepositoryFixture(t)
	fixture := reviewFixture()
	installReviewFixture(t, fixture)
	_, _, path, err := stopReviewIdentity(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := contextopt.WriteSnapshot(t.Context(), path, []byte(`{"Outcome":0}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if verdict := evaluateStopReviewThreads(t.Context(), root); verdict.Outcome != Deny || fixture.reads.Load() != 1 {
		t.Fatalf("malformed cache silently allowed: %+v reads=%d", verdict, fixture.reads.Load())
	}
	if filepath.Base(path) != "praetor-stop-reviews.json" {
		t.Fatalf("unexpected cache: %s", path)
	}
}
