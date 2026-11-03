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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// TestReviewHoldOpenThreadPlanted requires a review barrier before the heavy work can proceed.
func TestReviewHoldOpenThreadPlanted(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/portability.yml")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"harness", "windows-shard"} {
		steps := spec.Jobs[id].Steps
		if len(steps) < 2 || steps[1].Name != "Stop while review is open" {
			t.Errorf("%s: planted unresolved thread would reach heavy work: review-hold step missing", id)
		}
	}
}

// reviewFixtureStub replaces gh entirely; only jq and Bash execute real parsing.
const reviewFixtureStub = `#!/usr/bin/env bash
set -euo pipefail
jq -cn --args '$ARGS.positional' -- "$@" >> "$FIXTURE_DIR/calls"
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
	dir := t.TempDir()
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

// runReviewScript runs the rendered Bash with a gh stub and the real installed jq.
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
	environment := append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FIXTURE_DIR="+dir, "GITHUB_REPOSITORY=fixture/repo", "PR_NUMBER=1", "HEAD_SHA=head",
		"RUNNER_TEMP="+dir,
		"GITHUB_STEP_SUMMARY="+filepath.Join(dir, "summary"), "GITHUB_OUTPUT="+filepath.Join(dir, "outputs"))
	ctx, err = util.WithCommandEnvironment(ctx, environment)
	if err != nil {
		t.Fatal(err)
	}
	out, err := util.RunCommandBytes(ctx, dir, bash, 1<<20, "-c", script)
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
			out, err := runReviewScript(t, dir, reviewHoldScript)
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
			out, err := runReviewScript(t, dir, reviewHoldScript)
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
	if len(data) > 64<<10 {
		t.Fatal("portability workflow exceeds metadata reader byte bound")
	}
	text := string(data)
	if strings.Count(text, RenderReviewHoldStep(false)) != 1 || strings.Count(text, RenderReviewHoldStep(true)) != 2 {
		t.Fatal("portability review steps differ from renderer")
	}
	spec, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"harness", "windows-shard"} {
		assertReviewJob(t, id, spec.Jobs[id])
	}
	assertAggregateReviewHold(t, spec.Jobs["windows"])
	release, err := os.ReadFile("../../.github/workflows/review-hold-release.yml")
	if err != nil {
		t.Fatal(err)
	}
	if string(release) != RenderReviewHoldReleaseWorkflow() {
		t.Fatal("release workflow differs from renderer")
	}
}

// assertAggregateReviewHold verifies the shared barrier precedes the aggregate's result check.
func assertAggregateReviewHold(t *testing.T, job Job) {
	t.Helper()
	if len(job.Steps) != 2 || job.Steps[0].Run != reviewHoldScript || job.Steps[0].If != ReviewHoldCondition {
		t.Fatal("windows aggregate review barrier differs from renderer")
	}
}

// assertReviewJob verifies a rendered barrier and every subsequent step condition.
func assertReviewJob(t *testing.T, id string, job Job) {
	t.Helper()
	if len(job.Steps) < 3 {
		t.Fatalf("%s: review steps missing", id)
	}
	hold := job.Steps[1]
	if hold.If != ReviewHoldCondition || hold.Shell != "bash" || hold.Run != reviewHoldScript {
		t.Fatalf("%s: review barrier shape differs", id)
	}
	for _, step := range job.Steps[2:] {
		if !strings.Contains(step.If, ReviewHoldClear) || !strings.Contains(step.If, HostedGateNotDraft) {
			t.Errorf("%s/%s: missing review/draft guard", id, step.Name)
		}
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
			out, err := runReviewScript(t, dir, reviewHoldScript)
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
	out, err := runReviewScript(t, dir, reviewHoldScript)
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
