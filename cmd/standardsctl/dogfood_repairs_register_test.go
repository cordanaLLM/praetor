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
