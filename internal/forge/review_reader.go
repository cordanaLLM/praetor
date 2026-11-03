package forge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// MaxReviewPullRequests bounds discovery, explicit selections and per-stop reads.
const MaxReviewPullRequests = 100

// ReviewThread retains the first comment's author and creation time, including bots.
type ReviewThread struct {
	ID, Path, Author string
	Line             int
	Resolved         bool
	CreatedAt        time.Time
}

// PullRequestReview represents any review; no reviewer allow-list is applied.
type PullRequestReview struct {
	ID, Author, State string
	SubmittedAt       time.Time
}

// ReviewSnapshot retains validated review state. Read fills reviews and checks;
// ReadBatch fills complete thread state only, for stop-hook evaluation.
type ReviewSnapshot struct {
	Number           int
	State            string
	HeadRefOID       string
	Threads          []ReviewThread
	Reviews          []PullRequestReview
	ChecksTerminal   bool
	ChecksConclusion string
}

// ReviewReader is shared by the background wait and stop evaluator.
// ReadBatch returns complete thread snapshots by number; absent entries are unread.
type ReviewReader interface {
	OwnOpen(context.Context, string) ([]int, error)
	ReadBatch(context.Context, string, []int) (map[int]ReviewSnapshot, error)
	Read(context.Context, string, int) (ReviewSnapshot, error)
}

// GHReviewReader uses the installed gh identity through the existing bounded exec
// boundary. It never obtains tokens or creates another HTTP client. Run is injectable
// for offline fixtures; nil selects util.RunCommandBytes.
type GHReviewReader struct {
	Dir string
	Run func(context.Context, string, ...string) ([]byte, error)
}

// call executes a bounded gh read with pacing sleeps disabled and classifies rate limits.
func (r GHReviewReader) call(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ctx, err := util.WithCommandEnvironment(ctx, util.InheritedEnvironment([]string{"PRAETOR_GH_NOWAIT=1"}))
	if err != nil {
		return nil, fmt.Errorf("gh review environment: %w", err)
	}
	var result util.CommandBytes
	if r.Run != nil {
		result.Stdout, err = r.Run(ctx, r.Dir, args...)
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			result.Stderr = exit.Stderr
		}
	} else {
		result, err = util.RunCommandBytes(ctx, r.Dir, "gh", util.MaxCommandOutputBytes, args...)
	}
	if err != nil {
		return nil, classifyGHError(fmt.Errorf("gh review read: %w: %s", err, util.BodyPreview(result.Stderr)), result.Stderr)
	}
	return result.Stdout, nil
}

// OwnOpen selects the authenticated gh user's open PRs. The extra item detects
// overflow instead of treating a truncated inventory as complete.
func (r GHReviewReader) OwnOpen(ctx context.Context, repo string) ([]int, error) {
	if _, _, err := ParseReviewRepository(repo); err != nil {
		return nil, err
	}
	data, err := r.call(ctx, "pr", "list", "--repo", repo, "--author", "@me", "--state", "open",
		"--limit", strconv.Itoa(MaxReviewPullRequests+1), "--json", "number")
	if err != nil {
		return nil, fmt.Errorf("list own pull requests in %s: %w", repo, err)
	}
	var rows []struct{ Number int }
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, fmt.Errorf("decode own pull requests: %w", err)
	}
	if len(rows) > MaxReviewPullRequests || rows == nil {
		return nil, errors.New("own pull request inventory missing or exceeds 100")
	}
	numbers := make([]int, 0, len(rows))
	for _, row := range rows {
		if row.Number < 1 {
			return nil, errors.New("own pull request inventory contains invalid number")
		}
		numbers = append(numbers, row.Number)
	}
	return numbers, nil
}

// ParseReviewRepository validates one shared owner/name contract before any read.
func ParseReviewRepository(repo string) (string, string, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return "", "", errors.New("review repository must be owner/name")
	}
	if err := util.ValidateGitHubRepositoryIdentity(owner, name); err != nil {
		return "", "", fmt.Errorf("review repository: %w", err)
	}
	return owner, name, nil
}

// Read requests complete connections within ten pages of 100 items each. Thread
// creation time is the first comment's createdAt: the thread has no such field.
func (r GHReviewReader) Read(ctx context.Context, repo string, number int) (ReviewSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	owner, name, err := ParseReviewRepository(repo)
	if err != nil {
		return ReviewSnapshot{}, err
	}
	if number < 1 {
		return ReviewSnapshot{}, errors.New("review pull request number must be positive")
	}
	request := reviewReadRequest{owner: owner, name: name, number: number}
	pull, err := r.reviewPage(ctx, request, "", "")
	if err != nil {
		return ReviewSnapshot{}, fmt.Errorf("read %s#%d: %w", repo, number, err)
	}
	if err := r.completeReviewPages(ctx, request, pull); err != nil {
		return ReviewSnapshot{}, fmt.Errorf("read %s#%d: %w", repo, number, err)
	}
	result, err := pull.snapshot(number)
	if err != nil {
		return ReviewSnapshot{}, fmt.Errorf("read %s#%d: %w", repo, number, err)
	}
	return result, nil
}

const reviewReadQuery = `query($owner:String!,$name:String!,$number:Int!,
 $threadsAfter:String,$reviewsAfter:String,$checksAfter:String,
 $threads:Boolean!=true,$reviews:Boolean!=true,$checks:Boolean!=true){
 repository(owner:$owner,name:$name){pullRequest(number:$number){number state headRefOid
 reviewThreads(first:100,after:$threadsAfter) @include(if:$threads){pageInfo{hasNextPage endCursor} nodes{id isResolved path line
 comments(first:1){nodes{author{login} createdAt}}}}
 reviews(first:100,after:$reviewsAfter) @include(if:$reviews){pageInfo{hasNextPage endCursor} nodes{id author{login} state submittedAt}}
 commits(last:1){nodes{commit{statusCheckRollup{state contexts(first:100,after:$checksAfter) @include(if:$checks){
 pageInfo{hasNextPage endCursor} nodes{__typename ... on CheckRun{status conclusion}
 ... on StatusContext{state}}}}}}}
 }}}
`
