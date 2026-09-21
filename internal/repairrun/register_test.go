package repairrun

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/dogfood"
)

const validInternalRepairSummary = "verdict: candidate\nchanged: internal/fixture/value.go\nran: none\nevidence: proposal.edits\nopen: verification pending"

func TestJobProviderAppliesTheRegisterBudget(t *testing.T) {
	provider := ProviderConfig{MaxOutputTokens: 1024}
	cases := map[string]struct {
		job  *dogfood.RepairJob
		want int
	}{
		"budget below the configured limit": {&dogfood.RepairJob{MaxOutputTokens: 512}, 512},
		"no budget":                         {&dogfood.RepairJob{}, 1024},
		"no job":                            {nil, 1024},
		"budget equal to the limit":         {&dogfood.RepairJob{MaxOutputTokens: 1024}, 1024},
		"budget above the limit never raises spend": {&dogfood.RepairJob{MaxOutputTokens: config.RegisterMaxTokensCeiling}, 1024},
		"smallest valid budget":                     {&dogfood.RepairJob{MaxOutputTokens: config.RegisterMaxTokensFloor}, config.RegisterMaxTokensFloor},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := jobProvider(provider, tc.job).MaxOutputTokens; got != tc.want {
				t.Fatalf("max output tokens = %d, want %d", got, tc.want)
			}
			if provider.MaxOutputTokens != 1024 {
				t.Fatal("jobProvider mutated the configured provider")
			}
		})
	}
}

// The provider limits and the manifest budget share one pair of constants, so a budget
// the manifest accepts is one the provider path accepts.
func TestProviderLimitsMatchTheRegisterBudgetRange(t *testing.T) {
	cfg := ProviderConfig{MaxInputBytes: 1}
	for budget, want := range map[int]bool{
		config.RegisterMaxTokensFloor - 1: false, config.RegisterMaxTokensFloor: true,
		config.RegisterMaxTokensCeiling: true, config.RegisterMaxTokensCeiling + 1: false, 0: false,
	} {
		cfg.MaxOutputTokens = budget
		if got := providerValidLimits(cfg); got != want {
			t.Errorf("providerValidLimits(%d) = %v, want %v", budget, got, want)
		}
	}
}

func TestProviderRequestForwardsTheJobBudget(t *testing.T) {
	// providerGenerate opens the repair credential helper, which needs Unix ownership and
	// nofollow support. Without this the Windows leg failed with "repair credential helper
	// open failed" instead of stating the platform limit (#135). macOS keeps running the
	// case: the guard returns everywhere but Windows.
	requireCredentialHelper(t)
	cfg := providerFixtureConfig(t, "printf '%s\\n' '"+providerFixtureToken+"'")
	cfg.MaxOutputTokens = 1024
	for name, job := range map[string]*dogfood.RepairJob{"budget": {MaxOutputTokens: 512}, "fallback": {}} {
		want := jobProvider(cfg, job).MaxOutputTokens
		response := providerFixtureResponse(t)
		client := providerFixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["max_output_tokens"] != float64(want) {
				t.Errorf("%s: max_output_tokens = %v, want %d", name, body["max_output_tokens"], want)
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(response); err != nil {
				t.Error(err)
			}
		})
		if _, err := providerGenerate(t.Context(), jobProvider(cfg, job), "Public context", client); err != nil {
			t.Fatalf("%s: generation: %v", name, err)
		}
	}
}

func TestRunRecordsRegisterAndTightensProviderBudget(t *testing.T) {
	// This case calls run() and reads result.Status on the error path, which is a nil
	// dereference wherever prepare() fails: run returns (nil, err) then, and Go evaluates
	// the second operand of `err == nil || result.Status != ...` once the first is false.
	// prepare fails on both non-Linux legs -- macOS rejects the symlinked /var component of
	// its own TMPDIR, Windows has no file isolation at all -- so the panic aborted the test
	// binary and masked every later case in the package (#135). This is the same guard the
	// package's other run()-calling cases already take; it was the one omission.
	//
	// The platform guard is not the whole repair. It returns early on Linux, where prepare
	// still refuses a TMPDIR that reaches its directory through a symlink -- openDirectory
	// walks every component of an absolute path and rejects a link -- and there the panic
	// aborted the whole test binary rather than failing one case.
	//
	// Measured on Linux against such a TMPDIR, one figure per column and not two: with -v the
	// package printed 25 outcome lines before this guard and prints 37 after it, so 12 cases
	// never ran; without -v the same pair is 1 line and 10. The earlier note here paired the
	// non-verbose count with the verbose one and reproduced as neither. Every read of a run()
	// result in this package is therefore gated on the result being there, which is what
	// HISS-07 asks of the test's own error path, and a failure prints the error instead of a
	// stack trace.
	requireRepairIsolation(t)
	f := newRunFixture(t, 1)
	f.config.RepairPolicy.Register, f.config.RepairPolicy.MaxOutputTokens = string(config.TextRegisterInternal), 512
	f.save(t)
	seen := 0
	generate := func(_ context.Context, provider ProviderConfig, _ string) (*Proposal, error) {
		seen = provider.MaxOutputTokens
		return nil, errors.New("provider failed")
	}
	result, err := run(t.Context(), f.configPath, f.reportPath, generate, fakeVerification(false))
	if err == nil || result == nil || result.Status != "agent_failed" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if seen != 512 {
		t.Fatalf("provider saw a %d-token limit, want the 512-token budget of the job", seen)
	}
	if result.Register != "internal" {
		t.Fatalf("report register = %q, want internal", result.Register)
	}
	if result.PromptValidation == nil || result.PromptValidation.Status != config.EmissionPass || result.PromptValidation.MaxTokens != 512 || result.PromptValidation.Source != "repair_job.register" || result.RequestInstructionsValidation == nil || result.RequestInstructionsValidation.Status != config.EmissionPass || result.RequestInstructionsValidation.Kind != caveman.KindMessage || result.RequestInstructionsValidation.MaxTokens != 512 {
		t.Fatalf("prompt validation = %+v; request instructions = %+v", result.PromptValidation, result.RequestInstructionsValidation)
	}

	// An invalid register row in the run configuration is rejected before any provider call.
	g := newRunFixture(t, 1)
	g.config.RepairPolicy.Register = "loud"
	g.save(t)
	if _, err := run(t.Context(), g.configPath, g.reportPath, noProvider(t), fakeVerification(false)); err == nil {
		t.Fatal("an unsupported register must fail the run")
	}
}

func TestBuildPromptRejectsInvalidInternalInstructionsBeforeEvidence(t *testing.T) {
	job := dogfood.RepairJob{Register: string(config.TextRegisterInternal), MaxOutputTokens: 512,
		Instructions: "I think the repair prompt is ready. " + config.RegisterDirective(config.TextRegisterInternal)}
	_, validation, err := buildPrompt(t.Context(), Config{}, nil, job, nil)
	if err == nil || validation == nil || validation.Status != config.EmissionFail || validation.MaxTokens != 512 || validation.Source != "repair_job.register" {
		t.Fatalf("invalid instructions = %+v, %v", validation, err)
	}
	if strings.Contains(err.Error(), "read") {
		t.Fatalf("untrusted inputs were read before instruction validation: %v", err)
	}
}

func TestRunRejectsInvalidInternalSummaryBeforeApplyingEdits(t *testing.T) {
	requireRepairIsolation(t)
	f := newRunFixture(t, 1)
	f.config.RepairPolicy.Register = string(config.TextRegisterInternal)
	f.save(t)
	verifyCalls := 0
	verify := func(_ context.Context, c Config, _ string) (*TestResult, []byte, error) {
		verifyCalls++
		return &TestResult{Passed: false, Packages: c.TestPackages, Tests: []TestOutcome{{Package: "fixture", Name: "TestValue", Status: "fail"}}}, nil, nil
	}
	generate := func(_ context.Context, _ ProviderConfig, prompt string) (*Proposal, error) {
		directive := config.RegisterDirective(config.TextRegisterInternal)
		if strings.Count(prompt, directive) != 1 || strings.Index(prompt, directive) > strings.Index(prompt, "UNTRUSTED_DATA_JSON:") {
			t.Fatalf("resolved directive not forwarded before evidence:\n%s", prompt)
		}
		return &Proposal{Summary: "I think the repair is probably ready.", Edits: []Edit{{Path: "internal/fixture/value.go", OriginalSHA256: bytesSHA([]byte(originalFixture)), Content: repairedFixture}}}, nil
	}
	result, err := run(t.Context(), f.configPath, f.reportPath, generate, verify)
	if err == nil || result == nil || result.Status != "change_rejected" || verifyCalls != 1 {
		t.Fatalf("result=%+v err=%v verify_calls=%d", result, err, verifyCalls)
	}
	if result.PromptValidation == nil || result.PromptValidation.Status != config.EmissionPass || result.RequestInstructionsValidation == nil || result.RequestInstructionsValidation.Status != config.EmissionPass || result.SummaryValidation == nil || result.SummaryValidation.Status != config.EmissionFail {
		t.Fatalf("validation records: prompt=%+v request=%+v summary=%+v", result.PromptValidation, result.RequestInstructionsValidation, result.SummaryValidation)
	}
	data, readErr := os.ReadFile(filepath.Join(result.AttemptDir, "candidate", "internal/fixture/value.go"))
	if readErr != nil || string(data) != originalFixture {
		t.Fatalf("invalid summary reached applyProposal: %q, %v", data, readErr)
	}
}

func TestRunAcceptsInternalReturnAtTheSummaryBoundary(t *testing.T) {
	requireRepairIsolation(t)
	f := newRunFixture(t, 1)
	f.config.RepairPolicy.Register = string(config.TextRegisterInternal)
	f.save(t)
	verifyCalls := 0
	verify := func(_ context.Context, c Config, _ string) (*TestResult, []byte, error) {
		verifyCalls++
		status, passed := "fail", false
		if verifyCalls == 2 {
			status, passed = "pass", true
		}
		return &TestResult{Passed: passed, Packages: c.TestPackages, Tests: []TestOutcome{{Package: "fixture", Name: "TestValue", Status: status}}}, nil, nil
	}
	generate := func(_ context.Context, _ ProviderConfig, _ string) (*Proposal, error) {
		return &Proposal{Summary: validInternalRepairSummary, Edits: []Edit{{Path: "internal/fixture/value.go", OriginalSHA256: bytesSHA([]byte(originalFixture)), Content: repairedFixture}}}, nil
	}
	result, err := run(t.Context(), f.configPath, f.reportPath, generate, verify)
	if err != nil || result == nil || result.Status != "scoped_test_verified" || verifyCalls != 2 {
		t.Fatalf("result=%+v err=%v verify_calls=%d", result, err, verifyCalls)
	}
	if result.PromptValidation == nil || result.PromptValidation.Status != config.EmissionPass || result.RequestInstructionsValidation == nil || result.RequestInstructionsValidation.Status != config.EmissionPass || result.SummaryValidation == nil || result.SummaryValidation.Status != config.EmissionPass {
		t.Fatalf("validation records: prompt=%+v request=%+v summary=%+v", result.PromptValidation, result.RequestInstructionsValidation, result.SummaryValidation)
	}
}

func TestProviderInstructionsRecordHumanRegisterAsNotApplicable(t *testing.T) {
	job := dogfood.RepairJob{Register: string(config.TextRegisterSocial)}
	validation, err := validateProviderRequestInstructions(job)
	if err != nil || validation == nil || validation.Status != config.EmissionNotApplicable || validation.Kind != caveman.KindMessage {
		t.Fatalf("social provider instructions = %+v, %v", validation, err)
	}
	body := providerRequestBody(jobProvider(ProviderConfig{Model: "fixture", MaxOutputTokens: 256}, &job), "prompt")
	if body["instructions"] != legacyProviderInstructions {
		t.Fatalf("social provider instructions changed: %q", body["instructions"])
	}
	job.Instructions = "human-facing repair brief"
	prompt, promptValidation, err := repairPromptInstructions(job)
	if err == nil || promptValidation == nil || promptValidation.Status != config.EmissionFail {
		t.Fatalf("missing social directive must fail: prompt=%q validation=%+v err=%v", prompt, promptValidation, err)
	}
	job.Instructions = "human-facing repair brief " + config.RegisterDirective(config.TextRegisterSocial)
	prompt, promptValidation, err = repairPromptInstructions(job)
	if err != nil || promptValidation == nil || promptValidation.Status != config.EmissionNotApplicable {
		t.Fatalf("social prompt instructions = %q, %+v, %v", prompt, promptValidation, err)
	}
	want := legacyRepairPromptInstructions() + " " + config.RegisterDirective(config.TextRegisterSocial)
	if prompt != want {
		t.Fatalf("social prompt prose changed:\n%s", prompt)
	}
}
