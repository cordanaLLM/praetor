// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Order: engine-fr-rule-checks-20261027. Offline planted cases exercise real files and Git.
func TestRepositoryChecksEditorSelection(t *testing.T) {
	cases := []struct {
		name          string
		clients       []string
		tracked, fail bool
	}{
		{"tracked excluded", []string{"claude"}, true, true},
		{"untracked excluded", []string{"claude"}, false, false},
		{"selection absent", nil, true, false},
		{"explicit empty", []string{}, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			initTestGit(t, root)
			mustWrite(t, filepath.Join(root, ".windsurfrules"), "stale\n")
			if tc.tracked {
				checkGit(t, root, "add", ".windsurfrules")
			}
			line, err := AuditEditorSelection(t.Context(), &config.Manifest{AgentClients: tc.clients}, root)
			checkRepositoryVerdict(t, line, err, tc.fail, "editor-selection")
			if tc.fail && !strings.Contains(fmt.Sprint(err), ".windsurfrules") {
				t.Fatal(err)
			}
		})
	}
}

func checkGit(t *testing.T, root string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := util.RunGitProbe(ctx, root, 64*1024, args...); err != nil {
		t.Fatal(err)
	}
}

func checkRepositoryVerdict(t *testing.T, line string, err error, fail bool, gate string) {
	t.Helper()
	if (err != nil) != fail {
		t.Fatalf("%s: line=%q err=%v; want failure=%t", gate, line, err, fail)
	}
	output := line
	if err != nil {
		output = err.Error()
	}
	if !strings.Contains(output, gate) {
		t.Fatalf("missing gate name: %s", output)
	}
	t.Log(output)
}

func TestRepositoryChecksRenovate(t *testing.T) {
	cases := []struct {
		name, config, module, want string
		fail                       bool
	}{
		{"missing entry", `{}`, "", "renovate.json", true},
		{"no config", "", "", "No Renovate configuration", false},
		{"engine identity", `{}`, "module github.com/cordanaLLM/praetor\n", "[SKIP]", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.config != "" {
				mustWrite(t, filepath.Join(root, "renovate.json"), tc.config)
			}
			if tc.module != "" {
				mustWrite(t, filepath.Join(root, "go.mod"), tc.module)
			}
			line, err := AuditRenovateEngineBumps(t.Context(), root)
			checkRepositoryVerdict(t, line, err, tc.fail, "renovate-engine-bumps")
			if !strings.Contains(line+fmt.Sprint(err), tc.want) {
				t.Fatalf("missing %q: %s %v", tc.want, line, err)
			}
		})
	}
}

const msrvCrate = "[package]\nname = 'fixture'\nversion = '0.1.0'\nrust-version = '1.80'\n"
const msrvJob = "jobs:\n  msrv:\n    runs-on: ubuntu-latest\n    steps:\n"

func TestRepositoryChecksRustMSRV(t *testing.T) {
	cases := []struct {
		name, cargo, pin, workflow, want string
		fail                             bool
	}{
		{"pinned minimum", msrvCrate, "1.80", "", "[PASS]", false},
		{"stable unproven", msrvCrate, "stable", "", "Cargo.toml", true},
		{"action and check", msrvCrate, "stable", msrvJob + "      - uses: dtolnay/rust-toolchain@1.80\n      - run: cargo check\n", "[PASS]", false},
		{"install without cargo", msrvCrate, "stable", msrvJob + "      - uses: dtolnay/rust-toolchain@1.80\n", "Cargo.toml", true},
		{"action input", msrvCrate, "stable", msrvJob + "      - uses: dtolnay/rust-toolchain@stable\n        with:\n          toolchain: '1.80'\n      - run: cargo test\n", "[PASS]", false},
		{"rustup install", msrvCrate, "stable", msrvJob + "      - run: rustup toolchain install 1.80\n      - run: cargo +1.80 build\n", "[PASS]", false},
		{"no declaration", "[package]\nname = 'fixture'\n", "stable", "", "No rust-version", false},
		{"no cargo", "", "", "", "[SKIP]", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.cargo != "" {
				mustWrite(t, filepath.Join(root, "Cargo.toml"), tc.cargo)
			}
			if tc.pin != "" {
				mustWrite(t, filepath.Join(root, "rust-toolchain.toml"), "[toolchain]\nchannel = '"+tc.pin+"'\n")
			}
			if tc.workflow != "" {
				mustWrite(t, filepath.Join(root, ".github/workflows/msrv.yml"), tc.workflow)
			}
			line, err := AuditRustMSRV(t.Context(), root)
			checkRepositoryVerdict(t, line, err, tc.fail, "rust-msrv")
			if !strings.Contains(line+fmt.Sprint(err), tc.want) {
				t.Fatalf("missing %q: %s %v", tc.want, line, err)
			}
		})
	}
}
