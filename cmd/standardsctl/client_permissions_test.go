package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/clientsetup"
	"github.com/cordanaLLM/praetor/internal/config"
)

type clientPermissionPlanMetadata struct {
	Target          string   `json:"target"`
	Managed         bool     `json:"managed"`
	BeforeCount     int      `json:"before_count"`
	Added           []string `json:"added"`
	AfterCount      int      `json:"after_count"`
	RuntimeVerified bool     `json:"runtime_verified"`
}

func TestClientPermissionsPlanApplyVerify(t *testing.T) {
	clearPermissionEnvironment(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")
	before := []byte("{\n  \"theme\": \"dark\",\n  \"permissions\": {\n    \"allow\": [\"command(git diff)\", \"mcp(existing/tool)\"],\n    \"ask\": [\"read_url(example.test)\"],\n    \"deny\": [\"write_file(.git/)\"]\n  }\n}\n")
	writeFixtureFile(t, home, ".gemini/antigravity-cli/settings.json", string(before))
	policy := permissionPolicy(t, root, true, []string{
		"command(git diff)",
		"mcp(hindsight/hindsight_list_knowledge_pages)",
		"command(git status)",
	})
	common := []string{"--client", "agy", "--fleet-config", policy,
		"--manifest", filepath.Join(root, "missing-install.json"), "--home", home}

	planDir := filepath.Join(root, "plan")
	planArgs := append([]string{"permissions", "plan"}, common...)
	planArgs = append(planArgs, "--out", planDir)
	if err := runClients(planArgs); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(target); err != nil || !bytes.Equal(got, before) {
		t.Fatalf("planning changed settings: %v", err)
	}
	metadata := readPermissionPlan(t, filepath.Join(planDir, "plan.json"))
	if metadata.Target != target || !metadata.Managed || metadata.BeforeCount != 2 || metadata.AfterCount != 4 ||
		len(metadata.Added) != 2 || metadata.RuntimeVerified {
		t.Fatalf("unexpected permission plan: %+v", metadata)
	}
	candidate, err := os.ReadFile(filepath.Join(planDir, "agy-settings.json"))
	if err != nil || !bytes.Contains(candidate, []byte(`"theme": "dark"`)) ||
		!bytes.Contains(candidate, []byte(`"ask"`)) || !bytes.Contains(candidate, []byte(`"deny"`)) {
		t.Fatalf("candidate lost existing settings: %v\n%s", err, candidate)
	}

	applyDir := filepath.Join(root, "apply")
	applyArgs := append([]string{"permissions", "apply"}, common...)
	applyArgs = append(applyArgs, "--out", applyDir)
	if err := runClients(applyArgs); err != nil {
		t.Fatal(err)
	}
	applied, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(applied, candidate) {
		t.Fatalf("apply did not publish planned bytes: %v", err)
	}
	backup, err := os.ReadFile(filepath.Join(applyDir, "config.before"))
	if err != nil || !bytes.Equal(backup, before) {
		t.Fatalf("exact backup missing: %v", err)
	}
	plannedMetadata, planErr := os.ReadFile(filepath.Join(planDir, "plan.json"))
	retainedMetadata, applyErr := os.ReadFile(filepath.Join(applyDir, "plan.json"))
	if planErr != nil || applyErr != nil || !bytes.Equal(plannedMetadata, retainedMetadata) {
		t.Fatalf("apply retained different plan metadata than plan wrote: %v %v\n%s\n%s",
			planErr, applyErr, plannedMetadata, retainedMetadata)
	}
	replayDir := filepath.Join(root, "replay")
	replayArgs := append([]string{"permissions", "apply"}, common...)
	replayArgs = append(replayArgs, "--out", replayDir)
	if err := runClients(replayArgs); err != nil {
		t.Fatal(err)
	}
	replayed, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(replayed, applied) {
		t.Fatalf("idempotent apply changed settings: %v", err)
	}

	verifyArgs := append([]string{"permissions", "verify"}, common...)
	stdout, err := captureStdout(t, func() error { return runClients(verifyArgs) })
	if err != nil {
		t.Fatal(err)
	}
	var report clientPermissionReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("decode verify report: %v\n%s", err, stdout)
	}
	if report.Status != permissionStatusConfigured || !report.ConfigurationVerified || report.RuntimeVerified ||
		report.Target != target || report.AfterCount != 4 || len(report.Missing) != 0 {
		t.Fatalf("unexpected verify report: %+v", report)
	}
}

func TestClientPermissionsUnmanagedDoesNotTouchHome(t *testing.T) {
	clearPermissionEnvironment(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	policy := permissionPolicy(t, root, false, []string{"command(git diff)"})
	output := filepath.Join(root, "must-not-exist")
	args := []string{"permissions", "apply", "--client", "agy", "--fleet-config", policy,
		"--manifest", filepath.Join(root, "missing-install.json"), "--home", home, "--out", output}
	stdout, err := captureStdout(t, func() error { return runClients(args) })
	if err != nil {
		t.Fatal(err)
	}
	var report clientPermissionReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("decode unmanaged report: %v\n%s", err, stdout)
	}
	if report.Status != permissionStatusUnmanaged || report.Managed || report.ConfigurationVerified || report.RuntimeVerified {
		t.Fatalf("unexpected unmanaged report: %+v", report)
	}
	for _, path := range []string{output, filepath.Join(home, ".gemini")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("unmanaged operation created %s: %v", path, err)
		}
	}
}

func TestClientPermissionsUnmanagedIgnoresArtifactOverlap(t *testing.T) {
	clearPermissionEnvironment(t)
	root := t.TempDir()
	policy := permissionPolicy(t, root, false, []string{"command(git status)"})
	for _, action := range []string{"plan", "apply"} {
		t.Run(action, func(t *testing.T) {
			target := filepath.Join(root, action+"-must-not-exist")
			args := []string{"permissions", action, "--client", "agy", "--fleet-config", policy,
				"--manifest", filepath.Join(root, "missing-install.json"), "--target", target, "--out", target}
			stdout, err := captureStdout(t, func() error { return runClients(args) })
			if err != nil {
				t.Fatal(err)
			}
			var report clientPermissionReport
			if err := json.Unmarshal([]byte(stdout), &report); err != nil {
				t.Fatalf("decode unmanaged report: %v\n%s", err, stdout)
			}
			if report.Status != permissionStatusUnmanaged || report.Managed {
				t.Fatalf("unexpected unmanaged report: %+v", report)
			}
			if _, err := os.Lstat(target); !os.IsNotExist(err) {
				t.Fatalf("unmanaged overlap created target or artifacts: %v", err)
			}
		})
	}
}

func TestClientPermissionsShadowedGrantDoesNotWrite(t *testing.T) {
	clearPermissionEnvironment(t)
	for _, test := range []struct {
		name, list, blocking, declared string
	}{
		{name: "mcp server wildcard", list: "ask", blocking: "mcp(hindsight/*)", declared: "mcp(hindsight/list)"},
		{name: "url rule spelled with scheme and port", list: "deny", blocking: "read_url(https://example.test:8443/admin)", declared: "read_url(api.example.test)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "settings.json")
			before := []byte(`{"theme":"dark","permissions":{"` + test.list + `":["` + test.blocking + `"],"future":["keep"]}}`)
			writeFixtureFile(t, root, "settings.json", string(before))
			policy := permissionPolicy(t, root, true, []string{test.declared})
			output := filepath.Join(root, "must-not-exist")
			args := []string{"permissions", "apply", "--client", "agy", "--fleet-config", policy,
				"--manifest", filepath.Join(root, "missing-install.json"), "--target", target, "--out", output}
			if err := runClients(args); err == nil || !strings.Contains(err.Error(), "permissions."+test.list) {
				t.Fatalf("shadowed grant did not surface %s rule: %v", test.list, err)
			}
			got, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(got, before) {
				t.Fatalf("rejected target changed: %v", err)
			}
			if _, err := os.Lstat(output); !os.IsNotExist(err) {
				t.Fatalf("rejected operation created artifacts: %v", err)
			}
		})
	}
}

func TestClientPermissionsDriftThenCreatesAbsentSettingsFromEnvironment(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	policy := permissionPolicy(t, root, true, []string{"command(git diff)"})
	t.Setenv(config.FleetConfigEnv, policy)
	t.Setenv(config.WorkstationConfigEnv, "")
	common := []string{"--client", "agy", "--manifest", filepath.Join(root, "missing-install.json"), "--home", home}
	target := filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")

	verifyArgs := append([]string{"permissions", "verify"}, common...)
	stdout, verifyErr := captureStdout(t, func() error { return runClients(verifyArgs) })
	if verifyErr == nil {
		t.Fatal("missing permission settings passed verify")
	}
	var report clientPermissionReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("decode drift report: %v\n%s", err, stdout)
	}
	if report.Status != permissionStatusDrifted || report.ConfigurationVerified || report.RuntimeVerified ||
		len(report.Missing) != 1 || report.Missing[0] != "command(git diff)" {
		t.Fatalf("unexpected drift report: %+v", report)
	}
	if _, err := os.Lstat(filepath.Join(home, ".gemini")); !os.IsNotExist(err) {
		t.Fatalf("verify created the target tree: %v", err)
	}

	output := filepath.Join(root, "apply")
	applyArgs := append([]string{"permissions", "apply"}, common...)
	applyArgs = append(applyArgs, "--out", output)
	if err := runClients(applyArgs); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil || !bytes.Contains(got, []byte(`"command(git diff)"`)) {
		t.Fatalf("absent settings were not created: %v\n%s", err, got)
	}
	if _, err := os.Lstat(filepath.Join(output, "config.before")); !os.IsNotExist(err) {
		t.Fatalf("absent target produced a false backup: %v", err)
	}
}

func TestClientPermissionsEmptyManagedPolicyOmitsAbsentCandidate(t *testing.T) {
	clearPermissionEnvironment(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	policy := permissionPolicy(t, root, true, nil)
	common := []string{"--client", "agy", "--fleet-config", policy,
		"--manifest", filepath.Join(root, "missing-install.json"), "--home", home}
	target := filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")
	for _, action := range []string{"plan", "apply"} {
		t.Run(action, func(t *testing.T) {
			output := filepath.Join(root, action)
			args := append([]string{"permissions", action}, common...)
			args = append(args, "--out", output)
			if err := runClients(args); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(output, "plan.json")); err != nil {
				t.Fatalf("plan metadata missing: %v", err)
			}
			if _, err := os.Lstat(filepath.Join(output, permissionPlanExport)); !os.IsNotExist(err) {
				t.Fatalf("empty candidate written: %v", err)
			}
			if _, err := os.Lstat(target); !os.IsNotExist(err) {
				t.Fatalf("empty policy created target: %v", err)
			}
		})
	}
}

func TestClientPermissionsPlanAndApplyRejectArtifactOverlapBeforeWrites(t *testing.T) {
	clearPermissionEnvironment(t)
	policyRoot := t.TempDir()
	policy := permissionPolicy(t, policyRoot, true, []string{"command(git status)"})
	for _, action := range []string{"plan", "apply"} {
		for _, shape := range []string{"equal", "target-under-output", "output-under-target"} {
			t.Run(action+"/"+shape, func(t *testing.T) {
				root := t.TempDir()
				target := filepath.Join(root, "settings.json")
				output := target
				switch shape {
				case "target-under-output":
					output = filepath.Join(root, "artifacts")
					target = filepath.Join(output, "settings.json")
				case "output-under-target":
					output = filepath.Join(target, "artifacts")
				}
				args := []string{"permissions", action, "--client", "agy", "--fleet-config", policy,
					"--manifest", filepath.Join(root, "missing-install.json"), "--target", target, "--out", output}
				if err := runClients(args); err == nil {
					t.Fatal("overlapping target and output accepted")
				}
				for _, path := range []string{target, output} {
					if _, err := os.Lstat(path); !os.IsNotExist(err) {
						t.Fatalf("rejected operation created %s: %v", path, err)
					}
				}
			})
		}
	}
}

func TestClientPermissionsRejectSyntheticSymlinkAliasesBeforeWrites(t *testing.T) {
	clearPermissionEnvironment(t)
	root := t.TempDir()
	realParent := filepath.Join(root, "real")
	aliasParent := filepath.Join(root, "alias")
	tests := []struct {
		name, target, output, resolvedTarget, resolvedOutput string
	}{
		{
			"direct leaf", filepath.Join(root, "settings-link"), filepath.Join(realParent, "settings.json", "artifacts"),
			filepath.Join(realParent, "settings.json"), filepath.Join(realParent, "settings.json", "artifacts"),
		},
		{
			"intermediate component", filepath.Join(realParent, "settings.json"), filepath.Join(aliasParent, "settings.json"),
			filepath.Join(realParent, "settings.json"), filepath.Join(realParent, "settings.json"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolve := func(_ context.Context, candidate string) (string, error) {
				if candidate == test.target {
					return test.resolvedTarget, nil
				}
				if candidate == test.output {
					return test.resolvedOutput, nil
				}
				return candidate, nil
			}
			_, _, err := separatedClientPublicationPathsWithResolver(t.Context(), test.target, test.output, resolve)
			if err == nil || !strings.Contains(err.Error(), "target and artifact directory") {
				t.Fatalf("synthetic symlink alias accepted: %v", err)
			}
		})
	}
}

func TestClientPermissionsRejectExistingEmptySettingsForEveryAction(t *testing.T) {
	clearPermissionEnvironment(t)
	policyRoot := t.TempDir()
	policy := permissionPolicy(t, policyRoot, true, []string{"command(git status)"})
	for _, action := range []string{"plan", "apply", "verify"} {
		t.Run(action, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "settings.json")
			if err := os.WriteFile(target, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(root, "must-not-exist")
			args := []string{"permissions", action, "--client", "agy", "--fleet-config", policy,
				"--manifest", filepath.Join(root, "missing-install.json"), "--target", target}
			if action != "verify" {
				args = append(args, "--out", output)
			}
			err := runClients(args)
			if err == nil || !strings.Contains(err.Error(), "empty") {
				t.Fatalf("existing zero-byte settings accepted: %v", err)
			}
			got, readErr := os.ReadFile(target)
			if readErr != nil || len(got) != 0 {
				t.Fatalf("rejected settings changed: bytes=%d err=%v", len(got), readErr)
			}
			if _, statErr := os.Lstat(output); !os.IsNotExist(statErr) {
				t.Fatalf("rejected operation created artifacts: %v", statErr)
			}
		})
	}
}

func TestClientPermissionsRejectMalformedOrSymlinkSettings(t *testing.T) {
	clearPermissionEnvironment(t)
	for _, fixture := range []struct {
		name    string
		content string
		link    bool
	}{
		{name: "jsonc", content: "{ // comment\n  \"permissions\": {}\n}\n"},
		{name: "symlink", content: "{}\n", link: true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "home")
			settingsDir := filepath.Join(home, ".gemini", "antigravity-cli")
			if err := os.MkdirAll(settingsDir, 0o700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(settingsDir, "settings.json")
			actual := target
			if fixture.link {
				actual = filepath.Join(root, "actual.json")
			}
			if err := os.WriteFile(actual, []byte(fixture.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if fixture.link {
				if err := os.Symlink(actual, target); err != nil {
					t.Fatal(err)
				}
			}
			policy := permissionPolicy(t, root, true, []string{"command(git diff)"})
			output := filepath.Join(root, "must-not-exist")
			args := []string{"permissions", "apply", "--client", "agy", "--fleet-config", policy,
				"--manifest", filepath.Join(root, "missing-install.json"), "--home", home, "--out", output}
			if err := runClients(args); err == nil {
				t.Fatal("unsafe settings accepted")
			}
			got, err := os.ReadFile(actual)
			if err != nil || string(got) != fixture.content {
				t.Fatalf("rejected settings changed: %v", err)
			}
			if _, err := os.Lstat(output); !os.IsNotExist(err) {
				t.Fatalf("rejected operation created artifacts: %v", err)
			}
		})
	}
}

func TestClientPermissionsRejectsUnsupportedInvocation(t *testing.T) {
	clearPermissionEnvironment(t)
	for _, args := range [][]string{
		{"permissions"},
		{"permissions", "unknown"},
		{"permissions", "verify", "--client", "codex"},
		{"permissions", "verify", "--client", "agy", "positional"},
		{"permissions", "verify", "--client", "agy", "--home", "/home", "--target", "/settings.json"},
	} {
		if err := runClients(args); err == nil {
			t.Fatalf("accepted invocation: %v", args)
		}
	}
}

func TestClientPermissionPublicationRejectsRetargeting(t *testing.T) {
	clearPermissionEnvironment(t)
	root := t.TempDir()
	plannedTarget := filepath.Join(root, "planned.json")
	actualTarget := filepath.Join(root, "other.json")
	permissionPlan, err := clientsetup.PlanAGYPermissions(t.Context(), nil, config.ClientPermissions{
		Manage: true, Allow: []string{"command(git diff)"},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := permissionPublicationPlan(plannedTarget, permissionPlan)
	output := filepath.Join(root, "must-not-exist")
	if err := publishClientPlan(t.Context(), plan, actualTarget, output, nil, false); err == nil {
		t.Fatal("permission plan was published to another target")
	}
	for _, path := range []string{actualTarget, output} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("retargeted publication created %s: %v", path, err)
		}
	}
}

func clearPermissionEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv(config.FleetConfigEnv, "")
	t.Setenv(config.WorkstationConfigEnv, "")
}

func permissionPolicy(t *testing.T, root string, manage bool, grants []string) string {
	t.Helper()
	var body strings.Builder
	body.WriteString("clients:\n  selected:\n    agy:\n      permissions:\n")
	body.WriteString("        manage: ")
	if manage {
		body.WriteString("true\n")
	} else {
		body.WriteString("false\n")
	}
	body.WriteString("        allow:\n")
	if len(grants) == 0 {
		body.WriteString("          []\n")
	}
	for _, grant := range grants {
		body.WriteString("          - ")
		encoded, err := json.Marshal(grant)
		if err != nil {
			t.Fatal(err)
		}
		body.Write(encoded)
		body.WriteByte('\n')
	}
	return writeFixtureFile(t, root, "fleet.yaml", body.String())
}

func readPermissionPlan(t *testing.T, path string) clientPermissionPlanMetadata {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var plan clientPermissionPlanMetadata
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatalf("decode permission plan: %v", err)
	}
	return plan
}
