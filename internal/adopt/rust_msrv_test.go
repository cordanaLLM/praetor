// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// setupRustMSRVFixture creates a temporary directory with optional Cargo.toml and manifest files.
func setupRustMSRVFixture(t *testing.T, manifest, cargoTOML string) string {
	t.Helper()
	root := t.TempDir()
	if cargoTOML != "" {
		mustWrite(t, filepath.Join(root, "Cargo.toml"), cargoTOML)
	}
	if manifest != "" {
		mustWrite(t, filepath.Join(root, manifestFile), manifest)
	}
	return root
}

// assertRustMSRVVerdict asserts that line and err match the expected failure or pass pattern.
func assertRustMSRVVerdict(t *testing.T, line string, err error, wantErr, wantPass string) {
	t.Helper()
	if wantErr != "" {
		if err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Fatalf("AuditRustMSRV = %q, %v; want refusal containing %q", line, err, wantErr)
		}
		return
	}
	if err != nil || !strings.Contains(line, "[PASS]") || (wantPass != "" && !strings.Contains(line, wantPass)) {
		t.Fatalf("AuditRustMSRV = %q, %v; want proof containing %q", line, err, wantPass)
	}
}

// TestRustMSRVDeclaredFileBytes exercises declared, default and ceiling workflow byte bounds.
func TestRustMSRVDeclaredFileBytes(t *testing.T) {
	cases := []struct {
		name, manifest, want string
		size                 int
	}{
		{"declared larger workflow", "verification:\n  max_file_bytes: 262144\n", "", 70 << 10},
		{"undeclared larger workflow", "", "declare verification.max_file_bytes in .standards.yaml", 70 << 10},
		{"default exact", "", "", maxVerificationInputBytes},
		{"declared exact", "verification:\n  max_file_bytes: 71680\n", "", 70 << 10},
		{"declared exceeded", "verification:\n  max_file_bytes: 71680\n", "declare verification.max_file_bytes in .standards.yaml", (70 << 10) + 1},
		{"ceiling exact", fmt.Sprintf("verification:\n  max_file_bytes: %d\n", contextopt.MaxSourceBytes), "", contextopt.MaxSourceBytes},
		{"ceiling exceeded", fmt.Sprintf("verification:\n  max_file_bytes: %d\n", contextopt.MaxSourceBytes), "max_file_bytes is already at its ceiling 1048576", contextopt.MaxSourceBytes + 1},
		{"undeclared ceiling exceeded", "", "max_file_bytes is already at its ceiling 1048576", contextopt.MaxSourceBytes + 1},
		{"unreadable manifest refused", "version: [\n", "failed to parse manifest at .standards.yaml", maxVerificationInputBytes},
		{"invalid verification declaration", "verification:\n  max_file_bytes: -1\n", "verification.max_file_bytes must be an integer", maxVerificationInputBytes},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := setupRustMSRVFixture(t, tc.manifest, msrvCrate)
			workflow := msrvJob + "      - uses: dtolnay/rust-toolchain@1.80\n      - run: cargo check\n"
			workflow += "#" + strings.Repeat("x", tc.size-len(workflow)-2) + "\n"
			mustWrite(t, filepath.Join(root, ".github/workflows/msrv.yml"), workflow)
			line, err := AuditRustMSRV(t.Context(), nil, root)
			assertRustMSRVVerdict(t, line, err, tc.want, "msrv.yml job msrv")
		})
	}
}

// TestRustMSRVRequestedLimits covers default, raised, exact and invalid requested policies.
func TestRustMSRVRequestedLimits(t *testing.T) {
	cases := []struct {
		name      string
		requested []*VerificationLimits
		want      string
	}{
		{"omitted", nil, "verification discovery exceeds 1 entries"},
		{"nil", []*VerificationLimits{nil}, "verification discovery exceeds 1 entries"},
		{"unchanged", []*VerificationLimits{{}}, "verification discovery exceeds 1 entries"},
		{"raised exact", []*VerificationLimits{{MaxEntries: 3}}, ""},
		{"raised exceeded", []*VerificationLimits{{MaxEntries: 2}}, "verification discovery exceeds 2 entries"},
		{"negative", []*VerificationLimits{{MaxEntries: -1}}, "max_entries (--verification-max-entries) must be 1..200000"},
		{"multiple", []*VerificationLimits{nil, nil}, "expected at most one requested verification policy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := setupRustMSRVFixture(t, "verification:\n  max_entries: 1\n", msrvCrate)
			mustWrite(t, filepath.Join(root, "rust-toolchain"), "1.80\n")
			line, err := AuditRustMSRV(t.Context(), nil, root, tc.requested...)
			assertRustMSRVVerdict(t, line, err, tc.want, "Cargo.toml 1.80")
		})
	}
}

// TestRustMSRVSelectedManifestLimits verifies that a selected manifest passes its limits to
// AuditRustMSRV, while nil keeps the root default limits.
func TestRustMSRVSelectedManifestLimits(t *testing.T) {
	root := t.TempDir()
	// Cargo.toml of 200,000 bytes: root default (65536) refuses; manifest with 262144 admits it.
	largeCargo := "[package]\nname='fixture'\nversion='0.1.0'\nrust-version='1.80'\n#" + strings.Repeat("x", 200000-80) + "\n"
	mustWrite(t, filepath.Join(root, "Cargo.toml"), largeCargo)
	mustWrite(t, filepath.Join(root, "rust-toolchain"), "1.80\n")

	line, err := AuditRustMSRV(t.Context(), nil, root)
	assertRustMSRVVerdict(t, line, err, "metadata exceeds byte bounds (file=65536", "")

	selected := &config.Manifest{
		Verification: &config.VerificationPolicy{MaxFileBytes: 262144},
	}
	line, err = AuditRustMSRV(t.Context(), selected, root)
	assertRustMSRVVerdict(t, line, err, "", "Cargo.toml 1.80")

	// Stricter selected limit refuses what root admits: 50,000-byte Cargo.toml.
	midCargo := "[package]\nname='fixture'\nversion='0.1.0'\nrust-version='1.80'\n#" + strings.Repeat("x", 50000-80) + "\n"
	mustWrite(t, filepath.Join(root, "Cargo.toml"), midCargo)
	line, err = AuditRustMSRV(t.Context(), nil, root)
	assertRustMSRVVerdict(t, line, err, "", "Cargo.toml 1.80")

	stricter := &config.Manifest{
		Verification: &config.VerificationPolicy{MaxFileBytes: 10000},
	}
	line, err = AuditRustMSRV(t.Context(), stricter, root)
	assertRustMSRVVerdict(t, line, err, "metadata exceeds byte bounds (file=10000", "")
}

// TestRustMSRVWorkspaceAndBounds covers workspace inheritance (inherited minimum passes, an unproven member or a
// missing inheritance fails) and the manifest bound, including an unproven 129th manifest under max_files 256.
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
			root := setupRustMSRVFixture(t, "", tc.root)
			mustWrite(t, filepath.Join(root, "rust-toolchain"), tc.pin+"\n")
			if tc.member != "" {
				mustWrite(t, filepath.Join(root, "crates/a/Cargo.toml"), tc.member)
			}
			line, err := AuditRustMSRV(t.Context(), nil, root)
			checkRepositoryVerdict(t, line, err, tc.fail, "rust-msrv")
		})
	}
	t.Run("manifest bound", func(t *testing.T) {
		root := t.TempDir()
		for i := 0; i <= maxVerificationInputs; i++ {
			mustWrite(t, filepath.Join(root, fmt.Sprintf("crates/c%d/Cargo.toml", i)), msrvCrate)
		}
		line, err := AuditRustMSRV(t.Context(), nil, root)
		checkRepositoryVerdict(t, line, err, true, "rust-msrv")
	})
	t.Run("manifest 129th unproven rust-version", func(t *testing.T) {
		root := setupRustMSRVFixture(t, "verification:\n  max_files: 256\n", "")
		// 129 manifests: first 128 have no rust-version, 129th has unproven rust-version.
		for i := 0; i < 128; i++ {
			mustWrite(t, filepath.Join(root, fmt.Sprintf("crates/c%03d/Cargo.toml", i)), fmt.Sprintf("[package]\nname='c%03d'\nversion='0.1.0'\n", i))
		}
		mustWrite(t, filepath.Join(root, "crates/c128/Cargo.toml"), "[package]\nname='c128'\nversion='0.1.0'\nrust-version='1.80'\n")
		line, err := AuditRustMSRV(t.Context(), nil, root)
		if err == nil || strings.Contains(line, "[PASS]") {
			t.Fatalf("AuditRustMSRV = %q, %v; want not PASS for unproven 129th manifest", line, err)
		}
	})
}

// TestRustMSRVWorkflowRejectsFalseProofs refuses workflow text that only looks like a minimum-version build: a
// heredoc, a dead conditional, a changed default toolchain, --ignore-rust-version, an echo or a comment.
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
			root := setupRustMSRVFixture(t, "", msrvCrate)
			prefix := "      - uses: dtolnay/rust-toolchain@1.80\n"
			if tc.name == "cargo before install" {
				prefix = ""
			}
			mustWrite(t, filepath.Join(root, ".github/workflows/msrv.yml"), msrvJob+prefix+tc.steps)
			line, err := AuditRustMSRV(t.Context(), nil, root)
			checkRepositoryVerdict(t, line, err, true, "rust-msrv")
		})
	}
}

// TestRepositoryChecksCancellationAndEngine runs each repository check with a cancelled context and expects a
// refusal, never a verdict; against the engine's own tree each check returns its verdict without error.
func TestRepositoryChecksCancellationAndEngine(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	checks := []func() (string, error){
		func() (string, error) { return AuditEditorSelection(ctx, &config.Manifest{}, root) },
		func() (string, error) { return AuditRenovateEngineBumps(ctx, root) },
		func() (string, error) { return AuditRustMSRV(ctx, nil, root) },
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
		func() (string, error) { return AuditRustMSRV(t.Context(), manifest, engine) },
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

// TestRustMSRVSkipBeforeInputBounds pins that repositories without Cargo manifests skip before input size bounds are evaluated.
func TestRustMSRVSkipBeforeInputBounds(t *testing.T) {
	const oversizedWorkflowBytes = maxVerificationInputBytes + 1
	oversizedWorkflow := strings.Repeat("#", oversizedWorkflowBytes)

	t.Run("no cargo with oversized workflow skips first", func(t *testing.T) {
		root := t.TempDir()
		mustWrite(t, filepath.Join(root, ".github/workflows/ci.yml"), oversizedWorkflow)
		line, err := AuditRustMSRV(t.Context(), nil, root)
		if err != nil {
			t.Fatalf("expected skip without error, got err=%v line=%q", err, line)
		}
		if line != "[SKIP] rust-msrv: no Cargo.toml found." {
			t.Fatalf("expected skip line, got %q", line)
		}
	})

	t.Run("cargo with rust-version passes", func(t *testing.T) {
		root := t.TempDir()
		mustWrite(t, filepath.Join(root, "Cargo.toml"), msrvCrate)
		mustWrite(t, filepath.Join(root, "rust-toolchain"), "1.80\n")
		line, err := AuditRustMSRV(t.Context(), nil, root)
		checkRepositoryVerdict(t, line, err, false, "rust-msrv")
		if !strings.HasPrefix(line, "[PASS]") {
			t.Fatalf("expected pass verdict, got line=%q", line)
		}
	})

	t.Run("boundary cargo with exact limit workflow passes", func(t *testing.T) {
		root := t.TempDir()
		mustWrite(t, filepath.Join(root, "Cargo.toml"), msrvCrate)
		mustWrite(t, filepath.Join(root, "rust-toolchain"), "1.80\n")
		mustWrite(t, filepath.Join(root, ".github/workflows/ci.yml"), strings.Repeat("#", maxVerificationInputBytes))
		line, err := AuditRustMSRV(t.Context(), root)
		checkRepositoryVerdict(t, line, err, false, "rust-msrv")
		if !strings.HasPrefix(line, "[PASS]") {
			t.Fatalf("expected pass verdict, got line=%q", line)
		}
	})

	t.Run("boundary cargo with oversized workflow reports bound", func(t *testing.T) {
		root := t.TempDir()
		mustWrite(t, filepath.Join(root, "Cargo.toml"), msrvCrate)
		mustWrite(t, filepath.Join(root, ".github/workflows/ci.yml"), oversizedWorkflow)
		line, err := AuditRustMSRV(t.Context(), nil, root)
		checkRepositoryVerdict(t, line, err, true, "rust-msrv")
		if !strings.Contains(fmt.Sprint(err), "metadata exceeds byte bounds") {
			t.Fatalf("expected metadata exceeds byte bounds, got %v (line=%q)", err, line)
		}
	})
}
