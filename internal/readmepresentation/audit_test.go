// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package readmepresentation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

func TestAudit_Positive_CompliantReadme(t *testing.T) {
	tempDir := t.TempDir()
	exemplarData, err := os.ReadFile(filepath.Join("testdata", "README-vmafx-2929.md"))
	if err != nil {
		t.Fatalf("read exemplar: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "README.md"), exemplarData, 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}

	line, err := Audit(t.Context(), Options{
		Root:  tempDir,
		Today: time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Audit unexpected error: %v", err)
	}
	if !strings.HasPrefix(line, "[PASS] README presentation:") {
		t.Errorf("expected [PASS], got: %s", line)
	}
}

func TestAudit_Negative_NonCompliantFailsWithoutException(t *testing.T) {
	tempDir := t.TempDir()
	badContent := "# My Project\n\nSome unbolded text.\n\n[![Bad](https://img.shields.io/badge/bad-midpage-red?style=for-the-badge)](https://example.com)\n"
	if err := os.WriteFile(filepath.Join(tempDir, "README.md"), []byte(badContent), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}

	_, err := Audit(t.Context(), Options{
		Root:  tempDir,
		Today: time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("Audit expected error for non-compliant README, got nil")
	}
	if !strings.Contains(err.Error(), "[FAIL] README presentation:") {
		t.Errorf("expected [FAIL] in error, got: %s", err.Error())
	}
}

func TestAudit_Boundary_LiveExceptionPassesRatchet(t *testing.T) {
	tempDir := t.TempDir()
	badContent := "# My Project\n\nSome unbolded text.\n\n[![Bad](https://img.shields.io/badge/bad-midpage-red?style=for-the-badge)](https://example.com)\n"
	if err := os.WriteFile(filepath.Join(tempDir, "README.md"), []byte(badContent), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}

	today := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	liveException := config.Exception{
		Rule:     config.ExceptionRuleReadmePresentation,
		Path:     "README.md",
		Reason:   "adopter migration in progress",
		Category: "D",
		Entered:  "2026-10-22",
		Expires:  "2027-02-19",
	}

	line, err := Audit(t.Context(), Options{
		Root:       tempDir,
		Exceptions: []config.Exception{liveException},
		Today:      today,
	})
	if err != nil {
		t.Fatalf("Audit unexpected error with live exception: %v", err)
	}
	if !strings.HasPrefix(line, "[PASS] README presentation: declared gap") {
		t.Errorf("expected declared gap [PASS], got: %s", line)
	}
	if !strings.Contains(line, "adopter migration in progress") {
		t.Errorf("expected reason in line, got: %s", line)
	}
}

func TestAudit_Boundary_StaleExceptionRefused(t *testing.T) {
	tempDir := t.TempDir()
	exemplarData, err := os.ReadFile(filepath.Join("testdata", "README-vmafx-2929.md"))
	if err != nil {
		t.Fatalf("read exemplar: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "README.md"), exemplarData, 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}

	today := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	staleException := config.Exception{
		Rule:     config.ExceptionRuleReadmePresentation,
		Path:     "README.md",
		Reason:   "adopter migration in progress",
		Category: "D",
		Entered:  "2026-10-22",
		Expires:  "2027-02-19",
	}

	_, err = Audit(t.Context(), Options{
		Root:       tempDir,
		Exceptions: []config.Exception{staleException},
		Today:      today,
	})
	if err == nil {
		t.Fatal("Audit expected error for stale exception, got nil")
	}
	if !strings.Contains(err.Error(), "excuse no findings") {
		t.Errorf("expected stale exception message, got: %s", err.Error())
	}
}

func TestAudit_Boundary_ExpiredExceptionRefused(t *testing.T) {
	tempDir := t.TempDir()
	badContent := "# My Project\n\nSome unbolded text.\n\n[![Bad](https://img.shields.io/badge/bad-midpage-red?style=for-the-badge)](https://example.com)\n"
	if err := os.WriteFile(filepath.Join(tempDir, "README.md"), []byte(badContent), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}

	today := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	expiredException := config.Exception{
		Rule:     config.ExceptionRuleReadmePresentation,
		Path:     "README.md",
		Reason:   "adopter migration in progress",
		Category: "B",
		Entered:  "2026-10-10",
		Expires:  "2026-10-13",
	}

	_, err := Audit(t.Context(), Options{
		Root:       tempDir,
		Exceptions: []config.Exception{expiredException},
		Today:      today,
	})
	if err == nil {
		t.Fatal("Audit expected error for expired exception, got nil")
	}
	if !strings.Contains(err.Error(), "expired on 2026-10-13") {
		t.Errorf("expected expired exception message, got: %s", err.Error())
	}
}
