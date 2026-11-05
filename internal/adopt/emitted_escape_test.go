// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"reflect"
	"strings"
	"testing"
)

// TestPythonRawLiteralWrapPreservesEscapes verifies that raw Python literal wrapping keeps escape sequences intact.
func TestPythonRawLiteralWrapPreservesEscapes(t *testing.T) {
	for _, escape := range []string{`\x60`, `\\`, `\u0060`, `\U00000060`} {
		t.Run(escape, func(t *testing.T) {
			// Three body bytes fit: the escape must move intact to the next literal.
			got := (pyToken{"ab" + escape + "!", pyRaw}).literals(6)
			want := []string{`r"ab"`, `r"` + escape + `"`, `r"!"`}
			if escape == `\\` {
				want = []string{`r"ab"`, `r"\\!"`}
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("wrap split escape: got %q, want %q", got, want)
			}
		})
	}
}

// TestEmptyInterceptorPrefiltersUseBlackStableLiteral verifies that empty interceptor prefilters render as a compact dictionary literal.
func TestEmptyInterceptorPrefiltersUseBlackStableLiteral(t *testing.T) {
	if got := interceptorPrefilters(); got != "RULE_PREFILTERS = {}\n" {
		t.Errorf("empty mapping must use Black's compact form: %q", got)
	}
	if strings.Contains(buildBlockEvasionPY(), "RULE_PREFILTERS = {\n}") {
		t.Error("rendered source contains an expanded empty mapping")
	}
}
