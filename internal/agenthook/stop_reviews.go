package agenthook

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
)

const stopReviewBudget = 4 * time.Second
const stopReviewCacheTTL = 2 * time.Minute

// The factory is replaced by offline tests; runtime uses only the shared forge reader.
var stopReviewReader = func(root string) forge.ReviewReader { return forge.GHReviewReader{Dir: root} }

type stopReviewCache struct {
	Repository string
	Head       string
	CheckedAt  time.Time
	Verdict    Verdict
}

func evaluateStopReviewThreads(ctx context.Context, root string) Verdict {
	ctx, cancel := context.WithTimeout(ctx, stopReviewBudget)
	defer cancel()
	repo, head, cachePath, err := stopReviewIdentity(ctx, root)
	if err != nil {
		return reviewSkip(err)
	}
	now := time.Now()
	if verdict, hit := loadStopReviewCache(cachePath, repo, head, now); hit {
		return verdict
	}
	verdict := readStopReviews(ctx, stopReviewReader(root), repo, now)
	cache := stopReviewCache{Repository: repo, Head: head, CheckedAt: now, Verdict: verdict}
	data, err := json.Marshal(cache)
	if err != nil {
		return reviewCacheNotice(verdict, fmt.Errorf("encode review cache: %w", err))
	}
	if err := util.WriteFileAtomic(cachePath, data, 0o600); err != nil {
		return reviewCacheNotice(verdict, fmt.Errorf("write review cache: %w", err))
	}
	return verdict
}

func stopReviewIdentity(ctx context.Context, root string) (string, string, string, error) {
	remote, err := util.ReadOriginRemote(ctx, root)
	if err != nil {
		return "", "", "", fmt.Errorf("resolve review repository: %w", err)
	}
	if !strings.EqualFold(remote.Host, "github.com") {
		return "", "", "", fmt.Errorf("review reader supports github.com; origin host is %s", remote.Host)
	}
	head, err := util.RunGit(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return "", "", "", fmt.Errorf("resolve review cache head: %w", err)
	}
	gitDir, err := util.RunGit(ctx, root, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", "", "", fmt.Errorf("resolve review cache git directory: %w", err)
	}
	return remote.Owner + "/" + remote.Repo, strings.TrimSpace(head),
		filepath.Join(strings.TrimSpace(gitDir), "praetor-stop-reviews.json"), nil
}

func loadStopReviewCache(path, repo, head string, now time.Time) (Verdict, bool) {
	data, err := util.ReadFileLimited(path, 65536)
	if err != nil {
		return Verdict{}, false // Missing, malformed and stale caches always force a read.
	}
	var cache stopReviewCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return Verdict{}, false
	}
	age := now.Sub(cache.CheckedAt)
	valid := cache.Repository == repo && cache.Head == head && age >= 0 && age < stopReviewCacheTTL
	if cache.Verdict.Outcome != Allow && cache.Verdict.Outcome != Skip {
		valid = false
	}
	return cache.Verdict, valid
}

func readStopReviews(ctx context.Context, reader forge.ReviewReader, repo string, now time.Time) Verdict {
	numbers, err := reader.OwnOpen(ctx, repo)
	if err != nil {
		return reviewSkip(fmt.Errorf("list own pull requests in %s: %w", repo, err))
	}
	if len(numbers) > forge.MaxReviewPullRequests {
		return reviewSkip(fmt.Errorf("own pull requests in %s exceed 100", repo))
	}
	var denies []Verdict
	for _, number := range numbers {
		snapshot, err := reader.Read(ctx, repo, number)
		if err != nil {
			return reviewCacheNotice(joinReviewDenies(denies), fmt.Errorf("read %s#%d: %w", repo, number, err))
		}
		if snapshot.State == "OPEN" {
			if verdict := stopReviewVerdict(repo, snapshot, now); verdict.Outcome == Deny {
				denies = append(denies, verdict)
			}
		}
	}
	return joinReviewDenies(denies)
}

func stopReviewVerdict(repo string, snapshot forge.ReviewSnapshot, now time.Time) Verdict {
	var threads []string
	oldest := now
	for _, thread := range snapshot.Threads {
		if !thread.Resolved {
			threads = append(threads, fmt.Sprintf("%s:%d by %s", thread.Path, thread.Line, thread.Author))
			if thread.CreatedAt.Before(oldest) {
				oldest = thread.CreatedAt
			}
		}
	}
	if len(threads) == 0 {
		return Verdict{Outcome: Allow}
	}
	return Verdict{Outcome: Deny, Reason: fmt.Sprintf("Own pull request %s#%d has %d unresolved review thread(s), oldest %s (%s): "+
		"take them as your next unit now: fix through a lane, reply with the evidence, resolve; no controller order is needed.",
		repo, snapshot.Number, len(threads), forge.ReviewAge(now, oldest), strings.Join(threads, ", "))}
}

func joinReviewDenies(denies []Verdict) Verdict {
	if len(denies) > 0 {
		return joinStopDenies(denies)
	}
	return Verdict{Outcome: Allow}
}

func reviewSkip(err error) Verdict {
	return Verdict{Outcome: Skip, Reason: "Own pull request review check skipped: " + err.Error()}
}

func reviewCacheNotice(verdict Verdict, err error) Verdict {
	notice := reviewSkip(err)
	if verdict.Outcome == Deny {
		verdict.Reason += " " + notice.Reason
		return verdict
	}
	return notice
}
