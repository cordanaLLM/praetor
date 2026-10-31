// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/templates"
)

func TestServerAuditScreenMatrixContract(t *testing.T) {
	srv, root := newFixtureServer(t)
	expectText(t, "non-UI", callTool(t, srv, "standards_audit", nil), "[SKIP] UI gates:")
	writeFixtureFile(t, root, "svelte.config.js", "export default {};\n")
	writeFixtureFile(t, root, "package.json", `{"name":"ui","devDependencies":{"@axe-core/playwright":"4.11.1"}}`)
	writeFixtureFile(t, root, "Makefile", "verify-all: ui-gates\nui-gates:\n\t@$$($(PRAETORCTL) ui env) && npx playwright test tests/a11y.spec.ts tests/responsiveness.spec.ts\n")
	writeMCPUISpecs(t, root)
	expectError(t, "missing key", callTool(t, srv, "standards_audit", nil), "missing required ui.screen_matrix")
	manifest, err := os.ReadFile(filepath.Join(root, ".standards.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, root, ".standards.yaml", string(manifest)+"\nui:\n  input_latency_ms: 100\n  input_latency_source: \"cordanaLLM/Aegis-OS@7828da5640001111222233334444555566667777:evidence/latency.json\"\n  screen_matrix: matrix.json\n")
	writeFixtureFile(t, root, "matrix.json", "{}")
	result := callTool(t, srv, "standards_audit", nil)
	expectText(t, "valid matrix reference", result, "[PASS] UI gates:")
	expectText(t, "unpinned schema", result, "schema validation pending A11Y-CHECK1 pin (sveltesentio-a11y-matrix-check-20261028)")
	writeFixtureFile(t, root, "matrix.json", "{")
	expectError(t, "invalid data", callTool(t, srv, "standards_audit", nil), "not valid JSON")
}

func writeMCPUISpecs(t *testing.T, root string) {
	t.Helper()
	ctx := templates.Context{RepoName: "ui-fixture", Owner: "acme", UIRoutes: templates.Routes("/")}
	for _, spec := range []struct{ source, path string }{
		{flavor.UIA11ySpecTemplate, flavor.UIA11ySpecPath},
		{flavor.UIResponsivenessSpecTemplate, flavor.UIResponsivenessSpecPath},
	} {
		content, err := templates.RenderFile(spec.source, ctx)
		if err != nil {
			t.Fatal(err)
		}
		writeFixtureFile(t, root, spec.path, content+"\nimport matrix from '../matrix.json';\n")
	}
}
