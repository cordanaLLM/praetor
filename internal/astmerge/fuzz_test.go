package astmerge

import (
	"testing"
)

// maxFuzzSource bounds each fuzzed input.
const maxFuzzSource = 20000

// FuzzASTMerge merges arbitrary inputs. A merge may fail or conflict, but a clean one must
// carry merged code that type-checks whenever both sides do, and must keep the probe's
// oracle: constants both sides keep or agree on keep their value, and initializers base
// and both sides run in one order keep it. Checking only for a non-nil result let clean
// merges that did not compile, or that changed constant values, pass. The seed corpus is
// every recorded probe case and every hand-written probe case.
func FuzzASTMerge(f *testing.F) {
	base := "package main\nfunc Foo() string { return \"base\" }\n"
	ours := "package main\nfunc Foo() string { return \"ours\" }\n"
	theirs := "package main\nfunc Bar() string { return \"theirs\" }\n"
	f.Add(base, ours, theirs)
	f.Add(orderBase, swapBC, orderBase+"\nfunc F() {}\n")
	for _, c := range loadCorpus(f) {
		f.Add(c.base, c.ours, c.theirs)
	}
	for _, c := range namedProbeCases {
		f.Add(c.base, c.ours, c.theirs)
	}

	f.Fuzz(func(t *testing.T, b, o, th string) {
		b, o, th = truncateFuzzSource(b), truncateFuzzSource(o), truncateFuzzSource(th)
		res, err := Merge(b, o, th)
		if err != nil {
			return
		}
		if res == nil {
			t.Fatal("Merge returned nil result without error")
		}
		if !res.Clean {
			if res.MergedCode != "" {
				t.Fatalf("a conflicted merge must not carry merged code:\n%s", res.MergedCode)
			}
			return
		}
		requireSoundCleanMerge(t, b, o, th, res.MergedCode)
	})
}

func truncateFuzzSource(src string) string {
	if len(src) > maxFuzzSource {
		return src[:maxFuzzSource]
	}
	return src
}
