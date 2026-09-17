package dogfood

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

func TestRepairJobsCarryTheRegisterRow(t *testing.T) {
	policy := repairTestPolicy(t)
	policy.Register, policy.MaxOutputTokens = string(config.TextRegisterInternal), 512
	plan, err := PlanRepairs(context.Background(), repairTestReport(t, 2), policy)
	if err != nil {
		t.Fatal(err)
	}
	clause := " " + config.RegisterDirective(config.TextRegisterInternal)
	for _, job := range plan.Jobs {
		if job.Instructions != repairInstructions+clause {
			t.Errorf("job %s instructions must end with the register clause:\n%s", job.ID, job.Instructions)
		}
		if job.Register != "internal" || job.MaxOutputTokens != 512 {
			t.Errorf("job %s carries register %q and budget %d", job.ID, job.Register, job.MaxOutputTokens)
		}
	}
	if plan.Policy.Register != "internal" || plan.Policy.MaxOutputTokens != 512 {
		t.Errorf("the plan must retain the policy it was planned with: %+v", plan.Policy)
	}
}

func TestRepairPolicyRejectsInvalidRegisterRow(t *testing.T) {
	for name, mutate := range map[string]func(*RepairPolicy){
		"unknown register":    func(p *RepairPolicy) { p.Register = "loud" },
		"budget below floor":  func(p *RepairPolicy) { p.MaxOutputTokens = config.RegisterMaxTokensFloor - 1 },
		"budget above cap":    func(p *RepairPolicy) { p.MaxOutputTokens = config.RegisterMaxTokensCeiling + 1 },
		"negative budget":     func(p *RepairPolicy) { p.MaxOutputTokens = -1 },
		"register with a tab": func(p *RepairPolicy) { p.Register = "internal\t" },
	} {
		t.Run(name, func(t *testing.T) {
			policy := repairTestPolicy(t)
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
	// An empty register leaves the instructions byte-identical to the constant.
	plan, err := PlanRepairs(context.Background(), repairTestReport(t, 1), repairTestPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Jobs[0].Instructions != repairInstructions || plan.Jobs[0].Register != "" || plan.Jobs[0].MaxOutputTokens != 0 {
		t.Fatalf("a policy without a register row must plan as before: %+v", plan.Jobs[0])
	}
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"register"`) || strings.Contains(string(data), `"max_output_tokens"`) {
		t.Fatalf("empty fields must not appear in the plan JSON: %s", data)
	}

	// A plan written before the fields existed decodes with both empty.
	var legacy RepairPlan
	if err := json.Unmarshal(data, &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Policy.Register != "" || legacy.Jobs[0].Register != "" || legacy.Jobs[0].MaxOutputTokens != 0 {
		t.Fatalf("legacy plan decoded with a register: %+v", legacy)
	}

	// Both ends of the budget range are accepted.
	for _, budget := range []int{config.RegisterMaxTokensFloor, config.RegisterMaxTokensCeiling} {
		policy := repairTestPolicy(t)
		policy.Register, policy.MaxOutputTokens = string(config.TextRegisterSocial), budget
		if err := ValidateRepairPolicy(context.Background(), policy); err != nil {
			t.Fatalf("budget %d: %v", budget, err)
		}
	}
}
