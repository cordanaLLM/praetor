// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/docstrings"
	"github.com/cordanaLLM/praetor/internal/util"
)

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
	for _, initial := range [2]string{at, string(got)} {
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
