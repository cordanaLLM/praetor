package adopt

import (
	"context"
	"strings"
)

// VerificationPlan declares selected project commands, never an execution result.
// Unavailable plans produce failing scaffold recipes; custom recipes are preserved.
type VerificationPlan struct {
	Status   string              `json:"status"`
	Runtimes []string            `json:"runtimes"`
	Build    [][]string          `json:"build"`
	Test     [][]string          `json:"test"`
	Reasons  []string            `json:"reasons,omitempty"`
	Limits   *VerificationLimits `json:"verification_limits,omitempty"`
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
	return resolveVerificationPlanWithLimits(ctx, root, nil)
}

func resolveVerificationPlanWithLimits(ctx context.Context, root string, requested *VerificationLimits) (*VerificationPlan, error) {
	limits, err := NormalizeVerificationLimits(requested)
	if err != nil {
		return nil, err
	}
	inputs, err := loadVerificationInputsWithLimits(ctx, root, limits)
	if err != nil {
		return nil, err
	}
	plan := &VerificationPlan{Status: verificationDeclared, Runtimes: []string{}, Build: [][]string{}, Test: [][]string{}, Limits: &limits}
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

// ObserveVerificationPlan exposes the bounded declarative planner to read-only
// observers. It does not adopt files or execute project commands.
func ObserveVerificationPlan(ctx context.Context, root string) (*VerificationPlan, error) {
	return resolveVerificationPlan(ctx, root)
}

// ObserveVerificationPlanWithLimits exposes the bounded planner with explicit limits.
func ObserveVerificationPlanWithLimits(ctx context.Context, root string, limits *VerificationLimits) (*VerificationPlan, error) {
	return resolveVerificationPlanWithLimits(ctx, root, limits)
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

// verificationRecipePrefix starts every generated command line with the shell's exec builtin, so
// make hands the line to the shell instead of running it directly.
//
// GNU make runs a line without a shell metacharacter itself, splitting it with its own parser, and
// its Windows build does that differently from sh. Measured with GNU Make 4.4.1 for Windows32 and
// Git for Windows' sh: the argument project'quote.csproj, quoted as below, arrived merged with
// every argument after it, and a quoted ~ arrived as the home directory. With exec in the line
// make recognizes a builtin and passes the line to sh, which delivers every argument as quoted;
// exec still replaces the shell, so the exit status is the command's own.
const verificationRecipePrefix = "\t@exec "

// priorVerificationRecipePrefix is how generated command lines began before
// verificationRecipePrefix. A Makefile rendered that way is still Praetor's own output and must
// not be mistaken for a custom verify-all that adoption has to preserve.
const priorVerificationRecipePrefix = "\t@"

func verificationRecipe(plan *VerificationPlan, commands [][]string) string {
	return verificationRecipeWith(plan, commands, verificationRecipePrefix)
}

func verificationRecipeWith(plan *VerificationPlan, commands [][]string, prefix string) string {
	if plan.Status == verificationUnavailable || emptyVerificationCommands(commands) {
		return "\t@printf '%s\\n' 'Project verification unavailable; see the adoption report and AGENTS.md.' >&2\n\t@exit 1\n"
	}
	var result strings.Builder
	for _, command := range commands {
		result.WriteString(prefix)
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
