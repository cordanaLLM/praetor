package main

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
)

// #1285: "already matched" is printed only when nothing is undeclared; undeclared metadata is
// printed with GitHub's values, bounded.
func TestPrintRepositoryMetadataReport_3D_UndeclaredIsNeverMatched(t *testing.T) {
	long := strings.Repeat("d", 3*maxUndeclaredValue)
	cases := []struct {
		name   string
		report forge.RepositoryMetadataReport
		want   []string
		absent []string
	}{
		{"positive: everything declared", forge.RepositoryMetadataReport{},
			[]string{"(already matched .standards.yaml;"}, []string{"[UNDECLARED]", "nothing written"}},
		{"negative: nothing declared", forge.RepositoryMetadataReport{UndeclaredDescription: "Widget engine", UndeclaredHomepage: "http://widgets.example", UndeclaredTopics: []string{"a", "b"}},
			[]string{"(nothing written;", `[UNDECLARED] Repository metadata GitHub holds and .standards.yaml does not declare: description "Widget engine"; homepage "http://widgets.example"; topics a, b; kept on GitHub`},
			[]string{"already matched"}},
		{"boundary: written fields and an undeclared topic", forge.RepositoryMetadataReport{Updated: []string{"description"}, UndeclaredTopics: []string{"legacy"}},
			[]string{"(updated description;", "does not declare: topics legacy;"}, []string{"already matched", "nothing written"}},
		{"boundary: a long value is cut", forge.RepositoryMetadataReport{UndeclaredDescription: long},
			[]string{"[UNDECLARED]"}, []string{long}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := captureStdout(t, func() error { printRepositoryMetadataReport(&tc.report); return nil })
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(out, absent) {
					t.Errorf("output holds %q:\n%s", absent, out)
				}
			}
		})
	}
}
