// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// Negative (engine-hiss18-full-tree-on-pr-20261027): a pull_request workflow with a step running
// pre-commit run --all-files produces a WARN finding naming the workflow, line, job and command.
func TestAuditWorkflowTriggers_FullTree_Negative_PullRequestPreCommitAllFiles(t *testing.T) {
	doc := "on: pull_request\njobs:\n  lint:\n    runs-on: ubuntu-latest\n    steps:\n      - run: pre-commit run --all-files\n"
	root := triggerRepository(t, map[string]string{"pr.yml": doc})
	out := auditTriggers(t, root)
	if wantFinding := "[WARN] Workflow triggers (HISS-18): .github/workflows/pr.yml:6: pull_request job lint step 1 runs full-tree command \"pre-commit run --all-files\" on every pull request: scope to changed files (pre-commit run --from-ref origin/main --to-ref HEAD) or guard to default branch, tags or schedule."; !strings.Contains(out, wantFinding) {
		t.Fatalf("output lacks expected finding:\n%s\nwant finding:\n%s", out, wantFinding)
	}
}

// Positive (engine-hiss18-full-tree-on-pr-20261027): job guarded by github.ref == 'refs/heads/main'
// or step guarded by github.event_name == 'schedule' produces no finding.
func TestAuditWorkflowTriggers_FullTree_Positive_GuardedJobOrStep(t *testing.T) {
	for name, doc := range map[string]string{"job": "on: pull_request\njobs:\n  lint:\n    if: github.ref == 'refs/heads/main'\n    runs-on: ubuntu-latest\n    steps:\n      - run: pre-commit run --all-files\n", "step": "on: pull_request\njobs:\n  lint:\n    runs-on: ubuntu-latest\n    steps:\n      - if: github.event_name == 'schedule'\n        run: pre-commit run --all-files\n"} {
		root := triggerRepository(t, map[string]string{"x.yml": doc})
		out := auditTriggers(t, root)
		if strings.Contains(out, "full-tree command") {
			t.Errorf("%s: guarded full-tree step must produce no finding:\n%s", name, out)
		}
	}
}

// Push filters (engine-hiss18-full-tree-on-pr-20261027): push filtered to [main] passes,
// tags-only push passes, and unfiltered push reports a finding.
func TestAuditWorkflowTriggers_FullTree_PushFilters(t *testing.T) {
	mainOnly := "on:\n  push:\n    branches: [main]\njobs:\n  lint:\n    runs-on: ubuntu-latest\n    steps:\n      - run: pre-commit run --all-files\n"
	tagsOnly := "on:\n  push:\n    tags: ['v*']\njobs:\n  lint:\n    runs-on: ubuntu-latest\n    steps:\n      - run: pre-commit run --all-files\n"
	unfiltered := "on: push\njobs:\n  lint:\n    runs-on: ubuntu-latest\n    steps:\n      - run: pre-commit run --all-files\n"

	rootMain := triggerRepository(t, map[string]string{"main.yml": mainOnly})
	outMain := auditTriggers(t, rootMain)
	if strings.Contains(outMain, "pre-commit run --all-files") {
		t.Errorf("main-only push must not report full-tree finding:\n%s", outMain)
	}

	rootTags := triggerRepository(t, map[string]string{"tags.yml": tagsOnly})
	outTags := auditTriggers(t, rootTags)
	if strings.Contains(outTags, "pre-commit run --all-files") {
		t.Errorf("tags-only push must not report full-tree finding:\n%s", outTags)
	}

	rootUnfiltered := triggerRepository(t, map[string]string{"unfiltered.yml": unfiltered})
	outUnfiltered := auditTriggers(t, rootUnfiltered)
	if !strings.Contains(outUnfiltered, "step 1 runs full-tree command \"pre-commit run --all-files\" on every branch push") {
		t.Errorf("unfiltered push must report full-tree finding:\n%s", outUnfiltered)
	}
}

// Positive (engine-hiss18-full-tree-on-pr-20261027): pre-commit scoped to changed files passes.
func TestAuditWorkflowTriggers_FullTree_Positive_ScopedPreCommit(t *testing.T) {
	doc := "on: pull_request\njobs:\n  lint:\n    runs-on: ubuntu-latest\n    steps:\n      - run: pre-commit run --from-ref origin/main --to-ref HEAD\n"
	root := triggerRepository(t, map[string]string{"scoped.yml": doc})
	out := auditTriggers(t, root)
	if strings.Contains(out, "full-tree command") {
		t.Fatalf("scoped pre-commit must pass:\n%s", out)
	}
}

// Negative (engine-hiss18-full-tree-on-pr-20261027): molecule test --all on pull request reports a finding.
func TestAuditWorkflowTriggers_FullTree_Negative_MoleculeAll(t *testing.T) {
	doc := "on: pull_request\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: molecule test --all\n"
	root := triggerRepository(t, map[string]string{"mol.yml": doc})
	out := auditTriggers(t, root)
	if wantFinding := "[WARN] Workflow triggers (HISS-18): .github/workflows/mol.yml:6: pull_request job test step 1 runs full-tree command \"molecule test --all\" on every pull request: scope to changed files (pre-commit run --from-ref origin/main --to-ref HEAD) or guard to default branch, tags or schedule."; !strings.Contains(out, wantFinding) {
		t.Fatalf("molecule test --all must be reported:\n%s\nwant:\n%s", out, wantFinding)
	}
}

// Positive (engine-hiss18-full-tree-on-pr-20261027): quoted full-tree command in echo produces no finding.
func TestAuditWorkflowTriggers_FullTree_Positive_QuotedText(t *testing.T) {
	doc := "on: pull_request\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo \"pre-commit run --all-files\"\n"
	root := triggerRepository(t, map[string]string{"echo.yml": doc})
	out := auditTriggers(t, root)
	if strings.Contains(out, "full-tree command") {
		t.Fatalf("quoted command must not be reported:\n%s", out)
	}
}

// Exceptions (engine-hiss18-full-tree-on-pr-20261027): a live HISS-18 exceptions entry for the workflow
// excuses the finding under a [PASS] line.
func TestAuditWorkflowTriggers_FullTree_Exceptions(t *testing.T) {
	doc := "on: pull_request\njobs:\n  lint:\n    runs-on: ubuntu-latest\n    steps:\n      - run: pre-commit run --all-files\n"
	root := triggerRepository(t, map[string]string{"exempt.yml": doc})
	entry := config.Exception{
		Rule:     config.ExceptionRuleWorkflowTriggers,
		Path:     ".github/workflows/exempt.yml",
		Reason:   "legacy full tree run on pull requests",
		Category: "A",
		Expires:  "2026-12-31",
	}
	out := auditTriggers(t, root, entry)
	if strings.Contains(out, "[WARN] Workflow triggers (HISS-18): .github/workflows/exempt.yml") {
		t.Fatalf("excepted workflow must not emit WARN:\n%s", out)
	}
	if !strings.Contains(out, "[PASS] Workflow triggers (HISS-18): .github/workflows/exempt.yml excepted until 2026-12-31") {
		t.Fatalf("output lacks PASS excepted line:\n%s", out)
	}
}

// Negative (engine-hiss18-full-tree-on-pr-20261027): a condition the check does not evaluate (an
// alternative, an inequality, a negation) reads as unguarded, since it can run the step on a pull
// request; a plain conjunction with the default-branch guard still passes.
func TestAuditWorkflowTriggers_FullTree_Negative_WideningConditions(t *testing.T) {
	step := "    runs-on: ubuntu-latest\n    steps:\n      - run: pre-commit run --all-files\n"
	for _, condition := range []string{
		"github.ref == 'refs/heads/main' || github.event_name == 'pull_request'",
		"github.ref_name != github.event.repository.default_branch",
		"!startsWith(github.ref, 'refs/tags/')",
	} {
		doc := "on: pull_request\njobs:\n  lint:\n    if: ${{ " + condition + " }}\n" + step
		out := auditTriggers(t, triggerRepository(t, map[string]string{"x.yml": doc}))
		if !strings.Contains(out, "full-tree command") {
			t.Errorf("condition %q must read as unguarded:\n%s", condition, out)
		}
	}
	guarded := "on: pull_request\njobs:\n  lint:\n    if: github.event_name == 'push' && github.ref == 'refs/heads/main'\n" + step
	if out := auditTriggers(t, triggerRepository(t, map[string]string{"x.yml": guarded})); strings.Contains(out, "full-tree command") {
		t.Errorf("conjunction with the default-branch guard must pass:\n%s", out)
	}
}

// pre-commit/action (engine-hiss18-full-tree-on-pr-20261027): the action runs `pre-commit run
// --all-files` unless extra_args says otherwise, so without extra_args, or with --all-files in
// them, it is reported; extra_args scoped to the changed files pass.
func TestAuditWorkflowTriggers_FullTree_PreCommitAction(t *testing.T) {
	uses := "on: pull_request\njobs:\n  lint:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: pre-commit/action@2c7b3805fd2a0fd8c1884dcaebf91fc102a13ecd # v3.0.1\n"
	for name, doc := range map[string]string{
		"default":   uses,
		"all files": uses + "        with:\n          extra_args: --all-files --show-diff-on-failure\n",
	} {
		if out := auditTriggers(t, triggerRepository(t, map[string]string{"x.yml": doc})); !strings.Contains(out, "pre-commit/action (extra_args") {
			t.Errorf("%s: pre-commit/action must be reported:\n%s", name, out)
		}
	}
	scoped := uses + "        with:\n          extra_args: --from-ref origin/main --to-ref HEAD\n"
	if out := auditTriggers(t, triggerRepository(t, map[string]string{"x.yml": scoped})); strings.Contains(out, "full-tree command") {
		t.Errorf("scoped pre-commit/action must pass:\n%s", out)
	}
}
