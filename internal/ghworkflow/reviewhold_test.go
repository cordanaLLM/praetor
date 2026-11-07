// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ghworkflow

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// updateReviewHoldWorkflowEnv rewrites the release workflow from its renderer.
const updateReviewHoldWorkflowEnv = "PRAETOR_UPDATE_REVIEW_HOLD_WORKFLOW"

// reviewCounterScript isolates one snapshot so pagination assertions remain independent of polling.
const reviewCounterScript = reviewHoldLibrary + `review_count
if ((REVIEW_THREADS > 0 || REVIEW_PENDING > 0)); then review_error; fi
printf 'clear=true\n' >> "$GITHUB_OUTPUT"
printf '%s\n' "$HOLD_CAUSE" >> "$GITHUB_STEP_SUMMARY"
`

// reviewPollClock advances Bash's clock without sleeping or changing production timing.
const reviewPollClock = `review_sleeps=0
sleep() {
  review_sleeps=$((review_sleeps + 1))
  printf '%s\n' "$1" >> "$FIXTURE_DIR/sleeps"
  SECONDS=$((SECONDS + $1))
  if [[ -f "$FIXTURE_DIR/expired-after-sleep" ]]; then SECONDS=$((SECONDS + 601)); fi
  if [[ -f "$FIXTURE_DIR/clear-after-sleep" && $review_sleeps -ge $(cat "$FIXTURE_DIR/clear-after-sleep") ]]; then
    cp "$FIXTURE_DIR/clear-threads.json" "$FIXTURE_DIR/threads.json"
    cp "$FIXTURE_DIR/clear-checks.json" "$FIXTURE_DIR/checks.json"
  fi
}
`

// TestReviewHoldBoundedWait plants transient and persistent review holds.
func TestReviewHoldBoundedWait(t *testing.T) {
	cases := []struct {
		name                         string
		clearAfter, polls, snapshots int
		expired                      bool
	}{
		{"clears within wait", 1, 1, 2, false},
		{"clears at deadline", 2, 2, 3, false},
		{"stays open", 0, 2, 3, false},
		{"scheduler overshoots deadline", 1, 1, 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := reviewWaitingFixtures(t)
			if tc.clearAfter > 0 {
				writeReviewFixture(t, dir, "clear-after-sleep", strconv.Itoa(tc.clearAfter))
			}
			if tc.expired {
				writeReviewFixture(t, dir, "expired-after-sleep", "")
			}
			out, err := runReviewScript(t, dir, renderReviewHoldScript(1, ReviewHoldPollSeconds))
			assertReviewPolls(t, dir, tc.polls, tc.snapshots)
			assertReviewWaitResult(t, dir, out, err, tc.clearAfter > 0 && !tc.expired)
		})
	}
}

// TestReviewHoldProductionBounds verifies production constants and rendered timing without execution.
func TestReviewHoldProductionBounds(t *testing.T) {
	wantPolls := ReviewHoldWaitMinutes * 60 / ReviewHoldPollSeconds
	if ReviewHoldWaitMinutes != 10 || ReviewHoldPollSeconds != 30 || wantPolls != 20 {
		t.Fatalf("production timing mismatch: wait=%d poll=%d polls=%d", ReviewHoldWaitMinutes, ReviewHoldPollSeconds, wantPolls)
	}
	wantPatterns := []string{
		"review_deadline=$((SECONDS + " + strconv.Itoa(ReviewHoldWaitMinutes*60) + "))\n",
		"for ((review_poll=0; review_poll<=" + strconv.Itoa(wantPolls) + "; review_poll++)); do\n",
		"review_poll == " + strconv.Itoa(wantPolls),
		"review_delay=" + strconv.Itoa(ReviewHoldPollSeconds) + "\n",
	}
	for _, pattern := range wantPatterns {
		if !strings.Contains(reviewHoldScript, pattern) {
			t.Errorf("reviewHoldScript missing %q", pattern)
		}
	}
	assertProductionRendered(t, RenderReviewHoldStep(false), wantPatterns[0], wantPatterns[1], true)
	// Planted negative: a script rendered with non-production bounds fails detection.
	assertProductionRendered(t, renderReviewHoldScript(1, ReviewHoldPollSeconds), wantPatterns[0], wantPatterns[1], false)
}

// assertProductionRendered validates presence or absence of production timing in a script.
func assertProductionRendered(t *testing.T, script, wantDeadline, wantLoop string, matches bool) {
	t.Helper()
	hasPatterns := strings.Contains(script, wantDeadline) && strings.Contains(script, wantLoop)
	if hasPatterns != matches {
		t.Errorf("production pattern match=%t; want %t", hasPatterns, matches)
	}
}

// reviewWaitingFixtures saves clear responses before planting open threads and a pending bot.
func reviewWaitingFixtures(t *testing.T) string {
	t.Helper()
	dir := reviewFixtures(t)
	for _, name := range []string{"threads", "checks"} {
		data, err := readReviewFixture(t, dir, name+".json")
		if err != nil {
			t.Fatal(err)
		}
		writeReviewFixture(t, dir, "clear-"+name+".json", string(data))
	}
	writeReviewFixture(t, dir, "threads.json", `{"data":{"repository":{"pullRequest":{"headRefOid":"head","reviewThreads":{"nodes":[{"isResolved":false}],"pageInfo":{"hasNextPage":false}}}}}}`)
	writeReviewFixture(t, dir, "checks.json", `{"check_runs":[{"name":"Codacy","status":"in_progress"}]}`)
	return dir
}

// assertReviewPolls checks the bounded waits and every paginated counter invocation.
func assertReviewPolls(t *testing.T, dir string, polls, snapshots int) {
	t.Helper()
	sleeps, err := readReviewFixture(t, dir, "sleeps")
	if err != nil || string(sleeps) != strings.Repeat("30\n", polls) {
		t.Fatalf("sleeps=%q read=%v; want %d bounded polls", sleeps, err, polls)
	}
	var want [][]string
	for i := 0; i < snapshots; i++ {
		want = append(want, reviewCountCalls("1", 1, 1, true)...)
	}
	assertReviewCalls(t, dir, want)
}

// assertReviewWaitResult checks clearance output or the original annotated hold cause.
func assertReviewWaitResult(t *testing.T, dir, out string, runErr error, clears bool) {
	t.Helper()
	output, err := readReviewFixture(t, dir, "outputs")
	if clears {
		if runErr != nil || err != nil || string(output) != "clear=true\n" {
			t.Fatalf("output=%q read=%v run=%v out=%s", output, err, runErr, out)
		}
		return
	}
	if !os.IsNotExist(err) || runErr == nil || !strings.Contains(out, "::error title=Gate held%3A review open::unresolved threads=1; pending bot reviews=1; bots=Codacy") {
		t.Fatalf("output=%q read=%v run=%v out=%s; want held without clearance", output, err, runErr, out)
	}
}

// TestReviewHoldReleaseBusyScheduled rescans busy held runs and releases exactly once.
func TestReviewHoldReleaseBusyScheduled(t *testing.T) {
	for _, status := range []string{"in_progress", "queued"} {
		t.Run(status, func(t *testing.T) {
			dir := reviewFixtures(t)
			completed, err := readReviewFixture(t, dir, "runs.json")
			if err != nil {
				t.Fatal(err)
			}
			busy := strings.Replace(string(completed), `"status":"completed","conclusion":"failure"`, `"status":"`+status+`","conclusion":null`, 1)
			writeReviewFixture(t, dir, "runs.json", busy)
			assertReviewReleasePass(t, dir, "check_run", "")
			want := append([][]string{reviewRESTCall("pulls?state=open&sort=created&direction=asc", 1)}, reviewReleaseCalls("1", 0)...)
			assertReviewCalls(t, dir, want)
			writeReviewFixture(t, dir, "runs.json", string(completed))
			assertReviewReleasePass(t, dir, "schedule", "42\n")
			want = append(want, reviewRESTCall("pulls?state=open&sort=created&direction=asc", 1))
			want = append(want, reviewReleaseCalls("1", 5)...)
			assertReviewCalls(t, dir, want)
			writeReviewFixture(t, dir, "runs.json", busy)
			assertReviewReleasePass(t, dir, "schedule", "42\n")
			want = append(want, reviewRESTCall("pulls?state=open&sort=created&direction=asc", 1))
			want = append(want, reviewReleaseCalls("1", 0)...)
			assertReviewCalls(t, dir, want)
		})
	}
}

// assertReviewReleasePass checks a real release script invocation against sandbox reruns.
func assertReviewReleasePass(t *testing.T, dir, event, want string) {
	t.Helper()
	out, err := runReviewScript(t, dir, "GITHUB_EVENT_NAME="+event+"\n"+reviewHoldReleaseScript)
	if err != nil {
		t.Fatalf("event=%s out=%s err=%v", event, out, err)
	}
	data, err := readReviewFixture(t, dir, "reruns")
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("event=%s reruns=%q want=%q", event, data, want)
	}
}

// loadPortabilityWorkflow reads and parses the repository's portability workflow.
func loadPortabilityWorkflow(t *testing.T) Spec {
	t.Helper()
	data, err := os.ReadFile("../../.github/workflows/portability.yml")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

// TestReviewHoldPackageStepBounds prevents a package step consuming the entire job budget.
func TestReviewHoldPackageStepBounds(t *testing.T) {
	spec := loadPortabilityWorkflow(t)
	count := 0
	for id, job := range spec.Jobs {
		for _, step := range job.Steps {
			if step.Name != "Test Every Package" {
				continue
			}
			count++
			bound, err := strconv.Atoi(step.TimeoutMinutes.Value)
			jobBound, jobErr := strconv.Atoi(job.TimeoutMinutes.Value)
			if err != nil || jobErr != nil || bound != 120 || bound >= jobBound {
				t.Errorf("%s: step timeout=%q job timeout=%q; want 120 below job bound", id, step.TimeoutMinutes.Value, job.TimeoutMinutes.Value)
			}
		}
	}
	if count != 2 {
		t.Fatalf("package steps=%d; want both harness and Windows shard", count)
	}
}

// TestReviewHoldStepBelowJobTimeout prevents a review barrier outlasting its job timeout.
func TestReviewHoldStepBelowJobTimeout(t *testing.T) {
	spec := loadPortabilityWorkflow(t)
	count := 0
	for id, job := range spec.Jobs {
		for _, step := range job.Steps {
			if step.ID != "review-hold" {
				continue
			}
			count++
			bound, err := strconv.Atoi(step.TimeoutMinutes.Value)
			jobBound, jobErr := strconv.Atoi(job.TimeoutMinutes.Value)
			if err != nil || jobErr != nil || bound >= jobBound {
				t.Errorf("%s: step timeout=%q job timeout=%q; want step timeout strictly below job timeout", id, step.TimeoutMinutes.Value, job.TimeoutMinutes.Value)
			}
		}
	}
	if count != 1 {
		t.Fatalf("review hold steps=%d; want review-gate only", count)
	}
}

// TestReviewHoldOpenThreadPlanted requires a review barrier before the heavy work can proceed.
// In the gate-job pattern, review-gate runs the barrier and heavy jobs read its result through
// a gate-check step that fails with the held annotation instead of silently skipping.
func TestReviewHoldOpenThreadPlanted(t *testing.T) {
	spec := loadPortabilityWorkflow(t)
	gate := spec.Jobs["review-gate"]
	if len(gate.Steps) < 2 || gate.Steps[1].ID != "review-hold" {
		t.Fatal("review-gate: review-hold step missing")
	}
	for _, id := range []string{"harness", "windows-shard"} {
		job := spec.Jobs[id]
		if !slices.Contains(job.NeedIDs(), "review-gate") {
			t.Errorf("%s: needs review-gate dependency missing", id)
		}
		found := false
		for _, step := range job.Steps {
			if step.ID == "review-check" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s: planted unresolved thread would reach heavy work: gate-check step missing", id)
		}
	}
}

// reviewFixtureStub replaces gh entirely; only jq and Bash execute real parsing.
const reviewFixtureStub = `#!/usr/bin/env bash
set -euo pipefail
MSYS2_ARG_CONV_EXCL='*' jq --binary -cn --args '$ARGS.positional' -- "$@" >> "$FIXTURE_DIR/calls"
if [[ "$1 $2" == 'run rerun' ]]; then
  printf '%s\n' "$3" >> "$FIXTURE_DIR/reruns"
  exit 0
fi
endpoint="$2"
if [[ "$endpoint" == --method ]]; then endpoint="$4"; fi
if [[ -f "$FIXTURE_DIR/fail-endpoint-$PR_NUMBER" && "$endpoint" == "$(cat "$FIXTURE_DIR/fail-endpoint-$PR_NUMBER")" ]]; then exit 1; fi
case "$endpoint" in
  graphql)
    if [[ -f "$FIXTURE_DIR/read-error" || -f "$FIXTURE_DIR/read-error-$PR_NUMBER" ]]; then exit 1; fi
    if [[ -f "$FIXTURE_DIR/threads-next.json" && "$*" == *cursor=next* ]]; then
      cat "$FIXTURE_DIR/threads-next.json"
    else cat "$FIXTURE_DIR/threads.json"; fi ;;
  */check-runs\?*)
    if [[ -f "$FIXTURE_DIR/checks-next.json" && "$*" == *page=2* ]]; then
      cat "$FIXTURE_DIR/checks-next.json"
    else cat "$FIXTURE_DIR/checks.json"; fi ;;
  */statuses) cat "$FIXTURE_DIR/statuses.json" ;;
  */pulls\?*) cat "$FIXTURE_DIR/prs.json" ;;
  */portability.yml/runs\?*) cat "$FIXTURE_DIR/runs.json" ;;
  */attempts/*/jobs) cat "$FIXTURE_DIR/jobs.json" ;;
  */annotations) cat "$FIXTURE_DIR/annotations.json" ;;
  */actions/runs/42) cat "$FIXTURE_DIR/current-run.json" ;;
  */pulls/[12]) cat "$FIXTURE_DIR/current-pr.json" ;;
  *) echo "unexpected stub call: $*" >&2; exit 2 ;;
esac
`

// reviewFixtures writes a complete clear review and held-run API fixture set.
func reviewFixtures(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(t.TempDir(), "fixture space*")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"threads.json":     `{"data":{"repository":{"pullRequest":{"headRefOid":"head","reviewThreads":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}}}`,
		"checks.json":      `{"check_runs":[{"name":"CodeRabbit","status":"completed","app":{"slug":"coderabbitai"}}]}`,
		"statuses.json":    `[]`,
		"prs.json":         `[{"number":1,"head":{"sha":"head","repo":{"full_name":"fixture/fork"}}}]`,
		"runs.json":        `{"workflow_runs":[{"id":42,"run_number":8,"run_attempt":1,"head_sha":"head","event":"pull_request","status":"completed","conclusion":"failure","pull_requests":[{"number":1}]}]}`,
		"jobs.json":        `{"jobs":[{"conclusion":"failure","check_run_url":"https://api.github.com/repos/fixture/repo/check-runs/7"}]}`,
		"annotations.json": `[{"title":"Gate held: review open","annotation_level":"failure"}]`,
		"current-run.json": `{"status":"completed","run_attempt":1}`,
		"current-pr.json":  `{"state":"open","head":{"sha":"head"}}`,
	}
	writeReviewStub(t, dir)
	for name, data := range files {
		writeReviewFixture(t, dir, name, data)
	}
	return dir
}

// writeReviewFixture writes only sandbox fixture data, never a credential location.
func writeReviewFixture(t *testing.T, dir, name, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeReviewStub installs the executable gh fixture stub in the sandbox.
func writeReviewStub(t *testing.T, dir string) {
	t.Helper()
	// nosemgrep: go_file-permissions_rule-fileperm, go.lang.correctness.permissions.file_permission.incorrect-default-permission -- the stub is executed through PATH and needs the owner execute bit
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(reviewFixtureStub), 0o700); err != nil {
		t.Fatal(err)
	}
}

// readReviewFixture reads a named fixture through its sandbox root.
func readReviewFixture(t *testing.T, dir, name string) ([]byte, error) {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	}()
	return root.ReadFile(name)
}

// assertReviewCalls compares recorded gh arguments with the expected call sequence.
func assertReviewCalls(t *testing.T, dir string, want [][]string) {
	t.Helper()
	data, err := readReviewFixture(t, dir, "calls")
	if err != nil {
		t.Fatal(err)
	}
	var got [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var args []string
		if err := json.Unmarshal([]byte(line), &args); err != nil {
			t.Fatal(err)
		}
		got = append(got, args)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("gh argv=%q; want %q", got, want)
	}
}

// reviewRESTCall builds the expected arguments for one paginated REST request.
func reviewRESTCall(endpoint string, page int) []string {
	return []string{"api", "--method", "GET", "repos/fixture/repo/" + endpoint, "-F", "per_page=100", "-F", "page=" + strconv.Itoa(page)}
}

// reviewCountCalls builds the expected thread, check-run and status request sequence.
func reviewCountCalls(number string, threadPages, checkPages int, statuses bool) [][]string {
	var calls [][]string
	for page := 1; page <= threadPages; page++ {
		cursor := "null"
		if page > 1 {
			cursor = "next"
		}
		calls = append(calls, []string{"api", "graphql", "-f", "query=" + ReviewHoldQuery,
			"-f", "owner=fixture", "-f", "name=repo", "-F", "number=" + number, "-F", "cursor=" + cursor})
	}
	for page := 1; page <= checkPages; page++ {
		calls = append(calls, reviewRESTCall("commits/head/check-runs?filter=latest", page))
	}
	if statuses {
		calls = append(calls, reviewRESTCall("commits/head/statuses", 1))
	}
	return calls
}

// reviewReleaseCalls describes each phase reached after listing open pull requests.
func reviewReleaseCalls(number string, phase int) [][]string {
	calls := [][]string{reviewRESTCall("actions/workflows/portability.yml/runs?event=pull_request&head_sha=head", 1)}
	if phase == 0 {
		return calls
	}
	calls = append(calls, reviewRESTCall("actions/runs/42/attempts/1/jobs", 1), reviewRESTCall("check-runs/7/annotations", 1))
	if phase == 1 {
		return calls
	}
	calls = append(calls, reviewCountCalls(number, 1, 1, true)...)
	if phase == 2 {
		return calls
	}
	calls = append(calls, []string{"api", "repos/fixture/repo/actions/runs/42"})
	if phase == 3 {
		return calls
	}
	calls = append(calls, []string{"api", "repos/fixture/repo/pulls/" + number})
	if phase == 4 {
		return calls
	}
	return append(calls, []string{"run", "rerun", "42", "--repo", "fixture/repo"})
}

// runReviewScript runs an LF file with shared relative paths, avoiding MSYS argv rewriting.
func runReviewScript(t *testing.T, dir, script string) (string, error) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("Bash absent: workflow fixture requires Bash")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq absent: workflow fixture requires jq")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Native Go -> MSYS -c arguments are globbed and truncated around 8 KiB.
	// Bash chooses its own PATH syntax; both runtimes share cmd.Dir for files.
	// All fixture callers, including annotation tests, share a deterministic clock.
	script = "review_fixture_seconds=0\n" + strings.ReplaceAll(reviewPollClock+script, "SECONDS", "review_fixture_seconds")
	writeReviewFixture(t, dir, "review-script.sh", "export PATH=\"$PWD:$PATH\"\n"+script)
	environment := append(os.Environ(),
		"FIXTURE_DIR=.", "GITHUB_REPOSITORY=fixture/repo", "PR_NUMBER=1", "HEAD_SHA=head",
		"RUNNER_TEMP=.", "GITHUB_STEP_SUMMARY=summary", "GITHUB_OUTPUT=outputs")
	ctx, err = util.WithCommandEnvironment(ctx, environment)
	if err != nil {
		t.Fatal(err)
	}
	out, err := util.RunCommandBytes(ctx, dir, bash, 1<<20, "review-script.sh")
	if err != nil {
		t.Logf("Bash script: %v; stdout=%s stderr=%s", err, out.Stdout, out.Stderr)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "review-hold.") {
			t.Errorf("temporary API directory retained after script exit: %s", entry.Name())
		}
	}
	return string(out.Stdout) + string(out.Stderr), err
}

// TestReviewHoldCounter covers open threads, bot checks/statuses, failures and bounds.
func TestReviewHoldCounter(t *testing.T) {
	cases := []struct{ name, file, data, reason string }{
		{"clear", "", "", ""},
		{"open thread", "threads.json", `{"data":{"repository":{"pullRequest":{"headRefOid":"head","reviewThreads":{"nodes":[{"isResolved":false}],"pageInfo":{"hasNextPage":false}}}}}}`, "unresolved threads=1"},
		{"pending CodeRabbit", "checks.json", `{"check_runs":[{"name":"CodeRabbit","status":"in_progress"}]}`, "pending bot reviews=1; bots=CodeRabbit"},
		{"pending app identity", "checks.json", `{"check_runs":[{"name":"Review","status":"queued","app":{"slug":"coderabbitai"}}]}`, "bots=Review"},
		{"pending Codacy", "checks.json", `{"check_runs":[{"name":"Codacy Static Code Analysis","status":"queued"}]}`, "bots=Codacy Static Code Analysis"},
		{"pending sourcery", "statuses.json", `[{"id":1,"context":"sourcery-ai","state":"pending"}]`, "bots=sourcery-ai"},
		{"latest status clears", "statuses.json", `[{"id":2,"context":"CodeRabbit","state":"success"},{"id":1,"context":"CodeRabbit","state":"pending"}]`, ""},
		{"other pending check", "checks.json", `{"check_runs":[{"name":"Build","status":"queued"}]}`, ""},
		{"read failure", "read-error", "", "thread read failed"},
		{"GraphQL errors", "threads.json", `{"errors":[{"message":"denied"}]}`, "thread read failed"},
		{"null response", "threads.json", `{"data":{"repository":{"pullRequest":null}}}`, "thread read failed"},
		{"malformed checks", "checks.json", `{"message":"denied"}`, "check-run read failed"},
		{"thread bound", "threads.json", `{"data":{"repository":{"pullRequest":{"headRefOid":"head","reviewThreads":{"nodes":[],"pageInfo":{"hasNextPage":true,"endCursor":"next"}}}}}}`, "pagination bound"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := reviewFixtures(t)
			if tc.file != "" {
				writeReviewFixture(t, dir, tc.file, tc.data)
			}
			out, err := runReviewScript(t, dir, reviewCounterScript)
			threadPages, checkPages, statuses := 1, 1, true
			switch tc.name {
			case "read failure", "GraphQL errors", "null response":
				checkPages, statuses = 0, false
			case "thread bound":
				threadPages, checkPages, statuses = 20, 0, false
			case "malformed checks":
				statuses = false
			}
			assertReviewCalls(t, dir, reviewCountCalls("1", threadPages, checkPages, statuses))
			if tc.reason != "" {
				if err == nil || !strings.Contains(out, tc.reason) || !strings.Contains(out, "::error title=Gate held%3A review open::") {
					t.Fatalf("out=%s err=%v; want hold %s", out, err, tc.reason)
				}
			} else if err != nil {
				t.Fatalf("out=%s err=%v", out, err)
			}
		})
	}
}

// TestReviewHoldRelease covers release, retained holds and unrelated failures.
func TestReviewHoldRelease(t *testing.T) {
	cases := []struct {
		name, file, data string
		rerun            bool
		phase            int
	}{
		{"held now clear", "", "", true, 5},
		{"fork head matches", "runs.json", reviewForkRun("fixture/fork", "head"), true, 5},
		{"fork repository differs", "runs.json", reviewForkRun("other/fork", "head"), false, 0},
		{"fork sha differs", "runs.json", reviewForkRun("fixture/fork", "other-head"), false, 0},
		{"fork repository missing", "runs.json", reviewForkRun("", "head"), false, 0},
		{"fork association differs", "runs.json", strings.Replace(reviewForkRun("fixture/fork", "head"), `"pull_requests":[]`, `"pull_requests":[{"number":2}]`, 1), false, 0},
		{"newest run queued", "runs.json", `{"workflow_runs":[{"id":42,"run_number":8,"run_attempt":1,"head_sha":"head","event":"pull_request","status":"completed","conclusion":"failure","pull_requests":[{"number":1}]},{"id":43,"run_number":9,"head_sha":"head","event":"pull_request","status":"queued","pull_requests":[{"number":1}]}]}`, false, 0},
		{"still open", "threads.json", `{"data":{"repository":{"pullRequest":{"headRefOid":"head","reviewThreads":{"nodes":[{"isResolved":false}],"pageInfo":{"hasNextPage":false}}}}}}`, false, 2},
		{"still pending bot", "checks.json", `{"check_runs":[{"name":"CodeRabbit","status":"queued"}]}`, false, 2},
		{"unrelated failure", "annotations.json", `[{"title":"test failed","annotation_level":"failure"}]`, false, 1},
		{"rerun already started", "current-run.json", `{"status":"queued","run_attempt":2}`, false, 3},
		{"latest attempt changed", "current-run.json", `{"status":"completed","run_attempt":2}`, false, 3},
		{"head moved", "current-pr.json", `{"state":"open","head":{"sha":"new-head"}}`, false, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := reviewFixtures(t)
			if tc.file != "" {
				writeReviewFixture(t, dir, tc.file, tc.data)
			}
			out, err := runReviewScript(t, dir, reviewHoldReleaseScript)
			want := append([][]string{reviewRESTCall("pulls?state=open&sort=created&direction=asc", 1)}, reviewReleaseCalls("1", tc.phase)...)
			assertReviewCalls(t, dir, want)
			if err != nil {
				t.Fatalf("out=%s err=%v", out, err)
			}
			data, err := readReviewFixture(t, dir, "reruns")
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if (string(data) == "42\n") != tc.rerun {
				t.Fatalf("reruns=%q want %t; %s", data, tc.rerun, out)
			}
		})
	}
}

// reviewForkRun builds a failed fork run without a pull-request association.
func reviewForkRun(repository, sha string) string {
	return `{"workflow_runs":[{"id":42,"run_number":8,"run_attempt":1,"head_sha":"` + sha +
		`","head_repository":{"full_name":"` + repository +
		`"},"event":"pull_request","status":"completed","conclusion":"failure","pull_requests":[]}]}`
}

// writeLargeReviewFixtures plants API pages exceeding a single argument's byte limit.
func writeLargeReviewFixtures(t *testing.T, dir string) {
	t.Helper()
	padding := strings.Repeat("x", 1536)
	check := `{"name":"Build","status":"completed","app":{"slug":"build","description":"` + padding + `"}}`
	writeReviewFixture(t, dir, "checks.json", `{"check_runs":[`+strings.Repeat(check+",", 99)+check+`]}`)
	writeReviewFixture(t, dir, "checks-next.json", `{"check_runs":[]}`)
	status := `{"id":1,"context":"Build","state":"success","description":"` + padding + `"}`
	writeReviewFixture(t, dir, "statuses.json", `[`+strings.Repeat(status+",", 98)+status+`]`)
}

// TestReviewHoldLargePayload counts oversized API pages without command-line data.
func TestReviewHoldLargePayload(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(strconv.FormatBool(pending), func(t *testing.T) {
			dir := reviewFixtures(t)
			writeLargeReviewFixtures(t, dir)
			if pending {
				writeReviewFixture(t, dir, "checks-next.json", `{"check_runs":[{"name":"CodeRabbit","status":"queued"}]}`)
			}
			out, err := runReviewScript(t, dir, reviewCounterScript)
			assertReviewCalls(t, dir, reviewCountCalls("1", 1, 2, true))
			summary, readErr := readReviewFixture(t, dir, "summary")
			want := "unresolved threads=0; pending bot reviews=0; bots="
			if pending {
				want = "unresolved threads=0; pending bot reviews=1; bots=CodeRabbit"
			}
			if readErr != nil || (err != nil) != pending || !strings.Contains(string(summary), want) || strings.Contains(out, "Argument list too long") {
				t.Fatalf("summary=%s out=%s err=%v read=%v; want %s", summary, out, err, readErr, want)
			}
		})
	}
}

// TestReviewHoldReleaseLargePayload releases a held run with oversized API responses.
func TestReviewHoldReleaseLargePayload(t *testing.T) {
	dir := reviewFixtures(t)
	writeLargeReviewFixtures(t, dir)
	padding := `"padding":"` + strings.Repeat("x", 160<<10) + `",`
	for _, name := range []string{"threads.json", "prs.json", "runs.json", "jobs.json", "annotations.json", "current-run.json", "current-pr.json"} {
		data, err := readReviewFixture(t, dir, name)
		if err != nil {
			t.Fatal(err)
		}
		writeReviewFixture(t, dir, name, strings.Replace(string(data), "{", "{"+padding, 1))
	}
	out, err := runReviewScript(t, dir, reviewHoldReleaseScript)
	want := [][]string{reviewRESTCall("pulls?state=open&sort=created&direction=asc", 1)}
	want = append(want, reviewReleaseCalls("1", 1)...)
	want = append(want, reviewCountCalls("1", 1, 2, true)...)
	want = append(want, reviewReleaseCalls("1", 5)[6:]...)
	assertReviewCalls(t, dir, want)
	reruns, readErr := readReviewFixture(t, dir, "reruns")
	if err != nil || readErr != nil || string(reruns) != "42\n" || strings.Contains(out, "::error") {
		t.Fatalf("reruns=%s out=%s err=%v read=%v", reruns, out, err, readErr)
	}
}

// TestReviewHoldRendering keeps the committed copies identical to their renderer.
func TestReviewHoldRendering(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/portability.yml")
	if err != nil {
		t.Fatal(err)
	}
	data = updateReviewHoldPortability(t, data)
	if len(data) > 64<<10 {
		t.Fatal("portability workflow exceeds metadata reader byte bound")
	}
	if strings.Count(string(data), RenderReviewHoldStep(false)) != 1 {
		t.Fatal("portability review-gate step differs from renderer")
	}
	spec, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	gate := spec.Jobs["review-gate"]
	if len(gate.Steps) < 2 || gate.Steps[1].ID != "review-hold" || gate.Steps[1].Run != reviewHoldScript {
		t.Fatal("review-gate: barrier step shape differs from renderer")
	}
	assertGateLegShape(t, spec, "harness")
	assertGateLegShape(t, spec, "windows-shard")
	winJob := spec.Jobs["windows"]
	if slices.Contains(winJob.NeedIDs(), "review-gate") {
		t.Error("windows: must not directly need review-gate (transitive through windows-shard)")
	}
	assertReleaseWorkflowRendered(t)
}

// assertReleaseWorkflowRendered verifies the release workflow matches its renderer.
func assertReleaseWorkflowRendered(t *testing.T) {
	t.Helper()
	wantRelease := RenderReviewHoldReleaseWorkflow()
	if os.Getenv(updateReviewHoldWorkflowEnv) == "1" {
		if err := os.WriteFile("../../.github/workflows/review-hold-release.yml", []byte(wantRelease), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	release, err := os.ReadFile("../../.github/workflows/review-hold-release.yml")
	if err != nil {
		t.Fatal(err)
	}
	if string(release) != wantRelease {
		t.Fatal("release workflow differs from renderer")
	}
}

// updateReviewHoldPortability regenerates only the rendered barrier step when explicitly requested.
func updateReviewHoldPortability(t *testing.T, data []byte) []byte {
	t.Helper()
	if os.Getenv(updateReviewHoldWorkflowEnv) != "1" {
		return data
	}
	pattern := regexp.MustCompile(`(?m)^      - name: Stop while review is open\n(?:        .*\n)*`)
	if matches := pattern.FindAll(data, -1); len(matches) != 1 {
		t.Fatalf("rendered barrier steps=%d; want 1 (review-gate only)", len(matches))
	}
	updated := pattern.ReplaceAllString(string(data), RenderReviewHoldStep(false))
	if err := os.WriteFile("../../.github/workflows/portability.yml", []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	return []byte(updated)
}

// TestReviewHoldStepTimeoutRendered verifies RenderReviewHoldStep emits timeout-minutes.
func TestReviewHoldStepTimeoutRendered(t *testing.T) {
	want := "        timeout-minutes: " + strconv.Itoa(ReviewHoldStepTimeoutMinutes) + "\n"
	if !strings.Contains(RenderReviewHoldStep(false), want) {
		t.Fatalf("RenderReviewHoldStep(false) does not contain %q", want)
	}
	if !strings.Contains(RenderReviewHoldStep(true), want) {
		t.Fatalf("RenderReviewHoldStep(true) does not contain %q", want)
	}
}

// assertGateLegShape checks that a gate-job leg needs review-gate and has a gate-check step.
func assertGateLegShape(t *testing.T, spec Spec, id string) {
	t.Helper()
	job := spec.Jobs[id]
	if !slices.Contains(job.NeedIDs(), "review-gate") {
		t.Errorf("%s: needs review-gate dependency missing", id)
	}
	found := false
	for _, step := range job.Steps {
		if step.ID == "review-check" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("%s: gate-check step missing", id)
	}
}

// TestReviewHoldPagination exercises actual REST and GraphQL continuation and exhaustion.
func TestReviewHoldPagination(t *testing.T) {
	fullChecks := `{"check_runs":[` + strings.Repeat(`{"name":"Build","status":"completed"},`, 99) + `{"name":"Build","status":"completed"}]}`
	cases := []struct{ name, first, next, reason string }{
		{"REST bound", fullChecks, "", "pagination bound reached"},
		{"REST second page", fullChecks, `{"check_runs":[{"name":"CodeRabbit","status":"queued"}]}`, "bots=CodeRabbit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := reviewFixtures(t)
			writeReviewFixture(t, dir, "checks.json", tc.first)
			if tc.next != "" {
				writeReviewFixture(t, dir, "checks-next.json", tc.next)
			}
			out, err := runReviewScript(t, dir, reviewCounterScript)
			pages, statuses := 20, false
			if tc.next != "" {
				pages, statuses = 2, true
			}
			assertReviewCalls(t, dir, reviewCountCalls("1", 1, pages, statuses))
			if err == nil || !strings.Contains(out, tc.reason) {
				t.Fatalf("out=%s err=%v want %s", out, err, tc.reason)
			}
		})
	}
	dir := reviewFixtures(t)
	writeReviewFixture(t, dir, "threads.json", `{"data":{"repository":{"pullRequest":{"headRefOid":"head","reviewThreads":{"nodes":[],"pageInfo":{"hasNextPage":true,"endCursor":"next"}}}}}}`)
	writeReviewFixture(t, dir, "threads-next.json", `{"data":{"repository":{"pullRequest":{"headRefOid":"head","reviewThreads":{"nodes":[{"isResolved":false}],"pageInfo":{"hasNextPage":false}}}}}}`)
	out, err := runReviewScript(t, dir, reviewCounterScript)
	assertReviewCalls(t, dir, reviewCountCalls("1", 2, 1, true))
	if err == nil || !strings.Contains(out, "unresolved threads=1") {
		t.Fatalf("out=%s err=%v", out, err)
	}
}

// TestReviewHoldReleaseReadFailure leaves every run untouched on a failed recount.
func TestReviewHoldReleaseReadFailure(t *testing.T) {
	dir := reviewFixtures(t)
	writeReviewFixture(t, dir, "read-error", "")
	out, err := runReviewScript(t, dir, reviewHoldReleaseScript)
	want := append([][]string{reviewRESTCall("pulls?state=open&sort=created&direction=asc", 1)}, reviewReleaseCalls("1", 1)...)
	want = append(want, reviewCountCalls("1", 1, 0, false)...)
	assertReviewCalls(t, dir, want)
	if err == nil || !strings.Contains(out, "thread read failed") {
		t.Fatalf("out=%s err=%v", out, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "reruns")); !os.IsNotExist(err) {
		t.Fatalf("unexpected rerun file: %v", err)
	}
}

// TestReviewHoldClearOutput proves a cleared review explicitly enables the later heavy steps.
func TestReviewHoldClearOutput(t *testing.T) {
	dir := reviewFixtures(t)
	out, err := runReviewScript(t, dir, reviewHoldScript)
	assertReviewCalls(t, dir, reviewCountCalls("1", 1, 1, true))
	if err != nil {
		t.Fatalf("out=%s err=%v", out, err)
	}
	data, err := readReviewFixture(t, dir, "outputs")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "clear=true\n" {
		t.Fatalf("clear output=%q", data)
	}
}

// TestReviewHoldReleaseIsolatesPRReadFailure plants a failed read before a clear held run.
func TestReviewHoldReleaseIsolatesPRReadFailure(t *testing.T) {
	cases := []struct {
		name, endpoint, cause string
		calls                 int
	}{
		{"runs", "repos/fixture/repo/actions/workflows/portability.yml/runs?event=pull_request&head_sha=head", "workflow run read failed", 1},
		{"jobs", "repos/fixture/repo/actions/runs/42/attempts/1/jobs", "job read failed", 2},
		{"annotations", "repos/fixture/repo/check-runs/7/annotations", "annotation read failed", 3},
		{"threads", "graphql", "thread read failed", 4},
		{"checks", "repos/fixture/repo/commits/head/check-runs?filter=latest", "check-run read failed", 5},
		{"statuses", "repos/fixture/repo/commits/head/statuses", "commit-status read failed", 6},
		{"current run", "repos/fixture/repo/actions/runs/42", "run revalidation failed", 7},
		{"current head", "repos/fixture/repo/pulls/1", "head revalidation failed", 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := reviewFixtures(t)
			writeReviewFixture(t, dir, "prs.json", `[{"number":1,"head":{"sha":"head"}},{"number":2,"head":{"sha":"head"}}]`)
			writeReviewFixture(t, dir, "runs.json", `{"workflow_runs":[{"id":42,"run_number":8,"run_attempt":1,"head_sha":"head","event":"pull_request","status":"completed","conclusion":"failure","pull_requests":[{"number":1},{"number":2}]}]}`)
			writeReviewFixture(t, dir, "fail-endpoint-1", tc.endpoint)
			out, err := runReviewScript(t, dir, reviewHoldReleaseScript)
			want := append([][]string{reviewRESTCall("pulls?state=open&sort=created&direction=asc", 1)}, reviewReleaseCalls("1", 5)[:tc.calls]...)
			want = append(want, reviewReleaseCalls("2", 5)...)
			assertReviewCalls(t, dir, want)
			if err == nil || !strings.Contains(out, "::error title=Review hold release read failed::pull request 1:") || !strings.Contains(out, tc.cause) {
				t.Errorf("out=%s err=%v; want PR 1 read failure: %s", out, err, tc.cause)
			}
			data, err := readReviewFixture(t, dir, "reruns")
			if err != nil || string(data) != "42\n" {
				t.Fatalf("reruns=%q err=%v; want only PR 2 released; %s", data, err, out)
			}
		})
	}
}

// TestReviewHoldReleaseTriggersAndCondition verifies the release workflow declares
// check_run and status triggers and restricts runs to canonical repo and configured bots.
func TestReviewHoldReleaseTriggersAndCondition(t *testing.T) {
	rendered := RenderReviewHoldReleaseWorkflow()
	spec, err := Parse([]byte(rendered))
	if err != nil {
		t.Fatalf("parse rendered release workflow: %v", err)
	}
	assertReviewReleaseTriggers(t, spec)
	releaseJob, ok := spec.Jobs["release"]
	if !ok {
		t.Fatal("rendered release workflow missing 'release' job")
	}
	assertReviewReleaseConditionGuards(t, releaseJob.If)
	assertReviewReleaseConditionBots(t, releaseJob.If)
}

// assertReviewReleaseTriggers verifies the check_run and status event declarations.
func assertReviewReleaseTriggers(t *testing.T, spec Spec) {
	t.Helper()
	var on struct {
		CheckRun struct {
			Types []string `yaml:"types"`
		} `yaml:"check_run"`
	}
	if err := spec.On.Decode(&on); err != nil {
		t.Fatalf("decode on node: %v", err)
	}

	hasCheckRun := false
	hasStatus := false
	for i := 0; i < len(spec.On.Content); i += 2 {
		switch spec.On.Content[i].Value {
		case "check_run":
			hasCheckRun = true
		case "status":
			hasStatus = true
		}
	}

	if !hasCheckRun {
		t.Errorf("rendered release workflow missing 'check_run' trigger")
	}
	if !slices.Equal(on.CheckRun.Types, []string{"completed"}) {
		t.Errorf("check_run types = %v, want [completed]", on.CheckRun.Types)
	}
	if !hasStatus {
		t.Errorf("rendered release workflow missing 'status' trigger")
	}
}

// assertReviewReleaseConditionGuards verifies repository, event and pending guards.
func assertReviewReleaseConditionGuards(t *testing.T, condition string) {
	t.Helper()
	cases := []struct{ term, guard string }{
		{"github.repository == (vars.PRAETOR_CANONICAL_REPOSITORY || 'cordanaLLM/praetor')", "canonical repository guard"},
		{"github.event_name != 'check_run'", "check_run disjunction"},
		{"github.event_name != 'status'", "status disjunction"},
		{"github.event.state != 'pending'", "status pending check"},
	}
	for _, tc := range cases {
		if !strings.Contains(condition, tc.term) {
			t.Errorf("release job if condition missing %s: %q", tc.guard, condition)
		}
	}
}

// assertReviewReleaseConditionBots verifies every configured bot identity term.
func assertReviewReleaseConditionBots(t *testing.T, condition string) {
	t.Helper()
	cases := []struct{ field, label string }{
		{"github.event.check_run.name", "check_run name"},
		{"github.event.check_run.app.slug", "check_run app slug"},
		{"github.event.context", "status context"},
		{"github.event.sender.login", "status sender"},
	}
	bots := strings.Split(ReviewHoldBots, "|")
	for _, bot := range bots {
		for _, tc := range cases {
			if !strings.Contains(condition, "contains("+tc.field+", '"+bot+"')") {
				t.Errorf("condition missing %s match for bot %q", tc.label, bot)
			}
		}
	}
}

// TestReviewHoldReleaseConditionAllowedFunctions rejects unsupported expression calls.
func TestReviewHoldReleaseConditionAllowedFunctions(t *testing.T) {
	spec, err := Parse([]byte(RenderReviewHoldReleaseWorkflow()))
	if err != nil {
		t.Fatalf("parse rendered release workflow: %v", err)
	}
	// https://docs.github.com/en/actions/reference/workflows-and-actions/expressions#functions
	allowed := []string{
		"always", "cancelled", "case", "contains", "endsWith", "failure", "format",
		"fromJSON", "hashFiles", "join", "startsWith", "success", "toJSON",
	}
	// Ignore quoted literals, including escaped single quotes, before finding calls.
	literals := regexp.MustCompile(`'(?:[^']|'')*'`)
	condition := literals.ReplaceAllString(spec.Jobs["release"].If, "''")
	calls := regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	matches := calls.FindAllStringSubmatch(condition, -1)
	if len(matches) == 0 {
		t.Fatal("rendered release condition contains no function calls")
	}
	for _, match := range matches {
		if !slices.Contains(allowed, match[1]) {
			t.Errorf("rendered release condition calls unsupported GitHub expression function %q", match[1])
		}
	}
}

// TestReviewHoldReleasePendingThenCompletedBot proves an in-progress bot check run
// does not trigger a rerun, and completing that check run releases the held run.
func TestReviewHoldReleasePendingThenCompletedBot(t *testing.T) {
	dir := reviewFixtures(t)
	pendingCheck := `{"check_runs":[{"name":"Codacy Static Code Analysis","status":"in_progress","app":{"slug":"codacy-production"}}]}`
	writeReviewFixture(t, dir, "checks.json", pendingCheck)

	out, err := runReviewScript(t, dir, reviewHoldReleaseScript)
	if err != nil {
		t.Fatalf("first run failed: %v\noutput: %s", err, out)
	}
	reruns, err := readReviewFixture(t, dir, "reruns")
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(reruns) > 0 {
		t.Fatalf("unexpected rerun while bot pending: %q; output: %s", reruns, out)
	}

	completedCheck := `{"check_runs":[{"name":"Codacy Static Code Analysis","status":"completed","conclusion":"success","app":{"slug":"codacy-production"}}]}`
	writeReviewFixture(t, dir, "checks.json", completedCheck)

	out, err = runReviewScript(t, dir, reviewHoldReleaseScript)
	if err != nil {
		t.Fatalf("second run failed: %v\noutput: %s", err, out)
	}
	reruns, err = readReviewFixture(t, dir, "reruns")
	if err != nil {
		t.Fatalf("expected rerun file missing: %v", err)
	}
	if string(reruns) != "42\n" {
		t.Fatalf("reruns = %q, want %q; output: %s", reruns, "42\n", out)
	}
}

// TestReviewHoldReleaseJobConditionBoundary asserts that non-bot check runs and pending
// commit statuses do not satisfy the rendered job condition structure.
func TestReviewHoldReleaseJobConditionBoundary(t *testing.T) {
	rendered := RenderReviewHoldReleaseWorkflow()
	spec, err := Parse([]byte(rendered))
	if err != nil {
		t.Fatalf("parse rendered release workflow: %v", err)
	}
	condition := spec.Jobs["release"].If
	conjuncts := reviewReleaseConditionConjuncts(t, condition)
	canonicalConjunct := conjuncts[0]
	if canonicalConjunct != "github.repository == (vars.PRAETOR_CANONICAL_REPOSITORY || 'cordanaLLM/praetor')" {
		t.Errorf("unexpected canonical conjunct: %s", canonicalConjunct)
	}
	assertReviewReleaseCheckRunBoundary(t, conjuncts[1])
	assertReviewReleaseStatusBoundary(t, conjuncts[2])
}

// reviewReleaseConditionConjuncts requires the rendered condition's three conjuncts.
func reviewReleaseConditionConjuncts(t *testing.T, condition string) []string {
	t.Helper()
	var conjuncts []string
	prefix := "github.repository == (vars.PRAETOR_CANONICAL_REPOSITORY || 'cordanaLLM/praetor') && "
	if strings.HasPrefix(condition, prefix) {
		conjuncts = append(conjuncts, "github.repository == (vars.PRAETOR_CANONICAL_REPOSITORY || 'cordanaLLM/praetor')")
		rest := strings.TrimPrefix(condition, prefix)
		sep := " && (github.event_name != 'status'"
		idx := strings.Index(rest, sep)
		if idx >= 0 {
			conjuncts = append(conjuncts, rest[:idx])
			conjuncts = append(conjuncts, rest[idx+4:])
		}
	}

	if len(conjuncts) != 3 {
		t.Fatalf("condition expected 3 top-level conjuncts, got %d in: %s", len(conjuncts), condition)
	}
	return conjuncts
}

// assertReviewReleaseCheckRunBoundary verifies bot and non-bot check_run cases.
func assertReviewReleaseCheckRunBoundary(t *testing.T, checkRunConjunct string) {
	t.Helper()
	if !strings.HasPrefix(checkRunConjunct, "(github.event_name != 'check_run' || (") || !strings.HasSuffix(checkRunConjunct, "))") {
		t.Fatalf("malformed check_run conjunct: %s", checkRunConjunct)
	}
	checkRunBotTerms := strings.TrimPrefix(checkRunConjunct, "(github.event_name != 'check_run' || (")
	checkRunBotTerms = strings.TrimSuffix(checkRunBotTerms, "))")

	cases := []struct {
		name, appSlug, failure string
		want                   bool
	}{
		{"Unit Tests", "github-actions", "boundary check_run for non-bot app unexpectedly passed", false},
		{"Coverage", "codecov", "boundary check_run for non-bot app unexpectedly passed", false},
		{"Codacy Static Code Analysis", "codacy-production", "expected review bot check_run to pass", true},
	}
	for _, tc := range cases {
		got := reviewBotTermsMatch(checkRunBotTerms, map[string]string{
			"github.event.check_run.name": tc.name, "github.event.check_run.app.slug": tc.appSlug,
		})
		if got != tc.want {
			t.Error(tc.failure)
		}
	}
}

// assertReviewReleaseStatusBoundary verifies pending, non-bot and completed statuses.
func assertReviewReleaseStatusBoundary(t *testing.T, statusConjunct string) {
	t.Helper()
	if !strings.HasPrefix(statusConjunct, "(github.event_name != 'status' || (github.event.state != 'pending' && (") || !strings.HasSuffix(statusConjunct, ")))") {
		t.Fatalf("malformed status conjunct: %s", statusConjunct)
	}
	statusBotTerms := strings.TrimPrefix(statusConjunct, "(github.event_name != 'status' || (github.event.state != 'pending' && (")
	statusBotTerms = strings.TrimSuffix(statusBotTerms, ")))")

	cases := []struct {
		state, context, sender, failure string
		want                            bool
	}{
		{"pending", "codacy", "codacy-bot", "boundary pending status unexpectedly passed", false},
		{"pending", "sourcery-ai", "sourcery-ai[bot]", "boundary pending status unexpectedly passed", false},
		{"success", "ci/circleci", "circleci", "boundary non-bot status unexpectedly passed", false},
		{"success", "codacy/pr", "codacy-bot", "expected review bot completed status to pass", true},
	}
	for _, tc := range cases {
		got := tc.state != "pending" && reviewBotTermsMatch(statusBotTerms, map[string]string{
			"github.event.context": tc.context, "github.event.sender.login": tc.sender,
		})
		if got != tc.want {
			t.Error(tc.failure)
		}
	}
}

// TestReviewHoldConditionIncludesPullRequestReview proves the review hold
// barrier fires on pull_request_review events (submitted, dismissed) so a
// resolved thread or dismissed review triggers a fresh evaluation.
func TestReviewHoldConditionIncludesPullRequestReview(t *testing.T) {
	if !strings.Contains(ReviewHoldCondition, "pull_request_review") {
		t.Fatal("ReviewHoldCondition must include pull_request_review")
	}
	if !strings.Contains(ReviewHoldClear, "pull_request_review") {
		t.Fatal("ReviewHoldClear must exclude pull_request_review from the pass-through")
	}
	if !strings.Contains(HostedGateLegReady, "pull_request_review") {
		t.Fatal("HostedGateLegReady inherits ReviewHoldClear; pull_request_review missing")
	}
}

// TestReviewHoldGateJobConstants proves the gate-job pattern constants exist
// and carry the review-check step outcome guard.
func TestReviewHoldGateJobConstants(t *testing.T) {
	if !strings.Contains(ReviewHoldGateCheck, "review-check") {
		t.Fatal("ReviewHoldGateCheck must reference the review-check step")
	}
	if !strings.Contains(HostedGateGateLegReady, ReviewHoldGateCheck) {
		t.Fatal("HostedGateGateLegReady must include ReviewHoldGateCheck")
	}
	if !strings.Contains(HostedGateGateLegReady, "!cancelled()") {
		t.Fatal("HostedGateGateLegReady must include !cancelled()")
	}
	if !strings.Contains(HostedGateGateLegReady, HostedGateNotDraft) {
		t.Fatal("HostedGateGateLegReady must include HostedGateNotDraft")
	}
}

// reviewBotTermsMatch evaluates configured bot terms against the supplied event fields.
func reviewBotTermsMatch(terms string, fields map[string]string) bool {
	for field, value := range fields {
		lowerValue := strings.ToLower(value)
		for _, term := range strings.Split(terms, " || ") {
			for _, bot := range strings.Split(ReviewHoldBots, "|") {
				if term == "contains("+field+", '"+bot+"')" && strings.Contains(lowerValue, bot) {
					return true
				}
			}
		}
	}
	return false
}
