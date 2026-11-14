package router

import "testing"

// TestIsReviewTask checks the declared review labels and that other labels are not review tasks.
func TestIsReviewTask(t *testing.T) {
	for _, tc := range []struct {
		label string
		want  bool
	}{
		{"code_review", true}, {"review", true}, {"single_file_audits", true},
		{"feature_implementation", false}, {"Review", false}, {"", false},
	} {
		if got := IsReviewTask(tc.label); got != tc.want {
			t.Errorf("IsReviewTask(%q) = %v, want %v", tc.label, got, tc.want)
		}
	}
}
