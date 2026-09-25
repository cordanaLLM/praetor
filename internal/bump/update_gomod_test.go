// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package bump

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// fallbackCandidate is the upgrade every fallback fixture below applies.
var fallbackCandidate = UpgradeCandidate{
	Package: "example.com/pkg", CurrentVersion: "v1.0.0", TargetVersion: "v1.2.0", ManifestType: "go.mod",
}

// goModVersion is one module path and version as `go mod edit -json` reports it.
type goModVersion struct {
	Path    string `json:"Path"`
	Version string `json:"Version"`
}

// goModParse is the part of `go mod edit -json` output the fixtures assert on.
type goModParse struct {
	Require []goModVersion `json:"Require"`
	Exclude []goModVersion `json:"Exclude"`
	Replace []struct {
		Old goModVersion `json:"Old"`
		New goModVersion `json:"New"`
	} `json:"Replace"`
}

// offlineGoContext returns a context whose child Go commands cannot reach a module proxy,
// read a workspace file, switch toolchains or reuse the host's module cache. `go get` then
// fails for any module, deterministically and without network, which is the failure the
// fallback edit exists for.
func offlineGoContext(t *testing.T) context.Context {
	t.Helper()
	inherited := os.Environ()
	env := make([]string, 0, len(inherited)+6)
	for _, entry := range inherited {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(key), "GO") {
			env = append(env, entry)
		}
	}
	env = append(env, "GOPROXY=off", "GOFLAGS=", "GOTOOLCHAIN=local", "GOWORK=off",
		"GOSUMDB=off", "GOMODCACHE="+t.TempDir())
	ctx, err := util.WithCommandEnvironment(t.Context(), env)
	if err != nil {
		t.Fatalf("offline Go environment: %v", err)
	}
	return ctx
}

// parseGoMod reads dir/go.mod with the Go toolchain's own parser, so a fixture proves the
// edited manifest is one the go command accepts, not only that a substring changed.
func parseGoMod(t *testing.T, dir string) goModParse {
	t.Helper()
	out, err := util.RunCommand(offlineGoContext(t), dir, "go", "mod", "edit", "-json")
	if err != nil {
		t.Fatalf("go mod edit -json rejected the manifest: %v\n%s", err, out)
	}
	var parsed goModParse
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("decode go mod edit -json: %v\n%s", err, out)
	}
	return parsed
}

func readGoMod(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	return string(data)
}

// requiredAt reports the version parsed requires for path, or "" when it does not.
func requiredAt(parsed goModParse, path string) string {
	for _, requirement := range parsed.Require {
		if requirement.Path == path {
			return requirement.Version
		}
	}
	return ""
}

// assertFallbackEdit applies fallbackCandidate to before and requires the result to be
// exactly want, byte for byte, and accepted by the go command with the package at the
// target version.
func assertFallbackEdit(t *testing.T, before, want string) goModParse {
	t.Helper()
	dir := t.TempDir()
	writeGoMod(t, dir, before)
	if err := fallbackGoModEdit(t.Context(), dir, fallbackCandidate); err != nil {
		t.Fatalf("fallback edit refused: %v", err)
	}
	if got := readGoMod(t, dir); got != want {
		t.Fatalf("go.mod after the edit:\n%q\nwant:\n%q", got, want)
	}
	parsed := parseGoMod(t, dir)
	if version := requiredAt(parsed, fallbackCandidate.Package); version != fallbackCandidate.TargetVersion {
		t.Fatalf("go command reads %s at %q, want %s", fallbackCandidate.Package, version, fallbackCandidate.TargetVersion)
	}
	return parsed
}

// Positive: the one requirement line in a require block moves to the target version. Every
// other byte, the neighbouring requirements and their // indirect markers included, stays.
func TestFallbackGoModEdit_Positive_RewritesTheRequireBlockLine(t *testing.T) {
	before := "module example.com/app\n\ngo 1.21\n\nrequire (\n" +
		"\tgithub.com/spf13/cobra v1.8.1\n" +
		"\texample.com/pkg v1.0.0\n" +
		"\tgopkg.in/yaml.v3 v3.0.1 // indirect\n)\n"
	want := "module example.com/app\n\ngo 1.21\n\nrequire (\n" +
		"\tgithub.com/spf13/cobra v1.8.1\n" +
		"\texample.com/pkg v1.2.0\n" +
		"\tgopkg.in/yaml.v3 v3.0.1 // indirect\n)\n"
	parsed := assertFallbackEdit(t, before, want)
	if len(parsed.Require) != 3 || requiredAt(parsed, "github.com/spf13/cobra") != "v1.8.1" || requiredAt(parsed, "gopkg.in/yaml.v3") != "v3.0.1" {
		t.Fatalf("neighbouring requirements changed: %+v", parsed.Require)
	}
}

// Negative: each fixture names "example.com/pkg v1.0.0" somewhere other than its require
// line, before that line. A whole-file substring replace edits the first occurrence -- the
// comment, the excluded prerelease, the replace directive or a longer module path -- and
// leaves the requirement alone. Only the require line may change.
func TestFallbackGoModEdit_Negative_OnlyTheRequirementChanges(t *testing.T) {
	const head = "module example.com/app\n\ngo 1.21\n\n"
	const require = "require example.com/pkg v1.0.0\n"
	const required = "require example.com/pkg v1.2.0\n"
	cases := map[string]string{
		"comment":             "// example.com/pkg v1.0.0 carries a regression fixed in v1.2.0.\n",
		"exclude block":       "exclude (\n\texample.com/pkg v1.0.0-rc.1\n)\n\n",
		"single-line exclude": "exclude example.com/pkg v1.0.0-rc.1\n\n",
		"replace block":       "replace (\n\texample.com/pkg v1.0.0 => example.com/fork v1.0.1\n)\n\n",
		"single-line replace": "replace example.com/pkg v1.0.0 => ../pkg\n\n",
		"longer module path":  "require mirror.example.com/pkg v1.0.0\n",
	}
	for name, prefix := range cases {
		t.Run(name, func(t *testing.T) {
			parsed := assertFallbackEdit(t, head+prefix+require, head+prefix+required)
			if name == "longer module path" && requiredAt(parsed, "mirror.example.com/pkg") != "v1.0.0" {
				t.Fatalf("a different module was rewritten: %+v", parsed.Require)
			}
			for _, excluded := range parsed.Exclude {
				if excluded.Version != "v1.0.0-rc.1" {
					t.Fatalf("exclude directive rewritten: %+v", parsed.Exclude)
				}
			}
			for _, replaced := range parsed.Replace {
				if replaced.Old.Version != "" && replaced.Old.Version != "v1.0.0" {
					t.Fatalf("replace directive rewritten: %+v", parsed.Replace)
				}
			}
		})
	}
}

// Negative: without exactly one require line naming the package at the current version,
// the edit is refused and go.mod keeps every byte. An empty current version used to match
// "example.com/pkg " and splice the target in front of the real version.
func TestFallbackGoModEdit_Negative_RefusesWithoutAnExactRequirement(t *testing.T) {
	const head = "module example.com/app\n\ngo 1.21\n\n"
	cases := map[string]struct {
		manifest string
		mutate   func(*UpgradeCandidate)
	}{
		"empty current version":  {head + "require example.com/pkg v2.0.0\n", func(c *UpgradeCandidate) { c.CurrentVersion = "" }},
		"drifted version":        {head + "require example.com/pkg v1.0.0-rc.1\n", nil},
		"excluded only":          {head + "exclude example.com/pkg v1.0.0\n", nil},
		"replaced only":          {head + "replace (\n\texample.com/pkg v1.0.0 => ../pkg\n)\n", nil},
		"commented out":          {head + "// require example.com/pkg v1.0.0\n", nil},
		"required twice":         {head + "require example.com/pkg v1.0.0\nrequire (\n\texample.com/pkg v1.1.0\n)\n", nil},
		"target is a query":      {head + "require example.com/pkg v1.0.0\n", func(c *UpgradeCandidate) { c.TargetVersion = "latest" }},
		"target carries a token": {head + "require example.com/pkg v1.0.0\n", func(c *UpgradeCandidate) { c.TargetVersion = "v1.2.0 // pinned" }},
		"target is not semver":   {head + "require example.com/pkg v1.0.0\n", func(c *UpgradeCandidate) { c.TargetVersion = "v1.2" }},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeGoMod(t, dir, tc.manifest)
			candidate := fallbackCandidate
			if tc.mutate != nil {
				tc.mutate(&candidate)
			}
			err := fallbackGoModEdit(t.Context(), dir, candidate)
			if !errors.Is(err, errFallbackRefused) {
				t.Fatalf("fallback edit err = %v, want errFallbackRefused", err)
			}
			if got := readGoMod(t, dir); got != tc.manifest {
				t.Fatalf("refused edit still changed go.mod:\n%q", got)
			}
		})
	}
}

// Boundary: the line shapes a requirement legally takes -- a single-line require, the first
// and the last line of a block, a file without a final newline, CRLF endings, space
// alignment and a trailing comment -- each keep their layout around the new version.
func TestFallbackGoModEdit_Boundary_LineShapes(t *testing.T) {
	const head = "module example.com/app\n\ngo 1.21\n\n"
	cases := map[string][2]string{
		"single-line require": {head + "require example.com/pkg v1.0.0\n", head + "require example.com/pkg v1.2.0\n"},
		"no final newline":    {head + "require example.com/pkg v1.0.0", head + "require example.com/pkg v1.2.0"},
		"first block line": {
			head + "require (\n\texample.com/pkg v1.0.0\n\texample.com/other v0.3.0\n)\n",
			head + "require (\n\texample.com/pkg v1.2.0\n\texample.com/other v0.3.0\n)\n",
		},
		"last block line": {
			head + "require (\n\texample.com/other v0.3.0\n\texample.com/pkg v1.0.0\n)\n",
			head + "require (\n\texample.com/other v0.3.0\n\texample.com/pkg v1.2.0\n)\n",
		},
		"crlf endings": {
			strings.ReplaceAll(head+"require (\n\texample.com/pkg v1.0.0\n)\n", "\n", "\r\n"),
			strings.ReplaceAll(head+"require (\n\texample.com/pkg v1.2.0\n)\n", "\n", "\r\n"),
		},
		"space alignment and comment": {
			head + "require (\n    example.com/pkg    v1.0.0   // indirect\n)\n",
			head + "require (\n    example.com/pkg    v1.2.0   // indirect\n)\n",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assertFallbackEdit(t, tc[0], tc[1])
		})
	}
}

// Boundary: a manifest of exactly MaxManifestLines lines is edited; one line more is
// refused before anything is written.
func TestFallbackGoModEdit_Boundary_LineBound(t *testing.T) {
	const head = "module example.com/app\nrequire example.com/pkg v1.0.0\n"
	exact := head + strings.Repeat("//\n", MaxManifestLines-2)
	dir := t.TempDir()
	writeGoMod(t, dir, exact)
	if err := fallbackGoModEdit(t.Context(), dir, fallbackCandidate); err != nil {
		t.Fatalf("manifest of exactly %d lines refused: %v", MaxManifestLines, err)
	}
	if !strings.HasPrefix(readGoMod(t, dir), "module example.com/app\nrequire example.com/pkg v1.2.0\n") {
		t.Fatal("manifest at the line bound was not edited")
	}
	over := exact + "//\n"
	dir = t.TempDir()
	writeGoMod(t, dir, over)
	if err := fallbackGoModEdit(t.Context(), dir, fallbackCandidate); !errors.Is(err, errFallbackRefused) {
		t.Fatalf("manifest of %d lines: err = %v, want errFallbackRefused", MaxManifestLines+1, err)
	}
	if readGoMod(t, dir) != over {
		t.Fatal("refused manifest changed")
	}
}

// Negative: `go get` fails and the fallback rewrites the requirement. The update is still
// reported as failed, carrying the go get error, whether or not go mod tidy then succeeds;
// before, a successful text edit discarded the go get error and reported success.
func TestApplyGoUpdate_Negative_GoGetFailureSurvivesTheFallback(t *testing.T) {
	for _, tidy := range []bool{false, true} {
		dir := t.TempDir()
		writeGoMod(t, dir, "module example.com/app\n\ngo 1.21\n\nrequire example.com/pkg v1.0.0\n")
		err := applyGoUpdate(offlineGoContext(t), dir, fallbackCandidate, tidy)
		if !errors.Is(err, errGoModFallbackEdit) {
			t.Fatalf("tidy=%v: err = %v, want errGoModFallbackEdit", tidy, err)
		}
		if !strings.Contains(err.Error(), "go get example.com/pkg@v1.2.0 failed") {
			t.Fatalf("tidy=%v: go get failure missing from %v", tidy, err)
		}
		if !tidy && requiredAt(parseGoMod(t, dir), fallbackCandidate.Package) != "v1.2.0" {
			t.Fatalf("fallback edit not applied: %s", readGoMod(t, dir))
		}
	}
}

// Negative: when `go get` fails and the fallback is refused, both reasons are reported and
// go.mod is untouched. The refusal used to be dropped, leaving only the go get error.
func TestApplyGoUpdate_Negative_RefusedFallbackIsReported(t *testing.T) {
	const manifest = "module example.com/app\n\ngo 1.21\n\nrequire example.com/pkg v2.0.0\n"
	dir := t.TempDir()
	writeGoMod(t, dir, manifest)
	candidate := fallbackCandidate
	candidate.CurrentVersion = ""
	err := applyGoUpdate(offlineGoContext(t), dir, candidate, true)
	if !errors.Is(err, errFallbackRefused) || errors.Is(err, errGoModFallbackEdit) {
		t.Fatalf("err = %v, want the fallback refusal and no fallback edit", err)
	}
	if !strings.Contains(err.Error(), "go get example.com/pkg@v1.2.0 failed") {
		t.Fatalf("go get failure missing from %v", err)
	}
	if readGoMod(t, dir) != manifest {
		t.Fatalf("refused fallback changed go.mod: %q", readGoMod(t, dir))
	}
}

// Boundary: UpdateAll does not count a fallback edit as applied, reports the go get error,
// and still runs the module's go mod tidy pass, because go.mod changed. The fixture imports
// the package, so the offline tidy fails and its error proves the pass ran.
func TestUpdateAll_Boundary_FallbackEditIsReportedAndTidied(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir, "module example.com/app\n\ngo 1.21\n\nrequire example.com/pkg v1.0.0\n")
	source := "package app\n\nimport _ \"example.com/pkg\"\n"
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	applied, err := UpdateAll(offlineGoContext(t), dir, []UpgradeCandidate{fallbackCandidate})
	if applied != 0 {
		t.Fatalf("fallback edit counted as applied: %d", applied)
	}
	if !errors.Is(err, errGoModFallbackEdit) {
		t.Fatalf("err = %v, want errGoModFallbackEdit", err)
	}
	if !strings.Contains(err.Error(), "go mod tidy in "+dir) {
		t.Fatalf("module with an edited go.mod was not tidied: %v", err)
	}
	if !strings.Contains(readGoMod(t, dir), "require example.com/pkg v1.2.0\n") {
		t.Fatalf("fallback edit not applied: %s", readGoMod(t, dir))
	}
}
