package forge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeWorkflowFixture(t *testing.T, root, name, content string) string {
	t.Helper()
	dir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRequiredStatusContextsCompleteInventoryBounds(t *testing.T) {
	root := t.TempDir()
	if names, err := RequiredStatusContexts(t.Context(), root); err != nil || len(names) != 0 {
		t.Fatalf("no workflows: %v %v", names, err)
	}
	for i := 0; i < maxWorkflowFiles; i++ {
		writeWorkflowFixture(t, root, fmt.Sprintf("%02d.yml", i), fmt.Sprintf("on: pull_request\njobs:\n  check_%d: {}\n", i))
	}
	if names, err := RequiredStatusContexts(t.Context(), root); err != nil || len(names) != maxWorkflowFiles {
		t.Fatalf("complete maximum inventory: %d %v", len(names), err)
	}
	writeWorkflowFixture(t, root, "overflow.yml", "on: pull_request\njobs: {}\n")
	if names, err := RequiredStatusContexts(t.Context(), root); err == nil || names != nil {
		t.Fatal("incomplete directory returned successful or partial contexts")
	}
	root = t.TempDir()
	var jobs strings.Builder
	jobs.WriteString("on: pull_request\njobs:\n")
	for i := 0; i <= maxJobsPerFile; i++ {
		fmt.Fprintf(&jobs, "  check_%d: {}\n", i)
	}
	writeWorkflowFixture(t, root, "large.yml", jobs.String())
	if names, err := RequiredStatusContexts(t.Context(), root); err == nil || names != nil {
		t.Fatal("incomplete job inventory returned successful or partial contexts")
	}
}

func TestRequiredStatusContextsRejectsUnsafeSources(t *testing.T) {
	for _, kind := range []string{"file-symlink", "directory-symlink", "oversize", "malformed"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := writeWorkflowFixture(t, root, "ci.yml", "on: pull_request\njobs:\n  verify: {}\n")
			var err error
			switch kind {
			case "file-symlink":
				if err = os.Remove(path); err == nil {
					err = os.Symlink(writeWorkflowFixture(t, t.TempDir(), "ci.yml", "jobs: {}"), path)
				}
			case "directory-symlink":
				root = t.TempDir()
				err = os.Symlink(filepath.Dir(filepath.Dir(path)), filepath.Join(root, ".github"))
			case "oversize":
				err = os.WriteFile(path, []byte(strings.Repeat(" ", (1<<20)+1)), 0o600)
			case "malformed":
				err = os.WriteFile(path, []byte("jobs: ["), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if names, err := RequiredStatusContexts(t.Context(), root); err == nil || names != nil {
				t.Fatalf("accepted unsafe %s workflow", kind)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := RequiredStatusContexts(ctx, t.TempDir()); err == nil {
		t.Fatal("cancelled workflow read succeeded")
	}
}

// A matrix job reports one check per leg, so the generator must expand its name rather than
// emit the template. Emitting "${{ matrix.name }}" verbatim would install a required status
// check that no run ever reports, which leaves every pull request "expected" forever.
func TestWorkflowContextsExpandsMatrixJobName(t *testing.T) {
	workflow := []byte(`
on:
  pull_request:
jobs:
  harness:
    name: Platform Neutrality (${{ matrix.name }})
    strategy:
      matrix:
        include:
          - os: ubuntu-latest
            name: Linux
          - os: windows-latest
            name: Windows
`)
	contexts, err := workflowPullRequestContexts(workflow)
	if err != nil {
		t.Fatalf("expand matrix job: %v", err)
	}
	want := []string{"Platform Neutrality (Linux)", "Platform Neutrality (Windows)"}
	if len(contexts) != len(want) {
		t.Fatalf("got %v, want %v", contexts, want)
	}
	for i := range want {
		if contexts[i] != want[i] {
			t.Fatalf("got %v, want %v", contexts, want)
		}
	}
}

func TestWorkflowContextsRejectsMatrixNameWithoutInclude(t *testing.T) {
	workflow := []byte("on:\n  pull_request:\njobs:\n  harness:\n    name: Gate (${{ matrix.name }})\n")
	if _, err := workflowPullRequestContexts(workflow); err == nil {
		t.Fatal("a name referencing a matrix with no include must fail, not emit the template")
	}
}

func TestWorkflowContextsRejectsUnresolvedExpression(t *testing.T) {
	// The leg supplies `os`, but the name interpolates `name`: substitution leaves the
	// expression intact and the context could never match a real check.
	workflow := []byte(`
on:
  pull_request:
jobs:
  harness:
    name: Gate (${{ matrix.name }})
    strategy:
      matrix:
        include:
          - os: ubuntu-latest
`)
	_, err := workflowPullRequestContexts(workflow)
	if err == nil {
		t.Fatal("an unresolved matrix reference must fail")
	}
	if !strings.Contains(err.Error(), "unresolved") {
		t.Fatalf("error should name the unresolved context, got %v", err)
	}
}

func TestWorkflowContextsLeavesPlainNamesAlone(t *testing.T) {
	// Boundary: the common case must not be disturbed, and a job with no name still falls
	// back to its identifier.
	workflow := []byte("on:\n  pull_request:\njobs:\n  verify:\n    name: Standards Gate\n  bare: {}\n")
	contexts, err := workflowPullRequestContexts(workflow)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"bare", "Standards Gate"}
	if len(contexts) != 2 || contexts[0] != want[0] || contexts[1] != want[1] {
		t.Fatalf("got %v, want %v", contexts, want)
	}
}

func TestWorkflowContextsBoundsMatrixLegs(t *testing.T) {
	var legs strings.Builder
	for i := 0; i <= maxMatrixLegs; i++ {
		fmt.Fprintf(&legs, "          - name: leg%d\n", i)
	}
	workflow := []byte("on:\n  pull_request:\njobs:\n  harness:\n    name: Gate (${{ matrix.name }})\n" +
		"    strategy:\n      matrix:\n        include:\n" + legs.String())
	if _, err := workflowPullRequestContexts(workflow); err == nil {
		t.Fatalf("a matrix beyond %d legs must fail the bound", maxMatrixLegs)
	}
}

// An advisory job reports success to the forge regardless of its result, so requiring it
// would install a check that can never fail. It must not reach the ruleset at all.
func TestWorkflowContextsSkipsAdvisoryJobs(t *testing.T) {
	for _, value := range []string{"true", "${{ matrix.experimental == true }}"} {
		workflow := []byte("on:\n  pull_request:\njobs:\n  advisory:\n    name: Advisory Gate\n" +
			"    continue-on-error: " + value + "\n  binding:\n    name: Binding Gate\n")
		contexts, err := workflowPullRequestContexts(workflow)
		if err != nil {
			t.Fatalf("continue-on-error %q: %v", value, err)
		}
		if len(contexts) != 1 || contexts[0] != "Binding Gate" {
			t.Fatalf("continue-on-error %q: got %v, want only the binding job", value, contexts)
		}
	}
}

func TestWorkflowContextsKeepsExplicitlyBindingJobs(t *testing.T) {
	// Boundary: `continue-on-error: false` is binding and must stay required.
	workflow := []byte("on:\n  pull_request:\njobs:\n  binding:\n    name: Binding Gate\n    continue-on-error: false\n")
	contexts, err := workflowPullRequestContexts(workflow)
	if err != nil {
		t.Fatal(err)
	}
	if len(contexts) != 1 || contexts[0] != "Binding Gate" {
		t.Fatalf("got %v, want the binding job", contexts)
	}
}
