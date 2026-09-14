package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
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
	for _, count := range []int{1, maxSyncLabels, maxSyncLabels + 1} {
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
			if (err != nil) != (count > maxSyncLabels) {
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
			stub := &forgeStub{writeStatus: http.StatusCreated}
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
