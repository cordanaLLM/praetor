// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"strings"
	"testing"
)

func TestReleaseSmokeCLIValidation(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{nil, "--repo"},
		{[]string{"--repo=acme/widgets"}, "--tag"},
		{[]string{"--repo=acme/widgets/extra", "--tag=v1"}, "repository"},
		{[]string{"--repo=acme/widgets", "--tag=v1", "extra"}, "unexpected arguments"},
		{[]string{"--unknown"}, "flag provided"},
		{[]string{"--repo=acme/widgets", "--tag=v1", "--max-bytes=0"}, "max-bytes"},
	}
	for i := 0; i < len(cases); i++ {
		args := append([]string{"smoke"}, cases[i].args...)
		err := runRelease(args)
		if err == nil || !strings.Contains(err.Error(), cases[i].want) {
			t.Errorf("args %q: %v; want %s", args, err, cases[i].want)
		}
	}
	if err := runRelease([]string{"smoke", "--help"}); err != nil {
		t.Fatal(err)
	}
}

func TestReleasePreparationDispatchPreserved(t *testing.T) {
	if err := runRelease([]string{"--help"}); err != nil {
		t.Fatal(err)
	}
	if err := runRelease(nil); err == nil || !strings.Contains(err.Error(), "release --version") {
		t.Fatalf("legacy release: %v", err)
	}
}
