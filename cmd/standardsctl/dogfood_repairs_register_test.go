package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/dogfood"
)

func readRepairPlan(t *testing.T, args []string) dogfood.RepairPlan {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(args[len(args)-1], "plan.json"))
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	var plan dogfood.RepairPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatalf("decode plan: %v", err)
	}
	return plan
}

func TestDogfoodRepairCLIPlansWithTheTaskRegister(t *testing.T) {
	// Without a manifest the default policy governs: ci_debugging is internal, no budget.
	args := repairCLIFixture(t)
	t.Chdir(t.TempDir())
	if err := runDogfoodRepairs(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	plan := readRepairPlan(t, args)
	if plan.Policy.Register != "internal" || plan.Policy.MaxOutputTokens != 0 || len(plan.Jobs) == 0 {
		t.Fatalf("default policy: %+v", plan.Policy)
	}
	if job := plan.Jobs[0]; job.Register != "internal" || job.MaxOutputTokens != 0 {
		t.Fatalf("default job: register %q budget %d", job.Register, job.MaxOutputTokens)
	}

	// A manifest row for the task decides the register and forwards its budget.
	args = repairCLIFixture(t)
	writeFixtureFile(t, ".", ".config/models/routing.yaml", readFixtureFile(t, filepath.Dir(args[3]), filepath.Base(args[3])))
	writeFixtureFile(t, ".", ".standards.yaml", "version: 1\nregister:\n  tasks:\n    ci_debugging: {register: social, max_tokens: 256}\n")
	if err := runDogfoodRepairs(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	plan = readRepairPlan(t, args)
	directive := config.RegisterDirective(config.TextRegisterSocial)
	job := plan.Jobs[0]
	if job.Register != "social" || job.MaxOutputTokens != 256 || job.Instructions[len(job.Instructions)-len(directive):] != directive {
		t.Fatalf("manifest row: register %q budget %d instructions %q", job.Register, job.MaxOutputTokens, job.Instructions)
	}
}

func TestDogfoodRepairCLIRejectsUndeclaredRegisterRow(t *testing.T) {
	args := repairCLIFixture(t)
	t.Chdir(t.TempDir())
	writeFixtureFile(t, ".", ".standards.yaml", "version: 1\nregister:\n  tasks:\n    deploy_prod: docs\n")
	err := runDogfoodRepairs(context.Background(), args)
	mustErrContain(t, err, `register task "deploy_prod" is not a declared target_tasks label`)
	if _, statErr := os.Stat(filepath.Join(args[len(args)-1], "plan.json")); statErr == nil {
		t.Fatal("a rejected manifest must not leave a plan behind")
	}
}

// narrowRepairRouting writes a repair routing config that declares only the given labels, the
// shape a dogfood schedule's repair_policy.routing_config has on a workstation (#765).
func narrowRepairRouting(t *testing.T, path string, labels string) {
	t.Helper()
	body := "version: 1\ntiers:\n  debug:\n    target_tasks: [" + labels + "]\n    models:\n      - id: cheap\n        family: openai\n        cost_per_m_in: 1\n        cost_per_m_out: 1\ngovernance:\n  exhaustion_threshold_percent: 80\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The repository's register rows are checked against the repository's own router; a narrow
// repair router only has to declare the repair task (#765).
func TestDogfoodRepairCLIChecksRowsAgainstTheRepositoryRouter(t *testing.T) {
	args := repairCLIFixture(t)
	t.Chdir(t.TempDir())
	writeFixtureFile(t, ".", ".config/models/routing.yaml", readFixtureFile(t, filepath.Dir(args[3]), filepath.Base(args[3])))
	writeFixtureFile(t, ".", ".standards.yaml", "version: 1\nregister:\n  tasks:\n    architecture_synthesis: docs\n    commit_message_synthesis: social\n")
	narrowRepairRouting(t, args[3], "ci_debugging")
	if err := runDogfoodRepairs(context.Background(), args); err != nil {
		t.Fatalf("rows the repository's router declares must not need the repair router: %v", err)
	}
	if plan := readRepairPlan(t, args); len(plan.Jobs) == 0 {
		t.Fatal("no repair job planned")
	}
}

// Negative: a repair router that does not declare the repair task plans an unroutable job, and
// a row the repository's own router does not declare is still refused.
func TestDogfoodRepairCLIRejectsRepairTaskMissingFromRepairRouter(t *testing.T) {
	args := repairCLIFixture(t)
	t.Chdir(t.TempDir())
	narrowRepairRouting(t, args[3], "function_docstrings")
	mustErrContain(t, runDogfoodRepairs(context.Background(), args), "blocked jobs")
	if plan := readRepairPlan(t, args); len(plan.Jobs) == 0 || plan.Jobs[0].Status != "unroutable" {
		t.Fatalf("repair task missing from the repair router: %+v", plan.Jobs)
	}

	args = repairCLIFixture(t)
	t.Chdir(t.TempDir())
	writeFixtureFile(t, ".", ".config/models/routing.yaml", readFixtureFile(t, filepath.Dir(args[3]), filepath.Base(args[3])))
	writeFixtureFile(t, ".", ".standards.yaml", "version: 1\nregister:\n  tasks:\n    deploy_prod: docs\n")
	narrowRepairRouting(t, args[3], "ci_debugging")
	mustErrContain(t, runDogfoodRepairs(context.Background(), args), `register task "deploy_prod" is not a declared target_tasks label`)
}
