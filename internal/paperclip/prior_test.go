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
	writeRepoFile(t, repo, ".standards.yaml", "repository:\n  owner: acme\n  name: legacy\n  forge: forgejo\n")
	writeRepoFile(t, repo, ".paperclip/harness.json", harness)
	if rules != "" {
		writeRepoFile(t, repo, ".paperclip/rules.md", rules)
	}
	current, _, err := SynthesizeHarness(context.Background(), repo, unknownFacts)
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
// including the review-branch push #458 added, and the Caveman release. This release's
// renderings under other repository facts follow them (TestPriorGeneratedRecognisesThisRelease*).
func TestPriorGeneratedRecognisesEveryReleaseEra(t *testing.T) {
	_, probe := priorRepo(t, "{}", "")
	stated := statedPolicyOf(probe.Invariants)
	eras, err := priorHarnesses(probe, stated)
	if err != nil {
		t.Fatal(err)
	}
	earlier := len(priorRegisterDirectives)*len(priorAGitPushFormats) + 1
	if len(eras) != earlier+len(releasedPushRows())*len(releasedReceiptRows())*len(releaseFacts(stated))*len(releasedRegisterDirectives()) {
		t.Fatalf("release eras = %d, want every directive under every push protocol, the Caveman release and every fact combination", len(eras))
	}
	for index := range eras[:earlier] {
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

// A released rules.md reads as earlier output in either layout a release wrote, the
// unwrapped one before #477 or the markdownlint-clean one since, but only as the rendering
// of the harness beside it.
func TestPriorGeneratedAcceptsBothRulesLayoutsOfItsHarness(t *testing.T) {
	harness := priorFixture(t, "harness.json.golden")
	unwrapped := priorFixture(t, "rules.md.golden")
	_, probe := priorRepo(t, harness, unwrapped)
	golden := priorHarness(probe, priorRegisterDirectives[2], priorAGitPushFormats[0])
	otherPush := priorHarness(probe, priorRegisterDirectives[2], priorAGitPushFormats[1])
	if renderUnwrappedRules(&golden) != unwrapped {
		t.Fatal("unwrapped renderer no longer reproduces the released rules.md byte for byte")
	}
	cases := []struct {
		name      string
		rules     string
		generated bool
	}{
		{"unwrapped layout", unwrapped, true},
		{"markdownlint layout", renderRules(&golden), true},
		{"unwrapped rules of the other push era", renderUnwrappedRules(&otherPush), false},
		{"markdownlint rules of the other push era", renderRules(&otherPush), false},
		{"unwrapped layout without final newline", strings.TrimSuffix(unwrapped, "\n"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, current := priorRepo(t, harness, tc.rules)
			state, err := PriorGenerated(context.Background(), repo, current)
			if err != nil || state.Generated != tc.generated || !state.Rules {
				t.Fatalf("%s: prior=%+v err=%v, want generated=%v", tc.name, state, err, tc.generated)
			}
		})
	}
}

// cavemanFixture reads the harness.json the Caveman release (#487) wrote for praetor itself,
// with the platform renamed to acme/legacy.
func cavemanFixture(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "harness-714913e0", "harness.json.golden"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

// The Caveman release (#487) prescribed receipts on every repository. Its unmodified output
// still refreshes once the receipt row follows the pinned key. Positive: its harness.json,
// alone or beside its rules.md, is earlier output. Negative: an edited row is operator-owned.
// Boundary: the Caveman contract never shipped with the single AGit push, so that pairing is
// not earlier output, and neither is today's synthesis.
func TestPriorGeneratedRecognisesTheCavemanRelease(t *testing.T) {
	harness := cavemanFixture(t)
	_, probe := priorRepo(t, harness, "")
	released := cavemanHarness(probe)
	single := released
	single.AGitPushFormat = priorAGitPushFormats[0]
	singleJSON, err := MarshalHarness(&single)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, harness, rules string
		generated            bool
	}{
		{"harness alone", harness, "", true},
		{"harness and rules", harness, renderRules(&released), true},
		{"edited receipt row", strings.Replace(harness, "to all PR proposals", "to some PR proposals", 1), "", false},
		{"single push", string(singleJSON), "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, current := priorRepo(t, tc.harness, tc.rules)
			state, err := PriorGenerated(context.Background(), repo, current)
			if err != nil || state.Generated != tc.generated {
				t.Fatalf("%s: prior=%+v err=%v, want generated=%v", tc.name, state, err, tc.generated)
			}
		})
	}
	repo, current := priorRepo(t, "{}", "")
	if err := WriteHarness(current, repo); err != nil {
		t.Fatal(err)
	}
	if state, err := PriorGenerated(context.Background(), repo, current); err != nil || state.Generated {
		t.Fatalf("current synthesis read as the Caveman release: prior=%+v err=%v", state, err)
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
