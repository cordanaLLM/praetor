// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package caveman

import (
	"reflect"
	"testing"
)

type citationCase struct {
	name string
	text string
	want []ReturnCitation
}

func assertCitationCases(t *testing.T, cases []citationCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractReturnCitations(tc.text)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestExtractReturnCitations(t *testing.T) {
	assertCitationCases(t, []citationCase{
		{
			name: "single changed line with diagnostic",
			text: "verdict: pass\nchanged: internal/agenthook/agent_traffic.go:208\nran: none\nevidence: exit 0\nopen: none\n",
			want: []ReturnCitation{
				{Path: "internal/agenthook/agent_traffic.go", Line: 208},
			},
		},
		{
			name: "multi-file changed section with links and bare files",
			text: "verdict: pass\nchanged:\n- internal/agenthook/agent_traffic.go:208\n- `cmd/standardsctl/main.go`\n- [root](file:///home/user/repo/AGENTS.md#L42)\nran: none\nevidence: exit 0\nopen: none\n",
			want: []ReturnCitation{
				{Path: "internal/agenthook/agent_traffic.go", Line: 208},
				{Path: "cmd/standardsctl/main.go", Line: 0},
				{Path: "/home/user/repo/AGENTS.md", Line: 42},
			},
		},
		{
			name: "evidence pointer extracted",
			text: "verdict: pass\nchanged: none\nran: none\nevidence: .workingdir/evidence/review.md sha256:0123456789ab lines:58\nopen: none\n",
			want: []ReturnCitation{
				{Path: ".workingdir/evidence/review.md", Line: 0},
			},
		},
		{
			name: "open field citation",
			text: "verdict: fail\nchanged: none\nran: none\nevidence: none\nopen: check internal/foo/bar.go:15 for bugs\n",
			want: []ReturnCitation{
				{Path: "internal/foo/bar.go", Line: 15},
			},
		},
		{
			name: "no citations",
			text: "verdict: pass\nchanged: none\nran: none\nevidence: exit 0\nopen: none\n",
			want: nil,
		},
	})
}

func TestExtractReturnCitations_GrammarShapes(t *testing.T) {
	assertCitationCases(t, []citationCase{
		{
			name: "line ranges and root dotfile",
			text: "verdict: pass\nchanged: internal/agenthook/policy.go:205-208, .standards.yaml:393-405\nran: none\nevidence: none\nopen: none\n",
			want: []ReturnCitation{
				{Path: "internal/agenthook/policy.go", Line: 208},
				{Path: ".standards.yaml", Line: 405},
			},
		},
		{
			name: "trailing sentence punctuation and bare filename skipped",
			text: "verdict: pass\nchanged: none\nran: none\nevidence: checked internal/compiler/register.go:99. and docs_references.go:90.\nopen: none\n",
			want: []ReturnCitation{
				{Path: "internal/compiler/register.go", Line: 99},
			},
		},
		{
			name: "list of lines after path",
			text: "verdict: pass\nchanged: policy.go:46, 57-63, 383; internal/agenthook/policy.go:46, 57-63, 383\nran: none\nevidence: none\nopen: none\n",
			want: []ReturnCitation{
				{Path: "internal/agenthook/policy.go", Line: 46},
				{Path: "internal/agenthook/policy.go", Line: 63},
				{Path: "internal/agenthook/policy.go", Line: 383},
			},
		},
		{
			name: "non-citation tokens ignored",
			text: "verdict: pass\nchanged: read, only, none\nran: none\nevidence: Policy.Command FleetTopology.Orgs config.LoadManifest policy.go dialect.go command_targets.go pr.go operator_merge.go\nopen: none\n",
			want: nil,
		},
	})
}

func TestExtractReturnRan(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want []string
	}{
		{
			name: "inline command",
			text: "verdict: pass\nchanged: none\nran: `go test -race ./...`\nevidence: exit 0\nopen: none\n",
			want: []string{"go test -race ./..."},
		},
		{
			name: "inline commands field",
			text: "verdict: pass\nchanged: none\ncommands: `go test -race ./...`\nevidence: exit 0\nopen: none\n",
			want: []string{"go test -race ./..."},
		},
		{
			name: "multiline ran list",
			text: "verdict: pass\nchanged: none\nran:\n- $ go test -race ./...\n- $ golangci-lint run ./...\nevidence: exit 0\nopen: none\n",
			want: []string{"go test -race ./...", "golangci-lint run ./..."},
		},
		{
			name: "prose in ran field is not a claimed command",
			text: "verdict: pass\nchanged: none\nran: grep, sed, cat reads of repo; no build, no git write, no edit\nevidence: none\nopen: none\n",
			want: nil,
		},
		{
			name: "honest return saying build could not run",
			text: "verdict: fail\nchanged: none\nran: build could not run\nevidence: none\nopen: none\n",
			want: nil,
		},
		{
			name: "honest return saying ran none",
			text: "verdict: pass\nchanged: none\nran: none\nevidence: exit 0\nopen: none\n",
			want: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractReturnRan(tc.text)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestExtractReturnCitationsExtensionShapeAndHosts(t *testing.T) {
	text := "evidence: src/engine/render.cpp:12; include/engine/render.hpp; github.com/o/r/blob/main/x.md; docs.example.org/a.html; go1.27; internal/forge.ForeignReferenceFindings"
	cites, err := ExtractReturnCitations(text)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	got := map[string]bool{}
	for _, c := range cites {
		got[c.Path] = true
	}
	for _, want := range []string{"src/engine/render.cpp", "include/engine/render.hpp"} {
		if !got[want] {
			t.Errorf("citation %s missing; got %v", want, got)
		}
	}
	for _, not := range []string{"github.com/o/r/blob/main/x.md", "docs.example.org/a.html", "go1.27", "internal/forge.ForeignReferenceFindings"} {
		if got[not] {
			t.Errorf("%s taken for a citation", not)
		}
	}
}
