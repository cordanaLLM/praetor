package adopt

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// A define only binds a variable; Make turns its body into rules when something parses the
// expansion as makefile syntax. Every row was measured against GNU Make 4.4.1 with "make -n
// docs-lint", and TestMakefileDefineOwnershipGNUReplay replays them: the accepted rows answer "No
// rule to make target 'docs-lint'", the refused rows either declare the target or leave the file
// to Make (include, an unterminated define). The reader evaluates no function, so every bare
// expansion beside a define counts, $(if ...) included, except a line made only of info, warning
// and error calls, which expands to nothing: info-bare-expansion and info-prints-define stay
// appendable, while "$(info)" with no blank after the name references a variable named info, which
// info-variable shows can hold the define.
var makefileDefineOwnershipRows = map[string]docsLintOwnershipRow{
	"called-in-recipe":     {calledDefineMakefile, false},
	"body-names-target":    {"define docs-rule\ndocs-lint:\n\t@echo template\nendef\n\nall:\n\t@echo all\n", false},
	"override-define-body": {"override define docs-rule\ndocs-lint: ; @echo template\nendef\n", false},
	"nested-define-closed": {"define outer\ndefine inner\ndocs-lint: ; @echo nested\nendef\nendef\n", false},
	"endef-with-comment":   {"define docs-rule\ndocs-lint: ; @echo template\nendef # done\n", false},
	"continued-assignment": {"define docs-rule\n@echo template\nendef\nOBJS := \\\n  $(patsubst %.c,%.o,$(SRCS))\n", false},
	"continued-recipe":     {"define docs-rule\n@echo template\nendef\nall:\n\t@echo a \\\n  $(CC)\n", false},
	"conditional":          {"define docs-rule\n@echo template\nendef\nifeq ($(CI),true)\nci: ; @echo ci\nendif\n", false},
	"export-list":          {"define docs-rule\n@echo template\nendef\nexport $(TOOLS)\n", false},
	"eval-expanded":        {"define docs-rule\ndocs-lint: ; @echo template\nendef\n$(eval $(docs-rule))\n", true},
	"brace-eval-expanded":  {"define docs-rule\ndocs-lint: ; @echo template\nendef\n${eval ${docs-rule}}\n", true},
	"bare-expansion":       {"define docs-rule\ndocs-lint: ; @echo template\nendef\n$(docs-rule)\n", true},
	"bare-call":            {"define docs-rule\ndocs-lint: ; @echo $(1)\nendef\n$(call docs-rule,template)\n", true},
	"bare-call-nested-arg": {"define docs-rule\n$(1): ; @echo template $(2)\nendef\n$(call docs-rule,$(P)docs-lint,FOO=bar)\n", true},
	"if-nested-arg":        {"define docs-rule\ndocs-lint: ; @echo template\nendef\n$(if $(A),A=B,$(docs-rule))\n", true},
	"info-variable":        {"define docs-rule\ndocs-lint: ; @echo template\nendef\ninfo = $(docs-rule)\n$(info)\n", true},
	"info-bare-expansion":  {"define docs-rule\n@echo template\nendef\n$(info building)\n", false},
	"info-prints-define":   {"define docs-rule\ndocs-lint: ; @echo template\nendef\n$(info $(docs-rule))\n", false},
	"eval-in-body":         {"define docs-rule\n$(eval docs-lint: ; @echo template)\nendef\n", true},
	"include":              {"define docs-rule\n@echo template\nendef\n-include docs.mk\n", true},
	"unterminated":         {"define docs-rule\n@echo template\n", true},
	"tab-indented-endef":   {"define docs-rule\n@echo template\n\tendef\nall: ; @echo all\n", true},
	"endef-glued-comment":  {"define docs-rule\n@echo template\nendef#done\nall: ; @echo all\n", true},
	"nested-override-leak": {"define outer\noverride define inner\nx\nendef\ndocs-lint: ; @echo leaked\nendef\n", true},
}

// calledDefineMakefile is the shape a large repository ships: one multi-line helper used only
// through $(call ...) in recipes, with no include and no eval.
const calledDefineMakefile = "define require-tool\n" +
	"@command -v $(1) >/dev/null || { \\\n" +
	"   echo \"error: $(1) not found\"; \\\n" +
	"   echo \"       install: $(2)\"; exit 1; }\n" +
	"endef\n\n" +
	".PHONY: lint\n" +
	"lint:\n" +
	"\t$(call require-tool,shellcheck,your package manager)\n" +
	"\t@shellcheck scripts/*.sh\n"

func TestMakefileDefineOwnershipFollowsExpansion(t *testing.T) {
	assertDocsLintOwnership(t, makefileDefineOwnershipRows)
}

// docsLintOwnershipRow is a Makefile and whether adoption must leave docs-lint ownership to Make.
type docsLintOwnershipRow struct {
	makefile string
	refused  bool
}

// assertDocsLintOwnership runs every row through the reader and through the documentation merge,
// which must refuse exactly the rows the reader leaves to Make.
func assertDocsLintOwnership(t *testing.T, rows map[string]docsLintOwnershipRow) {
	t.Helper()
	for name, tc := range rows {
		t.Run(name, func(t *testing.T) {
			if got := util.MakefileMayDefineTarget(tc.makefile, "docs-lint"); got != tc.refused {
				t.Fatalf("MakefileMayDefineTarget(%q, docs-lint) = %v, want %v", tc.makefile, got, tc.refused)
			}
			_, err := mergeDocumentationMakefile(tc.makefile, false)
			if (err != nil) != tc.refused {
				t.Fatalf("mergeDocumentationMakefile refused = %v, want %v: %v", err != nil, tc.refused, err)
			}
		})
	}
}

// A top-level bare expansion is parsed as makefile syntax after Make expands it, so whatever its
// references hold may declare a rule, with or without a define. Measured against GNU Make 4.4.1
// with "make -n docs-lint", every refused row from literal-rule to prefixed-expansion declares
// docs-lint, each through a different way of reaching rule text: a literal argument, a call of a
// template, a variable read as $R, $(value R) or $($(N)), a computed call name, the file and shell
// functions, a != or := assignment holding shell output, a chain of 80 variables, and text in front
// of the reference. These rows are the shapes issue #554 and its review reported. The accepted
// rows declare nothing and stay appendable: a $(shell ...) in
// a rule's prerequisites or a conditional is never parsed as makefile syntax, computed-name-suffix
// binds a variable whose computed name cannot be .RECIPEPREFIX, and the silent rows
// are lines made only of info, warning and error calls, which expand to nothing whatever they
// print -- cobra-missing-tool-warning holds the lines of spf13/cobra's Makefile, a public dogfood
// repository (.config/dogfood/public-suite.json). error-inside-if declares nothing either and is
// refused all the same: an $(if ...) around the call is a bare expansion whose branches could hold
// a rule, and the reader evaluates nothing. computed-assignment declares nothing and is refused
// too: its computed name may expand to .RECIPEPREFIX, which changes how Make reads every recipe
// line after it. silent-call-then-reference shows that text beside the calls is a bare expansion
// again.
var makefileBareExpansionRows = map[string]docsLintOwnershipRow{
	"literal-rule":               {"$(if X,docs-lint: ; @echo template)\n", true},
	"nested-literal-rule":        {"X = 1\n$(if $(X),docs-lint: ; @echo template)\n", true},
	"brace-literal-rule":         {"X = 1\n${if ${X},docs-lint: ; @echo template}\n", true},
	"foreach-literal-rule":       {"X = 1\n$(foreach t,docs-lint,$(if $(X),$(t): ; @echo template))\n", true},
	"single-line-template":       {"make-rule = $(1): ; @echo template\n$(call make-rule,docs-lint)\n", true},
	"foreach-call-template":      {"make-rule = $(1): ; @echo template\n$(foreach t,docs-lint,$(call make-rule,$(t)))\n", true},
	"if-call-template":           {"make-rule = $(1): ; @echo template\nX = 1\n$(if $(X),$(call make-rule,docs-lint))\n", true},
	"strip-call-template":        {"make-rule = $(1): ; @echo template\n$(strip $(call make-rule,docs-lint))\n", true},
	"indirect-rule-in-if":        {"R = docs-lint: ; @echo template\n$(if 1,$(R))\n", true},
	"concatenated-rule":          {"A = docs-lint\nB = : ; @echo template\n$(A)$(B)\n", true},
	"concatenated-rule-indirect": {"A = docs-lint\nB = : ; @echo template\nR = $(A)$(B)\n$(R)\n", true},
	"indirect-colon-template":    {"C = :\nmake-rule = $(1)$(C) ; @echo template\n$(call make-rule,docs-lint)\n", true},
	"single-letter-reference":    {"R = docs-lint: ; @echo template\n$R\n", true},
	"value-function":             {"R = docs-lint: ; @echo template\n$(value R)\n", true},
	"computed-variable-name":     {"X = docs-lint: ; @echo template\nN = X\n$($(N))\n", true},
	"computed-call-name":         {"make-rule = $(1): ; @echo template\nT = make-rule\n$(call $(T),docs-lint)\n", true},
	"file-function":              {"$(file <rules.txt)\n", true},
	"shell-function":             {"$(shell cat rules.txt)\n", true},
	"shell-stderr-silenced":      {"$(shell cat rules.txt 2>/dev/null)\n", true},
	"shell-assignment":           {"R != cat rules.txt\n$(R)\n", true},
	"simple-shell-assignment":    {"R := $(shell cat rules.txt 2>/dev/null)\n$(R)\n", true},
	"variable-chain":             {variableChain(80), true},
	"prefixed-expansion":         {"R = -lint: ; @echo template\ndocs$(R)\n", true},
	"info-literal-rule":          {"$(info docs-lint: ; @echo template)\nall: ; @echo all\n", false},
	"warning-tab-literal-rule":   {"$(warning\tdocs-lint: ; @echo template)\nall: ; @echo all\n", false},
	"cobra-missing-tool-warning": {"ifeq (, $(shell which golangci-lint))\n$(warning \"could not find golangci-lint in $(PATH), run: curl -sfL https://install.goreleaser.com/github.com/golangci/golangci-lint.sh | sh\")\nendif\nall: ; @echo all\n", false},
	"silent-call-then-reference": {"R = docs-lint: ; @echo template\n$(warning x) $(R)\n", true},
	"error-inside-if":            {"V = 1\n$(if $(V),,$(error V: set it))\nall: ; @echo all\n", true},
	"computed-assignment":        {"A = 1\n$(if $(A),docs-lint:c) = x\nall: ; @echo all\n", true},
	"computed-name-suffix":       {"PKG = app\n$(PKG)_SRCS := a.c\nall: ; @echo all\n", false},
	"shell-in-prerequisites":     {"all: $(shell echo a.c b.c)\n\t@echo all\n", false},
	"shell-in-conditional":       {"ifeq ($(shell echo x),x)\nX = 1\nendif\nall: ; @echo all\n", false},
}

// variableChain returns a Makefile in which V1 refers to V2 and so on up to Vn, which holds a
// docs-lint rule, and a bare $(V1) expands the chain.
func variableChain(n int) string {
	var chain strings.Builder
	for i := 1; i < n; i++ {
		chain.WriteString("V" + strconv.Itoa(i) + " = $(V" + strconv.Itoa(i+1) + ")\n")
	}
	chain.WriteString("V" + strconv.Itoa(n) + " = docs-lint: ; @echo template\n$(V1)\n")
	return chain.String()
}

func TestMakefileBareExpansionRuleText(t *testing.T) {
	assertDocsLintOwnership(t, makefileBareExpansionRows)
}

// A computed target name counts with the value the file fixes for it (issue #537): each variable
// bound once with ":=" to literal text and references bound the same way above it, as in the
// five-variable shape an adopter repository reported (testsupport.MakefileLiteralChains). Such a
// file declares no docs-lint and gains the documentation gate; a chain that expands to docs-lint
// declares it and is refused. A second binding, a MAKEFLAGS binding that defines the variable, and
// a variable bound nowhere, whose value comes from the environment, leave the name to Make as
// before: the first two declare docs-lint, the last declares nothing in the replay's environment
// and is refused all the same.
var makefileComputedTargetRows = map[string]docsLintOwnershipRow{
	"literal-chains":         {testsupport.MakefileLiteralChains, false},
	"chain-to-docs-lint":     {"GATE := docs\nNAME := $(GATE)-lint\n$(NAME): ; @echo operator\n", true},
	"chain-bound-twice":      {"NAME := build\nNAME := docs-lint\n$(NAME): ; @echo operator\n", true},
	"chain-through-flags":    {"NAME := build\nMAKEFLAGS += NAME=docs-lint\n$(NAME): ; @echo operator\n", true},
	"bound-from-environment": {"$(NAME): ; @echo operator\nall: ; @echo all\n", true},
}

func TestMakefileComputedTargetsFollowFixedValues(t *testing.T) {
	assertDocsLintOwnership(t, makefileComputedTargetRows)
}

// makeDeclaresDocsLint names the rows GNU Make 4.4.1 expands into a docs-lint rule; every other
// row declares none.
var makeDeclaresDocsLint = []string{
	"eval-expanded", "brace-eval-expanded", "bare-expansion", "bare-call", "bare-call-nested-arg",
	"if-nested-arg", "info-variable",
	"literal-rule", "nested-literal-rule", "brace-literal-rule", "foreach-literal-rule",
	"single-line-template", "foreach-call-template", "if-call-template", "strip-call-template",
	"indirect-rule-in-if", "concatenated-rule", "concatenated-rule-indirect", "indirect-colon-template",
	"single-letter-reference", "value-function", "computed-variable-name", "computed-call-name",
	"file-function", "shell-function", "shell-stderr-silenced", "shell-assignment",
	"simple-shell-assignment", "variable-chain", "prefixed-expansion", "silent-call-then-reference",
	"chain-to-docs-lint", "chain-bound-twice", "chain-through-flags",
}

// Replayed against the installed GNU Make for every row of both tables. Make's answer must be the
// one recorded in makeDeclaresDocsLint, and the reader must not fail open against it: a Makefile
// Make expands into a docs-lint rule is refused, and an accepted one has no docs-lint rule before
// the merge and exactly one managed recipe after it.
func TestMakefileDefineOwnershipGNUReplay(t *testing.T) {
	makePath := testsupport.GNUMake(t)
	// Make imports the environment as variables, so a caller's P, A or MAKEFLAGS would change
	// what the rows that reference $(P) and $(A) expand to.
	for _, name := range []string{"P", "A", "B", "C", "N", "R", "T", "X", "V", "GATE", "NAME", "TOOLS", "SRCS", "CC", "MAKEFLAGS", "MFLAGS", "GNUMAKEFLAGS", "MAKEFILES"} {
		t.Setenv(name, "")
	}
	for _, rows := range []map[string]docsLintOwnershipRow{makefileDefineOwnershipRows, makefileBareExpansionRows, makefileComputedTargetRows} {
		for name, tc := range rows {
			t.Run(name, func(t *testing.T) { replayDocsLintRow(t, makePath, name, tc.makefile) })
		}
	}
}

// replayDocsLintRow replays one row. A row that runs a shell command needs sh and cat
// (testsupport.RequireGNUMakeShell).
func replayDocsLintRow(t *testing.T, makePath, name, makefile string) {
	t.Helper()
	if strings.Contains(makefile, "$(shell") || strings.Contains(makefile, "!=") {
		testsupport.RequireGNUMakeShell(t, "cat")
	}
	out, err := replayDocsLint(t, makePath, makefile)
	declared, recorded := err == nil, slices.Contains(makeDeclaresDocsLint, name)
	if declared != recorded {
		t.Fatalf("GNU Make declares docs-lint = %v, recorded %v: %q %v", declared, recorded, out, err)
	}
	refused := util.MakefileMayDefineTarget(makefile, "docs-lint")
	if declared && !refused {
		t.Fatalf("GNU Make declares docs-lint, the reader accepts the Makefile: %q", out)
	}
	if !refused {
		replayAcceptedDocsLint(t, makePath, makefile)
	}
}

// replayAcceptedDocsLint checks an accepted Makefile both ways: GNU Make finds no docs-lint rule
// before the merge and selects exactly one managed recipe after it.
func replayAcceptedDocsLint(t *testing.T, makePath, makefile string) {
	t.Helper()
	if _, err := replayDocsLint(t, makePath, makefile); err == nil {
		t.Fatal("GNU Make found a docs-lint rule the reader accepted as absent")
	}
	merged, err := mergeDocumentationMakefile(makefile, false)
	if err != nil {
		t.Fatal(err)
	}
	out, err := replayDocsLint(t, makePath, merged)
	if err != nil || strings.Count(out, "node tools/markdownlint/verify.mjs") != 1 {
		t.Fatalf("GNU Make did not select exactly one managed recipe: %q %v", out, err)
	}
}

// replayDocsLint runs "make -n docs-lint" over makefile, beside a rules.txt holding a docs-lint
// rule for the rows that read it.
func replayDocsLint(t *testing.T, makePath, makefile string) (string, error) {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, makefileName), makefile)
	mustWrite(t, filepath.Join(root, "rules.txt"), "docs-lint: ; @echo template\n")
	return util.RunCommand(t.Context(), root, makePath, "--no-print-directory", "-n", "docs-lint")
}

// End to end: a project whose Makefile defines a helper used only through $(call ...) gains the
// documentation gate beside its own verify-all instead of failing adoption.
func TestAdoptionDocumentationGateAcceptsCalledDefine(t *testing.T) {
	root := newTestRepo(t, "documentation-called-define")
	makefile := calledDefineMakefile + "\n.PHONY: verify-all\nverify-all: lint\n"
	mustWrite(t, filepath.Join(root, makefileName), makefile)
	opts := AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	got := mustRead(t, filepath.Join(root, makefileName))
	if !strings.HasPrefix(got, makefile) || strings.Count(got, DocumentationMakefileBlock()) != 1 {
		t.Fatalf("custom Makefile did not gain exactly one documentation block:\n%s", got)
	}
}

// End to end: a project whose Makefile names its rule targets through ":=" chains of literals
// (issue #537) gains the documentation gate beside its own verify-all instead of failing adoption
// with "makefile may define target docs-lint outside the Praetor-managed block".
func TestAdoptionDocumentationGateAcceptsLiteralChainTargets(t *testing.T) {
	root := newTestRepo(t, "documentation-literal-chains")
	makefile := testsupport.MakefileLiteralChains + "\n.PHONY: verify-all\nverify-all: all\n"
	mustWrite(t, filepath.Join(root, makefileName), makefile)
	opts := AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	got := mustRead(t, filepath.Join(root, makefileName))
	if !strings.HasPrefix(got, makefile) || strings.Count(got, DocumentationMakefileBlock()) != 1 {
		t.Fatalf("the Makefile did not gain exactly one documentation block:\n%s", got)
	}
}

// End to end: a project whose Makefile declares docs-lint through a bare expansion of a
// single-line template (issue #554) keeps its rule: adoption stops with the collision instead of
// appending a block whose docs-lint recipe would override the operator's, and the Makefile is
// unchanged.
func TestAdoptionDocumentationGateRefusesBareExpansion(t *testing.T) {
	for name, makefile := range map[string]string{
		"bare-template-expansion": "make-rule = $(1): ; @echo operator\n$(call make-rule,docs-lint)\n\n.PHONY: verify-all\nverify-all: docs-lint\n",
		// The "!=" form of issue #304 binds a variable and declares no target, but Make expands the
		// command's output as makefile text wherever the variable is expanded, and the reader runs
		// no command (makefileCommandOutputRows in internal/util/makefile_target_test.go).
		"command-output-binding": "verify-all != date +%H:%M\nall:\n\t@echo original\n",
		// A computed name the file fixes counts like a literal one (issue #537).
		"computed-chain-to-docs-lint": "GATE := docs\nNAME := $(GATE)-lint\n$(NAME):\n\t@echo operator\n\n.PHONY: verify-all\nverify-all: $(NAME)\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := newTestRepo(t, "documentation-"+name)
			mustWrite(t, filepath.Join(root, makefileName), makefile)
			opts := AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)}
			_, err := Adopt(t.Context(), opts)
			if err == nil || !strings.Contains(err.Error(), "may define target docs-lint outside the Praetor-managed block") {
				t.Fatalf("adoption was not stopped by the collision: %v", err)
			}
			if got := mustRead(t, filepath.Join(root, makefileName)); got != makefile {
				t.Fatalf("the refused Makefile changed:\n%s", got)
			}
		})
	}
}

// cobraShapedMakefile has the shape of spf13/cobra's Makefile, a public dogfood repository
// (.config/dogfood/public-suite.json): a $(warning ...) inside a conditional before the first
// rule, and recipes that open with $(info ...) and run $(shell ...).
const cobraShapedMakefile = "SRC=$(shell find . -name \"*.go\")\n\n" +
	"ifeq (, $(shell which golangci-lint))\n" +
	"$(warning \"could not find golangci-lint in $(PATH), run: curl -sfL https://install.goreleaser.com/github.com/golangci/golangci-lint.sh | sh\")\n" +
	"endif\n\n" +
	".PHONY: fmt test\n\n" +
	"all: fmt test\n\n" +
	"fmt:\n" +
	"\t$(info ******************** checking formatting ********************)\n" +
	"\t@test -z $(shell gofmt -l $(SRC)) || (gofmt -d $(SRC); exit 1)\n\n" +
	"test:\n" +
	"\t$(info ******************** running tests ********************)\n" +
	"\tgo test -v ./...\n"

// End to end: a Makefile whose only bare expansion is a line of silent calls declares neither
// verify-all nor docs-lint, so adoption appends both, the verification targets and the
// documentation gate, instead of preserving a verify-all that does not exist or stopping on a
// docs-lint collision.
func TestAdoptionAppendsBesideSilentWarning(t *testing.T) {
	root := newTestRepo(t, "documentation-silent-warning")
	mustWrite(t, filepath.Join(root, makefileName), cobraShapedMakefile)
	opts := AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatalf("adoption beside a silent $(warning ...) failed: %v", err)
	}
	got := mustRead(t, filepath.Join(root, makefileName))
	if !strings.HasPrefix(got, cobraShapedMakefile) || !util.MakefileHasTarget(got, verificationTarget) ||
		strings.Count(got, DocumentationMakefileBlock()) != 1 {
		t.Fatalf("the Makefile did not gain verify-all and exactly one documentation block:\n%s", got)
	}
}
