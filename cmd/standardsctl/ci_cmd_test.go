package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/cifilter"
)

// newCIRepo builds a repository whose main branch holds code and whose docs branch
// changes only README.md, so the targeted decision is observable.
func newCIRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFixtureFile(t, dir, "main.go", "package main\n\nfunc main() {}\n")
	writeFixtureFile(t, dir, "README.md", "# Fixture\n")
	env := initGitFixture(t, dir)
	if out, err := runFixtureGit(t, dir, env, "checkout", "-q", "-b", "docs"); err != nil {
		t.Fatalf("checkout: %v (%s)", err, out)
	}
	writeFixtureFile(t, dir, "README.md", "# Fixture\n\nMore docs.\n")
	gitCommitAll(t, dir, env, "docs only")
	return dir
}

func runCIFilterCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return captureStdout(t, func() error { return dispatchCommand("ci", append([]string{"filter"}, args...)) })
}

func decodeDecision(t *testing.T, out string) *cifilter.FilterDecision {
	t.Helper()
	var dec cifilter.FilterDecision
	if err := json.Unmarshal([]byte(out), &dec); err != nil {
		t.Fatalf("decode decision: %v\n%s", err, out)
	}
	return &dec
}

func TestCIFilter_Positive_TargetedDecision(t *testing.T) {
	dir := newCIRepo(t)

	out, err := runCIFilterCmd(t, "--dir="+dir, "--base=main", "--head=HEAD", "--json")
	if err != nil {
		t.Fatalf("ci filter --json: %v\n%s", err, out)
	}
	dec := decodeDecision(t, out)
	if !dec.RunDocs || !dec.RunDocsOnly || dec.RunTests || !dec.SkipHeavyGates || dec.ChangeSet == nil || dec.ChangeSet.TotalFiles != 1 {
		t.Fatalf("expected a docs-only decision for one changed file, got %+v", dec)
	}

	// --force is observably different from the targeted decision.
	out, err = runCIFilterCmd(t, "--dir="+dir, "--base=main", "--head=HEAD", "--json", "--force")
	if err != nil {
		t.Fatalf("ci filter --force: %v", err)
	}
	forced := decodeDecision(t, out)
	if !forced.RunTests || !forced.RunDocs || forced.RunDocsOnly || !strings.Contains(forced.Reason, "force") {
		t.Fatalf("expected the full matrix under --force, got %+v", forced)
	}

	// The human summary renders the same decision.
	out, err = runCIFilterCmd(t, "--dir="+dir, "--base=main", "--head=HEAD")
	if err != nil {
		t.Fatalf("ci filter summary: %v", err)
	}
	mustContain(t, out, "Run Docs:          true", "Docs Only:         true", "Run Tests:         false")
}

func TestCIFilter_Env_WritesOnlyToTheProvidedOutputFile(t *testing.T) {
	dir := newCIRepo(t)
	outFile := filepath.Join(t.TempDir(), "github_output")
	if err := os.WriteFile(outFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	// Positive: the decision lines are appended to the file named by GITHUB_OUTPUT.
	t.Setenv("GITHUB_OUTPUT", outFile)
	printed, err := runCIFilterCmd(t, "--dir="+dir, "--base=main", "--head=HEAD", "--env")
	if err != nil {
		t.Fatalf("ci filter --env: %v", err)
	}
	if printed == "" || !strings.Contains(printed, "=") {
		t.Fatalf("expected key=value lines, got %q", printed)
	}
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != printed {
		t.Fatalf("GITHUB_OUTPUT content %q differs from printed %q", string(data), printed)
	}

	// Boundary: without GITHUB_OUTPUT nothing is appended and the command still succeeds.
	t.Setenv("GITHUB_OUTPUT", "")
	if _, err := runCIFilterCmd(t, "--dir="+dir, "--base=main", "--head=HEAD", "--env"); err != nil {
		t.Fatalf("ci filter --env without GITHUB_OUTPUT: %v", err)
	}
	after, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != printed {
		t.Fatal("the output file must not change when GITHUB_OUTPUT is unset")
	}

	// Negative: an unwritable output file is an error.
	t.Setenv("GITHUB_OUTPUT", filepath.Join(t.TempDir(), "missing", "out"))
	_, err = runCIFilterCmd(t, "--dir="+dir, "--base=main", "--head=HEAD", "--env")
	mustErrContain(t, err, "GITHUB_OUTPUT")
}

func TestCIFilter_Boundary(t *testing.T) {
	// Outside a repository the filter fails safe to the full matrix.
	dir := t.TempDir()
	out, err := runCIFilterCmd(t, "--dir="+dir, "--json")
	if err != nil {
		t.Fatalf("ci filter outside git: %v", err)
	}
	dec := decodeDecision(t, out)
	if !dec.RunTests || !dec.RunLinters || !dec.RunSecurity || !dec.RunDocs {
		t.Fatalf("expected the full matrix outside a repository, got %+v", dec)
	}

	_, err = runCIFilterCmd(t, "--dir="+dir, "extra")
	mustErrContain(t, err, "no positional arguments")
}
