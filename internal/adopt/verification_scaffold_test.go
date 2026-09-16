package adopt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerificationLegacyMigrationAndCustomPreservation(t *testing.T) {
	for name, existing := range map[string]string{
		"old-stub":          legacyVerificationStub,
		"old-stub-rendered": mustRead(t, filepath.Join("testdata", "legacy-echo.Makefile")),
		"old-go":            legacyVerificationMakefile("go test -v -race ./...", "go build -v ./..."),
		"old-meson":         legacyVerificationMakefile("meson test -C core/build --suite=fast", "meson compile -C core/build"),
		"custom-echo":       "verify-all:\n\t@echo claimed\n",
		"edited-old-stub":   "# operator changes\n" + legacyVerificationStub,
		"multi-target":      "verify-all other:\n\t@echo custom\n",
		"included":          "include shared.mk\n", "generated-target": "$(TARGET):\n\t@echo custom\n",
		"pattern-target": "verify-%:\n\t@echo custom\n",
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
			} else {
				if got != existing || applied.Verification.Status != verificationPreserved {
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
			if got != existing || report.Verification.Status != verificationPreserved {
				t.Fatalf("an edited Makefile was not preserved: %+v %q", report.Verification, got)
			}
			continue
		}
		if got != buildMakefile(plan) || report.Verification.Status != verificationDeclared {
			t.Fatalf("a prior generated Makefile was not regenerated: %+v %q", report.Verification, got)
		}
	}
}
