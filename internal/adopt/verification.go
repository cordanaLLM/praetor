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
	// Declared keeps the build and test commands the project markers declare when a preserved
	// custom verify-all replaces Build and Test, so the harness still lists the project's real
	// commands after a --force refresh instead of only the preserved target (BUG-949).
	Declared [][]string `json:"declared,omitempty"`
	// SelectedBy names the configuration that selected a command where the marker's presence alone
	// does not say which one did: the pytest configuration file, and the table or section in it,
	// that selected python3 -m pytest. The adoption report names it (notice), so an adopter can
	// check the file pytest itself will read (#594).
	SelectedBy []string `json:"selected_by,omitempty"`
	// SourceLanguages names the languages the walk found sources of, whether or not a build
	// marker in Runtimes also declares them: sourceLanguageC for C or C++ sources the audit's
	// native scan reads, so C/C++ a Makefile or script compiles is detected too (#549). Only
	// files git reports as the repository's own count. It selects the HISS clauses the harness
	// renders (planLanguages), never a build or test command.
	SourceLanguages []string `json:"source_languages,omitempty"`
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
	addZigVerification(plan, inputs)
	plan.SourceLanguages = inputs.cSources.languages(ctx, root)
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

// ObserveVerificationPlanWithLimits exposes the bounded declarative planner to read-only
// observers under limits (nil selects DefaultVerificationLimits). It does not adopt files or
// execute project commands.
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
			p.nativeStepUnavailable(marker, "test/build")
		}
	}
}

// nativeStepUnavailable records that the project's own steps for marker, a build whose steps
// a governance profile cannot select, are missing from the plan.
func (p *VerificationPlan) nativeStepUnavailable(marker, steps string) {
	p.unavailable("Select and exercise the project's configured native " + steps + " commands for " + marker + "; a governance profile cannot select them.")
}

// zigBuildMarker is a Zig build script at the repository root; zigRuntime is its runtime.
const (
	zigBuildMarker = "build.zig"
	zigRuntime     = "zig"
)

// addZigVerification runs `zig build`, the build script's default install step, ahead of every
// other build and test command: a language build such as Cargo's links the native libraries the
// Zig build produces, so the native step comes first. build.zig is a program, and `zig build test`
// runs only where it declares a test step, so the Zig build supplies no test command of its own:
// without a language marker that declares one the plan is unavailable and names the missing
// native test step, as for a meson or CMake marker.
func addZigVerification(p *VerificationPlan, inputs verificationInputs) {
	if !inputs.has(zigBuildMarker) {
		return
	}
	p.Runtimes = append(p.Runtimes, zigRuntime)
	p.Build = append([][]string{{"zig", "build"}}, p.Build...)
	if len(p.Test) == 0 {
		p.nativeStepUnavailable(zigBuildMarker, "test")
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

// unavailableVerificationRecipe is the recipe a generated rule gets when the plan selects no
// runnable command for it: it names the report and fails. A Makefile adoption does not recognise
// as its own current output that still holds this recipe has a verify-all that can only exit 1
// (preserveCustomVerification).
const unavailableVerificationRecipe = "\t@printf '%s\\n' 'Project verification unavailable; see the adoption report and AGENTS.md.' >&2\n\t@exit 1\n"

func verificationRecipeWith(plan *VerificationPlan, commands [][]string, prefix string) string {
	if plan.Status == verificationUnavailable || emptyVerificationCommands(commands) {
		return unavailableVerificationRecipe
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
	commands := plan.commands()
	if plan.Status == verificationPreserved && len(plan.Declared) > 0 {
		commands = plan.Declared
	}
	lines := make([]string, 0, len(commands)+1)
	lines = append(lines, "# Declared commands only; run them before claiming application verification.")
	for _, command := range commands {
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

// commands returns the plan's build commands followed by its test commands, the order verify-all
// runs them in.
func (p *VerificationPlan) commands() [][]string {
	return append(append([][]string(nil), p.Build...), p.Test...)
}

// notice is the report's warning for the plan. For an unavailable plan it also says what the
// verify-all adoption writes does, which of the build and test commands is missing, and every
// command discovery found that the failing recipe does not run, so the adopter can wire them into
// the project's own contract. For any plan it names the configuration that selected a command
// (SelectedBy) (#594).
func (p *VerificationPlan) notice() string {
	reason := "Selected commands have not been executed by adoption."
	if len(p.Reasons) > 0 {
		reason = strings.Join(p.Reasons, " ")
	}
	parts := []string{"Project verification " + p.Status + ": " + reason}
	if p.Status == verificationUnavailable {
		parts = append(parts, p.unavailableDetail()...)
	}
	if len(p.SelectedBy) > 0 {
		parts = append(parts, "Selected by: "+strings.Join(p.SelectedBy, "; ")+".")
	}
	return strings.Join(parts, " ")
}

// unavailableDetail says what an unavailable plan's verify-all runs, which of the build and test
// commands the plan is missing, and which discovered commands verify-all does not run.
func (p *VerificationPlan) unavailableDetail() []string {
	detail := []string{"Adoption's verify-all runs the governance checks and then exits 1."}
	var missing []string
	if len(p.Build) == 0 {
		missing = append(missing, "a build command")
	}
	if len(p.Test) == 0 {
		missing = append(missing, "a test command")
	}
	if len(missing) > 0 {
		detail = append(detail, "Missing: "+strings.Join(missing, " and ")+".")
	}
	commands := p.commands()
	if len(commands) == 0 {
		return detail
	}
	quoted := make([]string, 0, len(commands))
	for _, command := range commands {
		quoted = append(quoted, verificationCommand(command))
	}
	return append(detail, "Discovered but not written to verify-all: "+strings.Join(quoted, "; ")+".")
}
