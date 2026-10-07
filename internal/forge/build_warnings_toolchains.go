package forge

import (
	"slices"
	"strconv"
	"strings"
)

// The judges of the build-system and Rust and Go toolchains MeasureBuildWarnings reads, each
// against the form its toolchain documents:
//
//   - CMake: CMAKE_COMPILE_WARNING_AS_ERROR, "Specify whether to treat warnings on compile as
//     errors", added in 3.24, and --compile-no-warning-as-error, which ignores it
//     (https://cmake.org/cmake/help/latest/variable/CMAKE_COMPILE_WARNING_AS_ERROR.html, cmake(1)).
//   - Meson: the werror built-in option, "Treat warnings as errors", set by meson setup --werror
//     or -Dwerror=true (https://mesonbuild.com/Builtin-options.html).
//   - Cargo and rustc: -D warnings (rustc lint levels), from CARGO_ENCODED_RUSTFLAGS, else
//     RUSTFLAGS, the first of cargo's flag sources it uses, or after cargo clippy's "--"
//     (https://doc.rust-lang.org/cargo/reference/config.html,
//     https://doc.rust-lang.org/clippy/continuous_integration/index.html).
//   - Go: go vet exits non-zero when it reports a problem; go test -vet=all runs every vet check
//     (https://pkg.go.dev/cmd/vet, https://pkg.go.dev/cmd/go).

// cmakeNoConfigure are the cmake options that select a mode other than configuring a build tree.
var cmakeNoConfigure = map[string]bool{
	"--build": true, "--install": true, "--open": true, "-E": true, "-P": true, "-N": true,
	"--version": true, "-version": true, "--help": true, "-help": true, "-h": true,
	"--find-package": true, "--system-information": true,
}

// cmakeFlagVariables are the compile-flag cache entries a configure run may carry -Werror in.
var cmakeFlagVariables = [...]string{"CMAKE_C_FLAGS", "CMAKE_CXX_FLAGS"}

// judgeCMake decides a cmake command: a configure run is a lane judged by its definitions; cmake
// --build is a lane only in a job where no earlier run: step configured, since what configured
// its tree cannot be read.
func (m *buildMeasure) judgeCMake(site *laneSite, args []string) (laneVerdict, bool) {
	key := site.jobKey(ToolchainCMake)
	if slices.Contains(args, "--build") {
		if m.configured[key] {
			return laneVerdict{}, false
		}
		return laneVerdict{toolchain: ToolchainCMake, detail: "cmake --build builds a tree no earlier run: step of " +
			"this job configures, so whether CMAKE_COMPILE_WARNING_AS_ERROR is set cannot be read"}, true
	}
	if !cmakeConfigures(args) {
		return laneVerdict{}, false
	}
	m.configured[key] = true
	fatal, detail := cmakeWarningsAsErrors(args)
	return laneVerdict{toolchain: ToolchainCMake, fatal: fatal, detail: detail}, true
}

// cmakeConfigures reports whether a cmake command configures a build tree: it names a source or
// build directory, a preset or a workflow preset, and selects no other mode.
func cmakeConfigures(args []string) bool {
	if len(args) == 0 {
		return false
	}
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		if cmakeNoConfigure[args[i]] || strings.HasPrefix(args[i], "--help") || strings.HasPrefix(args[i], "--list-presets") {
			return false
		}
	}
	return true
}

// cmakeWarningsAsErrors decides one configure run: --compile-no-warning-as-error defeats every
// setting, CMAKE_COMPILE_WARNING_AS_ERROR decides when defined, and otherwise -Werror (or /WX)
// in every compile-flag entry it defines.
func cmakeWarningsAsErrors(args []string) (bool, string) {
	if slices.Contains(args, "--compile-no-warning-as-error") {
		return false, "--compile-no-warning-as-error ignores CMAKE_COMPILE_WARNING_AS_ERROR"
	}
	definitions := cmakeDefinitions(args)
	if value, defined := definitions["CMAKE_COMPILE_WARNING_AS_ERROR"]; defined {
		if cmakeTrue(value) {
			return true, "CMAKE_COMPILE_WARNING_AS_ERROR=" + value
		}
		return false, "CMAKE_COMPILE_WARNING_AS_ERROR=" + value + " is not a CMake true constant"
	}
	if flags := cmakeFlagsWithWerror(definitions); flags != "" {
		return true, flags + " carry -Werror"
	}
	detail := "the configure command defines no CMAKE_COMPILE_WARNING_AS_ERROR"
	if slices.ContainsFunc(args, isCMakePresetOption) {
		detail += "; a preset's cacheVariables are not read"
	}
	return false, detail
}

// isCMakePresetOption reports whether arg selects a configure or workflow preset.
func isCMakePresetOption(arg string) bool {
	return arg == "--preset" || arg == "--workflow" || strings.HasPrefix(arg, "--preset=")
}

// cmakeDefinitions returns the cache entries a configure command defines with -D<var>[:<type>]=
// <value> or -D <var>[:<type>]=<value>, the last definition of a name winning.
func cmakeDefinitions(args []string) map[string]string {
	definitions := map[string]string{}
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		definition, isDefinition := strings.CutPrefix(args[i], "-D")
		if !isDefinition {
			continue
		}
		if definition == "" && i+1 < len(args) {
			i++
			definition = args[i]
		}
		name, value, found := strings.Cut(definition, "=")
		if !found {
			continue
		}
		name, _, _ = strings.Cut(name, ":")
		definitions[name] = value
	}
	return definitions
}

// cmakeTrue reports whether value is a CMake true constant: 1, ON, YES, TRUE, Y in any case, or
// a non-zero number.
func cmakeTrue(value string) bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "1", "ON", "YES", "TRUE", "Y":
		return true
	}
	number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return err == nil && number != 0
}

// cmakeFlagsWithWerror names the compile-flag entries definitions holds when at least one is
// defined and every defined one ends with -Werror (or /WX) in force, else "".
func cmakeFlagsWithWerror(definitions map[string]string) string {
	var named []string
	for i := 0; i < len(cmakeFlagVariables); i++ {
		value, defined := definitions[cmakeFlagVariables[i]]
		if !defined {
			continue
		}
		if !lastSwitch(strings.Fields(value), []string{"-Werror", "/WX", "-WX"}, []string{"-Wno-error", "/WX-", "-WX-"}) {
			return ""
		}
		named = append(named, cmakeFlagVariables[i])
	}
	return strings.Join(named, " and ")
}

// judgeMeson decides a meson command: meson setup is a lane judged by its werror option; meson
// compile, test and install are lanes only in a job where no earlier run: step set the build
// directory up.
func (m *buildMeasure) judgeMeson(site *laneSite, args []string) (laneVerdict, bool) {
	key := site.jobKey(ToolchainMeson)
	switch subcommand := firstOperand(args); subcommand {
	case "setup":
		m.configured[key] = true
		if mesonWerror(args) {
			return laneVerdict{toolchain: ToolchainMeson, fatal: true, detail: "meson setup sets werror"}, true
		}
		return laneVerdict{toolchain: ToolchainMeson, detail: "meson setup runs without --werror or -Dwerror=true"}, true
	case "compile", "test", "install":
		if m.configured[key] {
			return laneVerdict{}, false
		}
		return laneVerdict{toolchain: ToolchainMeson, detail: "meson " + subcommand + " builds a directory no earlier " +
			"run: step of this job sets up, so whether werror is set cannot be read"}, true
	}
	return laneVerdict{}, false
}

// firstOperand returns the first argument that is not an option, or "".
func firstOperand(args []string) string {
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		if !strings.HasPrefix(args[i], "-") {
			return args[i]
		}
	}
	return ""
}

// mesonWerror reports whether the last werror setting of a meson setup command turns it on:
// --werror, -Dwerror=true or -D werror=true, against a later -Dwerror=false.
func mesonWerror(args []string) bool {
	werror := false
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		option, isOption := strings.CutPrefix(args[i], "-D")
		switch {
		case args[i] == "--werror":
			werror = true
		case !isOption:
			continue
		case option == "" && i+1 < len(args):
			i++
			option = args[i]
		}
		if value, isWerror := strings.CutPrefix(option, "werror="); isWerror {
			werror = value == "true"
		}
	}
	return werror
}

// cargoBuildSubcommands are the cargo subcommands that compile the repository's crates, with
// their aliases. install builds a published crate, and doc runs rustdoc; neither is a lane.
var cargoBuildSubcommands = map[string]bool{
	"build": true, "b": true, "check": true, "c": true, "test": true, "t": true, "run": true, "r": true,
	"bench": true, "clippy": true, "rustc": true, "nextest": true,
}

// cargoValueOptions are the cargo options before a subcommand whose value is the next argument.
var cargoValueOptions = map[string]bool{"--color": true, "--config": true, "-Z": true, "-C": true}

// cargoFlagSources are the environment variables cargo reads extra compiler flags from, in its
// order: the first one set is the one used.
var cargoFlagSources = [...]string{"CARGO_ENCODED_RUSTFLAGS", "RUSTFLAGS"}

// judgeCargo decides a cargo command that compiles the repository: fatal with -D warnings after
// clippy's or rustc's "--", or in the flag variable cargo uses.
func judgeCargo(args []string, lookup func(string) (string, bool, bool)) (laneVerdict, bool) {
	subcommand, rest := cargoSubcommand(args)
	if !cargoBuildSubcommands[subcommand] {
		return laneVerdict{}, false
	}
	if separator := slices.Index(rest, "--"); separator >= 0 && (subcommand == "clippy" || subcommand == "rustc") &&
		denyWarnings(rest[separator+1:]) {
		return laneVerdict{toolchain: ToolchainCargo, fatal: true, detail: "cargo " + subcommand + " -- -D warnings"}, true
	}
	return cargoRustflags(subcommand, lookup), true
}

// cargoSubcommand returns a cargo command's subcommand and the arguments after it, past a
// +toolchain override and the options before the subcommand.
func cargoSubcommand(args []string) (string, []string) {
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		switch arg := args[i]; {
		case cargoValueOptions[arg]:
			i++
		case strings.HasPrefix(arg, "+"), strings.HasPrefix(arg, "-"):
			continue
		default:
			return arg, args[i+1:]
		}
	}
	return "", nil
}

// cargoRustflags decides a cargo lane by the flag variable cargo uses.
func cargoRustflags(subcommand string, lookup func(string) (string, bool, bool)) laneVerdict {
	verdict := laneVerdict{toolchain: ToolchainCargo}
	for i := 0; i < len(cargoFlagSources); i++ {
		name := cargoFlagSources[i]
		value, set, known := lookup(name)
		switch {
		case !known:
			verdict.detail = "an env: that is one expression decides " + name + ", which the file cannot show"
			return verdict
		case !set:
			continue
		}
		flags := strings.Fields(value)
		if name == "CARGO_ENCODED_RUSTFLAGS" {
			flags = strings.Split(value, "\x1f")
		}
		verdict.fatal = denyWarnings(flags)
		verdict.detail = name + " is " + strconv.Quote(value)
		return verdict
	}
	verdict.detail = "cargo " + subcommand + " runs with neither RUSTFLAGS nor CARGO_ENCODED_RUSTFLAGS set"
	return verdict
}

// judgeRustc decides a rustc command that compiles a source file.
func judgeRustc(args []string) (laneVerdict, bool) {
	if !slices.ContainsFunc(args, func(arg string) bool { return strings.HasSuffix(arg, ".rs") }) {
		return laneVerdict{}, false
	}
	if denyWarnings(args) {
		return laneVerdict{toolchain: ToolchainRustc, fatal: true, detail: "-D warnings is on the command"}, true
	}
	return laneVerdict{toolchain: ToolchainRustc, detail: "the command carries no -D warnings"}, true
}

// lintLevels maps each rustc lint-level flag to whether it makes the lints it names errors.
var lintLevels = map[string]bool{
	"-D": true, "--deny": true, "-F": true, "--forbid": true,
	"-W": false, "--warn": false, "-A": false, "--allow": false,
}

// denyWarnings reports whether rustc flags leave the warnings lint group denied: the last level
// they give it is deny or forbid, or any is forbid, which no later level overrides. A level is
// written "-D warnings", "-Dwarnings" or "--deny=warnings".
func denyWarnings(flags []string) bool {
	deny, forbid := false, false
	for i := 0; i < len(flags) && i < maxRunScriptFields; i++ {
		level, next := warningsLevel(flags, i)
		if level == "" {
			continue
		}
		i = next
		deny = lintLevels[level]
		forbid = forbid || level == "-F" || level == "--forbid"
	}
	return deny || forbid
}

// warningsLevel returns the lint-level flag flags[i] gives the warnings group, and the index of
// the last flag it spans; "" when it gives warnings no level.
func warningsLevel(flags []string, i int) (string, int) {
	flag := flags[i]
	if _, known := lintLevels[flag]; known {
		if i+1 < len(flags) && flags[i+1] == "warnings" {
			return flag, i + 1
		}
		return "", i
	}
	if level, ok := strings.CutSuffix(flag, "=warnings"); ok && strings.HasPrefix(level, "--") {
		return level, i
	}
	if level, ok := strings.CutSuffix(flag, "warnings"); ok && len(level) == 2 {
		return level, i
	}
	return "", i
}

// goCommand records a go command: go vet and go test -vet=all are the vet evidence every Go lane
// is judged by, and go build, test and install of the repository are Go lanes.
func (m *buildMeasure) goCommand(site *laneSite, args []string) {
	if len(args) >= 2 && args[0] == "-C" {
		args = args[2:]
	}
	if len(args) == 0 {
		return
	}
	if goRunsVet(args) {
		m.vetAt(site)
	}
	if goBuildsRepository(args) {
		m.goLane(site)
	}
}

// goRunsVet reports whether a go command's args, from its subcommand, run every go vet check:
// go vet, or go test -vet=all.
func goRunsVet(args []string) bool {
	return args[0] == "vet" || args[0] == "test" && (slices.Contains(args, "-vet=all") || slices.Contains(args, "--vet=all"))
}

// goBuildsRepository reports whether a go command's args, from its subcommand, build or test the
// repository's packages: go build and test, and go install of anything but a module@version,
// which installs a tool. go run compiles a program only to execute it, such as the API gate
// tools/apicompat/gate/main.go adoption writes, and is no lane.
func goBuildsRepository(args []string) bool {
	switch args[0] {
	case "build", "test":
		return true
	case "install":
		return !strings.Contains(firstOperand(args[1:]), "@")
	}
	return false
}

// vetAt records the first binding step that runs go vet.
func (m *buildMeasure) vetAt(site *laneSite) {
	if !site.advisory && m.vet == "" {
		m.vet = site.lane.Where()
	}
}

// goLane records a Go lane, judged once every workflow is read (result).
func (m *buildMeasure) goLane(site *laneSite) {
	m.goLanes = append(m.goLanes, len(m.lanes))
	m.record(site, laneVerdict{toolchain: ToolchainGo})
}
