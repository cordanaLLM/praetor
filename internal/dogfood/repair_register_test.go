package dogfood

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/config"
)

func TestRepairJobsCarryTheRegisterRow(t *testing.T) {
	policy := repairTestPolicy(t)
	authority := repairRegisterAuthority(t, "ci_debugging: {register: internal, max_tokens: 512}")
	policy = canonicalRepairTestPolicy(t, policy, authority)
	plan, err := PlanRepairs(context.Background(), repairTestReport(t, 2), policy)
	if err != nil {
		t.Fatal(err)
	}
	directive := config.RegisterDirective(config.TextRegisterInternal)
	for _, job := range plan.Jobs {
		if !strings.HasSuffix(job.Instructions, directive) || !strings.HasPrefix(job.Instructions, "goal:") {
			t.Errorf("job %s instructions must be a brief ending with the register directive:\n%s", job.ID, job.Instructions)
		}
		if job.Register != "internal" || job.RegisterSource != "tasks.ci_debugging" || job.MaxOutputTokens != 512 ||
			job.PromptRegister != "internal" || job.PromptRegisterSource != "surfaces.prompts" {
			t.Errorf("job %s carries register %q and budget %d", job.ID, job.Register, job.MaxOutputTokens)
		}
		if job.InstructionsValidation == nil || job.InstructionsValidation.Status != config.EmissionPass ||
			job.InstructionsValidation.Kind != caveman.KindBrief || job.InstructionsValidation.MaxTokens != 512 ||
			job.InstructionsValidation.Source != "tasks.ci_debugging" ||
			job.InstructionsValidation.ManifestSHA256 != authority.ManifestSHA256() ||
			job.RegisterManifestSHA256 != authority.ManifestSHA256() {
			t.Errorf("job %s instructions validation = %+v", job.ID, job.InstructionsValidation)
		}
	}
	if plan.Policy.Register != "internal" || plan.Policy.RegisterSource != "tasks.ci_debugging" || plan.Policy.MaxOutputTokens != 512 ||
		plan.Policy.PromptRegister != "internal" || plan.Policy.PromptRegisterSource != "surfaces.prompts" {
		t.Errorf("the plan must retain the policy it was planned with: %+v", plan.Policy)
	}
}

func repairRegisterAuthority(t *testing.T, row string) config.RegisterAuthority {
	t.Helper()
	body := []byte("version: 1\nregister:\n  tasks:\n    " + row + "\n")
	authority, err := config.ParseRegisterAuthority(body, "fixture/.standards.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return authority
}

func canonicalRepairTestPolicy(t *testing.T, policy RepairPolicy, authority config.RegisterAuthority) RepairPolicy {
	t.Helper()
	bound, err := CanonicalRepairPolicy(policy, authority)
	if err != nil {
		t.Fatal(err)
	}
	return bound
}

func setRepairRegisterPolicy(policy *RepairPolicy, register config.TextRegister, source string, budget int, prompt config.TextRegister) {
	policy.Register, policy.RegisterSource, policy.MaxOutputTokens = string(register), source, budget
	policy.PromptRegister, policy.PromptRegisterSource = string(prompt), "surfaces.prompts"
}

func TestRepairPolicyRejectsMissingRuntimeRegisterResolutions(t *testing.T) {
	policy := repairTestPolicy(t)
	policy.Register, policy.RegisterSource = "", ""
	policy.PromptRegister, policy.PromptRegisterSource = "", ""
	if err := ValidateRepairPolicy(t.Context(), policy); err == nil {
		t.Fatal("repair policy without task and prompt resolutions accepted")
	}
	if plan, err := PlanRepairs(t.Context(), repairTestReport(t, 1), policy); err == nil || plan != nil {
		t.Fatalf("registerless repair policy planned: plan=%+v err=%v", plan, err)
	}
}

func TestRepairPolicyRejectsInvalidRegisterRow(t *testing.T) {
	for name, mutate := range map[string]func(*RepairPolicy){
		"unknown register":    func(p *RepairPolicy) { p.Register = "loud" },
		"budget below floor":  func(p *RepairPolicy) { p.MaxOutputTokens = config.RegisterMaxTokensFloor - 1 },
		"budget above cap":    func(p *RepairPolicy) { p.MaxOutputTokens = config.RegisterMaxTokensCeiling + 1 },
		"negative budget":     func(p *RepairPolicy) { p.MaxOutputTokens = -1 },
		"register with a tab": func(p *RepairPolicy) { p.Register = "internal\t" },
		"missing task source": func(p *RepairPolicy) { p.RegisterSource = "" },
		"wrong task source":   func(p *RepairPolicy) { p.RegisterSource = "tasks.other" },
		"missing prompt row":  func(p *RepairPolicy) { p.PromptRegister = "" },
		"unknown prompt row":  func(p *RepairPolicy) { p.PromptRegister = "loud" },
		"wrong prompt source": func(p *RepairPolicy) { p.PromptRegisterSource = "surfaces.agent" },
	} {
		t.Run(name, func(t *testing.T) {
			policy := repairTestPolicy(t)
			setRepairRegisterPolicy(&policy, config.TextRegisterInternal, "surfaces.agent", 0, config.TextRegisterInternal)
			mutate(&policy)
			if err := ValidateRepairPolicy(context.Background(), policy); err == nil {
				t.Fatal("invalid register row accepted")
			}
			if plan, err := PlanRepairs(context.Background(), repairTestReport(t, 1), policy); err == nil || plan != nil {
				t.Fatalf("invalid register row planned: %+v", plan)
			}
		})
	}
}

func TestRepairRegisterBoundary(t *testing.T) {
	// Both ends of the budget range are accepted.
	for _, budget := range []int{config.RegisterMaxTokensFloor, config.RegisterMaxTokensCeiling} {
		policy := repairTestPolicy(t)
		authority := repairRegisterAuthority(t, fmt.Sprintf("ci_debugging: {register: social, max_tokens: %d}", budget))
		policy = canonicalRepairTestPolicy(t, policy, authority)
		plan, err := PlanRepairs(context.Background(), repairTestReport(t, 1), policy)
		if err != nil {
			t.Fatalf("budget %d: %v", budget, err)
		}
		validation := plan.Jobs[0].InstructionsValidation
		if validation == nil || validation.Status != config.EmissionNotApplicable {
			t.Fatalf("social budget %d validation = %+v", budget, validation)
		}
		want := repairInstructions + " " + config.RegisterDirective(config.TextRegisterSocial)
		if plan.Jobs[0].Instructions != want {
			t.Fatalf("social budget %d instructions changed:\n%s", budget, plan.Jobs[0].Instructions)
		}
	}
}

func TestRepairPlanRetainsGeneratedInstructionFailure(t *testing.T) {
	policy := repairTestPolicy(t)
	policy.Task = "a"
	policy.RoutingConfig = repairTestFile(t, strings.Replace(repairRoutingFixture, "ci_debugging", "a", 1))
	policy = canonicalRepairTestPolicy(t, policy, repairRegisterAuthority(t, "a: internal"))
	plan, err := PlanRepairs(context.Background(), repairTestReport(t, 1), policy)
	if err == nil || plan == nil || plan.Status != "blocked" || len(plan.Jobs) != 1 {
		t.Fatalf("generated invalid brief must return a retainable blocked plan: plan=%+v err=%v", plan, err)
	}
	job := plan.Jobs[0]
	if job.Status != "invalid_instructions" || job.InstructionsValidation == nil ||
		job.InstructionsValidation.Status != config.EmissionFail || job.InstructionsValidation.Source != "tasks.a" {
		t.Fatalf("generated instruction failure was not retained: %+v", job)
	}
}

func TestRepairPolicyRejectsCanonicalRegisterMismatch(t *testing.T) {
	for name, mutate := range map[string]func(*RepairPolicy){
		"task register":   func(p *RepairPolicy) { p.Register = "docs" },
		"task source":     func(p *RepairPolicy) { p.RegisterSource = "tasks.ci_debugging" },
		"task budget":     func(p *RepairPolicy) { p.MaxOutputTokens = 256 },
		"prompt register": func(p *RepairPolicy) { p.PromptRegister = "docs" },
		"prompt source":   func(p *RepairPolicy) { p.PromptRegisterSource = "surfaces.agent" },
		"manifest digest": func(p *RepairPolicy) { p.RegisterManifestSHA256 = strings.Repeat("0", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			policy := repairTestPolicy(t)
			mutate(&policy)
			if plan, err := PlanRepairs(t.Context(), repairTestReport(t, 1), policy); err == nil || plan != nil {
				t.Fatalf("canonical mismatch planned: plan=%+v err=%v", plan, err)
			}
		})
	}
	policy := repairTestPolicy(t)
	policy.registerAuthority = config.RegisterAuthority{}
	if plan, err := PlanRepairs(t.Context(), repairTestReport(t, 1), policy); err == nil || plan != nil {
		t.Fatalf("caller tuple without authority planned: plan=%+v err=%v", plan, err)
	}
}

func TestRepairPolicyRejectsRegisterTaskOutsideRoutingVocabulary(t *testing.T) {
	policy := repairTestPolicy(t)
	authority := repairRegisterAuthority(t, "undeclared_task: internal")
	policy = canonicalRepairTestPolicy(t, policy, authority)
	if err := ValidateRepairPolicy(t.Context(), policy); err == nil {
		t.Fatal("register task outside captured routing vocabulary accepted")
	}
	if plan, err := PlanRepairs(t.Context(), repairTestReport(t, 1), policy); err == nil || plan != nil {
		t.Fatalf("undeclared register task planned: plan=%+v err=%v", plan, err)
	}
}
