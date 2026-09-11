package adopt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// =========================================================================
// Positive 3D Tests
// =========================================================================

func initTestGit(t *testing.T, dir string) {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0644); err != nil {
		t.Fatalf("write HEAD: %v", err)
	}
}

func TestAdopt_Positive_Greenfield(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	repoPath := filepath.Join(tmpDir, "new-service")
	_ = os.MkdirAll(repoPath, 0755)
	initTestGit(t, repoPath)
	_ = os.WriteFile(filepath.Join(repoPath, "go.mod"), []byte("module github.com/test/service\n"), 0644)

	opts := AdoptOptions{
		Path:    repoPath,
		Profile: "framework",
		DryRun:  false,
	}

	report, err := Adopt(ctx, opts)
	if err != nil {
		t.Fatalf("Adopt greenfield failed: %v", err)
	}

	if report.State != StateGreenfield {
		t.Fatalf("expected state greenfield, got: %s", report.State)
	}
	if report.Archetype != "framework" {
		t.Fatalf("expected framework archetype, got: %s", report.Archetype)
	}
	if len(report.CreatedFiles) == 0 {
		t.Fatal("expected created files in report")
	}

	// Verify files physically exist
	expectedFiles := []string{
		".standards.yaml",
		".standards.lock",
		".standards-baseline.json",
		"AGENTS.md",
		"CLAUDE.md",
		".devcontainer/devcontainer.json",
		"Makefile",
		".gitignore",
	}
	for _, ef := range expectedFiles {
		p := filepath.Join(repoPath, ef)
		if _, err := os.Stat(p); os.IsNotExist(err) {
			t.Fatalf("expected file %s to exist after greenfield adoption", ef)
		}
	}
}

func TestAdopt_Positive_PartialAndDryRun(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	repoPath := filepath.Join(tmpDir, "partial-repo")
	_ = os.MkdirAll(repoPath, 0755)
	initTestGit(t, repoPath)
	_ = os.WriteFile(filepath.Join(repoPath, ".standards.yaml"), []byte("version: 1\n"), 0644)

	// Dry run
	optsDry := AdoptOptions{
		Path:   repoPath,
		DryRun: true,
	}
	repDry, err := Adopt(ctx, optsDry)
	if err != nil {
		t.Fatalf("Adopt dry-run failed: %v", err)
	}
	if repDry.State != StatePartial {
		t.Fatalf("expected partial state, got: %s", repDry.State)
	}
	if _, err := os.Stat(filepath.Join(repoPath, ".standards.lock")); !os.IsNotExist(err) {
		t.Fatal("dry-run must not create .standards.lock")
	}

	// Live run
	optsLive := AdoptOptions{
		Path:   repoPath,
		DryRun: false,
	}
	repLive, err := Adopt(ctx, optsLive)
	if err != nil {
		t.Fatalf("Adopt live failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoPath, ".standards.lock")); os.IsNotExist(err) {
		t.Fatal("live adopt should create missing .standards.lock")
	}
	if len(repLive.ReconciledFiles) == 0 {
		t.Fatal("expected reconciled files in report")
	}
}

func TestAdopt_Positive_BrownfieldWithDebtRatcheting(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	repoPath := filepath.Join(tmpDir, "legacy-repo")
	_ = os.MkdirAll(repoPath, 0755)
	initTestGit(t, repoPath)

	// Simulate legacy files with HISS violation
	legacyCode := "package main\nfunc run() {\n\t_ = doSomething()\n}\n"
	_ = os.WriteFile(filepath.Join(repoPath, "main.go"), []byte(legacyCode), 0644)

	opts := AdoptOptions{
		Path:           repoPath,
		RecordBaseline: true,
		DryRun:         false,
	}

	rep, err := Adopt(ctx, opts)
	if err != nil {
		t.Fatalf("Adopt brownfield failed: %v", err)
	}

	if rep.LegacyDebtCount != 1 {
		t.Fatalf("expected 1 legacy infraction recorded, got: %d", rep.LegacyDebtCount)
	}

	// Check baseline file content
	data, err := os.ReadFile(filepath.Join(repoPath, ".standards-baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("baseline file should not be empty")
	}
}

// =========================================================================
// Negative 3D Tests
// =========================================================================

func TestAdopt_Negative_NilContext(t *testing.T) {
	opts := AdoptOptions{Path: "/tmp"}
	_, err := Adopt(nil, opts)
	if err == nil {
		t.Fatal("expected error with nil context")
	}
}

func TestAdopt_Negative_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	opts := AdoptOptions{Path: "/tmp"}
	_, err := Adopt(ctx, opts)
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
}

func TestAdopt_Negative_NonExistentPath(t *testing.T) {
	ctx := context.Background()
	opts := AdoptOptions{Path: "/tmp/this/path/absolutely/does/not/exist/ever"}
	_, err := Adopt(ctx, opts)
	if err == nil {
		t.Fatal("expected error with non-existent path")
	}
}

func TestAdopt_Negative_PathIsFileNotDir(t *testing.T) {
	ctx := context.Background()
	tmpFile, err := os.CreateTemp("", "adopt_test_file_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	opts := AdoptOptions{Path: tmpFile.Name()}
	_, err = Adopt(ctx, opts)
	if err == nil {
		t.Fatal("expected error when path is a regular file")
	}
}

// =========================================================================
// Boundary 3D Tests
// =========================================================================

func TestAdopt_Boundary_EmptyRepoPathDefaults(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	emptyRepo := filepath.Join(tmpDir, "empty")
	_ = os.MkdirAll(emptyRepo, 0755)
	initTestGit(t, emptyRepo)

	opts := AdoptOptions{
		Path:   emptyRepo,
		DryRun: true,
	}

	rep, err := Adopt(ctx, opts)
	if err != nil {
		t.Fatalf("Adopt on empty repo failed: %v", err)
	}

	if rep.Archetype != "template-seed" {
		t.Fatalf("expected template-seed archetype for empty dir, got: %s", rep.Archetype)
	}
	if len(rep.Facets) != 4 {
		t.Fatalf("expected 4 default facets, got: %d", len(rep.Facets))
	}
}

func TestDetectState_Boundary(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Completely empty directory
	state1 := DetectState(tmpDir)
	if state1 != StateGreenfield {
		t.Fatalf("expected greenfield for empty dir, got: %s", state1)
	}

	// 2. Only AGENTS.md exists
	_ = os.WriteFile(filepath.Join(tmpDir, "AGENTS.md"), []byte("rules"), 0644)
	state2 := DetectState(tmpDir)
	if state2 != StatePartial {
		t.Fatalf("expected partial for dir with only AGENTS.md, got: %s", state2)
	}

	// 3. All files exist
	_ = os.WriteFile(filepath.Join(tmpDir, ".standards.yaml"), []byte("manifest"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, ".standards.lock"), []byte("lock"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, ".standards-baseline.json"), []byte("{}"), 0644)
	state3 := DetectState(tmpDir)
	if state3 != StateBrownfield {
		t.Fatalf("expected brownfield for dir with all files, got: %s", state3)
	}
}

func TestResolveArchetype_Meson(t *testing.T) {
	tmpDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(tmpDir, "meson.build"), []byte("project('vmafx')"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module vmafx"), 0644)

	arch := resolveArchetype(tmpDir, "")
	if arch != "native-gpu-systems" {
		t.Fatalf("expected native-gpu-systems for meson project, got: %s", arch)
	}
}

func TestExtractOwnerFromURL(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"https://github.com/VMAFx/vmafx.git", "VMAFx"},
		{"git@github.com:VMAFx/pelorus.git", "VMAFx"},
		{"https://git.dev.cauda.dev/cordanaLLM/praetor.git", "cordanaLLM"},
		{"https://github.com/lusoris/home-zeus", "lusoris"},
	}

	for _, tc := range tests {
		got := extractOwnerFromURL(tc.url)
		if got != tc.expected {
			t.Errorf("for %s: expected %s, got %s", tc.url, tc.expected, got)
		}
	}
}

func TestExtractRepoFromURL(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		// Positive
		{"https://github.com/VMAFx/vmafx.git", "vmafx"},
		{"git@github.com:VMAFx/pelorus.git", "pelorus"},
		{"https://github.com/lusoris/home-zeus", "home-zeus"},
		{"https://git.dev.cauda.dev/cordanaLLM/praetor.git/", "praetor"},
		// Negative & Boundary
		{"", ""},
		{"/", ""},
		{"invalid", "invalid"},
	}

	for _, tc := range tests {
		got := extractRepoFromURL(tc.url)
		if got != tc.expected {
			t.Errorf("for %s: expected %s, got %s", tc.url, tc.expected, got)
		}
	}
}

func TestAdopt_MultiLanguageLegacyDebt(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	initTestGit(t, tmpDir)

	// Write C file with while (1) and strcpy
	cCode := "#include <stdio.h>\nvoid test() {\n    while (1) {}\n    strcpy(dst, src);\n}\n"
	_ = os.WriteFile(filepath.Join(tmpDir, "kernel.c"), []byte(cCode), 0644)

	// Write Python file with while True and bare except
	pyCode := "while True:\n    try:\n        pass\n    except:\n        pass\n"
	_ = os.WriteFile(filepath.Join(tmpDir, "script.py"), []byte(pyCode), 0644)

	// Write Rust file with unwrap
	rsCode := "fn main() {\n    let val = Some(1).unwrap();\n}\n"
	_ = os.WriteFile(filepath.Join(tmpDir, "lib.rs"), []byte(rsCode), 0644)

	opts := AdoptOptions{
		Path:           tmpDir,
		Profile:        "native-gpu-systems",
		RecordBaseline: true,
		DryRun:         true,
	}

	rep, err := Adopt(ctx, opts)
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}

	if rep.LegacyDebtCount < 5 {
		t.Fatalf("expected at least 5 legacy infractions across C, Python, and Rust, got: %d", rep.LegacyDebtCount)
	}
	if rep.DebtBreakdown["HISS-02"] == 0 {
		t.Errorf("expected HISS-02 infractions, got none")
	}
	if rep.DebtBreakdown["HISS-07"] == 0 {
		t.Errorf("expected HISS-07 infractions, got none")
	}
	if rep.DebtBreakdown["HISS-09"] == 0 {
		t.Errorf("expected HISS-09 infractions, got none")
	}
}

func TestAdopt_ExistingAgentsMDMerged(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	initTestGit(t, tmpDir)

	customInstructions := "# Custom Project Guidelines\n- Rule 1: Always check tests\n- Rule 2: Keep commits clean\n"
	_ = os.WriteFile(filepath.Join(tmpDir, "AGENTS.md"), []byte(customInstructions), 0644)

	opts := AdoptOptions{
		Path:    tmpDir,
		Profile: "framework",
		DryRun:  false,
	}

	_, err := Adopt(ctx, opts)
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}

	merged, err := os.ReadFile(filepath.Join(tmpDir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	content := string(merged)

	// Verify both Praetor harness and custom instructions exist
	if !strings.Contains(content, "Agent Operating Harness") {
		t.Errorf("expected AGENTS.md to contain Agent Operating Harness")
	}
	if !strings.Contains(content, "## Core Directives & Invariants") {
		t.Errorf("expected AGENTS.md to contain Core Directives & Invariants")
	}
	if !strings.Contains(content, "# Custom Project Guidelines") {
		t.Errorf("expected AGENTS.md to preserve original custom instructions")
	}
}

func TestAdopt_ExistingMakefileAppended(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	initTestGit(t, tmpDir)

	existingMakefile := "all:\n\t@echo \"Building...\"\n"
	_ = os.WriteFile(filepath.Join(tmpDir, "Makefile"), []byte(existingMakefile), 0644)

	opts := AdoptOptions{
		Path:    tmpDir,
		Profile: "framework",
		DryRun:  false,
	}

	_, err := Adopt(ctx, opts)
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "verify-all:") {
		t.Errorf("expected Makefile to contain appended verify-all target")
	}
	if !strings.Contains(content, "compile-context:") {
		t.Errorf("expected Makefile to contain appended compile-context target")
	}
	if !strings.Contains(content, "all:\n\t@echo \"Building...\"") {
		t.Errorf("expected Makefile to preserve existing all target")
	}
}

func TestAdopt_NASARule4_FunctionLengthLimit(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	initTestGit(t, tmpDir)

	// Write C file with a function longer than 60 lines
	var cCode strings.Builder
	cCode.WriteString("void long_c_function() {\n")
	for i := 0; i < 70; i++ {
		cCode.WriteString("    int x = 1;\n")
	}
	cCode.WriteString("}\n")
	_ = os.WriteFile(filepath.Join(tmpDir, "long.c"), []byte(cCode.String()), 0644)

	// Write Python file with a function longer than 60 lines
	var pyCode strings.Builder
	pyCode.WriteString("def long_python_function():\n")
	for i := 0; i < 70; i++ {
		pyCode.WriteString("    x = 1\n")
	}
	_ = os.WriteFile(filepath.Join(tmpDir, "long.py"), []byte(pyCode.String()), 0644)

	opts := AdoptOptions{
		Path:           tmpDir,
		Profile:        "framework",
		RecordBaseline: true,
		DryRun:         true,
	}

	rep, err := Adopt(ctx, opts)
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}

	if rep.DebtBreakdown["HISS-04"] < 2 {
		t.Errorf("expected at least 2 HISS-04 infractions for functions > 60 LOC, got: %d", rep.DebtBreakdown["HISS-04"])
	}
}

func TestAdopt_GovernanceTextsScaffolded(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	initTestGit(t, tmpDir)

	readmeContent := "# My Awesome Project\nSome description here.\n"
	_ = os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte(readmeContent), 0644)

	opts := AdoptOptions{
		Path:    tmpDir,
		Profile: "framework",
		DryRun:  false,
	}

	_, err := Adopt(ctx, opts)
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}

	// Verify CONTRIBUTING.md created
	if !fileExists(filepath.Join(tmpDir, "CONTRIBUTING.md")) {
		t.Errorf("expected CONTRIBUTING.md to be created")
	}

	// Verify PR template created
	if !fileExists(filepath.Join(tmpDir, ".github", "pull_request_template.md")) {
		t.Errorf("expected .github/pull_request_template.md to be created")
	}

	// Verify SECURITY.md created
	if !fileExists(filepath.Join(tmpDir, "SECURITY.md")) {
		t.Errorf("expected SECURITY.md to be created")
	}

	// Verify ADR directory and template created
	if !fileExists(filepath.Join(tmpDir, "docs", "adr", "README.md")) {
		t.Errorf("expected docs/adr/README.md to be created")
	}
	if !fileExists(filepath.Join(tmpDir, "docs", "adr", "0000-template.md")) {
		t.Errorf("expected docs/adr/0000-template.md to be created")
	}

	// Verify README.md patched with badge and table
	readmeData, err := os.ReadFile(filepath.Join(tmpDir, "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	content := string(readmeData)
	if !strings.Contains(content, "HISS--16%20Compliant") {
		t.Errorf("expected README.md to contain HISS-16 badge")
	}
	if !strings.Contains(content, "## Standards & Governance") {
		t.Errorf("expected README.md to contain Standards & Governance section")
	}
	if !strings.Contains(content, "# My Awesome Project") {
		t.Errorf("expected README.md to preserve original content")
	}
}

func TestAdopt_AgentsMD_ForcePreservesCustomInstructions(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	initTestGit(t, tmpDir)

	initialAgents := "<!-- markdownlint-disable -->\n# old Agent Operating Harness\n\n## Core Directives & Invariants\nold table\n\n---\n\n# Custom Repo Instructions\nDon't touch this proprietary text!\n"
	_ = os.WriteFile(filepath.Join(tmpDir, "AGENTS.md"), []byte(initialAgents), 0644)

	opts := AdoptOptions{
		Path:    tmpDir,
		Profile: "framework",
		Force:   true,
		DryRun:  false,
	}

	_, err := Adopt(ctx, opts)
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	content := string(data)

	// Verify new harness was generated (contains modernized NASA JPL rules)
	if !strings.Contains(content, "Modernized NASA JPL Power-of-10") {
		t.Errorf("expected updated harness with NASA JPL Power-of-10")
	}

	// Verify custom repo instructions were preserved
	if !strings.Contains(content, "# Custom Repo Instructions") || !strings.Contains(content, "Don't touch this proprietary text!") {
		t.Errorf("expected custom repo instructions to be preserved, got:\n%s", content)
	}
}
