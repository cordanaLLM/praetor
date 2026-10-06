// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agentcontext

import (
	"slices"
	"testing"
)

// Positive: every client selected keeps the one skill directory besides .agents/skills, Claude
// Code's, and so does a selection naming Claude Code.
func TestSkillDirs_Positive_ClaudeReadsItsOwnDirectory(t *testing.T) {
	for _, clients := range [][]string{nil, {"claude"}, {" Claude ", "codex"}} {
		selected, excluded, err := SkillDirs(clients)
		if err != nil || !slices.Equal(selected, []string{".claude/skills"}) || len(excluded) != 0 {
			t.Errorf("SkillDirs(%q) = %v, %v, %v", clients, selected, excluded, err)
		}
	}
}

// Negative: an unknown client id fails the selection, as it fails the context files.
func TestSkillDirs_Negative_UnknownClient(t *testing.T) {
	if _, _, err := SkillDirs([]string{"claud"}); err == nil {
		t.Fatal("an unknown client id was accepted")
	}
}

// Boundary: an empty selection and one of clients that read .agents/skills keep no directory
// and leave Claude Code's out.
func TestSkillDirs_Boundary_NoSeparateDirectory(t *testing.T) {
	for _, clients := range [][]string{{}, {"codex", "gemini", "cursor", "copilot", "windsurf"}} {
		selected, excluded, err := SkillDirs(clients)
		if err != nil || len(selected) != 0 || !slices.Equal(excluded, []string{".claude/skills"}) {
			t.Errorf("SkillDirs(%q) = %v, %v, %v", clients, selected, excluded, err)
		}
	}
}
