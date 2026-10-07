// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pymanifest

import "testing"

// Positive: every requirement form a requirements file or a pyproject.toml list holds reads as
// its lower-cased name and the version its first operator pins.
func TestParseRequirement_Positive_ReadsNameAndVersion(t *testing.T) {
	cases := []struct{ line, name, version string }{
		{"Click", "click", ""},
		{"yamllint==1.37.1", "yamllint", "1.37.1"},
		{"black===26.10.0", "black", "26.10.0"},
		{"requests>=2,<3", "requests", "2,<3"},
		{"ruff ~= 0.14", "ruff", "0.14"},
		{`requests[security]>=2.31 ; python_version<"3.12"  # pinned`, "requests", "2.31"},
		{`"mkdocs-material>=9.6",`, "mkdocs-material", "9.6"},
		{`'zope.interface' ,`, "zope.interface", ""},
		{"foo (>=1.0)", "foo", "1.0"},
		{"pkg @ git+https://example.com/pkg.git@v1#egg=pkg", "pkg", ""},
		{"Pkg_Name.ext@https://example.com/p.whl", "pkg_name.ext", ""},
	}
	for _, tc := range cases {
		got, ok := ParseRequirement(tc.line)
		if !ok || got.Name != tc.name || got.Version != tc.version {
			t.Errorf("ParseRequirement(%q) = %+v, %v; want {%s %s}", tc.line, got, ok, tc.name, tc.version)
		}
	}
}

// Negative: a line that names no distribution is refused.
func TestParseRequirement_Negative_NamesNoDistribution(t *testing.T) {
	for _, line := range []string{
		"", "   ", "# only a comment", "-r base.in", "  -e .", "--index-url https://example.com/simple",
		"<2", ">=1.0", `"",`, "; python_version<'3.8'", "@ https://example.com/p.whl",
	} {
		if got, ok := ParseRequirement(line); ok {
			t.Errorf("ParseRequirement(%q) = %+v, want no requirement", line, got)
		}
	}
}

// Boundary: an extras list nothing closes pins no version, a one-character name is a name, and
// an operator with nothing after it pins the empty version.
func TestParseRequirement_Boundary_EdgeForms(t *testing.T) {
	cases := []struct{ line, name, version string }{
		{"pkg[extra>=1", "pkg", ""},
		{"a", "a", ""},
		{"pkg==", "pkg", ""},
		{"pkg 1.0", "pkg", ""},
	}
	for _, tc := range cases {
		got, ok := ParseRequirement(tc.line)
		if !ok || got.Name != tc.name || got.Version != tc.version {
			t.Errorf("ParseRequirement(%q) = %+v, %v; want {%s %s}", tc.line, got, ok, tc.name, tc.version)
		}
	}
}
