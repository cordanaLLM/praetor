// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/supplychain"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// thirdPartyLicenses is the layout #1244 measured in an adopter: LICENSES/ holds the texts of
// third-party notices its build ships, named after the package, and there is no REUSE.toml.
var thirdPartyLicenses = map[string]string{
	supplychain.LicensesDir + "/CC-BY-SA-4.0.txt": "Creative Commons Attribution-ShareAlike 4.0\n",
	supplychain.LicensesDir + "/OFL-1.1.txt":      "SIL Open Font License 1.1\n",
	supplychain.LicensesDir + "/goldmark-MIT.txt": "MIT License, goldmark\n",
	"LICENSING.md": "# Licensing\n",
}

// reuseSession returns an adoption session over a fresh directory holding files, with declined as
// its adoption.decline list.
func reuseSession(t *testing.T, files map[string]string, declined ...string) *adoptSession {
	t.Helper()
	root := t.TempDir()
	for rel, text := range files {
		mustWrite(t, filepath.Join(root, filepath.FromSlash(rel)), text)
	}
	return &adoptSession{repoPath: root, report: &AdoptReport{}, declined: declined}
}

// withReuseFile returns files with a REUSE.toml beside them.
func withReuseFile(files map[string]string) map[string]string {
	merged := map[string]string{supplychain.ReuseFile: "version = 1\n"}
	for rel, text := range files {
		merged[rel] = text
	}
	return merged
}

// reuseRendering is lefthook.yml as adoption writes it with every language's jobs, with or
// without the reuse-lint job.
func reuseRendering(reuse bool) string {
	return buildLefthookYAMLFor(lefthookShape{languages: lefthookJobLanguages, reuse: reuse}, false)
}

// The REUSE gate, workflow and reuse-lint job alike, follows reuseGateApplies. Positive: REUSE.toml
// or .reuse/dep5 brings it. Negative: the measured layout, LICENSES/ with third-party texts and no
// REUSE.toml, brings neither, and a declined step drops both even where REUSE.toml stands.
// Boundary: a decline written in another spelling the manifest accepts counts the same.
func TestReuseGateApplies_3D(t *testing.T) {
	for name, tc := range map[string]struct {
		files    map[string]string
		declined []string
		want     bool
	}{
		"REUSE.toml":                        {withReuseFile(nil), nil, true},
		".reuse/dep5":                       {map[string]string{supplychain.ReuseDep5File: "Format: x\n"}, nil, true},
		"third-party LICENSES/ only":        {thirdPartyLicenses, nil, false},
		"REUSE.toml, reuse-gate declined":   {withReuseFile(thirdPartyLicenses), []string{reuseGateStep}, false},
		"REUSE.toml, another step declined": {withReuseFile(nil), []string{"editors"}, true},
	} {
		s := reuseSession(t, tc.files, tc.declined...)
		applies, err := s.reuseGateApplies(t.Context())
		if err != nil || applies != tc.want {
			t.Errorf("%s: reuseGateApplies = %v, %v; want %v", name, applies, err, tc.want)
		}
		shape, err := s.lefthookShape(t.Context())
		if err != nil || shape.reuse != tc.want {
			t.Errorf("%s: lefthookShape.reuse = %v, %v; want %v", name, shape.reuse, err, tc.want)
		}
	}
}

// Positive: declining reuse-gate after an adoption that wrote the gate removes both artefacts of
// the step: the declined step removes Praetor's unedited workflow, and lefthook.yml migrates to
// the rendering without the reuse-lint job; each report entry names the decline (#1244).
func TestDeclinedReuseGate_Positive_RemovesWorkflowAndJob(t *testing.T) {
	s := reuseSession(t, withReuseFile(map[string]string{
		reuseWorkflowFile: mustReuseWorkflow(t, forge.FallbackDefaultBranch),
		lefthookFile:      reuseRendering(true),
	}), reuseGateStep)
	if err := skipDeclinedStep(t.Context(), s, reuseGateStep); err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(s.repoPath, filepath.FromSlash(reuseWorkflowFile))) {
		t.Fatal("the declined step kept Praetor's unedited workflow")
	}
	if detail := findActionDetail(s.report.ActionDetails, reuseWorkflowFile); !strings.Contains(detail, "adoption.decline names "+reuseGateStep) {
		t.Errorf("removal detail %q does not name the decline", detail)
	}
	shape, err := s.lefthookShape(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	wrote, err := s.migrateReuseSwitch(lefthookTarget{shape: lefthookShape{languages: lefthookJobLanguages, reuse: shape.reuse}},
		[]byte(reuseRendering(true)))
	if err != nil || !wrote {
		t.Fatalf("lefthook.yml was not migrated: wrote=%v err=%v", wrote, err)
	}
	if got := mustRead(t, filepath.Join(s.repoPath, lefthookFile)); got != reuseRendering(false) || strings.Contains(got, reuseLintJob) {
		t.Fatalf("lefthook.yml still carries the reuse-lint job:\n%s", got)
	}
	if detail := findActionDetail(s.report.ActionDetails, lefthookFile); !strings.Contains(detail, "adoption.decline names "+reuseGateStep) {
		t.Errorf("migration detail %q does not name the decline", detail)
	}
}

// Negative: a declined step keeps an edited workflow, which is the repository's, and warns how
// to drop it; a dry run removes nothing and previews the removal.
func TestDeclinedReuseGate_Negative_EditedKeptAndDryRunWritesNothing(t *testing.T) {
	edited := strings.Replace(mustReuseWorkflow(t, forge.FallbackDefaultBranch), "name: REUSE\n", "name: Licensing\n", 1)
	s := reuseSession(t, withReuseFile(map[string]string{reuseWorkflowFile: edited}), reuseGateStep)
	if err := skipDeclinedStep(t.Context(), s, reuseGateStep); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(s.repoPath, filepath.FromSlash(reuseWorkflowFile))); got != edited {
		t.Fatal("the declined step removed an edited workflow")
	}
	if !slices.ContainsFunc(s.report.Warnings, func(w string) bool {
		return strings.Contains(w, reuseWorkflowFile+" kept: adoption.decline names "+reuseGateStep+", but the file is edited")
	}) {
		t.Errorf("no warning about the kept workflow: %v", s.report.Warnings)
	}

	dry := reuseSession(t, withReuseFile(map[string]string{reuseWorkflowFile: mustReuseWorkflow(t, forge.FallbackDefaultBranch)}), reuseGateStep)
	dry.opts.DryRun = true
	if err := skipDeclinedStep(t.Context(), dry, reuseGateStep); err != nil {
		t.Fatal(err)
	}
	if !fileExists(filepath.Join(dry.repoPath, filepath.FromSlash(reuseWorkflowFile))) || !hasAction(dry.report, reuseWorkflowFile, actionRemove) {
		t.Fatalf("a dry run must keep the file and preview its removal: %+v", dry.report.ActionDetails)
	}
}

// Boundary: a REUSE repository that declines nothing keeps both artefacts, and the measured layout
// whose earlier adoption wrote the gate loses it with the job, for the root, not a decline. A
// repository that labels its files by headers alone gets the gate's absence named in the report.
func TestReuseGate_Boundary_RealRepositoryKeepsAndHeaderOnlyIsNamed(t *testing.T) {
	// REUSE-IgnoreStart: the strings below hold licence lines as test data, not this file's licence.
	reuseRepo := reuseSession(t, withReuseFile(map[string]string{lefthookFile: reuseRendering(true)}))
	shape, err := reuseRepo.lefthookShape(t.Context())
	if err != nil || !shape.reuse {
		t.Fatalf("a REUSE repository lost the reuse-lint job: %+v, %v", shape, err)
	}
	if wrote, err := reuseRepo.migrateReuseSwitch(lefthookTarget{shape: lefthookShape{languages: lefthookJobLanguages, reuse: true}},
		[]byte(reuseRendering(true))); err != nil || wrote {
		t.Fatalf("a REUSE repository's lefthook.yml was migrated: wrote=%v err=%v", wrote, err)
	}

	measured := reuseSession(t, map[string]string{
		reuseWorkflowFile: mustReuseWorkflow(t, forge.FallbackDefaultBranch),
	})
	for rel, text := range thirdPartyLicenses {
		mustWrite(t, filepath.Join(measured.repoPath, filepath.FromSlash(rel)), text)
	}
	if err := reconcileReuseGate(t.Context(), measured); err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(measured.repoPath, filepath.FromSlash(reuseWorkflowFile))) {
		t.Fatal("the measured layout kept the hosted REUSE gate")
	}
	if detail := findActionDetail(measured.report.ActionDetails, reuseWorkflowFile); !strings.Contains(detail, "carries no REUSE.toml or .reuse/dep5") {
		t.Errorf("removal detail %q does not name the declaration files", detail)
	}

	headers := reuseSession(t, map[string]string{
		supplychain.LicensesDir + "/MIT.txt": "MIT License\n",
		"main.go":                            "// SPDX-License-Identifier: MIT\npackage main\n",
	})
	command := exec.Command("git", "init", "-q", headers.repoPath)
	command.Env = testsupport.HermeticGitEnv(t)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := reconcileReuseGate(context.Background(), headers); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(headers.report.Warnings, func(w string) bool {
		return strings.Contains(w, "REUSE by headers only, no REUSE.toml: the REUSE gate is not rendered; add REUSE.toml to keep it (1 file(s)")
	}) {
		t.Errorf("the header-only repository was not named: %v", headers.report.Warnings)
	}
	// REUSE-IgnoreEnd
}
