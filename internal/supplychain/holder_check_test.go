// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckCopyrightHolders_PlantedCases exercises the required planted cases for copyright holder checks (order engine-fr-rule-checks-20261027).
func TestCheckCopyrightHolders_PlantedCases(t *testing.T) {
	const declared = "example-holder <holder@example.invalid>"
	t.Run("no declared holder -> SKIP line", func(t *testing.T) { testHolderSkipCase(t) })
	t.Run("file with mismatching email -> FAIL", func(t *testing.T) { testHolderMismatchCase(t, declared) })
	t.Run("file with exact form -> PASS", func(t *testing.T) { testHolderExactCase(t, declared) })
	t.Run("file with year range -> PASS", func(t *testing.T) { testHolderYearRangeCase(t, declared) })
	t.Run("third-party holder line -> untouched", func(t *testing.T) { testHolderThirdPartyCase(t, declared) })
	t.Run("REUSE.toml annotation mismatch -> FAIL", func(t *testing.T) { testHolderReuseTomlCase(t, declared) })
}

func testHolderSkipCase(t *testing.T) {
	root := t.TempDir()
	rep, err := CheckCopyrightHolders(t.Context(), CopyrightHolderOptions{Root: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.Skipped == "" || !strings.Contains(rep.Skipped, "licensing.copyright_holder") {
		t.Errorf("expected SKIP line naming field, got %+v", rep)
	}
}

func testHolderMismatchCase(t *testing.T, declared string) {
	root := t.TempDir()
	filePath := filepath.Join(root, "pkg", "code.go")
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "// SPDX-FileCopyrightText: 2026 Example-Holder <other@example.invalid>\npackage pkg\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := CheckCopyrightHolders(t.Context(), CopyrightHolderOptions{Root: root, CopyrightHolder: declared})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rep.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %v", len(rep.Findings), rep.Findings)
	}
	want := `pkg/code.go:1: found "2026 Example-Holder <other@example.invalid>", expected "2026 example-holder <holder@example.invalid>"`
	if rep.Findings[0] != want {
		t.Errorf("got %q, want %q", rep.Findings[0], want)
	}
}

func testHolderExactCase(t *testing.T, declared string) {
	root := t.TempDir()
	filePath := filepath.Join(root, "pkg", "code.go")
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "// SPDX-FileCopyrightText: 2026 example-holder <holder@example.invalid>\npackage pkg\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := CheckCopyrightHolders(t.Context(), CopyrightHolderOptions{Root: root, CopyrightHolder: declared})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rep.Findings) != 0 {
		t.Errorf("expected 0 findings, got %v", rep.Findings)
	}
}

func testHolderYearRangeCase(t *testing.T, declared string) {
	root := t.TempDir()
	filePath := filepath.Join(root, "pkg", "code.go")
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "// SPDX-FileCopyrightText: 2024-2026 example-holder <holder@example.invalid>\npackage pkg\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := CheckCopyrightHolders(t.Context(), CopyrightHolderOptions{Root: root, CopyrightHolder: declared})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rep.Findings) != 0 {
		t.Errorf("expected 0 findings, got %v", rep.Findings)
	}
}

func testHolderThirdPartyCase(t *testing.T, declared string) {
	root := t.TempDir()
	filePath := filepath.Join(root, "vendor", "lib.c")
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "// SPDX-FileCopyrightText: 2025 Vectorize AI, Inc.\n// SPDX-License-Identifier: MIT\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := CheckCopyrightHolders(t.Context(), CopyrightHolderOptions{Root: root, CopyrightHolder: declared})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rep.Findings) != 0 {
		t.Errorf("expected 0 findings for third-party, got %v", rep.Findings)
	}
}

func testHolderReuseTomlCase(t *testing.T, declared string) {
	root := t.TempDir()
	reuseText := "version = 1\n\n[[annotations]]\npath = [\"**\"]\nSPDX-FileCopyrightText = \"2026 Example-Holder <other@example.invalid>\"\nSPDX-License-Identifier = \"EUPL-1.2\"\n"
	if err := os.WriteFile(filepath.Join(root, ReuseFile), []byte(reuseText), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := CheckCopyrightHolders(t.Context(), CopyrightHolderOptions{Root: root, CopyrightHolder: declared})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rep.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %v", len(rep.Findings), rep.Findings)
	}
	want := `REUSE.toml:5: found "2026 Example-Holder <other@example.invalid>", expected "2026 example-holder <holder@example.invalid>"`
	if rep.Findings[0] != want {
		t.Errorf("got %q, want %q", rep.Findings[0], want)
	}
}
