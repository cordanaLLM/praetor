package agenthook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/router"
)

const briefRoutingTemplate = `version: 1
tiers:
  only:
    description: "single tier"
    target_tasks: [%s]
    models:
      - {id: "m", family: "local", rpm_limit: 1, tpm_limit: 1, cost_per_m_in: 0, cost_per_m_out: 0}
    fallback_tier: ""
governance:
  max_concurrent_same_model: 1
  exhaustion_threshold_percent: 90
  orthogonal_audit_required: false
`

func writeBriefFixture(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeBriefRouting(t *testing.T, root string, labels ...string) {
	t.Helper()
	quoted := make([]string, len(labels))
	for index, label := range labels {
		quoted[index] = `"` + label + `"`
	}
	writeBriefFixture(t, root, ".config/models/routing.yaml",
		strings.Replace(briefRoutingTemplate, "%s", strings.Join(quoted, ", "), 1))
}

func briefFor(task, inputs string) string {
	return "goal: patch hook\ninputs: " + inputs + "\nreturn: diff plus tests\nevidence: focused tests\ntask: " + task + "\n"
}

func digestOf(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

// A documented brief resolves through the digest-bound authority: the resolution names the
// exact manifest bytes, so ValidateEmission and the stored return contract accept it.
func TestValidateAgentBriefBindsTheManifestDigest(t *testing.T) {
	ctx := context.Background()
	governed := repository(t, true)
	resolution, err := validateAgentBrief(ctx, governed, validBrief)
	if err != nil {
		t.Fatalf("documented brief refused: %v", err)
	}
	want := config.Resolution{Register: config.TextRegisterInternal, Source: "surfaces.agent", ManifestSHA256: digestOf("version: 1\n")}
	if resolution != want {
		t.Fatalf("resolution = %+v, want %+v", resolution, want)
	}

	ungoverned := repository(t, false)
	resolution, err = validateAgentBrief(ctx, ungoverned, validBrief)
	if err != nil || resolution.ManifestSHA256 != digestOf("") {
		t.Fatalf("absent manifest must bind SHA-256(empty): %+v, %v", resolution, err)
	}

	custom := repository(t, false)
	manifest := "version: 1\nregister:\n  tasks:\n    deploy_prod: {register: internal, max_tokens: 512}\n"
	writeBriefRouting(t, custom, "deploy_prod")
	writeBriefFixture(t, custom, manifestName, manifest)
	resolution, err = validateAgentBrief(ctx, custom, briefFor("deploy_prod", "internal/agenthook"))
	want = config.Resolution{Register: config.TextRegisterInternal, MaxTokens: 512, Source: "tasks.deploy_prod", ManifestSHA256: digestOf(manifest)}
	if err != nil || resolution != want {
		t.Fatalf("task row resolution = %+v, %v; want %+v", resolution, err, want)
	}
}

func TestValidateAgentBriefRefusesUndocumentedBriefs(t *testing.T) {
	cases := map[string]struct {
		setup func(t *testing.T, root string)
		brief string
		want  string
	}{
		"undeclared task": {
			setup: func(t *testing.T, root string) { writeBriefRouting(t, root, "deploy_prod") },
			brief: validBrief, want: `task "feature_implementation" is not declared by routing`,
		},
		"missing task field": {
			brief: "goal: patch hook\ninputs: internal/agenthook\nreturn: diff\nevidence: tests\n",
			want:  "brief task field is missing",
		},
		"duplicate task field": {
			brief: validBrief + "task: ci_debugging\n", want: "brief task field is duplicated",
		},
		"invalid manifest": {
			setup: func(t *testing.T, root string) {
				writeBriefFixture(t, root, manifestName, "version: 1\nregsiter: {}\n")
			},
			brief: validBrief, want: "load register policy",
		},
		"manifest row outside routing": {
			setup: func(t *testing.T, root string) {
				writeBriefRouting(t, root, "feature_implementation")
				writeBriefFixture(t, root, manifestName, "version: 1\nregister:\n  tasks:\n    deploy_prod: docs\n")
			},
			brief: validBrief, want: `"deploy_prod"`,
		},
		"broken routing": {
			setup: func(t *testing.T, root string) {
				writeBriefFixture(t, root, ".config/models/routing.yaml", "tiers: [\n")
			},
			brief: validBrief, want: "load register policy",
		},
		"prose brief": {
			brief: strings.Replace(validBrief, "goal: patch hook", "goal: I will patch the hook", 1),
			want:  "Caveman contract rejected",
		},
		"missing required field": {
			brief: strings.Replace(validBrief, "evidence: focused tests\n", "", 1),
			want:  "Caveman contract rejected",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			root := repository(t, true)
			if tc.setup != nil {
				tc.setup(t, root)
			}
			resolution, err := validateAgentBrief(t.Context(), root, tc.brief)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if resolution != (config.Resolution{}) {
				t.Fatalf("refused brief leaked a resolution: %+v", resolution)
			}
		})
	}
}

// An undeclared brief is refused at the native boundary and reserves no correlation.
func TestClaudeUndeclaredBriefReservesNothing(t *testing.T) {
	root, state := repository(t, true), t.TempDir()
	writeBriefRouting(t, root, "deploy_prod")
	pre := nativePayload(t, "PreToolUse", "session", "Agent", "tool",
		map[string]any{"prompt": validBrief, "run_in_background": true}, nil)
	response := runAgentHook(t, root, state, "claude", EventPreDispatch, pre)
	if response.ExitCode != 2 || !strings.Contains(string(response.Stderr), "not declared by routing") {
		t.Fatalf("undeclared brief accepted: %+v", response)
	}
	if entries, err := os.ReadDir(state); err != nil || len(entries) != 0 {
		t.Fatalf("refused brief reserved correlation: entries=%v err=%v", entries, err)
	}
}

func TestValidateAgentBriefLabelBoundary(t *testing.T) {
	ctx := context.Background()
	longest := strings.Repeat("x", router.MaxTaskLabelBytes)
	root := repository(t, true)
	writeBriefRouting(t, root, longest)
	if _, err := validateAgentBrief(ctx, root, briefFor(longest, "internal/agenthook")); err != nil {
		t.Fatalf("%d-byte declared label refused: %v", len(longest), err)
	}
	tooLong := longest + "x"
	if _, err := validateAgentBrief(ctx, root, briefFor(tooLong, "internal/agenthook")); err == nil ||
		!strings.Contains(err.Error(), "invalid task label") {
		t.Fatalf("%d-byte label accepted: %v", len(tooLong), err)
	}
}

// The resolved task budget is enforced on the brief itself: at the ceiling passes, one
// input line over is refused by the token ceiling.
func TestValidateAgentBriefTokenBoundary(t *testing.T) {
	ctx := context.Background()
	root := repository(t, false)
	writeBriefRouting(t, root, "deploy_prod")
	writeBriefFixture(t, root, manifestName, "version: 1\nregister:\n  tasks:\n    deploy_prod: {register: internal, max_tokens: 256}\n")
	// One input path per list line keeps every sentence short, so only the budget decides.
	inputs := func(count int) string {
		return briefFor("deploy_prod", "internal/agenthook"+strings.Repeat("\n- p.go", count))
	}
	count := 0
	for count < 256 && caveman.EstimateTokens(inputs(count+1)) <= 256 {
		count++
	}
	under, over := inputs(count), inputs(count+1)
	if caveman.EstimateTokens(under) > 256 || caveman.EstimateTokens(over) <= 256 {
		t.Fatalf("fixture misses the boundary: under=%d over=%d", caveman.EstimateTokens(under), caveman.EstimateTokens(over))
	}
	if _, err := validateAgentBrief(ctx, root, under); err != nil {
		t.Fatalf("brief at the %d-token ceiling refused: %v", caveman.EstimateTokens(under), err)
	}
	if count == 0 {
		t.Fatal("fixture never added an input line")
	}
	if _, err := validateAgentBrief(ctx, root, over); err == nil || !strings.Contains(err.Error(), caveman.RuleTokenCeiling) {
		t.Fatalf("brief over the ceiling accepted: %v", err)
	}
}
