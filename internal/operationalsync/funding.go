package operationalsync

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/cordanaLLM/praetor/internal/funding"
	"github.com/cordanaLLM/praetor/internal/util"
)

// The funding overlay. An operational fork carries the operator's funding document at
// funding.ConfigFile (an owner-only path), and plan and prepare render the engine's funding
// surfaces (funding.SurfacePaths) from it with the same renderer as `praetorctl docs funding`.
// The engine itself always carries the unconfigured rendering, so without this overlay a
// configured README.md, mkdocs.yml or .github/FUNDING.yml in the fork would be an unexpected
// owner tree difference and a configured account could never be published (issue #222).

// fundingSurface reports whether path is a file the funding overlay renders.
func fundingSurface(path string) bool {
	return slices.Contains(funding.SurfacePaths(), path)
}

// overlayPath reports whether plan and prepare accept an owner difference at path outside the
// owner-only prefixes: an identity overlay file, or a funding surface the overlay renders and
// then verifies byte for byte.
func overlayPath(path string) bool {
	return allowedPath(path) || fundingSurface(path)
}

// readSurfaces returns the funding surface files present in the commit or tree sha; an absent
// file has no key, the convention funding.RenderFiles reads.
func (op *operation) readSurfaces(ctx context.Context, dir, sha string) (map[string][]byte, error) {
	files := make(map[string][]byte, len(funding.SurfacePaths()))
	for _, path := range funding.SurfacePaths() {
		raw, present, err := op.git.optionalBlob(ctx, dir, sha, path)
		if err != nil {
			return nil, err
		}
		if len(raw) > 1<<20 {
			return nil, fmt.Errorf("%s exceeds 1 MiB", path)
		}
		if present {
			files[path] = raw
		}
	}
	return files, nil
}

// readFundingConfig loads the operator's funding document from the reviewed owner commit. A
// commit without one renders the unconfigured surfaces; an invalid one stops the operation.
func (op *operation) readFundingConfig(ctx context.Context) (*funding.Config, error) {
	raw, present, err := op.git.optionalBlob(ctx, op.opts.OwnerPath, op.opts.OwnerSHA, funding.ConfigFile)
	if err != nil || !present {
		return nil, err
	}
	cfg, err := funding.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("owner funding configuration %s: %w", funding.ConfigFile, err)
	}
	return cfg, nil
}

// checkFundingOverlay loads the owner's funding document and accepts each owner funding
// surface only when it equals the incorporated source's file (not rendered yet) or that
// file's rendering from the document. Anything else is a hand edit, or a rendering of another
// document, and is refused.
func (op *operation) checkFundingOverlay(ctx context.Context) error {
	cfg, err := op.readFundingConfig(ctx)
	if err != nil {
		return err
	}
	op.funding = cfg
	base, err := op.readSurfaces(ctx, op.opts.SourcePath, op.opts.BaseSHA)
	if err != nil {
		return err
	}
	current, err := op.readSurfaces(ctx, op.opts.OwnerPath, op.opts.OwnerSHA)
	if err != nil {
		return err
	}
	rendered, err := funding.RenderFiles(ctx, base, cfg)
	if err != nil {
		return fmt.Errorf("render incorporated funding surfaces: %w", err)
	}
	for _, path := range funding.SurfacePaths() {
		if !sameTreeFile(current, base, path) && !sameTreeFile(current, rendered, path) {
			return fmt.Errorf("unexpected owner override in %s: render it from %s with praetorctl docs funding", path, funding.ConfigFile)
		}
	}
	return nil
}

// renderOverlay computes everything the candidate must carry: the identity overlay of the
// reviewed source's configuration files and its funding surfaces rendered from the owner's
// funding document.
func (op *operation) renderOverlay(ctx context.Context, next map[string][]byte) error {
	var err error
	if op.expected, err = overlay(next, op.owner); err != nil {
		return err
	}
	surfaces, err := op.readSurfaces(ctx, op.opts.SourcePath, op.opts.SourceSHA)
	if err != nil {
		return err
	}
	if op.surfaces, err = funding.RenderFiles(ctx, surfaces, op.funding); err != nil {
		return fmt.Errorf("render reviewed funding surfaces: %w", err)
	}
	op.fundingChanged = op.fundingChanged[:0]
	for _, path := range funding.SurfacePaths() {
		if !sameTreeFile(surfaces, op.surfaces, path) {
			op.fundingChanged = append(op.fundingChanged, path)
		}
	}
	return nil
}

// renderedSurfaces lists the funding surfaces the overlay writes, in surface order.
func (op *operation) renderedSurfaces() []string {
	paths := make([]string, 0, len(op.surfaces))
	for _, path := range funding.SurfacePaths() {
		if _, ok := op.surfaces[path]; ok {
			paths = append(paths, path)
		}
	}
	return paths
}

// writeSurfaces writes the rendered funding surfaces into the candidate and stages them.
func (op *operation) writeSurfaces(ctx context.Context) error {
	paths := op.renderedSurfaces()
	if len(paths) == 0 {
		return nil
	}
	for _, path := range paths {
		target, err := util.ConfinePath(op.opts.Destination, path)
		if err != nil {
			return err
		}
		// A rendered FUNDING.yml may be the first file of .github/ in the candidate.
		if err := util.MkdirSecure(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := util.WriteFileSecure(target, op.surfaces[path], 0o644); err != nil {
			return err
		}
	}
	_, err := op.git.run(ctx, op.opts.Destination, append([]string{"add", "--"}, paths...)...)
	return err
}

// verifySurfaces requires every funding surface of the candidate tree to be exactly the
// rendering: present with the rendered bytes, or absent when the rendering has no such file.
func (op *operation) verifySurfaces(ctx context.Context, tree string) error {
	candidate, err := op.readSurfaces(ctx, op.opts.Destination, tree)
	if err != nil {
		return err
	}
	for _, path := range funding.SurfacePaths() {
		if !sameTreeFile(candidate, op.surfaces, path) {
			return fmt.Errorf("candidate differs from expected funding overlay: %s", path)
		}
	}
	return nil
}

// sameTreeFile reports whether two file maps agree on path: both absent, or both present with
// equal bytes.
func sameTreeFile(a, b map[string][]byte, path string) bool {
	av, aok := a[path]
	bv, bok := b[path]
	return aok == bok && bytes.Equal(av, bv)
}
