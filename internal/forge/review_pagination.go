package forge

import (
	"context"
	"errors"
	"fmt"
	"strconv"
)

const reviewPageSize = 100
const maxReviewPages = 10

type reviewReadRequest struct {
	owner, name string
	number      int
}

type reviewPageReader func(context.Context, string, string) (*reviewPullRaw, error)

// reviewPage reads one selected connection; completed connections are excluded from continuations.
func (r GHReviewReader) reviewPage(ctx context.Context, request reviewReadRequest, kind, cursor string) (*reviewPullRaw, error) {
	data, err := r.call(ctx, reviewPageArgs(request, kind, cursor)...)
	if err != nil {
		return nil, fmt.Errorf("read review page: %w", err)
	}
	return decodeReviewPull(data, request.number)
}

// reviewPageArgs builds the exact GraphQL argument vector sent through the reader's call boundary.
func reviewPageArgs(request reviewReadRequest, kind, cursor string) []string {
	args := []string{"api", "graphql", "-f", "query=" + reviewReadQuery,
		"-f", "owner=" + request.owner, "-f", "name=" + request.name, "-F", "number=" + strconv.Itoa(request.number)}
	for _, connection := range []string{"threads", "reviews", "checks"} {
		args = append(args, "-F", connection+"="+strconv.FormatBool(kind == "" || connection == kind))
	}
	if kind != "" {
		args = append(args, "-f", kind+"After="+cursor)
	}
	return args
}

// completeReviewPages collects each connection independently before decoding any snapshot.
func (r GHReviewReader) completeReviewPages(ctx context.Context, request reviewReadRequest, pull *reviewPullRaw) error {
	fetch := func(ctx context.Context, kind, cursor string) (*reviewPullRaw, error) {
		next, err := r.reviewPage(ctx, request, kind, cursor)
		if err != nil {
			return nil, err
		}
		if next.State != pull.State || next.HeadRefOID != pull.HeadRefOID {
			return nil, errors.New("review pull request changed during pagination")
		}
		return next, nil
	}
	var err error
	pull.ReviewThreads, err = paginateReviewConnection(ctx, pull.ReviewThreads, "threads", fetch,
		func(p *reviewPullRaw) reviewConnection[reviewThreadRaw] { return p.ReviewThreads })
	if err != nil {
		return fmt.Errorf("review threads: %w", err)
	}
	pull.Reviews, err = paginateReviewConnection(ctx, pull.Reviews, "reviews", fetch,
		func(p *reviewPullRaw) reviewConnection[reviewRaw] { return p.Reviews })
	if err != nil {
		return fmt.Errorf("reviews: %w", err)
	}
	if pull.State != "OPEN" || len(pull.Commits.Nodes) != 1 {
		return nil // The snapshot validator handles missing commits for open pull requests.
	}
	rollup := pull.Commits.Nodes[0].Commit.StatusCheckRollup
	if rollup == nil {
		return nil
	}
	rollup.Contexts, err = paginateReviewConnection(ctx, rollup.Contexts, "checks", fetch, reviewCheckConnection)
	if err != nil {
		return fmt.Errorf("check contexts: %w", err)
	}
	return nil
}

// reviewCheckConnection extracts head checks or returns missing data for fail-closed validation.
func reviewCheckConnection(pull *reviewPullRaw) reviewConnection[reviewCheckRaw] {
	if len(pull.Commits.Nodes) != 1 || pull.Commits.Nodes[0].Commit.StatusCheckRollup == nil {
		return reviewConnection[reviewCheckRaw]{}
	}
	return pull.Commits.Nodes[0].Commit.StatusCheckRollup.Contexts
}

// paginateReviewConnection follows at most ten pages, rejecting cursor cycles and partial results.
func paginateReviewConnection[T any](ctx context.Context, current reviewConnection[T], kind string,
	fetch reviewPageReader, selectConnection func(*reviewPullRaw) reviewConnection[T],
) (reviewConnection[T], error) {
	nodes := make([]T, 0, len(current.Nodes))
	seen := make(map[string]bool, maxReviewPages)
	for page := 0; page < maxReviewPages; page++ {
		if err := ctx.Err(); err != nil {
			return reviewConnection[T]{}, fmt.Errorf("review pagination cancelled: %w", err)
		}
		cursor, err := reviewPageCursor(current, seen)
		if err != nil {
			return reviewConnection[T]{}, err
		}
		seen[cursor] = true
		nodes = append(nodes, current.Nodes...)
		if !current.PageInfo.HasNextPage {
			current.Nodes = nodes
			return current, nil
		}
		if page == maxReviewPages-1 {
			break
		}
		next, err := fetch(ctx, kind, cursor)
		if err != nil {
			return reviewConnection[T]{}, fmt.Errorf("next review page: %w", err)
		}
		current = selectConnection(next)
	}
	return reviewConnection[T]{}, errors.New("connection exceeds page bound; refusing incomplete read")
}

// reviewPageCursor validates one page and refuses missing or previously consumed continuation cursors.
func reviewPageCursor[T any](page reviewConnection[T], seen map[string]bool) (string, error) {
	if err := validateReviewConnection(page, reviewPageSize); err != nil {
		return "", err
	}
	cursor := page.PageInfo.EndCursor
	if seen[cursor] || page.PageInfo.HasNextPage && cursor == "" {
		return "", errors.New("missing or repeated connection cursor")
	}
	return cursor, nil
}
