package adopt

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/cavemansource"
	"github.com/cordanaLLM/praetor/internal/config"
	"gopkg.in/yaml.v3"
)

// releasedIOInvariant is the released harness's HISS-02 wording; a refreshed harness states the
// catalog directive instead, which never says "context timeout".
const releasedIOInvariant = "context timeout on all I/O"

const legacyManifest = "version: 1\nrepository:\n  owner: acme\n  name: legacy\n  visibility: public\nprofiles: [framework]\n"

// releasedHarness reads the harness files the 462e3f3a release synthesized for acme/legacy.
func releasedHarness(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "paperclip", "testdata", "harness-462e3f3a", name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

func sourceAdoptOptions(t *testing.T, repoPath string, force bool) AdoptOptions {
	t.Helper()
	return AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "framework",
		RecordBaseline: true, Force: force}
}

// requirePassingSourceGate stages paths and runs the audit's own extraction plus the lint on
// every applicable value: the contract must match and its text must pass Caveman.
func requirePassingSourceGate(t *testing.T, repoPath string, paths ...string) *config.RegisterSources {
	t.Helper()
	stageAdoptPaths(t, repoPath, paths...)
	manifest, err := config.LoadManifest(filepath.Join(repoPath, manifestFile))
	if err != nil {
		t.Fatal(err)
	}
	result, err := cavemansource.ExtractDeclared(t.Context(), repoPath, manifest.Register.Sources)
	if err != nil {
		t.Fatalf("source contract does not match the adopted tree: %v", err)
	}
	for _, source := range result.Sources {
		if source.NotApplicable == "" && !caveman.Check(source.Text, caveman.Options{Kind: source.Kind}).Passed() {
			t.Fatalf("adopted source text fails Caveman: %s %q", source.Provenance(), source.Text)
		}
	}
	return manifest.Register.Sources
}

func reportDetail(report *AdoptReport, path string) string {
	details := []string{}
	for _, action := range report.ActionDetails {
		if action.Path == path {
			details = append(details, action.Details)
		}
	}
	return strings.Join(details, "\n")
}

func releasedHarnessFailsLint(t *testing.T, harness string) {
	t.Helper()
	coverage, err := cavemansource.CoverageFromDocuments(t.Context(), managedHarnessInputs(),
		map[string][]byte{paperclipFile: []byte(harness)})
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range coverage.Sources {
		if !caveman.Check(source.Text, caveman.Options{Kind: source.Kind}).Passed() {
			return
		}
	}
	t.Fatal("fixture precondition: the released harness text was expected to fail Caveman")
}

// TestAdoptUpgradesReleasedHarnessToPassingSourceGate is the documented migration: a
// repository adopted by the 462e3f3a release runs plain `praetorctl adopt` and reaches a
// passing source gate without hand edits.
func TestAdoptUpgradesReleasedHarnessToPassingSourceGate(t *testing.T) {
	repoPath := newTestRepo(t, "legacy")
	harness := releasedHarness(t, "harness.json.golden")
	releasedHarnessFailsLint(t, harness)
	mustWrite(t, filepath.Join(repoPath, manifestFile), legacyManifest)
	mustWrite(t, filepath.Join(repoPath, paperclipFile), harness)
	mustWrite(t, filepath.Join(repoPath, ".paperclip", "rules.md"), releasedHarness(t, "rules.md.golden"))
	report, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, false))
	if err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(repoPath, paperclipFile)); got == harness || strings.Contains(got, releasedIOInvariant) {
		t.Fatalf("released harness was not refreshed:\n%s", got)
	}
	if !strings.Contains(reportDetail(report, paperclipFile), "Refreshed unmodified earlier") {
		t.Fatalf("refresh not reported: %q", reportDetail(report, paperclipFile))
	}
	requirePassingSourceGate(t, repoPath, paperclipFile)
}

// TestAdoptRefreshRebindsContractBoundToReleasedHarness: a contract an earlier run bound to
// the released harness is re-bound when plain adopt refreshes that harness.
func TestAdoptRefreshRebindsContractBoundToReleasedHarness(t *testing.T) {
	repoPath := newTestRepo(t, "legacy")
	harness := releasedHarness(t, "harness.json.golden")
	bound, err := managedRegisterSources(t.Context(), []byte(harness))
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repoPath, manifestFile), legacyManifest+manifestSourcesYAML(t, bound))
	mustWrite(t, filepath.Join(repoPath, paperclipFile), harness)
	if _, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, false)); err != nil {
		t.Fatal(err)
	}
	if got := requirePassingSourceGate(t, repoPath, paperclipFile); got.SHA256 == bound.SHA256 {
		t.Fatal("contract still bound to the released harness digest")
	}
}

// TestAdoptKeepsEditedReleasedHarness: one edited character makes the harness operator-owned;
// plain adopt keeps its bytes and binds the contract to what is on disk.
func TestAdoptKeepsEditedReleasedHarness(t *testing.T) {
	repoPath := newTestRepo(t, "legacy")
	edited := strings.Replace(releasedHarness(t, "harness.json.golden"), "NOT shipping", "not shipping", 1)
	mustWrite(t, filepath.Join(repoPath, manifestFile), legacyManifest)
	mustWrite(t, filepath.Join(repoPath, paperclipFile), edited)
	if _, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, false)); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(repoPath, paperclipFile)); got != edited {
		t.Fatalf("operator-edited harness rewritten:\n%s", got)
	}
	stageAdoptPaths(t, repoPath, paperclipFile)
	manifest, err := config.LoadManifest(filepath.Join(repoPath, manifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cavemansource.ExtractDeclared(t.Context(), repoPath, manifest.Register.Sources); err != nil {
		t.Fatalf("contract not bound to the kept harness: %v", err)
	}
}

// extendedSourceRepo writes an operator-owned harness plus one operator hook input, and a
// contract over the managed rows and that hook that passes its gate.
func extendedSourceRepo(t *testing.T) (string, *config.RegisterSources) {
	t.Helper()
	return extendedSourceRepoFor(t, "acme/legacy")
}

// extendedSourceRepoFor is extendedSourceRepo with the harness naming platform. A platform other
// than the repository's gets one more operator input selecting it, so the contract binds the
// value the --force platform patch changes.
func extendedSourceRepoFor(t *testing.T, platform string) (string, *config.RegisterSources) {
	t.Helper()
	repoPath := newTestRepo(t, "legacy")
	mustWrite(t, filepath.Join(repoPath, paperclipFile), `{
  "version": 1,
  "platform": "`+platform+`",
  "operating_contract": ["result: custom contract."],
  "agit_push_format": "git push custom",
  "invariants": ["result: custom invariant."]
}
`)
	mustWrite(t, filepath.Join(repoPath, "hooks", "notify.sh"), "echo \"result: hook pass.\"\n")
	inputs := append(managedHarnessInputs(), config.RegisterSourceInput{Path: "hooks/notify.sh",
		Surface: config.SurfaceHooks, Kind: "message", Format: config.SourceFormatShell})
	if platform != "acme/legacy" {
		inputs = append(inputs, config.RegisterSourceInput{Path: paperclipFile, Surface: config.SurfacePrompts,
			Kind: "message", Format: config.SourceFormatJSON, Selector: "platform"})
	}
	result, err := cavemansource.ExtractInputs(t.Context(), repoPath, inputs)
	if err != nil {
		t.Fatal(err)
	}
	declared := &config.RegisterSources{Expected: result.Applicable, NotApplicable: result.NotApplicable,
		SHA256: result.SHA256, Inputs: inputs}
	mustWrite(t, filepath.Join(repoPath, manifestFile), legacyManifest+
		"# operator: hook text is governed too\n"+manifestSourcesYAML(t, declared))
	return repoPath, declared
}

// TestAdoptForceKeepsOperatorHarnessAndExtendedContract (#502): --force keeps an operator-owned
// harness, so the declared contract, operator rows included, stays bound to it as written and
// the manifest bytes stay.
func TestAdoptForceKeepsOperatorHarnessAndExtendedContract(t *testing.T) {
	repoPath, declared := extendedSourceRepo(t)
	harness := mustRead(t, filepath.Join(repoPath, paperclipFile))
	manifest := mustRead(t, filepath.Join(repoPath, manifestFile))
	report, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, true))
	if err != nil {
		t.Fatalf("--force refused an extended source contract: %v", err)
	}
	if got := mustRead(t, filepath.Join(repoPath, paperclipFile)); got != harness {
		t.Fatalf("--force rewrote the operator-owned harness:\n%s", got)
	}
	if got := mustRead(t, filepath.Join(repoPath, manifestFile)); got != manifest {
		t.Fatalf("--force re-bound a contract over a kept harness:\n%s", got)
	}
	if kept := requirePassingSourceGate(t, repoPath, paperclipFile, "hooks/notify.sh"); kept.SHA256 != declared.SHA256 {
		t.Fatalf("contract digest moved: %s, want %s", kept.SHA256, declared.SHA256)
	}
	if strings.Contains(reportDetail(report, manifestFile), "Re-bound register.sources") {
		t.Fatalf("re-binding reported for a kept harness: %q", reportDetail(report, manifestFile))
	}
}

// TestAdoptForcePlatformPatchRebindsExtendedContract (#502): the --force platform patch is the
// one write --force makes to an operator-owned harness. A declared contract selecting platform
// is re-bound to the patched bytes, keeping every declared input and the operator comment.
func TestAdoptForcePlatformPatchRebindsExtendedContract(t *testing.T) {
	repoPath, declared := extendedSourceRepoFor(t, "acme/renamed")
	report, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, true))
	if err != nil {
		t.Fatalf("--force refused an extended source contract: %v", err)
	}
	stageAdoptPaths(t, repoPath, paperclipFile, "hooks/notify.sh")
	manifest, err := config.LoadManifest(filepath.Join(repoPath, manifestFile))
	if err != nil {
		t.Fatal(err)
	}
	rebound := manifest.Register.Sources
	if _, err := cavemansource.ExtractDeclared(t.Context(), repoPath, rebound); err != nil {
		t.Fatalf("contract not bound to the patched harness: %v", err)
	}
	if !reflect.DeepEqual(rebound.Inputs, declared.Inputs) || rebound.SHA256 == declared.SHA256 {
		t.Fatalf("declared inputs not kept or digest not re-bound: %+v", rebound)
	}
	if !strings.Contains(reportDetail(report, manifestFile), "Re-bound register.sources to the rewritten Paperclip harness") {
		t.Fatalf("re-binding not reported: %q", reportDetail(report, manifestFile))
	}
	if !strings.Contains(mustRead(t, filepath.Join(repoPath, manifestFile)), "# operator: hook text is governed too") {
		t.Fatal("re-binding dropped the operator comment")
	}
}

// TestAdoptForceRefusesDriftedSourceContract: --force never re-blesses drift it did not
// cause; a contract that fails its own gate before the run still stops adoption.
func TestAdoptForceRefusesDriftedSourceContract(t *testing.T) {
	repoPath, _ := extendedSourceRepo(t)
	mustWrite(t, filepath.Join(repoPath, "hooks", "notify.sh"), "echo \"result: hook changed.\"\n")
	if _, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, true)); err == nil ||
		!strings.Contains(err.Error(), "fails its configured gate") {
		t.Fatalf("--force re-bound a drifted contract: %v", err)
	}
}

// TestAdoptForceLeavesCurrentContractUntouched: when the harness on disk is the current
// synthesis, --force writes none, so the contract and the manifest bytes stay.
func TestAdoptForceLeavesCurrentContractUntouched(t *testing.T) {
	repoPath := newTestRepo(t, "legacy")
	mustWrite(t, filepath.Join(repoPath, manifestFile), legacyManifest)
	if _, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, false)); err != nil {
		t.Fatal(err)
	}
	before := mustRead(t, filepath.Join(repoPath, manifestFile))
	report, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, true))
	if err != nil {
		t.Fatal(err)
	}
	if after := mustRead(t, filepath.Join(repoPath, manifestFile)); after != before {
		t.Fatalf("unchanged contract rewritten:\n%s", after)
	}
	if !strings.Contains(reportDetail(report, manifestFile), "Existing standards manifest preserved") {
		t.Fatalf("unexpected manifest note: %q", reportDetail(report, manifestFile))
	}
	requirePassingSourceGate(t, repoPath, paperclipFile)
}

// TestAdoptBindsSourcesUnderNullRegister: `register:` with no children, or with every child
// commented out, loads as no register policy. Adoption binds register.sources there instead
// of refusing a manifest the loader accepts, and keeps the operator's commented lines.
func TestAdoptBindsSourcesUnderNullRegister(t *testing.T) {
	for name, tail := range map[string]string{
		"empty register":     "register:\n",
		"commented children": "register:\n  # evidence:\n  #   inline_max_lines: 58\n",
		"explicit null":      "register: ~\n",
	} {
		t.Run(name, func(t *testing.T) {
			repoPath := newTestRepo(t, "legacy")
			mustWrite(t, filepath.Join(repoPath, manifestFile), legacyManifest+tail)
			if _, err := config.LoadManifest(filepath.Join(repoPath, manifestFile)); err != nil {
				t.Fatalf("fixture precondition: the loader must accept the manifest: %v", err)
			}
			if _, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, false)); err != nil {
				t.Fatalf("adopt refused a null register: %v", err)
			}
			got := mustRead(t, filepath.Join(repoPath, manifestFile))
			if !strings.HasPrefix(got, legacyManifest+"register:\n  sources:\n") ||
				strings.Contains(tail, "#") && !strings.HasSuffix(got, "  # evidence:\n  #   inline_max_lines: 58\n") {
				t.Fatalf("sources not bound under the null register as text:\n%s", got)
			}
			requirePassingSourceGate(t, repoPath, paperclipFile)
		})
	}
}

func manifestSourcesYAML(t *testing.T, sources *config.RegisterSources) string {
	t.Helper()
	data, err := yaml.Marshal(map[string]any{"register": map[string]any{"sources": sources}})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
