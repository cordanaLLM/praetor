// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/readmepresentation"
)

func TestReadmeMixedBaselineAdoptAuditDocsLoop(t *testing.T) {
	repo, source := adoptSuccessLineFixture(t, map[string]string{"main.go": cliDebt, "README.md": "# Widgets\n"})
	if out, err := adoptWithFlags(t, repo, source); err != nil {
		t.Fatalf("initial adoption: %v\n%s", err, out)
	}
	path := filepath.Join(repo, cliBaseline)
	base, err := baseline.LoadBaseline(path)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := readmepresentation.CheckFile(t.Context(), repo, "README.md")
	if err != nil || len(findings) == 0 {
		t.Fatalf("plant README debt: %v, findings: %v", err, findings)
	}
	base.Infractions = append(base.Infractions, readmepresentation.ToBaseline(findings)...)
	if err := baseline.SaveBaseline(path, base); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		checkReadmeCountLoop(t, repo, source)
	}
}

func checkReadmeCountLoop(t *testing.T, repo, source string) {
	t.Helper()
	if out, err := adoptWithFlags(t, repo, source, "--record-baseline=false"); err != nil {
		t.Fatalf("re-adoption: %v\n%s", err, out)
	}
	before := readFixtureFile(t, repo, "README.md")
	manifest, err := config.LoadManifest(filepath.Join(repo, config.ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	opts := &auditOptions{rootDir: repo, baselinePath: filepath.Join(repo, cliBaseline), maxStale: -1}
	if err := auditBaselineAndInvariants(t.Context(), opts); err != nil {
		t.Fatalf("HISS audit after adoption: %v", err)
	}
	if err := auditReadmePresentation(t.Context(), manifest, opts); err != nil {
		t.Fatalf("presentation audit after adoption: %v", err)
	}
	if err := auditReadmeGovernance(t.Context(), manifest, opts); err != nil {
		t.Errorf("audit after adoption: %v", err)
	}
	if out, err := docsReadme(t, repo); err != nil {
		t.Fatalf("docs readme: %v\n%s", err, out)
	}
	if after := readFixtureFile(t, repo, "README.md"); after != before {
		t.Error("docs readme rewrote the adopted governance block with mixed debt")
	}
}
