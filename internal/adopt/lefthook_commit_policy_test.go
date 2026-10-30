// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"github.com/cordanaLLM/praetor/internal/util"
	"go.yaml.in/yaml/v3"
)

// Main shipped the secrets job before commit policy; every such shape must migrate too.
func TestAdoptCommitPolicy_PriorWithSecrets(t *testing.T) {
	for _, languages := range lefthookLanguageSets {
		for _, reuse := range []bool{false, true} {
			for _, tool := range []bool{false, true} {
				if tool && languages&hisscatalog.LanguageGo == 0 {
					continue
				}
				assertPriorWithSecrets(t, lefthookShape{languages: languages, reuse: reuse, toolGovuln: tool})
			}
		}
	}
}

func assertPriorWithSecrets(t *testing.T, shape lefthookShape) {
	t.Helper()
	for _, checkpoint := range []bool{false, true} {
		body := buildLefthookYAMLFor(shape, checkpoint)
		body = strings.Replace(body, "commit-msg:\n  commands:\n    message-policy:\n"+
			lefthookRun(lefthookGovernedCommand("forge check-message {1}"))+"\n", "", 1)
		body = strings.Replace(body, "  scripts:\n    "+commitPolicyScriptName+":\n      runner: sh\n", "", 1)
		name := fmt.Sprintf("secrets-before-policy-%d-%t-%t-%t.lefthook.yml", shape.languages, shape.reuse, shape.toolGovuln, checkpoint)
		path := filepath.Join(priorLefthookFixtures, name)
		if os.Getenv(updateEmittedFixturesEnv) == "1" {
			mustWrite(t, path, body)
		}
		if got := mustRead(t, path); got != body {
			t.Fatalf("%s differs; regenerate with %s=1", path, updateEmittedFixturesEnv)
		}
		if !isPriorLefthookConfig([]byte(body)) {
			t.Errorf("prior secrets template unrecognized: %s %s", fixtureDigest(t, name, []byte(body)), name)
		}
	}
}

func addCustomCommitMsg(rendering string) string {
	return strings.Replace(rendering, "commit-msg:\n  commands:\n",
		"commit-msg:\n  commands:\n    conventional:\n      run: ./scripts/check-msg {1}\n", 1)
}

// omitCommitPolicyJobs narrows the historical folding comparison to jobs that existed then.
func omitCommitPolicyJobs(t *testing.T, decoded map[string]any) {
	t.Helper()
	push, ok := decoded["pre-push"].(map[string]any)
	if !ok {
		t.Fatal("pre-push mapping missing")
	}
	scripts, ok := push["scripts"].(map[string]any)
	if !ok {
		t.Fatal("pre-push scripts missing")
	}
	delete(decoded, "commit-msg")
	delete(scripts, commitPolicyScriptName)
	delete(push, "scripts")
}

// Every generated shape carries the policy through the same pinned engine launcher.
func TestLefthookCommitPolicy_Positive_ParsedJobs(t *testing.T) {
	for _, languages := range lefthookLanguageSets {
		for _, checkpoint := range []bool{false, true} {
			var decoded map[string]struct {
				Commands map[string]struct{ Run string }
				Scripts  map[string]struct{ Runner string }
			}
			body := buildLefthookYAMLFor(lefthookShape{languages: languages}, checkpoint)
			if err := yaml.Unmarshal([]byte(body), &decoded); err != nil {
				t.Fatal(err)
			}
			if got := decoded["commit-msg"].Commands["message-policy"].Run; got != lefthookGovernedCommand("forge check-message {1}") {
				t.Errorf("commit-msg run = %v", got)
			}
			commands := decoded["commit-msg"].Commands
			if len(commands) != 1 {
				t.Errorf("commit-msg jobs = %d, want one", len(commands))
			}
			if got := decoded["pre-push"].Scripts[commitPolicyScriptName].Runner; got != "sh" {
				t.Errorf("pre-push run = %v", got)
			}
		}
	}
}

// A repository-owned commit-msg stays byte-identical, even under --force, and is reported.
func TestAdoptCommitPolicy_Negative_CustomCommitMsgPreserved(t *testing.T) {
	custom := "commit-msg:\n  commands:\n    custom-policy:\n      run: echo custom {1}\n"
	for _, force := range []bool{false, true} {
		repo, report := adoptLefthookFixture(t, "custom-commit-msg", custom, force)
		assertLefthookKept(t, repo, report, custom)
		identity := classifyLefthookConfig([]byte(custom), lefthookShape{languages: lefthookJobLanguages})
		for _, want := range []string{"commit-msg/commands/message-policy", "pre-push/scripts/praetor-commit-policy.sh", "custom-policy"} {
			if !strings.Contains(identity.reason, want) {
				t.Errorf("preservation reason lacks %q: %s", want, identity.reason)
			}
		}
		if got := mustRead(t, filepath.Join(repo, lefthookFile)); got != custom {
			t.Fatal("custom commit-msg changed")
		}
	}
}

// Earlier unedited templates migrate through the existing digest reconciliation path.
// Existing generic prior-rendering tests replay these fixtures with and without CRLF.
func TestAdoptCommitPolicy_Boundary_PriorRenderingMigrates(t *testing.T) {
	fixtures := readPriorLefthookFixtures(t)
	count := 0
	for name, data := range fixtures {
		if !strings.HasPrefix(name, "before-commit-policy-") {
			continue
		}
		count++
		if got := classifyLefthookConfig(data, lefthookShape{languages: lefthookJobLanguages}); !got.prior || got.reason != "" {
			t.Errorf("%s not recognized as prior: %+v", name, got)
		}
	}
	if count != 24 {
		t.Fatalf("prior commit-policy renderings = %d, want 24", count)
	}
	prior := string(fixtures["before-commit-policy-0-false-false-false.lefthook.yml"])
	repo, report := adoptLefthookFixture(t, "commit-policy-upgrade", prior, false)
	if !hasAction(report, lefthookFile, actionReconcile) {
		t.Fatalf("prior template not reconciled: %+v", report.ActionDetails)
	}
	if got := mustRead(t, filepath.Join(repo, filepath.FromSlash(commitPolicyScriptFile))); got != buildCommitPolicyScript() {
		t.Fatal("pre-push policy script missing or changed")
	}
	jobs := writtenLefthookJobs(t, repo)
	assertJobs(t, "upgraded", jobs, []string{"commit-msg/commands/message-policy", "pre-push/scripts/praetor-commit-policy.sh"}, nil)
}

func TestHarnessCommitPolicy_Positive_BothStages(t *testing.T) {
	rows := invariantRows(renderTruthHarness(t, "", "fixture", truthPlan))
	for _, row := range rows {
		if !strings.HasPrefix(row, "| **HISS-14** ") {
			continue
		}
		if strings.Contains(row, "not enforced") || !strings.Contains(row, "lefthook commit-msg/pre-push") ||
			!strings.Contains(row, "policy violation blocks commit, blocks push") {
			t.Fatalf("HISS-14 row: %s", row)
		}
		return
	}
	t.Fatal("HISS-14 row missing")
}

func TestCommitPolicyScript_PositiveNegativeBoundary(t *testing.T) {
	stubs, work := t.TempDir(), t.TempDir()
	log := filepath.Join(work, "calls.log")
	mustWrite(t, filepath.Join(work, filepath.FromSlash(commitPolicyScriptFile)), buildCommitPolicyScript())
	for _, code := range []int{0, 17} {
		recordingStub(t, stubs, util.PraetorCLI, log, code)
		if out, got := runHookLine(t, "sh", "sh "+commitPolicyScriptFile, stubs, work); got != code {
			t.Fatalf("policy script exit %d, want %d: %s", got, code, out)
		}
	}
	want := strings.Repeat(util.PraetorCLI+" forge check-commits --pre-push\n", 2)
	if got := mustRead(t, log); got != want {
		t.Fatalf("script arguments = %q, want %q", got, want)
	}
}

func TestAdoptCommitPolicy_Negative_CustomScriptPreserved(t *testing.T) {
	for _, force := range []bool{false, true} {
		repo := newTestRepo(t, "custom-policy-script")
		custom := "#!/bin/sh\necho custom policy\n"
		path := filepath.Join(repo, filepath.FromSlash(commitPolicyScriptFile))
		mustWrite(t, path, custom)
		_, err := Adopt(t.Context(), AdoptOptions{Path: repo, LockSourceRoot: newAdoptLockSource(t), Force: force})
		if err == nil || !strings.Contains(err.Error(), "custom "+commitPolicyScriptFile+" preserved") {
			t.Fatalf("custom policy script accepted as a generated check: %v", err)
		}
		if got := mustRead(t, path); got != custom {
			t.Fatal("custom script overwritten")
		}
	}
}
