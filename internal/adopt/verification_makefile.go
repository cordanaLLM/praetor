package adopt

import (
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Only exact historical Praetor output is eligible for automatic replacement.
// Arbitrary user recipes, including edited generated files, remain untouched.
func isLegacyVerificationMakefile(data string) bool {
	if data == legacyVerificationStub || data == strings.TrimPrefix(legacyVerificationStub, "\n") {
		return true
	}
	for _, commands := range [][2]string{
		{"go test -v -race ./...", "go build -v ./..."},
		{"meson test -C core/build --suite=fast", "meson compile -C core/build"},
	} {
		if data == legacyVerificationMakefile(commands[0], commands[1]) {
			return true
		}
	}
	return false
}

const legacyVerificationStub = "\n.PHONY: all verify-all audit compile-context build test\n\nverify-all:\n\t@echo \"Running verification...\"\n\ncompile-context:\n\t@standardsctl compile-context\n\naudit:\n\t@standardsctl audit\n\ntest:\n\t@go test -v -race ./...\n\nbuild:\n\t@go build -v ./...\n"

func legacyVerificationMakefile(test, build string) string {
	return ".PHONY: all verify-all compile-context compile-context-verify audit build test\n\n" +
		"all: build\n\nverify-all: compile-context-verify audit test\n\n" +
		"compile-context:\n\t@standardsctl compile-context\n\n" +
		"compile-context-verify:\n\t@standardsctl compile-context --verify\n\n" +
		"audit:\n\t@standardsctl audit\n\ntest:\n\t@" + test + "\n\nbuild:\n\t@" + build + "\n"
}

func preserveCustomVerification(plan *VerificationPlan, data []byte) {
	text := string(data)
	if !mayDefineVerificationTarget(text) || isLegacyVerificationMakefile(text) || text == buildMakefile(plan) {
		return
	}
	plan.Status = verificationPreserved
	plan.Reasons = append(plan.Reasons, "Existing custom verify-all is preserved; execute and review it before claiming project verification.")
	plan.Build = [][]string{}
	plan.Test = [][]string{{"make", "verify-all"}}
}

func hasVerificationTarget(data, target string) bool {
	for _, line := range strings.Split(data, "\n") {
		if strings.HasPrefix(line, "\t") || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		left, _, ok := strings.Cut(line, ":")
		if ok {
			for _, name := range strings.Fields(left) {
				if name == target {
					return true
				}
			}
		}
	}
	return false
}

func appendVerificationTargets(existing string, plan *VerificationPlan) string {
	var result strings.Builder
	result.WriteString(existing)
	result.WriteString("\n# Praetor declared verification; existing project recipes remain unchanged.\n" +
		util.MakefileCLIVariable + ".PHONY: verify-all\nverify-all:\n\t@$(PRAETORCTL) compile-context --verify\n\t@$(PRAETORCTL) audit\n")
	result.WriteString(verificationRecipe(plan, plan.Build))
	result.WriteString(verificationRecipe(plan, plan.Test))
	for _, target := range []string{"compile-context", "audit"} {
		if !hasVerificationTarget(existing, target) {
			result.WriteString("\n" + target + ":\n\t@$(PRAETORCTL) " + target + "\n")
		}
	}
	return result.String()
}

// Includes, generated target names and pattern rules require Make evaluation.
// Never append a potentially overriding recipe when ownership is ambiguous.
func mayDefineVerificationTarget(data string) bool {
	if hasVerificationTarget(data, "verify-all") {
		return true
	}
	for _, line := range strings.Split(data, "\n") {
		if strings.HasPrefix(line, "\t") {
			continue
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "include", "-include", "sinclude", "define", "override":
			return true
		}
		if strings.Contains(line, "$(eval") {
			return true
		}
		left, _, ok := strings.Cut(line, ":")
		if ok && strings.ContainsAny(left, "$%") {
			return true
		}
	}
	return false
}
