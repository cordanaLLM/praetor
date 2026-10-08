package util_test

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// fragmentReader vouches for exactly the fragments it holds.
func fragmentReader(fragments map[string]string) util.MakefileIncludeReader {
	return func(path string) (string, bool) {
		text, ok := fragments[path]
		return text, ok
	}
}

func TestMakefileExpandIncludesFollowsLiteralFragments(t *testing.T) {
	read := fragmentReader(map[string]string{
		"tools/shared.mk": "help:\n\t@echo help\nlint:\n\t@true\n",
		"tools/vers.env":  "NAME=value\n",
	})
	data := "include tools/shared.mk\ninclude tools/vers.env\n\n.PHONY: verify-all\nverify-all: lint\n"
	got := util.MakefileExpandIncludes(data, read)
	if strings.Contains(got, "include") || !strings.Contains(got, "NAME=value") {
		t.Fatalf("fragments not spliced:\n%s", got)
	}
	if util.MakefileMayDefineTarget(got, "docs-lint") {
		t.Fatalf("tracked fragments without docs-lint still ambiguous:\n%s", got)
	}
	if !util.MakefileMayDefineTarget(data, "docs-lint") {
		t.Fatal("unexpanded include must stay ambiguous")
	}
}

func TestMakefileExpandIncludesFragmentDefiningTargetStillRefuses(t *testing.T) {
	read := fragmentReader(map[string]string{"docs.mk": "docs-lint:\n\t@echo fragment\n"})
	got := util.MakefileExpandIncludes("include docs.mk\n", read)
	if !util.MakefileHasTarget(got, "docs-lint") || !util.MakefileMayDefineTarget(got, "docs-lint") {
		t.Fatalf("fragment rule not seen:\n%s", got)
	}
}

func TestMakefileExpandIncludesAmbiguousFormsStayIncludes(t *testing.T) {
	read := fragmentReader(map[string]string{
		"ok.mk": "a:\n\t@true\n", "open.mk": "ifdef X\na:\n\t@true\n", "cont.mk": "A = b \\\n",
		"def.mk": "define D\nx\n", "tab.mk": "\techo stray\n", "rp.mk": ".RECIPEPREFIX = >\n",
	})
	forms := map[string]string{
		"soft include":   "-include ok.mk\n",
		"sinclude":       "sinclude ok.mk\n",
		"load":           "load ok.mk\n",
		"variable":       "include $(DIR)/ok.mk\n",
		"wildcard":       "include *.mk\n",
		"function":       "include $(wildcard ok.mk)\n",
		"outside":        "include ../ok.mk\n",
		"absolute":       "include /etc/ok.mk\n",
		"comment":        "include ok.mk # c\n",
		"missing":        "include missing.mk\n",
		"one bad of two": "include ok.mk missing.mk\n",
		"unclosed cond":  "include open.mk\n",
		"trailing cont":  "include cont.mk\n",
		"open define":    "include def.mk\n",
		"stray tab":      "include tab.mk\n",
		"recipe prefix":  "include rp.mk\n",
		"in recipe":      "all:\n\tinclude ok.mk\n",
		"in define":      "define D\ninclude ok.mk\nendef\n",
	}
	for name, data := range forms {
		t.Run(name, func(t *testing.T) {
			if got := util.MakefileExpandIncludes(data, read); got != data {
				t.Fatalf("ambiguous form expanded:\n%s", got)
			}
		})
	}
}

func TestMakefileExpandIncludesNilReaderChangesNothing(t *testing.T) {
	data := "include ok.mk\n"
	if got := util.MakefileExpandIncludes(data, nil); got != data {
		t.Fatalf("nil reader expanded: %q", got)
	}
}

// chain returns a reader whose fragment n includes fragment n+1 and whose last one holds a rule.
func chain(length int) util.MakefileIncludeReader {
	fragments := map[string]string{}
	for index := 1; index <= length; index++ {
		next := "leaf.mk"
		if index < length {
			next = "f" + string(rune('0'+index+1)) + ".mk"
		}
		fragments["f"+string(rune('0'+index))+".mk"] = "include " + next + "\n"
	}
	fragments["leaf.mk"] = "docs-lint:\n\t@true\n"
	return fragmentReader(fragments)
}

func TestMakefileExpandIncludesDepthBound(t *testing.T) {
	// The Makefile's include is level 1; f1 includes f2 (level 2) ... the leaf sits one level
	// below the last fragment. A leaf at level MaxMakefileIncludeDepth is read, one over is not.
	atBound := util.MaxMakefileIncludeDepth - 1
	got := util.MakefileExpandIncludes("include f1.mk\n", chain(atBound))
	if !util.MakefileHasTarget(got, "docs-lint") {
		t.Fatalf("leaf at depth %d not read:\n%s", util.MaxMakefileIncludeDepth, got)
	}
	got = util.MakefileExpandIncludes("include f1.mk\n", chain(atBound+1))
	if util.MakefileHasTarget(got, "docs-lint") || !util.MakefileMayDefineTarget(got, "docs-lint") {
		t.Fatalf("leaf one over the bound read or no longer ambiguous:\n%s", got)
	}
}

func TestMakefileExpandIncludesSelfIncludeTerminates(t *testing.T) {
	read := fragmentReader(map[string]string{"loop.mk": "include loop.mk\n"})
	got := util.MakefileExpandIncludes("include loop.mk\n", read)
	if !util.MakefileMayDefineTarget(got, "docs-lint") {
		t.Fatalf("self include became readable:\n%s", got)
	}
}

func TestMakefileExpandIncludesFileBound(t *testing.T) {
	read := fragmentReader(map[string]string{"a.mk": "a:\n\t@true\n"})
	exact := strings.Repeat("include a.mk\n", util.MaxMakefileIncludeFiles)
	if got := util.MakefileExpandIncludes(exact, read); strings.Contains(got, "include") {
		t.Fatal("includes at the file bound not all read")
	}
	if got := util.MakefileExpandIncludes(exact+"include a.mk\n", read); !strings.Contains(got, "include") {
		t.Fatal("include one over the file bound was read")
	}
}

// TestMakefileExpandIncludesRecipeStateDoesNotCrossTheSplice replays three Makefiles measured
// against GNU Make 4.4.1 that define docs-lint: Make closes the open rule at an include and reads
// the fragment and the rest of the including file with none open, so a tab-prefixed line right
// after the include is makefile syntax, not a recipe line of the fragment's last rule.
func TestMakefileExpandIncludesRecipeStateDoesNotCrossTheSplice(t *testing.T) {
	eval := "\tX := $(eval docs-lint: ; @echo x)\n"
	cases := map[string]struct {
		data      string
		fragments map[string]string
	}{
		"fragment ends in an open recipe": {
			data:      "include a.mk\n" + eval,
			fragments: map[string]string{"a.mk": "all:\n\t@true\n"},
		},
		"tab include after a fragment ending in a recipe": {
			data:      "include a.mk\n\tinclude b.mk\n",
			fragments: map[string]string{"a.mk": "all:\n\t@true\n", "b.mk": "docs-lint:\n\t@echo x\n"},
		},
		"parent recipe open and fragment leaves an unsure state": {
			data:      "all:\n\t@true\ninclude a.mk\n" + eval,
			fragments: map[string]string{"a.mk": "ifdef UNSET\nfoo:\nendif\n"},
		},
	}
	for name, tc := range cases {
		got := util.MakefileExpandIncludes(tc.data, fragmentReader(tc.fragments))
		if !util.MakefileMayDefineTarget(got, "docs-lint") {
			t.Errorf("%s: docs-lint reads as unowned:\n%s", name, got)
		}
	}
}

// TestMakefileExpandIncludesRemadeIncludeStaysAmbiguous covers an include Make remakes before it
// reads it (each replayed against GNU Make 4.4.1 in internal/adopt TestRemadeIncludeCasesMatchGNUMake):
// text in the Makefile or a fragment that may rebuild the included file, under any spelling Make
// treats alike, so its tracked text is not what Make reads. The reader is an allow-list.
func TestMakefileExpandIncludesRemadeIncludeStaysAmbiguous(t *testing.T) {
	read := fragmentReader(map[string]string{
		"gen.mk":   "help:\n\t@echo help\n",
		"./gen.mk": "help:\n", "././gen.mk": "help:\n",
		"b.mk": "gen.mk: gen.mk.in\n\tcp $< $@\n",
		"v.mk": "VPATH = src\n",
		"p.mk": "%.mk: %.in\n\tcp $< $@\n",
	})
	for name, data := range map[string]string{
		"rule in the Makefile":    "include gen.mk\ngen.mk: gen.mk.in\n\tcp $< $@\n",
		"rule in a fragment":      "include gen.mk\ninclude b.mk\n",
		"pattern rule":            "include gen.mk\n%.mk: %.mk.in\n\tcp $< $@\n",
		"pattern rule in include": "include gen.mk\ninclude p.mk\n",
		"rule before the line":    "gen.mk: gen.mk.in\n\tcp $< $@\ninclude gen.mk\n",
		"dot-dot-slash target":    "include gen.mk\n././gen.mk: gen.in\n",
		"dot-double-slash target": "include gen.mk\n.//gen.mk: gen.in\n",
		"dot operand":             "include ././gen.mk\ngen.mk: gen.in\n",
		"double-colon rule":       "include gen.mk\ngen.mk:: gen.in\n",
		"double-colon other":      "include gen.mk\nother:: x\n",
		"VPATH":                   "VPATH = src\ninclude gen.mk\n",
		"vpath":                   "vpath %.sh src\ninclude gen.mk\n",
		"VPATH in a fragment":     "include gen.mk\ninclude v.mk\n",
		".SUFFIXES":               ".SUFFIXES:\ninclude gen.mk\n",
		"default suffix":          "include gen.s\n",
		"static pattern rule":     "include gen.mk\ngen.mk gen.o: %.mk: %.in\n",
		"computed target":         "include gen.mk\nGEN := ././gen.mk\n$(GEN): gen.src\n",
		"computed dot target":     "include gen.mk\nGEN := .//gen.mk\n$(GEN): gen.src\n",
		"computed static pattern": "include gen.mk\nT := gen.mk\n$(T): %.mk: %.src\n",
		"computed VPATH name":     "X := VP\n$(X)ATH := src\ninclude gen.mk\n",
		"computed SUFFIXES name":  "S := .SUFF\n$(S)IXES: .in .mk\n.in.mk:\n\tcp $< $@\ninclude gen.mk\n",
		"grouped target":          "include gen.mk\ngen.mk&: gen.src\n\tcp $< $@\n",
		"grouped target spaced":   "include gen.mk\ngen.mk &: gen.src\n\tcp $< $@\n",
		"suffix rule":             ".in.mk:\n\tcp $< $@\ninclude gen.mk\n",
		"default rule":            "include gen.mk\n.DEFAULT:\n\tcp gen.src $@\n",
		"eval of a rule":          "include gen.mk\nX := $(eval gen.mk: gen.src)\n",
		"command binding":         "include gen.mk\nX != echo gen.mk: gen.src\n",
		"bare expansion":          "include gen.mk\n$(RULES)\n",
		"define block":            "include gen.mk\ndefine R\ngen.mk: gen.src\nendef\n",
		"bare export":             "include gen.mk\nexport X\n",
		"load":                    "include gen.mk\nload x.so\n",
		"computed in conditional": "include gen.mk\nifdef X\n$(G): s\nendif\n",
	} {
		got := util.MakefileExpandIncludes(data, read)
		if strings.Contains(name, "default suffix") {
			got = util.MakefileExpandIncludes(data, fragmentReader(map[string]string{"gen.s": "help:\n"}))
		}
		if got != data || !util.MakefileMayDefineTarget(got, "docs-lint") {
			t.Errorf("%s: include was followed:\n%s", name, got)
		}
	}
	for name, data := range map[string]string{
		"rule for another target": "include gen.mk\nother: x\n\t@true\n",
		"dot operand":             "include ./gen.mk\n",
		"assignment":              "include gen.mk\nT := a::b\nU = c:d\n",
		"percent in a prereq":     "include gen.mk\nobjs := $(S:%.c=%.o)\nother: $(objs)\n",
		"percent in a recipe":     "include gen.mk\nother:\n\t@printf '%s: x' y\n",
		"percent after semicolon": "include gen.mk\nother: x ; @printf '%s: x' y\n",
		"shared fragment shape":   "include gen.mk\nNAME := x\nexport V := 1\nifeq ($(NAME),x)\nT = y\nendif\n.PHONY: other\n.DEFAULT_GOAL := other\nother: gen.mk.txt\n\t@true\n",
	} {
		plain := util.MakefileExpandIncludes(data, read)
		if strings.Contains(plain, "include") || util.MakefileMayDefineTarget(plain, "docs-lint") {
			t.Errorf("%s: blocked the expansion:\n%s", name, plain)
		}
	}
}

func TestMakefileExpandIncludesReportNamesRemadeIncludes(t *testing.T) {
	read := fragmentReader(map[string]string{"gen.mk": "help:\n"})
	_, notes := util.MakefileExpandIncludesReport("include gen.mk\nvpath %.sh src\n", read)
	if len(notes) != 1 || !strings.Contains(notes[0], "gen.mk") || !strings.Contains(notes[0], "remake") {
		t.Fatalf("notes %q", notes)
	}
	if _, notes := util.MakefileExpandIncludesReport("include gen.mk\n", read); len(notes) != 0 {
		t.Fatalf("followed include noted: %q", notes)
	}
}
