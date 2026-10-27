// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package adopt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
)

// Order: engine-fr-rule-checks-20261027. Old bytes remain replayable and refresh safely.
func TestForgeText_PriorTemplate(t *testing.T) {
	assertPriorDigestsReproduced(t, "testdata/pr-template", priorPRTemplateDigests)
	prior := mustRead(t, "testdata/pr-template/wrapped.md.prior")
	for _, tc := range []struct {
		name, text string
		refresh    bool
	}{
		{"prior", prior, true},
		{"crlf", strings.ReplaceAll(prior, "\n", "\r\n"), true},
		{"edited", prior + "Custom paragraph.\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, prTemplateFile)
			mustWrite(t, path, tc.text)
			s := &adoptSession{repoPath: root, report: &AdoptReport{}}
			if err := reconcilePullRequestTemplate(t.Context(), s); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want := priorTemplateExpected(tc.text, tc.name, tc.refresh)
			if string(got) != want {
				t.Fatalf("template refresh=%v: got %q", tc.refresh, got)
			}
		})
	}
	if got := forge.BodyHardWrapFindings(buildPullRequestTemplate()); len(got) != 0 {
		t.Fatal(got)
	}
}

func priorTemplateExpected(prior, name string, refresh bool) string {
	if !refresh {
		return prior
	}
	text := buildPullRequestTemplate()
	if name == "crlf" {
		return strings.ReplaceAll(text, "\n", "\r\n")
	}
	return text
}
