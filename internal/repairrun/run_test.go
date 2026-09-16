package repairrun

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
		if output, err := runGit(t.Context(), "", maxLogBytes, args...); err != nil {
			t.Fatalf("fixture git: %s %v", output, err)
		}
	}
	sha, err := runGit(t.Context(), source, 128, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	routing := filepath.Join(root, "routing.yaml")
	writeFixture(t, routing, []byte("version: 1\ntiers:\n  local:\n    target_tasks: [ci_debugging]\n    models:\n      - id: fixture-model\n        family: openai\n        cost_per_m_in: 0\n        cost_per_m_out: 0\ngovernance:\n  exhaustion_threshold_percent: 80\n"))
	f.config = Config{Version: 1, SourceRoot: source, SourceSHA: strings.TrimSpace(string(sha)), StateDir: filepath.Join(root, "state"), AllowedFiles: []string{"internal/fixture/value.go"}, TestPackages: []string{"./internal/fixture"}, TimeoutSeconds: 300, MaxPatchBytes: 4096, RepairPolicy: dogfood.RepairPolicy{RoutingConfig: routing, Task: "ci_debugging", InputTokens: 1000, OutputTokens: 1000, MaxCost: 1}, Provider: ProviderConfig{BaseURL: "https://provider.example/v1", TokenCommand: filepath.Join(root, "token"), TokenCommandSHA256: strings.Repeat("a", 64), Model: "fixture-model", MaxOutputTokens: 1024, MaxInputBytes: 8192}}
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
	requireRepairIsolation(t)
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
	requireRepairIsolation(t)
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
	requireRepairIsolation(t)
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

// requireRepairIsolation skips a case where repair execution cannot run, naming why.
//
// Repair execution confines itself with Linux file isolation and locking, and the
// non-Linux build says so: openRegular returns "repair execution requires Linux file
// isolation". These cases did not ask. On Windows run() returned (nil, err), and
// TestRunFailClosedAndProviderExactlyOnce then read result.Status off the nil result and
// panicked, aborting every later case in the package. They were previously masked,
// failing earlier on a git environment that could not find git; fixing that let them
// reach run() and exposed the crash. The printed reason is the production code's own.
//
// openRegular is only called off Linux, where it returns immediately. On Linux the
// guard returns before calling it, so the nil root it is given is never used.
func requireRepairIsolation(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "linux" {
		return
	}
	_, err := openRegular(nil, "")
	t.Skipf("repair execution unavailable on this platform: %v", err)
}

// requireCredentialHelper skips a case where the repair credential helper cannot run.
//
// The helper checks file ownership and opens without following symlinks, and the non-Unix
// build says so: providerOpenHelper returns "repair credential helper requires Unix
// ownership and nofollow support". Its own tests are build-tagged accordingly; these
// generation cases reach it through the provider and did not ask, so on Windows they
// failed with "credential helper open failed" rather than stating the platform limit.
// Windows is the only non-Unix platform in the Platform Neutrality matrix, and the guard
// returns before calling the helper everywhere else.
func requireCredentialHelper(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" {
		return
	}
	_, err := providerOpenHelper("")
	t.Skipf("repair credential helper unavailable on this platform: %v", err)
}
