package gomanifest

import "testing"

func TestRequirementLineTransitions(t *testing.T) {
	if line, ok := RequirementLine("require example.com/a v1.0.0", nil); ok || line != "" {
		t.Fatal("nil state accepted")
	}
	cases := []struct {
		line, want          string
		dependency, inBlock bool
	}{
		{"module example.com/app", "module example.com/app", false, false},
		{" require example.com/a v1.0.0 ", "example.com/a v1.0.0", true, false},
		{"require (", "", false, true},
		{"// comment", "", false, true},
		{"\texample.com/b v2.0.0 // indirect", "example.com/b v2.0.0 // indirect", true, true},
		{")", "", false, false},
		{"", "", false, false},
	}
	inBlock := false
	for _, tc := range cases {
		line, dependency := RequirementLine(tc.line, &inBlock)
		if line != tc.want || dependency != tc.dependency || inBlock != tc.inBlock {
			t.Fatalf("%q: line=%q dependency=%v block=%v", tc.line, line, dependency, inBlock)
		}
	}
}
