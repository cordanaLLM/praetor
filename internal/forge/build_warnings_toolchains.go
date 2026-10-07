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
//     Without it, -Werror (or /WX) in the flags of every language the repository's sources need:
//     CMAKE_C_FLAGS and CMAKE_CXX_FLAGS, initialized from CFLAGS and CXXFLAGS when the command
//     defines neither (https://cmake.org/cmake/help/latest/variable/CMAKE_LANG_FLAGS.html), and
//     both when the repository holds no C or C++ source, since "By default C and CXX are
//     enabled if no language options are given" (https://cmake.org/cmake/help/latest/command/project.html).
//   - Meson: the werror built-in option, "Treat warnings as errors", set by meson setup --werror
//     or -Dwerror=true (https://mesonbuild.com/Builtin-options.html), the value compared without
//     case as meson 1.12.1's UserBooleanOption.validate_value does (value.lower() == 'true').
//   - Cargo and rustc: -D warnings (rustc lint levels) from the first of cargo's four flag sources
//     that is set, CARGO_ENCODED_RUSTFLAGS, RUSTFLAGS, CARGO_TARGET_<triple>_RUSTFLAGS and
//     CARGO_BUILD_RUSTFLAGS, after the levels the [lints] tables give, and before what follows
//     cargo clippy's or cargo rustc's "--"; or CARGO_BUILD_WARNINGS=deny, Cargo 1.97 and later
//     (https://doc.rust-lang.org/cargo/reference/config.html,
//     https://doc.rust-lang.org/clippy/continuous_integration/index.html). cross takes cargo's
//     command line and passes these variables into its container
//     (https://github.com/cross-rs/cross/blob/main/docs/environment_variables.md), and cargo
//     llvm-cov keeps them and appends its own (taiki-e/cargo-llvm-cov src/main.rs set_env).
//   - Go: go vet exits non-zero when it reports a problem; go test -vet=all runs every vet check
//     (https://pkg.go.dev/cmd/vet, https://pkg.go.dev/cmd/go). The C and C++ code of cgo files
//     compiles with CGO_CPPFLAGS then CGO_CFLAGS or CGO_CXXFLAGS ('go help environment',
//     cmd/go/internal/work/exec.go of go 1.27: str.StringList(cgoCPPFLAGS, cgoCFLAGS)), and
//     CGO_ENABLED=0 turns cgo off ('go help buildconstraint').

// cmakeNoConfigure are the cmake options that select a mode other than configuring a build tree.
var cmakeNoConfigure = map[string]bool{
	"--build": true, "--install": true, "--open": true, "-E": true, "-P": true, "-N": true,
	"--version": true, "-version": true, "--help": true, "-help": true, "-h": true,
	"--find-package": true, "--system-information": true,
}

// judgeCMake decides a cmake command: a configure run is a lane judged by its definitions, its
// flags deferred to the repository's sources (cmakeFlags); cmake --build is a lane only in a job
// where no earlier run: step configured, since what configured its tree cannot be read.
func (m *buildMeasure) judgeCMake(site *laneSite, args []string, lookup lookupFunc) (laneVerdict, bool) {
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
	definitions := cmakeDefinitions(args)
	fatal, detail := cmakeWarningAsError(args, definitions)
	verdict := laneVerdict{toolchain: ToolchainCMake, fatal: fatal, detail: detail}
	if flags, set := readCMakeFlags(definitions, lookup); !fatal && set {
		verdict.cmake = &flags
	}
	return verdict, true
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

// cmakeWarningAsError decides one configure run by CMAKE_COMPILE_WARNING_AS_ERROR: fatal when it
// is a CMake true constant and --compile-no-warning-as-error does not ignore it, else what the
// run lacks.
func cmakeWarningAsError(args []string, definitions map[string]string) (bool, string) {
	value, defined := definitions["CMAKE_COMPILE_WARNING_AS_ERROR"]
	ignored := slices.Contains(args, "--compile-no-warning-as-error")
	switch {
	case defined && ignored:
		return false, "--compile-no-warning-as-error ignores CMAKE_COMPILE_WARNING_AS_ERROR"
	case defined && cmakeTrue(value):
		return true, "CMAKE_COMPILE_WARNING_AS_ERROR=" + value
	case defined:
		return false, "CMAKE_COMPILE_WARNING_AS_ERROR=" + value + " is not a CMake true constant"
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

// cmakeLanguage is a language a CMake configure run compiles: its name, the flags entry that may
// carry -Werror for it, and the environment variable that entry is initialized from.
type cmakeLanguage struct {
	name, variable, env string
}

// cmakeLanguages are the languages the flags fallback reads, C and C++.
var cmakeLanguages = [...]cmakeLanguage{
	{name: "C", variable: "CMAKE_C_FLAGS", env: "CFLAGS"},
	{name: "C++", variable: "CMAKE_CXX_FLAGS", env: "CXXFLAGS"},
}

// cmakeWerrorOn and cmakeWerrorOff are the switches in a flags entry that turn warnings as
// errors on and off, for gcc and clang and for cl.
var (
	cmakeWerrorOn  = []string{"-Werror", "/WX", "-WX"}
	cmakeWerrorOff = []string{"-Wno-error", "/WX-", "-WX-"}
)

// cmakeFlags is, per cmakeLanguages entry, what sets its flags ("" for nothing) and whether
// -Werror (or /WX) is in force at their end.
type cmakeFlags struct {
	source [len(cmakeLanguages)]string
	werror [len(cmakeLanguages)]bool
}

// readCMakeFlags reads the flags of each language a configure run defines, or else takes from
// the environment, and reports whether any language's flags are set.
func readCMakeFlags(definitions map[string]string, lookup lookupFunc) (cmakeFlags, bool) {
	var flags cmakeFlags
	set := false
	for i := 0; i < len(cmakeLanguages); i++ {
		language := cmakeLanguages[i]
		value, source := definitions[language.variable], language.variable
		if _, defined := definitions[language.variable]; !defined {
			env, inEnv, known := lookup(language.env)
			if !inEnv || !known {
				continue
			}
			value, source = env, language.env
		}
		set = true
		flags.source[i] = source
		flags.werror[i] = lastSwitch(strings.Fields(value), cmakeWerrorOn, cmakeWerrorOff)
	}
	return flags, set
}

// judge decides the flags of a configure run against the languages the repository's sources
// need: each language with a translation unit, or C and C++, which project() enables by default,
// when the repository holds none of either. It returns the detail of a fatal run, or what a
// non-fatal one lacks.
func (f cmakeFlags) judge(sources nativeSources) (bool, string) {
	needed := [len(cmakeLanguages)]bool{sources.c, sources.cxx}
	why := ", and the repository holds %s sources"
	if !sources.c && !sources.cxx {
		needed = [len(cmakeLanguages)]bool{true, true}
		why = ", and CMake enables %s when project() names no language (the repository holds no C or C++ source)"
	}
	var carrying, names, lacking []string
	for i := 0; i < len(cmakeLanguages); i++ {
		switch {
		case !needed[i]:
			continue
		case f.werror[i]:
			carrying, names = append(carrying, f.source[i]), append(names, cmakeLanguages[i].name)
		default:
			lacking = append(lacking, cmakeLanguages[i].variable+" carries no -Werror"+
				strings.Replace(why, "%s", cmakeLanguages[i].name, 1))
		}
	}
	if len(lacking) > 0 {
		return false, strings.Join(lacking, "; ")
	}
	verb := " carries"
	if len(carrying) > 1 {
		verb = " carry"
	}
	return true, strings.Join(carrying, " and ") + verb + " -Werror for " + strings.Join(names, " and ")
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
// --werror, -Dwerror=true or -D werror=true in any case, against a later -Dwerror=false.
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
			werror = strings.EqualFold(value, "true")
		}
	}
	return werror
}

// cargoBuildSubcommands are the cargo subcommands that compile the repository's crates, with
// their aliases, and cargo llvm-cov, which builds and tests them for coverage. install builds a
// published crate, and doc runs rustdoc; neither is a lane.
var cargoBuildSubcommands = map[string]bool{
	"build": true, "b": true, "check": true, "c": true, "test": true, "t": true, "run": true, "r": true,
	"bench": true, "clippy": true, "rustc": true, "nextest": true, "llvm-cov": true,
}

// llvmCovNoBuild are the cargo llvm-cov subcommands that build nothing: report, show-env and
// clean (https://github.com/taiki-e/cargo-llvm-cov#usage).
var llvmCovNoBuild = map[string]bool{"report": true, "show-env": true, "clean": true}

// cargoValueOptions are the cargo options before a subcommand whose value is the next argument.
var cargoValueOptions = map[string]bool{"--color": true, "--config": true, "-Z": true, "-C": true}

// judgeCargo decides a cargo or cross command that compiles the repository: fatal with
// CARGO_BUILD_WARNINGS=deny, or when the flags rustc receives deny warnings for every target
// (cargoTargets): the [lints] levels (cargoLints), the flag source cargo uses (cargoFlagSource),
// then for clippy and rustc what follows "--".
func (m *buildMeasure) judgeCargo(site *laneSite, cmd shellCommand, args []string, lookup lookupFunc) (laneVerdict, bool) {
	subcommand, rest := cargoSubcommand(args)
	if !cargoBuilds(subcommand, rest) {
		return laneVerdict{}, false
	}
	if value, set, known := lookup("CARGO_BUILD_WARNINGS"); set && known && strings.TrimSpace(value) == "deny" {
		return laneVerdict{toolchain: ToolchainCargo, fatal: true, detail: "CARGO_BUILD_WARNINGS is deny"}, true
	}
	options, trailing := cargoTrailing(subcommand, rest)
	lints, lintsNote := m.cargoLints(site, options)
	targets := cargoTargets(options, lookup)
	verdict := laneVerdict{toolchain: ToolchainCargo}
	for i := 0; i < len(targets) && i < maxRunScriptFields; i++ {
		source := cargoFlagSource(site.env, cmd, targets[i], lookup)
		verdict.fatal = source.decided && denyWarnings(slices.Concat(lints, source.flags, trailing))
		verdict.detail = cargoDetail(subcommand, source.detail, trailing, lintsNote)
		if !verdict.fatal {
			break
		}
	}
	return verdict, true
}

// cargoBuilds reports whether a cargo subcommand with the arguments after it compiles the
// repository's crates.
func cargoBuilds(subcommand string, rest []string) bool {
	return cargoBuildSubcommands[subcommand] && (subcommand != "llvm-cov" || !llvmCovNoBuild[firstOperand(rest)])
}

// cargoTrailing splits the arguments after a cargo subcommand at "--" into cargo's options and
// what follows, which only clippy and rustc pass on to the compiler.
func cargoTrailing(subcommand string, rest []string) (options, trailing []string) {
	separator := slices.Index(rest, "--")
	if separator < 0 {
		return rest, nil
	}
	if subcommand == "clippy" || subcommand == "rustc" {
		trailing = rest[separator+1:]
	}
	return rest[:separator], trailing
}

// cargoDetail is the detail of a cargo lane: what follows clippy's or rustc's "--", the flag
// source, and the note on the [lints] tables.
func cargoDetail(subcommand, source string, trailing []string, lintsNote string) string {
	detail := source
	if len(trailing) > 0 {
		detail = "cargo " + subcommand + " -- " + strings.Join(trailing, " ") + ", after " + source
	}
	if lintsNote != "" {
		detail += "; " + lintsNote
	}
	return detail
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

// cargoLints returns the rustc flags the [lints] tables of the root Cargo.toml's workspace give
// the warnings group (BuildWarningsInputs.CargoLints), and a note for the lane's detail. They are
// not read for a command run outside the checkout's root or with --manifest-path: the manifest it
// builds may be another one.
func (m *buildMeasure) cargoLints(site *laneSite, options []string) ([]string, string) {
	lints := m.inputs.CargoLints
	switch {
	case lints.Level == "":
		return nil, lints.Detail
	case site.moved || slices.ContainsFunc(options, func(arg string) bool { return strings.HasPrefix(arg, "--manifest-path") }):
		return nil, "the root Cargo.toml's [lints] are not read for a command run in another directory or with --manifest-path"
	}
	return []string{"--" + lints.Level + "=warnings"}, lints.Detail
}

// cargoTargets returns the target triples a cargo command builds for: each --target it names,
// else CARGO_BUILD_TARGET, else "" for the runner's host.
func cargoTargets(options []string, lookup lookupFunc) []string {
	targets := flagValues(options, "--target")
	if len(targets) > 0 {
		return targets
	}
	if target, set, known := lookup("CARGO_BUILD_TARGET"); set && known && strings.TrimSpace(target) != "" {
		return []string{strings.TrimSpace(target)}
	}
	return []string{""}
}

// rustflagSource is the extra flags cargo passes rustc for one target: the flags, the detail
// naming where they come from, and whether the workflow decides them.
type rustflagSource struct {
	flags   []string
	detail  string
	decided bool
}

// cargoFlagVariables are the environment forms of cargo's flag sources in its order, the first one
// set being used; "" stands for CARGO_TARGET_<triple>_RUSTFLAGS.
var cargoFlagVariables = [...]string{"CARGO_ENCODED_RUSTFLAGS", "RUSTFLAGS", "", "CARGO_BUILD_RUSTFLAGS"}

// cargoFlagSource returns the flag source cargo uses for target ("" for the runner's host). A
// CARGO_TARGET_<triple>_RUSTFLAGS for a host build applies only when the runner's triple is that
// one, which the workflow does not show, so it leaves the source undecided.
func cargoFlagSource(env stepEnvironment, cmd shellCommand, target string, lookup lookupFunc) rustflagSource {
	for i := 0; i < len(cargoFlagVariables); i++ {
		name := cargoFlagVariables[i]
		if name == "" && target == "" {
			if names, _ := env.names(cmd, "CARGO_TARGET_", "_RUSTFLAGS"); len(names) > 0 {
				return rustflagSource{detail: strings.Join(names, ", ") + " applies only when the runner's host is that " +
					"triple, which the workflow does not show; name it with --target or CARGO_BUILD_TARGET"}
			}
			continue
		}
		if name == "" {
			name = "CARGO_TARGET_" + strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(target)) + "_RUSTFLAGS"
		}
		value, set, known := lookup(name)
		switch {
		case !known:
			return rustflagSource{detail: "an env: that is one expression decides " + name + ", which the file cannot show"}
		case !set:
			continue
		}
		flags := strings.Fields(value)
		if name == "CARGO_ENCODED_RUSTFLAGS" {
			flags = strings.Split(value, "\x1f")
		}
		return rustflagSource{flags: flags, detail: name + " is " + strconv.Quote(value), decided: true}
	}
	return rustflagSource{decided: true, detail: "no RUSTFLAGS, CARGO_ENCODED_RUSTFLAGS, CARGO_TARGET_<triple>_RUSTFLAGS " +
		"or CARGO_BUILD_RUSTFLAGS is set"}
}

// judgeRustc decides a rustc command that compiles a source file.
func judgeRustc(args []string) (laneVerdict, bool) {
	if !slices.ContainsFunc(args, func(arg string) bool { return strings.HasSuffix(arg, ".rs") }) {
		return laneVerdict{}, false
	}
	if denyWarnings(args) {
		return laneVerdict{toolchain: ToolchainRustc, fatal: true, detail: "-D warnings is on the command"}, true
	}
	return laneVerdict{toolchain: ToolchainRustc, detail: "the command carries no -D warnings in force"}, true
}

// lintLevels maps each rustc lint-level flag to whether it makes the lints it names errors.
var lintLevels = map[string]bool{
	"-D": true, "--deny": true, "-F": true, "--forbid": true,
	"-W": false, "--warn": false, "-A": false, "--allow": false,
}

// denyWarnings reports whether rustc flags leave the warnings lint group denied: no --cap-lints
// lowers it (capsLintsBelowDeny), and the last level they give it is deny or forbid, or any is
// forbid, which no later level overrides. A level is written "-D warnings", "-Dwarnings" or
// "--deny=warnings".
func denyWarnings(flags []string) bool {
	if capsLintsBelowDeny(flags) {
		return false
	}
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

// capsLintsBelowDeny reports whether the first --cap-lints of flags caps every lint at allow or
// warn, which turns a denied or forbidden warning back into a warning. rustc reads the first
// --cap-lints: measured with rustc 1.98.1 on an unused variable, -D warnings --cap-lints warn
// --cap-lints deny exits 0, -D warnings --cap-lints deny --cap-lints warn exits 1, and -F
// warnings --cap-lints warn exits 0.
func capsLintsBelowDeny(flags []string) bool {
	for i := 0; i < len(flags) && i < maxRunScriptFields; i++ {
		level, capped := strings.CutPrefix(flags[i], "--cap-lints=")
		if flags[i] == "--cap-lints" && i+1 < len(flags) {
			level, capped = flags[i+1], true
		}
		if capped {
			return level == "allow" || level == "warn"
		}
	}
	return false
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
// is judged by, and go build, test and install of the repository are Go lanes, judged once the
// repository's cgo files are read (judgeGoLane).
func (m *buildMeasure) goCommand(site *laneSite, args []string, lookup lookupFunc) {
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
		cgo := readGoCgo(lookup)
		m.record(site, laneVerdict{toolchain: ToolchainGo, detail: "go " + args[0], cgo: &cgo})
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

// goCgo is what a Go lane's environment gives the C and C++ code cgo compiles: whether
// CGO_ENABLED=0 turns cgo off, whether -Werror is in force at the end of CGO_CPPFLAGS then
// CGO_CFLAGS (c) and of CGO_CPPFLAGS then CGO_CXXFLAGS (cxx), and the variable an env:
// expression decides, if any.
type goCgo struct {
	disabled  bool
	c, cxx    bool
	undecided string
}

// readGoCgo reads a Go lane's cgo environment.
func readGoCgo(lookup lookupFunc) goCgo {
	var cgo goCgo
	if value, set, known := lookup("CGO_ENABLED"); set && known && strings.TrimSpace(value) == "0" {
		cgo.disabled = true
		return cgo
	}
	flags := map[string][]string{}
	for _, name := range [...]string{"CGO_CPPFLAGS", "CGO_CFLAGS", "CGO_CXXFLAGS"} {
		value, _, known := lookup(name)
		if !known && cgo.undecided == "" {
			cgo.undecided = name
		}
		flags[name] = strings.Fields(value)
	}
	cgo.c = lastSwitch(slices.Concat(flags["CGO_CPPFLAGS"], flags["CGO_CFLAGS"]), []string{"-Werror"}, []string{"-Wno-error"})
	cgo.cxx = lastSwitch(slices.Concat(flags["CGO_CPPFLAGS"], flags["CGO_CXXFLAGS"]), []string{"-Werror"}, []string{"-Wno-error"})
	return cgo
}

// judgeGoLane decides a Go lane once every workflow and the repository's sources are read: fatal
// when a binding step runs go vet (vet names it) and, in a module with cgo files cgo compiles,
// -Werror is in force for their C code, and for their C++ code when a cgo package holds C++ files.
func judgeGoLane(vet string, cgo goCgo, sources nativeSources) (bool, string) {
	var problems []string
	if vet == "" {
		problems = append(problems, "no binding step of any workflow runs go vet or go test -vet=all")
	}
	if missing := cgo.missing(sources); missing != "" {
		problems = append(problems, missing)
	}
	if len(problems) > 0 {
		return false, strings.Join(problems, "; ")
	}
	if sources.cgo && !cgo.disabled {
		return true, "go vet runs at " + vet + ", and -Werror is in force for the C code of the module's cgo files"
	}
	return true, "go vet runs at " + vet
}

// missing says what the lane lacks for the C and C++ code of the repository's cgo files, or "".
func (cgo goCgo) missing(sources nativeSources) string {
	switch {
	case !sources.cgo || cgo.disabled:
		return ""
	case cgo.undecided != "":
		return "an env: that is one expression decides " + cgo.undecided + ", so whether the C code of the module's " +
			"cgo files compiles with -Werror cannot be read"
	case !cgo.c:
		return `the module has cgo files (import "C"), and neither CGO_CPPFLAGS nor CGO_CFLAGS carries -Werror for the C code they compile`
	case sources.cgoCXX && !cgo.cxx:
		return "a cgo package holds C++ files, and neither CGO_CPPFLAGS nor CGO_CXXFLAGS carries -Werror for them"
	}
	return ""
}
