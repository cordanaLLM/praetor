package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
)

func TestAdoptionAndSyncAgreeOnPolicyAndRepositoryChecks(t *testing.T) {
	for _, workflows := range []bool{false, true} {
		name := "without-workflows"
		if workflows {
			name = "with-repository-workflows"
		}
		t.Run(name, func(t *testing.T) {
			f := newSyncValidationFixture(t)
			initGitFixture(t, f.dir)
			writeFixtureFile(t, f.dir, ".standards.yaml", fixtureManifest("acme", "widgets", true))
			if err := os.Remove(filepath.Join(f.dir, ".github/rulesets/main.json")); err != nil {
				t.Fatal(err)
			}
			if workflows {
				copySyncWorkflowFixtures(t, f.dir)
			}
			if _, err := adopt.Adopt(t.Context(), adopt.AdoptOptions{Path: f.dir, Profile: "framework"}); err != nil {
				t.Fatalf("adopt: %v", err)
			}
			before := readFixtureFile(t, f.dir, ".github/rulesets/main.json")
			// A repository that brings no workflow receives its flavor's CI workflow during
			// adoption, so the ruleset must require that job too: a workflow carrying real
			// jobs, where adoption used to scaffold a one-line comment (BUG-028).
			if !workflows && !strings.Contains(before, `"context": "test"`) {
				t.Fatalf("adoption ruleset omits the scaffolded CI job:\n%s", before)
			}
			if !hasType(rulesetTypes(t, f.dir), "required_status_checks") {
				t.Fatalf("adoption required checks do not match repository workflows:\n%s", before)
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
