// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"reflect"
	"testing"
)

// TestShellInspection_ReviewScopes preserves word identity while separating arithmetic code.
func TestShellInspection_ReviewScopes(t *testing.T) {
	for _, tc := range []struct {
		script string
		want   []string
	}{
		{`(( a = $(printf 1) ))`, []string{"printf 1", " ; \n"}},
		{`echo a$((1))b`, []string{"echo a$_b\n"}},
		{`echo $(( $(printf 1) + 1 ))`, []string{"printf 1", "echo $_\n"}},
		{`bas{h..h} -c 'sleep 5 &'`, []string{"bas{h..h} -c 'sleep 5 &'\n"}},
		{`b{a,}sh -c 'sleep 5 &'`, []string{"b{a,}sh -c 'sleep 5 &'\n"}},
		{`{ echo ok; }`, []string{" ;  echo ok;  ; \n"}},
	} {
		t.Run(tc.script, func(t *testing.T) {
			_, scopes, ok := ShellInspection(tc.script, 10)
			if !ok || !reflect.DeepEqual(scopes, tc.want) {
				t.Fatalf("ShellInspection(%q) = %q, %t; want %q", tc.script, scopes, ok, tc.want)
			}
		})
	}
}
