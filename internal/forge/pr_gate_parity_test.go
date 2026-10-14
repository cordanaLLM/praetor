// SPDX-FileCopyrightText: 2026 The Praetor Authors
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The API gate runs stdlib-only in adopted repositories and copies the commit policy's breaking and
// Migration patterns (#1317); a copy that drifts would judge a pre-v1 break by another rule.
func TestGatePatternsMatchCommitPolicy(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "tools", "apicompat", "gate", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	gate := string(data)
	for name, pattern := range map[string]string{
		"breakingHeaderRegex": breakingHeaderRegex.String(),
		"breakingFooterRegex": breakingFooterRegex.String(),
		"migrationRegex":      migrationRegex.String(),
	} {
		if !strings.Contains(gate, name+" = regexp.MustCompile(`"+pattern+"`)") &&
			!strings.Contains(gate, "regexp.MustCompile(`"+pattern+"`)") {
			t.Errorf("tools/apicompat/gate/main.go: %s differs from internal/forge/pr.go (%s)", name, pattern)
		}
	}
}
