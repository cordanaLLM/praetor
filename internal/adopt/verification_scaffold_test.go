package adopt

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

func TestVerificationLegacyMigrationAndCustomPreservation(t *testing.T) {
	for name, existing := range map[string]string{
		"old-stub":          legacyVerificationStub,
		"old-stub-rendered": mustRead(t, filepath.Join("testdata", "legacy-echo.Makefile")),
		"old-go":            legacyVerificationMakefile("go test -v -race ./...", "go build -v ./..."),
		"old-meson":         legacyVerificationMakefile("meson test -C core/build --suite=fast", "meson compile -C core/build"),
		"old-go-crlf":       strings.ReplaceAll(legacyVerificationMakefile("go test -v -race ./...", "go build -v ./..."), "\n", "\r\n"),
		"custom-echo":       "verify-all:\n\t@echo claimed\n",
		"edited-old-stub":   "# operator changes\n" + legacyVerificationStub,
		"multi-target":      "verify-all other:\n\t@echo custom\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := newTestRepo(t, name)
			mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = 'fixture'\nversion = '0.1.0'\n")
			mustWrite(t, filepath.Join(root, "Makefile"), existing)
			opts := AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t), DryRun: true}
			before := snapshotTree(t, root)
			preview, err := Adopt(t.Context(), opts)
			if err != nil {
				t.Fatal(err)
			}
			assertTreeUnchanged(t, before, snapshotTree(t, root))
			opts.DryRun = false
			applied, err := Adopt(t.Context(), opts)
			if err != nil {
				t.Fatal(err)
			}
			if preview.Verification.Status != applied.Verification.Status {
				t.Fatal("preview/apply plan mismatch")
			}
			got := mustRead(t, filepath.Join(root, "Makefile"))
			if strings.HasPrefix(name, "old-") {
				if got == existing || strings.Contains(got, "Running verification") || !strings.Contains(got, "'cargo' 'test' '--locked'") {
					t.Fatalf("known legacy template not migrated: %s", got)
				}
				if applied.Verification.Status != verificationDeclared {
					t.Fatalf("known migrated plan mislabeled: %+v", applied.Verification)
				}
				if crlf := strings.HasSuffix(name, "-crlf"); crlf != strings.Contains(got, "\r\n") || (crlf && strings.Count(got, "\n") != strings.Count(got, "\r\n")) {
					t.Fatalf("migrated Makefile changed its line-ending style: %q", got)
				}
			} else {
				want, mergeErr := mergeDocumentationMakefile(existing, false)
				if mergeErr != nil {
					t.Fatal(mergeErr)
				}
				if got != want || applied.Verification.Status != verificationPreserved {
					t.Fatalf("custom commands changed or certified: %+v %q", applied.Verification, got)
				}
			}
			after := snapshotTree(t, root)
			if _, err := Adopt(t.Context(), opts); err != nil {
				t.Fatal(err)
			}
			assertTreeUnchanged(t, after, snapshotTree(t, root))
		})
	}
}

func TestVerificationCommentDoesNotHideMissingGate(t *testing.T) {
	root := newTestRepo(t, "comment")
	mustWrite(t, filepath.Join(root, "go.mod"), "module fixture\n")
	mustWrite(t, filepath.Join(root, "Makefile"), "# verify-all: old proposal\nall:\n\t@echo original\n")
	report, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
	if err != nil {
		t.Fatal(err)
	}
	got := mustRead(t, filepath.Join(root, "Makefile"))
	if report.Verification.Status != verificationDeclared || !strings.Contains(got, "\nverify-all:\n") || !strings.Contains(got, "'go' 'test'") || !strings.Contains(got, "@echo original") {
		t.Fatalf("comment confused target detection: %+v %s", report.Verification, got)
	}
}

func TestVerificationAdoptionDetectsDotnetWithoutExecutingProject(t *testing.T) {
	root := newTestRepo(t, "dotnet")
	mustWrite(t, filepath.Join(root, "Test.csproj"), `<Project><PropertyGroup><IsTestProject>true</IsTestProject></PropertyGroup></Project>`)
	mustWrite(t, filepath.Join(root, "package.json"), `{"scripts":{"build":"touch INJECTED_BUILD","test":"touch INJECTED_TEST"}}`)
	report, err := Adopt(t.Context(), AdoptOptions{Path: root, LockSourceRoot: newAdoptLockSource(t)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(report.Warnings, "\n"), "declared-unverified") {
		t.Fatal("text adapters cannot see that project execution is unverified")
	}
	if report.Archetype != "app-service" || report.Verification.Status != verificationDeclared {
		t.Fatalf(".NET adoption picked wrong profile: %+v", report)
	}
	for _, path := range []string{"AGENTS.md", "Makefile"} {
		text := mustRead(t, filepath.Join(root, path))
		if !strings.Contains(text, "'dotnet' 'test' './Test.csproj'") || strings.Contains(text, "'go' 'test'") {
			t.Fatalf("%s still contains wrong toolchain: %s", path, text)
		}
	}
	for _, path := range []string{"INJECTED_BUILD", "INJECTED_TEST"} {
		if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
			t.Fatal("adoption executed a declared project script")
		}
	}
}

// TestVerificationPriorGeneratedRecipesAreStillPraetorOwned covers the recipe prefix change. A
// Makefile generated before command lines began with exec is Praetor's own output: adoption must
// regenerate it in the current form rather than preserve it as a custom verify-all. Edited, it is
// the operator's, and stays untouched.
func TestVerificationPriorGeneratedRecipesAreStillPraetorOwned(t *testing.T) {
	for _, edited := range []bool{false, true} {
		root := newTestRepo(t, "prior-generated")
		mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = 'fixture'\nversion = '0.1.0'\n")
		plan, err := resolveVerificationPlan(t.Context(), root)
		if err != nil {
			t.Fatal(err)
		}
		existing := buildMakefileWith(plan, priorVerificationRecipePrefix)
		if existing == buildMakefile(plan) || !strings.Contains(existing, "\t@'cargo' 'test'") {
			t.Fatalf("fixture is not the prior rendering: %q", existing)
		}
		if edited {
			existing = "# operator changes\n" + existing
		}
		mustWrite(t, filepath.Join(root, "Makefile"), existing)
		report, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
		if err != nil {
			t.Fatal(err)
		}
		got := mustRead(t, filepath.Join(root, "Makefile"))
		if edited {
			want, mergeErr := mergeDocumentationMakefile(existing, false)
			if mergeErr != nil {
				t.Fatal(mergeErr)
			}
			if got != want || report.Verification.Status != verificationPreserved {
				t.Fatalf("an edited Makefile was not preserved: %+v %q", report.Verification, got)
			}
			continue
		}
		want, mergeErr := mergeDocumentationMakefile(buildMakefile(plan), false)
		if mergeErr != nil {
			t.Fatal(mergeErr)
		}
		if got != want || report.Verification.Status != verificationDeclared {
			t.Fatalf("a prior generated Makefile was not regenerated: %+v %q", report.Verification, got)
		}
	}
}

// Positive: a Makefile generated before PRAETORCTL resolved through the engine launcher
// (priorPathResolvedMakefile, resolving from PATH) is regenerated to resolve through
// the engine launcher, while an edited one is preserved.
func TestVerificationPriorPathResolvedMakefileIsRegenerated(t *testing.T) {
	t.Run("unedited", func(t *testing.T) {
		root, plan, existing := preparePriorPathResolvedRepo(t)
		mustWrite(t, filepath.Join(root, "Makefile"), existing)
		report, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
		if err != nil {
			t.Fatal(err)
		}
		got := mustRead(t, filepath.Join(root, "Makefile"))
		want, mergeErr := mergeDocumentationMakefile(buildMakefile(plan), false)
		if mergeErr != nil {
			t.Fatal(mergeErr)
		}
		if got != want || report.Verification.Status != verificationDeclared {
			t.Fatalf("a prior path-resolved Makefile was not regenerated: %+v %q", report.Verification, got)
		}
	})
	t.Run("edited", func(t *testing.T) {
		root, _, existing := preparePriorPathResolvedRepo(t)
		existing = "# operator changes\n" + existing
		mustWrite(t, filepath.Join(root, "Makefile"), existing)
		report, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
		if err != nil {
			t.Fatal(err)
		}
		got := mustRead(t, filepath.Join(root, "Makefile"))
		want, mergeErr := mergeDocumentationMakefile(existing, false)
		if mergeErr != nil {
			t.Fatal(mergeErr)
		}
		if got != want || report.Verification.Status != verificationPreserved {
			t.Fatalf("an edited Makefile was not preserved: %+v %q", report.Verification, got)
		}
	})
}

func preparePriorPathResolvedRepo(t *testing.T) (string, *VerificationPlan, string) {
	t.Helper()
	root := newTestRepo(t, "prior-path-resolved")
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = 'fixture'\nversion = '0.1.0'\n")
	plan, err := resolveVerificationPlan(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	existing := priorPathResolvedMakefile(plan)
	if existing == buildMakefile(plan) || !strings.Contains(existing, util.MakefileCLIVariable) {
		t.Fatalf("fixture is not the prior path-resolved rendering: %q", existing)
	}
	return root, plan, existing
}

// Boundary: when git-hooks is declined in the manifest, the generated Makefile keeps
// the existing contract and resolves PRAETORCTL from PATH (util.MakefileCLIVariable).
func TestVerificationDeclinedGitHooksResolvesFromPath(t *testing.T) {
	root := newTestRepo(t, "declined-hooks-makefile")
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = 'fixture'\nversion = '0.1.0'\n")
	mustWrite(t, filepath.Join(root, ".standards.yaml"), "version: 1\nadoption:\n  decline:\n    - git-hooks\n")
	report, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
	if err != nil {
		t.Fatal(err)
	}
	assertNoIssues(t, report)
	got := mustRead(t, filepath.Join(root, "Makefile"))
	if !strings.Contains(got, util.MakefileCLIVariable) || strings.Contains(got, engineLauncherFile) {
		t.Fatalf("declined git-hooks did not resolve from PATH:\n%s", got)
	}
}

// Positive: a repository with its own Makefile plus the main-era appended block ends with
// the launcher line after adopt.
func TestVerificationAppendedBlock_Positive_MainEraAppendedBlockSwapsLauncher(t *testing.T) {
	root := newTestRepo(t, "appended-main-era")
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = 'fixture'\nversion = '0.1.0'\n")
	plan, err := resolveVerificationPlan(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	ownMakefile := "all: build\nbuild:\n\t@cargo build\ntest:\n\t@cargo test\n"
	appended, err := appendVerificationTargetsWithLauncher(ownMakefile, plan, false)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "Makefile"), appended)
	report, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
	if err != nil {
		t.Fatal(err)
	}
	got := mustRead(t, filepath.Join(root, "Makefile"))
	launcherLine := "PRAETORCTL ?= " + lefthookGovernedCommand("")
	if !strings.Contains(got, launcherLine) {
		t.Fatalf("Makefile does not contain launcher line:\n%s", got)
	}
	if strings.Contains(got, util.MakefileCLIVariable) {
		t.Fatalf("Makefile still contains path-resolved MakefileCLIVariable line:\n%s", got)
	}
	if report.Verification.Status != verificationDeclared {
		t.Fatalf("status = %v, want %v", report.Verification.Status, verificationDeclared)
	}
}

// Negative: a hand-edited appended block is kept and a warning naming the line to change is emitted.
func TestVerificationAppendedBlock_Negative_HandEditedBlockKeptAndWarningEmitted(t *testing.T) {
	root := newTestRepo(t, "appended-hand-edited")
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = 'fixture'\nversion = '0.1.0'\n")
	plan, err := resolveVerificationPlan(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	ownMakefile := "all: build\nbuild:\n\t@cargo build\ntest:\n\t@cargo test\n"
	appended, err := appendVerificationTargetsWithLauncher(ownMakefile, plan, false)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(appended, util.MakefileCLIVariable, "PRAETORCTL ?= /usr/local/bin/praetorctl\n", 1)
	if edited == appended {
		t.Fatal("failed to hand-edit Makefile fixture")
	}
	mustWrite(t, filepath.Join(root, "Makefile"), edited)
	report, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
	if err != nil {
		t.Fatal(err)
	}
	got := mustRead(t, filepath.Join(root, "Makefile"))
	if !strings.Contains(got, "PRAETORCTL ?= /usr/local/bin/praetorctl") {
		t.Fatalf("hand-edited block was not kept:\n%s", got)
	}
	launcherLine := "PRAETORCTL ?= " + lefthookGovernedCommand("")
	if strings.Contains(got, launcherLine) {
		t.Fatalf("hand-edited line was overwritten with launcher line:\n%s", got)
	}
	wantWarning := fmt.Sprintf("Makefile line 8: verification block was edited; change %q to %q to resolve the engine launcher",
		"PRAETORCTL ?= /usr/local/bin/praetorctl", launcherLine)
	if !slices.Contains(report.Warnings, wantWarning) {
		t.Fatalf("expected warning %q, got warnings: %v", wantWarning, report.Warnings)
	}
	if report.Verification.Status != verificationPreserved {
		t.Fatalf("status = %v, want %v", report.Verification.Status, verificationPreserved)
	}
}

func hasWarningMatching(warnings []string, prefix, substr string) bool {
	return slices.ContainsFunc(warnings, func(w string) bool {
		return strings.Contains(w, prefix) && strings.Contains(w, substr)
	})
}

// Regresses #594: an own Makefile with a main-era appended block holding the placeholder
// recipe (written while the plan was unavailable) must stay unavailable with the placeholder
// reason even after a language manifest (Cargo.toml) is added, rather than claiming declared-unverified.
func TestVerificationAppendedBlock_Negative_PlaceholderAppendedBlockStaysUnavailable(t *testing.T) {
	root := newTestRepo(t, "appended-placeholder-regress")
	ownMakefile := "all: build\nbuild:\n\t@cargo build\ntest:\n\t@cargo test\n"
	placeholderPlan := &VerificationPlan{Status: verificationUnavailable}
	appended, err := appendVerificationTargetsWithLauncher(ownMakefile, placeholderPlan, false)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "Makefile"), appended)
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = 'fixture'\nversion = '0.1.0'\n")

	report, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
	if err != nil {
		t.Fatal(err)
	}
	if report.Verification.Status != verificationUnavailable {
		t.Fatalf("status = %v, want %v", report.Verification.Status, verificationUnavailable)
	}
	wantReason := "The Makefile still holds the failing placeholder recipe adoption writes; replace it with the project's build and test commands."
	if !slices.Contains(report.Verification.Reasons, wantReason) {
		t.Fatalf("expected reasons to contain %q, got: %v", wantReason, report.Verification.Reasons)
	}
}

// Negative: adopter swaps the compile-context/caveman/audit lines in verify-all for custom scripts
// while leaving the PRAETORCTL variable line untouched. Adoption must preserve the custom verify-all,
// emit a warning, and generatedPipelines must credit nothing for it (status is verificationPreserved).
func TestVerificationAppendedBlock_Negative_EditedVerifyAllBodyPreservedAndWarnedAndNoPipeline(t *testing.T) {
	root := newTestRepo(t, "appended-edited-body")
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = 'fixture'\nversion = '0.1.0'\n")
	plan, err := resolveVerificationPlan(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	ownMakefile := "all: build\nbuild:\n\t@cargo build\ntest:\n\t@cargo test\n"
	appended, err := appendVerificationTargetsWithLauncher(ownMakefile, plan, false)
	if err != nil {
		t.Fatal(err)
	}
	// Swap compile-context/caveman/audit lines for ./scripts/my-lint.sh
	oldBody := "\t@$(PRAETORCTL) compile-context --verify\n\t@$(PRAETORCTL) caveman check --configured-sources\n\t@$(PRAETORCTL) audit\n"
	newBody := "\t@./scripts/my-lint.sh\n"
	edited := strings.Replace(appended, oldBody, newBody, 1)
	if edited == appended {
		t.Fatal("failed to edit verify-all body in Makefile fixture")
	}
	mustWrite(t, filepath.Join(root, "Makefile"), edited)

	report, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
	if err != nil {
		t.Fatal(err)
	}
	got := mustRead(t, filepath.Join(root, "Makefile"))
	if !strings.Contains(got, "./scripts/my-lint.sh") {
		t.Fatalf("custom recipe was not preserved:\n%s", got)
	}
	if report.Verification.Status != verificationPreserved {
		t.Fatalf("status = %v, want %v", report.Verification.Status, verificationPreserved)
	}
	launcherLine := "PRAETORCTL ?= " + lefthookGovernedCommand("")
	if !hasWarningMatching(report.Warnings, "Makefile line", launcherLine) {
		t.Fatalf("expected warning naming line to change to %q, got: %v", launcherLine, report.Warnings)
	}
	// Verify AGENTS.md does NOT claim audit / compile-context in verify-all (BUG-804 class)
	agentsMD := mustRead(t, filepath.Join(root, "AGENTS.md"))
	if strings.Contains(agentsMD, "`make verify-all` = repository gate") {
		t.Fatalf("AGENTS.md incorrectly claimed verify-all is repository gate:\n%s", agentsMD)
	}
}

// Boundary: an appended block in CRLF checkout normalises and swaps the launcher line.
func TestVerificationAppendedBlock_Boundary_WhitespaceNormalisation(t *testing.T) {
	root := newTestRepo(t, "appended-crlf-boundary")
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = 'fixture'\nversion = '0.1.0'\n")
	plan, err := resolveVerificationPlan(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	ownMakefile := "all: build\nbuild:\n\t@cargo build\ntest:\n\t@cargo test\n"
	appended, err := appendVerificationTargetsWithLauncher(ownMakefile, plan, false)
	if err != nil {
		t.Fatal(err)
	}
	crlfAppended := strings.ReplaceAll(appended, "\n", "\r\n")
	mustWrite(t, filepath.Join(root, "Makefile"), crlfAppended)

	report, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
	if err != nil {
		t.Fatal(err)
	}
	got := mustRead(t, filepath.Join(root, "Makefile"))
	launcherLine := "PRAETORCTL ?= " + lefthookGovernedCommand("")
	if !strings.Contains(got, launcherLine) {
		t.Fatalf("CRLF block was not swapped to launcher:\n%s", got)
	}
	if report.Verification.Status != verificationDeclared {
		t.Fatalf("status = %v, want %v", report.Verification.Status, verificationDeclared)
	}
}

// Positive: runs 2 and 3 on an appended-block Makefile write identical bytes and report
// actionReconcile, never actionAppend.
func TestVerificationAppendedBlock_Positive_IdempotentRerunReportsReconciled(t *testing.T) {
	root := newTestRepo(t, "appended-rerun")
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = 'fixture'\nversion = '0.1.0'\n")
	plan, err := resolveVerificationPlan(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	ownMakefile := "all: build\nbuild:\n\t@cargo build\ntest:\n\t@cargo test\n"
	appended, err := appendVerificationTargetsWithLauncher(ownMakefile, plan, true)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "Makefile"), appended)

	// Run 1: initial adoption attaches documentation gate
	if _, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)}); err != nil {
		t.Fatal(err)
	}

	// Run 2: idempotent re-run on unchanged bytes reports reconcile, not append
	report2, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
	if err != nil {
		t.Fatal(err)
	}
	entry2 := findLastAction(report2.ActionDetails, makefileName)
	if entry2 == nil || entry2.Action == actionAppend {
		t.Fatalf("run 2 reported action %v, want reconcile", entry2)
	}

	// Run 3: third run continues to report reconcile
	report3, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
	if err != nil {
		t.Fatal(err)
	}
	entry3 := findLastAction(report3.ActionDetails, makefileName)
	if entry3 == nil || entry3.Action == actionAppend {
		t.Fatalf("run 3 reported action %v, want reconcile", entry3)
	}
}

// Positive: runs 1 and 2 on a declined generated Makefile write identical bytes and report
// actionReconcile, never actionAppend on rerun.
func TestVerificationGeneratedBlock_Positive_DeclinedRerunReportsReconciled(t *testing.T) {
	rootDeclined := newTestRepo(t, "declined-generated-rerun")
	mustWrite(t, filepath.Join(rootDeclined, "Cargo.toml"), "[package]\nname = 'fixture'\nversion = '0.1.0'\n")
	mustWrite(t, filepath.Join(rootDeclined, ".standards.yaml"), "version: 1\nadoption:\n  decline:\n    - git-hooks\n")
	optsDeclined := AdoptOptions{Path: rootDeclined, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)}
	reportD1, err := Adopt(t.Context(), optsDeclined)
	if err != nil {
		t.Fatal(err)
	}
	if entry := findLastAction(reportD1.ActionDetails, makefileName); entry == nil || entry.Action != actionCreate {
		t.Fatalf("declined run 1 reported action %v, want create", entry)
	}
	reportD2, err := Adopt(t.Context(), optsDeclined)
	if err != nil {
		t.Fatal(err)
	}
	if entry := findLastAction(reportD2.ActionDetails, makefileName); entry == nil || entry.Action == actionAppend {
		t.Fatalf("declined run 2 reported action %v, want reconcile", entry)
	}
}

func findLastAction(details []ActionDetail, path string) *ActionDetail {
	var found *ActionDetail
	for i := range details {
		if details[i].Path == path {
			found = &details[i]
		}
	}
	return found
}

// Positive: a pre-#146 appended block (marker, .PHONY: verify-all, literal @standardsctl recipes,
// no var line) is recognised as unedited and swaps to the launcher line without false warnings.
func TestVerificationAppendedBlock_Positive_Pre146AppendedBlockSwapsLauncher(t *testing.T) {
	root := newTestRepo(t, "appended-pre146")
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = 'fixture'\nversion = '0.1.0'\n")
	plan, err := resolveVerificationPlan(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	ownMakefile := "all: build\nbuild:\n\t@cargo build\ntest:\n\t@cargo test\n"
	pre146Block := "\n" + renderAppendedBlock(ownMakefile, plan, "", "standardsctl", false, priorVerificationRecipePrefix)
	mustWrite(t, filepath.Join(root, "Makefile"), ownMakefile+pre146Block)

	report, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
	if err != nil {
		t.Fatal(err)
	}
	got := mustRead(t, filepath.Join(root, "Makefile"))
	launcherLine := "PRAETORCTL ?= " + lefthookGovernedCommand("")
	if !strings.Contains(got, launcherLine) {
		t.Fatalf("pre-#146 block did not swap to launcher:\n%s", got)
	}
	if strings.Contains(got, "@standardsctl") {
		t.Fatalf("pre-#146 block still contains literal @standardsctl:\n%s", got)
	}
	if slices.ContainsFunc(report.Warnings, func(w string) bool { return strings.Contains(w, "verification block was edited") }) {
		t.Fatalf("unexpected edited warning for unedited pre-#146 block: %v", report.Warnings)
	}
	if report.Verification.Status != verificationDeclared {
		t.Fatalf("status = %v, want %v", report.Verification.Status, verificationDeclared)
	}
}

// Negative: PRAETORCTL line moved to the Makefile top leaves the appended block without a var line.
// Adoption must keep the block and warn with "insert after line N", without deleting .PHONY.
func TestVerificationAppendedBlock_Negative_MovedCLIVarWarnsInsertAfterLine(t *testing.T) {
	root := newTestRepo(t, "appended-moved-var")
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = 'fixture'\nversion = '0.1.0'\n")
	plan, err := resolveVerificationPlan(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	ownMakefile := "PRAETORCTL ?= /custom/path\nall: build\nbuild:\n\t@cargo build\ntest:\n\t@cargo test\n"
	appended, err := appendVerificationTargetsWithLauncher(ownMakefile, plan, false)
	if err != nil {
		t.Fatal(err)
	}
	// Remove the variable line from the appended block
	withoutVar := strings.Replace(appended, util.MakefileCLIVariable, "", 1)
	mustWrite(t, filepath.Join(root, "Makefile"), withoutVar)

	report, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
	if err != nil {
		t.Fatal(err)
	}
	got := mustRead(t, filepath.Join(root, "Makefile"))
	if !strings.Contains(got, ".PHONY: verify-all") {
		t.Fatalf(".PHONY: verify-all was deleted or missing:\n%s", got)
	}
	launcherLine := "PRAETORCTL ?= " + lefthookGovernedCommand("")
	wantSubstr := "insert " + strconv.Quote(launcherLine) + " after line"
	if !slices.ContainsFunc(report.Warnings, func(w string) bool { return strings.Contains(w, wantSubstr) }) {
		t.Fatalf("expected warning with %q, got: %v", wantSubstr, report.Warnings)
	}
	// Must NOT tell user to change .PHONY
	if slices.ContainsFunc(report.Warnings, func(w string) bool { return strings.Contains(w, ".PHONY") }) {
		t.Fatalf("warning must not mention .PHONY: %v", report.Warnings)
	}
}

// Positive: when git-hooks is declined, a Makefile holding the launcher line swaps back to
// the PATH-resolving variable line.
func TestVerificationAppendedBlock_Positive_LauncherToPathSwapWhenGitHooksDeclined(t *testing.T) {
	root := newTestRepo(t, "appended-launcher-to-path")
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = 'fixture'\nversion = '0.1.0'\n")
	plan, err := resolveVerificationPlan(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	ownMakefile := "all: build\nbuild:\n\t@cargo build\ntest:\n\t@cargo test\n"
	appended, err := appendVerificationTargetsWithLauncher(ownMakefile, plan, true)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "Makefile"), appended)

	mustWrite(t, filepath.Join(root, ".standards.yaml"), "version: 1\nadoption:\n  decline:\n    - git-hooks\n")
	report, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
	if err != nil {
		t.Fatal(err)
	}
	got := mustRead(t, filepath.Join(root, "Makefile"))
	if !strings.Contains(got, util.MakefileCLIVariable) {
		t.Fatalf("Makefile does not contain PATH-resolving line:\n%s", got)
	}
	launcherLine := "PRAETORCTL ?= " + lefthookGovernedCommand("")
	if strings.Contains(got, launcherLine) {
		t.Fatalf("Makefile still contains launcher line:\n%s", got)
	}
	if report.Verification.Status != verificationDeclared {
		t.Fatalf("status = %v, want %v", report.Verification.Status, verificationDeclared)
	}
}

// Boundary: when an un-reconciled custom lefthook.yml exists, launcherInstalled returns false
// and the appended Makefile resolves PRAETORCTL from PATH rather than the launcher (#906, HISS-19).
func TestVerificationAppendedBlock_Boundary_CustomLefthookResolvesFromPath(t *testing.T) {
	root := newTestRepo(t, "appended-custom-lefthook")
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = 'fixture'\nversion = '0.1.0'\n")
	customLefthook := "pre-commit:\n  commands:\n    custom:\n      run: echo custom\n"
	mustWrite(t, filepath.Join(root, "lefthook.yml"), customLefthook)
	ownMakefile := "all: build\nbuild:\n\t@cargo build\ntest:\n\t@cargo test\n"
	mustWrite(t, filepath.Join(root, "Makefile"), ownMakefile)

	report, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
	if err != nil {
		t.Fatal(err)
	}
	got := mustRead(t, filepath.Join(root, "Makefile"))
	if !strings.Contains(got, util.MakefileCLIVariable) {
		t.Fatalf("custom lefthook did not cause Makefile to resolve from PATH:\n%s", got)
	}
	launcherLine := "PRAETORCTL ?= " + lefthookGovernedCommand("")
	if strings.Contains(got, launcherLine) {
		t.Fatalf("custom lefthook incorrectly received engine launcher in Makefile:\n%s", got)
	}
	if report.Verification.Status != verificationDeclared {
		t.Fatalf("status = %v, want %v", report.Verification.Status, verificationDeclared)
	}
}
