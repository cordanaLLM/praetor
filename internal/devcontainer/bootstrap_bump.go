// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package devcontainer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// bumpBound bounds one bump: reading and writing the pin source, the prior list and the
// Dockerfiles, then one source capture for the regenerated bundle, its plan, its publication
// and its verification. That is one generation, which bootstrapBound bounds, and the source
// edits and the verification around it, so the bound is twice bootstrapBound.
const bumpBound = 2 * bootstrapBound

// BumpOptions selects the Praetor checkout whose reviewed default images a bump moves, the
// bundle it regenerates, and the pins it moves to.
type BumpOptions struct {
	// SourceRoot is the Praetor checkout holding ReviewedPinsFile, PriorImagesFile and
	// DevImageDockerfile; the regenerated bundle captures its source.
	SourceRoot string
	// Output is the bundle's devcontainer.json. It must hold a ready bundle Praetor generated.
	Output string
	// Name, Profiles, Facets and Features select the configuration, as for PrepareBundle.
	Name             string
	Profiles, Facets []string
	Features         []config.DevContainerFeature
	// Expected is the configuration the declared standards synthesize; the regenerated
	// bundle is verified against it, as devcontainer verify does.
	Expected *DevContainer
	// BuilderImage and BaseImage move a pin to repository:tag@sha256:<digest>, within the
	// pin's repository. Empty keeps the pin the checkout holds.
	BuilderImage, BaseImage string
}

// BumpChange reports one reviewed default after a bump: the reference it was reviewed at
// before and after, and the images the prior-default list gained.
type BumpChange struct {
	Role     string
	From, To string
	Retired  []string
}

// String renders the change as one line of command output.
func (c BumpChange) String() string {
	line := fmt.Sprintf("[PIN KEPT] %s %s", c.Role, c.To)
	if c.From != c.To {
		line = fmt.Sprintf("[PIN MOVED] %s %s -> %s", c.Role, c.From, c.To)
	}
	if len(c.Retired) > 0 {
		line += "; " + PriorImagesFile + " records " + strings.Join(c.Retired, ", ")
	}
	return line
}

// bumpFile is one file a bump writes: the bytes it found and the bytes it leaves.
type bumpFile struct {
	path          string
	existed       bool
	before, after []byte
}

// bumpPlan is everything a bump writes, computed before the first write.
type bumpPlan struct {
	options BumpOptions
	pins    map[string]reviewedPin
	changes []BumpChange
	edits   []bumpFile
}

// Bump moves the reviewed default images of a Praetor checkout and regenerates its bundle as
// one change (#323). It moves each selected pin in ReviewedPinsFile, comment and constant,
// and the FROM lines of the Dockerfiles that build from it; appends every pin it replaces, and
// the image the bundle recorded when that is another digest of the pin's repository, to
// PriorImagesFile, so InheritRecordedImages refreshes a bundle that recorded it; regenerates
// the bundle at Output from the checkout with the new pins; and verifies it. Without a
// selected image it finishes a move made elsewhere, such as Renovate's update of the pins,
// which never touches the generated bundle: the image the bundle still records is the one
// replaced. Any failure restores every file the bump wrote.
func Bump(ctx context.Context, options BumpOptions) ([]BumpChange, error) {
	if ctx == nil {
		return nil, errors.New("devcontainer bump requires context")
	}
	if options.Expected == nil {
		return nil, errors.New("devcontainer bump requires the declared configuration")
	}
	ctx, cancel := context.WithTimeout(ctx, bumpBound)
	defer cancel()
	plan, err := planBump(ctx, options)
	if err != nil {
		return nil, err
	}
	if err := plan.apply(ctx); err != nil {
		return nil, err
	}
	return plan.changes, nil
}

// planBump reads the checkout and the recorded bundle and computes every source edit.
func planBump(ctx context.Context, options BumpOptions) (*bumpPlan, error) {
	recorded, err := readReadyBundle(ctx, options.Output)
	if err != nil {
		return nil, err
	}
	source, err := readBumpFile(ctx, options.SourceRoot, ReviewedPinsFile)
	if err != nil {
		return nil, err
	}
	current, text, crlf, err := readReviewedPins(source.before)
	if err != nil {
		return nil, err
	}
	plan := &bumpPlan{options: options, pins: map[string]reviewedPin{}}
	if err := plan.movePins(current, recorded); err != nil {
		return nil, err
	}
	source.after = []byte(util.RestoreLineEndings(rewritePins(text, current, plan.pins), crlf))
	plan.stage(source)
	if err := plan.planPriors(ctx); err != nil {
		return nil, err
	}
	if err := plan.planDockerfiles(ctx, current, recorded); err != nil {
		return nil, err
	}
	return plan, nil
}

// readReadyBundle returns the bootstrap specification of the ready bundle at path.
func readReadyBundle(ctx context.Context, path string) (*BootstrapSpec, error) {
	data, exists, err := contextopt.ObserveSnapshot(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("read bundle %s: %w", path, err)
	}
	spec := decodeRecordedBootstrap(data, path)
	if !exists || spec == nil || spec.State != BootstrapReady {
		return nil, fmt.Errorf("%s holds no ready Praetor bundle; generate one with praetorctl devcontainer generate --source-root <Praetor checkout> before a bump", path)
	}
	return spec, nil
}

// readBumpFile reads one checkout file a bump edits.
func readBumpFile(ctx context.Context, root, rel string) (bumpFile, error) {
	path := filepath.Join(root, filepath.FromSlash(rel))
	data, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		return bumpFile{}, fmt.Errorf("devcontainer bump reads %s in the Praetor checkout %s: %w", rel, root, err)
	}
	return bumpFile{path: path, existed: true, before: data, after: data}, nil
}

// stage records file as an edit when the bump changes it.
func (p *bumpPlan) stage(file bumpFile) {
	if !bytes.Equal(file.before, file.after) {
		p.edits = append(p.edits, file)
	}
}

// movePins resolves each role's next pin and the images it retires.
func (p *bumpPlan) movePins(current map[string]reviewedPin, recorded *BootstrapSpec) error {
	selected := map[string]string{"base": p.options.BaseImage, "builder": p.options.BuilderImage}
	recordedImages := map[string]string{"base": recorded.BaseImage, "builder": recorded.BuilderImage}
	for _, role := range reviewedRoles {
		next, err := nextPin(current[role.name], selected[role.name])
		if err != nil {
			return fmt.Errorf("%s: %w", role.flag, err)
		}
		p.pins[role.name] = next
		p.changes = append(p.changes, BumpChange{Role: role.name, From: current[role.name].reference(), To: next.reference(),
			Retired: retiredImages(current[role.name], next, recordedImages[role.name])})
	}
	return nil
}

// nextPin returns pin moved to reference, or pin itself when reference is empty. A move stays
// within the pin's repository: another repository is a reviewed source change of its own.
func nextPin(pin reviewedPin, reference string) (reviewedPin, error) {
	if reference == "" {
		return pin, nil
	}
	repository, tag, digest, err := parseTaggedPin(reference)
	if err != nil {
		return reviewedPin{}, err
	}
	if repository != pin.repository {
		return reviewedPin{}, fmt.Errorf("%s names repository %s, but the pin is %s; a bump moves a pin within its repository", reference, repository, pin.repository)
	}
	pin.tag, pin.digest = tag, digest
	return pin, nil
}

// retiredImages returns, as repository@digest, the replaced pin and the recorded image when
// either is another digest of next's repository; the caller drops those already listed.
func retiredImages(current, next reviewedPin, recorded string) []string {
	var retired []string
	for _, image := range []string{current.image(), recorded} {
		repository, _, digest := util.SplitImageReference(image)
		candidate := repository + "@" + digest
		if repository == next.repository && digest != "" && digest != next.digest && !slices.Contains(retired, candidate) {
			retired = append(retired, candidate)
		}
	}
	return retired
}

// rewritePins returns LF-normalized source with every pin of current moved to next. Pins are
// rewritten from the last to the first so earlier offsets stay valid.
func rewritePins(text string, current, next map[string]reviewedPin) string {
	order := make([]reviewedPin, 0, len(current))
	for _, pin := range current {
		order = append(order, pin)
	}
	sort.Slice(order, func(i, j int) bool { return order[i].start > order[j].start })
	for _, pin := range order {
		moved := next[pin.role.name]
		text = text[:pin.constantValueStart] + moved.image() + text[pin.constantValueEnd:]
		text = text[:pin.commentReferenceFrom] + moved.reference() + text[pin.commentReferenceTo:]
	}
	return text
}

// planPriors appends every retired image the prior-default list lacks.
func (p *bumpPlan) planPriors(ctx context.Context) error {
	file, err := readBumpFile(ctx, p.options.SourceRoot, PriorImagesFile)
	if err != nil {
		return err
	}
	priors, err := ParsePriorImages(file.before)
	if err != nil {
		return err
	}
	for index := range p.changes {
		role := p.changes[index].Role
		list, current := priors.role(role), p.pins[role].image()
		p.changes[index].Retired = slices.DeleteFunc(p.changes[index].Retired, func(image string) bool { return slices.Contains(*list, image) })
		// A pin moved back to an earlier digest is current again, not prior.
		*list = append(slices.DeleteFunc(*list, func(image string) bool { return image == current }), p.changes[index].Retired...)
	}
	if err := validatePriors(priors); err != nil {
		return err
	}
	rendered, err := RenderPriorImages(priors)
	if err != nil {
		return err
	}
	if !bytes.Equal(rendered, file.before) {
		file.after = rendered
	}
	p.stage(file)
	return nil
}

// validatePriors holds an extended list to the rules ParsePriorImages reads it back with.
func validatePriors(priors PriorImages) error {
	for _, role := range reviewedRoles {
		if err := validatePriorList(role.name, *priors.role(role.name)); err != nil {
			return err
		}
	}
	return nil
}

// planDockerfiles moves the FROM lines of every Dockerfile that builds from a moved pin.
func (p *bumpPlan) planDockerfiles(ctx context.Context, current map[string]reviewedPin, recorded *BootstrapSpec) error {
	recordedImages := map[string]string{"base": recorded.BaseImage, "builder": recorded.BuilderImage}
	for _, role := range reviewedRoles {
		_, _, recordedDigest := util.SplitImageReference(recordedImages[role.name])
		replaced := []string{current[role.name].digest, recordedDigest}
		for _, rel := range role.dockerfiles {
			file, err := readBumpFile(ctx, p.options.SourceRoot, rel)
			if err != nil {
				return err
			}
			moved, err := moveDockerfilePin(rel, string(file.before), p.pins[role.name], replaced)
			if err != nil {
				return err
			}
			file.after = []byte(moved)
			p.stage(file)
		}
	}
	return nil
}

// moveDockerfilePin rewrites each FROM line of dockerfile pinned to a replaced digest to the
// next tag and digest, keeping the repository spelling, and requires one FROM line to build
// from next afterwards.
func moveDockerfilePin(rel, dockerfile string, next reviewedPin, replaced []string) (string, error) {
	text, crlf, err := util.NormalizeLineEndingsStrict(dockerfile)
	if err != nil {
		return "", fmt.Errorf("%s: %w", rel, err)
	}
	lines := strings.Split(text, "\n")
	if len(lines) > MaxLoopLimit {
		return "", fmt.Errorf("%s carries %d lines, past the %d-line bound", rel, len(lines), MaxLoopLimit)
	}
	pinned := false
	for index := 0; index < len(lines) && index < MaxLoopLimit; index++ {
		moved, builds := moveFromLine(lines[index], next, replaced)
		lines[index], pinned = moved, pinned || builds
	}
	if !pinned {
		return "", fmt.Errorf("%s has no FROM line pinned to %s or to the image it replaces; move it by hand", rel, next.reference())
	}
	return util.RestoreLineEndings(strings.Join(lines, "\n"), crlf), nil
}

// moveFromLine rewrites a FROM line pinned to a replaced digest to next's tag and digest,
// keeping the repository spelling, and reports whether the line then builds from next. The
// replaced digests include the current one, so a move to another tag of the same digest (a
// floating tag and a patch tag share one) rewrites the tag alone.
func moveFromLine(line string, next reviewedPin, replaced []string) (string, bool) {
	reference := fromReference(line)
	if reference == "" {
		return line, false
	}
	repository, tag, digest := util.SplitImageReference(reference)
	if digest != "" && slices.Contains(replaced, digest) && (tag != next.tag || digest != next.digest) {
		tag, digest = next.tag, next.digest
		line = strings.Replace(line, reference, repository+":"+tag+"@"+digest, 1)
	}
	return line, tag == next.tag && digest == next.digest
}

// fromReference returns the image of a FROM instruction, skipping flags such as --platform,
// or "" when line is not one.
func fromReference(line string) string {
	fields := strings.Fields(line)
	if len(fields) < 2 || !strings.EqualFold(fields[0], "FROM") {
		return ""
	}
	for index := 1; index < len(fields) && index < MaxLoopLimit; index++ {
		if !strings.HasPrefix(fields[index], "--") {
			return fields[index]
		}
	}
	return ""
}

// apply writes the source edits, regenerates and verifies the bundle, and restores every
// written file when a step fails.
func (p *bumpPlan) apply(ctx context.Context) error {
	var written []bumpFile
	for _, edit := range p.edits {
		options := contextopt.ReplaceOptions{Expected: edit.before, Exists: true, Mode: 0644}
		if err := contextopt.ReplaceSnapshot(ctx, edit.path, edit.after, options); err != nil {
			return restoreBump(ctx, written, fmt.Errorf("write %s: %w", edit.path, err))
		}
		written = append(written, edit)
	}
	bundle, err := p.regenerate(ctx)
	written = append(written, bundle...)
	if err != nil {
		return restoreBump(ctx, written, err)
	}
	return nil
}

// regenerate writes the bundle from the checkout with the new pins and verifies it. It
// returns the bundle files it planned, written or not, for restoreBump.
func (p *bumpPlan) regenerate(ctx context.Context) ([]bumpFile, error) {
	images := BootstrapOptions{SourceRoot: p.options.SourceRoot, Features: p.options.Features,
		BuilderImage: p.pins["builder"].image(), BaseImage: p.pins["base"].image()}
	bundle, err := PrepareBundle(ctx, p.options.Name, p.options.Profiles, p.options.Facets, images)
	if err != nil {
		return nil, fmt.Errorf("prepare devcontainer bootstrap: %w", err)
	}
	if bundle.Spec().State != BootstrapReady {
		return nil, fmt.Errorf("%w: %s", ErrBootstrapUnavailable, bundle.Spec().Reason)
	}
	plan, err := PlanBundle(ctx, p.options.Output, bundle, true)
	if err != nil {
		return nil, fmt.Errorf("plan devcontainer bundle: %w", err)
	}
	files := make([]bumpFile, 0, len(plan.writes))
	for _, file := range plan.Files() {
		files = append(files, bumpFile{path: file.Path, existed: file.Existed, before: file.Before, after: file.After})
	}
	if err := plan.Publish(ctx); err != nil {
		return files, fmt.Errorf("write devcontainer bundle: %w", err)
	}
	if err := Verify(ctx, p.options.Output, p.options.Expected); err != nil {
		return files, fmt.Errorf("verify regenerated bundle: %w", err)
	}
	return files, nil
}

// restoreBump puts every file of written back to the bytes the bump found, last first, and
// returns cause joined with any file it could not restore. A file that no longer holds what
// the bump wrote is left as found and named. The restore runs on its own bound, so a bump
// that ran out of time still undoes its writes.
func restoreBump(ctx context.Context, written []bumpFile, cause error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), contextopt.MaxDuration)
	defer cancel()
	failures := []error{fmt.Errorf("devcontainer bump failed and restored the files it wrote: %w", cause)}
	for index := len(written) - 1; index >= 0; index-- {
		if err := restoreBumpFile(ctx, written[index]); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// restoreBumpFile restores one file, or does nothing when it still holds what the bump found.
func restoreBumpFile(ctx context.Context, file bumpFile) error {
	data, exists, err := contextopt.ObserveSnapshot(ctx, file.path)
	switch {
	case err != nil:
		return fmt.Errorf("restore %s: %w", file.path, err)
	case exists == file.existed && (!exists || bytes.Equal(data, file.before)):
		return nil
	case !exists || !bytes.Equal(data, file.after):
		return fmt.Errorf("restore %s: it changed during the bump and is left as found", file.path)
	case file.existed:
		return contextopt.ReplaceSnapshot(ctx, file.path, file.before, contextopt.ReplaceOptions{Expected: file.after, Exists: true, Mode: 0644})
	}
	return contextopt.RemoveSnapshot(ctx, file.path, file.after)
}
