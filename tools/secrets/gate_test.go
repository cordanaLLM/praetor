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

func runGateCLI(t *testing.T, dir string, envExceptions string, args ...string) (string, int) {
	t.Helper()
	ctx, err := util.WithCommandEnvironment(t.Context(), append(os.Environ(), "SECRETS_EXCEPTIONS="+envExceptions))
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

func TestGate_PullRequestRange_Planted_3D(t *testing.T) {
	if _, err := exec.LookPath("gitleaks"); err != nil {
		t.Skip("gitleaks not installed on PATH")
	}
	dir, _ := secrets.BuildOrdinaryLeakRepo(t)
	base := commitGateFile(t, dir, "creds.py", "clean = 1\n")
	head := commitGateFile(t, dir, "README.md", "clean pull request\n")
	want := "secrets gate: scanning pull request range " + base[:12] + ".." + head[:12]
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
