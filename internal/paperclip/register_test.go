package paperclip

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
)

// cavemanSkillRel is the canonical caveman skill a repository carries.
var cavemanSkillRel = compiler.CanonicalSkillRel("caveman")

// plainInternalDirective is the internal register directive without the skill's name.
var plainInternalDirective = config.RegisterDirectiveWithout(config.TextRegisterInternal, config.RegisterSkills())

// A Paperclip run reports to an orchestrating agent, so the contract names the internal
// register as its last line, and the rendered rules and a reload keep it. A repository that
// carries the caveman skill gets the directive naming it.
func TestHarnessContractStatesTheInternalRegister(t *testing.T) {
	repo := identifiedRepo(t)
	writeRepoFile(t, repo, cavemanSkillRel, "---\nname: caveman\ndescription: fixture\n---\n")
	h, _, err := SynthesizeHarness(t.Context(), repo, unknownFacts)
	if err != nil {
		t.Fatal(err)
	}
	directive := config.RegisterDirective(config.TextRegisterInternal)
	if len(h.OperatingContract) != 6 || h.OperatingContract[5] != directive || !strings.Contains(directive, "`caveman` skill:") {
		t.Fatalf("contract = %q, want the internal register directive naming the skill as the sixth line", h.OperatingContract)
	}
	for _, other := range []config.TextRegister{config.TextRegisterSocial, config.TextRegisterDocs} {
		if strings.Contains(strings.Join(h.OperatingContract, "\n"), config.RegisterDirective(other)) {
			t.Fatalf("contract must not name the %s register", other)
		}
	}
	if err := validateHarnessValues(h.OperatingContract); err != nil {
		t.Fatalf("the extended contract must stay within the harness bounds: %v", err)
	}
	// List items wrap at the Markdown line limit; joining the two-space continuation lines
	// restores each item to the one line the directive is compared against.
	if rules := renderRules(h); strings.Count(strings.ReplaceAll(rules, "\n  ", " "), "- "+directive+"\n") != 1 {
		t.Fatalf("rendered rules must list the directive once:\n%s", rules)
	}
}

// Negative: a repository without the caveman skill gets the internal form without a skill name,
// so the harness adoption writes points at nothing the repository lacks (#235). The harness an
// earlier release wrote there, naming the skill, is still earlier output, so adoption refreshes
// it rather than keeping it as the operator's.
func TestHarnessContractNamesNoAbsentSkill(t *testing.T) {
	repo := identifiedRepo(t)
	writeRepoFile(t, repo, compiler.CanonicalSkillRel("social-text"), "---\nname: social-text\ndescription: fixture\n---\n")
	h, _, err := SynthesizeHarness(t.Context(), repo, unknownFacts)
	if err != nil {
		t.Fatal(err)
	}
	if h.OperatingContract[5] != plainInternalDirective || strings.Contains(strings.Join(h.OperatingContract, "\n"), "`caveman`") {
		t.Fatalf("contract = %q, want the internal form without the absent caveman skill", h.OperatingContract)
	}
	if !strings.HasPrefix(plainInternalDirective, "Text register internal: fragments, no filler") {
		t.Fatalf("plain directive = %q", plainInternalDirective)
	}
	named := *h
	named.OperatingContract = append([]string(nil), h.OperatingContract...)
	named.OperatingContract[5] = config.RegisterDirective(config.TextRegisterInternal)
	if err := WriteHarness(&named, repo); err != nil {
		t.Fatal(err)
	}
	state, err := PriorGenerated(context.Background(), repo, h)
	if err != nil || !state.Generated {
		t.Fatalf("the harness naming the skill is not earlier output where the skill is absent: %+v %v", state, err)
	}
}

// Boundary: a skill the caller installs before the harness lands (pending) counts as carried,
// a pending name that is no register skill changes nothing, and neither is a substitution. A
// caveman path the confined read refuses counts as absent, so the directive names no skill a run
// cannot open and the harness still synthesizes, and the substitution names the refused path, so
// the plain directive never lands unreported.
func TestSynthesizeHarnessOver_Boundary(t *testing.T) {
	repo := identifiedRepo(t)
	named, substitution, err := SynthesizeHarnessOver(t.Context(), repo, unknownFacts, []string{"caveman"})
	if err != nil || substitution != "" || named.OperatingContract[5] != config.RegisterDirective(config.TextRegisterInternal) {
		t.Fatalf("pending caveman: contract = %+v, substitution = %q, err = %v", named, substitution, err)
	}
	plain, substitution, err := SynthesizeHarnessOver(t.Context(), repo, unknownFacts, []string{"adhd-format", "unknown"})
	if err != nil || substitution != "" || plain.OperatingContract[5] != plainInternalDirective {
		t.Fatalf("pending without caveman: contract = %+v, substitution = %q, err = %v", plain, substitution, err)
	}
	unreadable := identifiedRepo(t)
	if err := os.MkdirAll(filepath.Join(unreadable, filepath.FromSlash(cavemanSkillRel)), 0o755); err != nil {
		t.Fatal(err)
	}
	h, substitution, err := SynthesizeHarness(t.Context(), unreadable, unknownFacts)
	if err != nil || h.OperatingContract[5] != plainInternalDirective {
		t.Fatalf("a directory at %s: harness = %+v, err = %v; want the directive without the skill", cavemanSkillRel, h, err)
	}
	if !strings.Contains(substitution, "names no skill") || !strings.Contains(substitution, "read canonical skill caveman") {
		t.Fatalf("a directory at %s: substitution = %q, want the plain directive reported with the refused read", cavemanSkillRel, substitution)
	}
}

// Negative: the skill read runs under the caller's context. A cancelled context fails it, and the
// failure is an error rather than a substitution, so a cancelled run neither reads on for the
// read's own bound nor writes a harness stating the plain directive.
func TestHarnessAbsentSkills_Negative_CancelledContextFails(t *testing.T) {
	repo := identifiedRepo(t)
	writeRepoFile(t, repo, cavemanSkillRel, "---\nname: caveman\ndescription: fixture\n---\n")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	absent, substitution, err := harnessAbsentSkills(ctx, repo, nil)
	if !errors.Is(err, context.Canceled) || substitution != "" || absent != nil {
		t.Fatalf("cancelled read: absent = %v, substitution = %q, err = %v; want context.Canceled alone", absent, substitution, err)
	}
	if absent, substitution, err := harnessAbsentSkills(t.Context(), repo, nil); err != nil || substitution != "" || slices.Contains(absent, "caveman") {
		t.Fatalf("live read: absent = %v, substitution = %q, err = %v; want caveman carried", absent, substitution, err)
	}
}
