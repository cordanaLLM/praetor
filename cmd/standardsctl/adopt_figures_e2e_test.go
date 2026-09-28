// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

// figureCommandTimeout bounds one run of the figure engine (HISS-02).
const figureCommandTimeout = 2 * time.Minute

// demoFigureSpec is a valid spec whose evidence anchor the fixture provides in src/router.ts.
const demoFigureSpec = `import type { PraetorFigure } from '../../tools/figures/types.ts';

export default {
  title: 'Request path',
  alt: 'The router hands each request to its handler.',
  evidence: ['src/router.ts:route'],
  props: {
    layout: { direction: 'row', children: [{ id: 'router', label: 'Router' }, { id: 'handler', label: 'Handler' }] },
    edges: [{ from: 'router', to: 'handler' }],
    steps: [{ label: 'Route', caption: 'One request.', flow: [{ edges: 'router->handler', say: 'The router picks the handler.' }] }],
  },
} satisfies PraetorFigure;
`

// auditManifestAndLockfileQuiet resolves the effective policy of the repository at root the way
// audit does, with its report lines captured.
func auditManifestAndLockfileQuiet(t *testing.T, root string) (*config.EffectivePolicy, error) {
	t.Helper()
	var effective *config.EffectivePolicy
	_, err := captureStdout(t, func() error {
		var resolveErr error
		manifestPath := filepath.Join(root, ".standards.yaml")
		effective, resolveErr = auditManifestAndLockfile(t.Context(), &auditOptions{
			rootDir: root, manifestPath: manifestPath,
			policy: config.EffectiveOptions{Root: root, ManifestPath: manifestPath, Audit: true},
		})
		return resolveErr
	})
	return effective, err
}

// runFigureEngine runs `node tools/figures/build.mjs <command>` in root and returns its exit
// code and combined output.
func runFigureEngine(t *testing.T, node, root, command string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), figureCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, filepath.Join("tools", "figures", "build.mjs"), command)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), string(out)
	}
	if err != nil {
		t.Fatalf("node tools/figures/build.mjs %s: %v", command, err)
	}
	return 0, string(out)
}

// End to end through the adopt command and the adopted engine: adoption with the default facets,
// docs:seo-portal among them, writes the engine, the attribute block and the docs-figures target
// that audit then accepts. In the adopted repository check and sources skip with the reason while
// it has no spec (positive), fail on a malformed spec (negative), and pass on a built valid one
// (boundary: the first spec turns the skip into a real check).
func TestAdoptFigureEngineEndToEnd(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH: the figure engine needs Node 22.18 or later, so its adopted commands cannot run here")
	}
	source, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeFixtureFile(t, root, "src/router.ts", "export function route() {}\n")
	initGitFixture(t, root)
	if _, err := captureStdout(t, func() error {
		return dispatchCommand("adopt", []string{"--path", root, "--profile", "framework", "--lock-source-root", source})
	}); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	effective, err := auditManifestAndLockfileQuiet(t, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := captureStdout(t, func() error {
		return auditDocumentationGate(t.Context(), effective.Manifest, root, effective.Policy.BranchProtection)
	}); err != nil {
		t.Fatalf("audit of the adopted documentation gate: %v", err)
	}
	for _, command := range []string{"check", "sources"} {
		code, out := runFigureEngine(t, node, root, command)
		if code != 0 || !strings.Contains(out, "skipped: this repository has no figure spec") {
			t.Fatalf("%s without a spec: exit %d\n%s", command, code, out)
		}
	}
	writeFixtureFile(t, root, "docs/figures/broken.ts", "export default { title: 'Broken' };\n")
	if code, out := runFigureEngine(t, node, root, "check"); code != 1 || !strings.Contains(out, "docs/figures/broken.ts") {
		t.Fatalf("check over a malformed spec: exit %d\n%s", code, out)
	}
	if err := os.Remove(filepath.Join(root, "docs", "figures", "broken.ts")); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, root, "docs/figures/request-path.ts", demoFigureSpec)
	for _, command := range []string{"build", "check", "sources"} {
		if code, out := runFigureEngine(t, node, root, command); code != 0 || strings.Contains(out, "skipped") {
			t.Fatalf("%s over a valid spec: exit %d\n%s", command, code, out)
		}
	}
}
