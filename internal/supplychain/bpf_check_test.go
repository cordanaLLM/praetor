// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCheckBPFLicenses_PlantedCases exercises the planted test cases for BPF dual licensing and GPL restrictions (order engine-fr-rule-checks-20261027).
func TestCheckBPFLicenses_PlantedCases(t *testing.T) {
	const declared = "example-holder <holder@example.invalid>"
	t.Run("prog.bpf.c with EUPL-1.2 only -> FAIL", func(t *testing.T) { testBPFProgEUPLOnlyCase(t, declared) })
	t.Run("prog.bpf.c with dual expression -> PASS", func(t *testing.T) { testBPFProgDualCase(t, declared) })
	t.Run("plain C file with SEC(\" and vmlinux.h, EUPL only -> FAIL", func(t *testing.T) { testBPFCProbeEUPLOnlyCase(t, declared) })
	t.Run("ordinary main.c with GPL-2.0-only -> FAIL", func(t *testing.T) { testBPFMainGPLCase(t, declared) })
	t.Run("vendored file under REUSE annotation of other holder with GPL-2.0-only -> PASS", func(t *testing.T) { testBPFVendoredGPLCase(t, declared) })
}

func testBPFProgEUPLOnlyCase(t *testing.T, declared string) {
	root := t.TempDir()
	code := "// SPDX-FileCopyrightText: 2026 example-holder <holder@example.invalid>\n// SPDX-License-Identifier: EUPL-1.2\n"
	if err := os.WriteFile(filepath.Join(root, "prog.bpf.c"), []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := CheckBPFLicenses(t.Context(), BPFLicenseOptions{Root: root, DeclaredHolder: declared})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rep.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %v", len(rep.Findings), rep.Findings)
	}
	want := "prog.bpf.c: kernel-loaded BPF source must carry SPDX-License-Identifier: EUPL-1.2 OR GPL-2.0-only"
	if rep.Findings[0] != want {
		t.Errorf("got %q, want %q", rep.Findings[0], want)
	}
}

func testBPFProgDualCase(t *testing.T, declared string) {
	root := t.TempDir()
	code := "// SPDX-FileCopyrightText: 2026 example-holder <holder@example.invalid>\n// SPDX-License-Identifier: EUPL-1.2 OR GPL-2.0-only\n"
	if err := os.WriteFile(filepath.Join(root, "prog.bpf.c"), []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := CheckBPFLicenses(t.Context(), BPFLicenseOptions{Root: root, DeclaredHolder: declared})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rep.Findings) != 0 {
		t.Errorf("expected 0 findings, got %v", rep.Findings)
	}
}

func testBPFCProbeEUPLOnlyCase(t *testing.T, declared string) {
	root := t.TempDir()
	code := "// SPDX-FileCopyrightText: 2026 example-holder <holder@example.invalid>\n// SPDX-License-Identifier: EUPL-1.2\n" +
		"#include \"vmlinux.h\"\nSEC(\"tracepoint/syscalls/sys_enter_open\")\nint test(void) { return 0; }\n"
	if err := os.WriteFile(filepath.Join(root, "probe.c"), []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := CheckBPFLicenses(t.Context(), BPFLicenseOptions{Root: root, DeclaredHolder: declared})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rep.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %v", len(rep.Findings), rep.Findings)
	}
	want := "probe.c: kernel-loaded BPF source must carry SPDX-License-Identifier: EUPL-1.2 OR GPL-2.0-only"
	if rep.Findings[0] != want {
		t.Errorf("got %q, want %q", rep.Findings[0], want)
	}
}

func testBPFMainGPLCase(t *testing.T, declared string) {
	root := t.TempDir()
	code := "// SPDX-FileCopyrightText: 2026 example-holder <holder@example.invalid>\n// SPDX-License-Identifier: GPL-2.0-only\nint main(void) { return 0; }\n"
	if err := os.WriteFile(filepath.Join(root, "main.c"), []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := CheckBPFLicenses(t.Context(), BPFLicenseOptions{Root: root, DeclaredHolder: declared})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rep.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %v", len(rep.Findings), rep.Findings)
	}
	want := "main.c: own file carries GPL-2.0-only licence"
	if rep.Findings[0] != want {
		t.Errorf("got %q, want %q", rep.Findings[0], want)
	}
}

func testBPFVendoredGPLCase(t *testing.T, declared string) {
	root := t.TempDir()
	reuseText := "version = 1\n\n[[annotations]]\npath = [\"third_party/upstream/**\"]\n" +
		"SPDX-FileCopyrightText = \"2025 Upstream Author\"\nSPDX-License-Identifier = \"GPL-2.0-only\"\n"
	if err := os.WriteFile(filepath.Join(root, ReuseFile), []byte(reuseText), 0o644); err != nil {
		t.Fatal(err)
	}
	codePath := filepath.Join(root, "third_party", "upstream", "lib.c")
	if err := os.MkdirAll(filepath.Dir(codePath), 0o755); err != nil {
		t.Fatal(err)
	}
	code := "// SPDX-FileCopyrightText: 2025 Upstream Author\n// SPDX-License-Identifier: GPL-2.0-only\n"
	if err := os.WriteFile(codePath, []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := CheckBPFLicenses(t.Context(), BPFLicenseOptions{Root: root, DeclaredHolder: declared})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rep.Findings) != 0 {
		t.Errorf("expected 0 findings for vendored GPL code, got %v", rep.Findings)
	}
}
