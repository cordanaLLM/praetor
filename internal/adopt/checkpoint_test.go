package adopt

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func checkpointSourceFixture(t *testing.T, complete bool) string {
	t.Helper()
	root := t.TempDir()
	if complete {
		for name, body := range map[string]string{
			checkpointScript: "#!/usr/bin/env python3\nprint('shared')\n",
			checkpointCommon: "class HookError(Exception):\n    pass\n",
		} {
			mustWrite(t, filepath.Join(root, name), body)
		}
	}
	return root
}

func checkpointSession(t *testing.T, source string) *adoptSession {
	t.Helper()
	return &adoptSession{repoPath: newTestRepo(t, "checkpoint-adoption"), repoName: "fixture", opts: AdoptOptions{LockSourceRoot: source}, report: &AdoptReport{}}
}

func TestAdoptCheckpointBundleAddsJobsAndLocalPolicy(t *testing.T) {
	session := checkpointSession(t, checkpointSourceFixture(t, true))
	ready, err := reconcileCheckpointBundle(context.Background(), session, false)
	if err != nil || !ready {
		t.Fatalf("complete source was not installed: ready=%v err=%v", ready, err)
	}
	for _, name := range []string{checkpointScript, checkpointCommon, checkpointPolicy} {
		if _, err := os.Stat(filepath.Join(session.repoPath, filepath.FromSlash(name))); err != nil {
			t.Fatalf("missing installed checkpoint file %s: %v", name, err)
		}
	}
	yaml := buildLefthookYAMLFor(ready)
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
	if strings.Contains(buildLefthookYAMLFor(ready), "agent-checkpoint-tool:") {
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
	if !strings.Contains(buildLefthookYAMLFor(ready), "agent-checkpoint-stop:") || !strings.Contains(mustRead(t, filepath.Join(session.repoPath, filepath.FromSlash(checkpointPolicy))), `"commit_after_minutes":7`) {
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
	for _, name := range []string{checkpointScript, checkpointCommon} {
		data, err := os.ReadFile(filepath.Join("..", "..", ".config", "lefthook", "scripts", filepath.FromSlash(filepath.Base(name))))
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

// Negative: without a canonical policy the --force contract is unchanged: the differing script
// is replaced by the verified source.
func TestAdoptCheckpointBundle_Negative_ForceReplacesUnvendoredScript(t *testing.T) {
	session := checkpointSession(t, checkpointSourceFixture(t, true))
	session.opts.Force = true
	mustWrite(t, filepath.Join(session.repoPath, filepath.FromSlash(checkpointScript)), "print('stale')\n")
	if ready, err := reconcileCheckpointBundle(context.Background(), session, false); err != nil || !ready {
		t.Fatalf("ready=%v err=%v", ready, err)
	}
	if got := mustRead(t, filepath.Join(session.repoPath, filepath.FromSlash(checkpointScript))); !strings.Contains(got, "shared") {
		t.Fatalf("--force kept a stale unvendored script: %q", got)
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
	mustWrite(t, filepath.Join(source, filepath.FromSlash(checkpointScript)), "#!/usr/bin/env python3\nprint('shared')\n")
	mustWrite(t, filepath.Join(source, filepath.FromSlash(checkpointCommon)), "class HookError(Exception):\n    pass\n")
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
