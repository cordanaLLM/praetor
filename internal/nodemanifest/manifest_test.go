// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package nodemanifest

import (
	"errors"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/strictjson"
)

// Positive: the name and all four dependency groups are read, in npm's order, and a member the
// struct does not name (scripts) is ignored.
func TestParseManifest_Positive_ReadsNameAndGroups(t *testing.T) {
	manifest, err := ParseManifest([]byte(`{"name":"site","scripts":{"build":"astro build"},` +
		`"dependencies":{"astro":"^7.3.6"},"devDependencies":{"typescript":"~5.9.0"},` +
		`"peerDependencies":{"react":">=19"},"optionalDependencies":{"fsevents":"2.3.3"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Name != "site" {
		t.Errorf("name = %q", manifest.Name)
	}
	want := []string{"astro", "typescript", "react", "fsevents"}
	groups := manifest.Groups()
	if len(groups) != len(want) {
		t.Fatalf("groups = %v", groups)
	}
	for index, group := range groups {
		if len(group) != 1 || group[want[index]] == "" {
			t.Errorf("group %d = %v, want %s", index, group, want[index])
		}
	}
}

// Negative: a repeated member, a second document, broken JSON and a dependency group that is not
// a map of strings are refused, each naming the file kind.
func TestParseManifest_Negative_RefusesAmbiguousOrBrokenText(t *testing.T) {
	cases := []struct {
		text string
		want error
	}{
		{`{"dependencies":{"a":"1"},"dependencies":{"b":"2"}}`, strictjson.ErrDuplicate},
		{`{"dependencies":{"a":"1","a":"2"}}`, strictjson.ErrDuplicate},
		{`{} {}`, strictjson.ErrTrailing},
		{`{`, strictjson.ErrSyntax},
		{`{"dependencies":{"a":1}}`, nil},
	}
	for _, tc := range cases {
		_, err := ParseManifest([]byte(tc.text))
		if err == nil || !strings.HasPrefix(err.Error(), "package.json: ") {
			t.Errorf("ParseManifest(%s) = %v, want a package.json error", tc.text, err)
			continue
		}
		if tc.want != nil && !errors.Is(err, tc.want) {
			t.Errorf("ParseManifest(%s) = %v, want %v", tc.text, err, tc.want)
		}
	}
}

// Boundary: an empty object declares no groups, and text one byte past MaxManifestBytes is
// refused while text at the bound is read.
func TestParseManifest_Boundary_EmptyObjectAndByteBound(t *testing.T) {
	manifest, err := ParseManifest([]byte(`{}`))
	if err != nil || manifest.Name != "" {
		t.Fatalf("empty object = %+v, %v", manifest, err)
	}
	for _, group := range manifest.Groups() {
		if group != nil {
			t.Errorf("an absent group decoded as %v", group)
		}
	}
	prefix, suffix := `{"name":"`, `"}`
	atBound := prefix + strings.Repeat("a", MaxManifestBytes-len(prefix)-len(suffix)) + suffix
	if _, err := ParseManifest([]byte(atBound)); err != nil {
		t.Errorf("text at the byte bound: %v", err)
	}
	pastBound := prefix + strings.Repeat("a", MaxManifestBytes-len(prefix)-len(suffix)+1) + suffix
	if _, err := ParseManifest([]byte(pastBound)); !errors.Is(err, strictjson.ErrSize) {
		t.Errorf("text past the byte bound: %v", err)
	}
}
