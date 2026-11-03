package forge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

const reviewBatchConcurrency = 8

// ReadBatch reads the discovered own inventory in one query; only truncated threads use paginated Read.
// Missing map entries are unread, even when other entries succeed. All calls share the caller's deadline.
func (r GHReviewReader) ReadBatch(ctx context.Context, repo string, numbers []int) (map[int]ReviewSnapshot, error) {
	args, err := reviewBatchArgs(repo, numbers)
	if err != nil {
		return nil, err
	}
	if len(numbers) == 0 {
		return map[int]ReviewSnapshot{}, nil
	}
	data, err := r.call(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("read review batch: %w", err)
	}
	var result struct{ Repository map[string]*reviewPullRaw }
	if err := decodeReviewData(data, &result); err != nil {
		return nil, err
	}
	snapshots := make([]ReviewSnapshot, len(numbers))
	failures := make([]error, len(numbers))
	var group sync.WaitGroup
	for worker := 0; worker < min(reviewBatchConcurrency, len(numbers)); worker++ {
		group.Go(func() {
			for i := worker; i < len(numbers); i += reviewBatchConcurrency {
				pull := result.Repository[fmt.Sprintf("pr%d", numbers[i])]
				snapshots[i], failures[i] = r.batchSnapshot(ctx, repo, numbers[i], pull)
			}
		})
	}
	group.Wait()
	complete := make(map[int]ReviewSnapshot, len(numbers))
	for i, number := range numbers {
		if failures[i] != nil {
			failures[i] = fmt.Errorf("read %s#%d: %w", repo, number, failures[i])
			continue
		}
		complete[number] = snapshots[i]
	}
	return complete, errors.Join(failures...)
}

// reviewBatchArgs binds both non-null variables and selects at most 100 validated PR aliases.
func reviewBatchArgs(repo string, numbers []int) ([]string, error) {
	owner, name, err := ParseReviewRepository(repo)
	if err != nil {
		return nil, err
	}
	if len(numbers) > MaxReviewPullRequests {
		return nil, errors.New("review batch exceeds 100 pull requests")
	}
	var query strings.Builder
	query.WriteString(`query($owner:String!,$name:String!){repository(owner:$owner,name:$name){`)
	seen := make(map[int]bool, len(numbers))
	for _, number := range numbers {
		if number < 1 || seen[number] {
			return nil, errors.New("review batch contains invalid or duplicate number")
		}
		seen[number] = true
		fmt.Fprintf(&query, `pr%d:pullRequest(number:%d){number state headRefOid
 reviewThreads(first:100){pageInfo{hasNextPage endCursor} nodes{id isResolved path line
 comments(first:1){nodes{author{login} createdAt}}}}}`, number, number)
	}
	query.WriteString(`}}`)
	return []string{"api", "graphql", "-f", "query=" + query.String(), "-f", "owner=" + owner, "-f", "name=" + name}, nil
}

// batchSnapshot refuses incomplete nodes and delegates truncated thread connections to bounded Read.
func (r GHReviewReader) batchSnapshot(ctx context.Context, repo string, number int, pull *reviewPullRaw) (ReviewSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return ReviewSnapshot{}, err
	}
	if pull == nil || pull.Number != number || pull.HeadRefOID == "" || !ValidReviewPRState(pull.State) {
		return ReviewSnapshot{}, errors.New("review batch pull request identity, head or state missing or invalid")
	}
	if err := validateReviewConnection(pull.ReviewThreads, reviewPageSize); err != nil {
		return ReviewSnapshot{}, err
	}
	if !pull.ReviewThreads.PageInfo.HasNextPage {
		return pull.threadSnapshot(number)
	}
	snapshot, err := r.Read(ctx, repo, number)
	if err != nil {
		return ReviewSnapshot{}, err
	}
	if snapshot.HeadRefOID != pull.HeadRefOID || snapshot.State != pull.State {
		return ReviewSnapshot{}, errors.New("review pull request changed during batch pagination")
	}
	return snapshot, nil
}
