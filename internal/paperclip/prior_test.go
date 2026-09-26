package paperclip

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// priorFixture reads a golden harness file an earlier release wrote. CRLF is folded so a
// Windows checkout with core.autocrlf compares the bytes the release produced.
func priorFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "harness-462e3f3a", name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

// priorRepo writes a manifest naming acme/legacy plus the given harness files.
func priorRepo(t *testing.T, harness, rules string) (string, *Harness) {
	t.Helper()
	repo := t.TempDir()
	writeRepoFile(t, repo, ".standards.yaml", "repository:\n  owner: acme\n  name: legacy\n")
	writeRepoFile(t, repo, ".paperclip/harness.json", harness)
	if rules != "" {
		writeRepoFile(t, repo, ".paperclip/rules.md", rules)
	}
	current, err := SynthesizeHarness(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	return repo, current
}

func TestPriorGeneratedRecognisesReleasedHarness(t *testing.T) {
	repo, current := priorRepo(t, priorFixture(t, "harness.json.golden"), priorFixture(t, "rules.md.golden"))
	prior, err := PriorGenerated(context.Background(), repo, current)
	if err != nil || !prior.Generated || !prior.Rules {
		t.Fatalf("462e3f3a harness not recognised as earlier output: prior=%+v err=%v", prior, err)
	}
	// The same release after #458 added the review-branch push to the AGit protocol.
	const single, withReview = "topic=<issue-id>", "topic=<issue-id> && git push origin HEAD:refs/heads/paperclip/<issue-id>"
	const singleJSON, withReviewJSON = "topic=\\u003cissue-id\\u003e\"", "topic=\\u003cissue-id\\u003e \\u0026\\u0026 " +
		"git push origin HEAD:refs/heads/paperclip/\\u003cissue-id\\u003e\""
	harness := strings.Replace(priorFixture(t, "harness.json.golden"), singleJSON, withReviewJSON, 1)
	rules := strings.Replace(priorFixture(t, "rules.md.golden"), single, withReview, 1)
	if !strings.Contains(harness, "refs/heads/paperclip/") || !strings.Contains(rules, "refs/heads/paperclip/") {
		t.Fatal("fixture rewrite to the #458 push protocol missed")
	}
	repo, current = priorRepo(t, harness, rules)
	if prior, err = PriorGenerated(context.Background(), repo, current); err != nil || !prior.Generated {
		t.Fatalf("#458 harness not recognised as earlier output: prior=%+v err=%v", prior, err)
	}
}

// Every release era is recognised: each register directive form under each push protocol,
// including the review-branch push #458 added.
func TestPriorGeneratedRecognisesEveryReleaseEra(t *testing.T) {
	_, probe := priorRepo(t, "{}", "")
	eras := priorHarnesses(probe)
	if len(eras) != len(priorRegisterDirectives)*len(priorAGitPushFormats) {
		t.Fatalf("release eras = %d, want every directive under every push protocol", len(eras))
	}
	for index := range eras {
		repo, current := priorRepo(t, "{}", "")
		prior := eras[index]
		if err := WriteHarness(&prior, repo); err != nil {
			t.Fatal(err)
		}
		if state, err := PriorGenerated(context.Background(), repo, current); err != nil || !state.Generated {
			t.Fatalf("release era %d (%q) not recognised: prior=%+v err=%v", index, prior.AGitPushFormat, state, err)
		}
	}
}

func TestPriorGeneratedRejectsOperatorEdits(t *testing.T) {
	harness := priorFixture(t, "harness.json.golden")
	rules := priorFixture(t, "rules.md.golden")
	cases := map[string][2]string{
		"edited contract":   {strings.Replace(harness, "NOT shipping", "not shipping", 1), rules},
		"edited rules":      {harness, rules + "- local rule\n"},
		"other platform":    {strings.Replace(harness, "acme/legacy", "acme/other", 1), rules},
		"other push format": {strings.Replace(harness, "refs/for/main", "refs/for/dev", 1), rules},
		"current synthesis": {"", ""},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			repo, current := priorRepo(t, files[0], files[1])
			if name == "current synthesis" {
				if err := WriteHarness(current, repo); err != nil {
					t.Fatal(err)
				}
			}
			if state, err := PriorGenerated(context.Background(), repo, current); err != nil || state.Generated {
				t.Fatalf("%s treated as unmodified earlier output: prior=%+v err=%v", name, state, err)
			}
		})
	}
}

func TestPriorGeneratedBoundaries(t *testing.T) {
	repo, current := priorRepo(t, priorFixture(t, "harness.json.golden"), "")
	if state, err := PriorGenerated(context.Background(), repo, current); err != nil || !state.Generated || state.Rules {
		t.Fatalf("earlier harness without rules.md: prior=%+v err=%v", state, err)
	}
	if _, err := PriorGenerated(context.Background(), t.TempDir(), current); err == nil {
		t.Fatal("missing harness.json reported as earlier output without error")
	}
	if _, err := PriorGenerated(nil, repo, current); err == nil { //nolint:staticcheck // exercising the nil-context contract
		t.Fatal("nil context accepted")
	}
	if _, err := PriorGenerated(context.Background(), repo, nil); err == nil {
		t.Fatal("nil current harness accepted")
	}
}

// crlf renders text the way a Windows checkout with core.autocrlf=true writes it.
func crlf(text string) string {
	return strings.ReplaceAll(text, "\n", "\r\n")
}

func TestPriorGeneratedFoldsCheckoutLineEndings(t *testing.T) {
	harness := priorFixture(t, "harness.json.golden")
	rules := priorFixture(t, "rules.md.golden")
	cases := map[string][2]string{
		"crlf harness and rules": {crlf(harness), crlf(rules)},
		"crlf harness only":      {crlf(harness), rules},
		"crlf harness no rules":  {crlf(harness), ""},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			repo, current := priorRepo(t, files[0], files[1])
			if state, err := PriorGenerated(context.Background(), repo, current); err != nil || !state.Generated {
				t.Fatalf("%s checkout not recognised as earlier output: prior=%+v err=%v", name, state, err)
			}
		})
	}
}

func TestPriorGeneratedRejectsMixedLineEndings(t *testing.T) {
	harness := priorFixture(t, "harness.json.golden")
	rules := priorFixture(t, "rules.md.golden")
	firstLine := strings.Index(harness, "\n")
	cases := map[string][2]string{
		"mixed harness":     {harness[:firstLine] + "\r" + harness[firstLine:], rules},
		"lone cr harness":   {strings.Replace(harness, "\n", "\r", 1), rules},
		"mixed rules":       {harness, strings.Replace(rules, "\n", "\r\n", 1)},
		"crlf edited rules": {crlf(harness), crlf(rules + "- local rule\n")},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			repo, current := priorRepo(t, files[0], files[1])
			if state, err := PriorGenerated(context.Background(), repo, current); err != nil || state.Generated {
				t.Fatalf("%s treated as unmodified earlier output: prior=%+v err=%v", name, state, err)
			}
		})
	}
}

func TestWriteHarnessFilesRulesFlag(t *testing.T) {
	for _, rules := range []bool{true, false} {
		repo, current := priorRepo(t, "{}", "")
		if err := WriteHarnessFiles(current, repo, rules); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(repo, ".paperclip", "harness.json"))
		if err != nil || !strings.Contains(string(data), current.Platform) {
			t.Fatalf("rules=%v: harness.json not written: %v", rules, err)
		}
		_, statErr := os.Stat(filepath.Join(repo, ".paperclip", "rules.md"))
		if written := statErr == nil; written != rules {
			t.Fatalf("rules=%v: rules.md written=%v", rules, written)
		}
	}
	if err := WriteHarnessFiles(nil, t.TempDir(), false); err == nil {
		t.Fatal("nil harness accepted")
	}
}
