package prwait

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
)

type fixtureReader struct {
	steps        []forge.ReviewSnapshot
	reads        int
	err          error
	discoveryErr error
	requested    []int
}

// OwnOpen returns the configured mock pull request numbers or discovery error.
func (f *fixtureReader) OwnOpen(context.Context, string) ([]int, error) {
	return []int{7}, f.discoveryErr
}

// ReadBatch rejects accidental use of the stop-only thread batch in the complete pr-wait reader.
func (f *fixtureReader) ReadBatch(context.Context, string, []int) (map[int]forge.ReviewSnapshot, error) {
	return nil, errors.New("pr wait must use complete per-PR Read")
}

// Read records the requested pull request number and returns the configured mock snapshot.
func (f *fixtureReader) Read(ctx context.Context, _ string, number int) (forge.ReviewSnapshot, error) {
	if _, ok := ctx.Deadline(); !ok {
		return forge.ReviewSnapshot{}, errors.New("missing read deadline")
	}
	f.requested = append(f.requested, number)
	index := min(f.reads, len(f.steps)-1)
	f.reads++
	if f.err != nil {
		return forge.ReviewSnapshot{}, f.err
	}
	snapshot := f.steps[index]
	snapshot.Number = number
	return snapshot, nil
}

// pending constructs an open pull request snapshot fixture with pending status checks.
func pending() forge.ReviewSnapshot {
	return forge.ReviewSnapshot{Number: 7, State: "OPEN", ChecksConclusion: "PENDING"}
}

// thread constructs an unresolved review thread fixture.
func thread() forge.ReviewThread {
	return forge.ReviewThread{ID: "new-thread", Path: "main.go", Line: 9, Author: "human", CreatedAt: time.Now().Add(-time.Hour)}
}

// testOptions returns default wait options configured with short intervals for testing.
func testOptions() Options {
	return Options{Repository: "example/widget", Timeout: 100 * time.Millisecond, Interval: 2 * time.Millisecond}
}

// TestWaitStandingThreadDoesNotWait pins that standing unresolved review threads return immediately without waiting.
func TestWaitStandingThreadDoesNotWait(t *testing.T) {
	snapshot := pending()
	snapshot.Threads = []forge.ReviewThread{thread()}
	reader := &fixtureReader{steps: []forge.ReviewSnapshot{snapshot}}
	options := testOptions()
	options.Interval = time.Hour
	result := Wait(t.Context(), reader, options)
	if result.Code != 10 || reader.reads != 1 || !strings.Contains(result.Items[0], "unresolved thread main.go:9 by human (") {
		t.Fatalf("standing finding must return before any interval: %+v reads=%d", result, reader.reads)
	}
}

// Planted arrival occurs strictly after the baseline; reading once cannot pass.
func TestWaitThreadArrivesOnSecondPoll(t *testing.T) {
	second := pending()
	second.Threads = []forge.ReviewThread{thread()}
	reader := &fixtureReader{steps: []forge.ReviewSnapshot{pending(), second}}
	result := Wait(t.Context(), reader, testOptions())
	if result.Code != 10 || reader.reads != 2 || !strings.Contains(result.Items[0], "new thread main.go:9 by human") {
		t.Fatalf("missed planted arrival: %+v reads=%d", result, reader.reads)
	}
}

// TestWaitNewBotReview pins that reviews submitted after the baseline trigger immediate wait termination.
func TestWaitNewBotReview(t *testing.T) {
	second := pending()
	second.Reviews = []forge.PullRequestReview{{ID: "bot-review", Author: "new-bot[bot]", State: "COMMENTED", SubmittedAt: time.Now()}}
	reader := &fixtureReader{steps: []forge.ReviewSnapshot{pending(), second}}
	result := Wait(t.Context(), reader, testOptions())
	if result.Code != 10 || result.Items[0] != "review example/widget#7: new review COMMENTED by new-bot[bot]" {
		t.Fatalf("bot review missed: %+v", result)
	}
}

// TestWaitTerminalClosedTimeoutError pins exit codes and prefix formatting across terminal wait outcomes.
func TestWaitTerminalClosedTimeoutError(t *testing.T) {
	for _, scenario := range []string{"checks", "merged", "closed", "timeout", "read-error", "discovery-error"} {
		t.Run(scenario, func(t *testing.T) {
			reader, options, want, prefix := terminalFixture(scenario)
			result := Wait(t.Context(), reader, options)
			if result.Code != want || len(result.Items) != 1 || !strings.HasPrefix(result.Items[0], prefix) {
				t.Fatalf("%s: %+v want code=%d prefix=%q", scenario, result, want, prefix)
			}
			if strings.HasSuffix(scenario, "error") && !strings.Contains(result.Items[0], "fixture offline") {
				t.Fatalf("read failure lost cause: %+v", result)
			}
		})
	}
}

// terminalFixture constructs a fixture reader and options for a specific terminal wait scenario.
func terminalFixture(scenario string) (*fixtureReader, Options, int, string) {
	snapshot, options, code, prefix := pending(), testOptions(), 0, "checks example/widget#7: SUCCESS"
	switch scenario {
	case "checks":
		snapshot.ChecksTerminal, snapshot.ChecksConclusion = true, "SUCCESS"
	case "merged":
		snapshot.State, code, prefix = "MERGED", 11, "merged example/widget#7"
	case "closed":
		snapshot.State, code, prefix = "CLOSED", 11, "closed example/widget#7"
	case "timeout":
		options.Timeout, code, prefix = time.Millisecond, 124, "timeout example/widget:"
	case "read-error", "discovery-error":
		code, prefix = 2, "read example/widget:"
	}
	reader := &fixtureReader{steps: []forge.ReviewSnapshot{snapshot}}
	if scenario == "read-error" {
		reader.err = errors.New("fixture offline")
	}
	if scenario == "discovery-error" {
		reader.discoveryErr = errors.New("fixture offline")
	}
	return reader, options, code, prefix
}

// TestWaitBaselineResolvedAndExplicitScope pins that baseline reviews and resolved threads do not trigger review exits.
func TestWaitBaselineResolvedAndExplicitScope(t *testing.T) {
	first := pending()
	resolved := thread()
	resolved.Resolved = true
	first.Threads = []forge.ReviewThread{resolved}
	first.Reviews = []forge.PullRequestReview{{ID: "old", State: "APPROVED", Author: "human"}}
	second := first
	second.ChecksTerminal, second.ChecksConclusion = true, "FAILURE"
	reader := &fixtureReader{steps: []forge.ReviewSnapshot{first, second}, discoveryErr: errors.New("must not discover")}
	options := testOptions()
	options.Numbers = []int{7}
	result := Wait(t.Context(), reader, options)
	if result.Code != 0 || reader.reads != 2 || result.Items[0] != "checks example/widget#7: FAILURE" {
		t.Fatalf("baseline review or resolved thread woke wait: %+v reads=%d", result, reader.reads)
	}
}

// TestWaitInputBoundaries pins input validation rejections for invalid wait options before any forge reads occur.
func TestWaitInputBoundaries(t *testing.T) {
	for _, mutate := range []func(*Options){
		func(o *Options) { o.Repository = "" },
		func(o *Options) { o.Timeout = 0 },
		func(o *Options) { o.Interval = -1 },
		func(o *Options) { o.Numbers = []int{0} },
		func(o *Options) { o.Numbers = make([]int, 101) },
		func(o *Options) { o.Timeout = time.Hour; o.Interval = time.Nanosecond },
	} {
		options := testOptions()
		mutate(&options)
		reader := &fixtureReader{}
		if result := Wait(t.Context(), reader, options); result.Code != 2 || reader.reads != 0 {
			t.Fatalf("invalid options reached forge: %+v reads=%d", result, reader.reads)
		}
	}
}

// TestWaitRequiresEveryNamedPRToFinish pins that wait does not terminate until all named pull requests reach a terminal state.
func TestWaitRequiresEveryNamedPRToFinish(t *testing.T) {
	terminal := pending()
	terminal.ChecksTerminal, terminal.ChecksConclusion = true, "SUCCESS"
	reader := &fixtureReader{steps: []forge.ReviewSnapshot{terminal, pending(), terminal, terminal}}
	options := testOptions()
	options.Numbers = []int{7, 8}
	result := Wait(t.Context(), reader, options)
	if result.Code != 0 || reader.reads != 4 || len(result.Items) != 2 || result.Items[1] != "checks example/widget#8: SUCCESS" {
		t.Fatalf("one terminal PR ended the whole wait: %+v reads=%d", result, reader.reads)
	}
}

// TestWaitReviewPrecedesTerminalChecks pins that unresolved review threads take precedence over successful terminal checks.
func TestWaitReviewPrecedesTerminalChecks(t *testing.T) {
	terminal := pending()
	terminal.ChecksTerminal, terminal.ChecksConclusion = true, "SUCCESS"
	terminal.Threads = []forge.ReviewThread{thread()}
	result := Wait(t.Context(), &fixtureReader{steps: []forge.ReviewSnapshot{terminal}}, testOptions())
	if result.Code != 10 {
		t.Fatalf("green checks hid standing review: %+v", result)
	}
}
