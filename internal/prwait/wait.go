// Package prwait waits for review work or terminal checks using a bounded forge reader.
package prwait

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
)

const maxPolls = 1000000

// Options controls one repository's wait. Numbers omitted selects own open PRs.
type Options struct {
	Repository string
	Numbers    []int
	Timeout    time.Duration
	Interval   time.Duration
}

// Result carries the CLI exit contract and one text item per finding or PR.
type Result struct {
	Code  int      `json:"exit_code"`
	Items []string `json:"items"`
}

// Wait establishes a baseline only after reporting standing unresolved threads.
// All reads and timers share the overall timeout; each forge call has its own cap.
func Wait(ctx context.Context, reader forge.ReviewReader, options Options) Result {
	if err := options.Validate(); err != nil {
		return failure(options.Repository, err)
	}
	ctx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	numbers, err := selectNumbers(ctx, reader, options)
	if err != nil {
		return readFailure(ctx, options.Repository, err)
	}
	baseline := make(map[string]bool)
	limit := int(options.Timeout/options.Interval) + 2
	for poll := 0; poll < limit; poll++ {
		if err := ctx.Err(); err != nil {
			return Result{Code: 124, Items: []string{"timeout " + options.Repository + ": " + err.Error()}}
		}
		snapshots, err := readAll(ctx, reader, options.Repository, numbers)
		if err != nil {
			return readFailure(ctx, options.Repository, err)
		}
		result := classify(options.Repository, snapshots, baseline, poll == 0, time.Now())
		if result.Code != -1 {
			return result
		}
		if err := waitInterval(ctx, options.Interval); err != nil {
			return Result{Code: 124, Items: []string{"timeout " + options.Repository + ": " + err.Error()}}
		}
	}
	return Result{Code: 124, Items: []string{"timeout " + options.Repository + ": poll bound reached"}}
}

// Validate rejects invalid inputs before any forge command runs.
func (o Options) Validate() error {
	if o.Repository == "" || o.Timeout <= 0 || o.Interval <= 0 {
		return errors.New("pr wait requires --repo owner/name and positive --timeout and --interval")
	}
	if _, _, err := forge.ParseReviewRepository(o.Repository); err != nil {
		return fmt.Errorf("pr wait --repo: %w", err)
	}
	if o.Timeout/o.Interval > maxPolls-2 || len(o.Numbers) > forge.MaxReviewPullRequests {
		return errors.New("pr wait exceeds poll or pull request bound")
	}
	for _, number := range o.Numbers {
		if number < 1 {
			return errors.New("pr wait --pr must be positive")
		}
	}
	return nil
}

// selectNumbers returns explicit pull request numbers or discovers own open pull requests.
func selectNumbers(ctx context.Context, reader forge.ReviewReader, options Options) ([]int, error) {
	if len(options.Numbers) > 0 {
		return options.Numbers, nil
	}
	numbers, err := reader.OwnOpen(ctx, options.Repository)
	if err != nil {
		return nil, fmt.Errorf("discover own pull requests: %w", err)
	}
	if len(numbers) > forge.MaxReviewPullRequests {
		return nil, errors.New("own pull request inventory exceeds 100")
	}
	return numbers, nil
}

// readAll fetches review snapshots for the given pull request numbers with a per-request timeout.
func readAll(ctx context.Context, reader forge.ReviewReader, repo string, numbers []int) ([]forge.ReviewSnapshot, error) {
	snapshots := make([]forge.ReviewSnapshot, 0, len(numbers))
	for _, number := range numbers {
		readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		snapshot, err := reader.Read(readCtx, repo, number)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("read %s#%d: %w", repo, number, err)
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

// failure constructs an error Result with exit code 2 and formatted error message.
func failure(repo string, err error) Result {
	return Result{Code: 2, Items: []string{"read " + repo + ": " + err.Error()}}
}

// readFailure constructs a Result for a failed read, distinguishing context timeout from other errors.
func readFailure(ctx context.Context, repo string, err error) Result {
	if ctx.Err() != nil {
		return Result{Code: 124, Items: []string{"timeout " + repo + ": " + ctx.Err().Error()}}
	}
	return failure(repo, err)
}

// waitInterval pauses for the specified duration or until context cancellation.
func waitInterval(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("wait interval: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}
