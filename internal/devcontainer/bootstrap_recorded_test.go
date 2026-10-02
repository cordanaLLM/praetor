package devcontainer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Neutral fixture images: an adopter-owned base and a mirrored builder from repositories
// other than the reviewed defaults; adopter choices inside the reviewed default
// repositories; and the reviewed pins before the 26.04 move as generation recorded them,
// the tag put back onto the tagless prior-default entries.
var (
	adopterBase           = "registry.example/team/dev-toolchains@sha256:" + strings.Repeat("a", 64)
	adopterBuilder        = "registry.example/mirror/golang:1.27-alpine@sha256:" + strings.Repeat("b", 64)
	sameRepositoryBase    = "mcr.microsoft.com/devcontainers/base:debian-12@sha256:" + strings.Repeat("e", 64)
	sameRepositoryBuilder = "docker.io/library/golang:1.28-bookworm@sha256:" + strings.Repeat("f", 64)
)

// shippedPriors is the prior-default list compiled into this package, read the way
// InheritRecordedImages reads it.
func shippedPriors(t *testing.T) PriorImages {
	t.Helper()
	priors, err := loadPriorImages()
	if err != nil {
		t.Fatal(err)
	}
	return priors
}

// earlierReviewedBase is the base default before the 26.04 move, with its tag put back.
func earlierReviewedBase(t *testing.T) string {
	return strings.Replace(shippedPriors(t).Base[0], "@", ":ubuntu-24.04@", 1)
}

// earlierReviewedBuilder is the builder default before #352, with its tag put back.
func earlierReviewedBuilder(t *testing.T) string {
	return strings.Replace(shippedPriors(t).Builder[0], "@", ":1.27-alpine@", 1)
}

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

// Positive: another tag or digest of a reviewed default repository is a deliberate choice
// too, so it is kept like any other image, in full or short reference form (#536).
func TestInheritRecordedImagesKeepsSameRepositoryChoice(t *testing.T) {
	source := bootstrapSourceFixture(t)
	for name, recorded := range map[string]BootstrapOptions{
		"other tags":   {SourceRoot: source, BaseImage: sameRepositoryBase, BuilderImage: sameRepositoryBuilder},
		"short form":   {SourceRoot: source, BaseImage: sameRepositoryBase, BuilderImage: "golang:1.28-alpine@sha256:" + strings.Repeat("f", 64)},
		"other digest": {SourceRoot: source, BaseImage: strings.SplitN(earlierReviewedBase(t), "@", 2)[0] + "@sha256:" + strings.Repeat("0", 64), BuilderImage: strings.SplitN(DefaultBuilderImage, "@", 2)[0] + "@sha256:" + strings.Repeat("1", 64)},
	} {
		options, notes, err := InheritRecordedImages(t.Context(), writeRecordedBundle(t, recorded), BootstrapOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if options.BaseImage != recorded.BaseImage || options.BuilderImage != recorded.BuilderImage || len(notes) != 2 {
			t.Fatalf("%s: same-repository choice not kept: %+v %+v", name, options, notes)
		}
		for _, note := range notes {
			if note.Action != ImageKept || !strings.HasPrefix(note.String(), "[RECORDED IMAGE KEPT] "+note.Role+" image "+note.Recorded) {
				t.Fatalf("%s: %s note = %q", name, note.Role, note)
			}
		}
	}
}

// Boundary: exactly an earlier reviewed default pin is refreshed to the current pin, not
// frozen, and the note names how to keep it. An unavailable placeholder records no builder,
// so none is inherited.
func TestInheritRecordedImagesRefreshesEarlierReviewedPin(t *testing.T) {
	path := writeRecordedBundle(t, BootstrapOptions{BaseImage: earlierReviewedBase(t)})
	options, notes, err := InheritRecordedImages(t.Context(), path, BootstrapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if options.BaseImage != DefaultBaseImage || options.BuilderImage != "" || len(notes) != 1 {
		t.Fatalf("earlier reviewed pin not refreshed: %+v %+v", options, notes)
	}
	want := "[RECORDED IMAGE REFRESHED] base image " + earlierReviewedBase(t) + " -> reviewed default " + DefaultBaseImage +
		"; devcontainer generate --base-image " + earlierReviewedBase(t) + " keeps it"
	if note := noteFor(t, notes, "base"); note.Action != ImageRefreshed || note.String() != want {
		t.Fatalf("refresh note = %q, want %q", note, want)
	}
	both := writeRecordedBundle(t, BootstrapOptions{SourceRoot: bootstrapSourceFixture(t), BaseImage: earlierReviewedBase(t), BuilderImage: earlierReviewedBuilder(t)})
	options, notes, err = InheritRecordedImages(t.Context(), both, BootstrapOptions{})
	if err != nil || len(notes) != 2 || options.BaseImage != DefaultBaseImage || options.BuilderImage != DefaultBuilderImage {
		t.Fatalf("earlier reviewed builder pin not refreshed: %+v %+v %v", options, notes, err)
	}
	if note := noteFor(t, notes, "builder"); note.Action != ImageRefreshed || note.Selected != DefaultBuilderImage {
		t.Fatalf("builder refresh note = %+v", note)
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

// The refresh history holds tagless, digest-pinned, retired defaults only: a current default
// or an unpinned entry there would refresh or match an image no reviewed release recorded,
// and a tag would make it a second live pin of that tag to the repository pin scan.
func TestPriorDefaultImagesArePinnedAndRetired(t *testing.T) {
	for current, prior := range map[string][]string{DefaultBaseImage: shippedPriors(t).Base, DefaultBuilderImage: shippedPriors(t).Builder} {
		if len(prior) == 0 {
			t.Fatalf("no earlier reviewed default recorded for %s", current)
		}
		if err := validateBootstrapImages(prior...); err != nil {
			t.Fatalf("earlier reviewed default not digest-pinned: %v", err)
		}
		for i, image := range prior {
			if _, tag, _ := util.SplitImageReference(image); tag != "" {
				t.Fatalf("earlier reviewed default %s carries tag %q", image, tag)
			}
			if isPriorDefault(current, prior) || slices.Contains(prior[:i], image) {
				t.Fatalf("earlier reviewed default %s is current or repeated", image)
			}
		}
	}
}

// A prior default matches by repository and digest under any tag; another digest, the short
// Docker Hub form of the repository, a tagless reference without digest or an empty history
// does not.
func TestIsPriorDefaultMatchesRepositoryAndDigest(t *testing.T) {
	digest := strings.SplitN(shippedPriors(t).Builder[0], "@", 2)[1]
	for image, want := range map[string]bool{
		earlierReviewedBuilder(t):                 true,
		shippedPriors(t).Builder[0]:               true,
		"docker.io/library/golang:1.28@" + digest: true,
		"golang:1.27-alpine@" + digest:            false,
		"docker.io/library/golang:1.27-alpine":    false,
		sameRepositoryBuilder:                     false,
		"":                                        false,
		"docker.io/library/golang:1.27-alpine@sha256:" + strings.Repeat("0", 64): false,
	} {
		if got := isPriorDefault(image, shippedPriors(t).Builder); got != want {
			t.Errorf("isPriorDefault(%q) = %v, want %v", image, got, want)
		}
	}
	if isPriorDefault(earlierReviewedBuilder(t), nil) {
		t.Fatal("empty history matched")
	}
}
