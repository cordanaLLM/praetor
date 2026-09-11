package cifilter_test

import (
	"context"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/cifilter"
)

func TestClassifyChanges_DocsOnly(t *testing.T) {
	files := []string{
		"docs/guides/getting-started.md",
		"README.md",
		"LICENSE",
		"assets/logo.png",
	}

	cs := cifilter.ClassifyChanges(files)
	if !cs.DocsOnly {
		t.Errorf("expected DocsOnly=true, got false")
	}
	if !cs.DocsChanged {
		t.Errorf("expected DocsChanged=true, got false")
	}
	if cs.CodeChanged {
		t.Errorf("expected CodeChanged=false, got true")
	}
	if cs.ConfigChanged {
		t.Errorf("expected ConfigChanged=false, got true")
	}
}

func TestClassifyChanges_CodeAndTests(t *testing.T) {
	files := []string{
		"internal/cifilter/filter.go",
		"internal/cifilter/cifilter_test.go",
		".github/workflows/ci.yml",
	}

	cs := cifilter.ClassifyChanges(files)
	if cs.DocsOnly {
		t.Errorf("expected DocsOnly=false, got true")
	}
	if !cs.CodeChanged {
		t.Errorf("expected CodeChanged=true, got false")
	}
	if !cs.TestsChanged {
		t.Errorf("expected TestsChanged=true, got false")
	}
	if !cs.ConfigChanged {
		t.Errorf("expected ConfigChanged=true, got false")
	}
}

func TestClassifyChanges_StateOnly(t *testing.T) {
	files := []string{
		".workingdir/STATE.md",
		".workingdir/OPEN.md",
		".workingdir/BUGS.md",
	}

	cs := cifilter.ClassifyChanges(files)
	if !cs.StateOnly {
		t.Errorf("expected StateOnly=true, got false")
	}
	if cs.CodeChanged || cs.ConfigChanged || cs.DocsOnly {
		t.Errorf("unexpected flag set on StateOnly change")
	}
}

func TestClassifyChanges_Boundary_Empty(t *testing.T) {
	cs := cifilter.ClassifyChanges([]string{})
	if cs.TotalFiles != 0 {
		t.Errorf("expected TotalFiles=0, got %d", cs.TotalFiles)
	}
	if cs.DocsOnly || cs.StateOnly || cs.CodeChanged {
		t.Errorf("expected all false on empty change set")
	}
}

func TestMakeDecision_DocsOnly(t *testing.T) {
	cs := &cifilter.ChangeSet{
		TotalFiles:  2,
		DocsChanged: true,
		DocsOnly:    true,
	}

	dec := cifilter.MakeDecision(cs, false)
	if dec.RunTests {
		t.Errorf("expected RunTests=false for docs-only, got true")
	}
	if dec.RunLinters {
		t.Errorf("expected RunLinters=false for docs-only, got true")
	}
	if dec.RunSecurity {
		t.Errorf("expected RunSecurity=false for docs-only, got true")
	}
	if !dec.RunAudit {
		t.Errorf("expected RunAudit=true for docs-only, got false")
	}
	if !dec.RunDocsOnly {
		t.Errorf("expected RunDocsOnly=true for docs-only, got false")
	}
	if !dec.SkipHeavyGates {
		t.Errorf("expected SkipHeavyGates=true for docs-only, got false")
	}
}

func TestMakeDecision_CodeChanged(t *testing.T) {
	cs := &cifilter.ChangeSet{
		TotalFiles:  1,
		CodeChanged: true,
	}

	dec := cifilter.MakeDecision(cs, false)
	if !dec.RunTests {
		t.Errorf("expected RunTests=true for code changes, got false")
	}
	if !dec.RunLinters {
		t.Errorf("expected RunLinters=true for code changes, got false")
	}
	if !dec.RunSecurity {
		t.Errorf("expected RunSecurity=true for code changes, got false")
	}
	if dec.SkipHeavyGates {
		t.Errorf("expected SkipHeavyGates=false for code changes, got true")
	}
}

func TestMakeDecision_ForceAll(t *testing.T) {
	cs := &cifilter.ChangeSet{
		TotalFiles:  1,
		DocsChanged: true,
		DocsOnly:    true,
	}

	dec := cifilter.MakeDecision(cs, true)
	if !dec.RunTests || !dec.RunLinters || !dec.RunSecurity {
		t.Errorf("expected all gates enabled when ForceAll=true")
	}
	if dec.SkipHeavyGates {
		t.Errorf("expected SkipHeavyGates=false when ForceAll=true")
	}
}

func TestMakeDecision_StateOnly(t *testing.T) {
	cs := &cifilter.ChangeSet{
		TotalFiles: 1,
		StateOnly:  true,
	}

	dec := cifilter.MakeDecision(cs, false)
	if dec.RunTests || dec.RunLinters || dec.RunSecurity || dec.RunAudit {
		t.Errorf("expected all gates skipped when StateOnly=true")
	}
	if !dec.SkipHeavyGates {
		t.Errorf("expected SkipHeavyGates=true when StateOnly=true")
	}
}

func TestFilterDecision_Formatting(t *testing.T) {
	dec := &cifilter.FilterDecision{
		RunTests:       true,
		RunLinters:     false,
		RunSecurity:    true,
		RunAudit:       true,
		RunDocsOnly:    false,
		SkipHeavyGates: false,
		Reason:         "test run",
	}

	out := dec.FormatGitHubOutput()
	if !strings.Contains(out, "run_tests=true") {
		t.Errorf("expected run_tests=true in GitHub output, got: %s", out)
	}
	if !strings.Contains(out, "run_linters=false") {
		t.Errorf("expected run_linters=false in GitHub output, got: %s", out)
	}

	jsonBytes, err := dec.ToJSON()
	if err != nil {
		t.Fatalf("failed serializing to JSON: %v", err)
	}
	if len(jsonBytes) == 0 {
		t.Errorf("expected non-empty JSON output")
	}
}

func TestAnalyzeChanges_FallbackOnInvalidDir(t *testing.T) {
	ctx := context.Background()
	opts := cifilter.FilterOptions{
		RepoDir: "/nonexistent/invalid/directory/xyz",
	}

	dec, err := cifilter.AnalyzeChanges(ctx, opts)
	if err != nil {
		t.Fatalf("expected fallback decision, got error: %v", err)
	}
	if !dec.RunTests || !dec.RunLinters || !dec.RunSecurity {
		t.Errorf("expected fail-safe execution (all true) on git diff error")
	}
}
