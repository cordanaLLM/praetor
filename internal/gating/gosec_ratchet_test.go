package gating

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
	apiassets "github.com/cordanaLLM/praetor/tools/apicompat"
)

// setupGosecPlantedRepo creates a hermetic git repo with a Go module and one planted G204 finding.
func setupGosecPlantedRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ctx, err := util.WithCommandEnvironment(t.Context(), testsupport.HermeticGitEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	if out, err := util.RunGit(ctx, dir, "init"); err != nil {
		t.Fatalf("git init: %v (%s)", err, out)
	}
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/planted\n\ngo 1.22\n")
	writeFile(t, filepath.Join(dir, GosecConfigFile), "{\n  \"global\": {\n    \"exclude\": \"\"\n  }\n}\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

import "os/exec"

func runCommand(cmd string) error {
	return exec.Command("sh", "-c", cmd).Run()
}

func main() {
	_ = runCommand("echo test")
}
`)
	if out, err := util.RunGit(ctx, dir, "add", "."); err != nil {
		t.Fatalf("git add: %v (%s)", err, out)
	}
	if out, err := util.RunGit(ctx, dir, "commit", "-m", "initial"); err != nil {
		t.Fatalf("git commit: %v (%s)", err, out)
	}
	return dir
}

func TestGosecRatchet_PlantedFinding_FailsOnMain(t *testing.T) {
	if _, err := exec.LookPath("gosec"); err != nil {
		t.Skip("gosec not installed")
	}
	dir := setupGosecPlantedRepo(t)
	rep := &PipelineReport{Stages: make([]StageResult, 0, maxStages)}
	cfg := &stageConfig{
		repoDir:         dir,
		dryRun:          false,
		run:             util.RunCommand,
		lookPath:        exec.LookPath,
		rep:             rep,
		moduleDiscovery: apiassets.TrackedModuleFiles,
	}

	_, err := runSecurityStage(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected runSecurityStage to fail on planted gosec finding")
	}
	t.Logf("Observed red-first failure on main 32a35c8f:\n%v", err)
	if !strings.Contains(err.Error(), "G204") {
		t.Fatalf("expected error to name G204, got: %v", err)
	}
}

func TestGosecRatchet_PassesAfterRecordingBaseline(t *testing.T) {
	if _, err := exec.LookPath("gosec"); err != nil {
		t.Skip("gosec not installed")
	}
	dir := setupGosecPlantedRepo(t)
	findings, hasGo, err := ScanGosec(t.Context(), dir)
	if err != nil || !hasGo || len(findings) == 0 {
		t.Fatalf("ScanGosec failed: %v (hasGo: %v, count: %d)", err, hasGo, len(findings))
	}
	base, err := baseline.Record(&baseline.Baseline{Absent: true}, findings, baseline.RecordOptions{
		AllowIncrease: true,
		Rationale:     "planted initial debt",
	})
	if err != nil {
		t.Fatalf("baseline.Record: %v", err)
	}
	if err := baseline.SaveBaseline(filepath.Join(dir, GosecBaselineFile), base); err != nil {
		t.Fatalf("SaveBaseline: %v", err)
	}

	rep := &PipelineReport{Stages: make([]StageResult, 0, maxStages)}
	cfg := &stageConfig{
		repoDir:         dir,
		dryRun:          false,
		run:             util.RunCommand,
		lookPath:        exec.LookPath,
		rep:             rep,
		moduleDiscovery: apiassets.TrackedModuleFiles,
	}

	msg, err := runSecurityStage(context.Background(), cfg)
	if err != nil {
		t.Fatalf("expected runSecurityStage to pass after recording baseline, got: %v", err)
	}
	if !strings.Contains(msg, "1 gosec infractions within 1 baselined limit") {
		t.Fatalf("unexpected stage message: %s", msg)
	}
}

func TestGosecRatchet_GrowthFailsWithNewOneNamed(t *testing.T) {
	if _, err := exec.LookPath("gosec"); err != nil {
		t.Skip("gosec not installed")
	}
	dir := setupGosecPlantedRepo(t)
	findings, _, err := ScanGosec(t.Context(), dir)
	if err != nil || len(findings) == 0 {
		t.Fatalf("initial scan failed: %v", err)
	}
	base, err := baseline.Record(&baseline.Baseline{Absent: true}, findings, baseline.RecordOptions{
		AllowIncrease: true,
		Rationale:     "planted initial debt",
	})
	if err != nil {
		t.Fatalf("baseline.Record: %v", err)
	}
	if err := baseline.SaveBaseline(filepath.Join(dir, GosecBaselineFile), base); err != nil {
		t.Fatalf("SaveBaseline: %v", err)
	}

	writeFile(t, filepath.Join(dir, "extra.go"), `package main

import "os/exec"

func runExtra(arg string) error {
	return exec.Command("bash", "-c", arg).Run()
}
`)

	rep := &PipelineReport{Stages: make([]StageResult, 0, maxStages)}
	cfg := &stageConfig{
		repoDir:         dir,
		dryRun:          false,
		run:             util.RunCommand,
		lookPath:        exec.LookPath,
		rep:             rep,
		moduleDiscovery: apiassets.TrackedModuleFiles,
	}

	_, err = runSecurityStage(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected growth to fail security stage")
	}
	if !strings.Contains(err.Error(), "extra.go") || !strings.Contains(err.Error(), "G204") {
		t.Fatalf("expected error to name the new finding in extra.go, got: %v", err)
	}
}

func TestGosecRatchet_RemovalLeavesStageGreenAndRecordShrinksBaseline(t *testing.T) {
	if _, err := exec.LookPath("gosec"); err != nil {
		t.Skip("gosec not installed")
	}
	dir := setupGosecPlantedRepo(t)
	findings, _, err := ScanGosec(t.Context(), dir)
	if err != nil || len(findings) == 0 {
		t.Fatalf("initial scan failed: %v", err)
	}
	base, err := baseline.Record(&baseline.Baseline{Absent: true}, findings, baseline.RecordOptions{
		AllowIncrease: true,
		Rationale:     "planted initial debt",
	})
	if err != nil {
		t.Fatalf("baseline.Record: %v", err)
	}
	if err := baseline.SaveBaseline(filepath.Join(dir, GosecBaselineFile), base); err != nil {
		t.Fatalf("SaveBaseline: %v", err)
	}

	writeFile(t, filepath.Join(dir, "main.go"), `package main

func main() {}
`)

	rep := &PipelineReport{Stages: make([]StageResult, 0, maxStages)}
	cfg := &stageConfig{
		repoDir:         dir,
		dryRun:          false,
		run:             util.RunCommand,
		lookPath:        exec.LookPath,
		rep:             rep,
		moduleDiscovery: apiassets.TrackedModuleFiles,
	}

	if _, err := runSecurityStage(context.Background(), cfg); err != nil {
		t.Fatalf("expected stage to be green after removing infraction, got: %v", err)
	}

	newFindings, _, err := ScanGosec(t.Context(), dir)
	if err != nil {
		t.Fatalf("rescan failed: %v", err)
	}
	shrunk, err := baseline.Record(base, newFindings, baseline.RecordOptions{})
	if err != nil {
		t.Fatalf("baseline shrink record failed: %v", err)
	}
	if shrunk.TotalInfractions != 0 {
		t.Fatalf("expected baseline to shrink to 0 infractions, got: %d", shrunk.TotalInfractions)
	}
}

func TestGosecRatchet_NoBlanketExclude(t *testing.T) {
	tmplPath := filepath.Join("..", "..", "templates", "go", ".gosec.json.tmpl")
	content, err := os.ReadFile(tmplPath)
	if err != nil {
		t.Fatalf("read template %s: %v", tmplPath, err)
	}
	if !strings.Contains(string(content), `"exclude": ""`) {
		t.Fatalf("expected empty exclude in template:\n%s", string(content))
	}
}

func TestGosecOutput_ZeroIssuesYieldsZeroInfractions(t *testing.T) {
	dir := t.TempDir()
	zeroReport := `{
		"Golang errors": {},
		"Issues": [],
		"Stats": {"files": 1, "lines": 10, "nosec": 0, "found": 0}
	}`
	infractions, err := parseGosecOutput(dir, dir, zeroReport)
	if err != nil {
		t.Fatalf("expected nil error for zero issues, got: %v", err)
	}
	if len(infractions) != 0 {
		t.Fatalf("expected 0 infractions, got: %d", len(infractions))
	}
}

func TestGosecOutput_MalformedReport_FailsStageWithError(t *testing.T) {
	dir := t.TempDir()
	malformed := `[main.go:34] - G204: Audit use of command execution`
	_, parseErr := parseGosecOutput(dir, dir, malformed)
	if parseErr == nil {
		t.Fatal("expected parseGosecOutput to fail on non-JSON text output")
	}
	if !strings.Contains(parseErr.Error(), stageSecurity) {
		t.Fatalf("expected error to name stage %q, got: %v", stageSecurity, parseErr)
	}

	dir = setupGosecPlantedRepo(t)
	rep := &PipelineReport{Stages: make([]StageResult, 0, maxStages)}
	cfg := &stageConfig{
		repoDir:         dir,
		dryRun:          false,
		lookPath:        exec.LookPath,
		rep:             rep,
		moduleDiscovery: apiassets.TrackedModuleFiles,
		run: func(ctx context.Context, cmdDir, name string, args ...string) (string, error) {
			if name == "gosec" {
				return malformed, exec.ErrNotFound
			}
			return util.RunCommand(ctx, cmdDir, name, args...)
		},
	}
	_, err := runSecurityStage(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected runSecurityStage to fail on malformed gosec report")
	}
	if !strings.Contains(err.Error(), stageSecurity) {
		t.Fatalf("expected error to name stage %q, got: %v", stageSecurity, err)
	}
}

func TestGosecOutput_LineRange_AnchorsAtFirstNumber(t *testing.T) {
	dir := t.TempDir()
	src := `package main

type Service struct{}

func (s *Service) Run() {
	println("hello")
}
`
	writeFile(t, filepath.Join(dir, "main.go"), src)
	report := `{
		"Issues": [
			{
				"rule_id": "G204",
				"file": "main.go",
				"line": "5-7",
				"details": "Audit use of command execution"
			}
		]
	}`
	infractions, err := parseGosecOutput(dir, dir, report)
	if err != nil {
		t.Fatalf("parseGosecOutput failed: %v", err)
	}
	if len(infractions) != 1 {
		t.Fatalf("expected 1 infraction, got %d", len(infractions))
	}
	if infractions[0].LineNumber != 5 {
		t.Errorf("line number = %d, want 5", infractions[0].LineNumber)
	}
	if infractions[0].Anchor != "fn:Service.Run" {
		t.Errorf("anchor = %q, want %q", infractions[0].Anchor, "fn:Service.Run")
	}
}

func TestGosecOutput_InvalidLine_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n")
	for _, badLine := range []string{"abc", "10-5", "-5", "5-", "0", "1-2-3"} {
		report := `{
			"Issues": [
				{
					"rule_id": "G204",
					"file": "main.go",
					"line": "` + badLine + `",
					"details": "Audit use of command execution"
				}
			]
		}`
		if _, err := parseGosecOutput(dir, dir, report); err == nil {
			t.Errorf("expected error for line %q, got nil", badLine)
		}
	}
}

// Negative: a finding in a file that cannot be read fails the scan instead of anchoring
// against an empty source. Positive: the same report passes once the file exists.
func TestGosecOutput_UnreadableReportedFile_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	report := `{"Issues": [{"rule_id": "G204", "file": "gone.go", "line": "3", "details": "Audit use of command execution"}]}`
	_, err := parseGosecOutput(dir, dir, report)
	if err == nil || !strings.Contains(err.Error(), "gone.go") {
		t.Fatalf("expected an error naming gone.go, got: %v", err)
	}

	writeFile(t, filepath.Join(dir, "gone.go"), "package main\n\nfunc run() {}\n")
	infractions, err := parseGosecOutput(dir, dir, report)
	if err != nil || len(infractions) != 1 || infractions[0].Anchor != "fn:run" {
		t.Fatalf("expected one infraction anchored at fn:run, got %+v, %v", infractions, err)
	}
}
