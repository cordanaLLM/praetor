package devcontainer

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
)

const (
	BootstrapReady       = "source-bundle"
	BootstrapUnavailable = "unavailable"
	bootstrapVersion     = 1
	bootstrapDockerfile  = "Dockerfile.praetor"
	maxBootstrapParts    = 4
	bootstrapPartBytes   = 512 * 1024
	DefaultBuilderImage  = "docker.io/library/golang:1.27-alpine@sha256:4cb7ac979db5fcc41cae44b2227ba5ab8a51e8807f40d9ba4dee20a0ad960b5b"
	// The 26.04 tag drops the hyphen the 24.04 and earlier tags carried:
	// mcr.microsoft.com/devcontainers/base publishes "ubuntu26.04", and
	// "ubuntu-26.04" is not a tag on that repository. The digest is what the
	// bundle actually pulls; the tag is read by humans.
	DefaultBaseImage = "mcr.microsoft.com/devcontainers/base:ubuntu26.04@sha256:edfb983aab9c579a385dc23c57d7d3703f5ec920124d99c16204a2cac465aab4"
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
	ctx, cancel := context.WithTimeout(ctx, contextopt.MaxDuration)
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

func validateBootstrapSpec(spec *BootstrapSpec) error {
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
	return validateReadyBootstrap(spec)
}

func validateReadyBootstrap(spec *BootstrapSpec) error {
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
	if bootstrapDigest([]byte(renderBootstrapDockerfile(spec))) != spec.DockerfileSHA256 {
		return errors.New("bootstrap Dockerfile identity differs from its recorded inputs")
	}
	return nil
}

func renderBootstrapDockerfile(spec *BootstrapSpec) string {
	var s strings.Builder
	fmt.Fprintf(&s, "# Praetor bootstrap v1; selected source digest %s\nFROM %s AS praetor_build\nWORKDIR /praetor-source\n", spec.SourceSHA256, spec.BuilderImage)
	for i := 0; i < spec.ArchiveParts && i < maxBootstrapParts; i++ {
		fmt.Fprintf(&s, "COPY [\"%s\", \"/tmp/praetor-source/%03d.b64\"]\n", bootstrapPartName(i), i)
	}
	fmt.Fprintf(&s, "RUN cat /tmp/praetor-source/*.b64 | base64 -d > /tmp/praetor-source.tar.gz\nRUN echo '%s  /tmp/praetor-source.tar.gz' | sha256sum -c - && tar -xzf /tmp/praetor-source.tar.gz -C /praetor-source\n", strings.TrimPrefix(spec.ArchiveSHA256, "sha256:"))
	s.WriteString("ENV GOTOOLCHAIN=local CGO_ENABLED=0 GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org\nRUN sha256sum go.mod go.sum > /tmp/praetor-modules.sha256 && /usr/local/go/bin/go mod download && /usr/local/go/bin/go mod verify && sha256sum -c /tmp/praetor-modules.sha256\n")
	s.WriteString("RUN /usr/local/go/bin/go build -mod=readonly -trimpath -buildvcs=false -o /out/praetorctl ./cmd/standardsctl\n")
	fmt.Fprintf(&s, "FROM %s\nCOPY --from=praetor_build --chmod=0444 /praetor-source/LICENSE /usr/local/share/praetor/LICENSE\nCOPY --from=praetor_build --chmod=0555 /out/praetorctl /usr/local/bin/praetorctl\nCOPY --from=praetor_build --chmod=0555 /out/praetorctl /usr/local/bin/standardsctl\nUSER vscode\n", spec.BaseImage)
	return s.String()
}

func validateUnavailableBootstrap(spec *BootstrapSpec) error {
	if spec.Reason == "" || len(spec.Reason) > 512 || spec.BuilderImage != "" || spec.SourceSHA256 != "" || spec.ArchiveSHA256 != "" || spec.ArchiveParts != 0 || spec.DockerfileSHA256 != "" {
		return errors.New("invalid unavailable bootstrap metadata")
	}
	return nil
}

var ErrBootstrapUnavailable = errors.New("devcontainer bootstrap unavailable")
