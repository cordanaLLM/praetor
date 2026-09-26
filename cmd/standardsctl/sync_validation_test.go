package main

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

func newSyncValidationFixture(t *testing.T) *auditFixture {
	t.Helper()
	f := newAuditFixture(t)
	if err := synthesizeDefaultLabels(filepath.Join(f.dir, ".config/labels.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := synthesizeRuleset(filepath.Join(f.dir, ".github/rulesets/main.json"), config.DefaultPolicy().BranchProtection, nil); err != nil {
		t.Fatal(err)
	}
	return f
}

// sync checks the ruleset against the branch protection adopt renders, which joins the
// pinned profile's requirements, instead of against the built-in defaults.
func TestSyncRulesetFollowsTheJoinedProfileBranchProtection(t *testing.T) {
	f := newSyncValidationFixture(t)
	writeFixtureFile(t, f.dir, ".config/archetypes/framework.yaml",
		"id: \"framework\"\nname: \"Framework\"\nbranch_protection:\n  require_signed_commits: true\n  required_approving_reviewers: 2\n")
	lf := &lockFixture{dir: f.dir}
	lf.writeLock(t, lf.digestOf(t, ".config/archetypes/framework.yaml"), lf.digestOf(t, ".config/archetypes/facets/security-high.yaml"), "")
	// Negative: the defaults-rendered ruleset no longer matches the declared policy.
	_, err := runSyncCmd(t, "--config="+f.manifestPath)
	mustErrContain(t, err, "ruleset differs from declared branch protection policy")
	// Positive: the ruleset rendered from the joined policy verifies.
	joined := config.DefaultPolicy().BranchProtection
	joined.RequireSignedCommits, joined.RequiredApprovingReviewers = true, 2
	if err := synthesizeRuleset(filepath.Join(f.dir, ".github/rulesets/main.json"), joined, nil); err != nil {
		t.Fatal(err)
	}
	out, err := runSyncCmd(t, "--config="+f.manifestPath)
	if err != nil {
		t.Fatalf("joined ruleset rejected: %v\n%s", err, out)
	}
	mustContain(t, out, "Branch protection ruleset verified", "0 companion checks missing")
}

// strictSyncFixture pins a framework profile that requires signed commits and two
// reviewers, while the ruleset on disk still renders the weaker built-in defaults.
func strictSyncFixture(t *testing.T) *auditFixture {
	t.Helper()
	f := newSyncValidationFixture(t)
	writeFixtureFile(t, f.dir, ".config/archetypes/framework.yaml",
		"id: \"framework\"\nname: \"Framework\"\nbranch_protection:\n  require_signed_commits: true\n  required_approving_reviewers: 2\n")
	lf := &lockFixture{dir: f.dir}
	lf.writeLock(t, lf.digestOf(t, ".config/archetypes/framework.yaml"), lf.digestOf(t, ".config/archetypes/facets/security-high.yaml"), "")
	return f
}

// detachCatalog moves dir's pinned .config/archetypes into a fresh catalog root, so the lock
// stays valid but its catalog is no longer materialized, and returns that root.
func detachCatalog(t *testing.T, dir string) string {
	t.Helper()
	catalog := t.TempDir()
	if err := os.Mkdir(filepath.Join(catalog, ".config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, ".config", "archetypes"), filepath.Join(catalog, ".config", "archetypes")); err != nil {
		t.Fatal(err)
	}
	return catalog
}

// A valid lock whose catalog is not materialized leaves the policy unresolved. sync then
// neither verifies the retained ruleset nor synthesizes one from a stand-in policy, counts
// the single cause once and never reaches the forge; --catalog-root resolves it.
func TestSyncLeavesTheRulesetUncheckedWhileThePolicyIsUnresolved(t *testing.T) {
	f := strictSyncFixture(t)
	catalog := detachCatalog(t, f.dir)
	rulesetPath := filepath.Join(f.dir, ".github/rulesets/main.json")
	weaker := readFixtureFile(t, f.dir, ".github/rulesets/main.json")
	stub := &forgeStub{writeStatus: http.StatusCreated}
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)
	// Negative: the retained defaults ruleset is weaker than the profile; it is not verified.
	out, err := runSyncCmd(t, "--config="+f.manifestPath, "--remote", "--token=test-fixture", "--endpoint="+srv.URL)
	mustErrContain(t, err, "1 companion checks missing or unverified")
	mustContain(t, out, "[UNVERIFIED] Lockfile .standards.lock",
		"[UNVERIFIED] Branch protection ruleset .github/rulesets/main.json neither checked nor synthesized", "counted once")
	if strings.Contains(out, "Branch protection ruleset verified") || len(stub.recorded()) != 0 {
		t.Fatalf("unresolved policy verified the ruleset or reached the forge:\n%s", out)
	}
	if got := readFixtureFile(t, f.dir, ".github/rulesets/main.json"); got != weaker {
		t.Fatal("retained ruleset rewritten under an unresolved policy")
	}
	// Boundary: an absent ruleset is not synthesized from a stand-in policy.
	if err := os.Remove(rulesetPath); err != nil {
		t.Fatal(err)
	}
	out, err = runSyncCmd(t, "--config="+f.manifestPath)
	mustErrContain(t, err, "1 companion checks missing or unverified")
	if strings.Contains(out, "Synthesizing declarative branch protection ruleset") || util.PathExists(rulesetPath) {
		t.Fatalf("ruleset synthesized under an unresolved policy:\n%s", out)
	}
	// Positive: the selected catalog resolves the policy; the ruleset follows the profile.
	out, err = runSyncCmd(t, "--config="+f.manifestPath, "--catalog-root="+catalog)
	if err != nil {
		t.Fatalf("selected catalog: %v\n%s", err, out)
	}
	mustContain(t, out, "Synthesizing declarative branch protection ruleset", "Branch protection ruleset verified", "0 companion checks missing")
	if !hasType(rulesetTypes(t, f.dir), "required_signatures") {
		t.Fatal("ruleset synthesized without the profile's signed commits")
	}
}

// A verified lock whose policy still does not resolve (here an explicit zero complexity
// override, which the resolver rejects) is its own cause: the ruleset is left unchecked and
// counted once, alongside any other missing companion.
func TestSyncCountsAnUnresolvedPolicyOnceBesideAVerifiedLock(t *testing.T) {
	f := newSyncValidationFixture(t)
	writeFixtureFile(t, f.dir, ".standards.yaml",
		fixtureManifest("acme", "widgets", false)+"overrides:\n  complexity:\n    max_cyclomatic: 0\n")
	out, err := runSyncCmd(t, "--config="+f.manifestPath)
	mustErrContain(t, err, "1 companion checks missing or unverified")
	mustContain(t, out, "[OK] Lockfile .standards.lock verified", "neither checked nor synthesized", "verification incomplete")
	if strings.Contains(out, "Branch protection ruleset verified") {
		t.Fatalf("ruleset verified against a stand-in policy:\n%s", out)
	}
	// Boundary: a second, distinct cause is counted on its own.
	if err := os.Remove(filepath.Join(f.dir, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	_, err = runSyncCmd(t, "--config="+f.manifestPath)
	mustErrContain(t, err, "2 companion checks missing or unverified")
}

func TestSyncRejectsInvalidExistingArtifacts(t *testing.T) {
	tests := []struct{ name, path, content string }{
		{"malformed labels", ".config/labels.yaml", "labels: ["},
		{"empty labels", ".config/labels.yaml", "version: 1\nlabels: []\n"},
		{"unknown labels field", ".config/labels.yaml", "version: 1\nlabels: []\nunknown: true\n"},
		{"multiple documents", ".config/labels.yaml", "version: 1\nlabels: []\n---\nversion: 1\n"},
		{"duplicate label field", ".config/labels.yaml", "version: 1\nversion: 2\nlabels: []\n"},
		{"malformed ruleset", ".github/rulesets/main.json", "{"},
		{"empty ruleset", ".github/rulesets/main.json", "{}"},
		{"duplicate ruleset field", ".github/rulesets/main.json", `{"rules": [], "rules": []}`},
		{"non-JSON ruleset", ".github/rulesets/main.json", "rules: []\n"},
		{"corrupt lock", ".standards.lock", "broken lock"},
		{"empty canonical context", "AGENTS.md", ""},
		{"stale projection", "CLAUDE.md", "stale instructions"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newSyncValidationFixture(t)
			writeFixtureFile(t, f.dir, tc.path, tc.content)
			out, err := runSyncCmd(t, "--config="+f.manifestPath)
			if err == nil {
				t.Fatalf("sync accepted invalid %s:\n%s", tc.path, out)
			}
			if strings.Contains(out, "Synchronization complete.") {
				t.Fatalf("invalid artifact reported complete:\n%s", out)
			}
			if got := readFixtureFile(t, f.dir, tc.path); got != tc.content {
				t.Fatalf("invalid input overwritten: %q", got)
			}
		})
	}
}

func TestSyncVerifiesExistingContentWithoutRewriting(t *testing.T) {
	f := newSyncValidationFixture(t)
	labelData := "# reviewed local taxonomy\nversion: 1\nlabels:\n  - name: custom\n    color: AbC012\n"
	writeFixtureFile(t, f.dir, ".config/labels.yaml", labelData)
	before := readFixtureFile(t, f.dir, ".github/rulesets/main.json")
	out, err := runSyncCmd(t, "--config="+f.manifestPath)
	if err != nil {
		t.Fatalf("valid sync: %v\n%s", err, out)
	}
	mustContain(t, out, "schema and 1 unique labels", "Lockfile .standards.lock verified", "six compiled projections", "0 companion checks missing")
	if got := readFixtureFile(t, f.dir, ".config/labels.yaml"); got != labelData {
		t.Fatalf("custom labels rewritten: %q", got)
	}
	if got := readFixtureFile(t, f.dir, ".github/rulesets/main.json"); got != before {
		t.Fatal("matching ruleset rewritten")
	}
}

func TestSyncLabelValidationBoundaries(t *testing.T) {
	for _, count := range []int{1, forge.MaxLabelsLimit, forge.MaxLabelsLimit + 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			f := newSyncValidationFixture(t)
			labels := make([]forge.Label, count)
			for i := range labels {
				labels[i] = forge.Label{Name: fmt.Sprintf("label-%d", i), Color: "ABCdef"}
			}
			data, err := yaml.Marshal(map[string]any{"version": 1, "labels": labels})
			if err != nil {
				t.Fatal(err)
			}
			writeFixtureFile(t, f.dir, ".config/labels.yaml", string(data))
			out, err := runSyncCmd(t, "--config="+f.manifestPath)
			if (err != nil) != (count > forge.MaxLabelsLimit) {
				t.Fatalf("count=%d: err=%v\n%s", count, err, out)
			}
		})
	}
}

func TestSyncLabelNamesAndColors(t *testing.T) {
	for _, labels := range []string{
		"  - {name: '', color: abcdef}",
		"  - {name: ' padded ', color: abcdef}",
		"  - {name: duplicate, color: abcdef}\n  - {name: duplicate, color: abcdef}",
		"  - {name: bad, color: zz0000}",
		"  - {name: short, color: abc}",
	} {
		f := newSyncValidationFixture(t)
		writeFixtureFile(t, f.dir, ".config/labels.yaml", "version: 1\nlabels:\n"+labels+"\n")
		out, err := runSyncCmd(t, "--config="+f.manifestPath)
		if err == nil {
			t.Fatalf("invalid labels accepted: %s\n%s", labels, out)
		}
	}
}

func TestSyncMissingCompanionsRetainsScaffoldAndPreventsRemoteWrites(t *testing.T) {
	for _, missing := range []string{".standards.lock", "AGENTS.md", "both"} {
		t.Run(missing, func(t *testing.T) {
			f := newSyncValidationFixture(t)
			paths := []string{".config/labels.yaml", ".github/rulesets/main.json", missing}
			if missing == "both" {
				paths = []string{".config/labels.yaml", ".github/rulesets/main.json", ".standards.lock", "AGENTS.md"}
			}
			for _, path := range paths {
				if err := os.Remove(filepath.Join(f.dir, path)); err != nil {
					t.Fatal(err)
				}
			}
			stub := &forgeStub{}
			srv := httptest.NewServer(stub.handler())
			t.Cleanup(srv.Close)
			out, err := runSyncCmd(t, "--config="+f.manifestPath, "--remote", "--token=test-fixture", "--endpoint="+srv.URL)
			mustErrContain(t, err, "local sync verification incomplete")
			mustContain(t, out, "[MISSING]", "Branch protection ruleset verified")
			if len(stub.recorded()) != 0 || strings.Contains(out, "Local sync checks finished") {
				t.Fatalf("incomplete sync published or reported finished:\n%s", out)
			}
			if readFixtureFile(t, f.dir, ".config/labels.yaml") == "" || !hasType(rulesetTypes(t, f.dir), "required_linear_history") {
				t.Fatal("scaffold was not retained for completion")
			}
		})
	}
}

// A valid lock whose catalog is not materialized is reported unverified and keeps sync
// incomplete; selecting the catalog with --catalog-root verifies it.
func TestSyncReportsSourceLessLockUnverified(t *testing.T) {
	f := newSyncValidationFixture(t)
	catalog := t.TempDir()
	if err := os.Mkdir(filepath.Join(catalog, ".config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(f.dir, ".config", "archetypes"), filepath.Join(catalog, ".config", "archetypes")); err != nil {
		t.Fatal(err)
	}
	out, err := runSyncCmd(t, "--config="+f.manifestPath)
	mustErrContain(t, err, "companion checks missing or unverified")
	mustContain(t, out, "[UNVERIFIED] Lockfile .standards.lock", config.ErrLockUnverifiable.Error())
	if strings.Contains(out, "[OK] Lockfile") || strings.Contains(out, "Local sync checks finished") {
		t.Fatalf("source-less lock reported verified:\n%s", out)
	}
	out, err = runSyncCmd(t, "--config="+f.manifestPath, "--catalog-root="+catalog)
	if err != nil {
		t.Fatalf("selected catalog: %v\n%s", err, out)
	}
	mustContain(t, out, "[OK] Lockfile .standards.lock verified", "0 companion checks missing")
}

func TestSyncRejectsChangedDeclaredRulesetPolicy(t *testing.T) {
	f := newSyncValidationFixture(t)
	writeFixtureFile(t, f.dir, ".standards.yaml", fixtureManifest("acme", "widgets", true))
	out, err := runSyncCmd(t, "--config="+f.manifestPath)
	if err == nil {
		t.Fatalf("sync accepted ruleset without newly required signatures:\n%s", out)
	}
	if hasType(rulesetTypes(t, f.dir), "required_signatures") {
		t.Fatal("stale ruleset changed without review")
	}
}

func TestSyncRejectsUnsafeExistingArtifact(t *testing.T) {
	for _, kind := range []string{"symlink", "directory", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			f := newSyncValidationFixture(t)
			path := filepath.Join(f.dir, ".config/labels.yaml")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "symlink":
				target := writeFixtureFile(t, t.TempDir(), "labels.yaml", "version: 1\nlabels: []\n")
				err = os.Symlink(target, path)
			case "directory":
				err = os.Mkdir(path, 0o750)
			case "oversize":
				err = os.WriteFile(path, []byte(strings.Repeat(" ", (1<<20)+1)), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			out, err := runSyncCmd(t, "--config="+f.manifestPath)
			if err == nil {
				t.Fatalf("accepted %s artifact:\n%s", kind, out)
			}
		})
	}
}
