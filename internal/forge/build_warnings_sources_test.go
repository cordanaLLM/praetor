package forge

import (
	"context"
	"strings"
	"testing"
)

// cgoFile is a Go file whose package compiles C through cgo.
const cgoFile = "package codec\n\n// #include <stdlib.h>\nimport \"C\"\n\nfunc Free() { C.free(nil) }\n"

// vetAndBuild is a workflow that vets the repository and builds it in a step with env.
func vetAndBuild(env string) string {
	return buildJob("      - run: go vet ./...\n      - run: go build ./...\n" + env)
}

// goBuildLane returns the go build lane of a repository holding files.
func goBuildLane(t *testing.T, files map[string]string) BuildLane {
	t.Helper()
	lanes := measureRepo(t, files, BuildWarningsInputs{})
	for _, lane := range lanes {
		if lane.Toolchain == ToolchainGo {
			return lane
		}
	}
	t.Fatalf("lanes = %+v; want a Go lane", lanes)
	return BuildLane{}
}

// A Go lane in a module with cgo files is fatal only when -Werror is in force for the C code cgo
// compiles: CGO_CPPFLAGS then CGO_CFLAGS ('go help environment'; cmd/go/internal/work/exec.go of
// go 1.27 passes str.StringList(cgoCPPFLAGS, cgoCFLAGS) to the C compiler), and CGO_CXXFLAGS for a
// cgo package holding C++ files. CGO_ENABLED=0 compiles no C ('go help buildconstraint').
func TestMeasureBuildWarnings_GoLanesWithCgo(t *testing.T) {
	cases := map[string]struct {
		files       map[string]string
		fatal       bool
		detail, env string
	}{
		"positive: CGO_CFLAGS": {map[string]string{"codec/codec.go": cgoFile}, true, "cgo files",
			"        env:\n          CGO_CFLAGS: -O2 -Werror\n"},
		"positive: CGO_CPPFLAGS": {map[string]string{"codec/codec.go": cgoFile}, true, "cgo files",
			"        env:\n          CGO_CPPFLAGS: -Werror\n"},
		"positive: CGO_ENABLED=0": {map[string]string{"codec/codec.go": cgoFile}, true, "go vet runs at", "        env:\n          CGO_ENABLED: \"0\"\n"},
		"positive: C++ with CGO_CXXFLAGS": {map[string]string{"codec/codec.go": cgoFile, "codec/impl.cpp": "int f();\n"}, true, "cgo files",
			"        env:\n          CGO_CFLAGS: -Werror\n          CGO_CXXFLAGS: -Werror\n"},
		"negative: no CGO_CFLAGS": {map[string]string{"codec/codec.go": cgoFile}, false, `cgo files (import "C"), and neither CGO_CPPFLAGS nor CGO_CFLAGS`, ""},
		"negative: -Werror undone": {map[string]string{"codec/codec.go": cgoFile}, false, "neither CGO_CPPFLAGS nor CGO_CFLAGS",
			"        env:\n          CGO_CPPFLAGS: -Werror\n          CGO_CFLAGS: -Wno-error\n"},
		"negative: C++ without CGO_CXXFLAGS": {map[string]string{"codec/codec.go": cgoFile, "codec/impl.cc": "int f();\n"}, false, "holds C++ files",
			"        env:\n          CGO_CFLAGS: -Werror\n"},
		"negative: an env: expression": {map[string]string{"codec/codec.go": cgoFile}, false, "decides CGO_CPPFLAGS", "        env: ${{ fromJSON(vars.GO_ENV) }}\n"},
		"boundary: no cgo file":        {map[string]string{"codec/codec.go": "package codec\n\n// \"C\" is a letter.\nimport \"fmt\"\n"}, true, "go vet runs at", ""},
		"boundary: cgo only in tests and skipped trees": {map[string]string{"codec/codec_test.go": cgoFile, "codec/testdata/c.go": cgoFile,
			"_tools/c.go": cgoFile, ".hidden/c.go": cgoFile, "codec/broken.go": "package codec\nimport \"C"}, true, "go vet runs at", ""},
		"boundary: C++ beside no cgo file": {map[string]string{"codec/codec.go": cgoFile, "other/impl.cpp": "int f();\n"}, true, "cgo files",
			"        env:\n          CGO_CFLAGS: -Werror\n"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			files := map[string]string{".github/workflows/ci.yml": vetAndBuild(tc.env)}
			for rel, content := range tc.files {
				files[rel] = content
			}
			lane := goBuildLane(t, files)
			if lane.Fatal != tc.fatal || !strings.Contains(lane.Detail, tc.detail) {
				t.Fatalf("lane = %+v; want fatal %t, detail naming %q", lane, tc.fatal, tc.detail)
			}
		})
	}
}

// The CMake flags form needs -Werror in the flags of every language the repository's sources
// need, CMAKE_C_FLAGS for .c units and CMAKE_CXX_FLAGS for C++ units, and both when it holds
// neither, since project() enables C and CXX by default (cmake.org command/project).
func TestMeasureBuildWarnings_CMakeFlagsPerLanguage(t *testing.T) {
	cFlags := buildJob("      - run: cmake -B build -DCMAKE_C_FLAGS=-Werror\n")
	cases := map[string]struct {
		sources map[string]string
		fatal   bool
		detail  string
	}{
		"positive: C sources only":      {map[string]string{"src/a.c": "int a;\n", "include/a.h": "int a;\n"}, true, "CMAKE_C_FLAGS carries -Werror for C"},
		"negative: C++ sources too":     {map[string]string{"src/a.c": "int a;\n", "src/b.cpp": "int b;\n"}, false, "CMAKE_CXX_FLAGS carries no -Werror, and the repository holds C++ sources"},
		"negative: C++ sources only":    {map[string]string{"src/b.cxx": "int b;\n"}, false, "holds C++ sources"},
		"boundary: no source at all":    {map[string]string{"README.md": "x\n"}, false, "CMake enables C++ when project() names no language"},
		"boundary: an ignored C++ tree": {map[string]string{"src/a.c": "int a;\n", ".git/b.cpp": "int b;\n", "zig-out/c.cpp": "int c;\n"}, true, "for C"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			files := map[string]string{".github/workflows/ci.yml": cFlags}
			for rel, content := range tc.sources {
				files[rel] = content
			}
			lane := oneRepoLane(t, files, BuildWarningsInputs{})
			if lane.Fatal != tc.fatal || !strings.Contains(lane.Detail, tc.detail) {
				t.Fatalf("lane = %+v; want fatal %t, detail naming %q", lane, tc.fatal, tc.detail)
			}
		})
	}
}

// A cargo lane takes the level the [lints] tables of the root Cargo.toml give the warnings group
// (BuildWarningsInputs.CargoLints), before its flag source: cargo 1.98.1 passes --deny=warnings
// from [lints] ahead of RUSTFLAGS, so RUSTFLAGS="-W warnings" undoes it. The level is not read
// for a command run in another directory or with --manifest-path.
func TestMeasureBuildWarnings_CargoLints(t *testing.T) {
	deny := BuildWarningsInputs{CargoLints: CargoLints{Level: "deny", Detail: "every crate of the root Cargo.toml denies warnings in [lints]"}}
	cases := map[string]struct {
		steps  string
		inputs BuildWarningsInputs
		fatal  bool
		detail string
	}{
		"positive: [lints] deny":           {"      - run: cargo build\n", deny, true, "denies warnings in [lints]"},
		"positive: [lints] with clippy":    {"      - run: cargo clippy --all-targets\n", deny, true, "denies warnings"},
		"negative: RUSTFLAGS -W undoes it": {"      - run: cargo build\n        env:\n          RUSTFLAGS: -W warnings\n", deny, false, "RUSTFLAGS is"},
		"negative: a cd elsewhere":         {"      - run: cd fuzz && cargo build\n", deny, false, "not read for a command run in another directory"},
		"negative: working-directory":      {"      - run: cargo build\n        working-directory: fuzz\n", deny, false, "another directory"},
		"negative: --manifest-path":        {"      - run: cargo build --manifest-path fuzz/Cargo.toml\n", deny, false, "--manifest-path"},
		"negative: a crate without it": {"      - run: cargo build\n",
			BuildWarningsInputs{CargoLints: CargoLints{Detail: "crates/b/Cargo.toml does not deny warnings in [lints.rust]"}}, false, "crates/b/Cargo.toml does not deny"},
		"boundary: working-directory .": {"      - run: cargo build\n        working-directory: ./\n", deny, true, "denies"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			lane := oneRepoLane(t, map[string]string{".github/workflows/ci.yml": buildJob(tc.steps)}, tc.inputs)
			if lane.Fatal != tc.fatal || lane.Toolchain != ToolchainCargo || !strings.Contains(lane.Detail, tc.detail) {
				t.Fatalf("lane = %+v; want a Cargo lane, fatal %t, detail naming %q", lane, tc.fatal, tc.detail)
			}
		})
	}
}

// Boundary: the source walk fails closed on a cancelled context, a repository without sources
// holds none, and one holding every kind reports each.
func TestReadNativeSources_Boundary(t *testing.T) {
	empty, err := readNativeSources(context.Background(), t.TempDir())
	if err != nil || empty != (nativeSources{}) {
		t.Fatalf("an empty repository = %+v, %v; want no sources", empty, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readNativeSources(ctx, sbomRepo(t, map[string]string{"a.c": "int a;\n"})); err == nil {
		t.Fatal("a cancelled walk passed; want a failure")
	}
	found, err := readNativeSources(context.Background(), sbomRepo(t, map[string]string{"x/a.go": cgoFile, "x/b.cpp": "int b;\n", "y/c.c": "int c;\n"}))
	if err != nil || found != (nativeSources{c: true, cxx: true, cgo: true, cgoCXX: true}) {
		t.Fatalf("sources = %+v, %v; want every kind", found, err)
	}
}
