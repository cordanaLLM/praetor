// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
	"go.yaml.in/yaml/v3"
)

func smokeWorkflowSteps(t *testing.T) []workflowStep {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release-smoke.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var spec workflowSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	return spec.Jobs["smoke"].Steps
}

func engineResolver(t *testing.T) string {
	t.Helper()
	steps := smokeWorkflowSteps(t)
	for i := 0; i < len(steps); i++ {
		if steps[i].ID == "engine" {
			return steps[i].Run
		}
	}
	t.Fatal("engine resolver step missing")
	return ""
}

func TestReleaseSmokeWorkflowPinnedSource(t *testing.T) {
	sha := strings.Repeat("a", 40)
	other := strings.Repeat("b", 40)
	job := `{"workflow_sha":"` + sha + `","workflow_repository":"acme/engine"}`
	cases := []struct {
		name, ref, job, want string
		fail                 bool
	}{
		{"own commit", "", job, sha, false},
		{"explicit commit", other, job, other, false},
		{"mutable branch", "main", job, "", true},
		{"unknown identity", "", "{}", "", true},
	}
	for i := 0; i < len(cases); i++ {
		t.Run(cases[i].name, func(t *testing.T) {
			c := cases[i]
			output := filepath.Join(t.TempDir(), "outputs")
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			env := []string{"PATH=" + os.Getenv("PATH"), "PRAETOR_REF=" + c.ref, "WORKFLOW_JOB=" + c.job, "GITHUB_OUTPUT=" + output}
			cmdCtx, envErr := util.WithCommandEnvironment(ctx, env)
			if envErr != nil {
				t.Fatal(envErr)
			}
			res, err := util.RunCommandBytes(cmdCtx, t.TempDir(), "python3", util.MaxCommandOutputBytes, "-c", engineResolver(t))
			if (err != nil) != c.fail {
				t.Fatalf("error %v: %s%s", err, res.Stdout, res.Stderr)
			}
			if c.fail {
				return
			}
			data, err := os.ReadFile(output)
			if err != nil || !strings.Contains(string(data), "ref="+c.want) || !strings.Contains(string(data), "repository=acme/engine") {
				t.Fatalf("output %s, error %v", data, err)
			}
		})
	}
}
