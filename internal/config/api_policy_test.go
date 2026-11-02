// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func apiManifest(packages ...string) string {
	body := "version: 1\napi:\n  system_packages:\n"
	for _, name := range packages {
		body += "    - " + name + "\n"
	}
	return body
}

// Positive: a list of Debian package names loads and is returned in order; an absent api section
// and an empty list declare none. Boundary: the longest list and the longest name are accepted.
func TestAPIPolicy_Positive_DeclaredPackagesLoad(t *testing.T) {
	m, err := LoadManifest(writeManifest(t, apiManifest("libudev-dev", "libopenal-dev", "g++-14", "libstdc++-14-dev", "7zip")))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(m.API.Packages(), " ")
	if got != "libudev-dev libopenal-dev g++-14 libstdc++-14-dev 7zip" {
		t.Fatalf("packages = %q", got)
	}
	for _, body := range []string{"version: 1\n", "version: 1\napi: {}\n", "version: 1\napi:\n  system_packages: []\n"} {
		loaded, err := LoadManifest(writeManifest(t, body))
		if err != nil || len(loaded.API.Packages()) != 0 {
			t.Fatalf("%q: packages %v, err %v; want none", body, loaded.API.Packages(), err)
		}
	}
	names := make([]string, MaxAPISystemPackages)
	for index := range names {
		names[index] = fmt.Sprintf("pkg-%02d", index)
	}
	names[0] = "a" + strings.Repeat("b", MaxAPISystemPackageBytes-1)
	if _, err := LoadManifest(writeManifest(t, apiManifest(names...))); err != nil {
		t.Fatalf("the largest valid list was refused: %v", err)
	}
}

// Negative: a name outside the Debian grammar, an option-like name, a repeated name and an
// unknown key under api are refused with api.system_packages named.
// Boundary: one name too long and one list too long.
func TestAPIPolicy_Negative_InvalidPackagesFail(t *testing.T) {
	cases := map[string]string{
		"upper case":     apiManifest("LibUdev-dev"),
		"underscore":     apiManifest("lib_udev"),
		"version pin":    apiManifest("libudev-dev=1.0"),
		"shell metachar": apiManifest(`"libudev-dev;id"`),
		"option":         apiManifest("-oAPT::Get::Assume-Yes=true"),
		"one character":  apiManifest("a"),
		"leading dot":    apiManifest(".hidden"),
		"empty":          apiManifest(`""`),
		"repeated":       apiManifest("libudev-dev", "libudev-dev"),
		"too long":       apiManifest("a" + strings.Repeat("b", MaxAPISystemPackageBytes)),
		"too many":       apiManifest(strings.Split(strings.Repeat("pkg-a ", MaxAPISystemPackages+1), " ")[:MaxAPISystemPackages+1]...),
	}
	for name, body := range cases {
		_, err := LoadManifest(writeManifest(t, body))
		if err == nil || !strings.Contains(err.Error(), "api.system_packages") {
			t.Errorf("%s: err = %v, want api.system_packages refused", name, err)
		}
	}
	if _, err := LoadManifest(writeManifest(t, "version: 1\napi:\n  packages: [libudev-dev]\n")); err == nil {
		t.Error("an unknown key under api loaded")
	}
}

// The api-compatibility rule names the go.mod of one module and a reason the workflow can carry.
// Positive: root and nested go.mod paths. Negative: a glob, a file that is no go.mod, a directory
// and an expression opener in the reason. Boundary: an expired entry stays valid, as for every
// rule, and a lookalike file name is refused.
func TestExceptions_APICompatibilityRuleNamesAModule(t *testing.T) {
	today := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	entry := func(path, glob, reason, expires string) []Exception {
		return []Exception{{Rule: ExceptionRuleAPICompatibility, Path: path, Glob: glob, Entered: today.AddDate(0, 0, 0).Format(ExceptionDateLayout), Reason: reason, Category: "A", Expires: expires}}
	}
	for _, valid := range [][]Exception{
		entry("go.mod", "", "needs libudev.h", today.AddDate(0, 0, 54).Format(ExceptionDateLayout)),
		entry("hw/udev/go.mod", "", "needs libudev.h", today.AddDate(0, 0, 54).Format(ExceptionDateLayout)),
		entry("hw/udev/go.mod", "", "needs libudev.h", today.AddDate(0, 0, -280).Format(ExceptionDateLayout)),
		entry("a_b/C-d.1/go.mod", "", "needs libudev.h", today.AddDate(0, 0, 54).Format(ExceptionDateLayout)),
	} {
		if err := ValidateExceptions(valid, today); err != nil {
			t.Errorf("%+v refused: %v", valid, err)
		}
	}
	for name, invalid := range map[string][]Exception{
		"glob":        entry("", "hw/**/go.mod", "r", today.AddDate(0, 0, 54).Format(ExceptionDateLayout)),
		"not go.mod":  entry("hw/udev/udev.go", "", "r", today.AddDate(0, 0, 54).Format(ExceptionDateLayout)),
		"directory":   entry("hw/udev", "", "r", today.AddDate(0, 0, 54).Format(ExceptionDateLayout)),
		"lookalike":   entry("hw/mygo.mod", "", "r", today.AddDate(0, 0, 54).Format(ExceptionDateLayout)),
		"expression":  entry("hw/udev/go.mod", "", "see ${{ secrets.X }}", today.AddDate(0, 0, 54).Format(ExceptionDateLayout)),
		"path expr":   entry("hw/${{ github.actor }}/go.mod", "", "r", today.AddDate(0, 0, 54).Format(ExceptionDateLayout)),
		"path space":  entry("hw/a b/go.mod", "", "r", today.AddDate(0, 0, 54).Format(ExceptionDateLayout)),
		"path quote":  entry("hw/a\"b/go.mod", "", "r", today.AddDate(0, 0, 54).Format(ExceptionDateLayout)),
		"too far out": entry("hw/udev/go.mod", "", "r", today.AddDate(0, 0, 419).Format(ExceptionDateLayout)),
	} {
		if err := ValidateExceptions(invalid, today); err == nil {
			t.Errorf("%s: %+v accepted", name, invalid)
		}
	}
	if err := ValidateExceptions(entry("hw/udev/go.mod", "", "r", today.AddDate(0, 0, 54).Format(ExceptionDateLayout)), today); err != nil {
		t.Fatal(err)
	}
}
