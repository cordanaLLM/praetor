package forge

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type reviewConnection[T any] struct {
	Nodes    []T
	PageInfo *struct{ HasNextPage bool }
}

type reviewActor struct{ Login string }
type reviewComment struct {
	Author    *reviewActor
	CreatedAt time.Time
}
type reviewThreadRaw struct {
	ID, Path   string
	Line       int
	IsResolved *bool
	Comments   struct{ Nodes []reviewComment }
}
type reviewRaw struct {
	ID, State   string
	Author      *reviewActor
	SubmittedAt time.Time
}
type reviewCheckRaw struct {
	Typename                  string `json:"__typename"`
	Status, Conclusion, State string
}
type reviewRollup struct {
	State    string
	Contexts reviewConnection[reviewCheckRaw]
}
type reviewCommitRaw struct {
	Commit struct{ StatusCheckRollup *reviewRollup }
}
type reviewPullRaw struct {
	Number        int
	State         string
	ReviewThreads reviewConnection[reviewThreadRaw]
	Reviews       reviewConnection[reviewRaw]
	Commits       struct{ Nodes []reviewCommitRaw }
}

func decodeReviewSnapshot(data []byte, number int) (ReviewSnapshot, error) {
	var envelope graphQLResponse
	if err := json.Unmarshal(data, &envelope); err != nil {
		return ReviewSnapshot{}, fmt.Errorf("decode review envelope: %w", err)
	}
	if len(envelope.Errors) > 0 {
		return ReviewSnapshot{}, fmt.Errorf("GraphQL review read: %s", envelope.Errors[0].Message)
	}
	var result struct {
		Repository *struct{ PullRequest *reviewPullRaw }
	}
	if err := json.Unmarshal(envelope.Data, &result); err != nil {
		return ReviewSnapshot{}, fmt.Errorf("decode review data: %w", err)
	}
	if result.Repository == nil || result.Repository.PullRequest == nil {
		return ReviewSnapshot{}, errors.New("review repository or pull request missing")
	}
	return result.Repository.PullRequest.snapshot(number)
}

func (p reviewPullRaw) snapshot(number int) (ReviewSnapshot, error) {
	if p.Number != number || !validReviewPRState(p.State) {
		return ReviewSnapshot{}, errors.New("review pull request identity or state invalid")
	}
	if err := completeReviewConnection(p.ReviewThreads); err != nil {
		return ReviewSnapshot{}, fmt.Errorf("review threads: %w", err)
	}
	if err := completeReviewConnection(p.Reviews); err != nil {
		return ReviewSnapshot{}, fmt.Errorf("reviews: %w", err)
	}
	snapshot := ReviewSnapshot{Number: p.Number, State: p.State}
	threads, err := decodeReviewThreads(p.ReviewThreads.Nodes)
	if err != nil {
		return ReviewSnapshot{}, err
	}
	snapshot.Threads = threads
	for _, review := range p.Reviews.Nodes {
		if review.ID == "" || review.State == "" {
			return ReviewSnapshot{}, errors.New("review identity or state missing")
		}
		snapshot.Reviews = append(snapshot.Reviews, PullRequestReview{
			ID: review.ID, Author: reviewLogin(review.Author), State: review.State, SubmittedAt: review.SubmittedAt,
		})
	}
	return snapshotWithChecks(snapshot, p.Commits.Nodes)
}

func validReviewPRState(state string) bool {
	return state == "OPEN" || state == "CLOSED" || state == "MERGED"
}

func completeReviewConnection[T any](c reviewConnection[T]) error {
	if c.PageInfo == nil || c.Nodes == nil {
		return errors.New("missing connection data")
	}
	if c.PageInfo.HasNextPage || len(c.Nodes) > 100 {
		return errors.New("connection exceeds first 100 items; refusing incomplete read")
	}
	return nil
}

func reviewLogin(actor *reviewActor) string {
	if actor == nil || actor.Login == "" {
		return "unknown"
	}
	return actor.Login
}

func decodeReviewThreads(rows []reviewThreadRaw) ([]ReviewThread, error) {
	threads := make([]ReviewThread, 0, len(rows))
	for _, row := range rows {
		if row.ID == "" || row.Path == "" || row.IsResolved == nil || len(row.Comments.Nodes) != 1 {
			return nil, errors.New("review thread identity, resolution or first comment missing")
		}
		comment := row.Comments.Nodes[0]
		if comment.CreatedAt.IsZero() {
			return nil, errors.New("review thread first comment createdAt missing")
		}
		threads = append(threads, ReviewThread{ID: row.ID, Path: row.Path, Line: row.Line,
			Resolved: *row.IsResolved, Author: reviewLogin(comment.Author), CreatedAt: comment.CreatedAt})
	}
	return threads, nil
}

func snapshotWithChecks(snapshot ReviewSnapshot, commits []reviewCommitRaw) (ReviewSnapshot, error) {
	if snapshot.State != "OPEN" {
		return snapshot, nil
	}
	if len(commits) != 1 {
		return ReviewSnapshot{}, errors.New("review head commit missing")
	}
	rollup := commits[0].Commit.StatusCheckRollup
	if rollup == nil {
		return snapshot, nil // No published checks yet: keep waiting.
	}
	if !validReviewRollupState(rollup.State) {
		return ReviewSnapshot{}, errors.New("check rollup state missing or unknown")
	}
	if err := completeReviewConnection(rollup.Contexts); err != nil {
		return ReviewSnapshot{}, fmt.Errorf("check contexts: %w", err)
	}
	snapshot.ChecksConclusion = rollup.State
	snapshot.ChecksTerminal = len(rollup.Contexts.Nodes) > 0
	for _, check := range rollup.Contexts.Nodes {
		terminal, err := reviewCheckTerminal(check)
		if err != nil {
			return ReviewSnapshot{}, err
		}
		snapshot.ChecksTerminal = snapshot.ChecksTerminal && terminal
	}
	return snapshot, nil
}

func validReviewRollupState(state string) bool {
	switch state {
	case "SUCCESS", "FAILURE", "ERROR", "PENDING", "EXPECTED":
		return true
	default:
		return false
	}
}

func reviewCheckTerminal(check reviewCheckRaw) (bool, error) {
	switch check.Typename {
	case "CheckRun":
		if check.Status == "" {
			return false, errors.New("check run status missing")
		}
		return check.Status == "COMPLETED" && check.Conclusion != "", nil
	case "StatusContext":
		switch check.State {
		case "SUCCESS", "FAILURE", "ERROR":
			return true, nil
		case "PENDING", "EXPECTED":
			return false, nil
		}
	}
	return false, errors.New("unknown review check context type or state")
}
