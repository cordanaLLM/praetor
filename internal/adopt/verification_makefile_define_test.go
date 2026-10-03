package adopt

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// A define only binds a variable; Make turns its body into rules when something parses the
// expansion as makefile syntax. Every row was measured against GNU Make 4.4.1 with "make -n
// docs-lint": the accepted rows answer "No rule to make target 'docs-lint'", the refused rows
// either declare the target or leave the file to Make (include, an unterminated define). The reader
// evaluates no function except to know that info, warning and error expand to nothing, so any other
// bare expansion beside a define counts, $(if ...) included; "$(info)" with no blank after the
// name references a variable named info, which info-variable shows can hold the define.
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

// A top-level bare expansion is parsed as makefile syntax after Make expands it, so rule text in a
// function argument declares a rule without any define. Measured against GNU Make 4.4.1 with "make
// -n docs-lint": the refused rows declare docs-lint, the accepted rows answer "No rule to make
// target 'docs-lint'" because info, warning and error expand to nothing and computed-assignment
// binds a variable. Nesting decides: the colon sits inside a nested reference in the refused
// nested and foreach rows and in the accepted error-inside-if and computed-assignment rows.
var makefileBareExpansionRows = map[string]docsLintOwnershipRow{
	"literal-rule":               {"$(if X,docs-lint: ; @echo template)\n", true},
	"nested-literal-rule":        {"X = 1\n$(if $(X),docs-lint: ; @echo template)\n", true},
	"brace-literal-rule":         {"X = 1\n${if ${X},docs-lint: ; @echo template}\n", true},
	"foreach-literal-rule":       {"X = 1\n$(foreach t,docs-lint,$(if $(X),$(t): ; @echo template))\n", true},
	"foreach-call-template":      {"make-rule = $(1): ; @echo template\n$(foreach t,docs-lint,$(call make-rule,$(t)))\n", true},
	"if-call-template":           {"make-rule = $(1): ; @echo template\nX = 1\n$(if $(X),$(call make-rule,docs-lint))\n", true},
	"strip-call-template":        {"make-rule = $(1): ; @echo template\n$(strip $(call make-rule,docs-lint))\n", true},
	"indirect-rule-in-if":        {"R = docs-lint: ; @echo template\n$(if 1,$(R))\n", true},
	"concatenated-rule-indirect": {"A = docs-lint\nB = : ; @echo template\nR = $(A)$(B)\n$(R)\n", true},
	"indirect-colon-template":    {"C = :\nmake-rule = $(1)$(C) ; @echo template\n$(call make-rule,docs-lint)\n", true},
	"info-literal-rule":          {"$(info docs-lint: ; @echo template)\nall: ; @echo all\n", false},
	"warning-tab-literal-rule":   {"$(warning\tdocs-lint: ; @echo template)\nall: ; @echo all\n", false},
	"error-inside-if":            {"V = 1\n$(if $(V),,$(error V: set it))\nall: ; @echo all\n", false},
	"computed-assignment":        {"A = 1\n$(if $(A),docs-lint:c) = x\nall: ; @echo all\n", false},
}

func TestMakefileBareExpansionRuleText(t *testing.T) {
	assertDocsLintOwnership(t, makefileBareExpansionRows)
}

// makefileCandidates names the GNU Make candidates tried in order on any platform (HISS-21).
var makefileCandidates = []string{"make", "gmake", "mingw32-make"}

// resolveGNUMake finds the first GNU Make binary on PATH that reports GNU Make 4.x.
// If make is absent or not GNU Make 4.x, it skips the test with a stated reason (HISS-21).
func resolveGNUMake(t *testing.T) string {
	t.Helper()
	var tried []string
	for _, name := range makefileCandidates {
		path, err := exec.LookPath(name)
		if err != nil {
			tried = append(tried, fmt.Sprintf("%s (not on PATH)", name))
			continue
		}
		version, err := util.RunCommand(t.Context(), t.TempDir(), path, "--version")
		if err != nil {
			tried = append(tried, fmt.Sprintf("%s (%v)", path, err))
			continue
		}
		firstLine := strings.SplitN(version, "\n", 2)[0]
		if strings.HasPrefix(version, "GNU Make 4.") {
			return path
		}
		tried = append(tried, fmt.Sprintf("%s (%s)", path, firstLine))
	}
	t.Skipf("GNU Make 4.x is required for replay; tried: %s", strings.Join(tried, "; "))
	return ""
}

// Replayed both directions against the installed GNU Make: an accepted Makefile has no docs-lint
// rule before the merge and exactly one managed recipe after it; a refused one already answers
// docs-lint on its own, so appending the block would override an operator rule.
func TestMakefileDefineOwnershipGNUReplay(t *testing.T) {
	makePath := resolveGNUMake(t)
	// Make imports the environment as variables, so a caller's P, A or MAKEFLAGS would change
	// what the rows that reference $(P) and $(A) expand to.
	for _, name := range []string{"P", "A", "B", "C", "R", "X", "V", "MAKEFLAGS", "MFLAGS", "GNUMAKEFLAGS"} {
		t.Setenv(name, "")
	}
	accepted := map[string]string{}
	for _, name := range []string{"called-in-recipe", "body-names-target", "nested-define-closed", "continued-assignment", "info-prints-define"} {
		accepted[name] = makefileDefineOwnershipRows[name].makefile
	}
	for _, name := range []string{"info-literal-rule", "warning-tab-literal-rule", "error-inside-if", "computed-assignment"} {
		accepted[name] = makefileBareExpansionRows[name].makefile
	}
	for name, makefile := range accepted {
		t.Run(name, func(t *testing.T) { replayAcceptedDocsLint(t, makePath, makefile) })
	}
	refused := map[string]string{}
	for _, name := range []string{"eval-expanded", "brace-eval-expanded", "bare-expansion", "bare-call", "bare-call-nested-arg", "if-nested-arg", "info-variable"} {
		refused[name] = makefileDefineOwnershipRows[name].makefile
	}
	for _, name := range []string{
		"literal-rule", "nested-literal-rule", "brace-literal-rule", "foreach-literal-rule",
		"foreach-call-template", "if-call-template", "strip-call-template",
		"indirect-rule-in-if", "concatenated-rule-indirect", "indirect-colon-template",
	} {
		refused[name] = makefileBareExpansionRows[name].makefile
	}
	for name, makefile := range refused {
		t.Run(name, func(t *testing.T) {
			out, err := replayDocsLint(t, makePath, makefile)
			if err != nil || !strings.Contains(out, "echo template") {
				t.Fatalf("GNU Make did not expand the Makefile into a docs-lint rule: %q %v", out, err)
			}
		})
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

func replayDocsLint(t *testing.T, makePath, makefile string) (string, error) {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, makefileName), makefile)
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

// End to end: a project whose Makefile defines a single-line template expanded at top level
// (shape 1) may define docs-lint, so adoption refuses to append rather than overriding it.
func TestAdoptionDocumentationGateRefusesSingleLineTemplate(t *testing.T) {
	makefile := "make-rule = $(1): ; @echo operator\n$(call make-rule,docs-lint)\n"
	if !util.MakefileMayDefineTarget(makefile, "docs-lint") {
		t.Fatal("MakefileMayDefineTarget must report true for single-line template")
	}
	_, err := mergeDocumentationMakefile(makefile, false)
	if err == nil {
		t.Fatal("mergeDocumentationMakefile must refuse to append to a single-line template Makefile")
	}
}
