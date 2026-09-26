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
		// Negative: typescript-node is detected, but its CI job runs `npm ci` and `npm test`
		// and this pnpm project has no package-lock.json, so the job is withheld and nothing
		// is required. It used to be scaffolded and required, and could never pass.
		name: "pnpm-project-without-workflows",
		arrange: func(t *testing.T, dir string) {
			writeFixtureFile(t, dir, "package.json", `{"name": "widgets", "scripts": {"test": "vitest run"}}`)
			writeFixtureFile(t, dir, "pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
		},
	}, {
		// Positive: an npm project with a lockfile and a test script gets the job, required.
		name: "npm-project-without-workflows",
		arrange: func(t *testing.T, dir string) {
			writeFixtureFile(t, dir, "package.json", `{"name": "widgets", "scripts": {"test": "node --test"}}`)
			writeFixtureFile(t, dir, "package-lock.json", `{"name": "widgets", "lockfileVersion": 3, "requires": true, "packages": {"": {"name": "widgets"}}}`)
		},
		wantChecks: true, wantTest: true,
	}, {
		name:       "with-repository-workflows",
		arrange:    copySyncWorkflowFixtures,
		wantChecks: true,
	}}
}

func TestAdoptionAndSyncAgreeOnPolicyAndRepositoryChecks(t *testing.T) {
	for _, tc := range adoptionChecksCases() {
		t.Run(tc.name, func(t *testing.T) {
			f := newSyncValidationFixture(t)
			initGitFixture(t, f.dir)
			writeFixtureFile(t, f.dir, ".standards.yaml", fixtureManifest("acme", "widgets", true))
			if err := os.Remove(filepath.Join(f.dir, ".github/rulesets/main.json")); err != nil {
				t.Fatal(err)
			}
			tc.arrange(t, f.dir)
			if _, err := adopt.Adopt(t.Context(), adopt.AdoptOptions{Path: f.dir, Profile: "framework"}); err != nil {
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
