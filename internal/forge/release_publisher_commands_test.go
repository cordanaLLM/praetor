// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"strings"
	"testing"
)

func TestScriptPublishesRelease(t *testing.T) {
	cases := []struct {
		name       string
		script     string
		want       bool
		wantErrSub string
	}{
		{
			name:   "gh release create",
			script: "gh release create v1.0.0 --title 'Initial Release'",
			want:   true,
		},
		{
			name:   "gh release upload",
			script: "gh release upload v1.0.0 dist/*",
			want:   true,
		},
		{
			name:   "gh release edit publish draft",
			script: "gh release edit v1.0.0 --draft=false",
			want:   true,
		},
		{
			name:   "gh release edit keep draft",
			script: "gh release edit v1.0.0 --draft=true",
			want:   false,
		},
		{
			name:   "gh release view does not publish",
			script: "gh release view v1.0.0",
			want:   false,
		},
		{
			name:   "echo gh release create is printed only",
			script: "echo gh release create v1.0.0",
			want:   false,
		},
		{
			name:   "other tool mentioning gh release",
			script: "git commit -m 'gh release create v1'",
			want:   false,
		},
		{
			name:   "chained commands find publisher",
			script: "test -d dist && gh release upload v1 dist/artifact.tar.gz",
			want:   true,
		},
		{
			name:       "script past the shell reader bound fails closed (ErrShellReadLimit)",
			script:     strings.Repeat("echo test\n", maxRunScriptFields),
			wantErrSub: "4096",
		},
		{
			name:   "script boundary finds publisher at last position",
			script: strings.Repeat("true;", 2046) + "gh release create v1",
			want:   true,
		},
	}

	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			got, err := scriptPublishesRelease(tc.script)
			if tc.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("expected error containing %q, got: %v", tc.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("scriptPublishesRelease = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGhCommandPublishesRelease(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"too few args", []string{"release"}, false},
		{"different subcommand", []string{"repo", "create"}, false},
		{"release create", []string{"release", "create", "v1.0.0"}, true},
		{"release upload", []string{"release", "upload", "v1.0.0", "bin"}, true},
		{"release edit draft false", []string{"release", "edit", "v1.0.0", "--draft=false"}, true},
		{"release edit draft true", []string{"release", "edit", "v1.0.0", "--draft=true"}, false},
		{"release edit no draft flag", []string{"release", "edit", "v1.0.0", "--notes", "hello"}, false},
		{"release delete", []string{"release", "delete", "v1.0.0"}, false},
	}
	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			if got := ghCommandPublishesRelease(tc.args); got != tc.want {
				t.Fatalf("ghCommandPublishesRelease(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}
