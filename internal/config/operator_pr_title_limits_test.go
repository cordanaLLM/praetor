// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// TestOperatorPRTitleLimitsDecodeMerge covers defaults, decoding and keyed layer overrides.
func TestOperatorPRTitleLimitsDecodeMerge(t *testing.T) {
	policy, err := loadOperatorDocs(t,
		"forge: {pr_title_limits: [{repo: Example/Widget, max: 64}, {repo: example/other, max: 80}]}",
		"forge: {pr_title_limits: [{repo: EXAMPLE/widget, max: 72}, {repo: example/new, max: 100}]}")
	if err != nil {
		t.Fatal(err)
	}
	want := []PRTitleLimit{{Repo: "EXAMPLE/widget", Max: 72}, {Repo: "example/other", Max: 80}, {Repo: "example/new", Max: 100}}
	if got := policy.OperatorSettings().Forge.PRTitleLimits; !reflect.DeepEqual(got, want) {
		t.Fatalf("merged = %+v, want %+v", got, want)
	}
	if got := policy.OperatorFields["forge.pr_title_limits"]; !reflect.DeepEqual(got, []string{"fleet", "workstation"}) {
		t.Fatalf("contributors = %v", got)
	}
	if got := DefaultOperatorSettings().Forge.PRTitleLimits; len(got) != 0 {
		t.Fatalf("defaults = %v", got)
	}
	empty, err := loadOperatorDocs(t, "forge: {pr_title_limits: [{repo: example/widget, max: 64}]}", "forge: {pr_title_limits: []}")
	if err != nil || len(empty.OperatorSettings().Forge.PRTitleLimits) != 1 {
		t.Fatalf("empty later list erased earlier record: %v, %v", empty, err)
	}
}

// TestOperatorPRTitleLimitsValidation refuses bad shapes, bounds and duplicate repositories.
func TestOperatorPRTitleLimitsValidation(t *testing.T) {
	cases := []struct{ name, value string }{
		{"mapping", "{}"}, {"scalar", "64"}, {"string entry", "[example/widget]"},
		{"missing repo", "[{max: 64}]"}, {"missing max", "[{repo: example/widget}]"},
		{"unknown key", "[{repo: example/widget, max: 64, extra: true}]"},
		{"duplicate field", "[{repo: example/widget, max: 64, max: 72}]"},
		{"bare repo", "[{repo: widget, max: 64}]"}, {"too many parts", "[{repo: example/widget/extra, max: 64}]"},
		{"bad owner", "[{repo: bad_owner/widget, max: 64}]"}, {"space", "[{repo: 'example/widget ', max: 64}]"},
		{"low max", "[{repo: example/widget, max: 19}]"}, {"high max", "[{repo: example/widget, max: 257}]"},
		{"float max", "[{repo: example/widget, max: 64.5}]"}, {"string max", "[{repo: example/widget, max: '64'}]"},
		{"duplicate", "[{repo: example/widget, max: 64}, {repo: EXAMPLE/Widget, max: 72}]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadOperatorDocs(t, "forge: {pr_title_limits: "+tc.value+"}", "")
			want := "pr_title_limits"
			if tc.name == "duplicate field" {
				want = "policy mapping keys must be unique strings"
			}
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want %q", err, want)
			}
		})
	}
}

// prTitleLimitsDocument builds distinct repository records for list-bound tests.
func prTitleLimitsDocument(count, offset int) string {
	var body strings.Builder
	body.WriteString("forge:\n  pr_title_limits:\n")
	for i := 0; i < count && i <= MaxPRTitleLimits; i++ {
		fmt.Fprintf(&body, "    - {repo: example/repo%d, max: 64}\n", offset+i)
	}
	return body.String()
}

// TestOperatorPRTitleLimitsBounds covers inclusive limits and per-layer/merged list bounds.
func TestOperatorPRTitleLimitsBounds(t *testing.T) {
	for _, max := range []int{20, 256} {
		t.Run(fmt.Sprint(max), func(t *testing.T) {
			if _, err := loadOperatorDocs(t, fmt.Sprintf("forge: {pr_title_limits: [{repo: example/widget, max: %d}]}", max), ""); err != nil {
				t.Fatal(err)
			}
		})
	}
	if _, err := loadOperatorDocs(t, prTitleLimitsDocument(64, 0), ""); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, fleet, workstation string }{
		{"layer overflow", prTitleLimitsDocument(65, 0), ""},
		{"merge overflow", prTitleLimitsDocument(64, 0), prTitleLimitsDocument(1, 64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := loadOperatorDocs(t, tc.fleet, tc.workstation); err == nil || !strings.Contains(err.Error(), "pr_title_limits") {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if _, err := loadOperatorDocs(t, prTitleLimitsDocument(64, 0), prTitleLimitsDocument(1, 0)); err != nil {
		t.Fatal(err)
	}
}

// TestOperatorPRTitleLimitsProgrammaticShape validates callers bypassing YAML decoding.
func TestOperatorPRTitleLimitsProgrammaticShape(t *testing.T) {
	for _, setting := range []OperatorSetting{
		{Path: "forge.pr_title_limits", List: []string{"example/widget"}},
		{Path: "forge.pr_title_limits", PRTitleLimits: []PRTitleLimit{{Repo: "example/widget", Max: 0}}},
		{Path: "forge.default_owner", PRTitleLimits: []PRTitleLimit{{Repo: "example/widget", Max: 64}}},
	} {
		if err := newOperatorMerge().apply("fleet", []OperatorSetting{setting}); err == nil {
			t.Fatalf("accepted %+v", setting)
		}
	}
	original := OperatorSetting{Path: "forge.pr_title_limits", PRTitleLimits: []PRTitleLimit{{Repo: "example/widget", Max: 64}}}
	cloned := cloneSetting(original)
	cloned.PRTitleLimits[0].Max = 72
	if original.PRTitleLimits[0].Max != 64 || sameSetting(original, cloned) {
		t.Fatal("mutable record slice shared or comparison ignored records")
	}
}
