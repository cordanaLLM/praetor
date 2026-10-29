package caveman

import (
	"strings"
	"testing"
)

func TestExtractReturnVerdict(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"verdict: pass", "pass"},
		{"- Verdict: GREEN details", "green"},
		{"```text\nverdict: pass\n```\nverdict: fail", "fail"},
		{"<!-- caveman:off -->\nverdict: pass\n<!-- caveman:on -->\nverdict: blocked", "blocked"},
	} {
		word, err := ExtractReturnVerdict(tc.text)
		if err != nil || word != tc.want {
			t.Errorf("word=%q want %q: %v", word, tc.want, err)
		}
	}
	for _, text := range []string{"", "verdict:", "verdict: pass\nverdict: fail", "verdict: pass" + strings.Repeat(" ", maxReturnExtractBytes)} {
		if _, err := ExtractReturnVerdict(text); err == nil {
			t.Error("invalid verdict accepted")
		}
	}
	text := "verdict: pass" + strings.Repeat(" ", maxReturnExtractBytes-len("verdict: pass"))
	if word, err := ExtractReturnVerdict(text); err != nil || word != "pass" {
		t.Fatalf("exact byte bound: %q %v", word, err)
	}
}
