// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge_test

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/forge"
)

func TestParseBriefClaim_Positive(t *testing.T) {
	brief := "goal: patch hook\ntask: feature_implementation\nissue: acme/widgets#7, other/lib#9\nissue: acme/widgets#8\nsession: s-1\n"
	got, err := forge.ParseBriefClaim(brief)
	want := []string{"acme/widgets#7", "other/lib#9", "acme/widgets#8"}
	if err != nil || got.Session != "s-1" || strings.Join(got.Issues, " ") != strings.Join(want, " ") {
		t.Fatalf("claim = %+v, %v", got, err)
	}
	none, err := forge.ParseBriefClaim("goal: x\ntask: ci_debugging\n")
	if err != nil || len(none.Issues) != 0 || none.Session != "" {
		t.Fatalf("a brief without the fields is the zero claim, got %+v, %v", none, err)
	}
}

func TestParseBriefClaim_Negative(t *testing.T) {
	cases := map[string]string{
		"bare number":         "issue: #7\n",
		"no owner":            "issue: widgets#7\n",
		"empty":               "issue:   \n",
		"trailing junk":       "issue: acme/widgets#7x\n",
		"one bad of two":      "issue: acme/widgets#7, nonsense\n",
		"traversal":           "issue: ../x#7\n",
		"ten digits":          "issue: acme/widgets#1234567890\n",
		"issue zero":          "issue: acme/widgets#0\n",
		"issue leading zero":  "issue: acme/widgets#07\n",
		"issue leading zeros": "issue: acme/widgets#007\n",
		"session no valid":    "issue: acme/widgets#7\nsession: a b\n",
		"session 129 chars":   "issue: acme/widgets#7\nsession: " + strings.Repeat("s", 129) + "\n",
		"duplicate session":   "issue: acme/widgets#7\nsession: a\nsession: b\n",
	}
	for name, brief := range cases {
		t.Run(name, func(t *testing.T) {
			if got, err := forge.ParseBriefClaim(brief); err == nil {
				t.Fatalf("claim = %+v, want an error", got)
			}
		})
	}
}

func TestParseBriefClaim_Boundary(t *testing.T) {
	var refs []string
	for i := 1; i <= caveman.MaxBriefClaimIssues; i++ {
		refs = append(refs, "acme/widgets#"+strings.Repeat("1", i%9+1))
	}
	got, err := forge.ParseBriefClaim("issue: " + strings.Join(refs, " ") + "\n")
	if err != nil || len(got.Issues) != caveman.MaxBriefClaimIssues {
		t.Fatalf("%d issues: %+v, %v", caveman.MaxBriefClaimIssues, got, err)
	}
	if _, err := forge.ParseBriefClaim("issue: " + strings.Join(refs, " ") + " acme/widgets#99\n"); err == nil {
		t.Fatal("one issue over the limit must be refused")
	}
	gotSession, err := forge.ParseBriefClaim("issue: acme/widgets#7\nsession: " + strings.Repeat("s", 128) + "\n")
	if err != nil || len(gotSession.Session) != 128 {
		t.Fatalf("128-character session: %v", err)
	}
	if _, err := forge.ParseBriefClaim("issue: acme/widgets#7\nsession: " + strings.Repeat("s", 129) + "\n"); err == nil {
		t.Fatal("129-character session must be refused")
	}
}
