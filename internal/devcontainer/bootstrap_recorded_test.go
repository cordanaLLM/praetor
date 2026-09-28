package devcontainer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Neutral fixture images: an adopter-owned base and a mirrored builder from repositories
// other than the reviewed defaults, and the reviewed base pin before the 26.04 move.
var (
	adopterBase         = "registry.example/team/dev-toolchains@sha256:" + strings.Repeat("a", 64)
	adopterBuilder      = "registry.example/mirror/golang:1.27-alpine@sha256:" + strings.Repeat("b", 64)
	earlierReviewedBase = "mcr.microsoft.com/devcontainers/base:ubuntu-24.04@sha256:d94c97dd9cacf183d0a6fd12a8e87b526e9e928307674ae9c94139139c0c6eae"
)

// writeRecordedBundle writes what generation writes for options and returns the config path.
func writeRecordedBundle(t *testing.T, options BootstrapOptions) string {
	t.Helper()
	bundle, err := PrepareBundle(t.Context(), "adopted/app", []string{"framework"}, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), ".devcontainer", "devcontainer.json")
	if err := WriteBundle(t.Context(), path, bundle, false); err != nil {
		t.Fatal(err)
	}
	return path
}

func noteFor(t *testing.T, notes []ImageNote, role string) ImageNote {
	t.Helper()
	for _, note := range notes {
		if note.Role == role {
			return note
		}
	}
	t.Fatalf("no %s image note in %+v", role, notes)
	return ImageNote{}
}

// Positive: a regeneration without image options keeps the adopter's recorded images and
// says so, naming the reviewed default and the option that replaces each.
func TestInheritRecordedImagesKeepsAdopterImages(t *testing.T) {
	path := writeRecordedBundle(t, BootstrapOptions{SourceRoot: bootstrapSourceFixture(t), BaseImage: adopterBase, BuilderImage: adopterBuilder})
	options, notes, err := InheritRecordedImages(t.Context(), path, BootstrapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if options.BaseImage != adopterBase || options.BuilderImage != adopterBuilder || len(notes) != 2 {
		t.Fatalf("recorded images not inherited: %+v %+v", options, notes)
	}
	for role, want := range map[string][]string{
		"base":    {"[RECORDED IMAGE KEPT] base image " + adopterBase, "reviewed default " + DefaultBaseImage, "devcontainer generate --base-image replaces it"},
		"builder": {"[RECORDED IMAGE KEPT] builder image " + adopterBuilder, "reviewed default " + DefaultBuilderImage, "--builder-image replaces it"},
	} {
		note := noteFor(t, notes, role)
		for _, fragment := range want {
			if note.Action != ImageKept || !strings.Contains(note.String(), fragment) {
				t.Fatalf("%s note %q lacks %q", role, note, fragment)
			}
		}
	}
}

// Positive: an explicit option still selects its image, and replacing a recorded choice is
// reported; an option equal to the recorded image needs no note.
func TestInheritRecordedImagesExplicitOptionWins(t *testing.T) {
	path := writeRecordedBundle(t, BootstrapOptions{SourceRoot: bootstrapSourceFixture(t), BaseImage: adopterBase, BuilderImage: adopterBuilder})
	options, notes, err := InheritRecordedImages(t.Context(), path, BootstrapOptions{BaseImage: DefaultBaseImage, BuilderImage: adopterBuilder})
	if err != nil {
		t.Fatal(err)
	}
	if options.BaseImage != DefaultBaseImage || options.BuilderImage != adopterBuilder || len(notes) != 1 {
		t.Fatalf("explicit options overridden: %+v %+v", options, notes)
	}
	want := "[RECORDED IMAGE REPLACED] base image " + adopterBase + " -> " + DefaultBaseImage + " (--base-image)"
	if note := noteFor(t, notes, "base"); note.Action != ImageReplaced || note.String() != want {
		t.Fatalf("replacement note = %q, want %q", note, want)
	}
}

// Boundary: an earlier pin of the reviewed default repository is refreshed to the current
// pin, not frozen, and the note names how to keep it. An unavailable placeholder records no
// builder, so none is inherited.
func TestInheritRecordedImagesRefreshesEarlierReviewedPin(t *testing.T) {
	path := writeRecordedBundle(t, BootstrapOptions{BaseImage: earlierReviewedBase})
	options, notes, err := InheritRecordedImages(t.Context(), path, BootstrapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if options.BaseImage != DefaultBaseImage || options.BuilderImage != "" || len(notes) != 1 {
		t.Fatalf("earlier reviewed pin not refreshed: %+v %+v", options, notes)
	}
	want := "[RECORDED IMAGE REFRESHED] base image " + earlierReviewedBase + " -> reviewed default " + DefaultBaseImage +
		"; devcontainer generate --base-image " + earlierReviewedBase + " keeps it"
	if note := noteFor(t, notes, "base"); note.Action != ImageRefreshed || note.String() != want {
		t.Fatalf("refresh note = %q, want %q", note, want)
	}
	current := writeRecordedBundle(t, BootstrapOptions{SourceRoot: bootstrapSourceFixture(t)})
	options, notes, err = InheritRecordedImages(t.Context(), current, BootstrapOptions{})
	if err != nil || len(notes) != 0 || options.BaseImage != DefaultBaseImage || options.BuilderImage != DefaultBuilderImage {
		t.Fatalf("current reviewed pins reported or changed: %+v %+v %v", options, notes, err)
	}
}

// Negative: without a valid recorded specification nothing is inherited, so the reviewed
// defaults apply; a path that cannot be read is an error, not a silent default.
func TestInheritRecordedImagesWithoutRecordedChoice(t *testing.T) {
	dir := t.TempDir()
	unmanaged := filepath.Join(dir, "unmanaged.json")
	if err := os.WriteFile(unmanaged, []byte("{\"image\":\"owned:tag\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	recorded := writeRecordedBundle(t, BootstrapOptions{BaseImage: adopterBase})
	data, err := os.ReadFile(recorded)
	if err != nil {
		t.Fatal(err)
	}
	invalid := filepath.Join(dir, "invalid.json")
	if err := os.WriteFile(invalid, bytes.Replace(data, []byte(`"version": 1`), []byte(`"version": 2`), 1), 0600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"missing": filepath.Join(dir, "absent.json"), "unmanaged": unmanaged, "invalid-spec": invalid} {
		options, notes, err := InheritRecordedImages(t.Context(), path, BootstrapOptions{})
		if err != nil || options.BaseImage != "" || options.BuilderImage != "" || len(notes) != 0 {
			t.Fatalf("%s: inherited without a recorded choice: %+v %+v %v", name, options, notes, err)
		}
	}
	if _, _, err := InheritRecordedImages(t.Context(), dir, BootstrapOptions{}); err == nil {
		t.Fatal("unreadable config path treated as absent")
	}
	var absent context.Context
	if _, _, err := InheritRecordedImages(absent, recorded, BootstrapOptions{}); err == nil {
		t.Fatal("nil context accepted")
	}
}
