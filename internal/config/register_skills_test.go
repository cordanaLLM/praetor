// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"slices"
	"strings"
	"testing"
)

func mustRenderRegister(t *testing.T, p RegisterPolicy) string {
	t.Helper()
	block, err := RenderRegisterBlock(p, true)
	if err != nil {
		t.Fatal(err)
	}
	return block
}

// Positive: the register skills are social-text and caveman, in register order, and a policy
// that lacks none renders, and directs, with both named exactly as before #235.
func TestRegisterSkills_Positive_NamedByDefault(t *testing.T) {
	if got := RegisterSkills(); !slices.Equal(got, []string{"social-text", "caveman"}) {
		t.Fatalf("RegisterSkills() = %v", got)
	}
	// The bundle adds adhd-format, which social-text inherits from, and a caller appending to
	// one result never changes the next.
	bundle := RegisterSkillBundle()
	if !slices.Equal(bundle, []string{"social-text", "caveman", "adhd-format"}) {
		t.Fatalf("RegisterSkillBundle() = %v", bundle)
	}
	bundle[0] = "changed"
	if got := RegisterSkillBundle(); got[0] != "social-text" || len(RegisterSkills()) != 2 {
		t.Fatalf("RegisterSkillBundle shares its backing array: %v", got)
	}
	block := mustRenderRegister(t, DefaultRegisterPolicy())
	for _, named := range []string{"| `social-text` skill: BLUF", "| `caveman` skill: fragments", "`caveman` brief shape with `task:`"} {
		if !strings.Contains(block, named) {
			t.Errorf("default block lacks %q", named)
		}
	}
	if got := RegisterDirective(TextRegisterInternal); !strings.HasPrefix(got, "Text register internal: `caveman` skill: fragments") {
		t.Errorf("RegisterDirective(internal) = %q", got)
	}
}

// Negative: a policy lacking both skills renders the same forms without naming either skill,
// and the subagent brief rule without the caveman brief shape.
func TestWithAbsentSkills_Negative_NamesNoAbsentSkill(t *testing.T) {
	block := mustRenderRegister(t, DefaultRegisterPolicy().WithAbsentSkills(RegisterSkills()))
	if strings.Contains(block, "`social-text`") || strings.Contains(block, "`caveman`") {
		t.Fatalf("block names an absent skill:\n%s", block)
	}
	for _, form := range []string{"| BLUF, full sentences", "| fragments, no filler",
		"Subagent launch brief: internal register with `task:` = routing label; registered dispatch hook"} {
		if !strings.Contains(block, form) {
			t.Errorf("block lacks %q:\n%s", form, block)
		}
	}
}

// Boundary: each skill is dropped on its own, a name that is no register skill and an empty
// list change nothing, the receiver is not changed, and a convention still follows the form.
func TestWithAbsentSkills_Boundary(t *testing.T) {
	base := DefaultRegisterPolicy()
	base.Conventions = map[TextRegister]string{TextRegisterSocial: "release notes as fragments"}
	social := mustRenderRegister(t, base.WithAbsentSkills([]string{"social-text"}))
	if strings.Contains(social, "`social-text`") || !strings.Contains(social, "`caveman` skill:") ||
		!strings.Contains(social, "| BLUF, full sentences, scannable, enough and no more; conventional commit subject unchanged; release notes as fragments |") {
		t.Fatalf("social-text absent:\n%s", social)
	}
	plain := mustRenderRegister(t, base)
	for _, absent := range [][]string{nil, {}, {"adhd-format", "unknown"}} {
		if got := mustRenderRegister(t, base.WithAbsentSkills(absent)); got != plain {
			t.Errorf("WithAbsentSkills(%v) changed the block:\n%s", absent, got)
		}
	}
	_ = base.WithAbsentSkills(RegisterSkills())
	if got := mustRenderRegister(t, base); got != plain {
		t.Error("WithAbsentSkills changed its receiver")
	}
}

// Positive: RegisterDirectiveWithout with no absent skill is RegisterDirective, naming the skill.
// Negative: with the register's skill absent it states the form alone. Boundary: a skill of
// another register, an unknown name, and a register without a skill change nothing, and an
// unknown register yields "".
func TestRegisterDirectiveWithout(t *testing.T) {
	named := RegisterDirective(TextRegisterInternal)
	if got := RegisterDirectiveWithout(TextRegisterInternal, nil); got != named || !strings.Contains(got, "`caveman` skill:") {
		t.Fatalf("no absent skill: %q", got)
	}
	plain := "Text register internal: fragments, no filler, verbatim code/paths/errors; facts, paths, commands, verdict."
	if got := RegisterDirectiveWithout(TextRegisterInternal, []string{"caveman"}); got != plain {
		t.Fatalf("caveman absent: %q", got)
	}
	if got := RegisterDirectiveWithout(TextRegisterInternal, []string{"social-text", "unknown"}); got != named {
		t.Fatalf("another register's skill absent: %q", got)
	}
	if got := RegisterDirectiveWithout(TextRegisterDocs, RegisterSkills()); got != RegisterDirective(TextRegisterDocs) {
		t.Fatalf("docs, which has no skill: %q", got)
	}
	if got := RegisterDirectiveWithout(TextRegister("unknown"), nil); got != "" {
		t.Fatalf("unknown register: %q", got)
	}
}
