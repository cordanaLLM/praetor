// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/docstrings"
	"github.com/cordanaLLM/praetor/internal/util"
)

// TestCodeRabbitCIEvidenceCreated checks the actual scaffold's schema-defined review guidance.
func TestCodeRabbitCIEvidenceCreated(t *testing.T) {
	repo := t.TempDir()
	s := &adoptSession{repoPath: repo, report: &AdoptReport{}}
	if err := reconcileCodeRabbit(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	assertCodeRabbitCIEvidence(t, mustRead(t, filepath.Join(repo, codeRabbitFile)))
}

// assertCodeRabbitCIEvidence checks the global path, schema limit and evidence contract.
func assertCodeRabbitCIEvidence(t *testing.T, text string) {
	t.Helper()
	var got struct {
		Reviews struct {
			Instructions []struct {
				Path         string `yaml:"path"`
				Instructions string `yaml:"instructions"`
			} `yaml:"path_instructions"`
		} `yaml:"reviews"`
	}
	if err := util.DecodeYAMLDocument([]byte(text), &got, util.YAMLDocumentOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, entry := range got.Reviews.Instructions {
		if entry.Path != "**" {
			continue
		}
		if len([]rune(entry.Instructions)) > 20000 {
			t.Fatal("review guidance exceeds schema maxLength 20000")
		}
		for _, want := range []string{"CI verification job", ".github/workflows", "make verify-all", "green run", "pull request head", "AGENTS.md", "CLAUDE.md", "coding agents", "not the CodeRabbit reviewer"} {
			if !strings.Contains(entry.Instructions, want) {
				t.Errorf("review guidance missing %q: %s", want, entry.Instructions)
			}
		}
		return
	}
	t.Fatal("reviews.path_instructions lacks a global CI evidence instruction")
}

// TestCodeRabbitCIEvidenceManaged checks additive edits and byte preservation of user policy.
func TestCodeRabbitCIEvidenceManaged(t *testing.T) {
	const prefix = "# keep header\nreviews:\n  auto_review:\n    drafts: true\n    base_branches: [.*] # keep bases\n"
	const custom = "  path_instructions: # keep instructions\n    - path: '*.go' # keep glob\n      instructions: |-\n        Keep  two spaces.\n        Keep this line.\n"
	const suffix = "# keep footer\nchat: {auto_reply: false} # keep chat\n"
	for name, policy := range map[string]string{"missing": "", "custom": custom} {
		t.Run(name, func(t *testing.T) {
			initial := prefix + policy + suffix
			got, err := mergeCodeRabbit(t.Context(), []byte(initial))
			if err != nil {
				t.Fatal(err)
			}
			assertCodeRabbitCIEvidence(t, string(got))
			if !strings.HasPrefix(string(got), prefix+policy) || !strings.HasSuffix(string(got), suffix) {
				t.Fatalf("user text or comments changed: %s", got)
			}
			rerun, err := mergeCodeRabbit(t.Context(), got)
			if err != nil || string(rerun) != string(got) {
				t.Fatalf("instruction not idempotent: %s, %v", rerun, err)
			}
		})
	}
}

// TestCodeRabbitCIEvidenceKeepsUserGlobal checks existing global guidance remains byte-identical.
func TestCodeRabbitCIEvidenceKeepsUserGlobal(t *testing.T) {
	const initial = "reviews:\n  auto_review: {drafts: true, base_branches: [.*]}\n  path_instructions:\n    - path: '**' # user-owned global\n      instructions: 'Use our custom CI policy.' # keep wording\n"
	got, err := mergeCodeRabbit(t.Context(), []byte(initial))
	if err != nil || string(got) != initial {
		t.Fatalf("user global instruction changed: %s, %v", got, err)
	}
}

// TestCodeRabbitCIEvidenceEmittedFixture pins the scaffold, fixture and engine configuration.
func TestCodeRabbitCIEvidenceEmittedFixture(t *testing.T) {
	for _, path := range [2]string{"testdata/emitted/.coderabbit.yaml", "../../.coderabbit.yaml"} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		assertCodeRabbitCIEvidence(t, string(got))
		if string(got) != codeRabbitConfig {
			t.Errorf("%s differs from codeRabbitConfig", path)
		}
	}
}

// TestCodeRabbitCIEvidenceRefusesMalformed checks that unsafe policy never gets replaced.
func TestCodeRabbitCIEvidenceRefusesMalformed(t *testing.T) {
	for name, policy := range map[string]string{
		"scalar": "true", "mapping": "{}", "scalar entry": "[false]",
		"missing instructions":    "[{path: '**'}]",
		"non-string instructions": "[{path: '**', instructions: false}]",
		"invalid after global":    "[{path: '**', instructions: keep}, {path: 1, instructions: keep}]",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := mergeCodeRabbit(t.Context(), []byte("reviews:\n  path_instructions: "+policy+"\n")); err == nil {
				t.Fatal("accepted malformed path instructions")
			}
		})
	}
}

// TestCodeRabbitCIEvidenceLayouts checks null, empty, flow and CRLF policy merges.
func TestCodeRabbitCIEvidenceLayouts(t *testing.T) {
	for name, policy := range map[string]string{
		"null":  "  path_instructions: null # keep null\n",
		"empty": "  path_instructions: [] # keep empty\n",
		"flow":  "  path_instructions: [{path: '*.go', instructions: 'Keep  spaces.'}] # keep flow\n",
		"CRLF":  "  path_instructions:\r\n    - path: '*.go'\r\n      instructions: Keep it. # keep CRLF\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			initial := "reviews:\n" + policy
			if name == "CRLF" {
				initial = "reviews:\r\n" + policy
			}
			got, err := mergeCodeRabbit(t.Context(), []byte(initial))
			if err != nil {
				t.Fatal(err)
			}
			assertCodeRabbitCIEvidence(t, string(got))
			if !strings.Contains(string(got), "# keep") {
				t.Fatal("lost policy comment")
			}
			rerun, err := mergeCodeRabbit(t.Context(), got)
			if err != nil || string(rerun) != string(got) {
				t.Fatalf("layout not idempotent: %s, %v", rerun, err)
			}
		})
	}
}

// TestCodeRabbitCIEvidenceInstructionBounds checks insertion and rejection at the entry cap.
func TestCodeRabbitCIEvidenceInstructionBounds(t *testing.T) {
	const prefix = "reviews:\n  auto_review: {drafts: true, base_branches: [.*]}\n  path_instructions:\n"
	const item = "    - {path: '*.go', instructions: keep}\n"
	below := prefix + strings.Repeat(item, maxAdoptManifestMappingNodes-1)
	got, err := mergeCodeRabbit(t.Context(), []byte(below))
	if err != nil {
		t.Fatal(err)
	}
	assertCodeRabbitCIEvidence(t, string(got))
	rerun, err := mergeCodeRabbit(t.Context(), got)
	if err != nil || string(rerun) != string(got) {
		t.Fatalf("configured list at cap changed: %v", err)
	}
	for name, initial := range map[string]string{"full": below + item, "overfull": string(got) + item} {
		t.Run(name, func(t *testing.T) {
			if _, err := mergeCodeRabbit(t.Context(), []byte(initial)); err == nil {
				t.Fatal("accepted instruction list without capacity")
			}
		})
	}
}

// TestAdoptCodeRabbitCreatesAndPreserves verifies managed keys, unrelated text and rerun stability.
func TestAdoptCodeRabbitCreatesAndPreserves(t *testing.T) {
	for name, initial := range map[string]string{
		"absent":   "",
		"existing": "# repository review policy\nlanguage: en-US # keep language\nreviews:\n  profile: assertive # keep profile\n  auto_review:\n    drafts: false # managed switch\n    base_branches: ['release/.*'] # keep branches\n  pre_merge_checks:\n    docstrings:\n      threshold: 85 # keep threshold\nchat:\n  auto_reply: false # keep chat\n",
	} {
		t.Run(name, func(t *testing.T) {
			adoptCodeRabbitCase(t, name, initial)
		})
	}
}

// adoptCodeRabbitCase checks a complete adoption and a byte-identical second adoption.
func adoptCodeRabbitCase(t *testing.T, name, initial string) {
	t.Helper()
	repo := newTestRepo(t, "coderabbit-"+name)
	file := filepath.Join(repo, ".coderabbit.yaml")
	if initial != "" {
		mustWrite(t, file, initial)
	}
	opts := AdoptOptions{Path: repo, LockSourceRoot: newAdoptLockSource(t), SkipHookActivation: true}
	report, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	got := mustRead(t, file)
	assertCodeRabbitDraftKeys(t, got)
	assertCodeRabbitSettings(t, repo, initial, got)
	if findActionDetail(report.ActionDetails, ".coderabbit.yaml") == "" {
		t.Fatal("managed keys absent from adoption report")
	}
	before := snapshotTree(t, repo)
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatalf("readopt: %v", err)
	}
	assertTreeUnchanged(t, before, snapshotTree(t, repo))
}

// assertCodeRabbitSettings checks unrelated bytes and the existing docstring reader.
func assertCodeRabbitSettings(t *testing.T, repo, initial, got string) {
	t.Helper()
	if initial == "" {
		if !strings.Contains(got, "# yaml-language-server: $schema=https://coderabbit.ai/integrations/schema.v2.json") {
			t.Fatal("created configuration lacks the v2 schema comment")
		}
		return
	}
	want := strings.Replace(initial, "drafts: false", "drafts: true", 1)
	want = strings.Replace(want, "['release/.*']", "['release/.*', .*]", 1)
	instructions := codeRabbitConfig[strings.Index(codeRabbitConfig, "  path_instructions:"):]
	want = strings.Replace(want, "\nchat:", "\n"+instructions+"chat:", 1)
	if got != want {
		t.Fatalf("unrelated text changed:\n%s\nwant:\n%s", got, want)
	}
	cfg, err := docstrings.LoadConfig(repo)
	if err != nil || cfg.Threshold != 85 {
		t.Fatalf("docstring configuration changed: %+v, %v", cfg, err)
	}
}

// assertCodeRabbitDraftKeys checks the parsed values rather than a particular YAML spelling.
func assertCodeRabbitDraftKeys(t *testing.T, text string) {
	t.Helper()
	var got struct {
		Reviews struct {
			AutoReview struct {
				Drafts   bool     `yaml:"drafts"`
				Branches []string `yaml:"base_branches"`
			} `yaml:"auto_review"`
		} `yaml:"reviews"`
	}
	if err := util.DecodeYAMLDocument([]byte(text), &got, util.YAMLDocumentOptions{}); err != nil {
		t.Fatalf("decode CodeRabbit configuration: %v", err)
	}
	if !got.Reviews.AutoReview.Drafts || !slices.Contains(got.Reviews.AutoReview.Branches, ".*") {
		t.Fatalf("draft and stacked-base review missing: %+v", got)
	}
}

// TestCodeRabbitBoundaryLayouts covers empty, partial, null, flow, list and CRLF configurations.
func TestCodeRabbitBoundaryLayouts(t *testing.T) {
	cases := []string{
		"", "# keep empty file comment\n", "language: en-US\n",
		"reviews:\n  profile: chill\n", "reviews: null # keep null comment\n",
		"reviews:\n  auto_review: null # keep nested comment\n",
		"{reviews: {profile: chill, auto_review: {drafts: false, base_branches: []}}, language: en-US}\n",
		"reviews:\n  auto_review:\n    base_branches:\n      - release # keep pattern\n    drafts: false\n",
		"reviews:\r\n  auto_review:\r\n    drafts: false\r\n    base_branches: []\r\n",
		"reviews:\n  auto_review:\n    drafts: true\n    base_branches: [.*] # already configured\n",
	}
	for i := 0; i < len(cases) && i < 16; i++ {
		got, err := mergeCodeRabbit(t.Context(), []byte(cases[i]))
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		assertCodeRabbitDraftKeys(t, string(got))
		rerun, err := mergeCodeRabbit(t.Context(), got)
		if err != nil || string(rerun) != string(got) {
			t.Fatalf("case %d rerun changed bytes: %q, %v", i, rerun, err)
		}
		if strings.Contains(cases[i], "# keep") && !strings.Contains(string(got), "# keep") {
			t.Fatalf("case %d lost a comment: %s", i, got)
		}
		if strings.Contains(cases[i], "\r\n") && strings.Contains(strings.ReplaceAll(string(got), "\r\n", ""), "\n") {
			t.Fatalf("case %d changed CRLF endings: %q", i, got)
		}
	}
}

// TestCodeRabbitNegativeRefusesUnsafeYAML verifies malformed inputs fail instead of replacing data.
func TestCodeRabbitNegativeRefusesUnsafeYAML(t *testing.T) {
	cases := []string{
		"reviews: [", "reviews: {}\nreviews: {}\n", "---\nreviews: {}\n---\nchat: {}\n",
		"reviews: &reviews {}\n", "reviews: []\n", "reviews:\n  auto_review: []\n",
		"reviews:\n  auto_review:\n    base_branches: release\n",
		"reviews:\n  auto_review:\n    base_branches: [1]\n",
		"reviews:\n  auto_review:\n    base_branches: [.*, 1]\n",
		"reviews:\n  auto_review:\n    drafts: [false]\n",
		"reviews: {}\r\nchat: {}\n",
	}
	for i := 0; i < len(cases) && i < 16; i++ {
		if _, err := mergeCodeRabbit(t.Context(), []byte(cases[i])); err == nil {
			t.Fatalf("case %d accepted unsafe configuration: %q", i, cases[i])
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := mergeCodeRabbit(ctx, []byte("reviews: {}\n")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled edit did not return cancellation: %v", err)
	}
}

// TestCodeRabbitBranchesBoundary checks the entry cap without breaking rerun stability at it.
func TestCodeRabbitBranchesBoundary(t *testing.T) {
	const prefix = "reviews:\n  auto_review:\n    drafts: true\n    base_branches:\n"
	const item = "      - release\n"
	below := prefix + strings.Repeat(item, maxAdoptManifestMappingNodes-1)
	at := below + "      - .*\n"
	got, err := mergeCodeRabbit(t.Context(), []byte(below))
	if err != nil {
		t.Fatalf("append at bound: %v", err)
	}
	assertCodeRabbitDraftKeys(t, string(got))
	configured, err := mergeCodeRabbit(t.Context(), []byte(at))
	if err != nil {
		t.Fatalf("configure at bound: %v", err)
	}
	for _, initial := range [2]string{string(configured), string(got)} {
		rerun, err := mergeCodeRabbit(t.Context(), []byte(initial))
		if err != nil || string(rerun) != initial {
			t.Fatalf("configured at-bound list changed on rerun: %v", err)
		}
	}
	for _, initial := range [2]string{below + item, at + item} {
		if _, err := mergeCodeRabbit(t.Context(), []byte(initial)); err == nil {
			t.Fatal("list without space or over its bound was accepted")
		}
	}
}

// TestCodeRabbitDryRun plans both creation and replacement without changing the checkout.
func TestCodeRabbitDryRun(t *testing.T) {
	for _, initial := range [2]string{"", "reviews:\n  auto_review:\n    drafts: false\n"} {
		repo := t.TempDir()
		if initial != "" {
			mustWrite(t, filepath.Join(repo, codeRabbitFile), initial)
		}
		before := snapshotTree(t, repo)
		s := &adoptSession{repoPath: repo, opts: AdoptOptions{DryRun: true}, report: &AdoptReport{}}
		if err := reconcileCodeRabbit(t.Context(), s); err != nil {
			t.Fatal(err)
		}
		assertTreeUnchanged(t, before, snapshotTree(t, repo))
		assertCodeRabbitDraftKeys(t, string(s.dryRunWrites[codeRabbitFile]))
	}
}

// TestAdoptCodeRabbitDeclined keeps an explicit false when the repository declines management.
func TestAdoptCodeRabbitDeclined(t *testing.T) {
	repo := newTestRepo(t, "coderabbit-declined")
	const initial = "reviews:\n  auto_review:\n    drafts: false # intentional\n"
	mustWrite(t, filepath.Join(repo, ".coderabbit.yaml"), initial)
	mustWrite(t, filepath.Join(repo, manifestFile), "version: 1\nprofiles: [app-service]\nfacets: []\nadoption:\n  decline: [coderabbit]\n")
	_, err := Adopt(t.Context(), AdoptOptions{Path: repo, LockSourceRoot: newAdoptLockSource(t), SkipHookActivation: true})
	if err != nil {
		t.Fatalf("declined adoption: %v", err)
	}
	if got := mustRead(t, filepath.Join(repo, ".coderabbit.yaml")); got != initial {
		t.Fatalf("declined configuration changed: %q", got)
	}
}
