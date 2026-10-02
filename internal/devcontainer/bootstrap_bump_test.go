// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package devcontainer

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// bumpCheckout is a Praetor checkout fixture: a capturable source root carrying copies of the
// committed pin source, prior list and development Dockerfile, and a ready bundle generated
// from it with the reviewed defaults. Its pins are whatever the committed tree holds, so every
// test reads the tag and digest it starts from out of the checkout and spells neither.
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

// tree returns the checkout as the tree the committed-tree assertions read.
func (c bumpCheckout) tree() reviewedTree {
	return reviewedTree{root: c.root, bundle: filepath.Dir(c.output)}
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

// priors returns the checkout's prior-default list.
func (c bumpCheckout) priors(t *testing.T) PriorImages {
	t.Helper()
	priors, err := ParsePriorImages([]byte(c.read(t, PriorImagesFile)))
	if err != nil {
		t.Fatal(err)
	}
	return priors
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

// replayRenovate edits the checkout as Renovate's grouped update of the builder from one pin
// to another does: the custom manager moves the tag and the digest of the reviewed comment and
// the digest of the constant in bootstrap.go, the dockerfile manager the tag and the digest of
// the development Dockerfile's FROM line, and the generated bundle is left alone.
func (c bumpCheckout) replayRenovate(t *testing.T, from, to reviewedPin) {
	t.Helper()
	update := strings.NewReplacer(":"+from.tag+"@"+from.digest, ":"+to.tag+"@"+to.digest, from.digest, to.digest)
	for _, rel := range []string{ReviewedPinsFile, DevImageDockerfile} {
		writeBootstrapFile(t, c.root, rel, update.Replace(c.read(t, rel)))
	}
}

func withBuilder(options BumpOptions, image string) BumpOptions {
	options.BuilderImage = image
	return options
}

// assertBumped checks a checkout whose builder pin moved from the pins in before to want. The
// pin source holds want beside the unmoved base; the checkout passes the assertions the
// committed tree is held to, so the tree a bump leaves is one the package tests accept; the
// prior list gained the replaced builder and not the new one; and the bundle records the new
// digest-only image and verifies.
func assertBumped(t *testing.T, checkout bumpCheckout, before map[string]reviewedPin, want reviewedPin) {
	t.Helper()
	tree := checkout.tree()
	pins := tree.pins(t)
	if pins["builder"].reference() != want.reference() || pins["base"].reference() != before["base"].reference() {
		t.Fatalf("pins after bump: builder %s, base %s; want %s and %s", pins["builder"].reference(), pins["base"].reference(), want.reference(), before["base"].reference())
	}
	tree.assertDockerfilesBuildFromThePins(t)
	tree.assertRenovateReadsThePins(t, readRenovate(t).reviewedPinManager(t))
	if priors := checkout.priors(t); !slices.Contains(priors.Builder, before["builder"].image()) || slices.Contains(priors.Builder, want.image()) {
		t.Fatalf("prior builders after bump: %v", priors.Builder)
	}
	if recorded := checkout.recorded(t); recorded.BuilderImage != want.image() || recorded.BaseImage != before["base"].image() {
		t.Fatalf("bundle records %s / %s", recorded.BuilderImage, recorded.BaseImage)
	}
	if err := Verify(t.Context(), checkout.output, mustBaseContainer(t)); err != nil {
		t.Fatalf("bundle does not verify after the bump: %v", err)
	}
}

// Positive: a selected builder pin moves the comment, the constant and the Dockerfile, the
// replaced pin joins the prior list, and the regenerated bundle records the new digest-only
// image and verifies.
func TestBumpMovesTheBuilderPinAndRegenerates(t *testing.T) {
	checkout := newBumpCheckout(t)
	before := checkout.tree().pins(t)
	next := movedPin(before["builder"], before["builder"].tag, fixtureDigest(7))
	changes, err := Bump(t.Context(), withBuilder(checkout.options(t), next.reference()))
	if err != nil {
		t.Fatal(err)
	}
	builder := changes[slices.IndexFunc(changes, func(c BumpChange) bool { return c.Role == "builder" })]
	if builder.From != before["builder"].reference() || builder.To != next.reference() ||
		!slices.Equal(builder.Retired, []string{before["builder"].image()}) || !strings.HasPrefix(builder.String(), "[PIN MOVED] builder ") {
		t.Fatalf("builder change %+v (%s)", builder, builder)
	}
	assertBumped(t, checkout, before, next)
}

// finishRenovateUpdate replays Renovate's update of the checkout's builder pin to next, which
// moves the pin and the Dockerfile but not the bundle, and finishes it with a bump that
// selects no image: the image the bundle still records joins the prior list and the bundle is
// regenerated. A second bump changes nothing.
func finishRenovateUpdate(t *testing.T, checkout bumpCheckout, next reviewedPin) {
	t.Helper()
	before := checkout.tree().pins(t)
	checkout.replayRenovate(t, before["builder"], next)
	changes, err := Bump(t.Context(), checkout.options(t))
	if err != nil {
		t.Fatal(err)
	}
	assertBumped(t, checkout, before, next)
	for _, change := range changes {
		if change.From != change.To || (change.Role == "builder") != (len(change.Retired) == 1) {
			t.Fatalf("change %+v", change)
		}
	}
	finished := checkout.snapshot(t)
	again, err := Bump(t.Context(), checkout.options(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range again {
		if len(change.Retired) != 0 || !strings.HasPrefix(change.String(), "[PIN KEPT] ") {
			t.Fatalf("repeated bump reported %s", change)
		}
	}
	assertSnapshot(t, checkout, finished)
}

// Positive: a bump without a selected image finishes a Renovate update, whether Renovate moved
// the digest under the reviewed tag or the tag with it. Renovate proposes both into the
// reviewed group.
func TestBumpFinishesARenovateUpdate(t *testing.T) {
	for name, tag := range map[string]func(reviewedPin) string{
		"digest update": func(pin reviewedPin) string { return pin.tag },
		"tag update":    movedTag,
	} {
		t.Run(name, func(t *testing.T) {
			checkout := newBumpCheckout(t)
			builder := checkout.tree().pins(t)["builder"]
			finishRenovateUpdate(t, checkout, movedPin(builder, tag(builder), fixtureDigest(8)))
		})
	}
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

// Boundary, the tag move: one bump to another tag rewrites the tag in the comment and the
// Dockerfile and leaves a checkout that passes the assertions the committed tree is held to
// (assertBumped). The checkout then stands where the committed tree stands after a tag move,
// and the Renovate replay TestBumpFinishesARenovateUpdate runs on a fresh checkout passes on
// it under the moved tag. A move back to an earlier digest takes it out of the prior list.
func TestBumpMovesTheTagAndBackToAPriorDigest(t *testing.T) {
	checkout := newBumpCheckout(t)
	before := checkout.tree().pins(t)
	moved := movedPin(before["builder"], movedTag(before["builder"]), fixtureDigest(5))
	if _, err := Bump(t.Context(), withBuilder(checkout.options(t), moved.reference())); err != nil {
		t.Fatal(err)
	}
	assertBumped(t, checkout, before, moved)
	updated := movedPin(moved, moved.tag, fixtureDigest(6))
	finishRenovateUpdate(t, checkout, updated)
	if _, err := Bump(t.Context(), withBuilder(checkout.options(t), before["builder"].reference())); err != nil {
		t.Fatal(err)
	}
	priors := checkout.priors(t)
	if slices.Contains(priors.Builder, before["builder"].image()) || !slices.Contains(priors.Builder, moved.image()) || !slices.Contains(priors.Builder, updated.image()) {
		t.Fatalf("prior builders after moving back: %v", priors.Builder)
	}
	if pins := checkout.tree().pins(t); pins["builder"].reference() != before["builder"].reference() {
		t.Fatalf("builder pin after moving back: %s", pins["builder"].reference())
	}
}

// Boundary: the prior list only grows, and each role's list is bounded (maxPriorImages). A
// bump that fills the last free entry succeeds and keeps every earlier entry. One that would
// pass the bound is refused before anything is written, names the bound and the remedy, and
// prunes nothing: a bundle that recorded a pruned image would no longer be refreshed.
func TestBumpRefusesPastThePriorBoundWithoutPruning(t *testing.T) {
	for name, listed := range map[string]int{"last free entry": maxPriorImages - 1, "at the bound": maxPriorImages} {
		t.Run(name, func(t *testing.T) {
			checkout := newBumpCheckout(t)
			builder := checkout.tree().pins(t)["builder"]
			priors := PriorImages{Base: checkout.priors(t).Base}
			for index := range listed {
				priors.Builder = append(priors.Builder, builder.repository+"@"+fixtureDigest(1000+index))
			}
			rendered, err := RenderPriorImages(priors)
			if err != nil {
				t.Fatal(err)
			}
			writeBootstrapFile(t, checkout.root, PriorImagesFile, string(rendered))
			before := checkout.snapshot(t)
			_, err = Bump(t.Context(), withBuilder(checkout.options(t), movedPin(builder, builder.tag, fixtureDigest(7)).reference()))
			if listed < maxPriorImages {
				if got := checkout.priors(t).Builder; err != nil || !slices.Equal(got, append(priors.Builder, builder.image())) {
					t.Fatalf("bump into the last free entry: %v, %d prior builders", err, len(got))
				}
				return
			}
			for _, want := range []string{"more than the bound of " + strconv.Itoa(maxPriorImages), "never pruned", "raise maxPriorImages in " + reviewedImagesSource} {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("bump past the bound: error %v does not name %q", err, want)
				}
			}
			assertSnapshot(t, checkout, before)
		})
	}
}

// Negative: a tagless reference, another repository, a checkout without the pin source, an
// output without a ready bundle and a missing context are refused before anything is written.
func TestBumpRefusesBeforeWriting(t *testing.T) {
	checkout := newBumpCheckout(t)
	before := checkout.snapshot(t)
	builder := checkout.tree().pins(t)["builder"]
	unavailable := filepath.Join(t.TempDir(), "devcontainer.json")
	writeBootstrapFile(t, filepath.Dir(unavailable), "devcontainer.json", "{\"image\":\"owned:tag\"}\n")
	for name, tc := range map[string]struct {
		options BumpOptions
		reason  string
	}{
		"tagless":          {withBuilder(checkout.options(t), builder.repository+"@"+fixtureDigest(4)), "with a tag"},
		"other repository": {withBuilder(checkout.options(t), "registry.example/golang:"+builder.tag+"@"+fixtureDigest(4)), "within its repository"},
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
	builder := checkout.tree().pins(t)["builder"]
	writeBootstrapFile(t, checkout.root, "cmd/standardsctl/main.go", "package main\nimport _ \"github.com/cordanaLLM/praetor/internal/absent\"\nfunc main() {}\n")
	_, err := Bump(t.Context(), withBuilder(checkout.options(t), movedPin(builder, builder.tag, fixtureDigest(6)).reference()))
	if err == nil || !strings.Contains(err.Error(), "restored the files it wrote") {
		t.Fatalf("failed bump reported %v", err)
	}
	assertSnapshot(t, checkout, before)
}
