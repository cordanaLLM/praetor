// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package devcontainer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rerecordDockerfile rewrites the ready bundle at path as a generation that rendered its
// Dockerfile as dockerfile would have written it: Dockerfile.praetor holds dockerfile and the
// specification records its digest.
func rerecordDockerfile(t *testing.T, path string, dockerfile func(*BootstrapSpec) string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dc, err := decodeManagedConfig(data, path)
	if err != nil {
		t.Fatal(err)
	}
	spec := (&Bundle{Config: dc}).Spec()
	text := dockerfile(spec)
	spec.DockerfileSHA256 = bootstrapDigest([]byte(text))
	rendered, err := Render(dc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, rendered, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), bootstrapDockerfile), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// pipedBundle writes a ready bundle with the adopter's images as generation wrote it before
// #351, with the piped Dockerfile rendering, and returns its config path.
func pipedBundle(t *testing.T) string {
	t.Helper()
	path := writeRecordedBundle(t, BootstrapOptions{SourceRoot: bootstrapSourceFixture(t), BaseImage: adopterBase, BuilderImage: adopterBuilder})
	rerecordDockerfile(t, path, func(spec *BootstrapSpec) string { return renderBootstrapDockerfileAs(spec, dockerfilePiped) })
	return path
}

// Positive: regenerating a bundle recorded with the piped rendering keeps the adopter's images,
// as it does for a bundle of the current rendering, instead of reading the specification as
// invalid and falling back to the reviewed defaults without a note.
func TestRecordedPipedRenderingKeepsAdopterImages(t *testing.T) {
	options, notes, err := InheritRecordedImages(t.Context(), pipedBundle(t), BootstrapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if options.BaseImage != adopterBase || options.BuilderImage != adopterBuilder || len(notes) != 2 {
		t.Fatalf("images recorded with the piped rendering were not inherited: %+v %+v", options, notes)
	}
}

// Positive: --force without a source root still refuses to replace a ready bundle recorded with
// the piped rendering by an unavailable placeholder.
func TestRecordedPipedRenderingKeepsReadyBootstrapGuard(t *testing.T) {
	path := pipedBundle(t)
	unavailable, err := PrepareBundle(t.Context(), "adopted/app", []string{"framework"}, nil, BootstrapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	err = WriteBundle(t.Context(), path, unavailable, true)
	if err == nil || !strings.Contains(err.Error(), "refusing to replace the ready DevContainer bootstrap") {
		t.Fatalf("ready bundle of the piped rendering replaced by a placeholder: %v", err)
	}
}

// Negative: verification requires the current rendering, so a bundle of the piped rendering
// is reported for regeneration.
func TestVerifyRefusesThePipedRendering(t *testing.T) {
	err := Verify(t.Context(), pipedBundle(t), mustBaseContainer(t))
	if err == nil || !strings.Contains(err.Error(), "bootstrap Dockerfile identity differs from its recorded inputs") {
		t.Fatalf("verification accepted the piped rendering: %v", err)
	}
}

// Boundary: a recorded digest that is no rendering's, earlier or current, records no choice,
// so the options stay as given.
func TestRecordedUnknownRenderingRecordsNoChoice(t *testing.T) {
	path := writeRecordedBundle(t, BootstrapOptions{SourceRoot: bootstrapSourceFixture(t), BaseImage: adopterBase, BuilderImage: adopterBuilder})
	rerecordDockerfile(t, path, func(spec *BootstrapSpec) string { return renderBootstrapDockerfile(spec) + "RUN true\n" })
	options, notes, err := InheritRecordedImages(t.Context(), path, BootstrapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if options.BaseImage != "" || options.BuilderImage != "" || len(notes) != 0 {
		t.Fatalf("a specification of no known rendering decided the images: %+v %+v", options, notes)
	}
}
