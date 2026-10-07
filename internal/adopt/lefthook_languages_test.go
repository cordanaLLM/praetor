package adopt

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"gopkg.in/yaml.v3"
)

const (
	goModMarker   = "module example.com/widget\n\ngo 1.27\n"
	cargoTOMLBody = "[package]\nname = \"widget\"\nversion = \"0.1.0\"\nedition = \"2021\"\n"
)

var (
	goLanguageJobs   = []string{"pre-commit/commands/gofmt", "pre-commit/commands/govet", "pre-push/commands/security"}
	rustLanguageJobs = []string{"pre-commit/commands/clippy", "pre-commit/commands/rustfmt"}
)

// adoptMarkedRepo adopts a fresh repository holding markers, with lefthook (when not empty)
// written before adoption, and returns the repository and the report.
func adoptMarkedRepo(t *testing.T, name string, markers map[string]string, lefthook string) (string, *AdoptReport) {
	t.Helper()
	repoPath := newTestRepo(t, name)
	for rel, body := range markers {
		mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(rel)), body)
	}
	if lefthook != "" {
		mustWrite(t, filepath.Join(repoPath, lefthookFile), lefthook)
	}
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	assertNoIssues(t, rep)
	return repoPath, rep
}

// writtenLefthookJobs names the jobs of the lefthook.yml adoption left in repoPath.
func writtenLefthookJobs(t *testing.T, repoPath string) map[string]bool {
	t.Helper()
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(mustRead(t, filepath.Join(repoPath, lefthookFile))), &parsed); err != nil {
		t.Fatalf("lefthook.yml does not parse: %v", err)
	}
	return lefthookJobs(parsed)
}

// assertJobs fails unless jobs holds every name of present and none of absent.
func assertJobs(t *testing.T, label string, jobs map[string]bool, present, absent []string) {
	t.Helper()
	for _, job := range present {
		if !jobs[job] {
			t.Errorf("%s: lefthook.yml lacks %s", label, job)
		}
	}
	for _, job := range absent {
		if jobs[job] {
			t.Errorf("%s: lefthook.yml carries %s", label, job)
		}
	}
}

// Positive (#568): the generated jobs follow the languages the verification plan detects. A
// Cargo-only repository gets cargo fmt and cargo clippy and no Go job, a Go-only one the Go jobs
// and no Cargo job, a mixed one both; a Cargo workspace whose Go module sits in a subdirectory
// is a Cargo repository, as its harness rows say. Each rendering is praetor's and is activated.
func TestAdopt_Positive_LefthookJobsFollowDetectedLanguages(t *testing.T) {
	cases := []struct {
		name            string
		markers         map[string]string
		header          string
		present, absent []string
	}{
		{"cargo-only", map[string]string{"Cargo.toml": cargoTOMLBody}, "(Rust & HISS Governance)", rustLanguageJobs, goLanguageJobs},
		{"go-only", map[string]string{"go.mod": goModMarker}, "(Go & HISS Governance)", goLanguageJobs, rustLanguageJobs},
		{"mixed", map[string]string{"go.mod": goModMarker, "Cargo.toml": cargoTOMLBody}, "(Go, Rust & HISS Governance)",
			append(append([]string{}, goLanguageJobs...), rustLanguageJobs...), nil},
		{"cargo-nested-go", map[string]string{"Cargo.toml": cargoTOMLBody, "tooling/go.mod": goModMarker}, "(Rust & HISS Governance)",
			rustLanguageJobs, goLanguageJobs},
	}
	for _, tc := range cases {
		repoPath, rep := adoptMarkedRepo(t, tc.name, tc.markers, "")
		present := append(append([]string{}, tc.present...), "pre-commit/commands/hiss-audit", "pre-push/commands/gate")
		assertJobs(t, tc.name, writtenLefthookJobs(t, repoPath), present, tc.absent)
		written := mustRead(t, filepath.Join(repoPath, lefthookFile))
		if !strings.HasPrefix(written, "# Lefthook Configuration "+tc.header+"\n") {
			t.Errorf("%s: the header does not name the languages: %q", tc.name, strings.SplitN(written, "\n", 2)[0])
		}
		if !hasAction(rep, lefthookFile, actionCreate) || !fileExists(filepath.Join(repoPath, ".git", "hooks", preCommitHook)) {
			t.Errorf("%s: the rendering was not created and activated: %+v", tc.name, rep.ActionDetails)
		}
	}
}

// rustSystemsLefthookSetting is rust-systems' lefthook.yml setting.
func rustSystemsLefthookSetting(t *testing.T) flavor.SettingItem {
	t.Helper()
	rust, err := flavor.Get("rust-systems")
	if err != nil {
		t.Fatal(err)
	}
	for _, setting := range rust.RequiredSettings() {
		if setting.Path == lefthookFile {
			return setting
		}
	}
	t.Fatal("rust-systems declares no lefthook.yml setting")
	return flavor.SettingItem{}
}

// Negative (#568): the Go-only renderings earlier releases wrote into every repository are
// earlier Praetor output, so a plain run migrates them to the rendering for the repository's
// languages, a reconcile with no backup, and a Cargo repository's lefthook.yml then satisfies
// the setting rust-systems describes as clippy and rustfmt enforcement, which the Go-only text
// did not.
func TestAdopt_Negative_GoEveryRepositoryRenderingMigratesToCargoJobs(t *testing.T) {
	fixtures := readPriorLefthookFixtures(t)
	setting := rustSystemsLefthookSetting(t)
	for _, name := range []string{"go-every-repository.lefthook.yml", "go-every-repository-checkpoint.lefthook.yml"} {
		if setting.Validator(fixtures[name]) {
			t.Fatalf("%s: the Go-only rendering already satisfies rust-systems", name)
		}
		repoPath, rep := adoptMarkedRepo(t, "go-every-repository", map[string]string{"Cargo.toml": cargoTOMLBody}, string(fixtures[name]))
		if got := mustRead(t, filepath.Join(repoPath, lefthookFile)); got != buildLefthookYAMLFor(lefthookShape{languages: hisscatalog.LanguageRust}, false) {
			t.Fatalf("%s: not migrated to the Cargo rendering:\n%s", name, got)
		}
		if !hasAction(rep, lefthookFile, actionReconcile) || hasAction(rep, lefthookFile, actionReplace) {
			t.Errorf("%s: want a reconcile and no replace: %+v", name, rep.ActionDetails)
		}
		if !flavor.SettingSatisfied(repoPath, setting) {
			t.Errorf("%s: the migrated lefthook.yml does not satisfy rust-systems", name)
		}
	}
}

// Boundary: a repository with no detected language keeps every language's jobs, each guarded by
// its root marker, as it keeps every HISS clause; one whose only detected language has no jobs
// (Python) gets the governance jobs alone and a header naming no language.
func TestAdopt_Boundary_LefthookJobsForUnknownAndJoblessLanguages(t *testing.T) {
	every := append(append([]string{}, goLanguageJobs...), rustLanguageJobs...)
	repoPath, _ := adoptMarkedRepo(t, "no-marker", nil, "")
	assertJobs(t, "no marker", writtenLefthookJobs(t, repoPath), every, nil)
	repoPath, rep := adoptMarkedRepo(t, "python-only", map[string]string{"pytest.ini": "[pytest]\n"}, "")
	assertJobs(t, "python only", writtenLefthookJobs(t, repoPath), []string{"pre-commit/commands/context-check", "pre-push/commands/audit"}, every)
	if got := mustRead(t, filepath.Join(repoPath, lefthookFile)); !strings.HasPrefix(got, "# Lefthook Configuration (HISS Governance)\n") {
		t.Errorf("python only: the header names a language: %q", strings.SplitN(got, "\n", 2)[0])
	}
	if !hasAction(rep, lefthookFile, actionCreate) {
		t.Errorf("python only: lefthook.yml not created: %+v", rep.ActionDetails)
	}
}
