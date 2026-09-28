package adopt

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// A define only binds a variable; Make turns its body into rules when something parses the
// expansion as makefile syntax. Every row was measured against GNU Make 4.4.1 with "make -n
// docs-lint": the accepted rows answer "No rule to make target 'docs-lint'", the refused rows
// either declare the target or leave the file to Make (include, an unterminated define). The one
// deliberate over-refusal is info-bare-expansion: Make answers it with "No rule", but the reader
// does not evaluate functions, so any bare expansion beside a define counts.
var makefileDefineOwnershipRows = map[string]struct {
	makefile string
	refused  bool
}{
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
	"info-bare-expansion":  {"define docs-rule\n@echo template\nendef\n$(info building)\n", true},
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
	for name, tc := range makefileDefineOwnershipRows {
		t.Run(name, func(t *testing.T) {
			if got := mayDefineTarget(tc.makefile, "docs-lint"); got != tc.refused {
				t.Fatalf("mayDefineTarget(%q, docs-lint) = %v, want %v", tc.makefile, got, tc.refused)
			}
			_, err := mergeDocumentationMakefile(tc.makefile, false)
			if (err != nil) != tc.refused {
				t.Fatalf("mergeDocumentationMakefile refused = %v, want %v: %v", err != nil, tc.refused, err)
			}
		})
	}
}

// A define body is variable text: a rule line inside one declares nothing, while the same line
// after endef does. The verify-all reader shares this, so a define holding a verify-all template
// no longer makes adoption preserve a rule Make cannot run.
func TestMakefileDefineBodyDeclaresNoTarget(t *testing.T) {
	body := "define gates\nverify-all: lint\n\t@echo template\nendef\n"
	if hasVerificationTarget(body, "verify-all") || mayDefineVerificationTarget(body) {
		t.Fatal("a rule inside a define body was read as a declared target")
	}
	if !hasVerificationTarget(body+"verify-all: lint\n", "verify-all") {
		t.Fatal("a rule after endef was not read")
	}
	nested := "define outer\ndefine inner\nendef\nverify-all: lint\nendef\n"
	if hasVerificationTarget(nested, "verify-all") {
		t.Fatal("the inner endef closed the outer define")
	}
	for _, assignment := range []string{"define := x\n", "define = x\n", "override define ?= x\n"} {
		if !hasVerificationTarget(assignment+"verify-all: lint\n", "verify-all") {
			t.Fatalf("%q binds a variable named define but was read as opening a block", assignment)
		}
	}
}

// Replayed both directions against the installed GNU Make: an accepted Makefile has no docs-lint
// rule before the merge and exactly one managed recipe after it; a refused one already answers
// docs-lint on its own, so appending the block would override an operator rule.
func TestMakefileDefineOwnershipGNUReplay(t *testing.T) {
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skipf("make is not on PATH, so the replay cannot run; the table test covers the reader: %v", err)
	}
	for _, name := range []string{"called-in-recipe", "body-names-target", "nested-define-closed", "continued-assignment"} {
		t.Run(name, func(t *testing.T) {
			makefile := makefileDefineOwnershipRows[name].makefile
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
		})
	}
	for _, name := range []string{"eval-expanded", "brace-eval-expanded", "bare-expansion", "bare-call"} {
		t.Run(name, func(t *testing.T) {
			out, err := replayDocsLint(t, makePath, makefileDefineOwnershipRows[name].makefile)
			if err != nil || !strings.Contains(out, "echo template") {
				t.Fatalf("GNU Make did not expand the define into a docs-lint rule: %q %v", out, err)
			}
		})
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
