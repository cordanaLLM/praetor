package devcontainer

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

const (
	BootstrapReady       = "source-bundle"
	BootstrapUnavailable = "unavailable"
	bootstrapVersion     = 1
	bootstrapDockerfile  = "Dockerfile.praetor"
	// maxBootstrapParts caps the base64 frames of one archive. Every consumer reads this
	// constant: framing, the Dockerfile COPY lines, spec validation and companion reads.
	// Part names are zero-padded to three digits, so the image's `cat *.b64` joins them in
	// order for any cap up to 1000. It was 4 until #501; a spec recorded then stays valid.
	maxBootstrapParts  = 8
	bootstrapPartBytes = 512 * 1024
	// bootstrapBound bounds preparing, planning and publishing one bundle (HISS-02). It is not the
	// context optimizer's 30-second contextopt.MaxDuration: preparing snapshots the whole build
	// source twice, and on a loaded host that took past 30 seconds and failed adoption with
	// "prepare devcontainer bootstrap: context deadline exceeded".
	bootstrapBound = 5 * time.Minute
	// The reviewed defaults are digest-only, repository@sha256:<digest>, because
	// @devcontainers/cli 0.89.0 refuses repository:tag@sha256:<digest> while it
	// inspects the registry (devcontainers/cli#1307; fixed upstream by #1311, which no
	// release carries yet), so a bundle built on the tagged form could not be built by
	// the documented consumer (#333). The digest is what the bundle pulls. The
	// "Reviewed at" line directly above each default is its one canonical pin: the full
	// reference it was reviewed at, tag included. Renovate reads that line through the
	// customManagers entry in renovate.json (ReviewedPinPattern), so it looks the digest
	// up under the reviewed tag rather than as latest, and one update moves the comment
	// and the constant together; devcontainer bump does the same by hand and finishes
	// either move (#323). TestReviewedDefaultCommentsNameTheirDigest binds each comment to
	// its constant, and the repository pin scan holds the tag to one digest across files.
	// A recorded tagged form of the same repository and digest still counts as this
	// default (isReviewedPin). Earlier defaults are in prior-images.json
	// (reviewed_images.go).
	//
	// Reviewed at docker.io/library/golang:1.27-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414
	DefaultBuilderImage = "docker.io/library/golang@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414"
	// The 26.04 tag drops the hyphen the 24.04 and earlier tags carried: that repository
	// publishes "ubuntu26.04", and "ubuntu-26.04" is not a tag on it.
	//
	// Reviewed at mcr.microsoft.com/devcontainers/base:ubuntu26.04@sha256:0b997af705ff88f10e3293326dce61b4a9676b9f98de6aad67f1973691737dd8
	DefaultBaseImage = "mcr.microsoft.com/devcontainers/base@sha256:0b997af705ff88f10e3293326dce61b4a9676b9f98de6aad67f1973691737dd8"
)

// BootstrapOptions selects a local Praetor source snapshot and immutable images.
// SourceRoot is explicit; an ordinary config-only catalog cannot install a CLI.
type BootstrapOptions struct {
	SourceRoot   string
	BuilderImage string
	BaseImage    string
	Features     []config.DevContainerFeature
}

// BootstrapSpec records generation inputs, not a claim of build or execution.
// Its digests are integrity checks, not signatures or source authentication.
type BootstrapSpec struct {
	Version          int    `json:"version"`
	State            string `json:"state"`
	Reason           string `json:"reason,omitempty"`
	BuilderImage     string `json:"builderImage,omitempty"`
	BaseImage        string `json:"baseImage"`
	SourceSHA256     string `json:"sourceSHA256,omitempty"`
	ArchiveSHA256    string `json:"archiveSHA256,omitempty"`
	ArchiveParts     int    `json:"archiveParts,omitempty"`
	DockerfileSHA256 string `json:"dockerfileSHA256,omitempty"`
}

type PraetorCustomization struct {
	Bootstrap *BootstrapSpec `json:"bootstrap,omitempty"`
}

// BootstrapArtifact is an exact bounded UTF-8 companion, relative to the config.
// Compressed source is base64-framed so the shared pinned text writer remains valid.
type BootstrapArtifact struct {
	Name    string
	Content []byte
}

type Bundle struct {
	Config    *DevContainer       `json:"config"`
	Artifacts []BootstrapArtifact `json:"-"`
}

func (b *Bundle) Spec() *BootstrapSpec {
	if b == nil || b.Config == nil || b.Config.Customizations == nil || b.Config.Customizations.Praetor == nil {
		return nil
	}
	return b.Config.Customizations.Praetor.Bootstrap
}

// PrepareBundle never executes selected source or writes to the selected repository.
// It snapshots the selected build source twice and refuses a changing capture. Go's test
// surface (_test.go files, testdata directories) is not a build input and is not captured.
func PrepareBundle(ctx context.Context, name string, profiles, facets []string, options BootstrapOptions) (*Bundle, error) {
	if err := validateBootstrapInputs(ctx, name, profiles, facets); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, bootstrapBound)
	defer cancel()
	if options.BuilderImage == "" {
		options.BuilderImage = DefaultBuilderImage
	}
	if options.BaseImage == "" {
		options.BaseImage = DefaultBaseImage
	}
	if err := validateBootstrapImages(options.BuilderImage, options.BaseImage); err != nil {
		return nil, err
	}
	dc, err := SynthesizeFromProfilesWithFeatures(name, profiles, facets, options.Features)
	if err != nil {
		return nil, err
	}
	bundle := &Bundle{Config: dc}
	spec := &BootstrapSpec{Version: bootstrapVersion, State: BootstrapUnavailable, BaseImage: options.BaseImage, Reason: "Select a complete Praetor source root; a config-only catalog cannot bootstrap the CLI."}
	files, err := captureBootstrapSource(ctx, options.SourceRoot)
	if err != nil {
		return nil, err
	}
	if len(files) > 0 {
		if err := bundle.prepareSource(ctx, files, options, spec); err != nil {
			return nil, err
		}
	}
	applyBootstrap(dc, spec)
	return bundle, nil
}

func validateBootstrapInputs(ctx context.Context, name string, profiles, facets []string) error {
	if ctx == nil {
		return errors.New("devcontainer preparation requires context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return validateSynthesisBounds(name, profiles, facets)
}

// validateSynthesisBounds is the single scalar bound on the declared inputs,
// shared by PrepareBundle and by synthesize so both refuse the same input.
func validateSynthesisBounds(name string, profiles, facets []string) error {
	if len(name) > 512 || len(profiles) > MaxLoopLimit || len(facets) > MaxLoopLimit {
		return errors.New("devcontainer name, profile or facet inputs exceed bounds")
	}
	return nil
}

func (b *Bundle) prepareSource(ctx context.Context, files []bootstrapSourceFile, options BootstrapOptions, spec *BootstrapSpec) error {
	archive, err := encodeBootstrapArchive(files)
	if err != nil {
		return err
	}
	repeated, err := captureBootstrapSource(ctx, options.SourceRoot)
	if err != nil {
		return err
	}
	if bootstrapSourceDigest(files) != bootstrapSourceDigest(repeated) {
		return errors.New("praetor source changed during bootstrap capture")
	}
	parts, err := frameBootstrapArchive(archive)
	if err != nil {
		return err
	}
	spec.State = BootstrapReady
	spec.Reason = ""
	spec.BuilderImage = options.BuilderImage
	spec.SourceSHA256 = bootstrapSourceDigest(files)
	spec.ArchiveSHA256 = bootstrapDigest(archive)
	spec.ArchiveParts = len(parts)
	dockerfile := renderBootstrapDockerfile(spec)
	spec.DockerfileSHA256 = bootstrapDigest([]byte(dockerfile))
	b.Artifacts = append(parts, BootstrapArtifact{Name: bootstrapDockerfile, Content: []byte(dockerfile)})
	return nil
}

func applyBootstrap(dc *DevContainer, spec *BootstrapSpec) {
	if dc.Customizations == nil {
		dc.Customizations = &Customizations{}
	}
	copySpec := *spec
	dc.Customizations.Praetor = &PraetorCustomization{Bootstrap: &copySpec}
	dc.Build = nil
	dc.Image = spec.BaseImage
	dc.PostCreateCommand = "printf '%s\\n' 'Praetor bootstrap unavailable: select a complete, verified Praetor source bundle and regenerate this DevContainer.' >&2; exit 1"
	if spec.State == BootstrapReady {
		dc.Image = ""
		dc.Build = &BuildConfig{Dockerfile: bootstrapDockerfile, Context: "."}
		dc.PostCreateCommand = "export PATH=\"/usr/local/bin:$PATH\"; /usr/local/bin/praetorctl compile-context && /usr/bin/make verify-all"
	}
}

func validateBootstrapImages(images ...string) error {
	valid := regexp.MustCompile(`^[a-z0-9][a-z0-9./:_-]{0,220}@sha256:[a-f0-9]{64}$`)
	for _, image := range images {
		if !valid.MatchString(image) {
			return errors.New("bootstrap images require an explicit lowercase repository@sha256 digest")
		}
	}
	return nil
}

// dockerfileRendering selects one rendering of Dockerfile.praetor. A ready bundle records the
// digest of the rendering it was generated with (dockerfileSHA256), so a change to the
// rendering leaves every bundle recorded before it with a digest the current renderer no longer
// produces. Verification requires dockerfileCurrent; reading what a bundle records
// (decodeRecordedBootstrap) also accepts every earlier rendering listed here, so regenerating
// such a bundle still keeps its recorded images and its ready-bootstrap guard.
type dockerfileRendering int

const (
	// dockerfilePiped joined the archive frames and checked the archive digest through pipes.
	// A pipeline's status is its last command's, so a failed cat or echo went unnoticed, and
	// Hadolint reported DL4006 on both lines (#351).
	dockerfilePiped dockerfileRendering = iota
	// dockerfileUnpiped writes each intermediate to a file, so every command's status counts.
	dockerfileUnpiped
	// dockerfileCurrent is the rendering generation writes and verification requires.
	dockerfileCurrent = dockerfileUnpiped
	// oldestDockerfileRendering is the earliest rendering a recorded specification may carry.
	oldestDockerfileRendering = dockerfilePiped
)

// validateBootstrapSpec validates spec as generation writes it and verification requires it:
// a ready specification must record the current Dockerfile rendering.
func validateBootstrapSpec(spec *BootstrapSpec) error {
	return validateBootstrapSpecFrom(spec, dockerfileCurrent)
}

// validateBootstrapSpecFrom validates spec, accepting a recorded Dockerfile digest of any
// rendering from oldest through dockerfileCurrent.
func validateBootstrapSpecFrom(spec *BootstrapSpec, oldest dockerfileRendering) error {
	if spec == nil || spec.Version != bootstrapVersion {
		return errors.New("unsupported or absent bootstrap specification")
	}
	if err := validateBootstrapImages(spec.BaseImage); err != nil {
		return err
	}
	if spec.State == BootstrapUnavailable {
		return validateUnavailableBootstrap(spec)
	}
	if spec.State != BootstrapReady || spec.Reason != "" {
		return errors.New("invalid bootstrap state")
	}
	return validateReadyBootstrap(spec, oldest)
}

func validateReadyBootstrap(spec *BootstrapSpec, oldest dockerfileRendering) error {
	if err := validateBootstrapImages(spec.BuilderImage); err != nil {
		return err
	}
	if spec.ArchiveParts < 1 || spec.ArchiveParts > maxBootstrapParts {
		return errors.New("bootstrap archive part count exceeds bounds")
	}
	for _, digest := range []string{spec.SourceSHA256, spec.ArchiveSHA256, spec.DockerfileSHA256} {
		if !validBootstrapDigest(digest) {
			return errors.New("bootstrap digest must be lowercase sha256")
		}
	}
	if !recordsDockerfileRendering(spec, oldest) {
		return errors.New("bootstrap Dockerfile identity differs from its recorded inputs; " + bundleRepair)
	}
	return nil
}

// recordsDockerfileRendering reports whether spec's dockerfileSHA256 is the digest of its
// Dockerfile at one of the renderings from oldest through dockerfileCurrent.
func recordsDockerfileRendering(spec *BootstrapSpec, oldest dockerfileRendering) bool {
	for rendering := dockerfileCurrent; rendering >= oldest && rendering >= 0; rendering-- {
		if bootstrapDigest([]byte(renderBootstrapDockerfileAs(spec, rendering))) == spec.DockerfileSHA256 {
			return true
		}
	}
	return false
}

// renderBootstrapDockerfile builds praetorctl alone (bootstrapBuildPackage) and installs it
// under its praetorctl and standardsctl names: the postCreateCommand compiles agent context
// and runs the gates, and needs nothing else. A workstation install places more
// (binaryNames, internal/workstation/workstation.go); tribunusctl in particular stays out of
// the image on purpose, because it syncs the operator's model data for the Tribunus router
// and an adopter DevContainer never runs that sync (#377).
func renderBootstrapDockerfile(spec *BootstrapSpec) string {
	return renderBootstrapDockerfileAs(spec, dockerfileCurrent)
}

// renderBootstrapDockerfileAs renders spec's Dockerfile at one rendering.
func renderBootstrapDockerfileAs(spec *BootstrapSpec, rendering dockerfileRendering) string {
	var s strings.Builder
	fmt.Fprintf(&s, "# Praetor bootstrap v1; selected source digest %s\nFROM %s AS praetor_build\nWORKDIR /praetor-source\n", spec.SourceSHA256, spec.BuilderImage)
	for i := 0; i < spec.ArchiveParts && i < maxBootstrapParts; i++ {
		fmt.Fprintf(&s, "COPY [\"%s\", \"/tmp/praetor-source/%03d.b64\"]\n", bootstrapPartName(i), i)
	}
	s.WriteString(bootstrapArchiveSteps(strings.TrimPrefix(spec.ArchiveSHA256, "sha256:"), rendering))
	s.WriteString("ENV GOTOOLCHAIN=local CGO_ENABLED=0 GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org\nRUN sha256sum go.mod go.sum > /tmp/praetor-modules.sha256 && /usr/local/go/bin/go mod download && /usr/local/go/bin/go mod verify && sha256sum -c /tmp/praetor-modules.sha256\n")
	s.WriteString("RUN /usr/local/go/bin/go build -mod=readonly -trimpath -buildvcs=false -o /out/praetorctl ./" + bootstrapBuildPackage + "\n")
	fmt.Fprintf(&s, "FROM %s\nCOPY --from=praetor_build --chmod=0444 /praetor-source/LICENSE /usr/local/share/praetor/LICENSE\nCOPY --from=praetor_build --chmod=0555 /out/praetorctl /usr/local/bin/praetorctl\nCOPY --from=praetor_build --chmod=0555 /out/praetorctl /usr/local/bin/standardsctl\nUSER vscode\n", spec.BaseImage)
	return s.String()
}

// bootstrapArchiveSteps renders the RUN lines that join the archive frames, check the archive
// against sum and unpack it. The current rendering has no pipe, so no step can hide a failure
// behind a later command's success (#351). It sets no pipefail SHELL instead, because the
// builder image is the adopter's choice (--builder-image, kept on regeneration) and the
// pipefail shells Hadolint DL4006 accepts, /bin/ash and /bin/bash, are each missing from one
// image family: Alpine ships no bash, Debian no ash. Writing each intermediate to a file works
// in any POSIX shell. TestBootstrapDockerfilePipesFollowPipefailShell holds any later pipe in
// the rendering to a preceding pipefail SHELL.
func bootstrapArchiveSteps(sum string, rendering dockerfileRendering) string {
	if rendering == dockerfilePiped {
		return fmt.Sprintf("RUN cat /tmp/praetor-source/*.b64 | base64 -d > /tmp/praetor-source.tar.gz\n"+
			"RUN echo '%s  /tmp/praetor-source.tar.gz' | sha256sum -c - && tar -xzf /tmp/praetor-source.tar.gz -C /praetor-source\n", sum)
	}
	return fmt.Sprintf("RUN cat /tmp/praetor-source/*.b64 > /tmp/praetor-source.b64 && base64 -d /tmp/praetor-source.b64 > /tmp/praetor-source.tar.gz\n"+
		"RUN echo '%s  /tmp/praetor-source.tar.gz' > /tmp/praetor-source.sha256 && sha256sum -c /tmp/praetor-source.sha256 && tar -xzf /tmp/praetor-source.tar.gz -C /praetor-source\n", sum)
}

func validateUnavailableBootstrap(spec *BootstrapSpec) error {
	if spec.Reason == "" || len(spec.Reason) > 512 || spec.BuilderImage != "" || spec.SourceSHA256 != "" || spec.ArchiveSHA256 != "" || spec.ArchiveParts != 0 || spec.DockerfileSHA256 != "" {
		return errors.New("invalid unavailable bootstrap metadata")
	}
	return nil
}

var ErrBootstrapUnavailable = errors.New("devcontainer bootstrap unavailable")
