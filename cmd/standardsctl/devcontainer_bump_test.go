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

// Regression for #323: replay Renovate's builder digest update, which now moves the reviewed
// pin in bootstrap.go and the FROM line of docker/dev/Dockerfile and leaves the generated
// bundle alone, then finish it with devcontainer bump. The replaced digest joins the prior
// list, the bundle records the new one, and devcontainer verify passes.
func TestDevContainerCLIBumpFinishesARenovateBuilderUpdate(t *testing.T) {
	source, manifest, output := cliBumpCheckout(t)
	_, _, old := util.SplitImageReference(devcontainer.DefaultBuilderImage)
	next := "sha256:" + strings.Repeat("c", 64)
	for _, rel := range []string{devcontainer.ReviewedPinsFile, devcontainer.DevImageDockerfile} {
		replaceInFile(t, filepath.Join(source, filepath.FromSlash(rel)), old, next)
	}
	message, err := captureStdout(t, func() error {
		return runDevContainer([]string{"bump", "--config", manifest, "--output", output, "--source-root", source})
	})
	if err != nil {
		t.Fatalf("bump: %v\n%s", err, message)
	}
	for _, want := range []string{"[PIN KEPT] builder docker.io/library/golang:1.27-alpine@" + next, "records " + devcontainer.DefaultBuilderImage, "[PASS]"} {
		if !strings.Contains(message, want) {
			t.Fatalf("bump output lacks %q:\n%s", want, message)
		}
	}
	if err := runDevContainer([]string{"verify", "--config", manifest, "--output", output}); err != nil {
		t.Fatalf("devcontainer verify after the bump: %v", err)
	}
	if got := recordedBuilder(t, output); got != "docker.io/library/golang@"+next {
		t.Fatalf("bundle records builder %s", got)
	}
	priors, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(devcontainer.PriorImagesFile)))
	if err != nil || !strings.Contains(string(priors), devcontainer.DefaultBuilderImage) {
		t.Fatalf("prior list lacks the replaced builder: %s %v", priors, err)
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
