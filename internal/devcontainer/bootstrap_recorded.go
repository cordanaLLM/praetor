package devcontainer

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Image selection outcomes a regeneration reports (ImageNote.Action).
const (
	// ImageKept: the recorded image is not a reviewed default, so it is the adopter's choice
	// and generation reuses it, whatever repository, tag or digest it names.
	ImageKept = "kept"
	// ImageRefreshed: the recorded image names the repository and digest of the current or
	// an earlier reviewed default under another spelling, so generation takes the current
	// reviewed pin.
	ImageRefreshed = "refreshed"
	// ImageReplaced: an explicit option selected another image than the recorded one.
	ImageReplaced = "replaced"
)

// ImageNote reports one bootstrap image whose selection differs from the recorded one or
// from the reviewed default, so a regeneration never changes or keeps an image silently.
type ImageNote struct {
	Role     string // "base" or "builder"
	Flag     string // generate option that selects this image
	Action   string // ImageKept, ImageRefreshed or ImageReplaced
	Recorded string // image the replaced bootstrap specification records
	Selected string // image the new bundle uses
	Default  string // reviewed default for this role
}

// String renders the note as one line of generation output.
func (n ImageNote) String() string {
	switch n.Action {
	case ImageKept:
		return fmt.Sprintf("[RECORDED IMAGE KEPT] %s image %s; reviewed default %s; devcontainer generate %s replaces it",
			n.Role, n.Recorded, n.Default, n.Flag)
	case ImageRefreshed:
		return fmt.Sprintf("[RECORDED IMAGE REFRESHED] %s image %s -> reviewed default %s; devcontainer generate %s %s keeps it",
			n.Role, n.Recorded, n.Selected, n.Flag, n.Recorded)
	default:
		return fmt.Sprintf("[RECORDED IMAGE REPLACED] %s image %s -> %s (%s)", n.Role, n.Recorded, n.Selected, n.Flag)
	}
}

// imageRole binds one image option to what the recorded specification holds for it.
type imageRole struct {
	name, flag, fallback, recorded string
	prior                          []string // earlier reviewed defaults for this role
	selected                       *string
}

// InheritRecordedImages resolves the images a regeneration of the config at path uses, so
// --force no longer swaps an adopter's recorded image for the reviewed default (#536). An
// image options sets explicitly always wins. Otherwise a valid bootstrap specification
// recorded at path decides: a recorded image naming the repository and digest of the
// current or an earlier reviewed default, under any tag or none, is refreshed to the
// current pin, so reviewed updates still reach default users and a bundle recorded with
// the tagged form of today's digest-only default (#333) takes the digest-only form; any
// other recorded image, another digest of the default repository or the same digest from
// another repository included, is the adopter's choice and is kept. A missing, unmanaged or
// invalid config records no choice and leaves options unchanged. The notes list every
// image kept, refreshed or replaced against its recorded value. The earlier reviewed defaults
// are the prior-images.json compiled into this binary (loadPriorImages).
func InheritRecordedImages(ctx context.Context, path string, options BootstrapOptions) (BootstrapOptions, []ImageNote, error) {
	if ctx == nil {
		return options, nil, errors.New("recorded image inheritance requires context")
	}
	data, exists, err := contextopt.ObserveSnapshot(ctx, path)
	if err != nil {
		return options, nil, fmt.Errorf("read recorded DevContainer bootstrap %s: %w", path, err)
	}
	if !exists {
		return options, nil, nil
	}
	recorded := decodeRecordedBootstrap(data, path)
	if recorded == nil {
		return options, nil, nil
	}
	priors, err := loadPriorImages()
	if err != nil {
		return options, nil, err
	}
	var notes []ImageNote
	for _, role := range []imageRole{
		{name: "base", flag: "--base-image", fallback: DefaultBaseImage, recorded: recorded.BaseImage, prior: priors.Base, selected: &options.BaseImage},
		{name: "builder", flag: "--builder-image", fallback: DefaultBuilderImage, recorded: recorded.BuilderImage, prior: priors.Builder, selected: &options.BuilderImage},
	} {
		if note, changed := inheritImage(role); changed {
			notes = append(notes, note)
		}
	}
	return options, notes, nil
}

// inheritImage fills role.selected from the recorded image when no option chose one, and
// reports whether the selection deserves a note.
func inheritImage(role imageRole) (ImageNote, bool) {
	note := ImageNote{Role: role.name, Flag: role.flag, Recorded: role.recorded, Default: role.fallback}
	switch {
	case role.recorded == "":
		return note, false
	case *role.selected != "":
		note.Action, note.Selected = ImageReplaced, *role.selected
		return note, *role.selected != role.recorded
	case isReviewedPin(role.recorded, role.fallback, role.prior):
		*role.selected = role.fallback
		note.Action, note.Selected = ImageRefreshed, role.fallback
		return note, role.recorded != role.fallback
	default:
		*role.selected = role.recorded
		note.Action, note.Selected = ImageKept, role.recorded
		return note, true
	}
}

// isReviewedPin reports whether image is the current reviewed default or one of the
// earlier ones in prior, by repository and digest under any tag or none, so the tagged
// form the defaults carried before they became digest-only (#333) still matches today's.
func isReviewedPin(image, current string, prior []string) bool {
	repository, _, digest := util.SplitImageReference(current)
	return image == current || isPriorDefault(image, []string{repository + "@" + digest}) || isPriorDefault(image, prior)
}

// isPriorDefault reports whether image is one of the earlier reviewed defaults in prior,
// which are held as repository@digest: the same repository and digest under any tag match,
// another digest or repository does not.
func isPriorDefault(image string, prior []string) bool {
	repository, _, digest := util.SplitImageReference(image)
	return digest != "" && slices.Contains(prior, repository+"@"+digest)
}
