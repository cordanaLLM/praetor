package astmerge

import (
	"testing"
)

func FuzzASTMerge(f *testing.F) {
	base := "package main\nfunc Foo() string { return \"base\" }\n"
	ours := "package main\nfunc Foo() string { return \"ours\" }\n"
	theirs := "package main\nfunc Bar() string { return \"theirs\" }\n"
	f.Add(base, ours, theirs)
	f.Add(orderBase, swapBC, orderBase+"\nfunc F() {}\n")

	f.Fuzz(func(t *testing.T, b, o, th string) {
		if len(b) > 20000 {
			b = b[:20000]
		}
		if len(o) > 20000 {
			o = o[:20000]
		}
		if len(th) > 20000 {
			th = th[:20000]
		}

		res, err := Merge(b, o, th)
		if err != nil {
			return
		}
		if res == nil {
			t.Fatal("Merge returned nil result without error")
		}
	})
}
