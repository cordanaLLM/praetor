// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
)

// TestAuditPluranoReuseCaller covers a pinned caller, copied gate, tag,
// other owner, and missing workflow through the licensing audit entry point.
func TestAuditPluranoReuseCaller(t *testing.T) {
	const path = ".github/workflows/reuse.yml"
	caller := "jobs:\n  licence:\n    uses: " + forge.PluranoHoristesLicenceAction + "@" +
		forge.PluranoHoristesLicenceWorkflowSHA + "\n"
	cases := []struct {
		name, owner, workflow string
		fails                 bool
	}{
		{"pinned caller", "plurano", caller, false},
		{"copied gate", "plurano", "jobs:\n  reuse:\n    steps:\n      - uses: fsfe/reuse-action@v6\n", true},
		{"tagged caller", "Plurano", "jobs:\n  licence:\n    uses: " + forge.PluranoHoristesLicenceAction + "@v1\n", true},
		{"other owner", "acme", "jobs:\n  reuse:\n    steps:\n      - uses: fsfe/reuse-action@v6\n", false},
		{"absent workflow", "plurano", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.workflow != "" {
				writeFixtureFile(t, root, path, tc.workflow)
			}
			manifest := &config.Manifest{Repository: config.RepositoryMetadata{Owner: tc.owner}}
			_, err := captureStdout(t, func() error {
				return auditLicensing(t.Context(), manifest, root, time.Now())
			})
			if tc.fails {
				if err == nil || !strings.Contains(err.Error(), path) ||
					!strings.Contains(err.Error(), "uses: "+forge.PluranoHoristesLicenceAction+"@<40-hex commit SHA>") ||
					!strings.Contains(err.Error(), "praetorctl adopt") {
					t.Fatalf("workflow should fail with file, expected uses and repair: %v", err)
				}
			} else if err != nil {
				t.Fatalf("workflow should pass: %v", err)
			}
		})
	}
}
