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

	"github.com/cordanaLLM/praetor/tools/secrets"
	"github.com/cordanaLLM/praetor/tools/secrets/rules"
)

func runGateCLI(t *testing.T, dir string, envExceptions string) (string, int) {
	t.Helper()
	cmd := exec.Command("go", "run", "-tags=secretsgate", "./tools/secrets/gate", "-path="+dir)
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = append(os.Environ(), "SECRETS_EXCEPTIONS="+envExceptions)
	out, err := cmd.CombinedOutput()
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

func TestGate_ExceptionsLifecycle_3D(t *testing.T) {
	if _, err := exec.LookPath("gitleaks"); err != nil {
		t.Skip("gitleaks not installed on PATH")
	}

	t.Run("unexcused token is refused", func(t *testing.T) {
		dir, _ := secrets.BuildOrdinaryLeakRepo(t)
		out, exitCode := runGateCLI(t, dir, "")
		if exitCode != 3 {
			t.Fatalf("expected exit code 3 on unexcused token, got %d: %s", exitCode, out)
		}
	})

	t.Run("dated entry excuses its finding", func(t *testing.T) {
		dir, _ := secrets.BuildOrdinaryLeakRepo(t)
		entries := []rules.Entry{
			{
				Path:    "creds.py",
				RuleID:  "generic-api-key",
				Expires: "2026-11-01",
				Reason:  "dated test waiver",
			},
		}
		data, err := json.Marshal(entries)
		if err != nil {
			t.Fatal(err)
		}
		out, exitCode := runGateCLI(t, dir, string(data))
		if exitCode != 0 {
			t.Fatalf("expected exit code 0 for dated entry, got %d: %s", exitCode, out)
		}
	})

	t.Run("expired entry fails", func(t *testing.T) {
		dir, _ := secrets.BuildOrdinaryLeakRepo(t)
		entries := []rules.Entry{
			{
				Path:    "creds.py",
				RuleID:  "generic-api-key",
				Expires: "2026-10-01",
				Reason:  "expired test waiver",
			},
		}
		data, err := json.Marshal(entries)
		if err != nil {
			t.Fatal(err)
		}
		out, exitCode := runGateCLI(t, dir, string(data))
		if exitCode != 3 {
			t.Fatalf("expected exit code 3 for expired entry, got %d: %s", exitCode, out)
		}
	})

	t.Run("stale entry fails", func(t *testing.T) {
		dir, _ := secrets.BuildOrdinaryLeakRepo(t)
		entries := []rules.Entry{
			{
				Path:    "other.py",
				RuleID:  "generic-api-key",
				Expires: "2026-11-01",
				Reason:  "stale waiver for other file",
			},
		}
		data, err := json.Marshal(entries)
		if err != nil {
			t.Fatal(err)
		}
		out, exitCode := runGateCLI(t, dir, string(data))
		if exitCode != 3 {
			t.Fatalf("expected exit code 3 for stale entry, got %d: %s", exitCode, out)
		}
	})
}
