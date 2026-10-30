// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

func TestBuildFakeGitleaks_Positive(t *testing.T) {
	dir := t.TempDir()
	bin := testsupport.BuildFakeGitleaks(t, dir)
	reportPath := filepath.Join(dir, "report.json")
	out, err := util.RunCommand(t.Context(), dir, bin, "detect", "--report-path", reportPath)
	if err != nil {
		t.Fatalf("run fake gitleaks failed: %v: %s", err, out)
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report failed: %v", err)
	}
	if string(data) != "[]" {
		t.Fatalf("report content = %q, want []", string(data))
	}
}

func TestBuildFakeMise_PositiveAndNegative(t *testing.T) {
	dir := t.TempDir()
	normalHome := t.TempDir()
	fakeReal := filepath.Join(dir, testsupport.ExecutableName("gitleaks"))
	miseBin := testsupport.BuildFakeMise(t, dir, normalHome, fakeReal)

	out, err := util.RunCommand(t.Context(), dir, miseBin, "which", "gitleaks")
	if err != nil {
		t.Fatalf("run fake mise which failed: %v: %s", err, out)
	}
	if out != fakeReal {
		t.Fatalf("which output = %q, want %q", out, fakeReal)
	}
}
