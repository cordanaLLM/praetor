package prwait

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
)

const completeReviewFixture = `{"data":{"repository":{"pullRequest":{
 "number":7,"state":"OPEN",
 "reviewThreads":{"nodes":[],"pageInfo":{"hasNextPage":false}},
 "reviews":{"nodes":[],"pageInfo":{"hasNextPage":false}},
 "commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"SUCCESS",
 "contexts":{"nodes":[{"__typename":"CheckRun","status":"COMPLETED","conclusion":"SUCCESS"}],"pageInfo":{"hasNextPage":false}}
 }}}]}
}}}}`

// fixtureGH constructs a mock GHReviewReader returning the specified answer payload.
func fixtureGH(t *testing.T, answer string) forge.GHReviewReader {
	t.Helper()
	return forge.GHReviewReader{Run: func(ctx context.Context, _ string, args ...string) ([]byte, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("gh read lacks timeout")
		}
		if len(args) < 2 || args[0] != "api" || args[1] != "graphql" {
			t.Fatalf("unexpected gh call: %q", args)
		}
		query := strings.Join(args, " ")
		for _, bound := range []string{"reviewThreads(first:100,after:$threadsAfter)", "reviews(first:100,after:$reviewsAfter)", "comments(first:1)", "commits(last:1)", "contexts(first:100,after:$checksAfter)"} {
			if !strings.Contains(query, bound) {
				t.Fatalf("query lacks bound %q", bound)
			}
		}
		return []byte(answer), nil
	}}
}

// TestGHReviewReaderCompleteAndThreadFields pins decoding of pull request review threads, comments, and status checks.
func TestGHReviewReaderCompleteAndThreadFields(t *testing.T) {
	reader := fixtureGH(t, completeReviewFixture)
	snapshot, err := reader.Read(t.Context(), "example/widget", 7)
	if err != nil || !snapshot.ChecksTerminal || snapshot.ChecksConclusion != "SUCCESS" {
		t.Fatalf("complete checks: %+v %v", snapshot, err)
	}
	threadJSON := `"reviewThreads":{"nodes":[{"id":"thread","path":"main.go","line":null,"isResolved":false,
 "comments":{"nodes":[{"author":{"login":"bot[bot]"},"createdAt":"2026-11-01T18:00:00Z"}]}}]`
	answer := strings.Replace(completeReviewFixture, `"reviewThreads":{"nodes":[]`, threadJSON, 1)
	snapshot, err = fixtureGH(t, answer).Read(t.Context(), "example/widget", 7)
	if err != nil || len(snapshot.Threads) != 1 || snapshot.Threads[0].Author != "bot[bot]" || snapshot.Threads[0].Line != 0 {
		t.Fatalf("first comment and file-level thread: %+v %v", snapshot, err)
	}
}

// TestGHReviewReaderFailsClosed pins error handling on corrupted, truncated, or malformed GraphQL responses.
func TestGHReviewReaderFailsClosed(t *testing.T) {
	for _, answer := range []string{
		`{`, `null`, `{"data":null}`, `{"data":{"repository":null}}`,
		`{"data":{"repository":{"pullRequest":null}}}`,
		`{"errors":[{"message":"fixture denied"}],"data":{"repository":null}}`,
		strings.Replace(completeReviewFixture, `"hasNextPage":false`, `"hasNextPage":true`, 1),
		strings.Replace(completeReviewFixture, `"number":7`, `"number":8`, 1),
		strings.Replace(completeReviewFixture, `"state":"OPEN"`, `"state":"SURPRISE"`, 1),
		strings.Replace(completeReviewFixture, `"state":"SUCCESS"`, `"state":"UNKNOWN"`, 1),
		strings.Replace(completeReviewFixture, `"__typename":"CheckRun"`, `"__typename":"Unknown"`, 1),
		strings.Replace(completeReviewFixture, `"reviewThreads":{"nodes":[]`, `"reviewThreads":{"nodes":[{"id":"missing-fields"}]`, 1),
	} {
		if _, err := fixtureGH(t, answer).Read(t.Context(), "example/widget", 7); err == nil {
			t.Fatalf("incomplete read silently passed: %s", answer)
		}
	}
}

// TestGHReviewReaderPendingDoesNotFinishOnFailedRollup pins that pending checks are not treated as terminal on rollup failure.
func TestGHReviewReaderPendingDoesNotFinishOnFailedRollup(t *testing.T) {
	answer := strings.Replace(completeReviewFixture, `"state":"SUCCESS"`, `"state":"FAILURE"`, 1)
	answer = strings.Replace(answer, `"status":"COMPLETED"`, `"status":"IN_PROGRESS"`, 1)
	snapshot, err := fixtureGH(t, answer).Read(t.Context(), "example/widget", 7)
	if err != nil || snapshot.ChecksTerminal {
		t.Fatalf("pending check masked by failed rollup: %+v %v", snapshot, err)
	}
}

// TestGHReviewReaderDiscovery pins open pull request discovery and error handling across varied CLI responses.
func TestGHReviewReaderDiscovery(t *testing.T) {
	for _, answer := range []string{`[{"number":7}]`, `[]`, `null`, " \nnull\n", `[{"number":0}]`, `{`} {
		reader := forge.GHReviewReader{Run: func(ctx context.Context, _ string, args ...string) ([]byte, error) {
			joined := strings.Join(args, " ")
			if !strings.Contains(joined, "--author @me --state open --limit 101 --json number") {
				t.Fatalf("wrong discovery scope: %q", args)
			}
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("discovery lacks deadline")
			}
			return []byte(answer), nil
		}}
		numbers, err := reader.OwnOpen(t.Context(), "example/widget")
		valid := answer == `[{"number":7}]` || answer == `[]`
		if (err == nil) != valid {
			t.Fatalf("answer=%s numbers=%v err=%v", answer, numbers, err)
		}
	}
}

// TestGHReviewReaderCommandError pins error propagation when the underlying gh CLI invocation fails.
func TestGHReviewReaderCommandError(t *testing.T) {
	reader := forge.GHReviewReader{Run: func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("fixture command failed")
	}}
	if _, err := reader.Read(t.Context(), "example/widget", 7); err == nil || !strings.Contains(err.Error(), "fixture command failed") {
		t.Fatalf("command failure disappeared: %v", err)
	}
}

// TestReviewRepositoryAndAgeBoundaries pins repository name parsing and relative age formatting boundaries.
func TestReviewRepositoryAndAgeBoundaries(t *testing.T) {
	for _, repo := range []string{"", "owner", "owner/name/extra", "../name"} {
		if _, _, err := forge.ParseReviewRepository(repo); err == nil {
			t.Fatalf("invalid repository accepted: %q", repo)
		}
	}
	owner, name, err := forge.ParseReviewRepository("example/widget")
	if err != nil || owner != "example" || name != "widget" {
		t.Fatalf("valid repository rejected: %q %q %v", owner, name, err)
	}
	now := time.Now()
	for delta, want := range map[time.Duration]string{0: "0s", time.Second: "0s", -time.Hour: "1h0m0s"} {
		if age := forge.ReviewAge(now, now.Add(delta)); age != want {
			t.Fatalf("delta=%s age=%q want=%q", delta, age, want)
		}
	}
}
