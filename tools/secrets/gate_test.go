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
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"

	"github.com/cordanaLLM/praetor/tools/secrets"
	"github.com/cordanaLLM/praetor/tools/secrets/rules"
)

func runGateCLI(t *testing.T, dir string, envExceptions string) (string, int) {
	t.Helper()
	ctx, err := util.WithCommandEnvironment(t.Context(), append(os.Environ(), "SECRETS_EXCEPTIONS="+envExceptions))
	if err != nil {
		t.Fatal(err)
	}
	res, err := util.RunCommandBytes(ctx, filepath.Join("..", ".."), "go", util.MaxCommandOutputBytes,
		"run", "-tags=secretsgate", "./tools/secrets/gate", "-path="+dir)
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

	t.Run("unexcused token is refused", func(t *testing.T) {
		out, exitCode := runGateLifecycleCase(t, nil)
		if exitCode != 3 {
			t.Fatalf("expected exit code 3 on unexcused token, got %d: %s", exitCode, out)
		}
	})

	t.Run("dated entry excuses its finding", func(t *testing.T) {
		entries := []rules.Entry{
			{Path: "creds.py", RuleID: "generic-api-key", Expires: "2026-11-01", Reason: "dated test waiver"},
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
			{Path: "other.py", RuleID: "generic-api-key", Expires: "2026-11-01", Reason: "stale waiver for other file"},
		}
		out, exitCode := runGateLifecycleCase(t, entries)
		if exitCode != 3 {
			t.Fatalf("expected exit code 3 for stale entry, got %d: %s", exitCode, out)
		}
	})
}
