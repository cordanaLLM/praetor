package forge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// TestErrRateLimitedWithoutCause requires a stable diagnostic and a nil unwrap for a zero value.
func TestErrRateLimitedWithoutCause(t *testing.T) {
	limited := &ErrRateLimited{}
	if cause := limited.Unwrap(); cause != nil {
		t.Fatalf("zero-value cause = %v", cause)
	}
	if got := limited.Error(); got != "GitHub read rate limited" {
		t.Fatalf("zero-value diagnostic = %q", got)
	}
}

// TestReviewGHCommandFixture supplies only explicit fixture output and selected nonsecret variables.
func TestReviewGHCommandFixture(t *testing.T) {
	if len(os.Args) < 4 || os.Args[len(os.Args)-4] != "--review-fixture" {
		return
	}
	args := os.Args[len(os.Args)-3:]
	code, err := strconv.Atoi(args[0])
	if err != nil {
		t.Fatal(err)
	}
	output, message := os.Stderr, args[2]
	if args[1] == "environment" {
		output, message = os.Stdout, os.Getenv("PRAETOR_GH_NOWAIT")+":"+os.Getenv("PRAETOR_REVIEW_FIXTURE")
	}
	if _, err := fmt.Fprint(output, message); err != nil {
		t.Fatal(err)
	}
	os.Exit(code)
}

// reviewGHFixtureRun replaces gh with the test executable and preserves its captured stderr.
func reviewGHFixtureRun(t *testing.T, code int, mode, diagnostic string) func(context.Context, string, ...string) ([]byte, error) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return func(ctx context.Context, dir string, _ ...string) ([]byte, error) {
		result, err := util.RunCommandBytes(ctx, dir, executable, util.MaxCommandOutputBytes,
			"-test.run=^TestReviewGHCommandFixture$", "--", "--review-fixture", strconv.Itoa(code), mode, diagnostic)
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			exit.Stderr = result.Stderr
		}
		return result.Stdout, err
	}
}

// TestGHReviewReaderNowaitEnvironment requires a per-call override without losing inherited values.
func TestGHReviewReaderNowaitEnvironment(t *testing.T) {
	t.Setenv("PRAETOR_GH_NOWAIT", "0")
	t.Setenv("PRAETOR_REVIEW_FIXTURE", "kept")
	reader := GHReviewReader{Dir: t.TempDir(), Run: reviewGHFixtureRun(t, 0, "environment", "")}
	data, err := reader.call(t.Context(), "fixture")
	if err != nil || string(data) != "1:kept" {
		t.Fatalf("gh environment = %q, err = %v", data, err)
	}
	if os.Getenv("PRAETOR_GH_NOWAIT") != "0" {
		t.Fatal("review read changed the parent environment")
	}
}

// TestGHReviewReaderRateLimits exercises pacing, GitHub messages, and missing metadata through the reader.
func TestGHReviewReaderRateLimits(t *testing.T) {
	for _, tc := range []struct {
		name, diagnostic                string
		code                            int
		bucket, remaining, limit, reset string
	}{
		{"paced", "gh_guard: review paced (graphql 37 of 100 remaining), reset 22:45", 75, "graphql", "37", "100", "22:45"},
		{"zero", "gh_guard: review paced (core 0 of 5000 remaining), reset 00:00", 75, "core", "0", "5000", "00:00"},
		{"no-stderr", "", 75, "", "", "", ""},
		{"primary", "API rate limit already exceeded", 1, "", "", "", ""},
		{"primary-case", "api RATE LIMIT ALREADY exceeded", 1, "", "", "", ""},
		{"secondary", "You hit a SECONDARY RATE LIMIT", 1, "", "", "", ""},
		{"bad-metadata", "gh_guard: paced (graphql ? of ?), reset 99:99", 75, "", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := GHReviewReader{Dir: t.TempDir(), Run: reviewGHFixtureRun(t, tc.code, "diagnostic", tc.diagnostic)}
			_, err := reader.OwnOpen(t.Context(), "example/widget")
			var limited *ErrRateLimited
			if !errors.As(err, &limited) {
				t.Fatalf("rate limit not typed: %v", err)
			}
			if limited.Bucket != tc.bucket || limited.Remaining != tc.remaining || limited.Limit != tc.limit || limited.Reset != tc.reset {
				t.Fatalf("rate metadata = %+v", limited)
			}
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != tc.code {
				t.Fatalf("command cause lost: %v", err)
			}
			if !strings.Contains(err.Error(), tc.diagnostic) {
				t.Fatalf("stderr cause lost: %v", err)
			}
		})
	}
}

// TestClassifyGHErrorBoundaries rejects diagnostic-only successes and errors without rate-limit stderr.
func TestClassifyGHErrorBoundaries(t *testing.T) {
	failure := errors.New("secondary rate limit in the error, not stderr")
	for _, tc := range []struct {
		failure error
		stderr  string
	}{
		{nil, "secondary rate limit"},
		{failure, ""},
		{failure, "gh_guard: review paced (graphql 0 of 100), reset 00:00"},
		{failure, "API request failed"},
	} {
		got := classifyGHError(tc.failure, []byte(tc.stderr))
		var limited *ErrRateLimited
		if !errors.Is(got, tc.failure) || errors.As(got, &limited) {
			t.Fatalf("non-rate failure changed: %v", got)
		}
	}
}

// TestGHReviewReaderOtherFailures retains ordinary errors instead of permitting a stop deferral.
func TestGHReviewReaderOtherFailures(t *testing.T) {
	for _, diagnostic := range []string{"permission denied", "gh_guard: review paced", "API query failed"} {
		reader := GHReviewReader{Dir: t.TempDir(), Run: reviewGHFixtureRun(t, 1, "diagnostic", diagnostic)}
		_, err := reader.ReadBatch(t.Context(), "example/widget", []int{7})
		var limited *ErrRateLimited
		if err == nil || errors.As(err, &limited) {
			t.Fatalf("ordinary failure became deferral: %v", err)
		}
	}
}
