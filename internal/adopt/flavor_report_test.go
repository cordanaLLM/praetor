package adopt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// flavorDetail returns the first action detail recorded for rel whose text names a flavor
// template, so a detail another adoption step wrote for the same path does not count.
func flavorDetail(rep *AdoptReport, rel string) (ActionDetail, bool) {
	for _, d := range rep.ActionDetails {
		if d.Path == rel && strings.Contains(d.Details, "flavor template") {
			return d, true
		}
	}
	return ActionDetail{}, false
}

// TestAdopt_Positive_ReportsFlavorScaffold pins the adoption half of BUG-188: the apply report
// used to be discarded, so the flavor templates adoption wrote never reached its report.
func TestAdopt_Positive_ReportsFlavorScaffold(t *testing.T) {
	repoPath := newTestRepo(t, "flavor-service")
	mustWrite(t, filepath.Join(repoPath, "go.mod"), "module github.com/test/svc\n")
	mustWrite(t, filepath.Join(repoPath, "cmd", "svc", "main.go"), "package main\n\nfunc main() {}\n")

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)
	d, ok := flavorDetail(rep, "Dockerfile")
	if !ok || d.Action != actionCreate || !strings.Contains(d.Details, "go-service") || !contains(rep.CreatedFiles, "Dockerfile") {
		t.Fatalf("the go-service Dockerfile must be reported as created by the flavor, got %+v in %v", d, rep.CreatedFiles)
	}
}

// newImageForge returns an os-image repository carrying the given files.
func newImageForge(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	repoPath := newTestRepo(t, name)
	mustWrite(t, filepath.Join(repoPath, "mkosi.conf"), "[Output]\nFormat=disk\n")
	for rel, body := range files {
		mustWrite(t, filepath.Join(repoPath, rel), body)
	}
	return repoPath
}

// adoptForge adopts an image forge under the os-image profile and fails the test on any error.
// The os-image flavor implements that profile, and adoption resolves the flavor only among the
// flavors of the profile it records (BUG-940), so the forge must declare it for mkosi.conf to
// select the os-image templates.
func adoptForge(t *testing.T, repoPath string) *AdoptReport {
	t.Helper()
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "os-image"})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)
	return rep
}

// TestAdopt_Positive_KeepsAnExistingYamllintConfig pins #505: adoption wrote a .yamllint.yml
// beside the repository's .yamllint.yaml, which yamllint reads first, so the new file was
// configuration nothing read. The existing file stays, and the report names it.
func TestAdopt_Positive_KeepsAnExistingYamllintConfig(t *testing.T) {
	const policy = "extends: default\nrules:\n  line-length: disable\n"
	repoPath := newImageForge(t, "forge-yamllint-yaml", map[string]string{".yamllint.yaml": policy})
	rep := adoptForge(t, repoPath)
	if _, err := os.Stat(filepath.Join(repoPath, ".yamllint.yml")); !os.IsNotExist(err) {
		t.Fatalf("adoption wrote .yamllint.yml beside .yamllint.yaml: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(repoPath, ".yamllint.yaml")); err != nil || string(got) != policy {
		t.Fatalf("adoption changed .yamllint.yaml: %q, %v", got, err)
	}
	d, ok := flavorDetail(rep, ".yamllint.yaml")
	if !ok || d.Action != actionSkip || !strings.Contains(d.Details, ".yamllint.yml was not written beside it") {
		t.Fatalf("the kept configuration must be reported, got %+v in %+v", d, rep.ActionDetails)
	}
	if _, ok := flavorDetail(rep, ".yamllint.yml"); ok || contains(rep.CreatedFiles, ".yamllint.yml") {
		t.Errorf("the report names a .yamllint.yml the repository does not have: %+v", rep.ActionDetails)
	}
}

// TestAdopt_Negative_ScaffoldsTheDefaultYamllintConfig asserts a forge with no yamllint
// configuration still gets the default one.
func TestAdopt_Negative_ScaffoldsTheDefaultYamllintConfig(t *testing.T) {
	repoPath := newImageForge(t, "forge-no-yamllint", nil)
	rep := adoptForge(t, repoPath)
	d, ok := flavorDetail(rep, ".yamllint.yml")
	if !ok || d.Action != actionCreate || !contains(rep.CreatedFiles, ".yamllint.yml") {
		t.Fatalf("the default .yamllint.yml must be scaffolded, got %+v in %v", d, rep.CreatedFiles)
	}
}

// TestAdopt_Boundary_ExistingScaffoldNameIsAnExistingFile asserts the scaffolded name already
// present keeps the existing-file report, not the covered one.
func TestAdopt_Boundary_ExistingScaffoldNameIsAnExistingFile(t *testing.T) {
	repoPath := newImageForge(t, "forge-yamllint-yml", map[string]string{".yamllint.yml": "extends: relaxed\n"})
	rep := adoptForge(t, repoPath)
	d, ok := flavorDetail(rep, ".yamllint.yml")
	if !ok || d.Action != actionSkip || !strings.Contains(d.Details, "was not written over it") {
		t.Fatalf("the existing .yamllint.yml must be reported kept, got %+v", d)
	}
}

// TestAdopt_Negative_FlavorWriteFailureIsAnError asserts a template the flavor could not write
// fails the adoption instead of disappearing with the discarded apply report.
func TestAdopt_Negative_FlavorWriteFailureIsAnError(t *testing.T) {
	repoPath := newTestRepo(t, "flavor-blocked")
	mustWrite(t, filepath.Join(repoPath, "go.mod"), "module github.com/test/svc\n")
	mustWrite(t, filepath.Join(repoPath, "cmd", "svc", "main.go"), "package main\n\nfunc main() {}\n")
	// A directory where the flavor writes its Dockerfile makes that one template unreadable.
	if err := os.MkdirAll(filepath.Join(repoPath, "Dockerfile"), 0o755); err != nil {
		t.Fatal(err)
	}

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("a flavor failure is recorded in the report, not returned: %v", err)
	}
	found := false
	for _, e := range rep.Errors {
		found = found || (strings.Contains(e, "apply flavor go-service") && strings.Contains(e, "Dockerfile"))
	}
	if !found {
		t.Fatalf("the failed flavor template must reach the report errors, got %v", rep.Errors)
	}
	if _, ok := flavorDetail(rep, ".gosec.json"); !ok {
		t.Errorf("templates written before the failure must still be reported, got %+v", rep.ActionDetails)
	}
}

// TestAdopt_Boundary_NoFlavorMatchScaffoldsNothing pins BUG-939: a repository no flavor matches
// used to be scaffolded as go-library, because detection substituted that name.
func TestAdopt_Boundary_NoFlavorMatchScaffoldsNothing(t *testing.T) {
	repoPath := newTestRepo(t, "flavor-unmatched")
	mustWrite(t, filepath.Join(repoPath, "Rakefile"), "task :default\n")

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)
	if !hasAction(rep, flavorReportPath, actionSkip) {
		t.Fatalf("an unmatched repository must record the flavor scaffold as skipped, got %+v", rep.ActionDetails)
	}
	warned := false
	for _, w := range rep.Warnings {
		warned = warned || strings.Contains(w, "Not applicable: profile template-seed has no flavor")
	}
	if !warned {
		t.Errorf("the operator must be told no flavor applied, got warnings %v", rep.Warnings)
	}
	for _, d := range rep.ActionDetails {
		if strings.Contains(d.Details, "go-library flavor template") {
			t.Errorf("no go-library template may be scaffolded for an unmatched repository: %+v", d)
		}
	}
}

// pinnedManifest declares framework and the given flavors pins.
func pinnedManifest(pins string) string {
	return "version: 1\nrepository:\n  owner: acme\n  name: svc\nprofiles:\n  - framework\n" + pins
}

// adoptGoModule adopts a Go module with a cmd/ entry point under manifest and returns the report.
func adoptGoModule(t *testing.T, name, manifest string) (*AdoptReport, string) {
	t.Helper()
	repoPath := newTestRepo(t, name)
	mustWrite(t, filepath.Join(repoPath, "go.mod"), "module github.com/test/svc\n")
	mustWrite(t, filepath.Join(repoPath, "cmd", "svc", "main.go"), "package main\n\nfunc main() {}\n")
	mustWrite(t, filepath.Join(repoPath, ".standards.yaml"), manifest)
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	return rep, repoPath
}

// TestAdopt_Positive_FollowsTheFlavorPin: a root pin of go-library wins over detection, which
// would scaffold go-service and its Dockerfile; the audit then measures what adoption wrote.
func TestAdopt_Positive_FollowsTheFlavorPin(t *testing.T) {
	_, repoPath := adoptGoModule(t, "pin-root", pinnedManifest("flavors:\n  - name: go-library\n"))
	if _, err := os.Stat(filepath.Join(repoPath, "Dockerfile")); err == nil {
		t.Fatal("adoption scaffolded go-service's Dockerfile although go-library is pinned")
	}
	if _, err := os.Stat(filepath.Join(repoPath, ".github", "workflows", "ci.yml")); err != nil {
		t.Fatalf("adoption must scaffold the pinned go-library workflow: %v", err)
	}
}

// TestAdopt_Negative_SkipsAFlavorItCannotScaffoldAtTheRoot: a directory-scoped pin names no root
// flavor, so adoption scaffolds no flavor template and says why.
func TestAdopt_Negative_SkipsAFlavorItCannotScaffoldAtTheRoot(t *testing.T) {
	rep, repoPath := adoptGoModule(t, "pin-scoped", pinnedManifest("flavors:\n  - name: go-service\n    path: cmd\n"))
	if _, err := os.Stat(filepath.Join(repoPath, "Dockerfile")); err == nil {
		t.Fatal("adoption scaffolded a flavor at the root for a directory-scoped pin")
	}
	var warned bool
	for _, d := range rep.ActionDetails {
		warned = warned || strings.Contains(d.Details, "flavors pins in .standards.yaml are scoped")
	}
	if !warned {
		t.Errorf("the skip must name the pins, got %+v", rep.ActionDetails)
	}
}
