package adopt

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/readmepresentation"
)

const (
	legacyPraetorBadge        = "[![HISS lattice](https://img.shields.io/badge/Standards-Praetor%20HISS%20lattice-brightgreen)](AGENTS.md)"
	testReadmeGovernanceStart = "<!-- praetor:readme-governance:start -->"
	testReadmeGovernanceEnd   = "<!-- praetor:readme-governance:end -->"
)

func TestAdoptReadmeGovernanceNeverCertifiesAnUnverifiedRepository(t *testing.T) {
	repoPath := newTestRepo(t, "readme-pending")
	initial := "# Demo\n\n" + legacyPraetorBadge + "\n\nHuman introduction.\n"
	mustWrite(t, filepath.Join(repoPath, readmeFile), initial)

	report, err := Adopt(t.Context(), AdoptOptions{
		LockSourceRoot: newAdoptLockSource(t),
		Path:           repoPath,
		Profile:        "framework",
		RecordBaseline: false,
	})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	assertNoIssues(t, report)
	content := mustRead(t, filepath.Join(repoPath, readmeFile))
	for _, falseClaim := range []string{"HISS%20Compliant", "This repository conforms"} {
		if strings.Contains(content, falseClaim) {
			t.Fatalf("unverified adoption published %q:\n%s", falseClaim, content)
		}
	}
	if strings.Count(content, legacyPraetorBadge) != 1 {
		t.Fatalf("custom HISS badge was removed or duplicated:\n%s", content)
	}
	for _, marker := range []string{testReadmeGovernanceStart, testReadmeGovernanceEnd} {
		if strings.Count(content, marker) != 1 {
			t.Fatalf("managed governance marker %q missing or duplicated:\n%s", marker, content)
		}
	}
	if !strings.Contains(content, "not a verification certificate") {
		t.Fatalf("managed block does not explain its evidence boundary:\n%s", content)
	}
}

func TestAdoptReadmeGovernanceRefreshesManagedStateIdempotently(t *testing.T) {
	repoPath := newTestRepo(t, "readme-refresh")
	readme := "# Legacy\n\n" + testReadmeGovernanceStart + "\nstale generated claim\n" +
		testReadmeGovernanceEnd + "\n\nHuman tail.\n"
	mustWrite(t, filepath.Join(repoPath, readmeFile), readme)
	mustWrite(t, filepath.Join(repoPath, "main.go"), "package main\nfunc run() {\n\t_ = fail()\n}\nfunc fail() error { return nil }\n")
	opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, RecordBaseline: true}

	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatalf("first adopt: %v", err)
	}
	first := mustRead(t, filepath.Join(repoPath, readmeFile))
	if strings.Contains(first, "stale generated claim") || !strings.Contains(first, "1%20baselined") {
		t.Fatalf("managed block was not refreshed from the recorded baseline:\n%s", first)
	}
	if !strings.Contains(first, "Human tail.") {
		t.Fatalf("human README content was not preserved:\n%s", first)
	}
	if _, err := Adopt(context.Background(), opts); err != nil {
		t.Fatalf("second adopt: %v", err)
	}
	if second := mustRead(t, filepath.Join(repoPath, readmeFile)); second != first {
		t.Fatalf("README reconciliation is not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestAdoptReadmeGovernanceRejectsMalformedMarkersWithoutChangingReadme(t *testing.T) {
	repoPath := newTestRepo(t, "readme-malformed")
	initial := "# Demo\n\n" + testReadmeGovernanceStart + "\nunterminated\n\nHuman tail.\n"
	path := filepath.Join(repoPath, readmeFile)
	mustWrite(t, path, initial)

	_, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err == nil || !strings.Contains(err.Error(), "README governance markers") {
		t.Fatalf("malformed managed markers must fail explicitly, got %v", err)
	}
	if got := mustRead(t, path); got != initial {
		t.Fatalf("failed reconciliation changed README:\n%s", got)
	}
}

// previousRelativeBlock is a README block an earlier Praetor rendered: its HISS badge linked
// AGENTS.md by a repository-relative path, which a strict MkDocs portal including the README
// cannot resolve (#506).
const previousRelativeBlock = testReadmeGovernanceStart + "\n" +
	"[![HISS Adopted][praetor-hiss-badge]](AGENTS.md)\n\n" +
	"[praetor-hiss-badge]: https://img.shields.io/badge/Standards-HISS%20Adopted%20(baseline%20pending)-yellow\n" +
	testReadmeGovernanceEnd

// Positive: plain adoption, without --force, refreshes the previous block, keeps the human
// text and converges on the rerun. The badge links the repository's AGENTS.md on the default
// branch of the origin's forge by absolute URL, with the documentation gate and without it.
func TestAdoptReadmeGovernanceRefreshesRelativeAgentsLink(t *testing.T) {
	const linked = "\n[praetor-hiss-agents]: https://github.com/acme/readme-agents-link/blob/HEAD/AGENTS.md\n"
	for name, facets := range map[string][]string{"documentation": nil, "no documentation": {"security:high"}} {
		t.Run(name, func(t *testing.T) {
			repoPath := newTestRepo(t, "readme-agents-link")
			path := filepath.Join(repoPath, readmeFile)
			mustWrite(t, path, "# Demo\n\n"+previousRelativeBlock+"\n\nHuman tail.\n")
			opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Facets: facets}
			report, err := Adopt(t.Context(), opts)
			if err != nil {
				t.Fatalf("adopt: %v", err)
			}
			first := mustRead(t, path)
			if strings.Contains(first, "](AGENTS.md)") || !strings.Contains(first, "Human tail.") ||
				!strings.Contains(first, "\n[![HISS Adopted][praetor-hiss-badge]][praetor-hiss-agents]\n") ||
				!strings.Contains(first, linked) {
				t.Fatalf("previous block not refreshed to the absolute AGENTS.md link:\n%s", first)
			}
			if detail := findActionDetail(report.ActionDetails, readmeFile); !strings.Contains(detail, "Reconciled the marker-owned") {
				t.Fatalf("README refresh not reported: %q", detail)
			}
			if _, err := Adopt(t.Context(), opts); err != nil {
				t.Fatalf("rerun: %v", err)
			}
			if second := mustRead(t, path); second != first {
				t.Fatalf("rerun changed the refreshed README:\n%s", second)
			}
		})
	}
}

// ReadmeIdentity in three dimensions. Positive: a manifest naming both fields returns them,
// with the documentation gate and without it. Boundary: without the gate, no identity or half
// of one returns none, so the HISS badge renders unlinked. Negative: the gate without an
// identity, or with half of one, is errReadmeIdentityUnset.
func TestReadmeIdentity(t *testing.T) {
	full := &config.Manifest{Repository: config.RepositoryMetadata{Owner: "acme", Name: "widgets"}}
	for _, documentation := range []bool{true, false} {
		if owner, name, err := ReadmeIdentity(full, documentation); err != nil || owner != "acme" || name != "widgets" {
			t.Fatalf("documentation gate %v: %q/%q %v", documentation, owner, name, err)
		}
	}
	half := &config.Manifest{Repository: config.RepositoryMetadata{Owner: "acme"}}
	for _, manifest := range []*config.Manifest{nil, {}, half} {
		if owner, name, err := ReadmeIdentity(manifest, false); err != nil || owner != "" || name != "" {
			t.Fatalf("no documentation gate, manifest %+v: %q/%q %v", manifest, owner, name, err)
		}
	}
	for _, manifest := range []*config.Manifest{nil, {}, half, {Repository: config.RepositoryMetadata{Name: "widgets"}}} {
		if _, _, err := ReadmeIdentity(manifest, true); !errors.Is(err, errReadmeIdentityUnset) {
			t.Fatalf("documentation gate, manifest %+v: %v, want errReadmeIdentityUnset", manifest, err)
		}
	}
}

// hissAgentsDefinition returns the reference definition of the HISS badge's AGENTS.md link in
// readme, or "" when the badge links nothing.
func hissAgentsDefinition(readme string) string {
	for line := range strings.SplitSeq(readme, "\n") {
		if strings.HasPrefix(line, "[praetor-hiss-agents]: ") {
			return line
		}
	}
	return ""
}

// The HISS badge links AGENTS.md on the forge the origin remote names (review of #506), under
// the default facets, whose documentation gate is on. Positive: github.com, gitlab.com and
// the Gitea and Forgejo instances gitea.com and codeberg.org each render their own URL shape,
// never github.com for another forge. Negative: a self-hosted host, whose forge software the
// host name does not reveal, and a nested GitLab namespace, which the manifest's owner/name
// does not name, render the badge unlinked. Boundary: a repository without an origin remote,
// so without an identity, renders it unlinked too (the gate is off there: with it, adoption
// skips the README, TestAdopt_UnresolvedIdentityCompletesWithoutGuessing).
func TestAdoptReadmeGovernanceLinksTheOriginForge(t *testing.T) {
	for name, tc := range map[string]struct{ origin, want string }{
		"github":        {"https://github.com/acme/widgets.git", "https://github.com/acme/widgets/blob/HEAD/AGENTS.md"},
		"gitlab":        {"git@gitlab.com:acme/widgets.git", "https://gitlab.com/acme/widgets/-/blob/HEAD/AGENTS.md"},
		"gitea":         {"https://gitea.com/acme/widgets.git", "https://gitea.com/acme/widgets/src/branch/HEAD/AGENTS.md"},
		"forgejo":       {"ssh://git@codeberg.org/acme/widgets.git", "https://codeberg.org/acme/widgets/src/branch/HEAD/AGENTS.md"},
		"unknown host":  {"https://git.example.org/acme/widgets.git", ""},
		"nested gitlab": {"https://gitlab.com/group/acme/widgets.git", ""},
		"no identity":   {"", ""},
	} {
		t.Run(name, func(t *testing.T) {
			repoPath := newTestRepo(t, "widgets")
			var facets []string
			if tc.origin == "" {
				mustWrite(t, filepath.Join(repoPath, ".git", "config"), "[core]\n\trepositoryformatversion = 0\n")
				facets = []string{"security:high"}
			} else {
				writeOriginRemote(t, repoPath, tc.origin)
			}
			path := filepath.Join(repoPath, readmeFile)
			mustWrite(t, path, "# Demo\n\n"+previousRelativeBlock+"\n")
			if _, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Facets: facets}); err != nil {
				t.Fatalf("adopt: %v", err)
			}
			got := mustRead(t, path)
			definition := hissAgentsDefinition(got)
			if tc.want == "" {
				if definition != "" || strings.Contains(got, "](AGENTS.md)") || !strings.Contains(got, "\n![HISS Adopted][praetor-hiss-badge]\n") {
					t.Fatalf("badge must render unlinked, got %q:\n%s", definition, got)
				}
				return
			}
			if definition != "[praetor-hiss-agents]: "+tc.want {
				t.Fatalf("AGENTS.md link %q, want %q:\n%s", definition, tc.want, got)
			}
		})
	}
}

const (
	testCompliantHeader = "# widgets\n\n[![Release](https://img.shields.io/badge/release-v1.0-blue?style=for-the-badge)](https://example.com)\n\n**Fast widget engine**\n\n"
	testCompliantBody   = "## 🚀 Get started\n\nInstall it.\n\n## 🤝 Contribute\n\nSend a PR.\n"
)

func TestAdoptPlacesGovernanceBlockLast(t *testing.T) {
	repoPath := newTestRepo(t, "widgets")
	readmePath := filepath.Join(repoPath, readmeFile)
	mustWrite(t, readmePath, testCompliantHeader+testCompliantBody)

	if _, err := Adopt(t.Context(), AdoptOptions{
		LockSourceRoot: newAdoptLockSource(t),
		Path:           repoPath,
		RecordBaseline: true,
	}); err != nil {
		t.Fatalf("scratch adopt: %v", err)
	}
	adopted := mustRead(t, readmePath)
	if strings.Index(adopted, testReadmeGovernanceStart) < strings.Index(adopted, "## 🤝 Contribute") {
		t.Fatalf("scratch adopt did not place governance block at end:\n%s", adopted)
	}
	findings, infos, err := readmepresentation.CheckContentWithInfo(repoPath, readmeFile, adopted)
	if err != nil || len(findings) != 0 || len(infos) != 0 {
		t.Fatalf("scratch adopt README-05 check: findings=%v infos=%v err=%v", findings, infos, err)
	}
}

func TestAdoptForceRelocatesGovernanceBlockAfterH1ToEnd(t *testing.T) {
	repoPath := newTestRepo(t, "widgets")
	readmePath := filepath.Join(repoPath, readmeFile)
	midReadme := testCompliantHeader + previousRelativeBlock + "\n\n" + testCompliantBody
	mustWrite(t, readmePath, midReadme)

	findings, infos, err := readmepresentation.CheckContentWithInfo(repoPath, readmeFile, midReadme)
	if err != nil || len(findings) != 0 || len(infos) != 1 {
		t.Fatalf("expected 0 findings and 1 info line when governance block is after H1, got findings=%v infos=%v err=%v", findings, infos, err)
	}
	if _, err := Adopt(t.Context(), AdoptOptions{
		LockSourceRoot: newAdoptLockSource(t),
		Path:           repoPath,
		Force:          true,
		RecordBaseline: true,
	}); err != nil {
		t.Fatalf("adopt --force: %v", err)
	}
	relocated := mustRead(t, readmePath)
	if strings.Index(relocated, testReadmeGovernanceStart) < strings.Index(relocated, "## 🤝 Contribute") {
		t.Fatalf("adopt --force did not move governance block to end:\n%s", relocated)
	}
	findings, infos, err = readmepresentation.CheckContentWithInfo(repoPath, readmeFile, relocated)
	if err != nil || len(findings) != 0 || len(infos) != 0 {
		t.Fatalf("after adopt --force expected 0 findings and 0 infos, got findings=%v infos=%v err=%v", findings, infos, err)
	}
}

func TestAdoptRecordBaselineKeepsReadmeFindingsWithZeroHISS(t *testing.T) {
	repoPath := newTestRepo(t, "readme-baseline-survives")
	mustWrite(t, filepath.Join(repoPath, readmeFile), "# Minimal\n\nPlain text without badges.\n")
	lockRoot := newAdoptLockSource(t)

	if _, err := Adopt(t.Context(), AdoptOptions{
		LockSourceRoot: lockRoot,
		Path:           repoPath,
		RecordBaseline: true,
	}); err != nil {
		t.Fatalf("initial adopt: %v", err)
	}
	// Zero-debt baseline + report-only README finding must rerecord cleanly without --allow-increase.
	if _, err := Adopt(t.Context(), AdoptOptions{
		LockSourceRoot:   lockRoot,
		Path:             repoPath,
		RecordBaseline:   true,
		RerecordBaseline: true,
	}); err != nil {
		t.Fatalf("rerecord report-only README finding with zero-debt baseline: %v", err)
	}
	enableReadmePresentationGate(t, repoPath)
	if _, err := Adopt(t.Context(), AdoptOptions{
		LockSourceRoot:   lockRoot,
		Path:             repoPath,
		RecordBaseline:   true,
		RerecordBaseline: true,
	}); err == nil || !strings.Contains(err.Error(), "README presentation finding") || strings.Contains(err.Error(), "HISS invariant violations introduced") {
		t.Fatalf("expected refusal naming README presentation findings apart from HISS, got: %v", err)
	}
	if _, err := Adopt(t.Context(), AdoptOptions{
		LockSourceRoot:         lockRoot,
		Path:                   repoPath,
		RecordBaseline:         true,
		RerecordBaseline:       true,
		AllowBaselineIncrease:  true,
		BaselineIncreaseReason: "baseline README presentation finding",
	}); err != nil {
		t.Fatalf("rerecord baseline with README finding: %v", err)
	}
	// Subsequent rerecord with 0 HISS findings must keep the README entry instead of wiping it.
	if _, err := Adopt(t.Context(), AdoptOptions{
		LockSourceRoot:   lockRoot,
		Path:             repoPath,
		RecordBaseline:   true,
		RerecordBaseline: true,
	}); err != nil {
		t.Fatalf("second rerecord baseline: %v", err)
	}
	assertReadmeBaselineRecorded(t, repoPath)
}

func enableReadmePresentationGate(t *testing.T, repoPath string) {
	t.Helper()
	manifestPath := filepath.Join(repoPath, ".standards.yaml")
	raw := mustRead(t, manifestPath)
	mustWrite(t, manifestPath, raw+"\ngate:\n  readme_presentation: true\n")
}

func assertReadmeBaselineRecorded(t *testing.T, repoPath string) {
	t.Helper()
	b, err := baseline.LoadBaseline(filepath.Join(repoPath, ".standards-baseline.json"))
	if err != nil {
		t.Fatalf("LoadBaseline: %v", err)
	}
	readmeBase, hasReadme := readmepresentation.ReadmeBaseline(b)
	if !hasReadme || readmeBase.TotalInfractions == 0 {
		t.Fatalf("expected README-* infractions to survive adopt rerecord with 0 HISS findings, got: %+v", b.Infractions)
	}
}

func assertKeptRatchet(t *testing.T, r *BaselineRatchet, wantPassed bool, wantActive, wantUnbaselined int) {
	t.Helper()
	if r == nil || r.Passed != wantPassed || r.Active != wantActive || r.Unbaselined != wantUnbaselined {
		t.Fatalf("kept baseline ratchet = %+v, want Passed=%v Active=%d Unbaselined=%d",
			r, wantPassed, wantActive, wantUnbaselined)
	}
}

func TestAdoptKeptBaselineMergesReadmeFindings(t *testing.T) {
	repoPath := newTestRepo(t, "readme-kept-baseline-merge")
	mustWrite(t, filepath.Join(repoPath, readmeFile), "# Minimal\n\nPlain text without badges.\n")
	lockRoot := newAdoptLockSource(t)

	mustAdoptReadmeBaseline(t, lockRoot, repoPath, AdoptOptions{
		LockSourceRoot: lockRoot, Path: repoPath, RecordBaseline: true,
	})
	enableReadmePresentationGate(t, repoPath)
	mustAdoptReadmeBaseline(t, lockRoot, repoPath, AdoptOptions{
		LockSourceRoot: lockRoot, Path: repoPath, RecordBaseline: true,
		RerecordBaseline: true, AllowBaselineIncrease: true, BaselineIncreaseReason: "record initial README finding",
	})

	rep := mustAdoptReadmeBaseline(t, lockRoot, repoPath, AdoptOptions{
		LockSourceRoot: lockRoot, Path: repoPath, RecordBaseline: true,
	})
	assertKeptRatchet(t, rep.BaselineRatchet, true, 1, 0)
	if strings.Contains(strings.Join(rep.Warnings, "\n"), "matches nothing in the tree") {
		t.Fatalf("unexpected stale baseline warning when README finding is still active: %v", rep.Warnings)
	}

	mustWrite(t, filepath.Join(repoPath, readmeFile), "# Minimal\n\n```mermaid\ngraph LR\nA-->B\n```\n")
	rep2 := mustAdoptReadmeBaseline(t, lockRoot, repoPath, AdoptOptions{
		LockSourceRoot: lockRoot, Path: repoPath, RecordBaseline: true,
	})
	assertKeptRatchet(t, rep2.BaselineRatchet, false, 2, 1)
}

func mustAdoptReadmeBaseline(t *testing.T, _, _ string, opts AdoptOptions) *AdoptReport {
	t.Helper()
	rep, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatalf("Adopt(%+v): %v", opts, err)
	}
	return rep
}

func TestAdoptRerecord_PerClassMonotonicAndLegacyDebtCountExcludesReadme(t *testing.T) {
	lockRoot := newAdoptLockSource(t)
	repoPath := newTestRepo(t, "readme-cancel-hiss")
	mustWrite(t, filepath.Join(repoPath, readmeFile), "# Minimal\n\nPlain text.\n")
	mustAdoptReadmeBaseline(t, lockRoot, repoPath, AdoptOptions{
		LockSourceRoot: lockRoot, Path: repoPath, RecordBaseline: true,
	})
	enableReadmePresentationGate(t, repoPath)
	rep := mustAdoptReadmeBaseline(t, lockRoot, repoPath, AdoptOptions{
		LockSourceRoot: lockRoot, Path: repoPath, RecordBaseline: true,
		RerecordBaseline: true, AllowBaselineIncrease: true, BaselineIncreaseReason: "initial readme finding",
	})
	if rep.LegacyDebtCount != 0 {
		t.Fatalf("LegacyDebtCount = %d, want 0 (README entries excluded)", rep.LegacyDebtCount)
	}
	keptRep := mustAdoptReadmeBaseline(t, lockRoot, repoPath, AdoptOptions{
		LockSourceRoot: lockRoot, Path: repoPath, RecordBaseline: true,
	})
	if keptRep.LegacyDebtCount != 0 {
		t.Fatalf("kept LegacyDebtCount = %d, want 0", keptRep.LegacyDebtCount)
	}
	verifyRep := mustAdoptReadmeBaseline(t, lockRoot, repoPath, AdoptOptions{
		LockSourceRoot: lockRoot, Path: repoPath, RecordBaseline: false,
	})
	if verifyRep.LegacyDebtCount != 0 {
		t.Fatalf("verify LegacyDebtCount = %d, want 0", verifyRep.LegacyDebtCount)
	}
	mustWrite(t, filepath.Join(repoPath, readmeFile), "# Minimal\n[![a](https://img.shields.io/badge/a-b-blue?style=for-the-badge)](https://x)\n**Tag**\n")
	mustWrite(t, filepath.Join(repoPath, "rec.go"), "package main\nfunc loop(n int) int { return loop(n - 1) }\n")
	if _, err := Adopt(t.Context(), AdoptOptions{
		LockSourceRoot: lockRoot, Path: repoPath, RecordBaseline: true, RerecordBaseline: true,
	}); !errors.Is(err, baseline.ErrDebtIncrease) {
		t.Fatalf("expected ErrDebtIncrease when README fix cancels new HISS violation, got: %v", err)
	}
}

func TestAdoptRerecord_HISSFixDoesNotCancelNewReadmeDebt(t *testing.T) {
	lockRoot := newAdoptLockSource(t)
	repoPath := newTestRepo(t, "hiss-cancel-readme")
	mustWrite(t, filepath.Join(repoPath, readmeFile), "# Minimal\n[![a](https://img.shields.io/badge/a-b-blue?style=for-the-badge)](https://x)\n**Tag**\n")
	mustWrite(t, filepath.Join(repoPath, "rec.go"), "package main\nfunc loop(n int) int { return loop(n - 1) }\n")
	mustAdoptReadmeBaseline(t, lockRoot, repoPath, AdoptOptions{
		LockSourceRoot: lockRoot, Path: repoPath, RecordBaseline: true,
	})
	enableReadmePresentationGate(t, repoPath)
	mustWrite(t, filepath.Join(repoPath, "rec.go"), "package main\nfunc calm() int { return 1 }\n")
	mustWrite(t, filepath.Join(repoPath, readmeFile), "# Minimal\n\nPlain text.\n")
	if _, err := Adopt(t.Context(), AdoptOptions{
		LockSourceRoot: lockRoot, Path: repoPath, RecordBaseline: true, RerecordBaseline: true,
	}); !errors.Is(err, baseline.ErrDebtIncrease) {
		t.Fatalf("expected ErrDebtIncrease when HISS fix cancels new README finding, got: %v", err)
	}
}

func TestAdopt_FirstBaselineAndKeptBaselineHonorReadmeGate(t *testing.T) {
	lockRoot := newAdoptLockSource(t)
	repoPath := newTestRepo(t, "first-and-kept-enforce")
	mustWrite(t, filepath.Join(repoPath, readmeFile), "# Minimal\n\nPlain text.\n")
	mustAdoptReadmeBaseline(t, lockRoot, repoPath, AdoptOptions{
		LockSourceRoot: lockRoot, Path: repoPath, RecordBaseline: false,
	})
	enableReadmePresentationGate(t, repoPath)
	firstRep := mustAdoptReadmeBaseline(t, lockRoot, repoPath, AdoptOptions{
		LockSourceRoot: lockRoot, Path: repoPath, RecordBaseline: true,
	})
	if firstRep.LegacyDebtCount != 0 {
		t.Fatalf("first baseline LegacyDebtCount = %d, want 0", firstRep.LegacyDebtCount)
	}
	if detail := actionDetail(firstRep, baselineFile, actionCreate); !strings.Contains(detail, "Recorded 0 legacy debt infractions") {
		t.Errorf("baseline creation included README debt in HISS count: %q", detail)
	}
	assertReadmeBaselineRecorded(t, repoPath)
	kept := mustAdoptReadmeBaseline(t, lockRoot, repoPath, AdoptOptions{
		LockSourceRoot: lockRoot, Path: repoPath, RecordBaseline: true,
	})
	if kept.BaselineRatchet == nil || kept.BaselineRatchet.Recorded != 0 {
		t.Errorf("kept baseline included README debt in recorded HISS count: %+v", kept.BaselineRatchet)
	}

	if err := baseline.SaveBaseline(filepath.Join(repoPath, ".standards-baseline.json"), &baseline.Baseline{
		Version: 1, TotalInfractions: 0, Infractions: []baseline.Infraction{},
	}); err != nil {
		t.Fatal(err)
	}
	keptRep := mustAdoptReadmeBaseline(t, lockRoot, repoPath, AdoptOptions{
		LockSourceRoot: lockRoot, Path: repoPath, RecordBaseline: true,
	})
	assertKeptRatchet(t, keptRep.BaselineRatchet, false, 1, 1)
}
