// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package devcontainer

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// bumpCheckout is a Praetor checkout fixture: a capturable source root carrying copies of the
// committed pin source, prior list and development Dockerfile, and a ready bundle generated
// from it with the reviewed defaults.
type bumpCheckout struct {
	root, output string
}

func newBumpCheckout(t *testing.T) bumpCheckout {
	t.Helper()
	root := bootstrapSourceFixture(t)
	for rel, source := range map[string]string{
		ReviewedPinsFile: "bootstrap.go", PriorImagesFile: priorImagesName,
		DevImageDockerfile: filepath.Join("..", "..", "docker", "dev", "Dockerfile"),
	} {
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		writeBootstrapFile(t, root, rel, string(data))
	}
	output := filepath.Join(t.TempDir(), ".devcontainer", "devcontainer.json")
	bundle, err := PrepareBundle(t.Context(), "adopted/app", []string{"framework"}, nil, BootstrapOptions{SourceRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteBundle(t.Context(), output, bundle, false); err != nil {
		t.Fatal(err)
	}
	return bumpCheckout{root: root, output: output}
}

func (c bumpCheckout) options(t *testing.T) BumpOptions {
	return BumpOptions{SourceRoot: c.root, Output: c.output, Name: "adopted/app", Profiles: []string{"framework"}, Expected: mustBaseContainer(t)}
}

func (c bumpCheckout) read(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(c.root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func (c bumpCheckout) recorded(t *testing.T) *BootstrapSpec {
	t.Helper()
	dc, err := LoadDevContainer(t.Context(), c.output)
	if err != nil {
		t.Fatal(err)
	}
	return dc.Customizations.Praetor.Bootstrap
}

// snapshot returns every file a bump may write, so a refused bump can be shown to leave all.
func (c bumpCheckout) snapshot(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, rel := range []string{ReviewedPinsFile, PriorImagesFile, DevImageDockerfile} {
		files[rel] = c.read(t, rel)
	}
	entries, err := os.ReadDir(filepath.Dir(c.output))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(filepath.Dir(c.output), entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[entry.Name()] = string(data)
	}
	return files
}

// replayRenovate edits the checkout as Renovate's grouped update does: the custom manager
// moves the reviewed comment and constant of bootstrap.go, the dockerfile manager the FROM
// line of the development Dockerfile, and the generated bundle is left alone.
func (c bumpCheckout) replayRenovate(t *testing.T, from, to string) {
	t.Helper()
	for _, rel := range []string{ReviewedPinsFile, DevImageDockerfile} {
		writeBootstrapFile(t, c.root, rel, strings.ReplaceAll(c.read(t, rel), from, to))
	}
}

func digestOf(image string) string {
	_, _, digest := util.SplitImageReference(image)
	return digest
}

// Positive: a selected builder pin moves the comment, the constant and the Dockerfile, the
// replaced pin joins the prior list, and the regenerated bundle records the new digest-only
// image and verifies.
func TestBumpMovesTheBuilderPinAndRegenerates(t *testing.T) {
	checkout := newBumpCheckout(t)
	next := "docker.io/library/golang:1.27-alpine@" + fixtureDigest(7)
	changes, err := Bump(t.Context(), withBuilder(checkout.options(t), next))
	if err != nil {
		t.Fatal(err)
	}
	builder := changes[slices.IndexFunc(changes, func(c BumpChange) bool { return c.Role == "builder" })]
	if builder.To != next || !slices.Equal(builder.Retired, []string{DefaultBuilderImage}) || !strings.HasPrefix(builder.String(), "[PIN MOVED] builder ") {
		t.Fatalf("builder change %+v (%s)", builder, builder)
	}
	assertBumped(t, checkout, fixtureDigest(7), "1.27-alpine")
	if recorded := checkout.recorded(t); recorded.BuilderImage != "docker.io/library/golang@"+fixtureDigest(7) || recorded.BaseImage != DefaultBaseImage {
		t.Fatalf("bundle records %s / %s", recorded.BuilderImage, recorded.BaseImage)
	}
}

func withBuilder(options BumpOptions, image string) BumpOptions {
	options.BuilderImage = image
	return options
}

// assertBumped checks the checkout's pin, Dockerfile and prior list after a builder bump to
// digest under tag, and that the bundle verifies.
func assertBumped(t *testing.T, checkout bumpCheckout, digest, tag string) {
	t.Helper()
	text, _, err := util.NormalizeLineEndingsStrict(checkout.read(t, ReviewedPinsFile))
	if err != nil {
		t.Fatal(err)
	}
	pins, err := parseReviewedPins(text)
	if err != nil || pins["builder"].digest != digest || pins["builder"].tag != tag || pins["base"].image() != DefaultBaseImage {
		t.Fatalf("pins after bump: %+v %v", pins, err)
	}
	if err := pinnedDigestDrift([]byte(checkout.read(t, DevImageDockerfile)), "golang:"+tag+"@"+digest); err != nil {
		t.Fatal(err)
	}
	priors, err := ParsePriorImages([]byte(checkout.read(t, PriorImagesFile)))
	if err != nil || !slices.Contains(priors.Builder, DefaultBuilderImage) || slices.Contains(priors.Builder, "docker.io/library/golang@"+digest) {
		t.Fatalf("prior builders after bump: %v %v", priors.Builder, err)
	}
	if err := Verify(t.Context(), checkout.output, mustBaseContainer(t)); err != nil {
		t.Fatalf("bundle does not verify after the bump: %v", err)
	}
}

// Positive: Renovate's update moves the pins and the Dockerfile but not the bundle. A bump
// without a selected image finishes it: the image the bundle still records joins the prior
// list and the bundle is regenerated. A second bump changes nothing.
func TestBumpFinishesARenovateUpdate(t *testing.T) {
	checkout := newBumpCheckout(t)
	checkout.replayRenovate(t, digestOf(DefaultBuilderImage), fixtureDigest(8))
	changes, err := Bump(t.Context(), checkout.options(t))
	if err != nil {
		t.Fatal(err)
	}
	assertBumped(t, checkout, fixtureDigest(8), "1.27-alpine")
	for _, change := range changes {
		if change.From != change.To || (change.Role == "builder") != (len(change.Retired) == 1) {
			t.Fatalf("change %+v", change)
		}
	}
	before := checkout.snapshot(t)
	again, err := Bump(t.Context(), checkout.options(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range again {
		if len(change.Retired) != 0 || !strings.HasPrefix(change.String(), "[PIN KEPT] ") {
			t.Fatalf("repeated bump reported %s", change)
		}
	}
	assertSnapshot(t, checkout, before)
}

func assertSnapshot(t *testing.T, checkout bumpCheckout, want map[string]string) {
	t.Helper()
	got := checkout.snapshot(t)
	for name, data := range want {
		if got[name] != data {
			t.Fatalf("%s changed", name)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("files %d, want %d", len(got), len(want))
	}
}

// Boundary: a move to a new tag rewrites the tag in the comment and the Dockerfile, and a move
// back to an earlier digest takes it out of the prior list again.
func TestBumpMovesTheTagAndBackToAPriorDigest(t *testing.T) {
	checkout := newBumpCheckout(t)
	if _, err := Bump(t.Context(), withBuilder(checkout.options(t), "docker.io/library/golang:1.28-alpine@"+fixtureDigest(5))); err != nil {
		t.Fatal(err)
	}
	assertBumped(t, checkout, fixtureDigest(5), "1.28-alpine")
	if _, err := Bump(t.Context(), withBuilder(checkout.options(t), strings.Replace(DefaultBuilderImage, "@", ":1.27-alpine@", 1))); err != nil {
		t.Fatal(err)
	}
	priors, err := ParsePriorImages([]byte(checkout.read(t, PriorImagesFile)))
	if err != nil || slices.Contains(priors.Builder, DefaultBuilderImage) || !slices.Contains(priors.Builder, "docker.io/library/golang@"+fixtureDigest(5)) {
		t.Fatalf("prior builders after moving back: %v %v", priors.Builder, err)
	}
}

// Negative: a tagless reference, another repository, a checkout without the pin source, an
// output without a ready bundle and a missing context are refused before anything is written.
func TestBumpRefusesBeforeWriting(t *testing.T) {
	checkout := newBumpCheckout(t)
	before := checkout.snapshot(t)
	unavailable := filepath.Join(t.TempDir(), "devcontainer.json")
	writeBootstrapFile(t, filepath.Dir(unavailable), "devcontainer.json", "{\"image\":\"owned:tag\"}\n")
	for name, tc := range map[string]struct {
		options BumpOptions
		reason  string
	}{
		"tagless":          {withBuilder(checkout.options(t), "docker.io/library/golang@"+fixtureDigest(4)), "with a tag"},
		"other repository": {withBuilder(checkout.options(t), "registry.example/golang:1.27-alpine@"+fixtureDigest(4)), "within its repository"},
		"no pin source":    {BumpOptions{SourceRoot: t.TempDir(), Output: checkout.output, Expected: mustBaseContainer(t)}, ReviewedPinsFile},
		"custom config":    {BumpOptions{SourceRoot: checkout.root, Output: unavailable, Expected: mustBaseContainer(t)}, "no ready Praetor bundle"},
		"no declaration":   {BumpOptions{SourceRoot: checkout.root, Output: checkout.output}, "declared configuration"},
	} {
		if _, err := Bump(t.Context(), tc.options); err == nil || !strings.Contains(err.Error(), tc.reason) {
			t.Errorf("%s: error %v does not name %q", name, err, tc.reason)
		}
	}
	var absent context.Context
	if _, err := Bump(absent, checkout.options(t)); err == nil {
		t.Error("nil context accepted")
	}
	assertSnapshot(t, checkout, before)
}

// Boundary: a bump whose regeneration fails after the source edits were written restores
// every file it wrote.
func TestBumpRestoresWhenRegenerationFails(t *testing.T) {
	checkout := newBumpCheckout(t)
	before := checkout.snapshot(t)
	writeBootstrapFile(t, checkout.root, "cmd/standardsctl/main.go", "package main\nimport _ \"github.com/cordanaLLM/praetor/internal/absent\"\nfunc main() {}\n")
	_, err := Bump(t.Context(), withBuilder(checkout.options(t), "docker.io/library/golang:1.27-alpine@"+fixtureDigest(6)))
	if err == nil || !strings.Contains(err.Error(), "restored the files it wrote") {
		t.Fatalf("failed bump reported %v", err)
	}
	assertSnapshot(t, checkout, before)
}
