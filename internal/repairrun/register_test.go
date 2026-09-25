package repairrun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/dogfood"
)

const validInternalRepairSummary = "verdict: candidate\nchanged: internal/fixture/value.go\nran: none\nevidence: proposal.edits\nopen: verification pending"
const alternateInternalRepairSummary = "verdict: alternate\nchanged: none\nran: none\nevidence: none\nopen: none"

func configureRunRegisters(t *testing.T, f *runFixture, register config.TextRegister, source string, budget int, prompt config.TextRegister) {
	t.Helper()
	body := "version: 1\n"
	if source == "tasks."+f.config.RepairPolicy.Task || budget > 0 {
		body += "register:\n  tasks:\n    " + f.config.RepairPolicy.Task + ": {register: " + string(register)
		if budget > 0 {
			body += ", max_tokens: " + strconv.Itoa(budget)
		}
		body += "}\n"
	} else if prompt != config.TextRegisterInternal {
		body += "register:\n  surfaces:\n    prompts: " + string(prompt) + "\n"
	}
	if body != "version: 1\n" {
		commitRegisterManifest(t, f, []byte(body))
	}
	authority, err := config.LoadRegisterAuthority(t.Context(), f.config.SourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := dogfood.CanonicalRepairPolicy(f.config.RepairPolicy, authority)
	if err != nil {
		t.Fatal(err)
	}
	if policy.RegisterSource != source || policy.PromptRegister != string(prompt) {
		t.Fatalf("fixture register request is not canonical: %+v", policy)
	}
	f.config.RepairPolicy = policy
}

func commitRegisterManifest(t *testing.T, f *runFixture, body []byte) {
	t.Helper()
	writeFixture(t, filepath.Join(f.config.SourceRoot, ".standards.yaml"), body)
	for _, args := range [][]string{{"-C", f.config.SourceRoot, "add", ".standards.yaml"},
		{"-C", f.config.SourceRoot, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "register fixture"}} {
		if output, err := runGit(t.Context(), "", maxLogBytes, args...); err != nil {
			t.Fatalf("commit register fixture: %s %v", output, err)
		}
	}
	sha, err := runGit(t.Context(), f.config.SourceRoot, 128, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	f.config.SourceSHA = strings.TrimSpace(string(sha))
}

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
	configureRunRegisters(t, f, config.TextRegisterInternal, "tasks.ci_debugging", 512, config.TextRegisterInternal)
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
	if result.Register != "internal" || result.RegisterSource != "tasks.ci_debugging" || result.MaxOutputTokens != 512 ||
		result.PromptRegister != "internal" || result.PromptRegisterSource != "surfaces.prompts" {
		t.Fatalf("report register resolutions = %+v", result)
	}
	if result.JobInstructionsValidation == nil || result.JobInstructionsValidation.Status != config.EmissionPass ||
		result.JobInstructionsValidation.MaxTokens != 512 || result.JobInstructionsValidation.Source != "tasks.ci_debugging" ||
		result.PromptValidation == nil || result.PromptValidation.Status != config.EmissionPass ||
		result.PromptValidation.MaxTokens != 0 || result.PromptValidation.Source != "surfaces.prompts" ||
		result.RequestInstructionsValidation == nil || result.RequestInstructionsValidation.Status != config.EmissionPass ||
		result.RequestInstructionsValidation.Kind != caveman.KindMessage || result.RequestInstructionsValidation.MaxTokens != 0 ||
		result.RequestInstructionsValidation.Source != "surfaces.prompts" {
		t.Fatalf("job=%+v prompt=%+v request=%+v", result.JobInstructionsValidation, result.PromptValidation, result.RequestInstructionsValidation)
	}

	// An invalid register row in the run configuration is rejected before any provider call.
	g := newRunFixture(t, 1)
	configureRunRegisters(t, g, config.TextRegisterInternal, "surfaces.agent", 0, config.TextRegisterInternal)
	g.config.RepairPolicy.Register = "loud"
	g.save(t)
	if _, err := run(t.Context(), g.configPath, g.reportPath, noProvider(t), fakeVerification(false)); err == nil {
		t.Fatal("an unsupported register must fail the run")
	}
}

func TestBuildPromptRejectsInvalidInternalInstructionsBeforeEvidence(t *testing.T) {
	job := dogfood.RepairJob{Register: string(config.TextRegisterInternal), RegisterSource: "surfaces.agent", MaxOutputTokens: 512,
		PromptRegister: string(config.TextRegisterInternal), PromptRegisterSource: "surfaces.prompts",
		RegisterManifestSHA256: config.AbsentRegisterAuthority().ManifestSHA256(),
		Instructions:           "I think the repair prompt is ready. " + config.RegisterDirective(config.TextRegisterInternal)}
	_, validations, err := buildPrompt(t.Context(), Config{}, nil, job, nil)
	if err == nil || validations.Job == nil || validations.Job.Status != config.EmissionFail ||
		validations.Job.MaxTokens != 512 || validations.Job.Source != "surfaces.agent" || validations.Prompt != nil {
		t.Fatalf("invalid instructions = %+v, %v", validations, err)
	}
	if strings.Contains(err.Error(), "read") {
		t.Fatalf("untrusted inputs were read before instruction validation: %v", err)
	}
}

func TestRunRejectsInvalidInternalSummaryBeforeApplyingEdits(t *testing.T) {
	requireRepairIsolation(t)
	base := validInternalRepairSummary + "\n"
	for name, summary := range map[string]string{
		"plain prose":        "I think the repair is probably ready.",
		"off region":         base + caveman.OffMarker + "\nwe are probably ready\n" + caveman.OnMarker,
		"HTML comment":       base + "<!-- we are probably ready -->",
		"fenced prose":       base + "```text\nwe are probably ready\n```",
		"heading prose":      base + "## we are probably ready",
		"Setext heading":     base + "runtime result\n=====",
		"table structure":    base + "| key | value |\n| --- | --- |\n| state | ready |",
		"ledger structure":   base + "- **State**: ready | **Owner**: agent",
		"quoted prose":       "verdict: pass\nchanged: none\nran: command \"we are probably ready\"\nevidence: none\nopen: none",
		"inline prose":       "verdict: pass\nchanged: none\nran: `we are probably ready`\nevidence: none\nopen: none",
		"trailing evidence":  "verdict: pass\nchanged: none\nran: none\nevidence: proof.json sha256:0123456789ab lines:1 we are probably ready\nopen: none",
		"malformed evidence": "verdict: pass\nchanged: none\nran: none\nevidence: proof.json sha256:invalid lines:1\nopen: none",
		"plus evidence":      "verdict: pass\nchanged: none\nran: none\n+ evidence: proof.json sha256:0123456789ab lines:1 extra\nopen: none",
		"uppercase evidence": "verdict: pass\nchanged: none\nran: none\nEvidence: proof.json SHA256:invalid LINES:1\nopen: none",
		"spaced evidence":    "verdict: pass\nchanged: none\nran: none\nevidence: proof.json sha256 : invalid lines : 1\nopen: none",
	} {
		t.Run(name, func(t *testing.T) {
			assertRejectedSummaryBeforeApplying(t, summary)
		})
	}
}

func assertRejectedSummaryBeforeApplying(t *testing.T, summary string) {
	t.Helper()
	f := newRunFixture(t, 1)
	configureRunRegisters(t, f, config.TextRegisterInternal, "surfaces.agent", 0, config.TextRegisterInternal)
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
		return &Proposal{Summary: summary, Edits: []Edit{{Path: "internal/fixture/value.go", OriginalSHA256: bytesSHA([]byte(originalFixture)), Content: repairedFixture}}}, nil
	}
	result, err := run(t.Context(), f.configPath, f.reportPath, generate, verify)
	if err == nil || result == nil || result.Status != "change_rejected" || verifyCalls != 1 {
		t.Fatalf("result=%+v err=%v verify_calls=%d", result, err, verifyCalls)
	}
	if result.JobInstructionsValidation == nil || result.JobInstructionsValidation.Status != config.EmissionPass ||
		result.PromptValidation == nil || result.PromptValidation.Status != config.EmissionPass ||
		result.RequestInstructionsValidation == nil || result.RequestInstructionsValidation.Status != config.EmissionPass ||
		result.SummaryValidation == nil || result.SummaryValidation.Status != config.EmissionFail {
		t.Fatalf("validation records: job=%+v prompt=%+v request=%+v summary=%+v", result.JobInstructionsValidation, result.PromptValidation, result.RequestInstructionsValidation, result.SummaryValidation)
	}
	data, readErr := os.ReadFile(filepath.Join(result.AttemptDir, "candidate", "internal/fixture/value.go"))
	if readErr != nil || string(data) != originalFixture {
		t.Fatalf("invalid summary reached applyProposal: %q, %v", data, readErr)
	}
	persistedData, readErr := os.ReadFile(filepath.Join(result.AttemptDir, "result.json"))
	var persisted Report
	if readErr != nil || json.Unmarshal(persistedData, &persisted) != nil || persisted.SummaryValidation == nil ||
		persisted.SummaryValidation.Status != config.EmissionFail {
		t.Fatalf("summary failure was not persisted: report=%+v err=%v", persisted, readErr)
	}
}

func TestLoadConfigRejectsCanonicalRegisterMismatch(t *testing.T) {
	for name, mutate := range map[string]func(*dogfood.RepairPolicy){
		"task register":   func(policy *dogfood.RepairPolicy) { policy.Register = "docs" },
		"task source":     func(policy *dogfood.RepairPolicy) { policy.RegisterSource = "tasks.ci_debugging" },
		"task budget":     func(policy *dogfood.RepairPolicy) { policy.MaxOutputTokens = config.RegisterMaxTokensFloor },
		"prompt register": func(policy *dogfood.RepairPolicy) { policy.PromptRegister = "docs" },
		"prompt source":   func(policy *dogfood.RepairPolicy) { policy.PromptRegisterSource = "surfaces.agent" },
		"manifest digest": func(policy *dogfood.RepairPolicy) { policy.RegisterManifestSHA256 = strings.Repeat("0", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			f := newRunFixture(t, 1)
			mutate(&f.config.RepairPolicy)
			f.save(t)
			if loaded, err := loadConfig(t.Context(), f.configPath, f.root); err == nil || loaded != nil {
				t.Fatalf("executor accepted non-canonical %s: config=%+v err=%v", name, loaded, err)
			}
			if _, err := os.Stat(f.config.StateDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("executor mismatch created state: %v", err)
			}
		})
	}
	t.Run("dirty manifest differs from pinned source", func(t *testing.T) {
		f := newRunFixture(t, 1)
		body := []byte("version: 1\nregister:\n  tasks:\n    ci_debugging: {register: docs, max_tokens: 256}\n")
		writeFixture(t, filepath.Join(f.config.SourceRoot, ".standards.yaml"), body)
		live, err := config.LoadRegisterAuthority(t.Context(), f.config.SourceRoot)
		if err != nil {
			t.Fatal(err)
		}
		f.config.RepairPolicy, err = dogfood.CanonicalRepairPolicy(f.config.RepairPolicy, live)
		if err != nil {
			t.Fatal(err)
		}
		f.save(t)
		if loaded, err := loadConfig(t.Context(), f.configPath, f.root); err == nil || loaded != nil {
			t.Fatalf("executor trusted dirty manifest over pinned source: config=%+v err=%v", loaded, err)
		}
	})
	t.Run("manifest task outside routing vocabulary", func(t *testing.T) {
		f := newRunFixture(t, 1)
		commitRegisterManifest(t, f, []byte("version: 1\nregister:\n  tasks:\n    undeclared_task: internal\n"))
		authority, err := loadPinnedRegisterAuthority(t.Context(), f.config)
		if err != nil {
			t.Fatal(err)
		}
		f.config.RepairPolicy, err = dogfood.CanonicalRepairPolicy(f.config.RepairPolicy, authority)
		if err != nil {
			t.Fatal(err)
		}
		f.save(t)
		if loaded, err := loadConfig(t.Context(), f.configPath, f.root); err == nil || loaded != nil {
			t.Fatalf("executor accepted manifest task outside routing vocabulary: config=%+v err=%v", loaded, err)
		}
	})
}

func TestPinnedRegisterManifestSizeBoundary(t *testing.T) {
	for _, size := range []int{contextopt.MaxSourceBytes, contextopt.MaxSourceBytes + 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			f := newRunFixture(t, 1)
			prefix := "version: 1\n#"
			commitRegisterManifest(t, f, []byte(prefix+strings.Repeat("x", size-len(prefix))))
			authority, err := loadPinnedRegisterAuthority(t.Context(), f.config)
			if size == contextopt.MaxSourceBytes && (err != nil || len(authority.ManifestSHA256()) != 64) {
				t.Fatalf("exact manifest bound rejected: digest=%q err=%v", authority.ManifestSHA256(), err)
			}
			if size > contextopt.MaxSourceBytes && err == nil {
				t.Fatal("oversize pinned register manifest accepted")
			}
		})
	}
}

func TestRunAcceptsInternalReturnAtTheSummaryBoundary(t *testing.T) {
	requireRepairIsolation(t)
	f := newRunFixture(t, 1)
	configureRunRegisters(t, f, config.TextRegisterInternal, "surfaces.agent", 0, config.TextRegisterInternal)
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
	generate := func(_ context.Context, _ ProviderConfig, prompt string) (*Proposal, error) {
		stated := strings.Join(statedSummaryFields(prompt), ",")
		if stated != strings.Join(caveman.SchemaFields(caveman.KindReturn), ",") ||
			strings.Index(prompt, "summary: ") > strings.Index(prompt, "UNTRUSTED_DATA_JSON:") {
			t.Fatalf("provider prompt states summary fields %q before evidence, want return shape:\n%s", stated, prompt)
		}
		return &Proposal{Summary: validInternalRepairSummary, Edits: []Edit{{Path: "internal/fixture/value.go", OriginalSHA256: bytesSHA([]byte(originalFixture)), Content: repairedFixture}}}, nil
	}
	result, err := run(t.Context(), f.configPath, f.reportPath, generate, verify)
	if err != nil || result == nil || result.Status != "scoped_test_verified" || verifyCalls != 2 {
		t.Fatalf("result=%+v err=%v verify_calls=%d", result, err, verifyCalls)
	}
	if result.JobInstructionsValidation == nil || result.JobInstructionsValidation.Status != config.EmissionPass ||
		result.PromptValidation == nil || result.PromptValidation.Status != config.EmissionPass ||
		result.RequestInstructionsValidation == nil || result.RequestInstructionsValidation.Status != config.EmissionPass ||
		result.SummaryValidation == nil || result.SummaryValidation.Status != config.EmissionPass {
		t.Fatalf("validation records: job=%+v prompt=%+v request=%+v summary=%+v", result.JobInstructionsValidation, result.PromptValidation, result.RequestInstructionsValidation, result.SummaryValidation)
	}
	assertTerminalRegisterProof(t, f, result)
}

func assertTerminalRegisterProof(t *testing.T, f *runFixture, result *Report) {
	t.Helper()
	begin := started{Version: 1, ExecutionKey: result.ExecutionKey, SourceSHA: result.SourceSHA, ConfigSHA256: result.ConfigSHA256}
	plan, planErr := dogfood.PlanRepairs(t.Context(), &f.report, f.config.RepairPolicy)
	if planErr != nil || len(plan.Jobs) != 1 {
		t.Fatalf("rebuild expected job: plan=%+v err=%v", plan, planErr)
	}
	expected := plan.Jobs[0]
	inputs := terminalInputsForJob(expected)
	summary := validInternalRepairSummary
	inputs.summary = &summary
	if !validTerminal(*result, begin, expected, inputs) {
		t.Fatal("current registered terminal outcome rejected")
	}
	early := *result
	early.Status, early.CandidateVerified = "not_reproduced", false
	early.PromptValidation, early.RequestInstructionsValidation, early.SummaryValidation = nil, nil, nil
	if !validTerminal(early, begin, expected, terminalInputsForJob(expected)) {
		t.Fatal("registered pre-dispatch outcome with planner proof rejected")
	}
	missing := *result
	missing.SummaryValidation = nil
	if validTerminal(missing, begin, expected, inputs) {
		t.Fatal("pre-enforcement registered terminal without summary proof accepted")
	}
	mismatched := *result
	copyRecord := *result.PromptValidation
	copyRecord.Source = "surfaces.agent"
	mismatched.PromptValidation = &copyRecord
	if validTerminal(mismatched, begin, expected, inputs) {
		t.Fatal("terminal outcome with mismatched prompt provenance accepted")
	}
	for name, mutate := range map[string]func(*config.EmissionValidation){
		"stale checker contract": func(record *config.EmissionValidation) { record.ContractVersion-- },
		"forged text digest":     func(record *config.EmissionValidation) { record.TextSHA256 = strings.Repeat("0", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			tampered := *result
			record := *result.SummaryValidation
			mutate(&record)
			tampered.SummaryValidation = &record
			if validTerminal(tampered, begin, expected, inputs) {
				t.Fatal("terminal outcome with forged validation proof accepted")
			}
		})
	}
	forged := *result
	forgedRecord, forgedErr := config.ValidateEmission(config.Resolution{Register: config.TextRegisterInternal,
		Source: "surfaces.agent", ManifestSHA256: result.RegisterManifestSHA256}, config.SurfaceAgent, caveman.KindReturn, alternateInternalRepairSummary)
	if forgedErr != nil {
		t.Fatal(forgedErr)
	}
	forged.SummaryValidation = &forgedRecord
	if validTerminal(forged, begin, expected, inputs) {
		t.Fatal("terminal outcome with another text's valid proof accepted")
	}
	literalSummary := "verdict: pass\nchanged: C:/work/we/is/value.go:42\nran: HTTPS://example.test/log_(we)/is\nevidence: none\nopen: none"
	literalRecord, literalErr := config.ValidateEmission(config.Resolution{Register: config.TextRegisterInternal,
		Source: "surfaces.agent", ManifestSHA256: result.RegisterManifestSHA256}, config.SurfaceAgent, caveman.KindReturn, literalSummary)
	if literalErr != nil {
		t.Fatal(literalErr)
	}
	literalResult, literalInputs := *result, inputs
	literalResult.SummaryValidation, literalInputs.summary = &literalRecord, &literalSummary
	if !validTerminal(literalResult, begin, expected, literalInputs) {
		t.Fatal("terminal replay rejected valid URL and path literals")
	}
	for _, invalid := range []struct {
		name    string
		summary string
	}{
		{"default-ignorable grammar", "verdict: pass\nchanged: none\nran: w\u034fe a\u034fre ready\nevidence: none\nopen: none"},
		{"punctuation grammar", "verdict: pass\nchanged: none\nran: w.e w:e w_e w-e w+e w=e w#e w|e w~e w,e\nevidence: none\nopen: none"},
		{"Unicode punctuation grammar", "verdict: pass\nchanged: none\nran: w\uFF0Ee w\u00B7e w\u2014e w\uFF0Fe w\u2215e w\u2024e\nevidence: none\nopen: none"},
		{"unsafe control grammar", "verdict: pass\nchanged: none\nran: w\x00e w\x08e w\x7fe\nevidence: none\nopen: none"},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			invalidInputs := inputs
			invalidInputs.summary = &invalid.summary
			digest := sha256.Sum256([]byte(invalid.summary))
			record := *result.SummaryValidation
			record.Status, record.Findings = config.EmissionPass, 0
			record.TextSHA256 = hex.EncodeToString(digest[:])
			forged := *result
			forged.SummaryValidation = &record
			if validTerminal(forged, begin, expected, invalidInputs) {
				t.Fatal("terminal replay accepted forged pass")
			}
		})
	}
	stripped := *result
	stripped.Register, stripped.RegisterSource, stripped.MaxOutputTokens = "", "", 0
	stripped.PromptRegister, stripped.PromptRegisterSource = "", ""
	stripped.JobInstructionsValidation, stripped.PromptValidation = nil, nil
	stripped.RequestInstructionsValidation, stripped.SummaryValidation = nil, nil
	if validTerminal(stripped, begin, expected, inputs) {
		t.Fatal("registered terminal stripped to a legacy-looking outcome accepted")
	}
	terminalPath := filepath.Join(result.AttemptDir, "result.json")
	terminalData, readErr := os.ReadFile(terminalPath)
	var terminal Report
	if readErr != nil {
		t.Fatalf("read terminal outcome: %v", readErr)
	}
	if unmarshalErr := json.Unmarshal(terminalData, &terminal); unmarshalErr != nil {
		t.Fatalf("decode terminal outcome: %v", unmarshalErr)
	}
	terminal.SummaryValidation = nil
	terminalData, marshalErr := json.Marshal(terminal)
	if marshalErr != nil {
		t.Fatalf("write stale terminal fixture: %v", marshalErr)
	}
	if writeErr := os.WriteFile(terminalPath, terminalData, 0o600); writeErr != nil {
		t.Fatalf("write stale terminal fixture: %v", writeErr)
	}
	if status, statusErr := Status(t.Context(), f.configPath, f.reportPath); statusErr == nil || status == nil {
		t.Fatalf("status accepted terminal without required proof: status=%+v err=%v", status, statusErr)
	}
}

func TestProviderInstructionsResolvePromptSurfaceIndependently(t *testing.T) {
	cases := []struct {
		name, taskRegister, taskSource, promptRegister, instructions string
		status                                                       config.EmissionStatus
		providerInstructions                                         string
	}{
		{"docs task internal prompt", "docs", "tasks.architecture_synthesis", "internal",
			"Review retained failure. " + config.RegisterDirective(config.TextRegisterDocs), config.EmissionPass, registeredProviderInstructions},
		{"internal task docs prompt", "internal", "surfaces.agent", "docs",
			"goal: reproduce failure\ninputs: retained evidence\nreturn: proposal only\nevidence: bounded case\ntask: ci_debugging\n" + config.RegisterDirective(config.TextRegisterInternal),
			config.EmissionNotApplicable, legacyProviderInstructions},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			job := dogfood.RepairJob{Register: tc.taskRegister, RegisterSource: tc.taskSource,
				PromptRegister: tc.promptRegister, PromptRegisterSource: "surfaces.prompts",
				RegisterManifestSHA256: config.AbsentRegisterAuthority().ManifestSHA256(), Instructions: tc.instructions}
			validation, err := validateProviderRequestInstructions(job)
			if err != nil || validation == nil || validation.Status != tc.status || validation.Source != "surfaces.prompts" {
				t.Fatalf("provider instructions = %+v, %v", validation, err)
			}
			body := providerRequestBody(jobProvider(ProviderConfig{Model: "fixture", MaxOutputTokens: 256}, &job), "prompt")
			if body["instructions"] != tc.providerInstructions {
				t.Fatalf("provider instructions = %q, want %q", body["instructions"], tc.providerInstructions)
			}
			prompt, validations, err := repairPromptInstructions(job)
			if err != nil || validations.Job == nil || validations.Prompt == nil || validations.Prompt.Status != tc.status {
				t.Fatalf("prompt=%q validations=%+v err=%v", prompt, validations, err)
			}
			if strings.Count(prompt, config.RegisterDirective(config.TextRegister(tc.taskRegister))) != 1 {
				t.Fatalf("task directive not forwarded exactly once: %q", prompt)
			}
		})
	}
}

// TestRepairPromptStatesTheSummaryReturnShape proves the provider is told the summary shape
// the validator enforces. The summary is built only from what the prompt states, the way a
// model would, so a prompt that omits or misstates the shape fails here.
func TestRepairPromptStatesTheSummaryReturnShape(t *testing.T) {
	internalBrief := "goal: reproduce failure\ninputs: retained evidence\nreturn: proposal only\nevidence: bounded case\ntask: ci_debugging\n" +
		config.RegisterDirective(config.TextRegisterInternal)
	cases := []struct {
		name, taskRegister, promptRegister, instructions string
		promptStatus                                     config.EmissionStatus
		stated                                           bool
	}{
		{"internal task internal prompt", "internal", "internal", internalBrief, config.EmissionPass, true},
		{"internal task docs prompt", "internal", "docs", internalBrief, config.EmissionNotApplicable, true},
		{"docs task internal prompt", "docs", "internal", "Review retained failure. " + config.RegisterDirective(config.TextRegisterDocs), config.EmissionPass, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			job := dogfood.RepairJob{Register: tc.taskRegister, RegisterSource: "surfaces.agent", MaxOutputTokens: 512,
				PromptRegister: tc.promptRegister, PromptRegisterSource: "surfaces.prompts",
				RegisterManifestSHA256: config.AbsentRegisterAuthority().ManifestSHA256(), Instructions: tc.instructions}
			prompt, validations, err := repairPromptInstructions(job)
			if err != nil || validations.Prompt == nil || validations.Prompt.Status != tc.promptStatus {
				t.Fatalf("prompt=%q validations=%+v err=%v", prompt, validations, err)
			}
			fields := statedSummaryFields(prompt)
			if !tc.stated {
				if fields != nil {
					t.Fatalf("summary shape stated for unvalidated %s summary: %q", tc.taskRegister, prompt)
				}
				return
			}
			if strings.Index(prompt, "summary: ") > strings.Index(prompt, "TASK_BRIEF:") || len(fields) == 0 {
				t.Fatalf("summary shape missing from engine-owned prefix:\n%s", prompt)
			}
			lines := make([]string, 0, len(fields))
			for _, field := range fields {
				lines = append(lines, field+": none")
			}
			if validation, err := validateProposalSummary(job, strings.Join(lines, "\n")); err != nil || validation.Status != config.EmissionPass {
				t.Fatalf("summary following stated shape %v rejected: %+v %v", fields, validation, err)
			}
			if validation, err := validateProposalSummary(job, strings.Join(lines[:len(lines)-1], "\n")); err == nil || validation.Status != config.EmissionFail {
				t.Fatalf("summary without stated field %q accepted: %+v", fields[len(fields)-1], validation)
			}
		})
	}
}

// statedSummaryFields reads the field list from the prompt's summary line, or nil.
func statedSummaryFields(prompt string) []string {
	const marker = "fields in order "
	for _, line := range strings.Split(prompt, "\n") {
		rest, found := strings.CutPrefix(line, "summary: ")
		if !found {
			continue
		}
		start := strings.Index(rest, marker)
		if start < 0 {
			return []string{}
		}
		list, _, _ := strings.Cut(rest[start+len(marker):], ";")
		return strings.Split(list, ", ")
	}
	return nil
}

func TestStatusRejectsSummaryChangedAfterValidation(t *testing.T) {
	requireRepairIsolation(t)
	f := newRunFixture(t, 1)
	configureRunRegisters(t, f, config.TextRegisterInternal, "surfaces.agent", 0, config.TextRegisterInternal)
	f.save(t)
	verifyCalls := 0
	verify := func(_ context.Context, c Config, _ string) (*TestResult, []byte, error) {
		verifyCalls++
		status, passed := "fail", false
		if verifyCalls == 2 {
			status, passed = "pass", true
		}
		return &TestResult{Passed: passed, Packages: c.TestPackages,
			Tests: []TestOutcome{{Package: "fixture", Name: "TestValue", Status: status}}}, nil, nil
	}
	generate := func(_ context.Context, _ ProviderConfig, _ string) (*Proposal, error) {
		return &Proposal{Summary: validInternalRepairSummary, Edits: []Edit{{Path: "internal/fixture/value.go",
			OriginalSHA256: bytesSHA([]byte(originalFixture)), Content: repairedFixture}}}, nil
	}
	result, err := run(t.Context(), f.configPath, f.reportPath, generate, verify)
	if err != nil || result == nil || result.Status != "scoped_test_verified" {
		t.Fatalf("registered run: result=%+v err=%v", result, err)
	}
	assertStatusRejectsTerminalMutation(t, f, result, func(report *Report) {
		report.SummaryValidation.ContractVersion--
	})
	assertStatusRejectsTerminalMutation(t, f, result, func(report *Report) {
		report.SummaryValidation.TextSHA256 = strings.Repeat("0", 64)
	})
	proposalPath := filepath.Join(result.AttemptDir, "proposal.json")
	data, readErr := os.ReadFile(proposalPath)
	var proposal Proposal
	if readErr != nil {
		t.Fatal(readErr)
	}
	if decodeErr := json.Unmarshal(data, &proposal); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	originalProposal := data
	for name, summary := range map[string]string{
		"different valid summary": alternateInternalRepairSummary,
		"runtime suppression":     validInternalRepairSummary + "\n<!-- we are probably ready -->",
	} {
		t.Run(name, func(t *testing.T) {
			proposal.Summary = summary
			changed, marshalErr := json.Marshal(proposal)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if writeErr := os.WriteFile(proposalPath, changed, 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
			if status, statusErr := Status(t.Context(), f.configPath, f.reportPath); statusErr == nil || status == nil {
				t.Fatalf("status accepted summary changed after validation: status=%+v err=%v", status, statusErr)
			}
			if writeErr := os.WriteFile(proposalPath, originalProposal, 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
		})
	}
	proposal.Summary = validInternalRepairSummary + "\n<!-- we are probably ready -->"
	suppressed, err := json.Marshal(proposal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(proposalPath, suppressed, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"not_reproduced", "agent_failed", "failed", "timeout"} {
		t.Run("downgrade to "+status, func(t *testing.T) {
			assertStatusRejectsTerminalMutation(t, f, result, func(report *Report) {
				report.Status = status
				report.CandidateVerified = false
				report.SummaryValidation = nil
				if status == "not_reproduced" {
					report.PromptValidation = nil
					report.RequestInstructionsValidation = nil
				}
			})
		})
	}
	if err := os.WriteFile(proposalPath, originalProposal, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertStatusRejectsTerminalMutation(t *testing.T, f *runFixture, result *Report, mutate func(*Report)) {
	t.Helper()
	path := filepath.Join(result.AttemptDir, "result.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var terminal Report
	if err := json.Unmarshal(original, &terminal); err != nil {
		t.Fatal(err)
	}
	mutate(&terminal)
	changed, err := json.Marshal(terminal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	status, statusErr := Status(t.Context(), f.configPath, f.reportPath)
	if statusErr == nil || status == nil {
		t.Fatalf("status accepted forged terminal proof: status=%+v err=%v", status, statusErr)
	}
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
}
