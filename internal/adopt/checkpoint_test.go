package adopt

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// checkpointFixtureTexts are stand-in texts for every file of the checkpoint bundle.
var checkpointFixtureTexts = map[string]string{
	checkpointScript:   "#!/usr/bin/env python3\nprint('shared')\n",
	checkpointCommon:   "class HookError(Exception):\n    pass\n",
	checkpointLauncher: "#!/bin/sh\nexec python3 \"$@\"\n",
}

// writeCheckpointBundle writes a stand-in for every file of the checkpoint bundle under root.
func writeCheckpointBundle(t *testing.T, root string) {
	t.Helper()
	for _, name := range checkpointBundle {
		mustWrite(t, filepath.Join(root, filepath.FromSlash(name)), checkpointFixtureTexts[name])
	}
}

func checkpointSourceFixture(t *testing.T, complete bool) string {
	t.Helper()
	root := t.TempDir()
	if complete {
		writeCheckpointBundle(t, root)
	}
	return root
}

func checkpointSession(t *testing.T, source string) *adoptSession {
	t.Helper()
	return &adoptSession{repoPath: newTestRepo(t, "checkpoint-adoption"), repoName: "fixture",
		identity: repoIdentity{owner: "acme", name: "widget"}, opts: AdoptOptions{LockSourceRoot: source}, report: &AdoptReport{}}
}

// TestCheckpointPolicyNamesTheResolvedRepository pins BUG-852 for the checkpoint policy: the
// repository is the resolved identity, never the checkout directory or a default owner, and
// an unresolved identity installs no part of the lifecycle.
func TestCheckpointPolicyNamesTheResolvedRepository(t *testing.T) {
	session := checkpointSession(t, checkpointSourceFixture(t, true))
	data, err := checkpointPolicyJSON(t.Context(), session)
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		Repository string `json:"repository"`
	}
	if err := json.Unmarshal(data, &policy); err != nil || policy.Repository != "acme/widget" {
		t.Fatalf("policy repository = %q (err %v), want the resolved acme/widget, not the checkout directory",
			policy.Repository, err)
	}
	unresolved := checkpointSession(t, checkpointSourceFixture(t, true))
	unresolved.identity = repoIdentity{owner: "", name: "widget"}
	ready, err := reconcileCheckpointBundle(t.Context(), unresolved, false)
	if !errors.Is(err, errCheckpointIdentity) || ready {
		t.Fatalf("unresolved identity must refuse the lifecycle: ready=%v err=%v", ready, err)
	}
	for _, name := range []string{checkpointScript, checkpointCommon, checkpointPolicy} {
		if _, statErr := os.Stat(filepath.Join(unresolved.repoPath, filepath.FromSlash(name))); !os.IsNotExist(statErr) {
			t.Fatalf("unresolved identity still installed %s: %v", name, statErr)
		}
	}
}

func TestAdoptCheckpointBundleAddsJobsAndLocalPolicy(t *testing.T) {
	session := checkpointSession(t, checkpointSourceFixture(t, true))
	ready, err := reconcileCheckpointBundle(context.Background(), session, false)
	if err != nil || !ready {
		t.Fatalf("complete source was not installed: ready=%v err=%v", ready, err)
	}
	for _, name := range []string{checkpointScript, checkpointCommon, checkpointLauncher, checkpointPolicy} {
		if _, err := os.Stat(filepath.Join(session.repoPath, filepath.FromSlash(name))); err != nil {
			t.Fatalf("missing installed checkpoint file %s: %v", name, err)
		}
	}
	yaml := buildLefthookYAMLFor(lefthookShape{languages: lefthookJobLanguages}, ready)
	if !strings.Contains(yaml, "agent-checkpoint-tool:") || !strings.Contains(yaml, "agent-checkpoint-stop:") {
		t.Fatal("complete bundle did not enable both lifecycle jobs")
	}
	policy := mustRead(t, filepath.Join(session.repoPath, filepath.FromSlash(checkpointPolicy)))
	if !strings.Contains(policy, `"publish": false`) || !strings.Contains(policy, `"require_pr": false`) {
		t.Fatalf("adoption policy is not local-only: %s", policy)
	}
}

func TestAdoptCheckpointBundleMissingSourceFailsClosedWithoutJobs(t *testing.T) {
	session := checkpointSession(t, checkpointSourceFixture(t, false))
	ready, err := reconcileCheckpointBundle(context.Background(), session, false)
	if err == nil || ready {
		t.Fatalf("incomplete source was accepted: ready=%v err=%v", ready, err)
	}
	if _, statErr := os.Stat(filepath.Join(session.repoPath, filepath.FromSlash(checkpointScript))); !os.IsNotExist(statErr) {
		t.Fatalf("partial checkpoint script was installed: %v", statErr)
	}
	if strings.Contains(buildLefthookYAMLFor(lefthookShape{languages: lefthookJobLanguages}, ready), "agent-checkpoint-tool:") {
		t.Fatal("incomplete source enabled checkpoint job")
	}
}

func TestAdoptCheckpointBundleCancellationIsRejected(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reconcileCheckpointBundle(ctx, checkpointSession(t, checkpointSourceFixture(t, true)), false); err == nil {
		t.Fatal("cancelled checkpoint bootstrap succeeded")
	}
}

func TestAdoptCheckpointBundlePreservesNonDefaultPolicy(t *testing.T) {
	session := checkpointSession(t, checkpointSourceFixture(t, true))
	mustWrite(t, filepath.Join(session.repoPath, filepath.FromSlash(checkpointPolicy)), `{"version":1,"enabled":true,"commit_after_minutes":7,"commit_after_files":3,"on_stop":true,"publish":false,"remote":"origin","base":"main","repository":"fixture/repo","branch_prefixes":["fix/"],"require_pr":false}`+"\n")
	ready, err := reconcileCheckpointBundle(context.Background(), session, false)
	if err != nil || !ready {
		t.Fatalf("non-default policy disabled lifecycle: ready=%v err=%v", ready, err)
	}
	if !strings.Contains(buildLefthookYAMLFor(lefthookShape{languages: lefthookJobLanguages}, ready), "agent-checkpoint-stop:") || !strings.Contains(mustRead(t, filepath.Join(session.repoPath, filepath.FromSlash(checkpointPolicy))), `"commit_after_minutes":7`) {
		t.Fatal("existing policy was not preserved while enabling jobs")
	}
}

func TestAdoptCheckpointBundleRunsActualEvaluator(t *testing.T) {
	// Capture the required interpreter before the Git fixture isolates PATH.
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("checkpoint integration requires python3: %v", err)
	}
	session := checkpointSession(t, checkpointSourceFixture(t, false))
	for _, name := range checkpointBundle {
		data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(session.opts.LockSourceRoot, filepath.FromSlash(name)), string(data))
	}
	ready, err := reconcileCheckpointBundle(context.Background(), session, false)
	if err != nil || !ready {
		t.Fatalf("actual source was not installed: ready=%v err=%v", ready, err)
	}
	if err := os.WriteFile(filepath.Join(session.repoPath, ".git", "HEAD"), []byte("ref: refs/heads/checkpoint/fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), python, "-B", filepath.FromSlash(checkpointScript), "--event", "tool", "--json", "--marker")
	cmd.Dir = session.repoPath
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual checkpoint evaluator failed: %v (%s)", err, raw)
	}
	line := strings.TrimSpace(string(raw))
	line = strings.TrimPrefix(line, "PRAETOR_CHECKPOINT_RESULT=")
	var result map[string]any
	if err := json.Unmarshal([]byte(line), &result); err != nil {
		t.Fatalf("evaluator did not return marked JSON: %v (%s)", err, raw)
	}
	if result["schema_version"] != float64(1) || result["publication_status"] != "not_due" || result["commit_due"] != true {
		t.Fatalf("unexpected evaluator result: %v", result)
	}
}

// Positive: beside a canonical hook policy, --force keeps the vendored checkpoint script that
// differs from the source bundle, so the policy and its scripts stay one version, and still
// installs a script the vendored bundle lacks (BUG-858).
func TestAdoptCheckpointBundle_Positive_VendoredScriptsSurviveForce(t *testing.T) {
	session := checkpointSession(t, checkpointSourceFixture(t, true))
	session.opts.Force = true
	vendored := "#!/usr/bin/env python3\nprint('vendored')\n"
	mustWrite(t, filepath.Join(session.repoPath, filepath.FromSlash(checkpointScript)), vendored)
	ready, err := reconcileCheckpointBundle(context.Background(), session, true)
	if err != nil || !ready {
		t.Fatalf("vendored bundle: ready=%v err=%v", ready, err)
	}
	if got := mustRead(t, filepath.Join(session.repoPath, filepath.FromSlash(checkpointScript))); got != vendored {
		t.Fatalf("--force replaced the vendored checkpoint script:\n%s", got)
	}
	if got := mustRead(t, filepath.Join(session.repoPath, filepath.FromSlash(checkpointCommon))); !strings.Contains(got, "HookError") {
		t.Fatalf("missing vendored script not installed: %q", got)
	}
}

// Boundary: audit does not read the checkpoint bundle, so without a canonical policy too --force
// keeps a drifted script byte for byte. The lifecycle is then unavailable, the generated
// lefthook.yml carries no checkpoint job, and the missing common.py is not installed beside it.
func TestAdopt_Boundary_ForceKeepsDriftedCheckpointScriptLifecycleUnavailable(t *testing.T) {
	repoPath := newTestRepo(t, "drifted-checkpoint")
	drifted := "print('stale')\n"
	mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(checkpointScript)), drifted)
	source := newAdoptLockSource(t)
	writeCheckpointBundle(t, source)
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: source, Path: repoPath, Force: true})
	if err != nil {
		t.Fatalf("Adopt --force: %v", err)
	}
	assertNoIssues(t, rep)
	if got := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(checkpointScript))); got != drifted {
		t.Fatalf("--force replaced a drifted checkpoint script: %q", got)
	}
	if !strings.Contains(strings.Join(rep.Warnings, "\n"), "checkpoint lifecycle unavailable: existing "+checkpointScript+" differs") {
		t.Fatalf("lifecycle not reported unavailable: %v", rep.Warnings)
	}
	if fileExists(filepath.Join(repoPath, filepath.FromSlash(checkpointCommon))) {
		t.Error("common.py was installed beside a kept checkpoint.py")
	}
	if got := mustRead(t, filepath.Join(repoPath, lefthookFile)); got != buildLefthookYAMLFor(lefthookShape{languages: lefthookJobLanguages}, false) {
		t.Error("lefthook.yml carries checkpoint jobs while the lifecycle is unavailable")
	}
}

// Boundary: through a full adopt --force, a lefthook.yml reaching the canonical policy only
// through remotes still protects the vendored checkpoint script, not just the configuration.
func TestAdopt_Boundary_ForceKeepsCheckpointScriptBesideRemoteCanonicalPolicy(t *testing.T) {
	repoPath := newTestRepo(t, "remote-canonical-lefthook")
	remote := "remotes:\n  - git_url: https://github.com/cordanaLLM/praetor\n    ref: v1.0.0\n    configs: [.config/lefthook/praetor.yml]\n"
	mustWrite(t, filepath.Join(repoPath, lefthookFile), remote)
	vendored := "#!/usr/bin/env python3\nprint('vendored')\n"
	mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(checkpointScript)), vendored)
	source := newAdoptLockSource(t)
	writeCheckpointBundle(t, source)
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: source, Path: repoPath, Force: true})
	if err != nil {
		t.Fatalf("Adopt --force: %v", err)
	}
	assertNoIssues(t, rep)
	if got := mustRead(t, filepath.Join(repoPath, lefthookFile)); got != remote {
		t.Fatalf("--force replaced a configuration reaching the canonical policy through remotes:\n%s", got)
	}
	if got := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(checkpointScript))); got != vendored {
		t.Fatalf("--force replaced the vendored checkpoint script:\n%s", got)
	}
}
