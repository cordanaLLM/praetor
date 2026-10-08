package adopt

import (
	"os"
	"path/filepath"
	"slices"
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
	if !hasWarningMatching(report.Warnings, "Makefile line", launcherLine) {
		t.Fatalf("expected warning naming line to change to %q, got warnings: %v", launcherLine, report.Warnings)
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
