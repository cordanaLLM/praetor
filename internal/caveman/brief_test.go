package caveman

import (
	"strings"
	"testing"
)

func TestExtractBriefTaskPositive(t *testing.T) {
	brief := "goal: patch hook\ninputs: event.go\nreturn: diff\nevidence: go test\n- task: feature_implementation\n"
	got, err := ExtractBriefTask(brief)
	if err != nil || got != "feature_implementation" {
		t.Fatalf("task = %q, %v", got, err)
	}
}

func TestExtractBriefTaskNegative(t *testing.T) {
	for name, brief := range map[string]string{
		"missing":   "goal: patch hook\ninputs: event.go\n",
		"empty":     "task:   \n",
		"duplicate": "task: ci_debugging\ntask: unit_test_suites\n",
		"code only": "```text\ntask: ci_debugging\n```\n",
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := ExtractBriefTask(brief); err == nil || got != "" {
				t.Fatalf("task = %q, %v", got, err)
			}
		})
	}
}

func TestExtractBriefTaskBoundary(t *testing.T) {
	task := strings.Repeat("x", 256)
	got, err := ExtractBriefTask("task: " + task + "\n")
	if err != nil || got != task {
		t.Fatalf("task length = %d, %v", len(got), err)
	}
}

func TestExtractBriefReadOnly_Positive(t *testing.T) {
	for name, tc := range map[string]struct {
		text string
		want bool
	}{
		"readonly true": {
			text: "goal: review diff\ninputs: a.go\nreturn: verdict\nevidence: none\ntask: review\nreadonly: true\n",
			want: true,
		},
		"read-only true": {
			text: "goal: review diff\ninputs: a.go\nreturn: verdict\nevidence: none\ntask: review\nread-only: true\n",
			want: true,
		},
		"readonly 1": {
			text: "goal: review diff\ninputs: a.go\nreturn: verdict\nevidence: none\ntask: review\nreadonly: 1\n",
			want: true,
		},
		"readonly yes": {
			text: "goal: review diff\ninputs: a.go\nreturn: verdict\nevidence: none\ntask: review\nreadonly: yes\n",
			want: true,
		},
		"readonly false": {
			text: "goal: review diff\ninputs: a.go\nreturn: verdict\nevidence: none\ntask: review\nreadonly: false\n",
			want: false,
		},
		"read-only no": {
			text: "goal: review diff\ninputs: a.go\nreturn: verdict\nevidence: none\ntask: review\nread-only: no\n",
			want: false,
		},
		"list item readonly": {
			text: "goal: review diff\ninputs: a.go\nreturn: verdict\nevidence: none\ntask: review\n- readonly: true\n",
			want: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ExtractBriefReadOnly(tc.text)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			if IsBriefReadOnly(tc.text) != tc.want {
				t.Fatalf("IsBriefReadOnly got %v, want %v", IsBriefReadOnly(tc.text), tc.want)
			}
		})
	}
}

func TestExtractBriefReadOnly_Negative(t *testing.T) {
	for name, text := range map[string]string{
		"duplicate readonly": "goal: review diff\ninputs: a.go\nreturn: verdict\nevidence: none\ntask: review\nreadonly: true\nreadonly: false\n",
		"duplicate hyphen":   "goal: review diff\ninputs: a.go\nreturn: verdict\nevidence: none\ntask: review\nreadonly: true\nread-only: true\n",
		"empty value":        "goal: review diff\ninputs: a.go\nreturn: verdict\nevidence: none\ntask: review\nreadonly:   \n",
		"invalid value":      "goal: review diff\ninputs: a.go\nreturn: verdict\nevidence: none\ntask: review\nreadonly: maybe\n",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ExtractBriefReadOnly(text)
			if err == nil {
				t.Fatalf("expected error, got %v", got)
			}
			if IsBriefReadOnly(text) {
				t.Fatalf("IsBriefReadOnly must return false on invalid brief")
			}
		})
	}
}

func TestExtractBriefReadOnly_Boundary(t *testing.T) {
	// Boundary: absent readonly field returns false, nil (default read-write)
	absent := "goal: review diff\ninputs: a.go\nreturn: verdict\nevidence: none\ntask: review\n"
	got, err := ExtractBriefReadOnly(absent)
	if err != nil || got {
		t.Fatalf("absent field: got %v, %v, want false, nil", got, err)
	}
	if IsBriefReadOnly(absent) {
		t.Fatalf("IsBriefReadOnly must return false when field absent")
	}

	// Boundary: code block readonly is ignored
	inCode := "goal: review diff\ninputs: a.go\nreturn: verdict\nevidence: none\ntask: review\n```text\nreadonly: true\n```\n"
	got, err = ExtractBriefReadOnly(inCode)
	if err != nil || got {
		t.Fatalf("inCode: got %v, %v, want false, nil", got, err)
	}

	// Boundary: case insensitive
	mixed := "goal: review diff\ninputs: a.go\nreturn: verdict\nevidence: none\ntask: review\nReadOnly: True\n"
	got, err = ExtractBriefReadOnly(mixed)
	if err != nil || !got {
		t.Fatalf("mixed case: got %v, %v, want true, nil", got, err)
	}
}

func TestExtractBriefClaimPositive(t *testing.T) {
	brief := "goal: patch hook\ntask: feature_implementation\n- issue: acme/widgets#7, other/lib#9\nissue: acme/widgets#8\n- session: s-1\n"
	got, err := ExtractBriefClaim(brief)
	want := []string{"acme/widgets#7", "other/lib#9", "acme/widgets#8"}
	if err != nil || got.Session != "s-1" || strings.Join(got.Issues, " ") != strings.Join(want, " ") {
		t.Fatalf("claim = %+v, %v", got, err)
	}
	if none, err := ExtractBriefClaim("goal: x\ntask: ci_debugging\n"); err != nil || len(none.Issues) != 0 || none.Session != "" {
		t.Fatalf("a brief without the fields is the zero claim, got %+v, %v", none, err)
	}
	if fenced, err := ExtractBriefClaim("```text\nissue: acme/widgets#7\n```\n"); err != nil || len(fenced.Issues) != 0 {
		t.Fatalf("fenced code is not a field: %+v, %v", fenced, err)
	}
}

func TestExtractBriefClaimNegative(t *testing.T) {
	for name, brief := range map[string]string{
		"bare number":        "issue: #7\n",
		"no owner":           "issue: widgets#7\n",
		"empty":              "issue:   \n",
		"trailing junk":      "issue: acme/widgets#7x\n",
		"one bad of two":     "issue: acme/widgets#7, nonsense\n",
		"traversal":          "issue: ../x#7\n",
		"ten digits":         "issue: acme/widgets#1234567890\n",
		"duplicate session":  "session: a\nsession: b\n",
		"session with space": "session: a b\n",
		"empty session":      "session:   \n",
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := ExtractBriefClaim(brief); err == nil {
				t.Fatalf("claim = %+v, want an error", got)
			}
		})
	}
}

func TestExtractBriefClaimBoundary(t *testing.T) {
	var refs []string
	for i := 1; i <= MaxBriefClaimIssues; i++ {
		refs = append(refs, "acme/widgets#"+strings.Repeat("1", i%9+1))
	}
	if got, err := ExtractBriefClaim("issue: " + strings.Join(refs, " ") + "\n"); err != nil || len(got.Issues) != MaxBriefClaimIssues {
		t.Fatalf("%d issues: %+v, %v", MaxBriefClaimIssues, got, err)
	}
	if _, err := ExtractBriefClaim("issue: " + strings.Join(refs, " ") + " acme/widgets#99\n"); err == nil {
		t.Fatal("one issue over the limit must be refused")
	}
	if got, err := ExtractBriefClaim("session: " + strings.Repeat("s", 128) + "\n"); err != nil || len(got.Session) != 128 {
		t.Fatalf("128-character session: %v", err)
	}
	if _, err := ExtractBriefClaim("session: " + strings.Repeat("s", 129) + "\n"); err == nil {
		t.Fatal("129-character session must be refused")
	}
}
