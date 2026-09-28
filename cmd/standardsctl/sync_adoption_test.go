package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
)

// adoptionChecksCase is one repository shape and the required checks adoption must derive.
type adoptionChecksCase struct {
	name string
	// arrange shapes the fixture before adoption runs.
	arrange func(t *testing.T, dir string)
	// profile is the profile the manifest declares; empty declares framework.
	profile string
	// wantChecks is whether the ruleset carries required status checks at all, wantTest
	// whether the scaffolded CI job "test" is one of them.
	wantChecks, wantTest bool
}

func adoptionChecksCases() []adoptionChecksCase {
	return []adoptionChecksCase{{
		// Boundary: nothing detects a flavor, so nothing is scaffolded and nothing is
		// required. Adoption used to scaffold go-library's CI here, and its setup-go step
		// against an absent go.mod became a required check no pull request could pass.
		name:    "undetected-without-workflows",
		arrange: func(*testing.T, string) {},
	}, {
		// Positive: the detected flavor's CI workflow is scaffolded before the ruleset is
		// derived, so its job is required on the first adoption rather than the second.
		name: "go-library-without-workflows",
		arrange: func(t *testing.T, dir string) {
			writeFixtureFile(t, dir, "go.mod", "module example.com/widgets\n\ngo 1.27\n")
			writeFixtureFile(t, dir, "internal/widget/widget.go", "package widget\n")
		},
		wantChecks: true, wantTest: true,
	}, {
		// Positive: typescript-node resolves under app-service, and this pnpm project gets a CI
		// job that installs with pnpm, required. It used to get an npm job that could never
		// pass, and then no job at all (BUG-1011).
		name: "pnpm-project-without-workflows", profile: "app-service",
		arrange: func(t *testing.T, dir string) {
			writeFixtureFile(t, dir, "package.json", `{"name": "widgets", "scripts": {"test": "vitest run"}}`)
			writeFixtureFile(t, dir, "pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
		},
		wantChecks: true, wantTest: true,
	}, {
		// Negative: undeclared lockfiles of two package managers leave the choice to the
		// repository, so the job is withheld and nothing is required.
		name: "two-lockfiles-without-workflows", profile: "app-service",
		arrange: func(t *testing.T, dir string) {
			writeFixtureFile(t, dir, "package.json", `{"name": "widgets", "scripts": {"test": "vitest run"}}`)
			writeFixtureFile(t, dir, "pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
			writeFixtureFile(t, dir, "yarn.lock", "# yarn lockfile v1\n")
		},
	}, {
		// Positive: an app-service npm project with a lockfile and a test script gets the job,
		// required.
		name: "npm-project-without-workflows", profile: "app-service",
		arrange:    arrangeNpmProject,
		wantChecks: true, wantTest: true,
	}, {
		// Boundary: the same npm project declaring framework gets no flavor, because no
		// framework flavor matches it, and so nothing is required (BUG-940). Detection across
		// the whole catalog used to scaffold typescript-node, a flavor of another profile.
		name:    "npm-project-declaring-framework",
		arrange: arrangeNpmProject,
	}, {
		// Negative: a library that git-ignores its lockfile while a local `npm install` wrote
		// one. CI's checkout has no lockfile, so the job is withheld and nothing is required.
		name: "npm-library-ignoring-its-lockfile", profile: "app-service",
		arrange: func(t *testing.T, dir string) {
			arrangeNpmProject(t, dir)
			writeFixtureFile(t, dir, ".gitignore", "node_modules/\npackage-lock.json\n")
		},
	}, {
		name:       "with-repository-workflows",
		arrange:    copySyncWorkflowFixtures,
		wantChecks: true,
	}}
}

// arrangeNpmProject writes an npm project with a lockfile and a test script.
func arrangeNpmProject(t *testing.T, dir string) {
	t.Helper()
	writeFixtureFile(t, dir, "package.json", `{"name": "widgets", "scripts": {"test": "node --test"}}`)
	writeFixtureFile(t, dir, "package-lock.json", `{"name": "widgets", "lockfileVersion": 3, "requires": true, "packages": {"": {"name": "widgets"}}}`)
}

// declareFixtureProfile makes the fixture declare profile: its archetype file, a lock pinning
// it, and a signed-commit manifest naming it.
func declareFixtureProfile(t *testing.T, dir, profile string) {
	t.Helper()
	archetype := ".config/archetypes/" + profile + ".yaml"
	writeFixtureFile(t, dir, archetype, "id: \""+profile+"\"\nname: \""+profile+"\"\n")
	lf := &lockFixture{dir: dir}
	lf.writeProfileLock(t, profile, lf.digestOf(t, archetype), lf.digestOf(t, ".config/archetypes/facets/security-high.yaml"), "")
	writeFixtureFile(t, dir, ".standards.yaml", fixtureProfileManifest("acme", "widgets", profile, true))
}

func TestAdoptionAndSyncAgreeOnPolicyAndRepositoryChecks(t *testing.T) {
	for _, tc := range adoptionChecksCases() {
		t.Run(tc.name, func(t *testing.T) {
			f := newSyncValidationFixture(t)
			initGitFixture(t, f.dir)
			profile := tc.profile
			if profile == "" {
				profile = "framework"
			}
			declareFixtureProfile(t, f.dir, profile)
			if err := os.Remove(filepath.Join(f.dir, ".github/rulesets/main.json")); err != nil {
				t.Fatal(err)
			}
			tc.arrange(t, f.dir)
			if _, err := adopt.Adopt(t.Context(), adopt.AdoptOptions{Path: f.dir, Profile: profile}); err != nil {
				t.Fatalf("adopt: %v", err)
			}
			before := readFixtureFile(t, f.dir, ".github/rulesets/main.json")
			if hasType(rulesetTypes(t, f.dir), "required_status_checks") != tc.wantChecks {
				t.Fatalf("adoption required checks do not match repository workflows:\n%s", before)
			}
			if strings.Contains(before, `"context": "test"`) != tc.wantTest {
				t.Fatalf("required check for the scaffolded CI job: want %v in\n%s", tc.wantTest, before)
			}
			if !hasType(rulesetTypes(t, f.dir), "required_signatures") || !strings.Contains(before, `"required_approving_review_count": 1`) {
				t.Fatalf("adoption ignored declared policy:\n%s", before)
			}
			out, err := runSyncCmd(t, "--config="+f.manifestPath)
			if err != nil {
				t.Fatalf("sync rejected freshly adopted policy/workflows: %v\n%s", err, out)
			}
			if readFixtureFile(t, f.dir, ".github/rulesets/main.json") != before {
				t.Fatal("sync rewrote adopted ruleset")
			}
		})
	}
}

func copySyncWorkflowFixtures(t *testing.T, root string) {
	t.Helper()
	for _, name := range []string{"ci.yml", "compliance.yml", "security.yml"} {
		path := filepath.Join("..", "..", ".github", "workflows", name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		writeFixtureFile(t, root, filepath.Join(".github/workflows", name), string(data))
	}
}
