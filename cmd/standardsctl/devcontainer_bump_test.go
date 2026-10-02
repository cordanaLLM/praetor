// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/devcontainer"
	"github.com/cordanaLLM/praetor/internal/util"
)

// cliBumpCheckout returns a Praetor checkout fixture carrying copies of the committed pin
// source, prior list and development Dockerfile, a manifest and the path of a ready bundle
// the generate command wrote from that checkout.
func cliBumpCheckout(t *testing.T) (source, manifest, output string) {
	t.Helper()
	source = cliBootstrapSource(t)
	repoRoot := filepath.Join("..", "..")
	for _, rel := range []string{devcontainer.ReviewedPinsFile, devcontainer.PriorImagesFile, devcontainer.DevImageDockerfile} {
		data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		writeFixtureFile(t, source, rel, string(data))
	}
	manifest, output = cliBootstrapPaths(t)
	args := []string{"generate", "--config", manifest, "--output", output, "--source-root", source}
	if _, err := captureStdout(t, func() error { return runDevContainer(args) }); err != nil {
		t.Fatal(err)
	}
	return source, manifest, output
}

// replaceInFile rewrites every occurrence of from in the file at path to to.
func replaceInFile(t *testing.T, path, from, to string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), from) {
		t.Fatalf("%s does not hold %s", path, from)
	}
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(data), from, to)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func recordedBuilder(t *testing.T, output string) string {
	t.Helper()
	dc, err := devcontainer.LoadDevContainer(t.Context(), output)
	if err != nil {
		t.Fatal(err)
	}
	return dc.Customizations.Praetor.Bootstrap.BuilderImage
}

// reviewedReferences returns the tagged reference each reviewed default of the Praetor checkout
// at root was reviewed at, by role, read from its pin source. The tests take the tag and the
// digest they start from out of the checkout they bump and spell neither, so they pass on a
// tree whose default has moved to another tag.
func reviewedReferences(t *testing.T, root string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(devcontainer.ReviewedPinsFile)))
	if err != nil {
		t.Fatal(err)
	}
	references, err := devcontainer.ReviewedReferences(data)
	if err != nil {
		t.Fatal(err)
	}
	return references
}

// digestOnly returns the repository@digest form of a tagged reference: the form the
// constants, the prior list and the bundle hold.
func digestOnly(reference string) string {
	repository, _, digest := util.SplitImageReference(reference)
	return repository + "@" + digest
}

// bumpCLI runs devcontainer bump on the checkout at source with extra options and requires
// its output to carry every line of want.
func bumpCLI(t *testing.T, source, manifest, output string, want []string, extra ...string) {
	t.Helper()
	args := append([]string{"bump", "--config", manifest, "--output", output, "--source-root", source}, extra...)
	message, err := captureStdout(t, func() error { return runDevContainer(args) })
	if err != nil {
		t.Fatalf("bump: %v\n%s", err, message)
	}
	for _, line := range want {
		if !strings.Contains(message, line) {
			t.Fatalf("bump output lacks %q:\n%s", line, message)
		}
	}
}

// assertCLIBumped checks the checkout at source after its builder moved from the reference
// before to next: the pin source names next, devcontainer verify passes, the bundle records
// the new digest-only image, and the prior list holds the replaced one.
func assertCLIBumped(t *testing.T, source, manifest, output, before, next string) {
	t.Helper()
	if got := reviewedReferences(t, source)["builder"]; got != next {
		t.Fatalf("pin source names builder %s, want %s", got, next)
	}
	if err := runDevContainer([]string{"verify", "--config", manifest, "--output", output}); err != nil {
		t.Fatalf("devcontainer verify after the bump: %v", err)
	}
	if got := recordedBuilder(t, output); got != digestOnly(next) {
		t.Fatalf("bundle records builder %s, want %s", got, digestOnly(next))
	}
	priors, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(devcontainer.PriorImagesFile)))
	if err != nil || !strings.Contains(string(priors), digestOnly(before)) || strings.Contains(string(priors), digestOnly(next)) {
		t.Fatalf("prior list after replacing %s with %s: %s %v", before, next, priors, err)
	}
}

// finishRenovateBuilderUpdate replays Renovate's update of the builder digest on the checkout
// at source, which moves the reviewed pin in bootstrap.go and the FROM line of
// docker/dev/Dockerfile and leaves the generated bundle alone, then finishes it with
// devcontainer bump. The pin keeps the tag the checkout holds, the replaced digest joins the
// prior list, the bundle records the new one, and devcontainer verify passes.
func finishRenovateBuilderUpdate(t *testing.T, source, manifest, output, digest string) {
	t.Helper()
	before := reviewedReferences(t, source)["builder"]
	repository, tag, old := util.SplitImageReference(before)
	for _, rel := range []string{devcontainer.ReviewedPinsFile, devcontainer.DevImageDockerfile} {
		replaceInFile(t, filepath.Join(source, filepath.FromSlash(rel)), old, digest)
	}
	next := repository + ":" + tag + "@" + digest
	bumpCLI(t, source, manifest, output, []string{"[PIN KEPT] builder " + next, "records " + digestOnly(before), "[PASS]"})
	assertCLIBumped(t, source, manifest, output, before, next)
}

// Regression for #323: a Renovate builder digest update is finished by devcontainer bump.
func TestDevContainerCLIBumpFinishesARenovateBuilderUpdate(t *testing.T) {
	source, manifest, output := cliBumpCheckout(t)
	finishRenovateBuilderUpdate(t, source, manifest, output, "sha256:"+strings.Repeat("c", 64))
}

// Regression for the tag move (#323): Renovate proposes tag updates into the reviewed group,
// and a tag move must be one command like a digest move. Replay a move of the builder to
// another tag through the command in a scratch checkout: the pin source and the development
// Dockerfile carry the new tag, the replaced image joins the prior list, and devcontainer
// verify passes. The checkout then stands where the committed tree stands after a tag move,
// and the Renovate replay of TestDevContainerCLIBumpFinishesARenovateBuilderUpdate passes on
// it with the same assertions, under the moved tag.
func TestDevContainerCLIBumpMovesTheBuilderTag(t *testing.T) {
	source, manifest, output := cliBumpCheckout(t)
	before := reviewedReferences(t, source)["builder"]
	repository, tag, _ := util.SplitImageReference(before)
	moved := ":moved-" + tag + "@sha256:" + strings.Repeat("e", 64)
	next := repository + moved
	want := []string{"[PIN MOVED] builder " + before + " -> " + next, "records " + digestOnly(before), "[PASS]"}
	bumpCLI(t, source, manifest, output, want, "--builder-image", next)
	assertCLIBumped(t, source, manifest, output, before, next)
	dockerfile, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(devcontainer.DevImageDockerfile)))
	if err != nil || !strings.Contains(string(dockerfile), moved) {
		t.Fatalf("%s does not build from %s: %v", devcontainer.DevImageDockerfile, moved, err)
	}
	finishRenovateBuilderUpdate(t, source, manifest, output, "sha256:"+strings.Repeat("f", 64))
	if got := reviewedReferences(t, source)["builder"]; !strings.HasPrefix(got, repository+":moved-"+tag+"@") {
		t.Fatalf("the Renovate update lost the moved tag: %s", got)
	}
}

// Boundary for the tag move (#323): a floating tag and a patch tag of one image share a digest.
// The command moves the builder to another tag of the digest it already pins: the pin source
// and the development Dockerfile carry the new tag, nothing joins the prior list, and
// devcontainer verify passes.
func TestDevContainerCLIBumpMovesTheBuilderTagUnderTheSameDigest(t *testing.T) {
	source, manifest, output := cliBumpCheckout(t)
	before := reviewedReferences(t, source)["builder"]
	repository, tag, digest := util.SplitImageReference(before)
	moved := ":moved-" + tag + "@" + digest
	next := repository + moved
	priorsFile := filepath.Join(source, filepath.FromSlash(devcontainer.PriorImagesFile))
	priors, err := os.ReadFile(priorsFile)
	if err != nil {
		t.Fatal(err)
	}
	bumpCLI(t, source, manifest, output, []string{"[PIN MOVED] builder " + before + " -> " + next, "[PASS]"}, "--builder-image", next)
	if after, err := os.ReadFile(priorsFile); err != nil || string(after) != string(priors) {
		t.Fatalf("a tag-only move changed %s: %v", devcontainer.PriorImagesFile, err)
	}
	if got := reviewedReferences(t, source)["builder"]; got != next {
		t.Fatalf("builder pin after the tag move: %s", got)
	}
	dockerfile, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(devcontainer.DevImageDockerfile)))
	if err != nil || !strings.Contains(string(dockerfile), moved) {
		t.Fatalf("%s does not build from %s: %v", devcontainer.DevImageDockerfile, moved, err)
	}
	if err := runDevContainer([]string{"verify", "--config", manifest, "--output", output}); err != nil {
		t.Fatalf("bundle does not verify after the tag move: %v", err)
	}
}

// The update Renovate used to open edits the generated Dockerfile.praetor alone. Verification
// rejects it and names the commands that repair it; devcontainer bump regenerates the bundle
// from the unchanged pins, and verification passes again.
func TestDevContainerCLIBundleEditNamesTheBump(t *testing.T) {
	source, manifest, output := cliBumpCheckout(t)
	_, _, old := util.SplitImageReference(devcontainer.DefaultBuilderImage)
	replaceInFile(t, filepath.Join(filepath.Dir(output), "Dockerfile.praetor"), old, "sha256:"+strings.Repeat("d", 64))
	err := runDevContainer([]string{"verify", "--config", manifest, "--output", output})
	if err == nil || !strings.Contains(err.Error(), "praetorctl devcontainer bump") || !strings.Contains(err.Error(), "praetorctl devcontainer generate") {
		t.Fatalf("edited bundle verification error does not name the repair: %v", err)
	}
	if _, err := captureStdout(t, func() error {
		return runDevContainer([]string{"bump", "--config", manifest, "--output", output, "--source-root", source})
	}); err != nil {
		t.Fatal(err)
	}
	if err := runDevContainer([]string{"verify", "--config", manifest, "--output", output}); err != nil {
		t.Fatalf("bump did not repair the bundle: %v", err)
	}
	if got := recordedBuilder(t, output); got != devcontainer.DefaultBuilderImage {
		t.Fatalf("bundle records builder %s", got)
	}
}

// Negative and boundary: bump refuses --force and --verify, a tagless pin, and, without
// --source-root, a configuration directory that is no Praetor checkout; none writes a file.
func TestDevContainerCLIBumpRefusals(t *testing.T) {
	source, manifest, output := cliBumpCheckout(t)
	before, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		args   []string
		reason string
	}{
		"force":          {[]string{"bump", "--force", "--source-root", source}, "--force are not accepted"},
		"verify":         {[]string{"bump", "--verify", "--source-root", source}, "--force are not accepted"},
		"tagless pin":    {[]string{"bump", "--source-root", source, "--builder-image", devcontainer.DefaultBuilderImage}, "with a tag"},
		"no source root": {[]string{"bump"}, devcontainer.ReviewedPinsFile},
	} {
		args := append(tc.args, "--config", manifest, "--output", output)
		if _, err := captureStdout(t, func() error { return runDevContainer(args) }); err == nil || !strings.Contains(err.Error(), tc.reason) {
			t.Errorf("%s: error %v does not name %q", name, err, tc.reason)
		}
	}
	if err := runDevContainer([]string{"unknown"}); err == nil || !strings.Contains(err.Error(), "bump") {
		t.Fatalf("unknown action error does not list bump: %v", err)
	}
	after, err := os.ReadFile(output)
	if err != nil || string(after) != string(before) {
		t.Fatal("a refused bump changed the bundle")
	}
}
