// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package secrets_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"

	"github.com/cordanaLLM/praetor/tools/secrets"
	"github.com/cordanaLLM/praetor/tools/secrets/rules"
)

// runGateCLI runs the gate in dir with the given SECRETS_EXCEPTIONS and no GitHub event, returning output and exit code.
func runGateCLI(t *testing.T, dir string, envExceptions string, args ...string) (string, int) {
	t.Helper()
	return runGateCLIEnvironment(t, dir, envExceptions, nil, args...)
}

// runGateCLIEnvironment runs the gate in dir with extra environment variables and arguments, returning output and exit code.
func runGateCLIEnvironment(t *testing.T, dir, envExceptions string, eventEnv []string, args ...string) (string, int) {
	t.Helper()
	env := append(os.Environ(), "GITHUB_EVENT_NAME=", "GITHUB_EVENT_PATH=", "SECRETS_EXCEPTIONS="+envExceptions)
	ctx, err := util.WithCommandEnvironment(t.Context(), append(env, eventEnv...))
	if err != nil {
		t.Fatal(err)
	}
	cmdArgs := append([]string{"run", "-p", "2", "-tags=secretsgate", "./tools/secrets/gate", "-path=" + dir}, args...)
	res, err := util.RunCommandBytes(ctx, filepath.Join("..", ".."), "go", util.MaxCommandOutputBytes, cmdArgs...)
	out := append(res.Stdout, res.Stderr...)
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
			if exitCode == 1 && strings.Contains(string(out), "exit status 3") {
				exitCode = 3
			} else if exitCode == 1 && strings.Contains(string(out), "exit status 2") {
				exitCode = 2
			}
		} else {
			t.Fatalf("run gate command failed: %v", err)
		}
	}
	return string(out), exitCode
}

// gateEventFile writes a pull_request event payload with the given base and head shas and returns its path.
func gateEventFile(t *testing.T, data string) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "event.json")
	if err := os.WriteFile(name, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return name
}

// gatePullRequestEnvironment returns the GitHub variables of a pull_request event whose payload is at path.
func gatePullRequestEnvironment(t *testing.T, base, head string) []string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"pull_request": map[string]any{
		"base": map[string]string{"sha": base}, "head": map[string]string{"sha": head},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return []string{"GITHUB_EVENT_NAME=pull_request", "GITHUB_EVENT_PATH=" + gateEventFile(t, string(data))}
}

// A pull_request event scans only base..head: an older secret passes, a secret inside the range fails, a push event still scans full history.
func TestGate_PullRequestEvent_Planted_3D(t *testing.T) {
	if _, err := exec.LookPath("gitleaks"); err != nil {
		t.Skip("gitleaks not installed on PATH")
	}
	dir, _ := secrets.BuildOrdinaryLeakRepo(t)
	base := commitGateFile(t, dir, "creds.py", "clean = 1\n")
	head := commitGateFile(t, dir, "README.md", "clean pull request\n")
	env := gatePullRequestEnvironment(t, base, head)
	want := "secrets gate: scanning pull request range " + base[:12] + ".." + head[:12] + " within 5m0s"
	if out, code := runGateCLIEnvironment(t, dir, "", env); code != 0 || !strings.Contains(out, want) {
		t.Fatalf("clean event range after historical secret: exit %d, want 0 and %q: %s", code, want, out)
	}
	pushEnv := append(append([]string{}, env...), "GITHUB_EVENT_NAME=push")
	if out, code := runGateCLIEnvironment(t, dir, "", pushEnv); code != 3 || !strings.Contains(out, "scanning full history within 10m0s") {
		t.Fatalf("push event: exit %d, want 3 and full history scope: %s", code, out)
	}
	if out, code := runGateCLIEnvironment(t, dir, "", pushEnv, "-range="+base+".."+head); code != 0 || !strings.Contains(out, want) {
		t.Fatalf("explicit range wins over push event: exit %d, want 0 and %q: %s", code, want, out)
	}
	leak := commitGateFile(t, dir, "creds.py", "api_key = \""+secrets.PlantedSecret()+"\"\n")
	if out, code := runGateCLIEnvironment(t, dir, "", gatePullRequestEnvironment(t, head, leak)); code != 3 || !strings.Contains(out, leak) {
		t.Fatalf("secret inside event range: exit %d, want 3 naming %s: %s", code, leak, out)
	}
}

// A pull_request event with a missing, malformed, oversized or non-regular payload, or an invalid sha, exits 2 naming the cause and never scans full history.
func TestGate_PullRequestEvent_Negative(t *testing.T) {
	valid := strings.Repeat("a", 40)
	cases := []struct{ name, path, cause string }{
		{"unset path", "", "GITHUB_EVENT_PATH"},
		{"missing file", filepath.Join(t.TempDir(), "missing.json"), "GITHUB_EVENT_PATH"},
		{"non regular file", t.TempDir(), "regular file"},
		{"invalid JSON", gateEventFile(t, "{"), "JSON"},
		{"oversized file", gateEventFile(t, strings.Repeat(" ", (4<<20)+1)), "4 MiB"},
		{"invalid head", gateEventFile(t, `{"pull_request":{"base":{"sha":"`+valid+`"},"head":{"sha":"main"}}}`), "pull_request.head.sha"},
		{"invalid base", gateEventFile(t, `{"pull_request":{"base":{"sha":"main"},"head":{"sha":"`+valid+`"}}}`), "pull_request.base.sha"},
		{"missing head", gateEventFile(t, `{"pull_request":{"base":{"sha":"`+valid+`"}}}`), "pull_request.head.sha"},
		{"missing pull request", gateEventFile(t, `{}`), "pull_request.base.sha"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := []string{"GITHUB_EVENT_NAME=pull_request", "GITHUB_EVENT_PATH=" + tc.path}
			out, code := runGateCLIEnvironment(t, t.TempDir(), "", env)
			if code != 2 || !strings.Contains(out, tc.cause) || strings.Contains(out, "scanning full history") {
				t.Fatalf("invalid event: exit %d, want 2 naming %q without fallback: %s", code, tc.cause, out)
			}
		})
	}
}

// An explicit -range wins over the event, so an invalid payload is not read.
func TestGate_PullRequestEvent_RangeOverridesInvalidEvent(t *testing.T) {
	if _, err := exec.LookPath("gitleaks"); err != nil {
		t.Skip("gitleaks not installed on PATH")
	}
	dir := secrets.BuildCleanRepo(t)
	base := commitGateFile(t, dir, "README.md", "base\n")
	head := commitGateFile(t, dir, "README.md", "head\n")
	env := []string{"GITHUB_EVENT_NAME=pull_request", "GITHUB_EVENT_PATH=missing.json"}
	if out, code := runGateCLIEnvironment(t, dir, "", env, "-range="+base+".."+head); code != 0 || !strings.Contains(out, "scanning pull request range") {
		t.Fatalf("explicit range bypasses invalid event: exit %d, want 0: %s", code, out)
	}
}

// Seven- and forty-character shas and a payload of exactly 4 MiB are accepted.
func TestGate_PullRequestEvent_Boundary(t *testing.T) {
	if _, err := exec.LookPath("gitleaks"); err != nil {
		t.Skip("gitleaks not installed on PATH")
	}
	dir := secrets.BuildCleanRepo(t)
	base := commitGateFile(t, dir, "README.md", "base\n")
	head := commitGateFile(t, dir, "README.md", "head\n")
	for _, length := range []int{7, 40} {
		env := gatePullRequestEnvironment(t, base[:length], head[:length])
		path := strings.TrimPrefix(env[1], "GITHUB_EVENT_PATH=")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, strings.Repeat(" ", (4<<20)-len(data))...)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if out, code := runGateCLIEnvironment(t, dir, "", env); code != 0 || !strings.Contains(out, "scanning pull request range") {
			t.Fatalf("%d-character SHAs and 4 MiB event: exit %d, want 0: %s", length, code, out)
		}
	}
}

// A range scan leaves an exception that matched nothing to the full-history scan; full history still fails it as unused.
func TestGate_RangeExceptionJudgment_3D(t *testing.T) {
	if _, err := exec.LookPath("gitleaks"); err != nil {
		t.Skip("gitleaks not installed on PATH")
	}
	dir, _ := secrets.BuildOrdinaryLeakRepo(t)
	base := commitGateFile(t, dir, "creds.py", "clean = 1\n")
	head := commitGateFile(t, dir, "README.md", "clean pull request\n")
	today := time.Now().UTC()
	entry := rules.Entry{Path: "creds.py", RuleID: "generic-api-key", Category: "C",
		Entered: today.Format(time.DateOnly), Expires: today.AddDate(0, 0, 7).Format(time.DateOnly), Reason: "historical waiver"}
	data, err := json.Marshal([]rules.Entry{entry})
	if err != nil {
		t.Fatal(err)
	}
	want := "not judged in a pull request range; the full-history scan on push, merge_group and schedule judges it"
	if out, code := runGateCLIEnvironment(t, dir, string(data), gatePullRequestEnvironment(t, base, head)); code != 0 || strings.Count(out, want) != 1 || !strings.Contains(out, "path creds.py, rule generic-api-key") {
		t.Fatalf("historical waiver in event range: exit %d, want 0 and one not-judged line: %s", code, out)
	}
	if out, code := runGateCLI(t, dir, string(data), "-range="+base+".."+head); code != 0 || strings.Count(out, want) != 1 {
		t.Fatalf("historical waiver in explicit range: exit %d, want 0 and one not-judged line: %s", code, out)
	}
	if out, code := runGateCLI(t, secrets.BuildCleanRepo(t), string(data)); code != 3 || !strings.Contains(out, "unused exception") {
		t.Fatalf("unused full-history waiver: exit %d, want 3 naming unused exception: %s", code, out)
	}
}

// An expired exception fails a range scan as it fails a full-history scan.
func TestGate_RangeExpiredException(t *testing.T) {
	if _, err := exec.LookPath("gitleaks"); err != nil {
		t.Skip("gitleaks not installed on PATH")
	}
	dir := secrets.BuildCleanRepo(t)
	base := commitGateFile(t, dir, "README.md", "base\n")
	head := commitGateFile(t, dir, "README.md", "head\n")
	today := time.Now().UTC()
	entry := rules.Entry{Path: "creds.py", RuleID: "generic-api-key", Category: "C", Reason: "expired historical waiver",
		Entered: today.AddDate(0, 0, -10).Format(time.DateOnly), Expires: today.AddDate(0, 0, -1).Format(time.DateOnly)}
	data, err := json.Marshal([]rules.Entry{entry})
	if err != nil {
		t.Fatal(err)
	}
	if out, code := runGateCLI(t, dir, string(data), "-range="+base+".."+head); code != 3 || !strings.Contains(out, "expired exception") {
		t.Fatalf("expired range waiver: exit %d, want 3 naming expired exception: %s", code, out)
	}
}

// An invalid exception entry stops a range scan with exit 2 before gitleaks runs.
func TestGate_RangeInvalidException(t *testing.T) {
	data := `[{"path":"../outside.py","rule_id":"generic-api-key","expires":"2026-11-01"}]`
	if out, code := runGateCLI(t, t.TempDir(), data, "-range=abcdef0..abcdef1"); code != 2 || !strings.Contains(out, "invalid exceptions") {
		t.Fatalf("invalid range exception: exit %d, want 2 naming invalid exceptions: %s", code, out)
	}
}

// commitGateFile writes name with content in dir, commits it and returns the commit sha.
func commitGateFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", name}, {"commit", "--quiet", "-m", "fixture change"}} {
		if out, err := util.RunGit(t.Context(), dir, args...); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	out, err := util.RunGit(t.Context(), dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

// An explicit -range scans only those commits: an older secret passes, a secret inside the range fails.
func TestGate_PullRequestRange_Planted_3D(t *testing.T) {
	if _, err := exec.LookPath("gitleaks"); err != nil {
		t.Skip("gitleaks not installed on PATH")
	}
	dir, _ := secrets.BuildOrdinaryLeakRepo(t)
	base := commitGateFile(t, dir, "creds.py", "clean = 1\n")
	head := commitGateFile(t, dir, "README.md", "clean pull request\n")
	want := "secrets gate: scanning pull request range " + base[:12] + ".." + head[:12] + " within 5m0s"
	if out, code := runGateCLI(t, dir, "", "-range="+base+".."+head); code != 0 || !strings.Contains(out, want) {
		t.Fatalf("clean range after historical secret: exit %d, want 0 and %q: %s", code, want, out)
	}
	if out, code := runGateCLI(t, dir, ""); code != 3 || !strings.Contains(out, "secrets gate: scanning full history") {
		t.Fatalf("full history: exit %d, want 3 and full history scope: %s", code, out)
	}
	leak := commitGateFile(t, dir, "creds.py", "api_key = \""+secrets.PlantedSecret()+"\"\n")
	if out, code := runGateCLI(t, dir, "", "-range="+head+".."+leak); code != 3 || !strings.Contains(out, leak) {
		t.Fatalf("secret inside range: exit %d, want 3 naming %s: %s", code, leak, out)
	}
}

// A -range that is not two 7-40 hex shas joined by two dots exits 2 naming the flag and value.
func TestGate_PullRequestRange_Negative(t *testing.T) {
	for _, value := range []string{
		"main..HEAD", "--all", "abc", "a1b2c3d", "a1b2c3d...e4f5a6b", "",
		"a1b2c3..e4f5a6b", strings.Repeat("a", 41) + "..e4f5a6b", "a1b2c3d..e4f5a6g",
		"a1b2c3d..e4f5a6b --all", "a1b2c3d..e4f5a6b\n",
	} {
		t.Run(value, func(t *testing.T) {
			out, code := runGateCLI(t, t.TempDir(), "", "-range="+value)
			if code != 2 || !strings.Contains(out, "-range") || !strings.Contains(out, strconv.Quote(value)) {
				t.Fatalf("invalid -range=%q: exit %d, want 2 naming flag and value: %s", value, code, out)
			}
		})
	}
}

// A -range with seven- and forty-character sides is accepted.
func TestGate_PullRequestRange_Boundary(t *testing.T) {
	if _, err := exec.LookPath("gitleaks"); err != nil {
		t.Skip("gitleaks not installed on PATH")
	}
	dir := secrets.BuildCleanRepo(t)
	base, err := util.RunGit(t.Context(), dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	base = strings.TrimSpace(base)
	head := commitGateFile(t, dir, "README.md", "clean update\n")
	for _, length := range []int{7, 40} {
		value := base[:length] + ".." + head[:length]
		want := "secrets gate: scanning pull request range " + base[:min(length, 12)] + ".." + head[:min(length, 12)]
		t.Run(value, func(t *testing.T) {
			if out, code := runGateCLI(t, dir, "", "-range="+value); code != 0 || !strings.Contains(out, want) {
				t.Fatalf("boundary range: exit %d, want 0 and %q: %s", code, want, out)
			}
		})
	}
}

// A secret added only in a merge commit is found by the full-history scan (#1237).
func TestGate_PlantedMergeOnly_3D(t *testing.T) {
	if _, err := exec.LookPath("gitleaks"); err != nil {
		t.Skip("gitleaks not installed on PATH")
	}
	dir, mergeHash := secrets.BuildMergeLeakRepo(t)

	out, exitCode := runGateCLI(t, dir, "")
	if exitCode != 3 {
		t.Fatalf("expected exit code 3 for merge commit leak, got %d. Output: %s", exitCode, out)
	}
	if !strings.Contains(out, mergeHash) {
		t.Errorf("output missing merge commit hash %s: %s", mergeHash, out)
	}
	base, err := util.RunGit(t.Context(), dir, "rev-parse", mergeHash+"^")
	if err != nil {
		t.Fatal(err)
	}
	if out, code := runGateCLI(t, dir, "", "-range="+strings.TrimSpace(base)+".."+mergeHash); code != 3 || !strings.Contains(out, mergeHash) {
		t.Fatalf("merge-only secret in range: exit %d, want 3 naming %s: %s", code, mergeHash, out)
	}
}

// pinTable is the engine's pin table file the .gitleaks.toml allowlist names.
const pinTable = "internal/forge/workflow_reusable_contexts.go"

// pinLine is a pinned reusable workflow commit id as the pin table writes it: a public 40-hex commit
// assigned to a ...WorkflowSHA constant whose name says Secret.
const pinLine = "\tPluranoHoristesSecretScanWorkflowSHA = \"c736fac69552061fb795ac49951c5284a3f5d041\" // public commit id\n"

// TestGate_PinnedWorkflowSHAAllowed_3D replays the repository's own .gitleaks.toml (order
// engine-red-main-secret-scan-pin-sha-20261026): the pinned commit id in the pin table passes, a
// key-shaped value in the same file is still a finding, and the same commit id line in any other
// file is still a finding.
func TestGate_PinnedWorkflowSHAAllowed_3D(t *testing.T) {
	if _, err := exec.LookPath("gitleaks"); err != nil {
		t.Skip("gitleaks not installed on PATH")
	}
	cfg, err := os.ReadFile(filepath.Join("..", "..", ".gitleaks.toml"))
	if err != nil {
		t.Fatal(err)
	}
	head := "package forge\n\nconst (\n"
	clean := secrets.BuildRepoWithFile(t, cfg, pinTable, head+pinLine+")\n")
	if out, code := runGateCLI(t, clean, ""); code != 0 {
		t.Fatalf("pinned commit id in the pin table: exit %d, want 0: %s", code, out)
	}
	planted := head + pinLine + "\tpluranoSecretToken = \"" + secrets.PlantedSecret() + "\"\n)\n"
	leak := secrets.BuildRepoWithFile(t, cfg, pinTable, planted)
	if out, code := runGateCLI(t, leak, ""); code != 3 || !strings.Contains(out, pinTable) {
		t.Fatalf("key-shaped value beside the pin: exit %d, want 3 naming %s: %s", code, pinTable, out)
	}
	other := secrets.BuildRepoWithFile(t, cfg, "internal/forge/other.go", head+pinLine+")\n")
	if out, code := runGateCLI(t, other, ""); code != 3 {
		t.Fatalf("pin line outside the pin table: exit %d, want 3: %s", code, out)
	}
}

func runGateLifecycleCase(t *testing.T, entries []rules.Entry) (string, int) {
	t.Helper()
	dir, _ := secrets.BuildOrdinaryLeakRepo(t)
	var exceptionsJSON string
	if entries != nil {
		data, err := json.Marshal(entries)
		if err != nil {
			t.Fatal(err)
		}
		exceptionsJSON = string(data)
	}
	return runGateCLI(t, dir, exceptionsJSON)
}

// Live, stale and expired exceptions pass, fail as unused and fail as expired; dates are relative to the test day (#1886).
func TestGate_ExceptionsLifecycle_3D(t *testing.T) {
	if _, err := exec.LookPath("gitleaks"); err != nil {
		t.Skip("gitleaks not installed on PATH")
	}
	today := time.Now().UTC()
	entered, expires := today.Format(time.DateOnly), today.AddDate(0, 0, 7).Format(time.DateOnly)

	t.Run("unexcused token is refused", func(t *testing.T) {
		out, exitCode := runGateLifecycleCase(t, nil)
		if exitCode != 3 {
			t.Fatalf("expected exit code 3 on unexcused token, got %d: %s", exitCode, out)
		}
	})

	t.Run("dated entry excuses its finding", func(t *testing.T) {
		entries := []rules.Entry{
			{Path: "creds.py", RuleID: "generic-api-key", Category: "C", Entered: entered, Expires: expires, Reason: "dated test waiver"},
		}
		out, exitCode := runGateLifecycleCase(t, entries)
		if exitCode != 0 {
			t.Fatalf("expected exit code 0 for dated entry, got %d: %s", exitCode, out)
		}
	})

	t.Run("expired entry fails", func(t *testing.T) {
		entries := []rules.Entry{
			{Path: "creds.py", RuleID: "generic-api-key", Expires: "2026-10-01", Reason: "expired test waiver"},
		}
		out, exitCode := runGateLifecycleCase(t, entries)
		if exitCode != 3 {
			t.Fatalf("expected exit code 3 for expired entry, got %d: %s", exitCode, out)
		}
	})

	t.Run("stale entry fails", func(t *testing.T) {
		entries := []rules.Entry{
			{Path: "other.py", RuleID: "generic-api-key", Category: "C", Entered: entered, Expires: expires, Reason: "stale waiver for other file"},
		}
		out, exitCode := runGateLifecycleCase(t, entries)
		if exitCode != 3 {
			t.Fatalf("expected exit code 3 for stale entry, got %d: %s", exitCode, out)
		}
	})
}

func TestGate_RefScoping_3D(t *testing.T) {
	if _, err := exec.LookPath("gitleaks"); err != nil {
		t.Skip("gitleaks not installed on PATH")
	}
	dir, leakHash := secrets.BuildForeignRemoteLeakRepo(t)

	t.Run("foreign remote refs excluded", func(t *testing.T) {
		out, exitCode := runGateCLI(t, dir, "")
		if exitCode != 0 {
			t.Fatalf("expected exit code 0 on foreign remote ref, got %d: %s", exitCode, out)
		}
	})

	t.Run("origin remote ref included", func(t *testing.T) {
		secrets.UpdateRef(t, dir, "refs/remotes/origin/feature", leakHash)
		secrets.DeleteRef(t, dir, "refs/remotes/upstream/leak")
		out, exitCode := runGateCLI(t, dir, "")
		if exitCode != 3 {
			t.Fatalf("expected exit code 3 on origin remote ref, got %d: %s", exitCode, out)
		}
		if !strings.Contains(out, leakHash) {
			t.Errorf("output missing leak commit hash %s: %s", leakHash, out)
		}
	})
}
