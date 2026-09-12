package adopt

import (
	"context"
	"strings"
)

// VerificationPlan declares selected project commands, never an execution result.
// Unavailable plans produce failing scaffold recipes; custom recipes are preserved.
type VerificationPlan struct {
	Status   string     `json:"status"`
	Runtimes []string   `json:"runtimes"`
	Build    [][]string `json:"build"`
	Test     [][]string `json:"test"`
	Reasons  []string   `json:"reasons,omitempty"`
}

const (
	verificationDeclared    = "declared-unverified"
	verificationUnavailable = "unavailable"
	verificationPreserved   = "preserved-unverified"
)

func containsRuntime(plan *VerificationPlan, runtime string) bool {
	for _, found := range plan.Runtimes {
		if found == runtime {
			return true
		}
	}
	return false
}

func resolveVerificationPlan(ctx context.Context, root string) (*VerificationPlan, error) {
	inputs, err := loadVerificationInputs(ctx, root)
	if err != nil {
		return nil, err
	}
	plan := &VerificationPlan{Status: verificationDeclared, Runtimes: []string{}, Build: [][]string{}, Test: [][]string{}}
	if err := addNodeVerification(plan, inputs); err != nil {
		return nil, err
	}
	if err := addDotnetVerification(plan, inputs); err != nil {
		return nil, err
	}
	addStandardVerification(plan, inputs)
	if len(plan.Runtimes) == 0 {
		plan.unavailable("No supported build-system marker or explicit test runner was found; define and exercise a project verify-all target.")
	}
	if len(plan.Build) == 0 || len(plan.Test) == 0 {
		plan.unavailable("A required build or test command is absent; define and exercise the project's verify-all contract.")
	}
	if len(plan.Reasons) > 0 {
		plan.Status = verificationUnavailable
	}
	preserveCustomVerification(plan, inputs.files["Makefile"])
	return plan, nil
}

func (p *VerificationPlan) unavailable(reason string) {
	p.Reasons = append(p.Reasons, reason)
}

func addStandardVerification(p *VerificationPlan, inputs verificationInputs) {
	if inputs.has("go.mod") {
		p.Runtimes = append(p.Runtimes, "go")
		p.Build = append(p.Build, []string{"go", "build", "-v", "./..."})
		p.Test = append(p.Test, []string{"go", "test", "-v", "-race", "./..."})
	}
	if inputs.has("Cargo.toml") {
		p.Runtimes = append(p.Runtimes, "cargo")
		p.Build = append(p.Build, []string{"cargo", "build", "--locked"})
		p.Test = append(p.Test, []string{"cargo", "test", "--locked"})
	}
	addPythonVerification(p, inputs)
	for _, marker := range []string{"meson.build", "core/meson.build", "CMakeLists.txt", "pom.xml", "build.gradle", "build.gradle.kts", "pubspec.yaml"} {
		if inputs.has(marker) {
			p.Runtimes = append(p.Runtimes, marker)
			p.unavailable("Select and exercise the project's configured native test/build commands for " + marker + "; a governance profile cannot select them.")
		}
	}
}

func verificationRecipe(plan *VerificationPlan, commands [][]string) string {
	if plan.Status == verificationUnavailable || emptyVerificationCommands(commands) {
		return "\t@printf '%s\\n' 'Project verification unavailable; see the adoption report and AGENTS.md.' >&2\n\t@exit 1\n"
	}
	var result strings.Builder
	for _, command := range commands {
		result.WriteString("\t@")
		result.WriteString(strings.ReplaceAll(verificationCommand(command), "$", "$$"))
		result.WriteByte('\n')
	}
	return result.String()
}

func verificationCommand(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
	}
	return strings.Join(quoted, " ")
}

func verificationTestText(plan *VerificationPlan) string {
	if plan.Status == verificationUnavailable {
		return "# Project verification unavailable: " + strings.Join(plan.Reasons, " ") + "\nexit 1"
	}
	lines := make([]string, 0, len(plan.Test)+1)
	lines = append(lines, "# Declared commands only; run them before claiming application verification.")
	for _, command := range append(append([][]string(nil), plan.Build...), plan.Test...) {
		lines = append(lines, verificationCommand(command))
	}
	return strings.Join(lines, "\n")
}

func emptyVerificationCommands(commands [][]string) bool {
	if len(commands) == 0 {
		return true
	}
	for _, command := range commands {
		if len(command) == 0 {
			return true
		}
	}
	return false
}

func (p *VerificationPlan) notice() string {
	reason := "Selected commands have not been executed by adoption."
	if len(p.Reasons) > 0 {
		reason = strings.Join(p.Reasons, " ")
	}
	return "Project verification " + p.Status + ": " + reason
}
