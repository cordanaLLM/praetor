package agenthook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

func TestStopReviewCacheKeyAndTTL(t *testing.T) {
	root := reviewRepositoryFixture(t)
	fixture := reviewFixture()
	installReviewFixture(t, fixture)
	if verdict := evaluateStopReviewThreads(t.Context(), root); verdict.Outcome != Deny {
		t.Fatal(verdict)
	}
	repo, head, path, err := stopReviewIdentity(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	data, err := util.ReadFileLimited(path, 65536)
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
		hit        bool
	}{
		{repo, head, cache.CheckedAt.Add(stopReviewCacheTTL - time.Nanosecond), true},
		{repo, head, cache.CheckedAt.Add(stopReviewCacheTTL), false},
		{repo, "different-head", cache.CheckedAt, false},
		{"different/repository", head, cache.CheckedAt, false},
		{repo, head, cache.CheckedAt.Add(-time.Nanosecond), false},
	} {
		if _, hit := loadStopReviewCache(path, scenario.repo, scenario.head, scenario.when); hit != scenario.hit {
			t.Fatalf("cache scenario=%+v hit=%t", scenario, hit)
		}
	}
	cache.CheckedAt = cache.CheckedAt.Add(-stopReviewCacheTTL)
	expired, err := json.Marshal(cache)
	if err != nil {
		t.Fatal(err)
	}
	if err := util.WriteFileAtomic(path, expired, 0o600); err != nil {
		t.Fatal(err)
	}
	evaluateStopReviewThreads(t.Context(), root)
	if fixture.reads != 2 {
		t.Fatalf("expired cache avoided fresh read: %d", fixture.reads)
	}
}

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

func TestStopReviewMalformedCacheForcesRead(t *testing.T) {
	root := reviewRepositoryFixture(t)
	fixture := reviewFixture()
	installReviewFixture(t, fixture)
	_, _, path, err := stopReviewIdentity(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"Outcome":0}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if verdict := evaluateStopReviewThreads(t.Context(), root); verdict.Outcome != Deny || fixture.reads != 1 {
		t.Fatalf("malformed cache silently allowed: %+v reads=%d", verdict, fixture.reads)
	}
	if filepath.Base(path) != "praetor-stop-reviews.json" {
		t.Fatalf("unexpected cache: %s", path)
	}
}
