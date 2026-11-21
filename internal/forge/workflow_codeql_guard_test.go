package forge

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/util"
	"go.yaml.in/yaml/v3"
)

// TestCodeQLDispatchRefGuard checks job admission, executes the first step's exact-ref guard,
// and pins the triggers, permissions and concurrency that this guard must leave unchanged.
func TestCodeQLDispatchRefGuard(t *testing.T) {
	workflows, identity := engineWorkflows(t)
	spec, err := ghworkflow.Parse(workflows["codeql.yml"])
	if err != nil {
		t.Fatalf("parse codeql.yml: %v", err)
	}
	job, found := spec.Jobs["analyze"]
	if !found || strings.TrimSpace(job.If) == "" {
		t.Fatal("codeql.yml/analyze must declare an if condition")
	}
	checkCodeQLUnchangedFields(t, spec)
	checkCodeQLExactRefStep(t, job)
	for _, canonical := range []struct{ name, repository, variable string }{
		{"default_repository", identity, ""},
		{"configured_repository", forkIdentity, forkIdentity},
	} {
		t.Run(canonical.name, func(t *testing.T) {
			for _, repository := range []string{canonical.repository, "unrelated/praetor"} {
				t.Run(repository, func(t *testing.T) {
					checkCodeQLDispatchCases(t, job.If, repositoryGuard(identity), canonical.repository, repository, canonical.variable)
				})
			}
		})
	}
}

// Values from origin/main at 97bdd7d3faa289a62671780c61168e4462d5868c (2026-11-19).
// Decode nodes from the shared workflow reader; compare values without YAML layout or comments.
func checkCodeQLUnchangedFields(t *testing.T, spec ghworkflow.Spec) {
	t.Helper()
	job := spec.Jobs["analyze"]
	for name, field := range map[string]struct {
		node yaml.Node
		want any
	}{
		"triggers": {spec.On, map[string]any{
			"push":              map[string]any{"branches": []any{"main"}},
			"schedule":          []any{map[string]any{"cron": "23 3 * * 2"}},
			"workflow_dispatch": nil,
		}},
		"concurrency": {spec.Concurrency, map[string]any{
			"group":              "${{ github.workflow }}-${{ github.event.pull_request.number || github.run_id }}",
			"cancel-in-progress": "${{ github.event_name == 'pull_request' }}",
		}},
		"workflow permissions": {spec.Permissions, map[string]any{}},
		"job permissions": {job.Permissions, map[string]any{
			"contents": "read", "security-events": "write",
		}},
	} {
		t.Run(name, func(t *testing.T) {
			var got any
			if err := field.node.Decode(&got); err != nil {
				t.Fatalf("decode %s: %v", name, err)
			}
			if !reflect.DeepEqual(got, field.want) {
				t.Errorf("%s = %#v, want origin/main value %#v", name, got, field.want)
			}
		})
	}
}

// checkCodeQLExactRefStep runs the analyze job's first step for each event and ref case and checks which ones it refuses.
func checkCodeQLExactRefStep(t *testing.T, job ghworkflow.Job) {
	t.Helper()
	if len(job.Steps) == 0 {
		t.Fatal("codeql.yml/analyze must start with an exact-ref guard")
	}
	step := job.Steps[0]
	checkCodeQLRefStepOptions(t, step)
	for _, tc := range []struct {
		name, event, ref string
		want             int
	}{
		{"dispatch_main", "workflow_dispatch", "refs/heads/main", 0},
		{"dispatch_MAIN", "workflow_dispatch", "refs/heads/MAIN", 1},
		{"dispatch_Main", "workflow_dispatch", "refs/heads/Main", 1},
		{"dispatch_main_suffix", "workflow_dispatch", "refs/heads/main-x", 1},
		{"dispatch_feature", "workflow_dispatch", "refs/heads/feature", 1},
		{"dispatch_tag_named_main", "workflow_dispatch", "refs/tags/main", 1},
		{"push", "push", "refs/heads/MAIN", 0},
		{"schedule", "schedule", "refs/heads/Main", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			ctx, err := util.WithCommandEnvironment(ctx, []string{
				"GITHUB_EVENT_NAME=" + tc.event, "GITHUB_REF=" + tc.ref,
			})
			if err != nil {
				t.Fatal(err)
			}
			res, err := util.RunCommandBytes(ctx, t.TempDir(), adoptShell(t), util.MaxCommandOutputBytes,
				"--noprofile", "--norc", "-e", "-o", "pipefail", "-c", step.Run)
			if got := exitCodeOf(t, err); got != tc.want {
				t.Errorf("exact-ref guard exit = %d, want %d; stdout: %s; stderr: %s", got, tc.want, res.Stdout, res.Stderr)
			}
			output := string(append(res.Stdout, res.Stderr...))
			if tc.want != 0 && (strings.Count(output, "\n") != 1 || !strings.Contains(output, tc.ref)) {
				t.Errorf("refusal must name ref %q on one line; got %q", tc.ref, output)
			}
		})
	}
}

// checkCodeQLRefStepOptions checks that the first step is the named bash guard, runs a script and uses no action.
func checkCodeQLRefStepOptions(t *testing.T, step ghworkflow.Step) {
	t.Helper()
	if step.Name != "Refuse dispatch outside the exact main ref" || step.Run == "" || step.Uses != "" {
		t.Error("codeql.yml/analyze first step must run the exact-ref guard before checkout")
	}
	if step.If != "" || step.ContinueOnError != "" || step.Shell != "bash" || step.TimeoutMinutes.Value != "1" {
		t.Error("exact-ref guard must be unconditional, fail closed, use bash and time out after one minute")
	}
	if strings.Contains(step.Run, "${{") {
		t.Error("exact-ref guard must read environment variables without expression interpolation")
	}
}

// The planted repository-only guard must disagree with the same admission table on
// both non-main dispatches. The other repository must remain excluded for every case.
func checkCodeQLDispatchCases(t *testing.T, condition, planted, canonical, repository, variable string) {
	t.Helper()
	cases := []struct {
		name, event, ref string
		want             bool
	}{
		{"dispatch_main", "workflow_dispatch", "refs/heads/main", true},
		{"dispatch_feature", "workflow_dispatch", "refs/heads/feature", false},
		{"dispatch_tag_named_main", "workflow_dispatch", "refs/tags/main", false},
		{"push_main", "push", "refs/heads/main", true},
		{"schedule_main", "schedule", "refs/heads/main", true},
	}
	var plantedMismatches []string
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			facts := map[string]string{
				"github.event_name":                   tc.event,
				"github.ref":                          tc.ref,
				"github.repository":                   repository,
				"vars." + canonicalRepositoryVariable: variable,
			}
			want := repository == canonical && tc.want
			if got := codeQLConditionHolds(t, condition, facts); got != want {
				t.Errorf("codeql.yml/analyze admission = %v, want %v; condition: %s", got, want, condition)
			}
			if codeQLConditionHolds(t, planted, facts) != want {
				plantedMismatches = append(plantedMismatches, tc.name)
			}
		})
	}
	var wantMismatches []string
	if repository == canonical {
		wantMismatches = []string{"dispatch_feature", "dispatch_tag_named_main"}
	}
	if !slices.Equal(plantedMismatches, wantMismatches) {
		t.Errorf("repository-only guard admission mismatches = %v, want %v", plantedMismatches, wantMismatches)
	}
}

// Reuse workflow_expression.go's bounded tokenizer and evaluator. Its named tokens
// accept only inputs.*, so this test adapter binds context names at token boundaries;
// quoted literals still pass unchanged through nextExpressionToken.
func codeQLConditionHolds(t *testing.T, condition string, facts map[string]string) bool {
	t.Helper()
	var tokens []exprToken
	for range maxExprTokens {
		condition = strings.TrimSpace(condition)
		if condition == "" {
			value, err := evaluateTokens(tokens, nil)
			if err != nil {
				t.Fatalf("evaluate CodeQL condition: %v", err)
			}
			return value.isTruthy()
		}
		token, next, err := codeQLContextToken(condition, facts)
		if err != nil {
			t.Fatalf("tokenize CodeQL condition: %v", err)
		}
		tokens = append(tokens, token)
		condition = condition[next:]
	}
	t.Fatal("CodeQL condition exceeds expression token bound")
	return false
}

// codeQLContextToken binds a known context name (github.*, vars.*) at the start of condition to its fixture value
// as a string token; any other text goes to the repository's expression tokenizer.
func codeQLContextToken(condition string, facts map[string]string) (exprToken, int, error) {
	for name, value := range facts {
		if strings.HasPrefix(condition, name) {
			return exprToken{kind: tokString, val: value}, len(name), nil
		}
	}
	return nextExpressionToken(condition, 0)
}
