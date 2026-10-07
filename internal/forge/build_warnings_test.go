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
	root := t.TempDir()
	for name, content := range workflows {
		writeWorkflowFixture(t, root, name, content)
	}
	measured, err := MeasureBuildWarnings(context.Background(), root)
	if err != nil {
		t.Fatalf("MeasureBuildWarnings: %v", err)
	}
	return measured.Lanes
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
		"gcc -Werror":                   {buildJob("      - run: gcc -Wall -Werror -c src/main.c\n"), ToolchainGCCClang, "-Werror"},
		"versioned clang++ -Werror":     {buildJob("      - run: clang++-18 -Werror src/a.cpp -o a\n"), ToolchainGCCClang, "-Werror"},
		"MinGW gcc through ccache":      {buildJob("      - run: ccache x86_64-w64-mingw32-gcc -Werror -c a.c\n"), ToolchainGCCClang, "-Werror"},
		"cl /WX":                        {buildJob("      - run: cl.exe /W4 /WX /c src\\main.c\n"), ToolchainMSVC, "/WX"},
		"cl with /WX in CL":             {buildJob("      - run: cl /c main.c\n        env:\n          CL: /W4 /WX\n"), ToolchainMSVC, "/WX"},
		"CMake warning as error":        {buildJob("      - run: cmake -S . -B build -DCMAKE_COMPILE_WARNING_AS_ERROR=ON && cmake --build build\n"), ToolchainCMake, "CMAKE_COMPILE_WARNING_AS_ERROR=ON"},
		"CMake typed definition":        {buildJob("      - run: cmake -B build -D CMAKE_COMPILE_WARNING_AS_ERROR:BOOL=true\n"), ToolchainCMake, "=true"},
		"CMake -Werror in both flags":   {buildJob("      - run: cmake -B build -DCMAKE_C_FLAGS=\"-Wall -Werror\" -DCMAKE_CXX_FLAGS=-Werror\n"), ToolchainCMake, "CMAKE_C_FLAGS and CMAKE_CXX_FLAGS carry -Werror"},
		"CMake through an env variable": {"on: push\nenv:\n  CMAKE_ARGS: -DCMAKE_COMPILE_WARNING_AS_ERROR=1\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: cmake -B build $CMAKE_ARGS\n", ToolchainCMake, "=1"},
		"Meson --werror":                {buildJob("      - run: meson setup build --werror\n      - run: meson compile -C build\n"), ToolchainMeson, "werror"},
		"Meson -D werror=true":          {buildJob("      - run: meson setup -D werror=true build\n"), ToolchainMeson, "werror"},
		"cargo with a quoted prefix":    {buildJob("      - run: RUSTFLAGS=\"-D warnings\" cargo build --locked\n"), ToolchainCargo, `RUSTFLAGS is "-D warnings"`},
		"cargo with an export":          {buildJob("      - run: |\n          export RUSTFLAGS=-Dwarnings\n          cargo +stable test\n"), ToolchainCargo, "-Dwarnings"},
		"cargo with workflow env":       {"on: push\nenv:\n  RUSTFLAGS: --deny=warnings\njobs:\n  t:\n    runs-on: ubuntu-latest\n    steps:\n      - run: cargo nextest run\n", ToolchainCargo, "--deny=warnings"},
		"cargo clippy -- -D warnings":   {buildJob("      - run: cargo clippy --all-targets -- -D warnings\n"), ToolchainCargo, "cargo clippy -- -D warnings"},
		"cargo forbid wins":             {buildJob("      - run: cargo build\n        env:\n          RUSTFLAGS: -F warnings -A warnings\n"), ToolchainCargo, "-F warnings"},
		"rustc -D warnings":             {buildJob("      - run: rustc -D warnings src/lib.rs\n"), ToolchainRustc, "-D warnings"},
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
		"cargo build":                             {buildJob("      - run: cargo build --locked\n"), ToolchainCargo, "neither RUSTFLAGS nor CARGO_ENCODED_RUSTFLAGS"},
		"cargo deny overridden":                   {buildJob("      - run: cargo build\n        env:\n          RUSTFLAGS: -D warnings -W warnings\n"), ToolchainCargo, "RUSTFLAGS is"},
		"cargo encoded flags decide":              {"on: push\nenv:\n  RUSTFLAGS: -D warnings\n  CARGO_ENCODED_RUSTFLAGS: --cfg=ci\njobs:\n  b:\n    runs-on: ubuntu-latest\n    steps:\n      - run: cargo check\n", ToolchainCargo, "CARGO_ENCODED_RUSTFLAGS is"},
		"cargo under an env expression":           {buildJob("      - run: cargo test\n        env: ${{ fromJSON(vars.CI_ENV) }}\n"), ToolchainCargo, "expression"},
		"clippy denying warnings in RUSTDOCFLAGS": {buildJob("      - run: RUSTDOCFLAGS=-Dwarnings cargo clippy\n"), ToolchainCargo, "neither"},
		"a plain assignment is not exported":      {buildJob("      - run: |\n          RUSTFLAGS=-Dwarnings\n          cargo build\n"), ToolchainCargo, "neither"},
		"rustc":                                   {buildJob("      - run: rustc src/lib.rs\n"), ToolchainRustc, "no -D warnings"},
		"continue-on-error":                       {buildJob("      - run: gcc -Werror -c a.c\n        continue-on-error: true\n"), ToolchainGCCClang, "continue-on-error"},
		"Go without go vet":                       {buildJob("      - run: go test ./...\n"), ToolchainGo, "no binding step of any workflow runs go vet"},
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
}

// Boundary: commands that compile nothing of the repository are no lanes: a tool installed at a
// version, a program go run executes (the API gate adoption writes), a compiler printing its
// version, a cmake mode that is no configure run, an echo, a command in a comment, cargo install
// and doc, and a job or step whose if: is the literal false. A repository without workflows has
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
		"      - run: cargo build\n        if: false\n"
	skipped := "  skipped:\n    if: ${{ false }}\n    runs-on: ubuntu-latest\n    steps:\n      - run: gcc -c a.c\n"
	if lanes := measureLanes(t, map[string]string{"ci.yml": buildJob(steps) + skipped}); len(lanes) != 0 {
		t.Fatalf("lanes = %+v; want none", lanes)
	}
	measured, err := MeasureBuildWarnings(context.Background(), t.TempDir())
	if err != nil || len(measured.Lanes) != 0 {
		t.Fatalf("a repository without workflows = %+v, %v; want no lanes", measured, err)
	}
	root := t.TempDir()
	writeWorkflowFixture(t, root, "ci.yml", "jobs: [unclosed\n")
	if _, err := MeasureBuildWarnings(context.Background(), root); err == nil || !strings.Contains(err.Error(), "workflow ci.yml") {
		t.Fatalf("a malformed workflow = %v; want a failure naming it", err)
	}
	//nolint:staticcheck // SA1012: a nil context is the refused input under test.
	if _, err := MeasureBuildWarnings(nil, root); err == nil {
		t.Fatal("MeasureBuildWarnings(nil context) passed; want a failure")
	}
}

// This repository's own workflows build Go alone, and every Go lane is fatal through the go vet
// step of the Platform Neutrality matrix (.github/workflows/portability.yml) or any other.
func TestMeasureBuildWarnings_RepositoryWorkflowsVet(t *testing.T) {
	measured, err := MeasureBuildWarnings(context.Background(), "../..")
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

// Boundary: shell words rejoin a quoted value split across fields, keep an escaped quote inside
// the word, and a variable reference to an unset or undecidable variable stays as written.
func TestShellWords_Boundary(t *testing.T) {
	got := shellWords([]string{`CFLAGS="-O2`, `-Werror"`, `'a`, `b'`, `x\"y`, "plain"})
	want := []string{"CFLAGS=-O2 -Werror", "a b", `x\y`, "plain"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("shellWords = %q; want %q", got, want)
	}
	env := stepEnvironment{script: map[string]string{"SET": "-a -b"}}
	if got := env.expand([]string{"$SET", "${SET}", "$UNSET", "pre$SET"}); strings.Join(got, " ") != "-a -b -a -b $UNSET pre$SET" {
		t.Fatalf("expand = %q", got)
	}
}
