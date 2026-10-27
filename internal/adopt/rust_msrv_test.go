// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

func TestRustMSRVWorkspaceAndBounds(t *testing.T) {
	cases := []struct {
		name, root, member, pin string
		fail                    bool
	}{
		{"inherited minimum", "[workspace]\nmembers=['crates/a']\n[workspace.package]\nrust-version='1.80'\n", "[package]\nname='a'\nrust-version.workspace=true\n", "1.80.3", false},
		{"member unproven", "[workspace]\nmembers=['crates/a']\n", msrvCrate, "stable", true},
		{"missing inheritance", "[workspace]\nmembers=['crates/a']\n", "[package]\nname='a'\nrust-version.workspace=true\n", "1.80", true},
		{"invalid type", "[package]\nrust-version=1.80\n", "", "1.80", true},
		{"malformed TOML", "[package\n", "", "1.80", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			mustWrite(t, filepath.Join(root, "Cargo.toml"), tc.root)
			mustWrite(t, filepath.Join(root, "rust-toolchain"), tc.pin+"\n")
			if tc.member != "" {
				mustWrite(t, filepath.Join(root, "crates/a/Cargo.toml"), tc.member)
			}
			line, err := AuditRustMSRV(t.Context(), root)
			checkRepositoryVerdict(t, line, err, tc.fail, "rust-msrv")
		})
	}
	t.Run("manifest bound", func(t *testing.T) {
		root := t.TempDir()
		for i := 0; i <= maxVerificationInputs; i++ {
			mustWrite(t, filepath.Join(root, fmt.Sprintf("crates/c%d/Cargo.toml", i)), msrvCrate)
		}
		line, err := AuditRustMSRV(t.Context(), root)
		checkRepositoryVerdict(t, line, err, true, "rust-msrv")
	})
}

func TestRustMSRVWorkflowRejectsFalseProofs(t *testing.T) {
	cases := []struct{ name, steps string }{
		{"heredoc", "      - run: |\n          cat <<EOF\n          cargo check\n          EOF\n"},
		{"conditional", "      - run: |\n          if false\n          then\n          cargo check\n          fi\n"},
		{"changed default", "      - run: rustup default stable\n      - run: cargo check\n"},
		{"ignore minimum", "      - run: cargo check --ignore-rust-version\n"},
		{"echo", "      - run: echo 'cargo check'\n"},
		{"comment", "      - run: '# cargo check'\n"},
		{"wrong override", "      - run: cargo +1.81 check\n"},
		{"ignored error", "      - run: cargo check || true\n"},
		{"different toolchain later", "      - uses: dtolnay/rust-toolchain@stable\n      - run: cargo check\n"},
		{"cargo before install", "      - run: cargo check\n      - uses: dtolnay/rust-toolchain@1.80\n"},
		{"skipped build", "      - run: cargo check\n        if: 'false'\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			mustWrite(t, filepath.Join(root, "Cargo.toml"), msrvCrate)
			prefix := "      - uses: dtolnay/rust-toolchain@1.80\n"
			if tc.name == "cargo before install" {
				prefix = ""
			}
			mustWrite(t, filepath.Join(root, ".github/workflows/msrv.yml"), msrvJob+prefix+tc.steps)
			line, err := AuditRustMSRV(t.Context(), root)
			checkRepositoryVerdict(t, line, err, true, "rust-msrv")
		})
	}
}

func TestRepositoryChecksCancellationAndEngine(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	checks := []func() (string, error){
		func() (string, error) { return AuditEditorSelection(ctx, &config.Manifest{}, root) },
		func() (string, error) { return AuditRenovateEngineBumps(ctx, root) },
		func() (string, error) { return AuditRustMSRV(ctx, root) },
	}
	for i, check := range checks {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			if _, err := check(); err == nil {
				t.Fatal("cancelled check passed")
			}
		})
	}
	engine := filepath.Join("..", "..")
	manifest := &config.Manifest{}
	for _, check := range []func() (string, error){
		func() (string, error) { return AuditEditorSelection(t.Context(), manifest, engine) },
		func() (string, error) { return AuditRenovateEngineBumps(t.Context(), engine) },
		func() (string, error) { return AuditRustMSRV(t.Context(), engine) },
	} {
		line, err := check()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(line, "[PASS]") && !strings.Contains(line, "[SKIP]") {
			t.Fatal(line)
		}
		t.Log(line)
	}
}
