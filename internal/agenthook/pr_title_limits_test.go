// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// TestForgeSettingsSelection preserves selected empty settings and refuses selection errors.
func TestForgeSettingsSelection(t *testing.T) {
	selected := config.ForgeSettings{
		PRTitleLimits:    []config.PRTitleLimit{{Repo: "example/repo", Max: 64}},
		OwnOrganizations: []string{"fleet-org"},
	}
	cause := errors.New("selected settings unavailable")
	for _, tc := range []struct {
		name string
		in   Invocation
		want config.ForgeSettings
		err  error
	}{
		{"selected without environment", Invocation{Forge: &selected}, selected, nil},
		{"selected overrides environment", Invocation{Forge: &selected, Getenv: titleLimitEnvironment(t, "")}, selected, nil},
		{"selected empty overrides environment", Invocation{Forge: &config.ForgeSettings{}, Getenv: titleLimitEnvironment(t, "")}, config.ForgeSettings{}, nil},
		{"selection error", Invocation{Forge: &selected, SettingsError: cause}, config.ForgeSettings{}, cause},
		{"unselected error", Invocation{SettingsError: cause}, config.ForgeSettings{}, cause},
		{"unselected without environment", Invocation{}, config.ForgeSettings{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := loadOperatorForgeSettings(t.Context(), tc.in)
			if !errors.Is(err, tc.err) || !slices.Equal(got.PRTitleLimits, tc.want.PRTitleLimits) ||
				!slices.Equal(got.OwnOrganizations, tc.want.OwnOrganizations) {
				t.Fatalf("forge settings %+v, error %v; want %+v, %v", got, err, tc.want, tc.err)
			}
		})
	}
}

// titleLimitEnvironment supplies only a fixture operator file and optional GH_REPO.
func titleLimitEnvironment(t *testing.T, ghRepo string) func(string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fleet.yaml")
	writeTestFixtureFile(t, path, "forge: {pr_title_limits: [{repo: example/widget, max: 64}]}\n")
	return func(key string) string {
		if key == config.FleetConfigEnv {
			return path
		}
		if key == "GH_REPO" {
			return ghRepo
		}
		return ""
	}
}

// TestPRTitleLimitsPreToolPlants65And64 exercises the actual pre-tool boundary.
func TestPRTitleLimitsPreToolPlants65And64(t *testing.T) {
	root := repository(t, true)
	getenv := titleLimitEnvironment(t, "")
	t.Run("65 denied", func(t *testing.T) {
		title := strings.Repeat("x", 65)
		response := runPreToolCommand(t, root, `gh pr create --repo example/widget --title "`+title+`"`, getenv)
		assertPRTitle65Denied(t, response, title)
	})
	t.Run("64 allowed", func(t *testing.T) {
		title := strings.Repeat("x", 64)
		response := runPreToolCommand(t, root, `gh pr create --repo example/widget --title "`+title+`"`, getenv)
		if response.ExitCode != 0 {
			t.Fatalf("64 must pass: %+v", response)
		}
	})
}

// assertPRTitle65Denied requires the rule, target, measured length, cap and offending title.
func assertPRTitle65Denied(t *testing.T, response Response, title string) {
	t.Helper()
	reason := string(response.Stderr)
	if response.ExitCode != 2 {
		t.Fatalf("65 must deny, got exit %d: %s", response.ExitCode, reason)
	}
	for _, want := range []string{"pr-title-limit", "example/widget", "65", "64", title} {
		if !strings.Contains(reason, want) {
			t.Errorf("refusal missing %q: %s", want, reason)
		}
	}
}

// TestPRTitleLimitsResolutionAndForms covers target precedence, flag forms and Unicode.
func TestPRTitleLimitsResolutionAndForms(t *testing.T) {
	root := repository(t, true)
	testsupport.RunFixtureGit(t, root, []string{"remote", "add", "origin", "https://github.com/example/widget.git"})
	long, boundary := strings.Repeat("é", 65), strings.Repeat("é", 64)
	cases := []struct {
		name, command, ghRepo string
		deny                  bool
	}{
		{"origin", `gh pr create --title "` + long + `"`, "", true},
		{"GH_REPO", `gh pr create --title "` + long + `"`, "example/widget", true},
		{"GH_REPO beats origin", `gh pr create --title "` + long + `"`, "example/other", false},
		{"repo beats env", `gh pr create --repo example/widget -t "` + long + `"`, "example/other", true},
		{"short repo", `gh pr create -R EXAMPLE/WIDGET -t "` + long + `"`, "", true},
		{"global repo", `gh -R example/widget pr create --title="` + long + `"`, "", true},
		{"inline repo", `gh pr create --repo=example/widget --title="` + long + `"`, "", true},
		{"short equals", `gh pr create -R=example/widget -t="` + long + `"`, "", true},
		{"attached short", `gh pr create -Rexample/widget -t"` + long + `"`, "", true},
		{"host prefix", `gh pr create -R github.com/example/widget -t "` + long + `"`, "", true},
		{"unicode boundary", `gh pr create -t "` + boundary + `"`, "", false},
		{"unconfigured", `gh pr create --repo example/other -t "` + long + `"`, "", false},
		{"fill", `gh pr create --fill`, "", false},
		{"interactive", `gh pr create`, "", false},
		{"edit", `gh pr edit 1 --title "` + long + `"`, "", false},
		{"issue", `gh issue create --title "` + long + `"`, "", false},
		{"body value", `gh pr create --body '--title' --title "` + long + `"`, "", true},
		{"literal quotes", `gh pr create -t '` + strings.Repeat(`'"'"'`, 65) + `'`, "", true},
		{"second call", `gh pr create -t ok; gh pr create -t "` + long + `"`, "", true},
		{"invalid repo", `gh pr create -R invalid -t ok`, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := runPreToolCommand(t, root, tc.command, titleLimitEnvironment(t, tc.ghRepo))
			if (response.ExitCode != 0) != tc.deny {
				t.Fatalf("deny=%v, got exit %d: %s", tc.deny, response.ExitCode, response.Stderr)
			}
			if tc.deny && !strings.Contains(string(response.Stderr), "pr-title-limit") {
				t.Fatalf("wrong refusal: %s", response.Stderr)
			}
		})
	}
}

// TestPRTitleLimitsEveryPreToolClient checks every registered client and ungoverned checkout.
func TestPRTitleLimitsEveryPreToolClient(t *testing.T) {
	root := repository(t, false)
	for _, client := range []string{"claude", "codex", "gemini", "lefthook", "agy"} {
		t.Run(client, func(t *testing.T) {
			command := `gh pr create -R example/widget -t "` + strings.Repeat("x", 65) + `"`
			payload := map[string]any{"tool_input": map[string]any{"command": command}}
			if client == "agy" {
				payload = map[string]any{"toolCall": map[string]any{"name": "run_command", "args": map[string]any{"CommandLine": command}}}
			}
			response := Run(t.Context(), Invocation{Client: client, Event: "pre-tool", WorkDir: root,
				Stdin: bytes.NewReader(commandPayload(t, payload)), Getenv: titleLimitEnvironment(t, ""), Policy: policy(t)})
			if client == "agy" {
				if !strings.Contains(string(response.Stdout), `"decision":"deny"`) {
					t.Fatalf("agy allowed: %+v", response)
				}
			} else if response.ExitCode != denyExit(client) {
				t.Fatalf("client %s exit %d: %s", client, response.ExitCode, response.Stderr)
			}
			if !strings.Contains(string(response.Stdout)+string(response.Stderr), "pr-title-limit") {
				t.Fatalf("wrong rule: %+v", response)
			}
		})
	}
}

// TestPRTitleLimitsReadFailures denies missing settings and unresolved targets by name.
func TestPRTitleLimitsReadFailures(t *testing.T) {
	root := repository(t, false)
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	response := runPreToolCommand(t, root, `gh pr create -R example/widget -t ok`, func(key string) string {
		if key == config.FleetConfigEnv {
			return missing
		}
		return ""
	})
	for _, want := range []string{"pr-title-limit", "operator forge settings unavailable", "missing.yaml"} {
		if response.ExitCode != 2 || !strings.Contains(string(response.Stderr), want) {
			t.Fatalf("missing %s: %+v", want, response)
		}
	}
	response = runPreToolCommand(t, root, `gh pr create -t ok`, titleLimitEnvironment(t, ""))
	if response.ExitCode != 2 || !strings.Contains(string(response.Stderr), "target repository unavailable") {
		t.Fatalf("unresolved target: %+v", response)
	}
	response = runPreToolCommand(t, root, `gh pr create -t ok`, noEnvironment)
	if response.ExitCode != 0 {
		t.Fatalf("no configured limits: %+v", response)
	}
}

// TestPRTitleLimitsCommandDirectory reuses scoped cd tracking and canonical command cwd.
func TestPRTitleLimitsCommandDirectory(t *testing.T) {
	outer, inner := repository(t, true), repository(t, false)
	testsupport.RunFixtureGit(t, inner, []string{"remote", "add", "origin", "https://github.com/example/widget.git"})
	command := `cd "` + inner + `" && gh pr create -t "` + strings.Repeat("x", 65) + `"`
	response := runPreToolCommand(t, outer, command, titleLimitEnvironment(t, ""))
	if response.ExitCode != 2 || !strings.Contains(string(response.Stderr), "pr-title-limit") {
		t.Fatalf("cd origin: %+v", response)
	}
	verdict := evaluatePRTitleLimits(context.Background(), Canonical{Command: `gh pr create -t "` + strings.Repeat("x", 65) + `"`, CommandDir: inner}, Invocation{WorkDir: outer, Getenv: titleLimitEnvironment(t, "")})
	if verdict.Outcome != Deny {
		t.Fatalf("canonical cwd: %+v", verdict)
	}
}

// TestPRTitleLimitsEngineCheckout reads back the hook from this engine checkout without writes.
func TestPRTitleLimitsEngineCheckout(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "internal", "agenthook", "evaluate.go")); err != nil {
		t.Fatal(err)
	}
	for _, count := range []int{65, 64} {
		response := runPreToolCommand(t, root, `gh pr create -R example/widget -t "`+strings.Repeat("x", count)+`"`, titleLimitEnvironment(t, ""))
		if (response.ExitCode == 2) != (count == 65) || (count == 64 && response.ExitCode != 0) {
			t.Fatalf("engine checkout, length %d: %+v", count, response)
		}
		t.Logf("engine checkout %s, length %d, exit %d, refusal %s", root, count, response.ExitCode, response.Stderr)
	}
}
