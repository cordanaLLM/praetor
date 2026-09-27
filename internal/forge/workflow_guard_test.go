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

// The guard rule decides on the same workflow subset the audits parse, so it reads
// workflowSpec (internal/forge/workflow_checks.go) rather than declaring a second copy of
// the `on`, `permissions` and per-job shape for the two of them to drift apart on.

// workflowGuardViolations reports every job of one workflow that may run outside the
// repository named identity although the workflow is scheduled or can publish on its own.
func workflowGuardViolations(name string, data []byte, identity string) ([]string, error) {
	var spec workflowSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("workflow %s: parse: %w", name, err)
	}
	if len(spec.Jobs) > maxJobsPerFile {
		return nil, fmt.Errorf("workflow %s exceeds %d jobs", name, maxJobsPerFile)
	}
	// sortedJobIDs (internal/forge/workflow_checks.go) is what both audits enumerate jobs
	// with; a second collect-and-sort here would be one more copy to drift.
	ids := sortedJobIDs(spec.Jobs)
	publishes := grantsWrite(&spec.Permissions)
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		permissions := spec.Jobs[ids[i]].Permissions
		publishes = publishes || grantsWrite(&permissions)
	}
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

// grantsWrite reports whether a permissions node grants any write scope. The scope shapes
// themselves are writeScopes' business (internal/forge/workflow_permissions.go); a second
// reading of the same node here would stop matching it the first time GitHub adds a
// permission spelling.
func grantsWrite(permissions *yaml.Node) bool {
	scopes, _ := writeScopes(permissions)
	return len(scopes) > 0
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

// hasTrigger reports whether a workflow declares the named event, through the same lookup
// the audits use.
func hasTrigger(on *yaml.Node, event string) bool {
	_, declared := eventTrigger(on, event)
	return declared
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
	for _, name := range []string{"sync-flavors.yml", "sync-models.yml", "pages.yml", "wiki-sync.yml", "release-binaries.yml", "security.yml"} {
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
	var spec workflowSpec
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
	overlaid := manifestWithRepository(t, "example-owner", "praetor", "cordanaLLM/praetor")
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
		dir := manifestWithRepository(t, "example-owner", "praetor", malformed)
		if _, err := manifestIdentity(dir); err == nil {
			t.Errorf("malformed source %q yielded an identity instead of failing closed", malformed)
		}
	}
}

// overlaidEngineRoot copies the engine's own checked-in workflow files -- unchanged, exactly
// as operational sync's owner overlay leaves them -- beside a manifest shaped the way
// internal/operationalsync's overlay() (owner: example-owner, source: cordanaLLM/praetor) leaves
// .standards.yaml. It reproduces issue #255's failure shape without touching the real fork
// checkout or any forge state.
func overlaidEngineRoot(t *testing.T) string {
	t.Helper()
	files, err := readWorkflowFiles(t.Context(), engineRoot)
	if err != nil {
		t.Fatalf("read engine workflows: %v", err)
	}
	root := manifestWithRepository(t, "example-owner", "praetor", "cordanaLLM/praetor")
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
// overlay applied (owner example-owner, source cordanaLLM/praetor), it must guard every scheduled
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

// The container image and the Helm chart are published under the repository identity too,
// but GHCR accepts lowercase names only, so the literal there is the lowercased identity
// rather than the guard's. It is written down in three files that must agree: the image
// .goreleaser.yaml pushes, the repository values.yaml pulls and the job environment the
// release workflow verifies and pushes the chart with. A drift between them publishes an
// image no install pulls, which is the ImagePullBackOff this rule exists for.

// ghcrImage is the GHCR image repository of identity.
func ghcrImage(identity string) string {
	return "ghcr.io/" + strings.ToLower(identity)
}

// ghcrChartRepository is the OCI repository the release job pushes the chart to: the
// charts namespace of the identity's owner, beside its image.
func ghcrChartRepository(identity string) string {
	owner, _, _ := strings.Cut(strings.ToLower(identity), "/")
	return "oci://ghcr.io/" + owner + "/charts"
}

// containerSources are the three files a container reference is written down in.
type containerSources struct {
	goreleaser, values, workflow []byte
}

// maxPushedImages bounds the dockers_v2 image scan (HISS-02).
const maxPushedImages = 16

// pushedImages lists every image name the dockers_v2 blocks of a GoReleaser configuration
// push.
func pushedImages(goreleaser []byte) ([]string, error) {
	var config struct {
		Dockers []struct {
			Images []string `yaml:"images"`
		} `yaml:"dockers_v2"`
	}
	if err := yaml.Unmarshal(goreleaser, &config); err != nil {
		return nil, fmt.Errorf("parse goreleaser: %w", err)
	}
	var images []string
	for i := 0; i < len(config.Dockers) && i < maxPushedImages; i++ {
		images = append(images, config.Dockers[i].Images...)
	}
	if len(images) > maxPushedImages {
		return nil, fmt.Errorf("goreleaser pushes more than %d images", maxPushedImages)
	}
	return images, nil
}

// jobEnvironment returns the value every job of a workflow sets for key, keyed by job ID.
func jobEnvironment(workflow []byte, key string) (map[string]string, error) {
	var spec struct {
		Jobs map[string]struct {
			Env map[string]string `yaml:"env"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(workflow, &spec); err != nil {
		return nil, fmt.Errorf("parse workflow: %w", err)
	}
	if len(spec.Jobs) > maxJobsPerFile {
		return nil, fmt.Errorf("workflow exceeds %d jobs", maxJobsPerFile)
	}
	values := make(map[string]string, len(spec.Jobs))
	for id, job := range spec.Jobs {
		if value, ok := job.Env[key]; ok {
			values[id] = value
		}
	}
	return values, nil
}

// referenceViolations reports each reference in got that differs from want, and reports the
// reference set itself when it is empty, so the rule cannot pass on a file that names none.
func referenceViolations(what, want string, got map[string]string) []string {
	if len(got) == 0 {
		return []string{fmt.Sprintf("%s: no reference found; want %s", what, want)}
	}
	keys := make([]string, 0, len(got))
	for key := range got {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var violations []string
	for i := 0; i < len(keys); i++ {
		if got[keys[i]] != want {
			violations = append(violations, fmt.Sprintf("%s %s = %q, want %q", what, keys[i], got[keys[i]], want))
		}
	}
	return violations
}

// containerReferenceViolations names every image or chart reference that is not the GHCR
// repository of identity.
func containerReferenceViolations(identity string, sources containerSources) ([]string, error) {
	images, err := pushedImages(sources.goreleaser)
	if err != nil {
		return nil, err
	}
	var values struct {
		Image struct {
			Repository string `yaml:"repository"`
		} `yaml:"image"`
	}
	if err := yaml.Unmarshal(sources.values, &values); err != nil {
		return nil, fmt.Errorf("parse values: %w", err)
	}
	imageEnv, err := jobEnvironment(sources.workflow, "IMAGE")
	if err != nil {
		return nil, err
	}
	chartEnv, err := jobEnvironment(sources.workflow, "CHART_REPOSITORY")
	if err != nil {
		return nil, err
	}
	pushed := make(map[string]string, len(images))
	for i := 0; i < len(images); i++ {
		pushed[fmt.Sprintf("[%d]", i)] = images[i]
	}
	image := ghcrImage(identity)
	violations := referenceViolations(".goreleaser.yaml dockers_v2 image", image, pushed)
	pulled := make(map[string]string, 1)
	if values.Image.Repository != "" {
		pulled["image.repository"] = values.Image.Repository
	}
	violations = append(violations, referenceViolations("values.yaml", image, pulled)...)
	violations = append(violations, referenceViolations("release job IMAGE", image, imageEnv)...)
	return append(violations, referenceViolations("release job CHART_REPOSITORY", ghcrChartRepository(identity), chartEnv)...), nil
}

// engineContainerSources reads the three shipped files a container reference lives in.
func engineContainerSources(t *testing.T) containerSources {
	t.Helper()
	workflows, _ := engineWorkflows(t)
	read := func(rel string) []byte {
		data, err := os.ReadFile(filepath.Join(engineRoot, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return data
	}
	return containerSources{
		goreleaser: read(".goreleaser.yaml"),
		values:     read("deploy/helm/praetor/values.yaml"),
		workflow:   workflows["release-binaries.yml"],
	}
}

// Positive: the image .goreleaser.yaml pushes, the repository the chart pulls and the release
// job's IMAGE and CHART_REPOSITORY are all the lowercased manifest identity.
func TestContainerReferencesFollowTheManifestIdentity(t *testing.T) {
	_, identity := engineWorkflows(t)
	violations, err := containerReferenceViolations(identity, engineContainerSources(t))
	if err != nil || len(violations) != 0 {
		t.Fatalf("container references: violations %v, err %v", violations, err)
	}
}

// Negative: judged against another identity every one of the four references fails, so each
// literal is tied to the manifest rather than merely present.
func TestContainerReferencesFailForAnotherIdentity(t *testing.T) {
	violations, err := containerReferenceViolations(forkIdentity, engineContainerSources(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, where := range []string{"dockers_v2 image", "values.yaml", "release job IMAGE", "release job CHART_REPOSITORY"} {
		if !strings.Contains(strings.Join(violations, "\n"), where) {
			t.Errorf("a reference in %s passed for %s: %v", where, forkIdentity, violations)
		}
	}
}

// Boundary: the mixed-case identity lowercases to the GHCR name, a configuration that pushes
// no image or a chart that names no repository fails rather than passing on nothing, and
// malformed input is an error.
func TestContainerReferenceViolationsBoundaries(t *testing.T) {
	if got := ghcrImage("cordanaLLM/praetor"); got != "ghcr.io/cordanallm/praetor" {
		t.Errorf("ghcrImage = %q", got)
	}
	if got := ghcrChartRepository("cordanaLLM/praetor"); got != "oci://ghcr.io/cordanallm/charts" {
		t.Errorf("ghcrChartRepository = %q", got)
	}
	workflow := []byte("jobs:\n  release:\n    env:\n      IMAGE: ghcr.io/acme/engine\n      CHART_REPOSITORY: oci://ghcr.io/acme/charts\n")
	goreleaser := []byte("dockers_v2:\n  - images: [ghcr.io/acme/engine]\n")
	values := []byte("image:\n  repository: ghcr.io/acme/engine\n")
	cases := []struct {
		name    string
		sources containerSources
		want    int
	}{
		{"all agree for a mixed-case identity", containerSources{goreleaser, values, workflow}, 0},
		{"no image pushed", containerSources{[]byte("builds: []\n"), values, workflow}, 1},
		{"chart names no repository", containerSources{goreleaser, []byte("replicaCount: 1\n"), workflow}, 1},
		{"workflow sets neither variable", containerSources{goreleaser, values, []byte("jobs:\n  release: {}\n")}, 2},
		{"uppercase image literal", containerSources{[]byte("dockers_v2:\n  - images: [ghcr.io/Acme/engine]\n"), values, workflow}, 1},
	}
	for _, tc := range cases {
		violations, err := containerReferenceViolations("Acme/engine", tc.sources)
		if err != nil || len(violations) != tc.want {
			t.Errorf("%s: violations %v, err %v, want %d", tc.name, violations, err, tc.want)
		}
	}
	if _, err := containerReferenceViolations("acme/engine", containerSources{[]byte("dockers_v2: [\n"), values, workflow}); err == nil {
		t.Error("malformed goreleaser configuration accepted")
	}
}

// docsAuditConditionGap names why a job does not run the Documentation Integrity Audit
// on run_docs, or returns "" when it does.
func docsAuditConditionGap(job workflowJob) string {
	for i := 0; i < len(job.Steps) && i < maxStepsPerJob; i++ {
		step := job.Steps[i]
		if step.Name == "Documentation Integrity Audit" {
			condition := strings.TrimSpace(step.If)
			if condition == "" {
				return "unconditional"
			}
			if condition != "steps.filter.outputs.run_docs == 'true' && github.event_name == 'pull_request'" {
				return "condition differs: " + condition
			}
			return ""
		}
	}
	return "no step named 'Documentation Integrity Audit'"
}

func TestVerifyJobRunsDocsAuditOnRunDocs(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	var spec workflowSpec
	if err := yaml.Unmarshal(workflows["ci.yml"], &spec); err != nil {
		t.Fatalf("parse ci.yml: %v", err)
	}
	verify := spec.Jobs["verify"]
	if gap := docsAuditConditionGap(verify); gap != "" {
		t.Fatalf("verify job: %s", gap)
	}

	cases := []struct {
		name string
		job  workflowJob
		want string
	}{
		{"positive", workflowJob{Steps: []workflowStep{{Name: "Documentation Integrity Audit", If: "steps.filter.outputs.run_docs == 'true' && github.event_name == 'pull_request'"}}}, ""},
		{"negative missing", workflowJob{Steps: []workflowStep{}}, "no step named"},
		{"negative condition", workflowJob{Steps: []workflowStep{{Name: "Documentation Integrity Audit", If: "steps.filter.outputs.docs_only == 'true' && github.event_name == 'pull_request'"}}}, "condition differs"},
		{"boundary unconditional", workflowJob{Steps: []workflowStep{{Name: "Documentation Integrity Audit", If: ""}}}, "unconditional"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := docsAuditConditionGap(tc.job)
			if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
				t.Fatalf("gap = %q, want containing %q", got, tc.want)
			}
		})
	}
}
