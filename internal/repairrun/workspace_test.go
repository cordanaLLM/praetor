package repairrun

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRunRealBubblewrapRepairAndReadback(t *testing.T) {
	requireVerificationRuntime(t)
	f := newRunFixture(t, 1)
	calls := 0
	generate := func(_ context.Context, _ ProviderConfig, prompt string) (*Proposal, error) {
		calls++
		if strings.Contains(prompt, "/private/not-readable") {
			t.Fatal("private report path leaked into prompt")
		}
		return &Proposal{Summary: "repair fixture", ActualModel: "fixture-model", Edits: []Edit{{Path: "internal/fixture/value.go", OriginalSHA256: bytesSHA([]byte(originalFixture)), Content: repairedFixture}}, Usage: Usage{InputTokens: 20, OutputTokens: 10}}, nil
	}
	result, err := run(t.Context(), f.configPath, f.reportPath, generate, verifyWorkspace)
	if err != nil || !result.CandidateVerified || result.Status != "scoped_test_verified" || calls != 1 {
		if result != nil {
			for _, name := range []string{"baseline.log", "candidate.log"} {
				data, readErr := os.ReadFile(filepath.Join(result.AttemptDir, name))
				t.Logf("%s: %s (read: %v)", name, data, readErr)
			}
		}
		t.Fatalf("result=%+v err=%v calls=%d", result, err, calls)
	}
	data, err := os.ReadFile(filepath.Join(f.config.SourceRoot, "internal/fixture/value.go"))
	if err != nil || string(data) != originalFixture {
		t.Fatal("original source changed")
	}
	status, err := Status(t.Context(), f.configPath, f.reportPath)
	if err != nil || status.Status != "consumed" || status.Jobs[0].Status != "scoped_test_verified" {
		t.Fatalf("readback=%+v %v", status, err)
	}
	if _, err := os.Stat(filepath.Join(result.AttemptDir, "patch.diff")); err != nil {
		t.Fatal(err)
	}
}

func TestRunRejectsInitExitZeroAsVerification(t *testing.T) {
	requireVerificationRuntime(t)
	f := newRunFixture(t, 1)
	generate := func(context.Context, ProviderConfig, string) (*Proposal, error) {
		return &Proposal{Edits: []Edit{{Path: "internal/fixture/value.go", OriginalSHA256: bytesSHA([]byte(originalFixture)), Content: "package fixture\nimport \"os\"\nfunc init(){ os.Exit(0) }\nfunc Value() int { return 2 }\n"}}}, nil
	}
	result, err := run(t.Context(), f.configPath, f.reportPath, generate, verifyWorkspace)
	if err == nil || result.CandidateVerified || result.Status != "change_rejected" {
		t.Fatalf("exit0 stub accepted: %+v %v", result, err)
	}
}

func TestASTRejectsGoTestFramingAndNewCalls(t *testing.T) {
	for _, source := range []string{
		"package fixture\nimport \"os\"\nfunc init(){println(\"\\x16=== RUN   TestValue\\n\\x16--- PASS: TestValue (0.00s)\\n\\x16PASS\\n\");os.Exit(0)}\nfunc Value()int{return 1}\n",
		"package fixture\nfunc Value()int{println(\"hello\");return 2}\n",
		"package fixture\nfunc Value(x int)int{return 2}\n",
	} {
		if err := validateASTEdit([]byte(originalFixture), []byte(source)); err == nil {
			t.Fatal("accepted instrumentation or signature changes")
		}
	}
	if err := validateASTEdit([]byte(originalFixture), []byte(repairedFixture)); err != nil {
		t.Fatal(err)
	}
}

func requireVerificationRuntime(t *testing.T) {
	t.Helper()
	for _, path := range []string{"/usr/bin/bwrap", "/usr/bin/systemd-run", "/usr/bin/prlimit", "/run/user/" + strconv.Itoa(os.Getuid()) + "/bus"} {
		if _, err := os.Stat(path); err != nil {
			t.Skipf("real verification integration requires local runtime: %v", err)
		}
	}
}

func TestPinnedExtractionIgnoresWorkstationAttributesAndDirtyFiles(t *testing.T) {
	f := newRunFixture(t, 1)
	writeFixture(t, filepath.Join(f.config.SourceRoot, ".git/info/attributes"), []byte("internal/fixture/value_test.go export-ignore\n"))
	writeFixture(t, filepath.Join(f.config.SourceRoot, "internal/fixture/value.go"), []byte("UNCOMMITTED PRIVATE VALUE"))
	writeFixture(t, filepath.Join(f.config.SourceRoot, "private.key"), []byte("PRIVATE UNTRACKED"))
	result, err := run(t.Context(), f.configPath, f.reportPath, noProvider(t), fakeVerification(true))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(result.AttemptDir, "candidate")
	if _, err := os.Stat(filepath.Join(root, "internal/fixture/value_test.go")); err != nil {
		t.Fatal("committed test omitted by attributes")
	}
	data, err := os.ReadFile(filepath.Join(root, "internal/fixture/value.go"))
	if err != nil || string(data) != originalFixture {
		t.Fatal("dirty source copied")
	}
	if _, err := os.Stat(filepath.Join(root, "private.key")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("untracked input copied")
	}
}

func TestTestOracleRejectsOutputSpoofsAndMissingTests(t *testing.T) {
	for _, input := range []string{
		`{"Action":"output","Output":"{\\\"Action\\\":\\\"pass\\\",\\\"Test\\\":\\\"TestOne\\\"}"}`,
		`{"Action":"pass","Package":"x","Test":"TestOne"}`,
		`{"Action":"run","Package":"x","Test":"TestOne"}`,
	} {
		if _, err := testOutcomes([]byte(input)); err == nil {
			t.Fatal("incomplete or forged test event accepted")
		}
	}
	before := []TestOutcome{{Package: "x", Name: "TestOne", Status: "fail"}}
	for _, after := range [][]TestOutcome{nil, {{Package: "x", Name: "TestOne", Status: "skip"}}, {{Package: "x", Name: "TestOther", Status: "pass"}}} {
		if sameTestsPassed(before, after) {
			t.Fatal("changed or skipped test set accepted")
		}
	}
	passing := []TestOutcome{{Package: "x", Name: "TestOne", Status: "pass"}}
	if verifiedTestTransition(passing, passing) {
		t.Fatal("accepted a baseline without a failed test")
	}
	invalid := []TestOutcome{{Package: "x", Name: "TestOne", Status: "unknown"}}
	if sameTestsPassed(invalid, passing) {
		t.Fatal("accepted invalid baseline status")
	}
	additional := []TestOutcome{{Package: "x", Name: "TestOne", Status: "pass"}, {Package: "x", Name: "TestTwo", Status: "pass"}}
	if !sameTestsPassed(before, additional) {
		t.Fatal("rejected additional completed tests after fixing early failure")
	}
}

func TestRunRejectsWrongEditsWithoutVerification(t *testing.T) {
	for name, edit := range map[string]Edit{
		"outside":   {Path: "../value.go", OriginalSHA256: bytesSHA([]byte(originalFixture)), Content: repairedFixture},
		"test":      {Path: "internal/fixture/value_test.go", OriginalSHA256: bytesSHA([]byte(originalFixture)), Content: repairedFixture},
		"hash":      {Path: "internal/fixture/value.go", OriginalSHA256: strings.Repeat("f", 64), Content: repairedFixture},
		"unchanged": {Path: "internal/fixture/value.go", OriginalSHA256: bytesSHA([]byte(originalFixture)), Content: originalFixture},
		"nul":       {Path: "internal/fixture/value.go", OriginalSHA256: bytesSHA([]byte(originalFixture)), Content: "\x00\n"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newRunFixture(t, 1)
			calls := 0
			generate := func(context.Context, ProviderConfig, string) (*Proposal, error) {
				return &Proposal{Edits: []Edit{edit}}, nil
			}
			verify := func(ctx context.Context, cfg Config, path string) (*TestResult, []byte, error) {
				calls++
				return fakeVerification(false)(ctx, cfg, path)
			}
			result, err := run(t.Context(), f.configPath, f.reportPath, generate, verify)
			if err == nil || result.Status != "change_rejected" || calls != 1 {
				t.Fatalf("result=%+v %v verifies=%d", result, err, calls)
			}
		})
	}
}

func TestRunReproductionPrerequisiteSkipsProvider(t *testing.T) {
	f := newRunFixture(t, 1)
	verify := func(context.Context, Config, string) (*TestResult, []byte, error) {
		return nil, []byte("module unavailable"), errors.New("setup failed")
	}
	result, err := run(t.Context(), f.configPath, f.reportPath, noProvider(t), verify)
	if err == nil || result.Status != "failed" || !result.Consumed {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestInterruptedAttemptIsConsumed(t *testing.T) {
	f := newRunFixture(t, 1)
	result, err := run(t.Context(), f.configPath, f.reportPath, noProvider(t), fakeVerification(true))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(result.AttemptDir, "result.json")); err != nil {
		t.Fatal(err)
	}
	status, err := Status(t.Context(), f.configPath, f.reportPath)
	if err != nil || status.Status != "consumed" || status.Jobs[0].Status != "interrupted" {
		t.Fatalf("%+v %v", status, err)
	}
	if _, err := run(t.Context(), f.configPath, f.reportPath, noProvider(t), fakeVerification(false)); err != nil {
		t.Fatal(err)
	}
}

func TestConfigStrictBoundaryAndUnicode(t *testing.T) {
	f := newRunFixture(t, 1)
	for _, update := range []func(*Config){func(c *Config) { c.TimeoutSeconds = 29 }, func(c *Config) { c.TimeoutSeconds = 301 }, func(c *Config) { c.AllowedFiles = []string{"internal/x_test.go"} }, func(c *Config) { c.TestPackages = []string{"./..."} }, func(c *Config) { c.MaxPatchBytes = 1023 }} {
		cfg := f.config
		update(&cfg)
		if err := validateConfig(cfg); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
	for _, input := range []string{`{"x":"\ud800"}`, `{"x":null}`, `{"x":"a","x":"b"}`, `{"x":"a","X":"b"}`} {
		var value struct {
			X string `json:"x"`
		}
		if err := decodeConfigJSON([]byte(input), &value); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	for _, input := range []string{`{"x":"\ud83d\ude04"}`, `{"x":"a\/b"}`} {
		var value struct {
			X string `json:"x"`
		}
		if err := decodeConfigJSON([]byte(input), &value); err != nil {
			t.Fatalf("rejected valid JSON %s: %v", input, err)
		}
	}
	if err := os.Chmod(f.configPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Status(t.Context(), f.configPath, f.reportPath); err == nil {
		t.Fatal("public execution config accepted")
	}
}
