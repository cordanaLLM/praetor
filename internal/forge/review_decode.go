package forge

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type reviewConnection[T any] struct {
	Nodes    []T
	PageInfo *reviewPageInfo
}

type reviewPageInfo struct {
	HasNextPage bool
	EndCursor   string
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
	HeadRefOID    string
	ReviewThreads reviewConnection[reviewThreadRaw]
	Reviews       reviewConnection[reviewRaw]
	Commits       struct{ Nodes []reviewCommitRaw }
}

// decodeReviewPull validates the envelope and identity before any continuation request.
func decodeReviewPull(data []byte, number int) (*reviewPullRaw, error) {
	var result struct {
		Repository *struct{ PullRequest *reviewPullRaw }
	}
	if err := decodeReviewData(data, &result); err != nil {
		return nil, err
	}
	if result.Repository == nil || result.Repository.PullRequest == nil {
		return nil, errors.New("review repository or pull request missing")
	}
	pull := result.Repository.PullRequest
	if pull.Number != number || !ValidReviewPRState(pull.State) {
		return nil, errors.New("review pull request identity or state invalid")
	}
	return pull, nil
}

// decodeReviewData rejects malformed and partial GraphQL envelopes before decoding their data.
func decodeReviewData(data []byte, result any) error {
	var envelope graphQLResponse
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("decode review envelope: %w", err)
	}
	if len(envelope.Errors) > 0 {
		return fmt.Errorf("GraphQL review read: %s", envelope.Errors[0].Message)
	}
	if err := json.Unmarshal(envelope.Data, result); err != nil {
		return fmt.Errorf("decode review data: %w", err)
	}
	return nil
}

// snapshot converts a raw GraphQL pull request node into a validated ReviewSnapshot.
func (p reviewPullRaw) snapshot(number int) (ReviewSnapshot, error) {
	snapshot, err := p.threadSnapshot(number)
	if err != nil {
		return ReviewSnapshot{}, err
	}
	if err := completeReviewConnection(p.Reviews); err != nil {
		return ReviewSnapshot{}, fmt.Errorf("reviews: %w", err)
	}
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

// threadSnapshot validates a complete thread connection using the shared snapshot and decoder.
func (p reviewPullRaw) threadSnapshot(number int) (ReviewSnapshot, error) {
	if p.Number != number || !ValidReviewPRState(p.State) {
		return ReviewSnapshot{}, errors.New("review pull request identity or state invalid")
	}
	if err := completeReviewConnection(p.ReviewThreads); err != nil {
		return ReviewSnapshot{}, fmt.Errorf("review threads: %w", err)
	}
	threads, err := decodeReviewThreads(p.ReviewThreads.Nodes)
	if err != nil {
		return ReviewSnapshot{}, err
	}
	return ReviewSnapshot{Number: p.Number, State: p.State, HeadRefOID: p.HeadRefOID, Threads: threads}, nil
}

// ValidReviewPRState reports whether a pull request state string is recognized by GitHub.
func ValidReviewPRState(state string) bool {
	return state == "OPEN" || state == "CLOSED" || state == "MERGED"
}

// completeReviewConnection verifies that a paginated GraphQL connection contains complete, non-truncated data.
func completeReviewConnection[T any](c reviewConnection[T]) error {
	if err := validateReviewConnection(c, maxReviewPages*reviewPageSize); err != nil {
		return err
	}
	if c.PageInfo.HasNextPage {
		return errors.New("connection exceeds page bound; refusing incomplete read")
	}
	return nil
}

// validateReviewConnection rejects absent data and counts beyond the caller's scalar bound.
func validateReviewConnection[T any](c reviewConnection[T], limit int) error {
	if c.PageInfo == nil || c.Nodes == nil {
		return errors.New("missing connection data")
	}
	if len(c.Nodes) > limit {
		return errors.New("connection exceeds item bound")
	}
	return nil
}

// reviewLogin returns the login username of an actor or "unknown" if missing.
func reviewLogin(actor *reviewActor) string {
	if actor == nil || actor.Login == "" {
		return "unknown"
	}
	return actor.Login
}

// decodeReviewThreads parses raw GraphQL review thread nodes into ReviewThread structures.
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

// snapshotWithChecks inspects head commit status check rollups and updates check conclusions on the snapshot.
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

// validReviewRollupState reports whether a status check rollup state string is recognized.
func validReviewRollupState(state string) bool {
	switch state {
	case "SUCCESS", "FAILURE", "ERROR", "PENDING", "EXPECTED":
		return true
	default:
		return false
	}
}

// reviewCheckTerminal reports whether an individual check run or status context has finished executing.
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
