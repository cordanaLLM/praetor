// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package compiler

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Positive: flash persona projects haiku into .claude/agents, flash into plugin copy,
// and no model line into .github/agents, .gemini/agents, .codex/agents.
func TestTransformPersonaForDir_Positive_FlashTierProjection(t *testing.T) {
	const canonical = "---\nname: worker\ndescription: Mechanical worker.\nmodel: flash\nmainAgent: true\n---\n\n# Worker\n\nRun tasks.\n"
	const rel = CanonicalAgentsRel + "/worker.md"

	claude, err := TransformPersonaForDir(".claude/agents", rel, []byte(canonical))
	if err != nil {
		t.Fatalf(".claude/agents transform failed: %v", err)
	}
	if !strings.Contains(string(claude), "model: haiku\n") {
		t.Errorf(".claude/agents want model: haiku, got:\n%s", string(claude))
	}
	if strings.Contains(string(claude), "model: flash") {
		t.Errorf(".claude/agents still contains model: flash:\n%s", string(claude))
	}

	plugin, err := TransformPersonaForDir(PluginAgentsRel, rel, []byte(canonical))
	if err != nil {
		t.Fatalf("%s transform failed: %v", PluginAgentsRel, err)
	}
	if !bytes.Equal(plugin, []byte(canonical)) {
		t.Errorf("%s want byte-identical copy, got:\n%s", PluginAgentsRel, string(plugin))
	}

	for _, dir := range []string{".github/agents", ".gemini/agents", ".codex/agents"} {
		out, err := TransformPersonaForDir(dir, rel, []byte(canonical))
		if err != nil {
			t.Fatalf("%s transform failed: %v", dir, err)
		}
		if strings.Contains(string(out), "model:") {
			t.Errorf("%s still contains model line:\n%s", dir, string(out))
		}
		if !strings.Contains(string(out), "name: worker\n") || !strings.Contains(string(out), "# Worker\n") {
			t.Errorf("%s corrupted other front matter or body:\n%s", dir, string(out))
		}
	}
}

// Positive: pro persona projects sonnet into .claude/agents, pro into plugin copy,
// and no model line into .github/agents.
func TestTransformPersonaForDir_Positive_ProTierProjection(t *testing.T) {
	const canonical = "---\nname: auditor\ndescription: Governance auditor.\nmodel: pro\nmainAgent: true\n---\n\n# Auditor\n\nRun sweeps.\n"
	const rel = CanonicalAgentsRel + "/auditor.md"

	claude, err := TransformPersonaForDir(".claude/agents", rel, []byte(canonical))
	if err != nil {
		t.Fatalf(".claude/agents transform failed: %v", err)
	}
	if !strings.Contains(string(claude), "model: sonnet\n") {
		t.Errorf(".claude/agents want model: sonnet, got:\n%s", string(claude))
	}

	plugin, err := TransformPersonaForDir(PluginAgentsRel, rel, []byte(canonical))
	if err != nil {
		t.Fatalf("%s transform failed: %v", PluginAgentsRel, err)
	}
	if !bytes.Equal(plugin, []byte(canonical)) {
		t.Errorf("%s want byte-identical copy", PluginAgentsRel)
	}

	github, err := TransformPersonaForDir(".github/agents", rel, []byte(canonical))
	if err != nil {
		t.Fatalf(".github/agents transform failed: %v", err)
	}
	if strings.Contains(string(github), "model:") {
		t.Errorf(".github/agents still contains model line:\n%s", string(github))
	}
}

// Negative: persona without model refused, model inherit refused, and model sonnet in canonical refused.
func TestTransformPersonaForDir_Negative_RefusesInvalidModelTiers(t *testing.T) {
	const rel = CanonicalAgentsRel + "/invalid.md"
	cases := map[string]struct {
		data string
		want string
	}{
		"missing model": {
			data: "---\nname: helper\ndescription: Helper.\n---\n# Body\n",
			want: "lacks the required key \"model\" (allowed values: flash, pro)",
		},
		"model inherit": {
			data: "---\nname: helper\ndescription: Helper.\nmodel: inherit\n---\n# Body\n",
			want: `model "inherit" is invalid (allowed values: flash, pro)`,
		},
		"model sonnet in canonical": {
			data: "---\nname: helper\ndescription: Helper.\nmodel: sonnet\n---\n# Body\n",
			want: `model "sonnet" is invalid (allowed values: flash, pro)`,
		},
		"model empty": {
			data: "---\nname: helper\ndescription: Helper.\nmodel: \"\"\n---\n# Body\n",
			want: "lacks the required key \"model\" (allowed values: flash, pro)",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := TransformPersonaForDir(".claude/agents", rel, []byte(tc.data))
			if err == nil {
				t.Fatalf("%s: expected error, got nil", name)
			}
			if !strings.Contains(err.Error(), rel) {
				t.Errorf("%s: error %q does not name file %q", name, err.Error(), rel)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s: error %q does not contain %q", name, err.Error(), tc.want)
			}
			if !strings.Contains(err.Error(), "flash, pro") {
				t.Errorf("%s: error %q does not name allowed values (flash, pro)", name, err.Error())
			}
		})
	}
}

// Negative: VerifyAgentProjections flags a .claude/agents copy whose model was hand-edited to opus.
func TestVerifyAgentProjections_Negative_HandEditedToOpus(t *testing.T) {
	root := t.TempDir()
	srcDir := filepath.Join(root, filepath.FromSlash(CanonicalAgentsRel))
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const canonical = "---\nname: auditor\ndescription: Auditor.\nmodel: pro\n---\n# Auditor\n"
	if err := os.WriteFile(filepath.Join(srcDir, "auditor.md"), []byte(canonical), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CompileAgentSurfaces(t.Context(), io.Discard, root); err != nil {
		t.Fatalf("CompileAgentSurfaces: %v", err)
	}
	if _, err := VerifyAgentProjections(t.Context(), root); err != nil {
		t.Fatalf("clean write must verify: %v", err)
	}

	claudeCopy := filepath.Join(root, ".claude", "agents", "auditor.md")
	content, err := os.ReadFile(claudeCopy)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(content), "model: sonnet", "model: opus", 1)
	if edited == string(content) {
		t.Fatal("failed to find model: sonnet to replace with model: opus")
	}
	if err := os.WriteFile(claudeCopy, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = VerifyAgentProjections(t.Context(), root)
	if err == nil {
		t.Fatal("VerifyAgentProjections accepted hand-edited opus model")
	}
	if !errors.Is(err, ErrAgentProjectionDrift) {
		t.Errorf("want ErrAgentProjectionDrift, got: %v", err)
	}
	if !strings.Contains(err.Error(), ".claude/agents/auditor.md") {
		t.Errorf("error %v does not name .claude/agents/auditor.md", err)
	}
}

// Boundary: CRLF persona keeps CRLF and only the model line changes.
func TestTransformPersonaForDir_Boundary_CRLFLineEndings(t *testing.T) {
	const canonicalCRLF = "---\r\nname: helper\r\ndescription: Helper.\r\nmodel: flash\r\nmainAgent: true\r\n---\r\n\r\n# Helper\r\n\r\nLine 1.\r\nLine 2.\r\n"
	const rel = CanonicalAgentsRel + "/helper.md"

	claude, err := TransformPersonaForDir(".claude/agents", rel, []byte(canonicalCRLF))
	if err != nil {
		t.Fatalf("transform CRLF for .claude/agents failed: %v", err)
	}
	if bytes.Contains(claude, []byte("\r\r")) {
		t.Fatal("contains double carriage return")
	}
	for _, line := range strings.Split(string(claude), "\n") {
		if line != "" && !strings.HasSuffix(line, "\r") {
			t.Errorf("line does not end with CR in CRLF output: %q", line)
		}
	}
	const wantClaude = "---\r\nname: helper\r\ndescription: Helper.\r\nmodel: haiku\r\nmainAgent: true\r\n---\r\n\r\n# Helper\r\n\r\nLine 1.\r\nLine 2.\r\n"
	if string(claude) != wantClaude {
		t.Errorf("CRLF transform mismatch:\ngot:  %q\nwant: %q", string(claude), wantClaude)
	}

	github, err := TransformPersonaForDir(".github/agents", rel, []byte(canonicalCRLF))
	if err != nil {
		t.Fatalf("transform CRLF for .github/agents failed: %v", err)
	}
	const wantGithub = "---\r\nname: helper\r\ndescription: Helper.\r\nmainAgent: true\r\n---\r\n\r\n# Helper\r\n\r\nLine 1.\r\nLine 2.\r\n"
	if string(github) != wantGithub {
		t.Errorf("CRLF removal mismatch:\ngot:  %q\nwant: %q", string(github), wantGithub)
	}
}

// Boundary: front matter with model only in body text untouched.
func TestTransformPersonaForDir_Boundary_ModelInBodyUntouched(t *testing.T) {
	const canonical = "---\nname: helper\ndescription: Helper.\nmodel: flash\n---\n# Helper\n\nNotes:\nmodel: flash\nmodel: pro\nmodel: inherit\n\n```yaml\n---\nmodel: flash\n---\n```\n"
	const rel = CanonicalAgentsRel + "/helper.md"

	const expectedBody = "# Helper\n\nNotes:\nmodel: flash\nmodel: pro\nmodel: inherit\n\n```yaml\n---\nmodel: flash\n---\n```\n"

	claude, err := TransformPersonaForDir(".claude/agents", rel, []byte(canonical))
	if err != nil {
		t.Fatalf("transform .claude/agents: %v", err)
	}
	_, claudeBody, found := strings.Cut(string(claude)[4:], "\n---\n")
	if !found || claudeBody != expectedBody {
		t.Errorf(".claude/agents body was altered:\ngot:  %q\nwant: %q", claudeBody, expectedBody)
	}

	github, err := TransformPersonaForDir(".github/agents", rel, []byte(canonical))
	if err != nil {
		t.Fatalf("transform .github/agents: %v", err)
	}
	_, githubBody, found := strings.Cut(string(github)[4:], "\n---\n")
	if !found || githubBody != expectedBody {
		t.Errorf(".github/agents body was altered:\ngot:  %q\nwant: %q", githubBody, expectedBody)
	}
}
