package devcontainer

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Issue #333: the reviewed defaults are digest-only, repository@sha256:<digest>, because
// @devcontainers/cli 0.89.0 refuses repository:tag@sha256:<digest> during registry
// inspection. These fixtures are the same defaults in the tagged form generation recorded
// before that change, at the tags named in the comments beside the constants.
var (
	taggedDefaultBase    = strings.Replace(DefaultBaseImage, "@", ":ubuntu26.04@", 1)
	taggedDefaultBuilder = strings.Replace(DefaultBuilderImage, "@", ":1.27-alpine@", 1)
)

// reviewedAt matches a "Reviewed at <reference>" comment line and the constant its comment
// block documents.
var reviewedAt = regexp.MustCompile(`// Reviewed at (\S+)\n(?:\t//[^\n]*\n)*\t(\w+)\s*=`)

// The comment beside each default keeps the full reference it was reviewed at, which the
// repository pin scan (TestRepositoryPinsOneDigestPerImageTag in internal/supplychain)
// holds to one digest per tag. That reference must name the constant's own repository and
// digest under a tag, so moving a default cannot leave its comment on the old digest.
func TestReviewedDefaultCommentsNameTheirDigest(t *testing.T) {
	source, err := os.ReadFile("bootstrap.go")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	for _, match := range reviewedAt.FindAllStringSubmatch(strings.ReplaceAll(string(source), "\r\n", "\n"), -1) {
		found[match[2]] = match[1]
	}
	want := map[string]string{"DefaultBuilderImage": taggedDefaultBuilder, "DefaultBaseImage": taggedDefaultBase}
	if len(found) != len(want) {
		t.Fatalf("reviewed-at comments %v, want one per default", found)
	}
	for name, reference := range want {
		if found[name] != reference {
			t.Fatalf("%s comment names %q, want %q", name, found[name], reference)
		}
	}
}

// Positive: each reviewed default is repository@digest without a tag, a first generation
// records and builds on exactly that form, and the bundle verifies.
func TestReviewedDefaultsRenderDigestOnly(t *testing.T) {
	for _, image := range []string{DefaultBuilderImage, DefaultBaseImage} {
		repository, tag, digest := util.SplitImageReference(image)
		if tag != "" || digest == "" || repository+"@"+digest != image {
			t.Fatalf("reviewed default %s is not repository@digest (tag %q)", image, tag)
		}
	}
	path := writeRecordedBundle(t, BootstrapOptions{SourceRoot: bootstrapSourceFixture(t)})
	if err := Verify(t.Context(), path, mustBaseContainer(t)); err != nil {
		t.Fatal(err)
	}
	dc, err := LoadDevContainer(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if spec := dc.Customizations.Praetor.Bootstrap; spec.BaseImage != DefaultBaseImage || spec.BuilderImage != DefaultBuilderImage {
		t.Fatalf("recorded images %s / %s, want the digest-only defaults", spec.BuilderImage, spec.BaseImage)
	}
	dockerfile, err := os.ReadFile(filepath.Join(filepath.Dir(path), bootstrapDockerfile))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"\nFROM " + DefaultBuilderImage + " AS praetor_build\n", "\nFROM " + DefaultBaseImage + "\n"} {
		if !strings.Contains(string(dockerfile), line) {
			t.Fatalf("Dockerfile lacks %q:\n%s", line, dockerfile)
		}
	}
}

// Boundary: a bundle recorded before #333 holds the current defaults in tagged form. It
// still verifies, and a regeneration refreshes both images to the digest-only defaults,
// says so, and verifies again.
func TestInheritRecordedImagesRefreshesTaggedCurrentDefault(t *testing.T) {
	path := writeRecordedBundle(t, BootstrapOptions{SourceRoot: bootstrapSourceFixture(t), BaseImage: taggedDefaultBase, BuilderImage: taggedDefaultBuilder})
	if err := Verify(t.Context(), path, mustBaseContainer(t)); err != nil {
		t.Fatalf("bundle recorded with tagged defaults no longer verifies: %v", err)
	}
	options, notes, err := InheritRecordedImages(t.Context(), path, BootstrapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if options.BaseImage != DefaultBaseImage || options.BuilderImage != DefaultBuilderImage || len(notes) != 2 {
		t.Fatalf("tagged defaults not refreshed to digest-only: %+v %+v", options, notes)
	}
	for role, recorded := range map[string]string{"base": taggedDefaultBase, "builder": taggedDefaultBuilder} {
		note := noteFor(t, notes, role)
		want := "[RECORDED IMAGE REFRESHED] " + role + " image " + recorded + " -> reviewed default " + note.Default +
			"; devcontainer generate " + note.Flag + " " + recorded + " keeps it"
		if note.Action != ImageRefreshed || note.Selected != note.Default || note.String() != want {
			t.Fatalf("%s note = %q, want %q", role, note, want)
		}
	}
	options.SourceRoot = bootstrapSourceFixture(t)
	bundle, err := PrepareBundle(t.Context(), "adopted/app", []string{"framework"}, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteBundle(t.Context(), path, bundle, true); err != nil {
		t.Fatal(err)
	}
	if err := Verify(t.Context(), path, mustBaseContainer(t)); err != nil {
		t.Fatalf("refreshed bundle does not verify: %v", err)
	}
	if _, notes, err := InheritRecordedImages(t.Context(), path, BootstrapOptions{}); err != nil || len(notes) != 0 {
		t.Fatalf("digest-only defaults reported again: %+v %v", notes, err)
	}
}

// Negative: the current default's digest under another repository, the short Docker Hub
// spelling of the builder and another digest of a default repository are operator choices,
// so a regeneration keeps them as recorded.
func TestInheritRecordedImagesKeepsOperatorImageBesideDigestOnlyDefault(t *testing.T) {
	_, _, baseDigest := util.SplitImageReference(DefaultBaseImage)
	_, _, builderDigest := util.SplitImageReference(DefaultBuilderImage)
	source := bootstrapSourceFixture(t)
	for name, recorded := range map[string]BootstrapOptions{
		"mirrored digest": {SourceRoot: source, BaseImage: "registry.example/mirror/base@" + baseDigest, BuilderImage: "registry.example/mirror/golang@" + builderDigest},
		"short form":      {SourceRoot: source, BaseImage: adopterBase, BuilderImage: "golang:1.27-alpine@" + builderDigest},
		"other digest":    {SourceRoot: source, BaseImage: "mcr.microsoft.com/devcontainers/base@sha256:" + strings.Repeat("0", 64), BuilderImage: "docker.io/library/golang@sha256:" + strings.Repeat("1", 64)},
	} {
		options, notes, err := InheritRecordedImages(t.Context(), writeRecordedBundle(t, recorded), BootstrapOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if options.BaseImage != recorded.BaseImage || options.BuilderImage != recorded.BuilderImage || len(notes) != 2 {
			t.Fatalf("%s: operator image not kept: %+v %+v", name, options, notes)
		}
		for _, note := range notes {
			if note.Action != ImageKept {
				t.Fatalf("%s: %s note = %q", name, note.Role, note)
			}
		}
	}
}

// isReviewedPin matches the current default and the earlier ones by repository and digest,
// under any tag or none; another repository, digest or an empty image does not match.
func TestIsReviewedPinMatchesCurrentAndPriorDefaults(t *testing.T) {
	_, _, digest := util.SplitImageReference(DefaultBuilderImage)
	for image, want := range map[string]bool{
		DefaultBuilderImage:                                          true,
		taggedDefaultBuilder:                                         true,
		"docker.io/library/golang:1.28@" + digest:                    true,
		earlierReviewedBuilder:                                       true,
		priorDefaultBuilderImages[0]:                                 true,
		"registry.example/golang@" + digest:                          false,
		"golang@" + digest:                                           false,
		"docker.io/library/golang@sha256:" + strings.Repeat("2", 64): false,
		"docker.io/library/golang:1.27-alpine":                       false,
		"":                                                           false,
	} {
		if got := isReviewedPin(image, DefaultBuilderImage, priorDefaultBuilderImages); got != want {
			t.Errorf("isReviewedPin(%q) = %v, want %v", image, got, want)
		}
	}
	// A tagged current default still matches itself exactly and by digest.
	if !isReviewedPin(taggedDefaultBase, taggedDefaultBase, nil) || !isReviewedPin(DefaultBaseImage, taggedDefaultBase, nil) {
		t.Fatal("a tagged current default does not match its own digest")
	}
}

// Generation and verification accept digest-only and tag@digest references and refuse a
// tag-only or malformed one, for both images; the digest is exactly 64 lowercase hex.
func TestBootstrapImageReferenceForms(t *testing.T) {
	hex := strings.Repeat("a", 64)
	for image, accepted := range map[string]bool{
		"docker.io/library/golang@sha256:" + hex:                     true,
		"docker.io/library/golang:1.27-alpine@sha256:" + hex:         true,
		"registry.test:5000/team/base@sha256:" + hex:                 true,
		strings.Repeat("r", 221) + "@sha256:" + hex:                  true,
		strings.Repeat("r", 222) + "@sha256:" + hex:                  false,
		"docker.io/library/golang:1.27-alpine":                       false,
		"docker.io/library/golang@sha256:" + hex[:63]:                false,
		"docker.io/library/golang@sha256:" + hex + "a":               false,
		"docker.io/library/golang@sha256:" + strings.ToUpper(hex):    false,
		"docker.io/library/golang@sha256:" + strings.Repeat("g", 64): false,
		"docker.io/library/golang@sha512:" + hex + hex:               false,
		"docker.io/library/golang@sha256:":                           false,
		"docker.io/library/golang@@sha256:" + hex:                    false,
	} {
		for role, options := range map[string]BootstrapOptions{"builder": {BuilderImage: image}, "base": {BaseImage: image}} {
			_, err := PrepareBundle(t.Context(), "app", nil, nil, options)
			if (err == nil) != accepted {
				t.Errorf("%s image %q: accepted=%v, want %v (%v)", role, image, err == nil, accepted, err)
			}
		}
		spec := &BootstrapSpec{Version: bootstrapVersion, State: BootstrapUnavailable, Reason: "fixture", BaseImage: image}
		if err := validateBootstrapSpec(spec); (err == nil) != accepted {
			t.Errorf("recorded base image %q: verified=%v, want %v (%v)", image, err == nil, accepted, err)
		}
	}
}
