package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/builder"
)

func TestBuildCLIRejectsUnexecutedBackend(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "absent-output")
	config := filepath.Join(dir, ".framework-build.yaml")
	manifest := fmt.Sprintf("version: 1\nproject: fixture\noutput_dir: %q\noptimize: true\ntargets:\n  app:\n    runtime: go\n    entrypoint: missing.go\n", output)
	if err := os.WriteFile(config, []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	text, err := captureStdout(t, func() error {
		return dispatchCommand("build", []string{"--config", config})
	})
	if !errors.Is(err, builder.ErrBackendUnavailable) {
		t.Fatalf("CLI lost backend rejection: %q, %v", text, err)
	}
	for _, fabricated := range []string{"[PASS]", "Successfully compiled", "Artifacts:", "Built static"} {
		if strings.Contains(text, fabricated) {
			t.Fatalf("CLI reported unexecuted work: %q", text)
		}
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("CLI created output directory: %v", err)
	}
}

func TestBuildCLIRejectsMissingConfiguration(t *testing.T) {
	text, err := captureStdout(t, func() error {
		return dispatchCommand("build", []string{"--config", filepath.Join(t.TempDir(), "missing.yaml")})
	})
	if !errors.Is(err, os.ErrNotExist) || text != "" {
		t.Fatalf("missing config must fail before build output: %q, %v", text, err)
	}
}

func TestBuildCLIRejectsUnsupportedRuntime(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, "build.yaml", "targets:\n  app:\n    runtime: unknown\n")
	text, err := captureStdout(t, func() error {
		return dispatchCommand("build", []string{"--config", filepath.Join(dir, "build.yaml")})
	})
	if !errors.Is(err, builder.ErrUnsupportedRuntime) || strings.Contains(text, "[PASS]") {
		t.Fatalf("unknown runtime falsely accepted: %q, %v", text, err)
	}
}

// TestReportBuildResults_Positive is the positive dimension: every result is labelled by its
// own status, its refusal reason is shown, and only targets the builder marked successful
// count towards the compiled total.
func TestReportBuildResults_Positive(t *testing.T) {
	results := []builder.BuildResult{
		{Target: "api", Runtime: "go", Status: builder.BuildUnavailable, Reason: "backend missing"},
		{Target: "exotic", Runtime: "cobol", Status: builder.BuildUnsupported, Reason: "unknown runtime"},
		{Target: "docs", Runtime: "mkdocs", Success: true, Artifacts: []string{"site.tar"}, OutputLogs: "docs.log"},
	}

	out, err := captureStdout(t, func() error {
		if got := reportBuildResults(results); got != 1 {
			t.Errorf("expected exactly one compiled target, got %d", got)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("capture stdout: %v", err)
	}

	for _, want := range []string{
		"[UNAVAILABLE] Target: api", "Reason: backend missing",
		"[UNSUPPORTED] Target: exotic", "Reason: unknown runtime",
		"[PASS] Target: docs", "Artifacts: [site.tar]", "Logs: docs.log",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report is missing %q:\n%s", want, out)
		}
	}
}

// TestReportBuildResults_NegativeUnknownStatusIsNotASuccess is the negative dimension: a
// result carrying neither success nor a known refusal class must report as a failure. The
// label must never default to the success marker for a target that compiled nothing.
func TestReportBuildResults_NegativeUnknownStatusIsNotASuccess(t *testing.T) {
	results := []builder.BuildResult{{Target: "ghost", Runtime: "go"}}

	out, err := captureStdout(t, func() error {
		if got := reportBuildResults(results); got != 0 {
			t.Errorf("an unsuccessful target must not count as compiled, got %d", got)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("capture stdout: %v", err)
	}
	if !strings.Contains(out, "[FAIL] Target: ghost") {
		t.Errorf("expected a failure label, got:\n%s", out)
	}
	if strings.Contains(out, "[PASS]") {
		t.Errorf("a target that compiled nothing was labelled as passing:\n%s", out)
	}
}

// TestReportBuildResults_BoundaryEmpty is the boundary dimension: no selected targets yields
// no report lines and no compiled targets, rather than an empty success claim.
func TestReportBuildResults_BoundaryEmpty(t *testing.T) {
	out, err := captureStdout(t, func() error {
		if got := reportBuildResults(nil); got != 0 {
			t.Errorf("expected no compiled targets, got %d", got)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("capture stdout: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected no output for an empty result set, got:\n%s", out)
	}
}

// TestBuildStatusLabel_Boundary is the boundary dimension for the label mapping: a result
// that is both successful and carries a refusal status reports as successful, because the
// builder only sets a refusal status on targets it declined to run.
func TestBuildStatusLabel_Boundary(t *testing.T) {
	label := buildStatusLabel(builder.BuildResult{Success: true, Status: builder.BuildUnavailable})
	if label != "[PASS]" {
		t.Errorf("success must win over a refusal status, got %q", label)
	}
	if got := buildStatusLabel(builder.BuildResult{Duration: time.Second}); got != "[FAIL]" {
		t.Errorf("an unclassified result must report as a failure, got %q", got)
	}
}

// TestDispatchCommand_BuildReportsRefusalsBeforeFailing covers the regression this reporting
// exists for: the command used to return the joined error without printing anything, so an
// operator could not tell which runtime was unsupported and which merely lacked a backend.
func TestDispatchCommand_BuildReportsRefusalsBeforeFailing(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, ".framework-build.yaml")
	cfgContent := "version: 1\nproject: report-test\noutput_dir: " + filepath.Join(tmpDir, "dist") + "\n" +
		"targets:\n  api:\n    runtime: go\n  exotic:\n    runtime: cobol\n"
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o600); err != nil {
		t.Fatalf("write build config: %v", err)
	}

	out, err := captureStdout(t, func() error {
		return dispatchCommand("build", []string{"--config=" + cfgPath})
	})
	if !errors.Is(err, builder.ErrBackendUnavailable) {
		t.Fatalf("expected the unavailable-backend error, got %v", err)
	}
	if !strings.Contains(out, "[UNAVAILABLE] Target: api") {
		t.Errorf("an unimplemented backend was not reported: \n%s", out)
	}
	if !strings.Contains(out, "[UNSUPPORTED] Target: exotic") {
		t.Errorf("an unrecognized runtime was not reported:\n%s", out)
	}
	if strings.Contains(out, "Compiled") {
		t.Errorf("a refused build must not print a compilation summary:\n%s", out)
	}
}
