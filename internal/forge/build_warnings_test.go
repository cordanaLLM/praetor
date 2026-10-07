package forge

import (
	"context"
	"strings"
	"testing"
)

// buildJob is a workflow whose one job, build, runs steps (already indented as a step list).
func buildJob(steps string) string {
	return "on: pull_request\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n" + steps
}

// measureLanes measures a repository holding workflows (file name to content).
func measureLanes(t *testing.T, workflows map[string]string) []BuildLane {
	t.Helper()
	files := make(map[string]string, len(workflows))
	for name, content := range workflows {
		files[".github/workflows/"+name] = content
	}
	return measureRepo(t, files, BuildWarningsInputs{})
}

// measureRepo measures a repository holding files (repository path to content) with inputs.
func measureRepo(t *testing.T, files map[string]string, inputs BuildWarningsInputs) []BuildLane {
	t.Helper()
	measured, err := MeasureBuildWarnings(context.Background(), sbomRepo(t, files), inputs)
	if err != nil {
		t.Fatalf("MeasureBuildWarnings: %v", err)
	}
	return measured.Lanes
}

// oneRepoLane measures a repository holding files and requires exactly one lane.
func oneRepoLane(t *testing.T, files map[string]string, inputs BuildWarningsInputs) BuildLane {
	t.Helper()
	lanes := measureRepo(t, files, inputs)
	if len(lanes) != 1 {
		t.Fatalf("lanes = %+v; want exactly one", lanes)
	}
	return lanes[0]
}

// oneLane measures one workflow, ci.yml, and requires exactly one lane.
func oneLane(t *testing.T, workflow string) BuildLane {
	t.Helper()
	lanes := measureLanes(t, map[string]string{"ci.yml": workflow})
	if len(lanes) != 1 {
		t.Fatalf("lanes = %+v; want exactly one", lanes)
	}
	return lanes[0]
}

// Positive: each toolchain's documented warnings-as-errors form makes its lane fatal, read from
// the command, from a quoted prefix assignment, from an export earlier in the script, from a
// variable the command expands, and from the step's, job's or workflow's env:.
func TestMeasureBuildWarnings_Positive_DocumentedFormsAreFatal(t *testing.T) {
	cases := map[string]struct {
		workflow, toolchain, detail string
	}{
		"gcc -Werror":               {buildJob("      - run: gcc -Wall -Werror -c src/main.c\n"), ToolchainGCCClang, "-Werror"},
		"versioned clang++ -Werror": {buildJob("      - run: clang++-18 -Werror src/a.cpp -o a\n"), ToolchainGCCClang, "-Werror"},
		"MinGW gcc through ccache":  {buildJob("      - run: ccache x86_64-w64-mingw32-gcc -Werror -c a.c\n"), ToolchainGCCClang, "-Werror"},
		"cl /WX":                    {buildJob("      - run: cl.exe /W4 /WX /c src\\main.c\n"), ToolchainMSVC, "/WX"},
		// clang-cl 23.1.1: -Werror /c exits 1 on -Wunused-variable, as /WX does.
		"clang-cl -Werror": {buildJob("      - run: clang-cl -Wunused-variable -Werror /c a.c\n"), ToolchainMSVC, "-Werror"},
		// meson 1.12.1 options.py UserBooleanOption.validate_value: value.lower() == 'true'.
		"Meson -Dwerror=True": {buildJob("      - run: meson setup build -Dwerror=True\n"), ToolchainMeson, "werror"},
		// Debian's gcc-mingw-w64-x86-64-posix and g++-mingw-w64-x86-64-win32 file lists.
		"MinGW g++ -posix":              {buildJob("      - run: x86_64-w64-mingw32-g++-posix -Werror -c a.cpp\n"), ToolchainGCCClang, "-Werror"},
		"MinGW gcc-14 -win32":           {buildJob("      - run: x86_64-w64-mingw32-gcc-14-win32 -Werror -c a.c\n"), ToolchainGCCClang, "-Werror"},
		"a Windows path":                {buildJob("      - run: C:\\msys64\\mingw64\\bin\\gcc.exe -Werror -c a.c\n"), ToolchainGCCClang, "-Werror"},
		"a compiler from env:":          {buildJob("      - run: $CC -Werror -c a.c\n        env:\n          CC: ccache clang-18\n"), ToolchainGCCClang, "-Werror"},
		"CMake in a subshell":           {buildJob("      - run: (cd build && cmake .. -DCMAKE_COMPILE_WARNING_AS_ERROR=ON)\n"), ToolchainCMake, "=ON"},
		"CMake ignoring the var":        {buildJob("      - run: cmake -B b -DCMAKE_COMPILE_WARNING_AS_ERROR=ON --compile-no-warning-as-error -DCMAKE_C_FLAGS=-Werror -DCMAKE_CXX_FLAGS=-Werror\n"), ToolchainCMake, "carry -Werror for C and C++"},
		"CMake flags from env:":         {buildJob("      - run: cmake -B build\n        env:\n          CFLAGS: -Werror\n          CXXFLAGS: -Wall -Werror\n"), ToolchainCMake, "CFLAGS and CXXFLAGS carry -Werror for C and C++"},
		"cl with /WX in CL":             {buildJob("      - run: cl /c main.c\n        env:\n          CL: /W4 /WX\n"), ToolchainMSVC, "/WX"},
		"CMake warning as error":        {buildJob("      - run: cmake -S . -B build -DCMAKE_COMPILE_WARNING_AS_ERROR=ON && cmake --build build\n"), ToolchainCMake, "CMAKE_COMPILE_WARNING_AS_ERROR=ON"},
		"CMake typed definition":        {buildJob("      - run: cmake -B build -D CMAKE_COMPILE_WARNING_AS_ERROR:BOOL=true\n"), ToolchainCMake, "=true"},
		"CMake -Werror in both flags":   {buildJob("      - run: cmake -B build -DCMAKE_C_FLAGS=\"-Wall -Werror\" -DCMAKE_CXX_FLAGS=-Werror\n"), ToolchainCMake, "CMAKE_C_FLAGS and CMAKE_CXX_FLAGS carry -Werror for C and C++"},
		"CMake through an env variable": {"on: push\nenv:\n  CMAKE_ARGS: -DCMAKE_COMPILE_WARNING_AS_ERROR=1\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: cmake -B build $CMAKE_ARGS\n", ToolchainCMake, "=1"},
		"Meson --werror":                {buildJob("      - run: meson setup build --werror\n      - run: meson compile -C build\n"), ToolchainMeson, "werror"},
		"Meson -D werror=true":          {buildJob("      - run: meson setup -D werror=true build\n"), ToolchainMeson, "werror"},
		"cargo with a quoted prefix":    {buildJob("      - run: RUSTFLAGS=\"-D warnings\" cargo build --locked\n"), ToolchainCargo, `RUSTFLAGS is "-D warnings"`},
		"cargo with an export":          {buildJob("      - run: |\n          export RUSTFLAGS=-Dwarnings\n          cargo +stable test\n"), ToolchainCargo, "-Dwarnings"},
		"cargo with workflow env":       {"on: push\nenv:\n  RUSTFLAGS: --deny=warnings\njobs:\n  t:\n    runs-on: ubuntu-latest\n    steps:\n      - run: cargo nextest run\n", ToolchainCargo, "--deny=warnings"},
		"cargo clippy -- -D warnings":   {buildJob("      - run: cargo clippy --all-targets -- -D warnings\n"), ToolchainCargo, "cargo clippy -- -D warnings"},
		"cargo forbid wins":             {buildJob("      - run: cargo build\n        env:\n          RUSTFLAGS: -F warnings -A warnings\n"), ToolchainCargo, "-F warnings"},
		"rustc -D warnings":             {buildJob("      - run: rustc -D warnings src/lib.rs\n"), ToolchainRustc, "-D warnings"},
		// The Cargo reference, build.rustflags: CARGO_ENCODED_RUSTFLAGS, RUSTFLAGS, then
		// target.<triple>.rustflags (CARGO_TARGET_<triple>_RUSTFLAGS), then build.rustflags
		// (CARGO_BUILD_RUSTFLAGS); build.warnings (CARGO_BUILD_WARNINGS) "deny", Cargo 1.97.
		"CARGO_BUILD_RUSTFLAGS": {buildJob("      - run: cargo build\n        env:\n          CARGO_BUILD_RUSTFLAGS: -D warnings\n"), ToolchainCargo, "CARGO_BUILD_RUSTFLAGS is"},
		"CARGO_TARGET_<triple>_RUSTFLAGS": {buildJob("      - run: cargo build --target aarch64-unknown-linux-gnu\n        env:\n" +
			"          CARGO_TARGET_AARCH64_UNKNOWN_LINUX_GNU_RUSTFLAGS: -Dwarnings\n          CARGO_BUILD_RUSTFLAGS: -A warnings\n"), ToolchainCargo, "CARGO_TARGET_AARCH64_UNKNOWN_LINUX_GNU_RUSTFLAGS is"},
		"CARGO_BUILD_TARGET":        {"on: push\nenv:\n  CARGO_BUILD_TARGET: wasm32-wasip1\n  CARGO_TARGET_WASM32_WASIP1_RUSTFLAGS: -D warnings\njobs:\n  b:\n    runs-on: x\n    steps:\n      - run: cargo check\n", ToolchainCargo, "WASM32_WASIP1"},
		"CARGO_BUILD_WARNINGS":      {buildJob("      - run: cargo test\n        env:\n          CARGO_BUILD_WARNINGS: deny\n          RUSTFLAGS: --cap-lints warn\n"), ToolchainCargo, "CARGO_BUILD_WARNINGS is deny"},
		"sudo -E cargo":             {buildJob("      - run: sudo -E cargo build\n        env:\n          RUSTFLAGS: -D warnings\n"), ToolchainCargo, "RUSTFLAGS is"},
		"sudo --preserve-env=":      {buildJob("      - run: sudo --preserve-env=RUSTFLAGS,PATH cargo build\n        env:\n          RUSTFLAGS: -D warnings\n"), ToolchainCargo, "RUSTFLAGS is"},
		"sudo with the flags":       {buildJob("      - run: sudo -u builder RUSTFLAGS=-Dwarnings cargo build\n"), ToolchainCargo, "RUSTFLAGS is"},
		"timeout cargo":             {buildJob("      - run: timeout -k 5 30m cargo build\n        env:\n          RUSTFLAGS: -D warnings\n"), ToolchainCargo, "RUSTFLAGS is"},
		"cargo llvm-cov":            {buildJob("      - run: cargo llvm-cov --lcov --output-path lcov.info\n        env:\n          RUSTFLAGS: -D warnings\n"), ToolchainCargo, "RUSTFLAGS is"},
		"cross build":               {buildJob("      - run: cross build --target aarch64-unknown-linux-gnu\n        env:\n          RUSTFLAGS: -D warnings\n"), ToolchainCargo, "RUSTFLAGS is"},
		"clippy after RUSTFLAGS -A": {buildJob("      - run: cargo clippy -- -D warnings\n        env:\n          RUSTFLAGS: -A warnings\n"), ToolchainCargo, "cargo clippy -- -D warnings, after RUSTFLAGS"},
		"cap-lints deny keeps":      {buildJob("      - run: cargo build\n        env:\n          RUSTFLAGS: -D warnings --cap-lints=deny\n"), ToolchainCargo, "--cap-lints=deny"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			lane := oneLane(t, tc.workflow)
			if !lane.Fatal || lane.Toolchain != tc.toolchain || !strings.Contains(lane.Detail, tc.detail) {
				t.Fatalf("lane = %+v; want a fatal %s lane whose detail names %q", lane, tc.toolchain, tc.detail)
			}
		})
	}
}

// Negative: a lane without the form is not fatal and says what it lacks, the lane names its
// workflow, job, step and line, and its remedy is the toolchain's documented form.
func TestMeasureBuildWarnings_Negative_LanesWithoutTheFormAreNamed(t *testing.T) {
	cases := map[string]struct {
		workflow, toolchain, detail string
	}{
		"gcc without -Werror":                     {buildJob("      - run: gcc -Wall -c src/main.c\n"), ToolchainGCCClang, "no -Werror"},
		"gcc -Werror undone":                      {buildJob("      - run: gcc -Werror -Wno-error -c a.c\n"), ToolchainGCCClang, "no -Werror"},
		"gcc -Werror=<warning> only":              {buildJob("      - run: gcc -Werror=switch -c a.c\n"), ToolchainGCCClang, "no -Werror"},
		"cl /WX undone":                           {buildJob("      - run: cl /WX /c main.c\n        env:\n          _CL_: /WX-\n"), ToolchainMSVC, "carries /WX"},
		"CMake default":                           {buildJob("      - run: cmake -S . -B build\n"), ToolchainCMake, "defines no CMAKE_COMPILE_WARNING_AS_ERROR"},
		"CMake false constant":                    {buildJob("      - run: cmake -B build -DCMAKE_COMPILE_WARNING_AS_ERROR=OFF\n"), ToolchainCMake, "not a CMake true constant"},
		"CMake overridden":                        {buildJob("      - run: cmake -B build -DCMAKE_COMPILE_WARNING_AS_ERROR=ON --compile-no-warning-as-error\n"), ToolchainCMake, "--compile-no-warning-as-error"},
		"CMake -Werror in one flags entry":        {buildJob("      - run: cmake -B build -DCMAKE_C_FLAGS=-Werror -DCMAKE_CXX_FLAGS=-Wall\n"), ToolchainCMake, "defines no"},
		"CMake preset":                            {buildJob("      - run: cmake --preset ci\n"), ToolchainCMake, "cacheVariables are not read"},
		"cmake --build without configure":         {buildJob("      - uses: lukka/run-cmake@v10\n      - run: cmake --build build\n"), ToolchainCMake, "no earlier run: step"},
		"Meson setup":                             {buildJob("      - run: meson setup build -Dwerror=true -Dwerror=false\n"), ToolchainMeson, "without --werror"},
		"meson compile without setup":             {buildJob("      - run: meson compile -C build\n"), ToolchainMeson, "no earlier run: step"},
		"cargo build":                             {buildJob("      - run: cargo build --locked\n"), ToolchainCargo, "no RUSTFLAGS, CARGO_ENCODED_RUSTFLAGS"},
		"cargo deny overridden":                   {buildJob("      - run: cargo build\n        env:\n          RUSTFLAGS: -D warnings -W warnings\n"), ToolchainCargo, "RUSTFLAGS is"},
		"cargo encoded flags decide":              {"on: push\nenv:\n  RUSTFLAGS: -D warnings\n  CARGO_ENCODED_RUSTFLAGS: --cfg=ci\njobs:\n  b:\n    runs-on: ubuntu-latest\n    steps:\n      - run: cargo check\n", ToolchainCargo, "CARGO_ENCODED_RUSTFLAGS is"},
		"cargo under an env expression":           {buildJob("      - run: cargo test\n        env: ${{ fromJSON(vars.CI_ENV) }}\n"), ToolchainCargo, "expression"},
		"clippy denying warnings in RUSTDOCFLAGS": {buildJob("      - run: RUSTDOCFLAGS=-Dwarnings cargo clippy\n"), ToolchainCargo, "no RUSTFLAGS"},
		"a plain assignment is not exported":      {buildJob("      - run: |\n          RUSTFLAGS=-Dwarnings\n          cargo build\n"), ToolchainCargo, "no RUSTFLAGS"},
		// rustc 1.98.1: -D warnings --cap-lints warn exits 0 on an unused variable.
		"cargo deny capped":              {buildJob("      - run: cargo build\n        env:\n          RUSTFLAGS: -D warnings --cap-lints warn\n"), ToolchainCargo, "RUSTFLAGS is"},
		"clippy deny capped":             {buildJob("      - run: cargo clippy -- -F warnings --cap-lints allow\n"), ToolchainCargo, "cargo clippy --"},
		"target flags for a host build":  {buildJob("      - run: cargo build\n        env:\n          CARGO_TARGET_X86_64_UNKNOWN_LINUX_GNU_RUSTFLAGS: -D warnings\n"), ToolchainCargo, "applies only when the runner's host"},
		"target flags for another one":   {buildJob("      - run: cargo build --target=aarch64-apple-darwin\n        env:\n          CARGO_TARGET_X86_64_UNKNOWN_LINUX_GNU_RUSTFLAGS: -D warnings\n"), ToolchainCargo, "no RUSTFLAGS"},
		"RUSTFLAGS overrides the target": {buildJob("      - run: cargo build --target x86_64-unknown-linux-gnu\n        env:\n          RUSTFLAGS: --cfg ci\n          CARGO_TARGET_X86_64_UNKNOWN_LINUX_GNU_RUSTFLAGS: -D warnings\n"), ToolchainCargo, "RUSTFLAGS is"},
		// sudoers(5): env_reset is on by default, so sudo drops RUSTFLAGS unless -E keeps it.
		"sudo resets the environment":       {buildJob("      - run: sudo cargo build\n        env:\n          RUSTFLAGS: -D warnings\n"), ToolchainCargo, "no RUSTFLAGS"},
		"sudo drops a prefix":               {buildJob("      - run: RUSTFLAGS=-Dwarnings sudo -n cargo build\n"), ToolchainCargo, "no RUSTFLAGS"},
		"env -u removes it":                 {buildJob("      - run: env -u RUSTFLAGS cargo build\n        env:\n          RUSTFLAGS: -D warnings\n"), ToolchainCargo, "no RUSTFLAGS"},
		"env -i empties it":                 {buildJob("      - run: env -i PATH=/usr/bin cargo build\n        env:\n          RUSTFLAGS: -D warnings\n"), ToolchainCargo, "no RUSTFLAGS"},
		"clang-cl -Werror undone":           {buildJob("      - run: clang-cl -Werror -Wno-error /c a.c\n"), ToolchainMSVC, "carries /WX"},
		"cl takes no -Werror":               {buildJob("      - run: cl -Werror /c a.c\n"), ToolchainMSVC, "carries /WX"},
		"Meson -Dwerror=FALSE":              {buildJob("      - run: meson setup build --werror -Dwerror=FALSE\n"), ToolchainMeson, "without --werror"},
		"a compiler from a matrix":          {buildJob("      - run: ${{ matrix.cc }} -Werror -c a.c\n"), ToolchainUnreadCompiler, "${{ matrix.cc }} names the compiler"},
		"a compiler from an unset variable": {buildJob("      - run: |\n          \"$CC\" -Werror -c a.c\n"), ToolchainUnreadCompiler, "$CC names the compiler"},
		"a compiler with a default":         {buildJob("      - run: ${CC:-gcc} -c a.c\n"), ToolchainUnreadCompiler, "${CC:-gcc} names"},
		"CMake with C flags only":           {buildJob("      - run: cmake -B build -DCMAKE_C_FLAGS=-Werror\n"), ToolchainCMake, "CMAKE_CXX_FLAGS carries no -Werror, and CMake enables C++"},
		"CMake undone in a subshell":        {buildJob("      - run: (cd build && cmake .. -DCMAKE_COMPILE_WARNING_AS_ERROR=OFF)\n"), ToolchainCMake, "=OFF is not"},
		"rustc":                             {buildJob("      - run: rustc src/lib.rs\n"), ToolchainRustc, "no -D warnings"},
		"continue-on-error":                 {buildJob("      - run: gcc -Werror -c a.c\n        continue-on-error: true\n"), ToolchainGCCClang, "continue-on-error"},
		"Go without go vet":                 {buildJob("      - run: go test ./...\n"), ToolchainGo, "no binding step of any workflow runs go vet"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			lane := oneLane(t, tc.workflow)
			if lane.Fatal || lane.Toolchain != tc.toolchain || !strings.Contains(lane.Detail, tc.detail) {
				t.Fatalf("lane = %+v; want a non-fatal %s lane whose detail names %q", lane, tc.toolchain, tc.detail)
			}
			if lane.Workflow != "ci.yml" || lane.Job == "" || lane.Step == "" || lane.Line == 0 || lane.Remedy != buildRemedies[tc.toolchain] {
				t.Fatalf("lane = %+v; want its workflow, job, step, line and the toolchain's remedy", lane)
			}
		})
	}
}

// The Go lanes of every workflow are fatal once any binding step of any workflow runs go vet or
// go test -vet=all, and the lane names where; an advisory go vet step does not count.
func TestMeasureBuildWarnings_GoLanesFollowTheVetStep(t *testing.T) {
	build := buildJob("      - run: go build ./...\n")
	lanes := measureLanes(t, map[string]string{"build.yml": build, "lint.yml": buildJob("      - name: Vet\n        run: go vet ./...\n")})
	if len(lanes) != 1 || !lanes[0].Fatal || !strings.Contains(lanes[0].Detail, ".github/workflows/lint.yml:7: job build, step \"go vet ./...\"") {
		t.Fatalf("lanes = %+v; want the go build lane fatal, naming the vet step", lanes)
	}
	lanes = measureLanes(t, map[string]string{"ci.yml": buildJob("      - run: go test -vet=all ./...\n")})
	if len(lanes) != 1 || !lanes[0].Fatal {
		t.Fatalf("go test -vet=all lanes = %+v; want it fatal", lanes)
	}
	lanes = measureLanes(t, map[string]string{"ci.yml": build + "      - run: go vet ./...\n        continue-on-error: true\n"})
	if len(lanes) != 1 || lanes[0].Fatal {
		t.Fatalf("advisory vet lanes = %+v; want the build lane not fatal", lanes)
	}
	// A continue-on-error go build stays advisory whatever vets the code (review of #816).
	advisory := buildJob("      - run: go build ./...\n        continue-on-error: true\n      - run: go vet ./...\n")
	lanes = measureLanes(t, map[string]string{"ci.yml": advisory})
	if len(lanes) != 1 || lanes[0].Fatal || !strings.Contains(lanes[0].Detail, "continue-on-error lets the step pass") {
		t.Fatalf("advisory go build lanes = %+v; want it not fatal, naming continue-on-error", lanes)
	}
}

// Boundary: commands that compile nothing of the repository are no lanes: a tool installed at a
// version, a program go run executes (the API gate adoption writes), a compiler printing its
// version, a cmake mode that is no configure run, an echo, a command in a comment, cargo install
// and doc, a compiler variable or expression that compiles no source, cargo llvm-cov report, a
// wrapper running something else, and a job or step whose if: is the literal false. A repository without workflows has
// no lanes; a malformed workflow fails closed.
func TestMeasureBuildWarnings_Boundary_NoLanes(t *testing.T) {
	steps := "      - run: go install golang.org/x/tools/cmd/goimports@v0.40.0\n" +
		"      - run: go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.6.0 run\n" +
		"      - run: go run tools/apicompat/gate/main.go -base=\"$BASE\"\n" +
		"      - run: gcc --version && clang-format -i src/a.c && clang-tidy src/a.c\n" +
		"      - run: cmake --version; cmake -E make_directory build\n" +
		"      - run: echo cargo build\n" +
		"      - run: |\n          # cargo build\n          true\n" +
		"      - run: cargo install cargo-deny && cargo doc\n" +
		"      - run: $CC --version && ${{ matrix.cxx }} -dumpversion && cargo llvm-cov report --lcov\n" +
		"      - run: cross --version && sudo -E apt-get install -y gcc && env -i make\n" +
		"      - run: cargo build\n        if: false\n"
	skipped := "  skipped:\n    if: ${{ false }}\n    runs-on: ubuntu-latest\n    steps:\n      - run: gcc -c a.c\n"
	if lanes := measureLanes(t, map[string]string{"ci.yml": buildJob(steps) + skipped}); len(lanes) != 0 {
		t.Fatalf("lanes = %+v; want none", lanes)
	}
	measured, err := MeasureBuildWarnings(context.Background(), t.TempDir(), BuildWarningsInputs{})
	if err != nil || len(measured.Lanes) != 0 {
		t.Fatalf("a repository without workflows = %+v, %v; want no lanes", measured, err)
	}
	root := t.TempDir()
	writeWorkflowFixture(t, root, "ci.yml", "jobs: [unclosed\n")
	if _, err := MeasureBuildWarnings(context.Background(), root, BuildWarningsInputs{}); err == nil || !strings.Contains(err.Error(), "workflow ci.yml") {
		t.Fatalf("a malformed workflow = %v; want a failure naming it", err)
	}
	//nolint:staticcheck // SA1012: a nil context is the refused input under test.
	if _, err := MeasureBuildWarnings(nil, root, BuildWarningsInputs{}); err == nil {
		t.Fatal("MeasureBuildWarnings(nil context) passed; want a failure")
	}
}

// This repository's own workflows build Go alone, and every Go lane is fatal through the go vet
// step of the Platform Neutrality matrix (.github/workflows/portability.yml) or any other.
func TestMeasureBuildWarnings_RepositoryWorkflowsVet(t *testing.T) {
	measured, err := MeasureBuildWarnings(context.Background(), "../..", BuildWarningsInputs{})
	if err != nil {
		t.Fatalf("MeasureBuildWarnings(repository): %v", err)
	}
	if len(measured.Lanes) == 0 {
		t.Fatal("the repository's workflows measured no Go lane; the go build steps are no longer read")
	}
	for _, lane := range measured.Lanes {
		if lane.Toolchain != ToolchainGo || !lane.Fatal || !strings.Contains(lane.Detail, "go vet runs at") {
			t.Errorf("lane %s: %+v; want a fatal Go lane naming its go vet step", lane.Where(), lane)
		}
	}
}

// A toolchain whose program path starts with a variable or an expression runs the program its
// base name names, whatever the directory expands to: each lane is that toolchain's, failing
// without its form and passing with it (review of #816: these were dropped or read as unread).
func TestMeasureBuildWarnings_VariableDirectoryPrograms(t *testing.T) {
	cases := map[string]struct {
		toolchain, without, with string
	}{
		"cargo under $HOME":         {ToolchainCargo, "$HOME/.cargo/bin/cargo build", "RUSTFLAGS=-Dwarnings $HOME/.cargo/bin/cargo build"},
		"cmake under $RUNNER_TEMP":  {ToolchainCMake, "$RUNNER_TEMP/cmake/bin/cmake -B b", "$RUNNER_TEMP/cmake/bin/cmake -B b -DCMAKE_COMPILE_WARNING_AS_ERROR=ON"},
		"meson under an expression": {ToolchainMeson, "${{ github.workspace }}/bin/meson setup build", "${{ github.workspace }}/bin/meson setup build --werror"},
		"clang under ${ANDROID_NDK}": {ToolchainGCCClang, "${ANDROID_NDK}/toolchains/llvm/prebuilt/linux-x86_64/bin/clang -c a.c",
			"${ANDROID_NDK}/toolchains/llvm/prebuilt/linux-x86_64/bin/clang -Werror -c a.c"},
		"gcc under a Windows expression": {ToolchainGCCClang, `${{ runner.temp }}\mingw64\bin\gcc.exe -c a.c`, `${{ runner.temp }}\mingw64\bin\gcc.exe -Werror -c a.c`},
		"go under $GOROOT":               {ToolchainGo, "$GOROOT/bin/go build ./...", "$GOROOT/bin/go build ./... && $GOROOT/bin/go vet ./..."},
		"go under a substitution":        {ToolchainGo, "$(go env GOROOT)/bin/go build ./...", "$(go env GOROOT)/bin/go build ./... && $(go env GOROOT)/bin/go vet ./..."},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			lane := oneLane(t, buildJob("      - run: |\n          "+tc.without+"\n"))
			if lane.Fatal || lane.Toolchain != tc.toolchain {
				t.Fatalf("%q: lane = %+v; want a non-fatal %s lane", tc.without, lane, tc.toolchain)
			}
			lane = oneLane(t, buildJob("      - run: |\n          "+tc.with+"\n"))
			if !lane.Fatal || lane.Toolchain != tc.toolchain {
				t.Fatalf("%q: lane = %+v; want a fatal %s lane", tc.with, lane, tc.toolchain)
			}
		})
	}
}

// An unread program is a C or C++ compiler lane only when its arguments name a source file.
// Negative: a bare -c or /c is the option of many other programs (python -c, sh -c, a tool's -c
// config file), so none of these is a lane. Positive: an unread program given a .c or .cpp
// file is, with or without -c, and its detail names the whole program word.
func TestMeasureBuildWarnings_UnreadProgramsNeedASource(t *testing.T) {
	script := "      - run: |\n" +
		"          ${{ steps.py.outputs.python-path }} -c \"import sys\"\n" +
		"          \"$PYTHON\" -c 'print(1)'\n" +
		"          $SHELL -c 'echo hi'\n" +
		"          ./${{ matrix.bin }} -c config.toml\n" +
		"          $(go env GOPATH)/bin/golangci-lint run -c .golangci.yml\n" +
		"          ${{ matrix.tool }} /c build\n" +
		"          `which python3` -c 'print(2)'\n"
	if lanes := measureLanes(t, map[string]string{"ci.yml": buildJob(script)}); len(lanes) != 0 {
		t.Fatalf("lanes = %+v; want none", lanes)
	}
	cases := map[string]string{
		"${{ matrix.cc }} a.c -o a":     "${{ matrix.cc }} names the compiler",
		"$(which gcc) -Werror -c a.c":   "$(which gcc) names the compiler",
		"`which g++` -c src/main.cpp":   "`which g++` names the compiler",
		"${{ matrix.cxx }} /c main.cpp": "${{ matrix.cxx }} names the compiler",
	}
	for script, detail := range cases {
		lane := oneLane(t, buildJob("      - run: |\n          "+script+"\n"))
		if lane.Fatal || lane.Toolchain != ToolchainUnreadCompiler || !strings.Contains(lane.Detail, detail) {
			t.Errorf("%q: lane = %+v; want an unread compiler lane whose detail names %q", script, lane, detail)
		}
	}
}

// Boundary: shell words rejoin a quoted value split across fields, keep an escaped quote inside
// the word, rejoin a $( ) or backtick substitution, and a variable reference to an unset or
// undecidable variable stays as written.
func TestShellWords_Boundary(t *testing.T) {
	got := shellWords([]string{`CFLAGS="-O2`, `-Werror"`, `'a`, `b'`, `x\"y`, "plain", "$(go", "env", "GOPATH)/bin/x",
		"`which", "gcc`", "$((1+2))", "$(a", "$(b))"})
	want := []string{"CFLAGS=-O2 -Werror", "a b", `x\y`, "plain", "$(go env GOPATH)/bin/x", "`which gcc`", "$((1+2))", "$(a $(b))"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("shellWords = %q; want %q", got, want)
	}
	env := stepEnvironment{script: map[string]string{"SET": "-a -b"}}
	if got := env.expand([]string{"$SET", "${SET}", "$UNSET", "pre$SET"}); strings.Join(got, " ") != "-a -b -a -b $UNSET pre$SET" {
		t.Fatalf("expand = %q", got)
	}
}
