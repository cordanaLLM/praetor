package repairrun

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/dogfood"
)

const originalFixture = "package fixture\n\nfunc Value() int { return 1 }\n"
const repairedFixture = "package fixture\n\nfunc Value() int { return 2 }\n"

type runFixture struct {
	root, configPath, reportPath string
	config                       Config
	report                       dogfood.SuiteReport
}

func writeFixture(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func jsonFixture(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, path, data)
}

func newRunFixture(t *testing.T, cases int) *runFixture {
	t.Helper()
	root := t.TempDir()
	f := &runFixture{root: root, configPath: filepath.Join(root, "execution.json"), reportPath: filepath.Join(root, "report.json")}
	source := filepath.Join(root, "source")
	writeFixture(t, filepath.Join(source, "go.mod"), []byte("module fixture\n\ngo 1.27\n"))
	writeFixture(t, filepath.Join(source, "internal/fixture/value.go"), []byte(originalFixture))
	writeFixture(t, filepath.Join(source, "internal/fixture/value_test.go"), []byte("package fixture\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value()!=2 { t.Fatal(\"wrong value\") } }\n"))
	for _, args := range [][]string{{"init", "--template=", source}, {"-C", source, "add", "."}, {"-C", source, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "fixture"}} {
		if output, err := command(t.Context(), "", gitEnvironment(), maxLogBytes, "/usr/bin/git", args...); err != nil {
			t.Fatalf("fixture git: %s %v", output, err)
		}
	}
	sha, err := command(t.Context(), source, gitEnvironment(), 128, "/usr/bin/git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	routing := filepath.Join(root, "routing.yaml")
	writeFixture(t, routing, []byte("version: 1\ntiers:\n  local:\n    target_tasks: [ci_debugging]\n    models:\n      - id: fixture-model\n        family: openai\n        cost_per_m_in: 0\n        cost_per_m_out: 0\ngovernance:\n  exhaustion_threshold_percent: 80\n"))
	f.config = Config{Version: 1, SourceRoot: source, SourceSHA: strings.TrimSpace(string(sha)), StateDir: filepath.Join(root, "state"), AllowedFiles: []string{"internal/fixture/value.go"}, TestPackages: []string{"./internal/fixture"}, TimeoutSeconds: 300, MaxPatchBytes: 4096, RepairPolicy: dogfood.RepairPolicy{RoutingConfig: routing, Task: "ci_debugging", InputTokens: 1000, OutputTokens: 1000, MaxCost: 1}, Provider: ProviderConfig{BaseURL: "https://litellm.ai.cauda.dev/v1", TokenCommand: filepath.Join(root, "token"), TokenCommandSHA256: strings.Repeat("a", 64), Model: "fixture-model", MaxOutputTokens: 1024, MaxInputBytes: 8192}}
	now := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	f.report = dogfood.SuiteReport{Version: 1, Status: "failed", ConfigSHA256: strings.Repeat("b", 64), StartedAt: now, FinishedAt: now.Add(time.Second), Options: dogfood.SuiteOptions{Stage: "verify"}}
	for n := 0; n < cases; n++ {
		id := "case-" + string(rune('a'+n))
		f.report.Cases = append(f.report.Cases, dogfood.SuiteCase{ID: id, Kind: "transcript", Status: "failed", Error: "fixture failure", Transcript: &dogfood.SuiteTranscript{ID: id, SourcePath: "/private/not-readable", SHA256: strings.Repeat("c", 64), Format: "claude-code-jsonl-v1"}})
	}
	f.save(t)
	return f
}

func (f *runFixture) save(t *testing.T) {
	t.Helper()
	jsonFixture(t, f.configPath, f.config)
	jsonFixture(t, f.reportPath, f.report)
}

func fakeVerification(pass bool) verifier {
	return func(_ context.Context, c Config, _ string) (*TestResult, []byte, error) {
		return &TestResult{Passed: pass, Packages: c.TestPackages}, []byte("fixture verification\n"), nil
	}
}

func noProvider(t *testing.T) generator {
	t.Helper()
	return func(context.Context, ProviderConfig, string) (*Proposal, error) {
		t.Error("unexpected provider call")
		return nil, errors.New("unexpected provider call")
	}
}

func TestRunReadOnlyStatusAndStableConsumption(t *testing.T) {
	f := newRunFixture(t, 2)
	status, err := Status(t.Context(), f.configPath, f.reportPath)
	if err != nil || status.Status != "ready" || len(status.Jobs) != 2 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if _, err := os.Stat(f.config.StateDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("status wrote state")
	}
	firstKey := status.ExecutionKey
	result, err := run(t.Context(), f.configPath, f.reportPath, noProvider(t), fakeVerification(true))
	if err != nil || result.Status != "not_reproduced" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	f.report.StartedAt = f.report.StartedAt.Add(time.Hour)
	f.report.FinishedAt = f.report.FinishedAt.Add(time.Hour)
	f.report.Cases[0].Error = "changed per-attempt path /run-002"
	f.save(t)
	status, err = Status(t.Context(), f.configPath, f.reportPath)
	if err != nil || status.Status != "ready" || status.CaseID != "case-b" || status.Jobs[0].ExecutionKey != firstKey {
		t.Fatalf("dedupe=%+v %v", status, err)
	}
	if _, err = run(t.Context(), f.configPath, f.reportPath, noProvider(t), fakeVerification(true)); err != nil {
		t.Fatal(err)
	}
	status, err = Status(t.Context(), f.configPath, f.reportPath)
	if err != nil || status.Status != "consumed" {
		t.Fatalf("consumed=%+v %v", status, err)
	}
}

func TestRunFailClosedAndProviderExactlyOnce(t *testing.T) {
	f := newRunFixture(t, 1)
	calls := 0
	generate := func(context.Context, ProviderConfig, string) (*Proposal, error) {
		calls++
		return nil, errors.New("provider failed")
	}
	result, err := run(t.Context(), f.configPath, f.reportPath, generate, fakeVerification(false))
	if err == nil || result.Status != "agent_failed" || calls != 1 {
		t.Fatalf("%+v %v calls=%d", result, err, calls)
	}
	result, err = run(t.Context(), f.configPath, f.reportPath, generate, fakeVerification(false))
	if err != nil || result.Status != "consumed" || calls != 1 {
		t.Fatalf("repeat=%+v %v calls=%d", result, err, calls)
	}
}

func TestRunRejectsMismatchedRouteAndConfinement(t *testing.T) {
	f := newRunFixture(t, 1)
	f.config.Provider.Model = "other-model"
	f.save(t)
	result, err := run(t.Context(), f.configPath, f.reportPath, noProvider(t), fakeVerification(false))
	if err != nil || result.Status != "blocked" {
		t.Fatalf("%+v %v", result, err)
	}
	if _, err := StatusWithinRoot(t.Context(), f.configPath, f.reportPath, filepath.Join(f.root, "confined")); err == nil {
		t.Fatal("outside config accepted")
	}
	f.config.Provider.Model = "fixture-model"
	f.config.SourceRoot = "/outside/source"
	f.save(t)
	if _, err := StatusWithinRoot(t.Context(), f.configPath, f.reportPath, f.root); err == nil {
		t.Fatal("outside embedded path accepted")
	}
}
