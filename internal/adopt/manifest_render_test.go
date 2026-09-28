package adopt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

const (
	// emittedManifestFixture is the current rendering of the declarations in
	// priorManifestFixture. scripts/test_emitted_yaml_lint.py lints it with yamllint's default
	// rules (make hooks-lint); TestEmittedManifestFixtureMatchesTheRendering keeps it equal to
	// what config.RenderManifest writes.
	emittedManifestFixture = "testdata/emitted/.standards.yaml"
	// priorManifestFixture is a manifest an earlier adoption wrote (yaml.Marshal's text).
	priorManifestFixture = "testdata/manifest/prior.standards.yaml"
	// updateEmittedManifestEnv rewrites the fixture from the rendering instead of comparing.
	updateEmittedManifestEnv = "PRAETOR_UPDATE_EMITTED_FIXTURES"
)

func renderedPriorManifest(t *testing.T) string {
	t.Helper()
	manifest, err := config.DecodeManifest([]byte(mustRead(t, priorManifestFixture)))
	if err != nil {
		t.Fatal(err)
	}
	data, err := config.RenderManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestEmittedManifestFixtureMatchesTheRendering(t *testing.T) {
	want := renderedPriorManifest(t)
	if os.Getenv(updateEmittedManifestEnv) == "1" {
		mustWrite(t, emittedManifestFixture, want)
		return
	}
	if got := strings.ReplaceAll(mustRead(t, emittedManifestFixture), "\r\n", "\n"); got != want {
		t.Errorf("%s differs from the rendering; regenerate it with %s=1 go test ./internal/adopt -run %s",
			emittedManifestFixture, updateEmittedManifestEnv, t.Name())
	}
}

// assertYamllintLayout checks, without yamllint on PATH, the two rules an earlier manifest
// failed: the document start, and every line within the limit or one unbroken word.
func assertYamllintLayout(t *testing.T, name, text string) {
	t.Helper()
	if !strings.HasPrefix(text, "---\n") {
		t.Errorf("%s: no document start", name)
	}
	for i, line := range strings.Split(text, "\n") {
		if utf8.RuneCountInString(line) > util.YAMLLineLimit && strings.Contains(strings.TrimLeft(line, " "), " ") {
			t.Errorf("%s line %d: %d columns: %s", name, i+1, utf8.RuneCountInString(line), line)
		}
	}
}

// Positive and boundary: the rendering opens with a document start and fits the digest, and an
// identity as long as a forge allows is fitted too; the declarations decode unchanged.
func TestRenderManifest_LintCleanLayout(t *testing.T) {
	rendered := renderedPriorManifest(t)
	assertYamllintLayout(t, "rendering", rendered)
	long := strings.Repeat("o", 100)
	manifest := &config.Manifest{Version: 1, Repository: config.RepositoryMetadata{Owner: long, Name: long},
		Profiles: []string{"framework"}}
	data, err := config.RenderManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	assertYamllintLayout(t, "long identity", string(data))
	decoded, err := config.DecodeManifest(data)
	if err != nil || decoded.Repository.Owner != long || decoded.Repository.Name != long {
		t.Fatalf("a fitted identity must decode unchanged: %+v %v", decoded, err)
	}
}

func TestIsPriorManifestRendering(t *testing.T) {
	prior := mustRead(t, priorManifestFixture)
	if !isPriorManifestRendering([]byte(prior)) {
		t.Error("positive: the earlier adoption's manifest must be recognised")
	}
	if isPriorManifestRendering([]byte(prior + "# operator note\n")) {
		t.Error("negative: an edited manifest is the operator's, not an earlier rendering")
	}
	if isPriorManifestRendering([]byte(renderedPriorManifest(t))) {
		t.Error("negative: the current rendering is not an earlier one")
	}
	if isPriorManifestRendering(nil) || isPriorManifestRendering([]byte("version: [\n")) {
		t.Error("boundary: an empty or unparsable manifest is not an earlier rendering")
	}
}

// earlierManifest rewrites repo's manifest as an earlier adoption would have written it.
func earlierManifest(t *testing.T, repo string) string {
	t.Helper()
	manifest, err := config.LoadManifest(filepath.Join(repo, manifestFile))
	if err != nil {
		t.Fatal(err)
	}
	prior, err := yaml.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, manifestFile), string(prior))
	return string(prior)
}

func readopt(t *testing.T, repo, source string) *AdoptReport {
	t.Helper()
	report, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: source, Path: repo, Profile: "framework"})
	if err != nil {
		t.Fatalf("re-adopt: %v", err)
	}
	assertNoIssues(t, report)
	return report
}

// Positive and boundary: an earlier adoption's manifest migrates to the current layout on
// plain re-adoption, the next run leaves it byte for byte, and an edited one is never touched.
func TestAdoptMigratesAnEarlierManifestRendering(t *testing.T) {
	repo := newTestRepo(t, "manifest-migration")
	mustWrite(t, filepath.Join(repo, "go.mod"), "module github.com/acme/manifest-migration\n")
	source := newAdoptLockSource(t)
	readopt(t, repo, source)
	current := mustRead(t, filepath.Join(repo, manifestFile))
	assertYamllintLayout(t, manifestFile, current)

	earlierManifest(t, repo)
	report := readopt(t, repo, source)
	if got := mustRead(t, filepath.Join(repo, manifestFile)); got != current {
		t.Fatalf("the earlier rendering was not migrated:\n%s", got)
	}
	if detail := findActionDetail(report.ActionDetails, manifestFile); !strings.HasPrefix(detail, "Migrated the unmodified earlier Praetor manifest") {
		t.Errorf("migration not reported: %q", detail)
	}

	readopt(t, repo, source)
	if got := mustRead(t, filepath.Join(repo, manifestFile)); got != current {
		t.Errorf("boundary: re-adopting the current rendering changed it:\n%s", got)
	}

	edited := earlierManifest(t, repo) + "# operator note\n"
	mustWrite(t, filepath.Join(repo, manifestFile), edited)
	readopt(t, repo, source)
	if got := mustRead(t, filepath.Join(repo, manifestFile)); got != edited {
		t.Errorf("negative: an edited manifest was rewritten:\n%s", got)
	}
}
