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

// The factories are replaced by offline tests; runtime uses only the shared forge reader and git identity.
var stopReviewReader = func(root string) forge.ReviewReader { return forge.GHReviewReader{Dir: root} }
var stopReviewIdentity = resolveStopReviewIdentity

type stopReviewCache struct {
	Repository string
	Head       string
	CheckedAt  time.Time
	Verdict    Verdict
}

// evaluateStopReviewThreads queries own open pull requests for unresolved review threads and caches the outcome.
func evaluateStopReviewThreads(ctx context.Context, root string) Verdict {
	ctx, cancel := context.WithTimeout(ctx, stopReviewBudget)
	defer cancel()
	repo, head, cachePath, err := stopReviewIdentity(ctx, root)
	if err != nil {
		if ctx.Err() != nil {
			return Verdict{Outcome: Deny, Reason: "Own pull request review state not read within the stop budget: " + err.Error()}
		}
		return reviewSkip(err)
	}
	now := time.Now()
	if verdict, hit := loadStopReviewCache(cachePath, repo, head, now); hit {
		return verdict
	}
	verdict := readStopReviews(ctx, stopReviewReader(root), repo, now)
	if verdict.Outcome == Skip {
		return verdict // Incomplete reads must retry even when HEAD has not moved.
	}
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

// resolveStopReviewIdentity resolves the repository, current HEAD commit, and cache file path for review evaluation.
func resolveStopReviewIdentity(ctx context.Context, root string) (string, string, string, error) {
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

// loadStopReviewCache retrieves a cached review verdict if it matches the current repository and commit within TTL.
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
	if cache.Verdict.Outcome != Allow {
		valid = false
	}
	return cache.Verdict, valid
}

// readStopReviews reads pull request review threads from the forge reader and constructs appropriate denial verdicts.
func readStopReviews(ctx context.Context, reader forge.ReviewReader, repo string, now time.Time) Verdict {
	numbers, err := reader.OwnOpen(ctx, repo)
	if err != nil {
		return Verdict{Outcome: Deny, Reason: fmt.Sprintf("Own pull request review inventory not read in %s: %v", repo, err)}
	}
	if len(numbers) > forge.MaxReviewPullRequests {
		return Verdict{Outcome: Deny, Reason: fmt.Sprintf("Own pull request review inventory in %s exceeds 100", repo)}
	}
	snapshots, batchErr := reader.ReadBatch(ctx, repo, numbers)
	denies := stopReviewBatchDenies(repo, numbers, snapshots, now)
	if batchErr != nil {
		denies = append(denies, Verdict{Outcome: Deny, Reason: "Own pull request review batch failed: " + batchErr.Error()})
	}
	return joinReviewDenies(denies)
}

// stopReviewBatchDenies retains known findings and names every missing or invalid snapshot in inventory order.
func stopReviewBatchDenies(repo string, numbers []int, snapshots map[int]forge.ReviewSnapshot, now time.Time) []Verdict {
	var denies []Verdict
	var unread []string
	for _, number := range numbers {
		snapshot, ok := snapshots[number]
		if !ok || snapshot.Number != number || !forge.ValidReviewPRState(snapshot.State) {
			unread = append(unread, fmt.Sprintf("#%d", number))
			continue
		}
		if snapshot.State == "OPEN" {
			if verdict := stopReviewVerdict(repo, snapshot, now); verdict.Outcome == Deny {
				denies = append(denies, verdict)
			}
		}
	}
	if len(unread) > 0 {
		denies = append(denies, Verdict{Outcome: Deny, Reason: "Own pull request review state not read for " +
			strings.Join(unread, ", ") + " within the stop budget"})
	}
	return denies
}

// stopReviewVerdict inspects a single pull request snapshot and returns a denial verdict if unresolved threads exist.
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

// joinReviewDenies merges multiple review denial verdicts into a single compound denial verdict.
func joinReviewDenies(denies []Verdict) Verdict {
	if len(denies) > 0 {
		return joinStopDenies(denies)
	}
	return Verdict{Outcome: Allow}
}

// reviewSkip returns a skip verdict indicating the review check was skipped due to an error.
func reviewSkip(err error) Verdict {
	return Verdict{Outcome: Skip, Reason: "Own pull request review check skipped: " + err.Error()}
}

// reviewCacheNotice appends a cache write or read error notice to an existing verdict or returns a skip verdict.
func reviewCacheNotice(verdict Verdict, err error) Verdict {
	notice := reviewSkip(err)
	if verdict.Outcome == Deny {
		verdict.Reason += " " + notice.Reason
		return verdict
	}
	return notice
}
