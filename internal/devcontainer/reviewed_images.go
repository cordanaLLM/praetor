// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package devcontainer

import (
	"embed"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Reviewed default images (#323). Each default is pinned once, in bootstrap.go: a
// "// Reviewed at <repository>:<tag>@sha256:<digest>" line directly above the digest-only
// constant. ReviewedPinPattern reads that pair, and renovate.json's customManagers entry uses
// the same expression, so Renovate looks the digest up under the reviewed tag and one update
// moves the comment and the constant together. A tagless reference would be looked up as
// latest (#700, #703). The generated .devcontainer bundle is never what Renovate reads.
//
// prior-images.json holds the earlier defaults of each role as repository@digest, oldest
// first. devcontainer bump appends the pin it replaces, so InheritRecordedImages refreshes a
// bundle that recorded it (#553) without a hand edit.
const (
	// ReviewedPinPattern matches one reviewed pin of bootstrap.go. The named groups are the
	// ones Renovate's regex manager reads; the two unnamed groups are the constant and its
	// digest-only value. The syntax is the RE2 subset both Go and Renovate compile.
	ReviewedPinPattern = `// Reviewed at (?<depName>[a-z0-9][a-z0-9./_-]*):(?<currentValue>[A-Za-z0-9_][A-Za-z0-9._-]*)@(?<currentDigest>sha256:[a-f0-9]{64})\n\t(Default(?:Builder|Base)Image) = "([a-z0-9][a-z0-9./_-]*@sha256:[a-f0-9]{64})"`
	// ReviewedPinsFile is the source file, relative to a Praetor checkout, that holds the pins.
	ReviewedPinsFile = "internal/devcontainer/bootstrap.go"
	// PriorImagesFile is the prior-default list, relative to a Praetor checkout.
	PriorImagesFile = "internal/devcontainer/" + priorImagesName
	// DevImageDockerfile builds the development image from the reviewed builder pin; a bump
	// of the builder moves its FROM line too.
	DevImageDockerfile = "docker/dev/Dockerfile"

	// bundleRepair ends every verification error a changed bundle file causes: the files are
	// generated, so a hand or bot edit is repaired by regeneration, and a default image moves
	// through the bump that regenerates the bundle (#323).
	bundleRepair = "the bundle is generated: regenerate it with praetorctl devcontainer generate --source-root <reviewed Praetor checkout> --force, " +
		"and move a reviewed default image in a Praetor checkout with praetorctl devcontainer bump"

	priorImagesName      = "prior-images.json"
	reviewedImagesSource = "internal/devcontainer/reviewed_images.go"
	priorImagesDirective = "//go:embed " + priorImagesName
	// maxPriorImages bounds each role's prior-default list (HISS-02). devcontainer bump only
	// appends, and nothing prunes the list: a bundle that recorded a removed image would be kept
	// as the adopter's choice instead of refreshed (#553). A list at the bound is therefore
	// refused, and the remedy is to raise this bound in a reviewed change (priorImagesRemedy).
	maxPriorImages = 256
	// priorImagesRemedy ends the refusal of a prior-default list past maxPriorImages.
	priorImagesRemedy = "the list is append-only and never pruned, because a bundle that recorded a removed image is no longer refreshed: " +
		"raise maxPriorImages in " + reviewedImagesSource + " in a reviewed change"
)

//go:embed prior-images.json
var priorImagesFS embed.FS

// reviewedPinExpression is ReviewedPinPattern compiled once.
var reviewedPinExpression = regexp.MustCompile(ReviewedPinPattern)

// reviewedRole is one reviewed default: the role name, the constant bootstrap.go declares, the
// generate option that overrides it, and the Dockerfiles that build from the same pin.
type reviewedRole struct {
	name, constant, flag string
	dockerfiles          []string
}

// reviewedRoles lists the reviewed defaults in report order.
var reviewedRoles = []reviewedRole{
	{name: "base", constant: "DefaultBaseImage", flag: "--base-image"},
	{name: "builder", constant: "DefaultBuilderImage", flag: "--builder-image", dockerfiles: []string{DevImageDockerfile}},
}

// UpdateBotBundleFiles returns the files of the bundle at config, a slash-separated repository
// path of its devcontainer.json, that a dependency update bot's managers read: the config and
// the Dockerfile beside it. Both are generated, so verification rejects a bot's edit, and the
// Dockerfile pins its images digest-only, which Renovate looks up as latest (#323). The
// base64 source parts hold nothing a manager parses. Praetor's renovate.json and the rule
// adoption writes for adopters disable Renovate for exactly these files.
func UpdateBotBundleFiles(config string) []string {
	return []string{config, path.Join(path.Dir(config), bootstrapDockerfile)}
}

// RecordsBootstrap reports whether data, the bytes of a devcontainer.json, is a config Praetor
// generated: one carrying a customizations.praetor.bootstrap object. It asks neither for the
// managed schema nor for a specification that still validates, unlike decodeRecordedBootstrap:
// a generated file a bot or a person edited since is still a generated file, and the one
// verification rejects.
func RecordsBootstrap(data []byte) bool {
	var recorded struct {
		Customizations struct {
			Praetor struct {
				Bootstrap jsontext.Value `json:"bootstrap"`
			} `json:"praetor"`
		} `json:"customizations"`
	}
	return json.Unmarshal(data, &recorded) == nil && recorded.Customizations.Praetor.Bootstrap.Kind() == '{'
}

// PriorImages is prior-images.json: every earlier reviewed default of each role, as
// repository@sha256:<digest> without a tag, oldest first.
type PriorImages struct {
	Base    []string `json:"base"`
	Builder []string `json:"builder"`
}

// role returns the list of one role.
func (p *PriorImages) role(name string) *[]string {
	if name == "base" {
		return &p.Base
	}
	return &p.Builder
}

// loadPriorImages reads the prior-default list compiled into this binary.
func loadPriorImages() (PriorImages, error) {
	data, err := util.ReadEmbeddedAsset(priorImagesFS, "reviewed image", []string{priorImagesName}, priorImagesName)
	if err != nil {
		return PriorImages{}, err
	}
	return ParsePriorImages(data)
}

// ParsePriorImages decodes and validates a prior-default list: strict JSON with exactly the two
// role members, each entry a lowercase repository@sha256:<digest> without a tag, none repeated.
func ParsePriorImages(data []byte) (PriorImages, error) {
	var priors PriorImages
	if err := json.Unmarshal(data, &priors, json.RejectUnknownMembers(true)); err != nil {
		return PriorImages{}, fmt.Errorf("parse %s: %w", PriorImagesFile, err)
	}
	for _, role := range reviewedRoles {
		if err := validatePriorList(role.name, *priors.role(role.name)); err != nil {
			return PriorImages{}, err
		}
	}
	return priors, nil
}

// validatePriorList holds one role's list to the prior-default form.
func validatePriorList(role string, images []string) error {
	if len(images) > maxPriorImages {
		return fmt.Errorf("%s lists %d %s images, more than the bound of %d; %s", PriorImagesFile, len(images), role, maxPriorImages, priorImagesRemedy)
	}
	for index := 0; index < len(images) && index < maxPriorImages; index++ {
		if _, tag, _ := util.SplitImageReference(images[index]); tag != "" || validateBootstrapImages(images[index]) != nil {
			return fmt.Errorf("%s %s image %q is not repository@sha256:<digest> without a tag", PriorImagesFile, role, images[index])
		}
		if slices.Contains(images[:index], images[index]) {
			return fmt.Errorf("%s lists %s image %s twice", PriorImagesFile, role, images[index])
		}
	}
	return nil
}

// RenderPriorImages renders a prior-default list in the committed form: two-space indentation
// and a final newline, so rendering a parsed file reproduces it byte for byte.
func RenderPriorImages(priors PriorImages) ([]byte, error) {
	data, err := json.Marshal(priors, jsontext.WithIndent("  "))
	if err != nil {
		return nil, fmt.Errorf("render %s: %w", PriorImagesFile, err)
	}
	return append(data, '\n'), nil
}

// reviewedPin is one parsed pin: the full reference the comment names and where the comment
// and constant sit in the source.
type reviewedPin struct {
	role                 reviewedRole
	repository, tag      string
	digest               string
	start, end           int // the whole match
	constantValueStart   int // the constant's digest-only string, without quotes
	constantValueEnd     int
	commentReferenceFrom int // the comment's repository:tag@digest
	commentReferenceTo   int
}

// reference returns the tagged reference the pin was reviewed at.
func (p reviewedPin) reference() string { return p.repository + ":" + p.tag + "@" + p.digest }

// image returns the digest-only form the constant holds.
func (p reviewedPin) image() string { return p.repository + "@" + p.digest }

// ReviewedReferences returns the reference each reviewed default of source, the bytes of
// ReviewedPinsFile, was reviewed at, by role ("base" and "builder"), as
// repository:tag@sha256:<digest>. The digest-only constants hold no tag, so this is where a
// caller reads the reviewed tag of a default. The pins are read as devcontainer bump reads
// them, in LF or CRLF form, and a source whose comment and constant disagree is refused.
func ReviewedReferences(source []byte) (map[string]string, error) {
	pins, _, _, err := readReviewedPins(source)
	if err != nil {
		return nil, err
	}
	references := make(map[string]string, len(pins))
	for _, role := range reviewedRoles {
		references[role.name] = pins[role.name].reference()
	}
	return references, nil
}

// readReviewedPins parses the pins of source, the bytes of ReviewedPinsFile in one consistent
// LF or CRLF form, and returns them with the LF-normalized text their offsets index and
// whether the source was CRLF.
func readReviewedPins(source []byte) (pins map[string]reviewedPin, text string, crlf bool, err error) {
	text, crlf, err = util.NormalizeLineEndingsStrict(string(source))
	if err != nil {
		return nil, "", false, fmt.Errorf("%s: %w", ReviewedPinsFile, err)
	}
	pins, err = parseReviewedPins(text)
	if err != nil {
		return nil, "", false, err
	}
	return pins, text, crlf, nil
}

// parseReviewedPins finds exactly one pin per reviewed role in LF-normalized source, each with
// its comment and constant naming the same repository and digest.
func parseReviewedPins(source string) (map[string]reviewedPin, error) {
	pins := map[string]reviewedPin{}
	matches := reviewedPinExpression.FindAllStringSubmatchIndex(source, len(reviewedRoles)+1)
	for _, match := range matches {
		pin, err := reviewedPinAt(source, match)
		if err != nil {
			return nil, err
		}
		if _, repeated := pins[pin.role.name]; repeated {
			return nil, fmt.Errorf("%s pins %s twice", ReviewedPinsFile, pin.role.constant)
		}
		pins[pin.role.name] = pin
	}
	for _, role := range reviewedRoles {
		if _, found := pins[role.name]; !found {
			return nil, fmt.Errorf("%s has no \"// Reviewed at <repository>:<tag>@sha256:<digest>\" line directly above %s", ReviewedPinsFile, role.constant)
		}
	}
	return pins, nil
}

// reviewedPinAt decodes one match of reviewedPinExpression.
func reviewedPinAt(source string, match []int) (reviewedPin, error) {
	group := func(index int) string { return source[match[2*index]:match[2*index+1]] }
	constant, value := group(4), group(5)
	pin := reviewedPin{repository: group(1), tag: group(2), digest: group(3), start: match[0], end: match[1],
		constantValueStart: match[10], constantValueEnd: match[11], commentReferenceFrom: match[2], commentReferenceTo: match[7]}
	index := slices.IndexFunc(reviewedRoles, func(role reviewedRole) bool { return role.constant == constant })
	if index < 0 {
		return reviewedPin{}, fmt.Errorf("%s pins unknown constant %s", ReviewedPinsFile, constant)
	}
	pin.role = reviewedRoles[index]
	if value != pin.image() {
		return reviewedPin{}, fmt.Errorf("%s: %s = %q, but its comment was reviewed at %s", ReviewedPinsFile, constant, value, pin.reference())
	}
	return pin, nil
}

// parseTaggedPin parses a reference a bump moves a pin to: repository:tag@sha256:<digest>,
// lowercase, with a tag. A tagless reference is refused: Renovate would read it as latest.
func parseTaggedPin(reference string) (repository, tag, digest string, err error) {
	repository, tag, digest = util.SplitImageReference(reference)
	if tag == "" || validateBootstrapImages(reference) != nil || strings.Contains(repository, ":") {
		return "", "", "", errors.New("a reviewed pin must be repository:tag@sha256:<64 lowercase hex> with a tag and no registry port, so Renovate reads its tag")
	}
	return repository, tag, digest, nil
}
