// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/agenthook"
	"github.com/cordanaLLM/praetor/internal/config"
)

// hookSettingsManifest writes isolated installed settings in the requested fixture state.
// Order: engine-hook-digest-mismatch-fail-closed-20261028.
func hookSettingsManifest(t *testing.T, state string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	fleet, workstation := filepath.Join(dir, "fleet.yaml"), filepath.Join(dir, "workstation.yaml")
	body := "hooks: {}\n"
	if state == "invalid" {
		body = "hooks: [\n"
	}
	if state == "forge" {
		body = "forge:\n  pr_title_limits: [{repo: example/repo, max: 64}]\n  own_organizations: [fleet-org]\n"
	}
	writeFixtureFile(t, dir, "fleet.yaml", body)
	writeFixtureFile(t, dir, "workstation.yaml", "hooks: {}\n")
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
	if state == "mismatch" {
		digest = fmt.Sprintf("%x", sha256.Sum256([]byte("other content\n")))
	}
	manifest := config.InstallManifest{
		Version: 1, EngineCommit: strings.Repeat("a", 40), InstalledAt: "2026-10-28T01:00:00Z", BinDir: dir,
		Binaries: map[string]config.InstalledBinary{"praetorctl": {SHA256: strings.Repeat("b", 64)}},
		Settings: config.InstalledSettings{
			Fleet:       &config.InstalledDocument{Path: fleet, SHA256: digest},
			Workstation: &config.InstalledDocument{Path: workstation, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("hooks: {}\n")))},
		},
	}
	path := filepath.Join(dir, "install.json")
	if err := config.WriteInstallManifest(path, manifest); err != nil {
		t.Fatal(err)
	}
	if state == "unreadable" {
		if err := os.Remove(fleet); err != nil {
			t.Fatal(err)
		}
	}
	return "PRAETOR_HOOK_PROCESS_MANIFEST=" + path, fleet
}

// TestHookForgeSettingsSources enforces manifest-only and environment-only forge settings.
func TestHookForgeSettingsSources(t *testing.T) {
	root := hookRepository(t, true)
	manifest, fleet := hookSettingsManifest(t, "forge")
	for _, source := range []struct{ name, env string }{
		{"manifest", manifest}, {"environment", "PRAETOR_HOOK_PROCESS_FLEET=" + fleet},
	} {
		for _, tc := range []struct {
			name, command, reason string
			exit                  int
		}{
			{"65 denied", `gh pr create --repo example/repo --title "` + strings.Repeat("x", 65) + `"`, "pr-title-limit", 2},
			{"64 allowed", `gh pr create --repo example/repo --title "` + strings.Repeat("x", 64) + `"`, "", 0},
			{"own organization", `gh issue comment 1 --body "fleet-org/service#11"`, "", 0},
			{"foreign organization", `gh issue comment 1 --body "outside-org/service#11"`, "forge-text-foreign-reference", 2},
		} {
			t.Run(source.name+"/"+tc.name, func(t *testing.T) {
				payload := []byte(`{"tool_input":{"command":` + quoteJSON(t, tc.command) + `}}`)
				result, exit := hookProcess(t, root, "claude pre-tool", payload, source.env)
				if exit != tc.exit || !strings.Contains(string(result.Stderr), tc.reason) {
					t.Fatalf("exit %d want %d, stdout %q stderr %q", exit, tc.exit, result.Stdout, result.Stderr)
				}
			})
		}
	}
}

// TestHookForgeSettingsMissingManifestDocument refuses forge writes naming the missing settings.
func TestHookForgeSettingsMissingManifestDocument(t *testing.T) {
	root := hookRepository(t, false)
	manifest, fleet := hookSettingsManifest(t, "unreadable")
	for _, command := range []string{
		`gh pr create --repo example/repo --title "` + strings.Repeat("x", 65) + `"`,
		`gh issue comment 1 --body "fleet-org/service#11"`,
	} {
		payload := []byte(`{"tool_input":{"command":` + quoteJSON(t, command) + `}}`)
		result, exit := hookProcess(t, root, "claude pre-tool", payload, manifest)
		if exit != 2 || !strings.Contains(string(result.Stderr), "operator settings unavailable:") ||
			!strings.Contains(string(result.Stderr), fleet) {
			t.Fatalf("exit %d stdout %q stderr %q", exit, result.Stdout, result.Stderr)
		}
	}
}

func TestHookSettingsUnavailableBlocks(t *testing.T) {
	root := hookRepository(t, true)
	for _, state := range []string{"mismatch", "unreadable", "invalid"} {
		env, fleet := hookSettingsManifest(t, state)
		for _, client := range []string{"claude", "codex", "gemini", "lefthook"} {
			t.Run(state+"/"+client, func(t *testing.T) {
				payload := []byte(`{"tool_input":{"command":"make test"}}`)
				result, exit := hookProcess(t, root, client+" pre-tool", payload, env)
				want := 2
				if client == "lefthook" {
					want = 1
				}
				line := string(result.Stdout) + string(result.Stderr)
				if exit != want || !strings.Contains(line, "[BLOCKED BY HISS] operator settings unavailable:") ||
					!strings.Contains(line, "workstation install --source") || !strings.Contains(line, strconv.Quote(fleet)) {
					t.Fatalf("exit %d want %d: %s", exit, want, line)
				}
				t.Logf("exit %d: %s", exit, strings.TrimSpace(line))
			})
		}
	}
}

func TestHookSettingsRepairAndSkip(t *testing.T) {
	root := hookRepository(t, true)
	env, fleet := hookSettingsManifest(t, "mismatch")
	for _, tc := range []struct{ name, event, command, notice string }{
		{"repair", "pre-tool", "praetorctl workstation install --source /x --fleet-config /y", "repair allowed"},
		{"alias repair", "pre-tool", "standardsctl workstation install --source /x", "repair allowed"},
		{"post-tool", "post-tool", "make test", "skipped"},
		{"prompt", "prompt", "", "skipped"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := []byte(`{"tool_input":{"command":` + quoteJSON(t, tc.command) + `}}`)
			result, exit := hookProcess(t, root, "claude "+tc.event, payload, env)
			line := string(result.Stdout) + string(result.Stderr)
			if exit != 0 || !strings.Contains(line, tc.notice) || !strings.Contains(line, "changed since") ||
				!strings.Contains(line, "workstation install --source") || !strings.Contains(line, fleet) {
				t.Fatalf("exit %d: %s", exit, line)
			}
			t.Logf("exit %d: %s", exit, strings.TrimSpace(line))
		})
	}
}

func TestHookSettingsFailureKeepsUsageAndBlocksOtherCalls(t *testing.T) {
	root := hookRepository(t, true)
	env, _ := hookSettingsManifest(t, "mismatch")
	for _, tc := range []struct{ name, arguments, payload, reason string }{
		{"unknown client", "cursor pre-tool", `{}`, "usage: praetorctl hook"},
		{"malformed payload", "claude pre-tool", `{`, "operator settings unavailable"},
		{"pre-edit", "claude pre-edit", `{}`, "operator settings unavailable"},
		{"pre-dispatch", "codex pre-dispatch", `{}`, "operator settings unavailable"},
		{"pre-handback", "claude pre-handback", `{}`, "operator settings unavailable"},
		{"repair chain", "claude pre-tool", `{"tool_input":{"command":"praetorctl workstation install; make test"}}`, "operator settings unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, exit := hookProcess(t, root, tc.arguments, []byte(tc.payload), env)
			if exit != 2 || !strings.Contains(string(result.Stderr), tc.reason) {
				t.Fatalf("exit %d stdout %q stderr %q", exit, result.Stdout, result.Stderr)
			}
		})
	}
}

func TestHookSettingsFailureCannotRecordAndAllow(t *testing.T) {
	root := hookRepository(t, true)
	env, _ := hookSettingsManifest(t, "mismatch")
	recordDir := t.TempDir()
	result, exit := hookProcess(t, root, "claude pre-tool", []byte(`{"tool_input":{"command":"make test"}}`),
		env, agenthook.RecordDirEnv+"="+recordDir)
	if exit != 2 || !strings.Contains(string(result.Stderr), "operator settings unavailable") {
		t.Fatalf("exit %d stdout %q stderr %q", exit, result.Stdout, result.Stderr)
	}
	entries, err := os.ReadDir(recordDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("unexpected recording: %v %v", entries, err)
	}
}

func TestHookSettingsMatchedAllows(t *testing.T) {
	root := hookRepository(t, true)
	env, _ := hookSettingsManifest(t, "matched")
	result, exit := hookProcess(t, root, "claude pre-tool", []byte(`{"tool_input":{"command":"git status"}}`), env)
	if exit != 0 || len(result.Stderr) != 0 || len(result.Stdout) != 0 {
		t.Fatalf("exit %d stdout %q stderr %q", exit, result.Stdout, result.Stderr)
	}
	t.Log("exit 0: matched digest; git status allowed")
}
