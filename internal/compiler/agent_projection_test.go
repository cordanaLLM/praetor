// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package compiler

import (
	"bytes"
	"errors"
	"fmt"
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

// Negative: verify rejects a hand-edited native tools allow-list.
func TestVerifyAgentProjections_Negative_HandEditedTools(t *testing.T) {
	root := t.TempDir()
	srcDir := filepath.Join(root, filepath.FromSlash(CanonicalAgentsRel))
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const canonical = "---\nname: reviewer\ndescription: Review.\nmodel: pro\ntools: [read, search]\n---\n# Reviewer\n"
	if err := os.WriteFile(filepath.Join(srcDir, "reviewer.md"), []byte(canonical), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CompileAgentSurfaces(t.Context(), io.Discard, root); err != nil {
		t.Fatalf("CompileAgentSurfaces: %v", err)
	}
	claudeCopy := filepath.Join(root, ".claude", "agents", "reviewer.md")
	content, err := os.ReadFile(claudeCopy)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(content), "Grep", "Bash", 1)
	if edited == string(content) {
		t.Fatal("failed to find the projected Grep tool")
	}
	if err := os.WriteFile(claudeCopy, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	// The verifier names the projection by its slash path on every platform.
	if _, err := VerifyAgentProjections(t.Context(), root); !errors.Is(err, ErrAgentProjectionDrift) || !strings.Contains(err.Error(), ".claude/agents/reviewer.md") {
		t.Fatalf("VerifyAgentProjections() = %v, want tool-list drift", err)
	}
}

// TestVerifyAgentProjections_IndependentFailures checks that a missing copy cannot mask later
// drift or orphan checks, and that successful copies are still counted.
func TestVerifyAgentProjections_IndependentFailures(t *testing.T) {
	root := t.TempDir()
	const name = "reviewer.md"
	writeRegisterFixture(t, root, CanonicalAgentsRel+"/"+name,
		"---\nname: reviewer\ndescription: Review.\nmodel: pro\n---\n# Reviewer\n")
	if _, err := CompileAgentSurfaces(t.Context(), io.Discard, root); err != nil {
		t.Fatal(err)
	}
	dirs, err := agentProjectionDirs(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) < 2 {
		t.Fatal("fixture needs at least two projection directories")
	}
	missing, drift, orphan := dirs[0]+"/"+name, dirs[1]+"/"+name, dirs[1]+"/orphan.md"
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(missing))); err != nil {
		t.Fatal(err)
	}
	writeRegisterFixture(t, root, drift, "drifted\n")
	writeRegisterFixture(t, root, orphan, "orphan\n")
	verified, err := VerifyAgentProjections(t.Context(), root)
	assertProjectionFailures(t, err, missing, drift, orphan)
	if !errors.Is(err, os.ErrNotExist) || verified != len(dirs)-2 {
		t.Errorf("verified=%d err=%v; want %d and missing target", verified, err, len(dirs)-2)
	}
}

// assertProjectionFailures checks joined drift chains and every independently failing path.
func assertProjectionFailures(t *testing.T, err error, paths ...string) {
	t.Helper()
	if !errors.Is(err, ErrAgentProjectionDrift) {
		t.Fatalf("want projection drift: %v", err)
	}
	for _, path := range paths {
		if !strings.Contains(err.Error(), path) {
			t.Errorf("missing failure %s: %v", path, err)
		}
	}
}

// Every copy of every persona at the directory cap fails independently.
func TestVerifyAgentProjections_Boundary_AllCopiesFailAtCap(t *testing.T) {
	root := t.TempDir()
	writeRegisterFixture(t, root, PluginManifestRel, `{"name":"praetor"}`)
	dirs, err := agentProjectionDirs(t.Context(), root)
	if err != nil || len(dirs) == 0 {
		t.Fatalf("projection directories = %v, %v", dirs, err)
	}
	var paths []string
	for i := 0; i < maxAgentProjections; i++ {
		name := fmt.Sprintf("persona-%03d.md", i)
		writeRegisterFixture(t, root, CanonicalAgentsRel+"/"+name,
			"---\nname: reviewer\ndescription: Review.\nmodel: pro\n---\n# Reviewer\n")
		for _, dir := range dirs {
			paths = append(paths, dir+"/"+name)
		}
	}
	verified, err := VerifyAgentProjections(t.Context(), root)
	assertMissingProjections(t, verified, err, paths)
	writeRegisterFixture(t, root, CanonicalAgentsRel+"/over-cap.md", "# Extra\n")
	verified, err = VerifyAgentProjections(t.Context(), root)
	if verified != 0 || err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("over cap: verified=%d err=%v", verified, err)
	}
}

// Missing-copy aggregation preserves the chain, count and diagnostic for every expected path.
func assertMissingProjections(t *testing.T, verified int, err error, paths []string) {
	t.Helper()
	if verified != 0 || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("all missing: verified=%d err=%v", verified, err)
	}
	if got := strings.Count(err.Error(), "missing or unreadable"); got != len(paths) {
		t.Errorf("failures=%d; want %d", got, len(paths))
	}
	for _, path := range paths {
		if !strings.Contains(err.Error(), "projection "+path+" missing") {
			t.Errorf("missing failure: %s", path)
		}
	}
}

// Invalid canonical items never compare their copies; later personas still verify.
func TestVerifyAgentProjections_Boundary_CanonicalFailure(t *testing.T) {
	cases := []struct{ name, content string }{
		{"unreadable source", "\xff"},
		{"invalid model tier", "---\nname: bad\ndescription: Bad.\nmodel: inherit\n---\n# Bad\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeRegisterFixture(t, root, CanonicalAgentsRel+"/a-bad.md", tc.content)
			writeRegisterFixture(t, root, CanonicalAgentsRel+"/z-good.md",
				"---\nname: good\ndescription: Good.\nmodel: pro\n---\n# Good\n")
			verified, err := VerifyAgentProjections(t.Context(), root)
			if verified != 0 || err == nil || !strings.Contains(err.Error(), CanonicalAgentsRel+"/a-bad.md") {
				t.Fatalf("canonical failure: verified=%d err=%v", verified, err)
			}
			if strings.Contains(err.Error(), "/agents/a-bad.md missing") {
				t.Errorf("compared invalid canonical persona: %v", err)
			}
			if !strings.Contains(err.Error(), "/agents/z-good.md missing") {
				t.Errorf("later persona never checked: %v", err)
			}
		})
	}
}

// Positive: a tree without personas verifies nothing, and a compiled tree verifies every projection it wrote.
func TestVerifyAgentProjections_Positive_EmptyAndMatching(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(fmt.Sprintf("populated=%v", populated), func(t *testing.T) {
			root := t.TempDir()
			if populated {
				writeRegisterFixture(t, root, CanonicalAgentsRel+"/reviewer.md",
					"---\nname: reviewer\ndescription: Review.\nmodel: pro\n---\n# Reviewer\n")
			}
			files, err := CompileAgentSurfaces(t.Context(), io.Discard, root)
			if err != nil {
				t.Fatal(err)
			}
			verified, err := VerifyAgentProjections(t.Context(), root)
			if err != nil || verified != len(files) || (verified > 0) != populated {
				t.Fatalf("matching tree: verified=%d files=%d err=%v", verified, len(files), err)
			}
		})
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

// Positive: a read-only persona exposes only read and search tools in supported clients.
func TestTransformPersonaForDir_Positive_ReadOnlyToolAllowLists(t *testing.T) {
	const canonical = "---\nname: reviewer\ndescription: Review changes.\nmodel: pro\ntools: [read, search]\n---\n# Reviewer\n"
	const rel = CanonicalAgentsRel + "/reviewer.md"
	wants := map[string][]string{
		".claude/agents":   {"Read", "Grep", "Glob"},
		".github/agents":   {"read", "search"},
		".gemini/agents":   {"read_file", "list_directory", "grep_search", "glob"},
		".opencode/agents": {"read", "grep", "glob", "list"},
	}
	for dir, tools := range wants {
		got, err := TransformPersonaForDir(dir, rel, []byte(canonical))
		if err != nil {
			t.Fatalf("TransformPersonaForDir(%s): %v", dir, err)
		}
		for _, tool := range tools {
			if !strings.Contains(string(got), tool) {
				t.Errorf("%s missing allowed tool %q: %s", dir, tool, got)
			}
		}
		for _, denied := range []string{"Edit", "Write", "Bash", "edit", "execute", "run_shell_command", "write_file", "replace"} {
			if strings.Contains(string(got), denied) {
				t.Errorf("%s contains disallowed tool %q: %s", dir, denied, got)
			}
		}
	}
}

// Negative: unknown tool classes name the persona and the closed vocabulary.
func TestTransformPersonaForDir_Negative_UnknownToolClass(t *testing.T) {
	const rel = CanonicalAgentsRel + "/reviewer.md"
	_, err := TransformPersonaForDir(".claude/agents", rel, []byte("---\nname: reviewer\ndescription: Review.\nmodel: pro\ntools: [admin]\n---\n"))
	if err == nil || !strings.Contains(err.Error(), rel) || !strings.Contains(err.Error(), "read, search, edit, shell, web, task") {
		t.Fatalf("unknown tool class error = %v", err)
	}
}

// Boundary: an absent tools field preserves every projected byte from the prior compiler.
func TestTransformPersonaForDir_Boundary_ToolsOmittedUnchanged(t *testing.T) {
	const canonical = "---\nname: reviewer\ndescription: Review.\nmodel: pro\n---\n# Reviewer\n"
	got, err := TransformPersonaForDir(".github/agents", CanonicalAgentsRel+"/reviewer.md", []byte(canonical))
	if err != nil {
		t.Fatal(err)
	}
	const want = "---\nname: reviewer\ndescription: Review.\n---\n# Reviewer\n"
	if string(got) != want {
		t.Fatalf("projection changed: got %q, want %q", got, want)
	}
}

// Positive: Codex receives a visible limitation comment when a persona declares tool classes.
func TestTransformPersonaForDir_Positive_CodexToolPermissionGap(t *testing.T) {
	const canonical = "---\nname: reviewer\ndescription: Review.\nmodel: pro\ntools: [read, search]\n---\n# Reviewer\n"
	got, err := TransformPersonaForDir(".codex/agents", CanonicalAgentsRel+"/reviewer.md", []byte(canonical))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "tools:") || !strings.Contains(string(got), "TOOL PERMISSION GAP") {
		t.Fatalf("Codex projection does not state its tool-control gap: %s", got)
	}
}

// Positive: OpenCode gets a deny-by-default permission map with only requested classes allowed.
func TestTransformPersonaForDir_Positive_OpenCodePermissionMap(t *testing.T) {
	const canonical = "---\nname: reviewer\ndescription: Review.\nmodel: pro\ntools: [read, search]\n---\n# Reviewer\n"
	got, err := TransformPersonaForDir(".opencode/agents", CanonicalAgentsRel+"/reviewer.md", []byte(canonical))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"permission:", "'*': deny", "read: allow", "grep: allow", "glob: allow", "list: allow"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("OpenCode projection lacks %q: %s", want, got)
		}
	}
	if strings.Contains(string(got), "tools:") || strings.Contains(string(got), "edit: allow") {
		t.Errorf("OpenCode projection leaks tool list or edit access: %s", got)
	}
}

// Positive: the audit reports the Codex limitation for each restricted persona.
func TestPersonaToolPermissionGaps_Positive_CodexReported(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, filepath.FromSlash(CanonicalAgentsRel))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := "---\nname: reviewer\ndescription: Review.\nmodel: pro\ntools: [read, search]\n---\n# Reviewer\n"
	if err := os.WriteFile(filepath.Join(dir, "reviewer.md"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	gaps, err := PersonaToolPermissionGaps(t.Context(), root)
	if err != nil || len(gaps) != 1 || !strings.Contains(gaps[0], "reviewer.md") || !strings.Contains(gaps[0], "inherits parent permissions") {
		t.Fatalf("PersonaToolPermissionGaps() = %v, %v", gaps, err)
	}
}

// Positive: every defined client map includes the native tool names for all six classes.
func TestClientPersonaTools_Positive_CompleteMappings(t *testing.T) {
	classes := []string{"read", "search", "edit", "shell", "web", "task"}
	for _, target := range []string{".claude/agents", ".github/agents", ".gemini/agents", ".opencode/agents"} {
		tools, ok := clientPersonaTools(target, classes)
		if !ok || len(tools) == 0 {
			t.Errorf("clientPersonaTools(%s) = %v, %t", target, tools, ok)
		}
	}
}

// Negative: a client without a mapping is refused rather than given unrestricted access.
func TestClientPersonaTools_Negative_UnknownClient(t *testing.T) {
	if _, ok := clientPersonaTools(".agy/agents", []string{"read"}); ok {
		t.Fatal("Antigravity without verified tool mapping was accepted")
	}
}
