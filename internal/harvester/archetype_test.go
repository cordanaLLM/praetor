package harvester

import "testing"

func TestDetectArchetype_Negative_NoHintsIsTemplateSeed(t *testing.T) {
	if got := DetectArchetype("", ""); got != "template-seed" {
		t.Fatalf("empty inputs must yield template-seed, got %s", got)
	}
	if got := DetectArchetype("COBOL", "legacy batch processing"); got != "template-seed" {
		t.Fatalf("unknown language without keywords must yield template-seed, got %s", got)
	}
}

func TestDetectArchetype_Boundary_PrecedenceAndCase(t *testing.T) {
	cases := []struct {
		lang, desc, want string
	}{
		{"Go", "", "app-service"},                                             // language alone
		{"ASTRO", "", "pages-site"},                                           // language match is case-insensitive
		{"go", "STATIC site generator", "pages-site"},                         // keyword beats the language rule
		{"python", "CUDA kernels for a k8s helm chart", "native-gpu-systems"}, // first rule wins
		{"rust", "client SDK for the composable framework", "framework"},      // earlier rule wins over later
		{"typescript", "modules", "library-client"},
	}
	for _, tc := range cases {
		if got := DetectArchetype(tc.lang, tc.desc); got != tc.want {
			t.Errorf("DetectArchetype(%q, %q) = %s, want %s", tc.lang, tc.desc, got, tc.want)
		}
	}
}
