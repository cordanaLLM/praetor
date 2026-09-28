package needs

import (
	"strings"
	"testing"
)

// zigManifestFixture is a build.zig.zon in the Zig 0.16.0 format (doc/build.zig.zon.md): an enum
// literal name, a hexadecimal fingerprint, a url package with its hash, a lazy one, a quoted
// name, a vendored and a local path package, and the paths list, with comments and trailing
// commas as `zig init` writes them.
const zigManifestFixture = `.{
    // The package name is an enum literal.
    .name = .example_engine,
    .version = "0.1.0",
    .fingerprint = 0x9d2c5e8b1a7f3c04, // Changing this has security and trust implications.
    .minimum_zig_version = "0.16.0",
    .dependencies = .{
        .zlib = .{
            .url = "https://example.com/zlib-1.3.1.tar.gz",
            .hash = "zlib-1.3.1-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
        },
        .@"sound-lib" = .{
            .url = "https://example.com/sound-lib.tar.gz",
            .hash = "N-V-__8AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
            .lazy = true,
        },
        .physics = .{ .path = "vendor/physics" },
        .core = .{ .path = "libs/core" },
    },
    .paths = .{
        "build.zig",
        "build.zig.zon",
        "src",
    },
}
`

// Positive: every dependency is read with its url or path, whatever its spelling, and the rest
// of the manifest (enum literal, hexadecimal number, comments, lists) is accepted.
func TestParseZon_Positive_ReadsDependencies(t *testing.T) {
	deps, err := parseZon(zigManifestFixture)
	if err != nil {
		t.Fatalf("parseZon() error = %v", err)
	}
	want := map[string]zonDependency{
		"zlib":      {url: "https://example.com/zlib-1.3.1.tar.gz", hasURL: true},
		"sound-lib": {url: "https://example.com/sound-lib.tar.gz", hasURL: true},
		"physics":   {path: "vendor/physics", hasPath: true},
		"core":      {path: "libs/core", hasPath: true},
	}
	if len(deps) != len(want) {
		t.Fatalf("dependencies = %+v, want %+v", deps, want)
	}
	for name, dep := range want {
		if deps[name] != dep {
			t.Errorf("dependency %q = %+v, want %+v", name, deps[name], dep)
		}
	}
}

// Positive: a url package and a package vendored under a directory discovery prunes are third
// party; a path package elsewhere in the tree, or beside it, is the repository's own.
func TestZonDependencyThirdParty_3D(t *testing.T) {
	for _, tc := range []struct {
		dep  zonDependency
		want bool
	}{
		{zonDependency{url: "https://example.com/x.tar.gz", hasURL: true}, true},
		{zonDependency{path: "vendor/physics", hasPath: true}, true},
		{zonDependency{path: "./deps/../third_party/audio", hasPath: true}, true},
		{zonDependency{path: `third_party\audio`, hasPath: true}, true},
		{zonDependency{path: "libs/core", hasPath: true}, false},
		{zonDependency{path: "../sibling", hasPath: true}, false},
		{zonDependency{path: "vendored/x", hasPath: true}, false},
		{zonDependency{path: "", hasPath: true}, false},
	} {
		if got := tc.dep.thirdParty(); got != tc.want {
			t.Errorf("%+v.thirdParty() = %v, want %v", tc.dep, got, tc.want)
		}
	}
}

// Negative: a manifest that is not ZON, is not one struct literal, or declares a dependency Zig
// refuses is refused with its line and a reason naming what is wrong.
func TestParseZon_Negative_MalformedManifestRefused(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
	}{
		{"empty", "", "line 1: a build.zig.zon holds one struct literal .{ ... }, found end of file"},
		{"top-level string", `"x"`, "holds one struct literal"},
		{"unterminated string", ".{\n .version = \"0.1.0,\n}", "line 2: unterminated string"},
		{"missing close", ".{ .name = .x,", "expected a value, found end of file"},
		{"missing comma", ".{ .name = .x .version = \"1\" }", "expected ',' or '}' after a value, found '.'"},
		{"trailing value", ".{} .{}", "unexpected '.' after the top-level struct literal"},
		{"unexpected character", ".{ .name = $x }", "unexpected character '$'"},
		{"invalid escape", `.{ .version = "\q" }`, `invalid escape sequence \q`},
		{"dependencies scalar", `.{ .dependencies = "zlib" }`, ".dependencies is a string, not a struct literal"},
		{"dependency scalar", `.{ .dependencies = .{ .zlib = "https://example.com" } }`, `dependency "zlib" is a string, not a struct literal`},
		{"unnamed dependency", `.{ .dependencies = .{ .{ .path = "x" } } }`, "found an unnamed entry"},
		{"url not a string", `.{ .dependencies = .{ .zlib = .{ .url = 42 } } }`, `dependency "zlib": .url is a value, not a string`},
		{"url and path", `.{ .dependencies = .{ .zlib = .{ .url = "u", .hash = "h", .path = "p" } } }`, `dependency "zlib" sets both url and path`},
		{"neither", `.{ .dependencies = .{ .zlib = .{ .hash = "h" } } }`, `dependency "zlib" sets neither url nor path`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, err := parseZon(tc.src)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("parseZon() = %+v, %v; want an error containing %q", deps, err, tc.want)
			}
		})
	}
}

// Boundary: an empty manifest and empty dependencies declare nothing; escapes, a multiline
// string at the end of the file and CRLF line breaks are read; nesting stops at maxZonDepth.
func TestParseZon_Boundary(t *testing.T) {
	for _, src := range []string{".{}", ".{ .dependencies = .{} }", ".{ .dependencies = .{}, }\r\n"} {
		if deps, err := parseZon(src); err != nil || len(deps) != 0 {
			t.Errorf("parseZon(%q) = %+v, %v; want no dependencies", src, deps, err)
		}
	}
	escaped := ".{\r\n .dependencies = .{ .a = .{ .url = \"https://example.com/\\x41\\u{263A}\\\"\" } },\r\n .note =\r\n  \\\\first\r\n  \\\\second\r\n,\r\n .c = 'x', .d = '\\n', .e = -1.5e-3,\r\n}"
	deps, err := parseZon(escaped)
	if err != nil || deps["a"].url != "https://example.com/A☺\"" {
		t.Errorf("parseZon(escaped) = %+v, %v", deps, err)
	}
	if deps, err := parseZon(".{ .note =\n \\\\only line"); err == nil || !strings.Contains(err.Error(), "expected ',' or '}'") {
		t.Errorf("multiline string at end of file = %+v, %v; want the open literal refused", deps, err)
	}
	nested := func(depth int) string {
		return strings.Repeat(".{", depth) + strings.Repeat("}", depth)
	}
	if _, err := parseZon(nested(maxZonDepth)); err != nil {
		t.Errorf("nesting at the bound refused: %v", err)
	}
	if _, err := parseZon(nested(maxZonDepth + 1)); err == nil || !strings.Contains(err.Error(), "nest deeper than 64 levels") {
		t.Errorf("nesting past the bound = %v, want it refused", err)
	}
	for _, src := range []string{`.{ .v = "\x4" }`, `.{ .v = "\u{110000}" }`, `.{ .v = "\u{D800}" }`, `.{ .v = "\u{41" }`, `.{ .c = 'x }`} {
		if _, err := parseZon(src); err == nil {
			t.Errorf("parseZon(%q) accepted a malformed literal", src)
		}
	}
}
