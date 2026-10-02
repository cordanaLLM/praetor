// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package devcontainer

import (
	"cmp"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// reviewedTree is a Praetor checkout the tree assertions read: the committed one this package
// is compiled from, or a scratch checkout after a bump. Every assertion takes the tag and the
// digest it expects from the tree's own pins and never spells either, so a tag move, by
// Renovate or by devcontainer bump, leaves the committed tree passing (#323).
type reviewedTree struct {
	root   string // the checkout
	bundle string // the directory of its generated bundle
}

// committedTree is the checkout the package tests run in.
func committedTree() reviewedTree {
	root := filepath.Join("..", "..")
	return reviewedTree{root: root, bundle: filepath.Join(root, ".devcontainer")}
}

// source returns the tree's reviewed pins, read the way devcontainer bump reads them, and the
// LF-normalized pin source they index.
func (tree reviewedTree) source(t *testing.T) (map[string]reviewedPin, string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(tree.root, filepath.FromSlash(ReviewedPinsFile)))
	if err != nil {
		t.Fatal(err)
	}
	pins, text, _, err := readReviewedPins(data)
	if err != nil {
		t.Fatal(err)
	}
	return pins, text
}

// pins returns the tree's reviewed pins. Reading them is itself the check that each comment
// names a tag and its constant's own repository and digest.
func (tree reviewedTree) pins(t *testing.T) map[string]reviewedPin {
	t.Helper()
	pins, _ := tree.source(t)
	return pins
}

// committedPins returns the pins of the committed tree and requires them to be the defaults
// compiled into this package, so a tagged form derived from a pin names a default's own digest.
func committedPins(t *testing.T) map[string]reviewedPin {
	t.Helper()
	pins := committedTree().pins(t)
	for role, constant := range map[string]string{"base": DefaultBaseImage, "builder": DefaultBuilderImage} {
		if pins[role].image() != constant {
			t.Fatalf("%s pin %s is not the compiled default %s", role, pins[role].reference(), constant)
		}
	}
	return pins
}

// taggedDefault returns the reviewed default of role in the tagged form generation recorded
// before the defaults became digest-only (#333): the reference its pin was reviewed at.
func taggedDefault(t *testing.T, role string) string {
	t.Helper()
	return committedPins(t)[role].reference()
}

// movedPin returns pin at another tag and digest of its repository.
func movedPin(pin reviewedPin, tag, digest string) reviewedPin {
	pin.tag, pin.digest = tag, digest
	return pin
}

// movedTag returns a tag the pin does not carry, for a move to another tag.
func movedTag(pin reviewedPin) string { return "moved-" + pin.tag }

// assertDockerfilesBuildFromThePins binds the tree's pins to the files that build with them: a
// pin nothing checks drifts silently. The development Dockerfile, which devcontainer bump moves
// with the builder pin, also carries the pin's tag; the generated bundle pins digest-only.
func (tree reviewedTree) assertDockerfilesBuildFromThePins(t *testing.T) {
	t.Helper()
	pins := tree.pins(t)
	development := filepath.Join(tree.root, filepath.FromSlash(DevImageDockerfile))
	for _, tc := range []struct{ name, path, role string }{
		{"development image builder", development, "builder"},
		{"recorded bootstrap builder", filepath.Join(tree.bundle, bootstrapDockerfile), "builder"},
		{"recorded bootstrap base", filepath.Join(tree.bundle, bootstrapDockerfile), "base"},
	} {
		data, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		pin := pins[tc.role]
		if err := pinnedDigestDrift(data, pin.reference()); err != nil {
			// A Renovate update moves the pins but never the generated bundle, so this
			// drift names the command that finishes it.
			t.Errorf("%s, %s: %v (move a reviewed default with praetorctl devcontainer bump, which records the replaced pin in prior-images.json and regenerates the bundle)", tc.name, tc.path, err)
		}
		if tagged := ":" + pin.tag + "@" + pin.digest; tc.path == development && !strings.Contains(string(data), tagged) {
			t.Errorf("%s, %s: no FROM line carries the reviewed tag and digest %s", tc.name, tc.path, tagged)
		}
	}
}

// assertRenovateReadsThePins applies manager, the reviewed-pin custom manager of renovate.json,
// to the tree's pin source and requires exactly the tree's pins in source order.
//
// Renovate rewrites the whole match: with autoReplaceGlobalMatch at its default, true, it
// replaces every occurrence of the current digest, which moves the comment and the constant
// together, and every occurrence of the current tag
// (lib/workers/repository/update/branch/auto-replace.ts in renovatebot/renovate). So each
// match must hold its tag once and its digest twice, or an update would leave the pair
// disagreeing or rewrite more than the tag.
func (tree reviewedTree) assertRenovateReadsThePins(t *testing.T, manager renovateCustomManager) {
	t.Helper()
	pins, text := tree.source(t)
	var want, found []string
	for _, pin := range slices.SortedFunc(maps.Values(pins), func(a, b reviewedPin) int { return cmp.Compare(a.start, b.start) }) {
		want = append(want, pin.reference())
	}
	expression := regexp.MustCompile(manager.MatchStrings[0])
	for _, match := range expression.FindAllStringSubmatch(text, -1) {
		group := func(name string) string { return match[expression.SubexpIndex(name)] }
		found = append(found, group("depName")+":"+group("currentValue")+"@"+group("currentDigest"))
		if tags, digests := strings.Count(match[0], group("currentValue")), strings.Count(match[0], group("currentDigest")); tags != 1 || digests != 2 {
			t.Errorf("the match for %s holds its tag %d times and its digest %d times, want 1 and 2", group("depName"), tags, digests)
		}
	}
	if !slices.Equal(found, want) {
		t.Fatalf("Renovate reads %v from %s, want the reviewed pins %v", found, ReviewedPinsFile, want)
	}
}
