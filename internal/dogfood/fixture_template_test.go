package dogfood

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// TestMain removes the package's shared fixture templates once every test has run.
func TestMain(m *testing.M) {
	code := m.Run()
	if err := removeFixtureTemplates(); err != nil {
		fmt.Fprintf(os.Stderr, "remove shared dogfood fixture templates: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// policyTemplate is one verified public-loop run that the package builds once and never
// hands out: callers receive copies of its evidence directory and of its report.
type policyTemplate struct {
	evidence string
	report   *PublicLoopReport
}

// policyTemplates caches templates by fixture arguments. The mutex is held for the whole
// build, so a second caller waits for the first run instead of starting its own; a build
// that fails stores nothing and fails its own test.
var policyTemplates = struct {
	sync.Mutex
	root  string
	byKey map[string]*policyTemplate
}{byKey: map[string]*policyTemplate{}}

// sharedPolicyTemplate returns the template for one adoptedPolicyFixture argument pair,
// building it with runAdoptedPolicyLoop on first use. The build installs the git shim
// with t.Setenv, so its callers stay serial.
func sharedPolicyTemplate(t *testing.T, limit, lines int) *policyTemplate {
	t.Helper()
	policyTemplates.Lock()
	defer policyTemplates.Unlock()
	key := fmt.Sprintf("policy-%d-%d", limit, lines)
	if template, ok := policyTemplates.byKey[key]; ok {
		return template
	}
	if policyTemplates.root == "" {
		root, err := os.MkdirTemp("", "praetor-dogfood-templates-")
		if err != nil {
			t.Fatal(err)
		}
		policyTemplates.root = root
	}
	evidence := filepath.Join(policyTemplates.root, key, "evidence")
	template := &policyTemplate{evidence: evidence, report: runAdoptedPolicyLoop(t, limit, lines, evidence)}
	policyTemplates.byKey[key] = template
	return template
}

// runAdoptedPolicyLoop applies Praetor to the policy fixture of adoptedPolicyFixture through
// RunPublicLoop, its evidence under artifactDir, and returns the verified report.
func runAdoptedPolicyLoop(t *testing.T, limit, lines int, artifactDir string) *PublicLoopReport {
	t.Helper()
	manifest := fmt.Sprintf("version: 1\nrepository:\n  owner: example\n  name: fixture\nprofiles: [framework]\noverrides:\n  complexity:\n    max_func_loc: %d\n", limit)
	opts, _ := publicLoopFixture(t, map[string]string{
		"fixture.go": publicPolicyFunction(lines), ".standards.yaml": manifest,
	})
	opts.Apply = true
	opts.ArtifactDir = artifactDir
	report, err := RunPublicLoop(t.Context(), opts)
	if err != nil || !report.Verified {
		t.Fatalf("policy adoption failed: %v (%+v)", err, report)
	}
	return report
}

func removeFixtureTemplates() error {
	policyTemplates.Lock()
	defer policyTemplates.Unlock()
	if policyTemplates.root == "" {
		return nil
	}
	return os.RemoveAll(policyTemplates.root)
}

// copyPolicyTemplate gives one test its own copy of the template's evidence directory,
// checkout included, and a report whose paths name that copy. The report is copied through
// its JSON form; the policy members JSON leaves out are restored from the template.
func copyPolicyTemplate(t *testing.T, template *policyTemplate) *PublicLoopReport {
	t.Helper()
	evidence := filepath.Join(t.TempDir(), "evidence")
	copyFixtureTree(t, template.evidence, evidence)
	data, err := json.Marshal(template.report)
	if err != nil {
		t.Fatal(err)
	}
	rebased := strings.ReplaceAll(string(data), jsonStringContent(t, template.evidence), jsonStringContent(t, evidence))
	var report PublicLoopReport
	if err := json.Unmarshal([]byte(rebased), &report); err != nil {
		t.Fatal(err)
	}
	// A path the loop spelled differently from the template root would keep naming the
	// template, and a test changing that checkout would change every later copy.
	within := evidence + string(filepath.Separator)
	for _, path := range append([]string{report.RunDir}, reportCheckouts(&report)...) {
		if !strings.HasPrefix(path, within) {
			t.Fatalf("report copy names %s outside its copy %s", path, evidence)
		}
	}
	if err := restoreHiddenPolicies(&report, template.report); err != nil {
		t.Fatal(err)
	}
	return &report
}

func reportCheckouts(report *PublicLoopReport) []string {
	checkouts := make([]string, 0, len(report.Results))
	for i := range report.Results {
		checkouts = append(checkouts, report.Results[i].Checkout)
	}
	return checkouts
}

// restoreHiddenPolicies copies the effective policies' manifest and catalog artifacts, which
// config keeps out of JSON, from the template report into its copy. The scans read the
// manifest's declared HISS exceptions, so a copy without it would scan differently.
func restoreHiddenPolicies(copied, template *PublicLoopReport) error {
	if len(copied.Results) != len(template.Results) {
		return fmt.Errorf("report copy has %d results, template %d", len(copied.Results), len(template.Results))
	}
	for i := range template.Results {
		if len(copied.Results[i].Attempts) != len(template.Results[i].Attempts) {
			return fmt.Errorf("report copy result %d has %d attempts, template %d", i, len(copied.Results[i].Attempts), len(template.Results[i].Attempts))
		}
		source, target := &template.Results[i], &copied.Results[i]
		if source.Plan != nil {
			restoreHiddenPolicy(target.Plan.EffectivePolicy, source.Plan.EffectivePolicy)
		}
		for j := range source.Attempts {
			if source.Attempts[j].Adoption != nil {
				restoreHiddenPolicy(target.Attempts[j].Adoption.EffectivePolicy, source.Attempts[j].Adoption.EffectivePolicy)
			}
		}
	}
	return nil
}

func restoreHiddenPolicy(target, source *config.EffectivePolicy) {
	if target == nil || source == nil {
		return
	}
	target.Manifest, target.CatalogArtifacts = source.Manifest, source.CatalogArtifacts
}

// copyFixtureTree copies src to dst, links included, then restores each entry's exact mode:
// os.CopyFS creates files at 0o666 and directories at 0o777 before the umask, while the
// public snapshot digest covers modes and the evidence directories are owner-only.
func copyFixtureTree(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatalf("copy fixture template: %v", err)
	}
	err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		return os.Chmod(filepath.Join(dst, rel), info.Mode().Perm())
	})
	if err != nil {
		t.Fatalf("restore fixture template modes: %v", err)
	}
}

// Positive and boundary: two copies of one template are independent trees and reports, each
// confined to its own test directory, and each copy is the exact tree the loop verified (its
// snapshot digest equals the last attempt's), modes included.
func TestAdoptedPolicyFixtureCopiesAreIndependent(t *testing.T) {
	first, firstAnchor := adoptedPolicyFixture(t, 35, 40)
	second, _ := adoptedPolicyFixture(t, 35, 40)
	a, b := first.Results[0], second.Results[0]
	if a.Checkout == b.Checkout || !strings.HasPrefix(a.Checkout, first.Options.ArtifactDir) || !strings.HasPrefix(first.RunDir, first.Options.ArtifactDir) {
		t.Fatalf("copies share or escape their evidence directory: %s, %s (%s)", a.Checkout, b.Checkout, first.Options.ArtifactDir)
	}
	for _, item := range []PublicRepositoryResult{a, b} {
		tree, err := snapshotPublicTree(t.Context(), item.Checkout)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := tree.digest(), item.Attempts[len(item.Attempts)-1].TreeDigest; got != want {
			t.Fatalf("copied checkout digest %s, verified tree %s", got, want)
		}
	}
	if firstAnchor.Policy != a.Plan.EffectivePolicy || firstAnchor.Policy.Manifest == nil || a.Plan.EffectivePolicy == b.Plan.EffectivePolicy {
		t.Fatal("anchor must alias its own copy's planned policy, manifest restored, never another copy's")
	}
	publicWrite(t, filepath.Join(a.Checkout, "only-first.go"), "package fixture\n", 0o600)
	if _, err := os.Stat(filepath.Join(b.Checkout, "only-first.go")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a write to one copy reached another: %v", err)
	}
	first.Results[0].Plan.EffectivePolicy.Sources[0].SHA256 = "changed"
	if second.Results[0].Plan.EffectivePolicy.Sources[0].SHA256 == "changed" {
		t.Fatal("a change to one report copy reached another")
	}
}

// Negative and boundary: a copy whose shape differs from its template is refused rather than
// restored partially; matching empty reports restore nothing and pass.
func TestRestoreHiddenPoliciesRefusesMismatchedCopy(t *testing.T) {
	template := &PublicLoopReport{Results: []PublicRepositoryResult{{Attempts: []PublicAttempt{{}}}}}
	for _, copied := range []*PublicLoopReport{{}, {Results: []PublicRepositoryResult{{}}}} {
		if err := restoreHiddenPolicies(copied, template); err == nil {
			t.Fatalf("a copy shaped %+v was restored from %+v", copied, template)
		}
	}
	if err := restoreHiddenPolicies(&PublicLoopReport{}, &PublicLoopReport{}); err != nil {
		t.Fatalf("matching empty reports refused: %v", err)
	}
}
