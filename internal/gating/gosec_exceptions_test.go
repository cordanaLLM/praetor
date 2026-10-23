// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func frozenGosecExceptionsConfig(t *testing.T, entry string, findings bool) *stageConfig {
	t.Helper()
	dir := setupGosecPlantedRepo(t)
	writeFile(t, filepath.Join(dir, ".standards.yaml"), "version: 1\n"+entry)
	cfg := gosecRatchetStageConfig(dir, &PipelineReport{})
	cfg.run = func(_ context.Context, cmdDir, name string, _ ...string) (string, error) {
		switch name {
		case "go":
			return cmdDir + "\n", nil
		case "govulncheck":
			return cleanGovulnStream, nil
		case "gosec":
			if !findings {
				return `{"Issues":[]}`, nil
			}
			return fmt.Sprintf(`{"Issues":[{"rule_id":"G204","file":%q,"line":"6","details":"planted command execution"}]}`, filepath.Join(dir, "main.go")), fmt.Errorf("gosec exit 1")
		default:
			return "", fmt.Errorf("unexpected frozen runner command %s", name)
		}
	}
	return cfg
}

func datedGosecEntry(path, rule string, expires time.Time) string {
	return fmt.Sprintf("exceptions:\n  - rule: gosec\n    path: %s\n    gosec_rules: [%s]\n    category: D\n    entered: %s\n    expires: %s\n    reason: Pending command execution redesign\n", path, rule, expires.AddDate(0, 0, -120).Format("2006-01-02"), expires.Format("2006-01-02"))
}

func TestGosecExceptions_FrozenStage(t *testing.T) {
	today := time.Now().UTC()
	cases := []struct {
		name, entry    string
		findings, pass bool
		want           string
	}{
		{"live", datedGosecEntry("main.go", "G204", today.AddDate(0, 0, 1)), true, true, "1 gosec findings excused"},
		{"removed", "", true, false, "G204"},
		{"expired", datedGosecEntry("main.go", "G204", today.AddDate(0, 0, -1)), true, false, "G204"},
		{"stale", datedGosecEntry("main.go", "G204", today), false, false, "stale gosec exception"},
		{"different rule", datedGosecEntry("main.go", "G304", today), true, false, "G204"},
		{"different path", datedGosecEntry("other.go", "G204", today), true, false, "G204"},
		{"expires today", datedGosecEntry("main.go", "G204", today), true, true, "main.go"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := frozenGosecExceptionsConfig(t, tc.entry, tc.findings)
			msg, err := runSecurityStage(t.Context(), cfg)
			if (err == nil) != tc.pass {
				t.Fatalf("pass=%v: msg=%s err=%v", tc.pass, msg, err)
			}
			if err != nil {
				msg += err.Error()
			}
			if !strings.Contains(msg, tc.want) {
				t.Fatalf("missing %q: %s", tc.want, msg)
			}
		})
	}
}

func TestGosecExceptions_FrozenMultipleModules(t *testing.T) {
	cfg := frozenGosecExceptionsConfig(t, datedGosecEntry("nested/main.go", "G204", time.Now().UTC()), true)
	writeFile(t, filepath.Join(cfg.repoDir, "nested", "main.go"), "package nested\nfunc main() {}\n")
	cfg.modules = []goModule{{file: "go.mod", dir: cfg.repoDir}, {file: "nested/go.mod", dir: filepath.Join(cfg.repoDir, "nested")}}
	cfg.modulesDiscovered = true
	original := cfg.run
	cfg.run = func(ctx context.Context, dir, name string, args ...string) (string, error) {
		if name != "gosec" {
			return original(ctx, dir, name, args...)
		}
		if dir == cfg.repoDir {
			return `{"Issues":[]}`, nil
		}
		return fmt.Sprintf(`{"Issues":[{"rule_id":"G204","file":%q,"line":"2","details":"planted command execution"},{"rule_id":"G204","file":%q,"line":"2","details":"second command"}]}`, filepath.Join(dir, "main.go"), filepath.Join(dir, "main.go")), fmt.Errorf("gosec exit 1")
	}
	msg, err := runSecurityStage(t.Context(), cfg)
	if err != nil || !strings.Contains(msg, "2 gosec findings excused") {
		t.Fatalf("msg=%s err=%v", msg, err)
	}
}

func TestGosecExceptions_FrozenRunnerFailures(t *testing.T) {
	for _, output := range []string{"", `{"Issues":[]}`, `not json`} {
		t.Run(output, func(t *testing.T) {
			cfg := frozenGosecExceptionsConfig(t, datedGosecEntry("main.go", "G204", time.Now().UTC()), true)
			original := cfg.run
			cfg.run = func(ctx context.Context, dir, name string, args ...string) (string, error) {
				if name == "gosec" {
					return output, fmt.Errorf("scanner failed")
				}
				return original(ctx, dir, name, args...)
			}
			if _, err := runSecurityStage(t.Context(), cfg); err == nil {
				t.Fatal("live exception hid failed scanner")
			}
		})
	}
}

func TestGosecExceptions_NoModuleIsStale(t *testing.T) {
	cfg := frozenGosecExceptionsConfig(t, datedGosecEntry("main.go", "G204", time.Now().UTC()), false)
	cfg.modules = nil
	cfg.modulesDiscovered = true
	_, err := runGoSecurity(t.Context(), cfg)
	if err == nil || !strings.Contains(err.Error(), "stale gosec exception") {
		t.Fatalf("expected stale entry without a module, got %v", err)
	}
}
