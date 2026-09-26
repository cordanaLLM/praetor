package astmerge

import "testing"

// probeVars starts a probe file whose var initializers record the order they run in.
const probeVars = probeHeader + "\n"

// namedProbeCase is one of the adversarial probe's hand-written cases, with the outcome the
// merge must now have.
type namedProbeCase struct {
	name, base, ours, theirs string
	clean                    bool
}

// namedProbeCases are the probe's hand-written cases. Each clean one is checked by
// requireSoundCleanMerge; each conflicting one either has no order or value that keeps
// both sides' meaning, or changes a block that merges as one unit on both sides.
var namedProbeCases = []namedProbeCase{
	{"var-move-to-front-of-earlier-block",
		probeVars + "var (\n\ta = rec(\"a\")\n\tb = rec(\"b\")\n)\n\nfunc F() {}\n\nvar (\n\tc = rec(\"c\")\n\td = rec(\"d\")\n)\n",
		probeVars + "var (\n\td = rec(\"d\")\n\ta = rec(\"a\")\n\tb = rec(\"b\")\n)\n\nfunc F() {}\n\nvar (\n\tc = rec(\"c\")\n)\n",
		probeVars + "var (\n\ta = rec(\"a\")\n\tb = rec(\"b\")\n)\n\nfunc F() {}\n\nvar (\n\tc = rec(\"c\")\n\td = rec(\"d\")\n)\n\nfunc G() {}\n", true},
	{"toplevel-var-swap",
		probeVars + "var a = rec(\"a\")\n\nvar b = rec(\"b\")\n",
		probeVars + "var b = rec(\"b\")\n\nvar a = rec(\"a\")\n",
		probeVars + "var a = rec(\"a\")\n\nvar b = rec(\"b\")\n\nfunc G() {}\n", true},
	{"var-block-moved-above-toplevel",
		probeVars + "var x = rec(\"x\")\n\nvar (\n\ta = rec(\"a\")\n\tb = rec(\"b\")\n)\n",
		probeVars + "var (\n\ta = rec(\"a\")\n\tb = rec(\"b\")\n)\n\nvar x = rec(\"x\")\n",
		probeVars + "var x = rec(\"x\")\n\nvar (\n\ta = rec(\"a\")\n\tb = rec(\"b\")\n)\n\nfunc G() {}\n", true},
	{"same-slot-insert-iota",
		"package p\n\nconst (\n\tA = iota\n\tB\n)\n",
		"package p\n\nconst (\n\tA = iota\n\tX\n\tB\n)\n",
		"package p\n\nconst (\n\tA = iota\n\tY\n\tB\n)\n", false},
	{"explicit-insert-vs-reorder",
		"package p\n\nconst (\n\tA = iota\n\tB\n\tC\n\tD\n)\n",
		"package p\n\nconst (\n\tA = iota\n\tB\n\tD\n\tC\n)\n",
		"package p\n\nconst (\n\tA = iota\n\tB\n\tX = 100\n\tC\n\tD\n)\n", false},
	{"reorder-vs-delete",
		"package p\n\nconst (\n\tA = iota\n\tB\n\tC\n\tD\n)\n",
		"package p\n\nconst (\n\tA = iota\n\tC\n\tB\n\tD\n)\n",
		"package p\n\nconst (\n\tA = iota\n\tC\n\tD\n)\n", false},
	{"ungroup-vs-add",
		"package p\n\nconst (\n\tA = iota\n)\n",
		"package p\n\nconst A = iota\n",
		"package p\n\nconst (\n\tA = iota\n\tB\n)\n", false},
	{"split-vs-first-expr-edit",
		"package p\n\nconst (\n\tA = iota\n\tB\n\tC\n\tD\n)\n",
		"package p\n\nconst (\n\tA = iota\n\tB\n)\n\nconst (\n\tC = iota + 2\n\tD\n)\n",
		"package p\n\nconst (\n\tA = iota * 10\n\tB\n\tC\n\tD\n)\n", false},
	{"split-out-last-vs-append",
		"package p\n\nconst (\n\tA = iota\n\tB\n\tC\n)\n\nfunc F() {}\n",
		"package p\n\nconst (\n\tA = iota\n\tB\n)\n\nfunc F() {}\n\nconst C = 2\n",
		"package p\n\nconst (\n\tA = iota\n\tB\n\tC\n\tE\n)\n\nfunc F() {}\n", false},
	{"swapBC-vs-deleteC",
		"package p\n\nconst (\n\tA = iota\n\tB\n\tC\n)\n",
		"package p\n\nconst (\n\tA = iota\n\tC\n\tB\n)\n",
		"package p\n\nconst (\n\tA = iota\n\tB\n)\n", false},
	{"move-single-into-other-block-vs-append",
		"package p\n\nconst (\n\tA = iota\n)\n\nconst (\n\tX = 5\n)\n",
		"package p\n\nconst (\n\tX = 5\n\tA = iota\n)\n",
		"package p\n\nconst (\n\tA = iota\n\tB\n)\n\nconst (\n\tX = 5\n)\n", false},
	{"ungroup-vs-move-in",
		"package p\n\nconst (\n\tA = iota\n)\n\nconst X = 7\n",
		"package p\n\nconst A = iota\n\nconst X = 7\n",
		"package p\n\nconst (\n\tA = iota\n\tX\n)\n", false},
	{"var-ungroup-vs-append",
		probeVars + "var (\n\ta = rec(\"a\")\n)\n",
		probeVars + "var a = rec(\"a\")\n",
		probeVars + "var (\n\ta = rec(\"a\")\n\tb = rec(\"b\")\n)\n", true},
	{"group-toplevel-vs-edit",
		"package p\n\nconst A = iota\n\nconst B = 3\n",
		"package p\n\nconst (\n\tA = iota\n\tB = 3\n)\n",
		"package p\n\nconst A = iota\n\nconst B = 4\n", false},
	{"reorder-first-vs-doc-edit",
		"package p\n\n// Doc.\nconst (\n\tA = 1\n\tB = 2\n)\n",
		"package p\n\n// Doc.\nconst (\n\tB = 2\n\tA = 1\n)\n",
		"package p\n\n// Doc two.\nconst (\n\tA = 1\n\tB = 2\n)\n", false},
	{"empty-block-vs-add",
		"package p\n\nconst ()\n\nfunc F() {}\n",
		"package p\n\nconst ()\n\nfunc F() {}\n\nfunc G() {}\n",
		"package p\n\nconst (\n\tA = 1\n)\n\nfunc F() {}\n", true},
	{"comment-in-block-vs-reorder",
		"package p\n\nconst (\n\tA = iota\n\t// mid\n\n\tB\n\tC\n)\n",
		"package p\n\nconst (\n\tA = iota\n\t// mid\n\n\tC\n\tB\n)\n",
		"package p\n\nconst (\n\tA = iota\n\t// mid\n\n\tB\n\tC\n)\n\nfunc F() {}\n", true},
	{"var-same-slot-insert",
		probeVars + "var (\n\ta = rec(\"a\")\n\tb = rec(\"b\")\n)\n",
		probeVars + "var (\n\ta = rec(\"a\")\n\tx = rec(\"x\")\n\tb = rec(\"b\")\n)\n",
		probeVars + "var (\n\ta = rec(\"a\")\n\ty = rec(\"y\")\n\tb = rec(\"b\")\n)\n", false},
	{"cosmetic-implicit-vs-predecessor-edit",
		"package p\n\nconst (\n\tA = 7\n\tB = 7\n)\n",
		"package p\n\nconst (\n\tA = 7\n\tB\n)\n",
		"package p\n\nconst (\n\tA = 1\n\tB = 7\n)\n", false},
	{"cosmetic-implicit-vs-predecessor-edit-nonadjacent",
		"package p\n\nconst (\n\tA = 7\n\tM\n\tB = 7\n)\n",
		"package p\n\nconst (\n\tA = 7\n\tM\n\tB\n)\n",
		"package p\n\nconst (\n\tA = 1\n\tM\n\tB = 7\n)\n", false},
	{"import-alias-change-one-side",
		"package p\n\nimport \"strings\"\n\nvar _ = strings.ToUpper\n",
		"package p\n\nimport s \"strings\"\n\nvar _ = s.ToUpper\n",
		"package p\n\nimport \"strings\"\n\nvar _ = strings.ToUpper\n\nfunc F() {}\n", true},
	{"implicit-edit-vs-move",
		"package p\n\nconst (\n\tA = iota * 10\n\tB = iota - 1\n)\n\nconst (\n\tC = iota\n\tD\n)\n",
		"package p\n\nconst (\n\tA = iota * 10\n\tB\n)\n\nconst (\n\tC = iota\n\tD\n)\n",
		"package p\n\nconst (\n\tA = iota * 10\n)\n\nconst (\n\tC = iota\n\tB = iota - 1\n\tD\n)\n", false},
	{"move-vs-delete-moved",
		"package p\n\nconst (\n\tA = iota\n\tB\n)\n\nconst (\n\tC = iota\n\tD\n\tE\n)\n",
		"package p\n\nconst (\n\tA = iota\n)\n\nconst (\n\tC = iota\n\tD\n\tE\n)\n",
		"package p\n\nconst (\n\tA = iota\n)\n\nconst (\n\tC = iota\n\tB\n\tE\n)\n\nconst D = 1\n", false},
	{"var-multi-name-reorder-vs-edit",
		"package p\n\nvar (\n\ta, b = 1, 2\n\tc    = 3\n)\n",
		"package p\n\nvar (\n\tc    = 3\n\ta, b = 1, 2\n)\n",
		"package p\n\nvar (\n\ta, b = 1, 2\n\tc    = 4\n)\n", false},
	{"reorder-vs-doc-edit",
		"package p\n\n// Doc.\nconst (\n\tA = iota\n\tB\n\tC\n)\n",
		"package p\n\n// Doc.\nconst (\n\tA = iota\n\tC\n\tB\n)\n",
		"package p\n\n// Doc two.\nconst (\n\tA = iota\n\tB\n\tC\n)\n", false},
	{"swapCD-vs-deleteB",
		"package p\n\nconst (\n\tA = iota\n\tB\n\tC\n\tD\n)\n",
		"package p\n\nconst (\n\tA = iota\n\tB\n\tD\n\tC\n)\n",
		"package p\n\nconst (\n\tA = iota\n\tC\n\tD\n)\n", false},
	{"delete-and-ungroup-vs-append",
		"package p\n\nconst (\n\tA = iota\n\tB\n)\n",
		"package p\n\nconst A = iota\n",
		"package p\n\nconst (\n\tA = iota\n\tB\n\tC\n)\n", false},
	{"join-by-move-vs-append",
		"package p\n\nconst (\n\tA = iota\n\tA2\n)\n\nconst (\n\tX = 5\n\tY\n)\n",
		"package p\n\nconst (\n\tX = 5\n\tY\n\tA = iota\n\tA2\n)\n",
		"package p\n\nconst (\n\tA = iota\n\tA2\n\tB\n)\n\nconst (\n\tX = 5\n\tY\n)\n", false},
	{"split-vs-delete-before-split",
		"package p\n\nconst (\n\tA = iota\n\tB\n\tC\n\tD\n)\n",
		"package p\n\nconst (\n\tA = iota\n\tB\n)\n\nconst (\n\tC = iota + 2\n\tD\n)\n",
		"package p\n\nconst (\n\tA = iota\n\tC\n\tD\n)\n", false},
	{"move-into-typed-block",
		"package p\n\ntype T int\ntype U string\n\nconst (\n\tA T = iota\n\tB\n)\n\nconst (\n\tX U = \"x\"\n)\n",
		"package p\n\ntype T int\ntype U string\n\nconst (\n\tA T = iota\n)\n\nconst (\n\tX U = \"x\"\n\tB\n)\n",
		"package p\n\ntype T int\ntype U string\n\nconst (\n\tA T = iota\n\tB\n\tC\n)\n\nconst (\n\tX U = \"x\"\n)\n", false},
	{"type-block-split-vs-append",
		"package p\n\ntype (\n\tT int\n\tU string\n)\n",
		"package p\n\ntype T int\n\ntype (\n\tU string\n)\n",
		"package p\n\ntype (\n\tT int\n\tU string\n\tV bool\n)\n", false},
	{"dup-import-path-two-aliases",
		"package p\n\nimport (\n\ta \"strings\"\n)\n\nvar _ = a.ToUpper\n",
		"package p\n\nimport (\n\ta \"strings\"\n\tb \"strings\"\n)\n\nvar _ = a.ToUpper\n\nvar _ = b.ToLower\n",
		"package p\n\nimport (\n\ta \"strings\"\n)\n\nvar _ = a.ToUpper\n\nfunc F() {}\n", false},
	{"typed-iota-type-inherit",
		"package p\n\ntype T int\n\nconst (\n\tA T = iota\n\tB\n)\n\nconst (\n\tX = 5\n)\n",
		"package p\n\ntype T int\n\nconst (\n\tA T = iota\n)\n\nconst (\n\tX = 5\n\tB\n)\n",
		"package p\n\ntype T int\n\nconst (\n\tA T = iota\n\tB\n)\n\nconst (\n\tX = 5\n)\n\nfunc F() {}\n", true},
}

// TestMerge_ProbeNamedCases replays the probe's hand-written cases: the ones the first
// per-spec attempt merged clean into wrong values, flipped initialization order or code
// that did not compile now conflict, and the ones with one unambiguous meaning merge clean.
func TestMerge_ProbeNamedCases(t *testing.T) {
	for _, c := range namedProbeCases {
		t.Run(c.name, func(t *testing.T) {
			res, err := Merge(c.base, c.ours, c.theirs)
			if err != nil {
				t.Fatalf("Merge failed on valid inputs: %v", err)
			}
			if res.Clean != c.clean {
				t.Fatalf("clean = %v, want %v; conflicts %+v\n%s", res.Clean, c.clean, res.Conflicts, res.MergedCode)
			}
			if res.Clean {
				requireSoundCleanMerge(t, c.base, c.ours, c.theirs, res.MergedCode)
			}
		})
	}
}
