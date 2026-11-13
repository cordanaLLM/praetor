package agenthook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
)

// stopReviewBudget keeps identity and inventory discovery within 4 s (#1894).
const stopReviewBudget = 4 * time.Second

// stopReviewPerPRBudget allows batch pagination another 100 ms per own PR (#1894).
const stopReviewPerPRBudget = 100 * time.Millisecond

// stopReviewBatchBudgetCap caps the batch at 12 s, the whole review check at 16 s (#1894).
const stopReviewBatchBudgetCap = 12 * time.Second

// stopReviewCacheStalenessCap permits completed-read fallback only younger than 30 min (#1894).
const stopReviewCacheStalenessCap = 30 * time.Minute

const stopReviewCacheTTL = 2 * time.Minute

// maxStopReviewReadErrors allows bounded wrappers around the entire review inventory.
const maxStopReviewReadErrors = 4 * forge.MaxReviewPullRequests

// The factories are replaced by offline tests; runtime uses only the shared forge reader and git identity.
var stopReviewReader = func(root string) forge.ReviewReader { return forge.GHReviewReader{Dir: root} }
var stopReviewIdentity = resolveStopReviewIdentity

// stopReviewNow permits deterministic cache-age boundary tests for #1894.
var stopReviewNow = time.Now

type stopReviewCache struct {
	Complete   bool
	Repository string
	Head       string
	CheckedAt  time.Time // Observation start; slow completions cannot refresh older state.
	Verdict    Verdict
}

type stopReviewReadError struct {
	err, diagnostic error
}

type stopReviewResult struct {
	verdict  Verdict
	known    Verdict
	complete bool
	failure  error
}

// evaluateStopReviewThreads scales live reads and falls back only to recent completed reads (#1894).
func evaluateStopReviewThreads(ctx context.Context, root string) Verdict {
	ctx, cancel := context.WithTimeout(ctx, stopReviewBudget+stopReviewBatchBudgetCap)
	defer cancel()
	inventoryCtx, inventoryCancel := context.WithTimeout(ctx, stopReviewBudget)
	defer inventoryCancel()
	repo, head, cachePath, err := stopReviewIdentity(inventoryCtx, root)
	if err != nil {
		if inventoryCtx.Err() != nil {
			return Verdict{Outcome: Deny, Reason: "Own pull request review state not read within the stop budget: " + err.Error()}
		}
		return reviewSkip(err)
	}
	now := stopReviewNow()
	if verdict, hit := loadStopReviewCache(ctx, cachePath, repo, head, now); hit {
		return verdict
	}
	result := readStopReviewsResult(ctx, inventoryCtx, stopReviewReader(root), repo, now)
	verdict := result.verdict
	if !result.complete {
		if result.failure != nil && verdict.Notice == "" {
			fallback := stopReviewFallback(ctx, cachePath, repo, head, stopReviewNow(), verdict, result.failure)
			if fallback.Notice == "" {
				return verdict
			}
			return mergeStopChecks(result.known, fallback)
		}
		return verdict // Incomplete reads and rate-limit deferrals must retry.
	}
	cache := stopReviewCache{Complete: true, Repository: repo, Head: head, CheckedAt: now, Verdict: verdict}
	if err := cache.publish(ctx, cachePath); err != nil {
		return reviewCacheNotice(verdict, fmt.Errorf("write review cache: %w", err))
	}
	return verdict
}

// publish retains newer observations and compares bytes through the shared snapshot lock.
func (cache stopReviewCache) publish(ctx context.Context, path string) error {
	before, exists, err := contextopt.ObserveSnapshot(ctx, path)
	if err != nil {
		return err
	}
	var previous stopReviewCache
	if exists && json.Unmarshal(before, &previous) == nil && previous.Repository == cache.Repository && !previous.CheckedAt.Before(cache.CheckedAt) {
		return nil
	}
	data, err := json.Marshal(cache)
	if err != nil {
		return fmt.Errorf("encode review cache: %w", err)
	}
	return contextopt.ReplaceSnapshot(ctx, path, data, contextopt.ReplaceOptions{Expected: before, Exists: exists, Mode: 0o600})
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

// loadStopReviewCache retains the same-HEAD, 2 min Allow fast path independently of fallback (#1894).
func loadStopReviewCache(ctx context.Context, path, repo, head string, now time.Time) (Verdict, bool) {
	cache, valid := readStopReviewCache(ctx, path, repo, now, stopReviewCacheTTL)
	return cache.Verdict, valid && cache.Head == head && cache.Verdict.Outcome == Allow
}

// readStopReviewCache validates completed verdicts and a strict age bound for either cache path (#1894).
func readStopReviewCache(ctx context.Context, path, repo string, now time.Time, maxAge time.Duration) (stopReviewCache, bool) {
	data, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		return stopReviewCache{}, false
	}
	var cache stopReviewCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return stopReviewCache{}, false
	}
	age := now.Sub(cache.CheckedAt)
	complete := cache.Complete && (cache.Verdict.Outcome == Allow || cache.Verdict.Outcome == Deny) && cache.Verdict.Notice == ""
	return cache, complete && cache.Repository == repo && age >= 0 && age < maxAge
}

// stopReviewFallback preserves recent completed verdicts, including named denials, after a live failure (#1894).
func stopReviewFallback(ctx context.Context, path, repo, head string, now time.Time, live Verdict, failure error) Verdict {
	cache, valid := readStopReviewCache(ctx, path, repo, now, stopReviewCacheStalenessCap)
	if !valid || (cache.Verdict.Outcome == Allow && cache.Head != head) {
		return live
	}
	verdict := cache.Verdict
	verdict.Notice = fmt.Sprintf("Own pull request review live read overran or failed; using newest completed read for %s, %s old; live failure: %s",
		repo, forge.ReviewAge(now, cache.CheckedAt), failure)
	return verdict
}

// stopReviewBatchBudget scales the batch independently of discovery, saturating before multiplication (#1894).
func stopReviewBatchBudget(count int) time.Duration {
	count = max(0, min(count, int((stopReviewBatchBudgetCap-stopReviewBudget)/stopReviewPerPRBudget)))
	return stopReviewBudget + time.Duration(count)*stopReviewPerPRBudget
}

// readStopReviews retains known denials and rate-limit deferrals under separate discovery and batch budgets (#1894).
func readStopReviews(ctx context.Context, reader forge.ReviewReader, repo string, now time.Time) Verdict {
	inventoryCtx, cancel := context.WithTimeout(ctx, stopReviewBudget)
	defer cancel()
	return readStopReviewsResult(ctx, inventoryCtx, reader, repo, now).verdict
}

// readStopReviewsResult marks incomplete reads explicitly so they never replace a completed cache (#1894).
func readStopReviewsResult(ctx, inventoryCtx context.Context, reader forge.ReviewReader, repo string, now time.Time) stopReviewResult {
	numbers, err := reader.OwnOpen(inventoryCtx, repo)
	if err != nil {
		return stopReviewResult{failure: err, verdict: stopReviewReadFailure(err, fmt.Sprintf("Own pull request review inventory not read in %s: ", repo))}
	}
	if len(numbers) > forge.MaxReviewPullRequests {
		return stopReviewResult{verdict: Verdict{Outcome: Deny, Reason: fmt.Sprintf("Own pull request review inventory in %s exceeds 100", repo)}}
	}
	batchCtx, cancel := context.WithTimeout(ctx, stopReviewBatchBudget(len(numbers)))
	defer cancel()
	snapshots, batchErr := reader.ReadBatch(batchCtx, repo, numbers)
	batchErr = errors.Join(batchErr, batchCtx.Err())
	failure := stopReviewBatchFailure(batchErr)
	denies, complete, known := stopReviewBatchFindings(repo, numbers, snapshots, now, failure.Notice != "")
	if !complete && batchErr == nil {
		batchErr = errors.New("review batch incomplete")
	}
	return stopReviewResult{verdict: mergeStopChecks(joinReviewDenies(denies), failure),
		complete: complete && batchErr == nil, failure: batchErr, known: known}
}

// stopReviewBatchFailure walks joined and wrapped failures with a bound, preserving each read's diagnostic.
// Ordinary failures deny independently of rate-limit notices; an exhausted walk also denies.
func stopReviewBatchFailure(err error) Verdict {
	const prefix = "Own pull request review batch failed: "
	failure := Verdict{Outcome: Allow}
	overflow := Verdict{Outcome: Deny, Reason: prefix + fmt.Sprintf("review error walk exceeds %d nodes", maxStopReviewReadErrors)}
	pending := []stopReviewReadError{{err, err}}
	for checked := 0; checked < maxStopReviewReadErrors && len(pending) > 0; checked++ {
		next := pending[0]
		pending = pending[1:]
		if next.err == nil {
			continue
		}
		if joined, ok := next.err.(interface{ Unwrap() []error }); ok {
			children := joined.Unwrap()
			if len(children) > maxStopReviewReadErrors-len(pending) {
				return mergeStopChecks(failure, overflow)
			}
			for _, child := range children {
				pending = append(pending, stopReviewReadError{child, child})
			}
			continue
		}
		if child := errors.Unwrap(next.err); child != nil {
			pending = append(pending, stopReviewReadError{child, next.diagnostic})
			continue
		}
		failure = mergeStopChecks(failure, stopReviewReadFailure(next.diagnostic, prefix))
	}
	if len(pending) > 0 {
		return mergeStopChecks(failure, overflow)
	}
	return failure
}

// stopReviewReadFailure denies ordinary errors and reports rate-limit metadata with an explicit unknown reset.
func stopReviewReadFailure(err error, prefix string) Verdict {
	var limited *forge.ErrRateLimited
	if !errors.As(err, &limited) {
		return Verdict{Outcome: Deny, Reason: prefix + err.Error()}
	}
	notice := "review inventory deferred: GitHub rate limited: " + limited.Error()
	if limited.Bucket != "" && limited.Remaining != "" && limited.Limit != "" {
		reset := limited.Reset
		if reset == "" {
			reset = "unknown"
		}
		notice = fmt.Sprintf("review inventory deferred: %s %s of %s, reset %s",
			limited.Bucket, limited.Remaining, limited.Limit, reset)
	}
	return Verdict{Outcome: Allow, Notice: notice}
}

// stopReviewBatchDenies retains known findings while the shared scanner tracks completeness (#1894).
func stopReviewBatchDenies(repo string, numbers []int, snapshots map[int]forge.ReviewSnapshot, now time.Time, deferUnread bool) []Verdict {
	denies, _, _ := stopReviewBatchFindings(repo, numbers, snapshots, now, deferUnread)
	return denies
}

// stopReviewBatchFindings reports known threads and unread snapshots without caching partial results (#1894).
func stopReviewBatchFindings(repo string, numbers []int, snapshots map[int]forge.ReviewSnapshot, now time.Time, deferUnread bool) ([]Verdict, bool, Verdict) {
	var denies []Verdict
	var unread []string
	complete := true
	for _, number := range numbers {
		snapshot, ok := snapshots[number]
		if !ok && deferUnread {
			complete = false
			continue
		}
		if !ok || snapshot.Number != number || !forge.ValidReviewPRState(snapshot.State) {
			complete = false
			unread = append(unread, fmt.Sprintf("#%d", number))
			continue
		}
		if snapshot.State == "OPEN" {
			if verdict := stopReviewVerdict(repo, snapshot, now); verdict.Outcome == Deny {
				denies = append(denies, verdict)
			}
		}
	}
	known := joinReviewDenies(denies)
	if len(unread) > 0 {
		denies = append(denies, Verdict{Outcome: Deny, Reason: "Own pull request review state not read for " +
			strings.Join(unread, ", ") + " within the stop budget"})
	}
	return denies, complete, known
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
