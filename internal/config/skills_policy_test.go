// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillsPolicy_Positive_ValidUpstream(t *testing.T) {
	manifestYAML := `version: 1
skills:
  upstream:
    - repo: https://github.com/example/skills
      tag: v0.1.0
      commit: 0123456789abcdef0123456789abcdef01234567
      skills:
        - helper-skill
        - analysis-tool
`
	m, err := ParseManifest("test", []byte(manifestYAML))
	if err != nil {
		t.Fatalf("ParseManifest failed: %v", err)
	}
	if m.Skills == nil {
		t.Fatal("m.Skills is nil, want non-nil")
	}
	sources := m.Skills.UpstreamSources()
	if len(sources) != 1 {
		t.Fatalf("len(sources) = %d, want 1", len(sources))
	}
	src := sources[0]
	if src.Repo != "https://github.com/example/skills" {
		t.Errorf("src.Repo = %q", src.Repo)
	}
	if src.Tag != "v0.1.0" {
		t.Errorf("src.Tag = %q", src.Tag)
	}
	if src.Commit != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("src.Commit = %q", src.Commit)
	}
	if len(src.Skills) != 2 || src.Skills[0] != "helper-skill" || src.Skills[1] != "analysis-tool" {
		t.Errorf("src.Skills = %v", src.Skills)
	}
}

func TestSkillsPolicy_Negative_InvalidCommit(t *testing.T) {
	cases := []struct {
		name   string
		commit string
	}{
		{"empty", ""},
		{"short", "0123456789abcdef"},
		{"long", "0123456789abcdef0123456789abcdef0123456789a"},
		{"non-hex", "0123456789abcdef0123456789abcdef0123456g"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifestYAML := `version: 1
skills:
  upstream:
    - repo: https://github.com/example/skills
      tag: v0.1.0
      commit: ` + tc.commit + `
      skills:
        - helper-skill
`
			_, err := ParseManifest("test", []byte(manifestYAML))
			if err == nil {
				t.Fatalf("expected error for commit %q, got nil", tc.commit)
			}
			if !strings.Contains(err.Error(), "40 hex characters") {
				t.Errorf("expected error mentioning 40 hex characters, got: %v", err)
			}
		})
	}
}

func TestSkillsPolicy_Negative_InvalidSkills(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name: "empty skills list",
			content: `version: 1
skills:
  upstream:
    - repo: https://github.com/example/skills
      tag: v0.1.0
      commit: 0123456789abcdef0123456789abcdef01234567
      skills: []
`,
			wantErr: "at least one skill",
		},
		{
			name: "duplicate skill name",
			content: `version: 1
skills:
  upstream:
    - repo: https://github.com/example/skills
      tag: v0.1.0
      commit: 0123456789abcdef0123456789abcdef01234567
      skills:
        - skill-a
        - skill-a
`,
			wantErr: "duplicate skill name",
		},
		{
			name: "invalid skill name with slash",
			content: `version: 1
skills:
  upstream:
    - repo: https://github.com/example/skills
      tag: v0.1.0
      commit: 0123456789abcdef0123456789abcdef01234567
      skills:
        - ../escape
`,
			wantErr: "not a valid skill name",
		},
		{
			name: "empty repo",
			content: `version: 1
skills:
  upstream:
    - repo: ""
      tag: v0.1.0
      commit: 0123456789abcdef0123456789abcdef01234567
      skills:
        - skill-a
`,
			wantErr: "valid repository URL or path",
		},
		{
			name: "empty tag",
			content: `version: 1
skills:
  upstream:
    - repo: https://github.com/example/skills
      tag: ""
      commit: 0123456789abcdef0123456789abcdef01234567
      skills:
        - skill-a
`,
			wantErr: "non-empty tag name",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseManifest("test", []byte(tc.content))
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestSkillsPolicy_Boundary_EmptyUpstreamChangesNothing(t *testing.T) {
	manifests := []string{
		`version: 1
skills:
  upstream: []
`,
		`version: 1
skills: {}
`,
		`version: 1
`,
	}
	for i, ym := range manifests {
		m, err := ParseManifest("test", []byte(ym))
		if err != nil {
			t.Fatalf("manifest[%d] failed: %v", i, err)
		}
		if len(m.Skills.UpstreamSources()) != 0 {
			t.Errorf("manifest[%d] expected empty sources", i)
		}
	}
}

func TestSkillsPolicy_Positive_UpstreamSkillSourceFor(t *testing.T) {
	policy := &SkillsPolicy{
		Upstream: []UpstreamSkillSource{
			{
				Repo:   "https://github.com/example/skills",
				Tag:    "v1.0.0",
				Commit: "0123456789abcdef0123456789abcdef01234567",
				Skills: []string{"calc", "helper"},
			},
		},
	}
	src, found := policy.UpstreamSkillSourceFor("calc")
	if !found {
		t.Fatal("expected found=true for calc")
	}
	if src.Repo != "https://github.com/example/skills" || src.Commit != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("unexpected src: %+v", src)
	}

	_, found = policy.UpstreamSkillSourceFor("missing")
	if found {
		t.Fatal("expected found=false for missing")
	}

	var nilPolicy *SkillsPolicy
	_, found = nilPolicy.UpstreamSkillSourceFor("calc")
	if found {
		t.Fatal("expected found=false on nil policy")
	}
}

func TestLoadSkillsPolicy(t *testing.T) {
	dir := t.TempDir()
	// No manifest
	pol, err := LoadSkillsPolicy(dir)
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if pol != nil {
		t.Fatalf("expected nil policy, got %+v", pol)
	}

	// Manifest with skills
	manifestContent := `version: 1
skills:
  upstream:
    - repo: https://github.com/example/skills
      tag: v1.0.0
      commit: 0123456789abcdef0123456789abcdef01234567
      skills:
        - my-skill
`
	if err := os.WriteFile(filepath.Join(dir, ManifestFileName), []byte(manifestContent), 0o644); err != nil {
		t.Fatal(err)
	}

	pol, err = LoadSkillsPolicy(dir)
	if err != nil {
		t.Fatalf("LoadSkillsPolicy failed: %v", err)
	}
	if pol == nil || len(pol.Upstream) != 1 {
		t.Fatalf("unexpected policy: %+v", pol)
	}
	if _, ok := pol.UpstreamSkillSourceFor("my-skill"); !ok {
		t.Fatal("expected my-skill to be found in loaded policy")
	}
}
