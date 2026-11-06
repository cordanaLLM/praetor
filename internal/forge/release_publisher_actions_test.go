// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

// TestActionPublishesRelease covers publisher actions and the GoReleaser input rules, including the argument bound.
func TestActionPublishesRelease(t *testing.T) {
	cases := []struct {
		name       string
		step       ghworkflow.Step
		want       bool
		wantErrSub string
	}{
		{
			name: "jellysin publish action",
			step: ghworkflow.Step{Uses: "jellysin/release-helper/actions/publish@v1"},
			want: true,
		},
		{
			name: "softprops action-gh-release",
			step: ghworkflow.Step{Uses: "softprops/action-gh-release@v2"},
			want: true,
		},
		{
			name: "ncipollo release-action",
			step: ghworkflow.Step{Uses: "ncipollo/release-action@v1"},
			want: true,
		},
		{
			name: "actions create-release",
			step: ghworkflow.Step{Uses: "actions/create-release@v1"},
			want: true,
		},
		{
			name: "checkout action does not publish",
			step: ghworkflow.Step{Uses: "actions/checkout@v4"},
			want: false,
		},
		{
			name: "goreleaser install only",
			step: ghworkflow.Step{
				Uses: "goreleaser/goreleaser-action@v6",
				With: map[string]any{"install-only": "true"},
			},
			want: false,
		},
		{
			name: "goreleaser help flag",
			step: ghworkflow.Step{
				Uses: "goreleaser/goreleaser-action@v6",
				With: map[string]any{"args": "--help"},
			},
			want: false,
		},
		{
			name: "goreleaser version flag",
			step: ghworkflow.Step{
				Uses: "goreleaser/goreleaser-action@v6",
				With: map[string]any{"args": "-v"},
			},
			want: false,
		},
		{
			name: "goreleaser build subcommand",
			step: ghworkflow.Step{
				Uses: "goreleaser/goreleaser-action@v6",
				With: map[string]any{"args": "build --snapshot"},
			},
			want: false,
		},
		{
			name: "goreleaser release default",
			step: ghworkflow.Step{
				Uses: "goreleaser/goreleaser-action@v6",
				With: map[string]any{"args": "release --clean"},
			},
			want: true,
		},
		{
			name: "goreleaser no args defaults to release",
			step: ghworkflow.Step{
				Uses: "goreleaser/goreleaser-action@v6",
				With: map[string]any{},
			},
			want: true,
		},
		{
			name: "goreleaser snapshot flag skips",
			step: ghworkflow.Step{
				Uses: "goreleaser/goreleaser-action@v6",
				With: map[string]any{"args": "release --snapshot"},
			},
			want: false,
		},
		{
			name: "goreleaser skip publish equals",
			step: ghworkflow.Step{
				Uses: "goreleaser/goreleaser-action@v6",
				With: map[string]any{"args": "release --skip=publish"},
			},
			want: false,
		},
		{
			name: "goreleaser skip publish next arg",
			step: ghworkflow.Step{
				Uses: "goreleaser/goreleaser-action@v6",
				With: map[string]any{"args": "release --skip publish"},
			},
			want: false,
		},
		{
			name: "goreleaser skip list with publish",
			step: ghworkflow.Step{
				Uses: "goreleaser/goreleaser-action@v6",
				With: map[string]any{"args": "release --skip=announce,publish,sign"},
			},
			want: false,
		},
		{
			name: "goreleaser skip list without publish publishes",
			step: ghworkflow.Step{
				Uses: "goreleaser/goreleaser-action@v6",
				With: map[string]any{"args": "release --skip=announce,sign"},
			},
			want: true,
		},
		{
			name: "goreleaser args exceed bound errors",
			step: ghworkflow.Step{
				Uses: "goreleaser/goreleaser-action@v6",
				With: map[string]any{"args": strings.Repeat("opt ", maxRunScriptFields+1)},
			},
			wantErrSub: "4096",
		},
		{
			name: "goreleaser exactly maxRunScriptFields args reads bound",
			step: ghworkflow.Step{
				Uses: "goreleaser/goreleaser-action@v6",
				With: map[string]any{
					"args": strings.Repeat("--skip=announce ", maxRunScriptFields-1) + "--skip=publish",
				},
			},
			want: false,
		},
		{
			name: "goreleaser exactly maxRunScriptFields args publishes at bound",
			step: ghworkflow.Step{
				Uses: "goreleaser/goreleaser-action@v6",
				With: map[string]any{
					"args": strings.Repeat("--skip=announce ", maxRunScriptFields-1) + "release",
				},
			},
			want: true,
		},
	}

	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			got, err := actionPublishesRelease(tc.step)
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
				t.Fatalf("actionPublishesRelease = %v, want %v", got, tc.want)
			}
		})
	}
}
