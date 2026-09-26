package forge

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	engineRoot          = "../.."
	forkIdentity        = "operator/praetor"
	portabilityVariable = "vars.PRAETOR_FORK_PORTABILITY == 'enabled'"
	scheduleLegPrefix   = "github.event_name != 'schedule' || "
)

// guardedWorkflow is the workflow subset the guard rule decides on. Permissions are nodes
// because a workflow may grant a scalar ("write-all") or a mapping.
type guardedWorkflow struct {
	On          yaml.Node `yaml:"on"`
	Permissions yaml.Node `yaml:"permissions"`
	Jobs        map[string]struct {
		If          string    `yaml:"if"`
		Permissions yaml.Node `yaml:"permissions"`
	} `yaml:"jobs"`
}

// workflowGuardViolations reports every job of one workflow that may run outside the
// repository named identity although the workflow is scheduled or can publish on its own.
func workflowGuardViolations(name string, data []byte, identity string) ([]string, error) {
	var spec guardedWorkflow
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("workflow %s: parse: %w", name, err)
	}
	if len(spec.Jobs) > maxJobsPerFile {
		return nil, fmt.Errorf("workflow %s exceeds %d jobs", name, maxJobsPerFile)
	}
	publishes := grantsWrite(&spec.Permissions)
	ids := make([]string, 0, len(spec.Jobs))
	for id := range spec.Jobs {
		ids = append(ids, id)
		permissions := spec.Jobs[id].Permissions
		publishes = publishes || grantsWrite(&permissions)
	}
	sort.Strings(ids)
	engineOnly := startsUnattended(&spec.On) && (publishes || hasTrigger(&spec.On, "schedule"))
	var violations []string
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		condition := spec.Jobs[ids[i]].If
		if strings.Count(condition, "github.repository ==") != strings.Count(condition, repositoryGuard(identity)) {
			violations = append(violations, fmt.Sprintf("%s: job %s: guard differs from the manifest identity %s", name, ids[i], identity))
		}
		if engineOnly && !confinesToRepository(condition, identity, publishes) {
			violations = append(violations, fmt.Sprintf("%s: job %s: scheduled or publishing job has no repository guard", name, ids[i]))
		}
	}
	return violations, nil
}

// confinesToRepository reports whether a condition keeps every unattended run inside the
// repository: the guard itself, the guard as a further conjunct, or the schedule-leg form
// that lets push and pull request verification run everywhere. A workflow that can publish
// never gets the schedule-leg form: its push leg would publish from any copy.
func confinesToRepository(condition, identity string, publishes bool) bool {
	guard := repositoryGuard(identity)
	condition = strings.TrimSpace(condition)
	if condition == guard || strings.HasSuffix(condition, " && "+guard) {
		return true
	}
	return !publishes && condition == scheduleLegPrefix+guard
}

// grantsWrite reports whether a permissions node grants any write scope.
func grantsWrite(permissions *yaml.Node) bool {
	if permissions.Kind == yaml.ScalarNode {
		return permissions.Value == "write-all"
	}
	for i := 1; i < len(permissions.Content) && i < 2*maxJobsPerFile; i += 2 {
		if permissions.Content[i].Value == "write" {
			return true
		}
	}
	return false
}

// startsUnattended reports whether any trigger fires without an operator asking for it.
// adopt.yml is the counter-example: a dispatch or a comment is a person's decision in the
// repository it is made in, so it runs wherever it is asked to.
func startsUnattended(on *yaml.Node) bool {
	attended := map[string]bool{"workflow_dispatch": true, "issue_comment": true, "workflow_call": true}
	triggers := triggerNames(on)
	for i := 0; i < len(triggers) && i < maxJobsPerFile; i++ {
		if !attended[triggers[i]] {
			return true
		}
	}
	return false
}

func hasTrigger(on *yaml.Node, event string) bool {
	triggers := triggerNames(on)
	for i := 0; i < len(triggers) && i < maxJobsPerFile; i++ {
		if triggers[i] == event {
			return true
		}
	}
	return false
}

// triggerNames lists the events of an "on" node in its scalar, sequence or mapping form.
func triggerNames(on *yaml.Node) []string {
	if on.Kind == yaml.ScalarNode {
		return []string{on.Value}
	}
	step := 1
	if on.Kind == yaml.MappingNode {
		step = 2
	}
	var names []string
	for i := 0; i < len(on.Content) && i < 2*maxJobsPerFile; i += step {
		names = append(names, on.Content[i].Value)
	}
	return names
}

func engineWorkflows(t *testing.T) (map[string][]byte, string) {
	t.Helper()
	files, err := readWorkflowFiles(t.Context(), engineRoot)
	if err != nil {
		t.Fatalf("read engine workflows: %v", err)
	}
	identity, err := manifestIdentity(engineRoot)
	if err != nil || identity == "" {
		t.Fatalf("engine manifest identity = %q, %v", identity, err)
	}
	byName := make(map[string][]byte, len(files))
	for i := 0; i < len(files) && i < maxWorkflowFiles; i++ {
		byName[files[i].Name] = files[i].Data
	}
	return byName, identity
}

// Positive: with the manifest identity every scheduled or publishing job in the real
// workflow files is confined, and the literal in each guard equals .standards.yaml.
func TestEngineWorkflowsGuardEveryScheduledOrPublishingJob(t *testing.T) {
	workflows, identity := engineWorkflows(t)
	if len(workflows) < 11 {
		t.Fatalf("expected the eleven engine workflows, read %d", len(workflows))
	}
	guarded := 0
	for name, data := range workflows {
		violations, err := workflowGuardViolations(name, data, identity)
		if err != nil || len(violations) != 0 {
			t.Errorf("%s: violations %v, err %v", name, violations, err)
		}
		guarded += strings.Count(string(data), repositoryGuard(identity))
	}
	if guarded == 0 {
		t.Fatal("no workflow carries the repository guard; the rule passed on nothing")
	}
}

// Negative: the same files judged against another identity fail, so the literal is tied to
// the manifest rather than merely present.
func TestEngineWorkflowGuardsFailForAnotherIdentity(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	for _, name := range []string{"sync-flavors.yml", "sync-models.yml", "pages.yml", "wiki-sync.yml", "release-binaries.yml", "sbom.yml", "security.yml"} {
		violations, err := workflowGuardViolations(name, workflows[name], forkIdentity)
		if err != nil || len(violations) == 0 {
			t.Errorf("%s: a guard for another repository passed (violations %v, err %v)", name, violations, err)
		}
	}
}

// Verification follows the code: these run in every copy and must never gain a guard.
func TestVerificationWorkflowsStayUnguarded(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	for _, name := range []string{"ci.yml", "compliance.yml", "adopt.yml"} {
		if data, ok := workflows[name]; !ok || strings.Contains(string(data), "github.repository") {
			t.Errorf("%s: missing, or carries a repository guard although it runs everywhere", name)
		}
	}
}

func TestWorkflowGuardViolationsSyntheticShapes(t *testing.T) {
	guard := repositoryGuard("acme/engine")
	const head = "permissions:\n  contents: %s\njobs:\n  job:\n    runs-on: ubuntu-latest\n%s"
	cases := []struct {
		name, on, permission, condition string
		want                            int
	}{
		{"guarded publisher", "on: [push]", "write", "    if: " + guard + "\n", 0},
		{"guard as last conjunct", "on: [push]", "write", "    if: github.ref == 'refs/heads/main' && " + guard + "\n", 0},
		{"schedule leg form", "on:\n  push:\n  schedule:\n    - cron: '0 4 * * *'", "read", "    if: " + scheduleLegPrefix + guard + "\n", 0},
		{"read-only push verification", "on: [push, pull_request]", "read", "", 0},
		{"dispatch-only writer", "on:\n  workflow_dispatch:\n  issue_comment:", "write", "", 0},
		{"schedule leg form on a publisher", "on:\n  push:\n  schedule:\n    - cron: '0 4 * * *'", "write", "    if: " + scheduleLegPrefix + guard + "\n", 1},
		{"unguarded publisher", "on: push", "write", "", 1},
		{"unguarded schedule", "on:\n  schedule:\n    - cron: '0 3 * * *'", "read", "", 1},
		{"negated guard", "on: [push]", "write", "    if: \"!(" + guard + ")\"\n", 1},
		{"guard that an alternative bypasses", "on: [push]", "write", "    if: " + guard + " || github.actor == 'someone'\n", 1},
		{"bare literal without the variable", "on: [push]", "write", "    if: github.repository == 'acme/engine'\n", 2},
		{"literal of another repository", "on: [push]", "write", "    if: " + repositoryGuard("other/engine") + "\n", 2},
	}
	for _, tc := range cases {
		data := []byte(tc.on + "\n" + fmt.Sprintf(head, tc.permission, tc.condition))
		violations, err := workflowGuardViolations(tc.name, data, "acme/engine")
		if err != nil || len(violations) != tc.want {
			t.Errorf("%s: violations %v, err %v, want %d", tc.name, violations, err, tc.want)
		}
	}
}

func TestWorkflowGuardViolationsBounds(t *testing.T) {
	build := func(jobs int) []byte {
		var b strings.Builder
		b.WriteString("on: [push]\njobs:\n")
		for i := 0; i < jobs && i <= maxJobsPerFile; i++ {
			fmt.Fprintf(&b, "  job%d:\n    runs-on: ubuntu-latest\n", i)
		}
		return []byte(b.String())
	}
	if _, err := workflowGuardViolations("limit", build(maxJobsPerFile), "acme/engine"); err != nil {
		t.Fatalf("%d jobs: %v", maxJobsPerFile, err)
	}
	if _, err := workflowGuardViolations("over", build(maxJobsPerFile+1), "acme/engine"); err == nil {
		t.Fatalf("%d jobs accepted", maxJobsPerFile+1)
	}
	if _, err := workflowGuardViolations("broken", []byte("jobs: [\n"), "acme/engine"); err == nil {
		t.Fatal("malformed workflow accepted")
	}
}

// Boundary: portability outside the canonical repository follows a repository variable,
// and exactly one of the matrix and its stated-reason job runs for any repository.
func TestPortabilityFollowsTheRepositoryVariable(t *testing.T) {
	workflows, identity := engineWorkflows(t)
	var spec guardedWorkflow
	if err := yaml.Unmarshal(workflows["portability.yml"], &spec); err != nil {
		t.Fatalf("parse portability.yml: %v", err)
	}
	runs := repositoryGuard(identity) + " || " + portabilityVariable
	if got := spec.Jobs["harness"].If; got != runs {
		t.Errorf("harness condition = %q, want %q", got, runs)
	}
	if got := spec.Jobs["skipped"].If; got != "!("+runs+")" {
		t.Errorf("skipped condition = %q, want the exact negation of the harness condition", got)
	}
}

const markdownGateSelfTest = "node tools/markdownlint/verify.mjs --self-test"

// markdownGateSelfTestGap names why a job does not replay the Markdown gate self-test on
// every leg, or returns "" when it does. A step skipped by an OS condition would restore
// the silent Windows gap HISS-21 forbids, so only unconditional steps count.
func markdownGateSelfTestGap(job workflowJob) string {
	if advisoryJob(job.ContinueOnError) {
		return "job is advisory"
	}
	setupNode := false
	for i := 0; i < len(job.Steps) && i < maxStepsPerJob; i++ {
		step := job.Steps[i]
		if strings.HasPrefix(step.Uses, "actions/setup-node@") && runsOnEveryLeg(step) {
			setupNode = true
		}
		if strings.TrimSpace(step.Run) != markdownGateSelfTest {
			continue
		}
		switch {
		case !setupNode:
			return "self-test runs before an unconditional setup-node step"
		case !runsOnEveryLeg(step):
			return "self-test step is conditional: " + step.If
		}
		return ""
	}
	return "no step runs " + markdownGateSelfTest
}

// runsOnEveryLeg reports whether a step's condition cannot differ between matrix legs.
func runsOnEveryLeg(step workflowStep) bool {
	condition := strings.TrimSpace(step.If)
	return condition == "" || condition == "${{ !cancelled() }}"
}

// The Markdown gate runner is emitted into adopters' verify-all, and only the portability
// matrix runs it on Windows. Positive: the real harness job replays it on every leg.
// Negative and boundary: a missing step, a step before Node exists, or an OS-guarded step.
func TestPortabilityReplaysMarkdownGateSelfTestOnEveryLeg(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	var spec workflowSpec
	if err := yaml.Unmarshal(workflows["portability.yml"], &spec); err != nil {
		t.Fatalf("parse portability.yml: %v", err)
	}
	harness := spec.Jobs["harness"]
	if gap := markdownGateSelfTestGap(harness); gap != "" {
		t.Fatalf("portability harness: %s", gap)
	}
	// The leg is identified by its image family, not by one label: the matrix names an
	// explicit image (windows-2025), never the windows-latest alias, and the next image
	// bump must not read as the Windows leg disappearing.
	legs := make([]string, 0, len(harness.Strategy.Matrix.Include))
	windows := false
	for i := 0; i < len(harness.Strategy.Matrix.Include) && i < maxMatrixLegs; i++ {
		leg := harness.Strategy.Matrix.Include[i]["os"]
		legs = append(legs, leg)
		windows = windows || strings.HasPrefix(leg, "windows-")
	}
	if !windows {
		t.Fatalf("portability harness legs = %v, want a Windows leg", legs)
	}
	node := workflowStep{Uses: "actions/setup-node@v4", If: "${{ !cancelled() }}"}
	selfTest := workflowStep{Run: markdownGateSelfTest + "\n", If: "${{ !cancelled() }}"}
	cases := []struct {
		name string
		job  workflowJob
		want string
	}{
		{"positive", workflowJob{Steps: []workflowStep{node, selfTest}}, ""},
		{"negative missing", workflowJob{Steps: []workflowStep{node}}, "no step runs"},
		{"boundary order", workflowJob{Steps: []workflowStep{selfTest, node}}, "before an unconditional setup-node"},
		{"boundary conditional node", workflowJob{Steps: []workflowStep{
			{Uses: node.Uses, If: "runner.os != 'Windows'"}, selfTest}}, "before an unconditional setup-node"},
		{"boundary os guard", workflowJob{Steps: []workflowStep{
			node, {Run: markdownGateSelfTest, If: "runner.os != 'Windows'"}}}, "conditional"},
		{"boundary advisory", workflowJob{ContinueOnError: "true", Steps: []workflowStep{node, selfTest}}, "advisory"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := markdownGateSelfTestGap(tc.job)
			if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
				t.Fatalf("gap = %q, want containing %q", got, tc.want)
			}
		})
	}
}

// The guards must not cost the canonical repository its required checks, and a fork must
// not be told to require checks its own runs will never report.
func TestRepositoryGuardKeepsRequiredContextsOnlyWhereItHolds(t *testing.T) {
	canonical, err := RequiredStatusContexts(t.Context(), engineRoot)
	if err != nil {
		t.Fatalf("RequiredStatusContexts: %v", err)
	}
	want := []string{"Platform Neutrality (Linux)", "Platform Neutrality (macOS)", "Platform Neutrality (Windows)", "Go Vulnerability & AST Security Scan"}
	joined := "\n" + strings.Join(canonical, "\n") + "\n"
	for _, context := range want {
		if !strings.Contains(joined, "\n"+context+"\n") {
			t.Errorf("canonical repository lost required context %q: %v", context, canonical)
		}
	}
	if strings.Contains(joined, "skipped") {
		t.Errorf("the stated-reason job became a required context: %v", canonical)
	}
	workflows, identity := engineWorkflows(t)
	for _, other := range []string{"", forkIdentity} {
		contexts, err := workflowContextsIn(workflows["portability.yml"], other)
		if err != nil || len(contexts) != 0 {
			t.Errorf("identity %q: portability contexts %v, err %v; want none", other, contexts, err)
		}
	}
	if contexts, err := workflowContextsIn(workflows["portability.yml"], identity); err != nil || len(contexts) != 3 {
		t.Errorf("canonical portability contexts = %v, %v", contexts, err)
	}
}

func TestGuardHoldsInRepository(t *testing.T) {
	guard := repositoryGuard("acme/engine")
	cases := []struct {
		condition, identity string
		want                bool
	}{
		{guard, "acme/engine", true},
		{"  " + scheduleLegPrefix + guard + "  ", "acme/engine", true},
		{guard + " || " + portabilityVariable, "acme/engine", true},
		{guard, "operator/engine", false},
		{guard, "", false},
		{"", "acme/engine", false},
		{"github.ref == 'refs/heads/main' && " + guard, "acme/engine", false},
		{"!(" + guard + ")", "acme/engine", false},
		{"github.repository == 'acme/engine'", "acme/engine", false},
	}
	for _, tc := range cases {
		if got := guardHoldsInRepository(tc.condition, tc.identity); got != tc.want {
			t.Errorf("guardHoldsInRepository(%q, %q) = %v, want %v", tc.condition, tc.identity, got, tc.want)
		}
	}
}

func TestManifestIdentity(t *testing.T) {
	if identity, err := manifestIdentity(engineRoot); err != nil || identity != "cordanaLLM/praetor" {
		t.Errorf("engine identity = %q, %v", identity, err)
	}
	empty := t.TempDir()
	if identity, err := manifestIdentity(empty); err != nil || identity != "" {
		t.Errorf("repository without a manifest: %q, %v", identity, err)
	}
	if identity, err := guardIdentity([]workflowFile{{Name: "ci.yml", Data: []byte("on: push\n")}}, filepath.Join(empty, "absent")); err != nil || identity != "" {
		t.Errorf("unguarded workflows must not read the manifest: %q, %v", identity, err)
	}
	if err := os.WriteFile(filepath.Join(empty, manifestFileName), []byte("repository: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manifestIdentity(empty); err == nil {
		t.Error("malformed manifest yielded an identity")
	}
	guarded := []workflowFile{{Name: "sync.yml", Data: []byte("if: " + repositoryGuard("acme/engine"))}}
	if _, err := guardIdentity(guarded, empty); err == nil {
		t.Error("guarded workflows beside a malformed manifest must fail closed")
	}
}

// manifestWithRepository writes a minimal manifest carrying only the repository fields the
// case supplies, omitting a blank source rather than writing an empty key.
func manifestWithRepository(t *testing.T, owner, name, source string) string {
	t.Helper()
	dir := t.TempDir()
	body := "version: 1\nrepository:\n  owner: \"" + owner + "\"\n  name: \"" + name + "\"\n  visibility: \"private\"\n"
	if source != "" {
		body += "  source: \"" + source + "\"\n"
	}
	if err := os.WriteFile(filepath.Join(dir, manifestFileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Positive, negative and boundary coverage for the repository.source precedence #255 adds:
// an operational fork's overlaid manifest resolves to the public source, a manifest that
// predates the field is unaffected, a source equal to owner/name still resolves (redundant,
// not wrong), and a source that is not an owner/name pair fails closed rather than silently
// falling back to the fork's own owner/name.
func TestManifestIdentityPrefersRepositorySource(t *testing.T) {
	overlaid := manifestWithRepository(t, "lusoris", "praetor", "cordanaLLM/praetor")
	if identity, err := manifestIdentity(overlaid); err != nil || identity != "cordanaLLM/praetor" {
		t.Errorf("overlaid manifest identity = %q, %v, want cordanaLLM/praetor", identity, err)
	}
	canonical := manifestWithRepository(t, "cordanaLLM", "praetor", "")
	if identity, err := manifestIdentity(canonical); err != nil || identity != "cordanaLLM/praetor" {
		t.Errorf("manifest without source = %q, %v, want owner/name unchanged", identity, err)
	}
	redundant := manifestWithRepository(t, "cordanaLLM", "praetor", "cordanaLLM/praetor")
	if identity, err := manifestIdentity(redundant); err != nil || identity != "cordanaLLM/praetor" {
		t.Errorf("source equal to owner/name = %q, %v, want cordanaLLM/praetor", identity, err)
	}
	for _, malformed := range []string{"not-an-identity", "cordanaLLM/praetor/extra", "/praetor", "cordanaLLM/"} {
		dir := manifestWithRepository(t, "lusoris", "praetor", malformed)
		if _, err := manifestIdentity(dir); err == nil {
			t.Errorf("malformed source %q yielded an identity instead of failing closed", malformed)
		}
	}
}

// overlaidEngineRoot copies the engine's own checked-in workflow files -- unchanged, exactly
// as operational sync's owner overlay leaves them -- beside a manifest shaped the way
// internal/operationalsync's overlay() (owner: lusoris, source: cordanaLLM/praetor) leaves
// .standards.yaml. It reproduces issue #255's failure shape without touching the real fork
// checkout or any forge state.
func overlaidEngineRoot(t *testing.T) string {
	t.Helper()
	files, err := readWorkflowFiles(t.Context(), engineRoot)
	if err != nil {
		t.Fatalf("read engine workflows: %v", err)
	}
	root := manifestWithRepository(t, "lusoris", "praetor", "cordanaLLM/praetor")
	workflows := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(workflows, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(files) && i < maxWorkflowFiles; i++ {
		if err := os.WriteFile(filepath.Join(workflows, files[i].Name), files[i].Data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestOverlaidManifestKeepsEngineGuardsAndRequiredContexts is the guard+ruleset regression
// issue #255 asks for: run against a temp copy of the repository manifest with the owner
// overlay applied (owner lusoris, source cordanaLLM/praetor), it must guard every scheduled
// or publishing job exactly as the canonical repository does, and it must derive the same
// required status contexts -- so a fork's own pushed-checks.sh gate, and internal/adopt's
// generated ruleset, do not drift from the checked-in one just because the fork carries the
// overlay.
func TestOverlaidManifestKeepsEngineGuardsAndRequiredContexts(t *testing.T) {
	root := overlaidEngineRoot(t)
	identity, err := manifestIdentity(root)
	if err != nil || identity != "cordanaLLM/praetor" {
		t.Fatalf("overlaid manifest identity = %q, %v", identity, err)
	}
	workflows, _ := engineWorkflows(t)
	for name, data := range workflows {
		violations, err := workflowGuardViolations(name, data, identity)
		if err != nil || len(violations) != 0 {
			t.Errorf("%s: violations %v, err %v", name, violations, err)
		}
	}
	overlaidContexts, err := RequiredStatusContexts(t.Context(), root)
	if err != nil {
		t.Fatalf("RequiredStatusContexts(overlaid): %v", err)
	}
	canonicalContexts, err := RequiredStatusContexts(t.Context(), engineRoot)
	if err != nil {
		t.Fatalf("RequiredStatusContexts(canonical): %v", err)
	}
	sort.Strings(overlaidContexts)
	sort.Strings(canonicalContexts)
	if strings.Join(overlaidContexts, "\n") != strings.Join(canonicalContexts, "\n") {
		t.Fatalf("required contexts drifted under the overlay:\noverlaid:  %v\ncanonical: %v", overlaidContexts, canonicalContexts)
	}
}
