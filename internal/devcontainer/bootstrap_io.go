package devcontainer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

type bootstrapWrite struct {
	path           string
	data, expected []byte
	exists         bool
}

// WriteBundle preflights every exact companion and publishes the JSON last.
// Existing differing files require explicit force; unrelated files remain intact.
func WriteBundle(ctx context.Context, path string, bundle *Bundle, force bool) error {
	if ctx == nil {
		return errors.New("bootstrap write requires context")
	}
	ctx, cancel := context.WithTimeout(ctx, contextopt.MaxDuration)
	defer cancel()
	if err := validateBundleContents(ctx, bundle); err != nil {
		return err
	}
	data, err := Render(bundle.Config)
	if err != nil {
		return err
	}
	writes, err := planBootstrapWrites(path, bundle, data)
	if err != nil {
		return err
	}
	if err := observeBootstrapWrites(ctx, writes, force); err != nil {
		return err
	}
	if err := contextopt.EnsureDirectory(ctx, filepath.Dir(path), 0755); err != nil {
		return err
	}
	for _, write := range writes {
		if write.exists && bytes.Equal(write.data, write.expected) {
			continue
		}
		if err := contextopt.ReplaceSnapshot(ctx, write.path, write.data, contextopt.ReplaceOptions{Expected: write.expected, Exists: write.exists, Mode: 0644}); err != nil {
			return err
		}
	}
	return nil
}

func observeBootstrapWrites(ctx context.Context, writes []bootstrapWrite, force bool) error {
	for i := range writes {
		data, exists, err := contextopt.ObserveSnapshot(ctx, writes[i].path)
		if err != nil {
			return err
		}
		if exists && !bytes.Equal(data, writes[i].data) && !force {
			return fmt.Errorf("preserving existing DevContainer artifact %s; review it before explicit replacement", writes[i].path)
		}
		writes[i].expected = data
		writes[i].exists = exists
	}
	return nil
}

func validateBundleContents(ctx context.Context, bundle *Bundle) error {
	spec := bundle.Spec()
	if err := validateBootstrapSpec(spec); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateBootstrapProjection(bundle.Config, spec); err != nil {
		return err
	}
	if spec.State == BootstrapUnavailable {
		if len(bundle.Artifacts) != 0 {
			return errors.New("unavailable bootstrap carries unexpected companions")
		}
		return nil
	}
	if len(bundle.Artifacts) != spec.ArchiveParts+1 {
		return errors.New("bootstrap companion set is incomplete")
	}
	artifacts, err := bootstrapArtifactMap(bundle.Artifacts)
	if err != nil {
		return err
	}
	dockerfile := artifacts[bootstrapDockerfile]
	if !bytes.Equal(dockerfile, []byte(renderBootstrapDockerfile(spec))) {
		return errors.New("bootstrap Dockerfile differs from its recorded inputs")
	}
	return validateArchivedSource(ctx, spec, artifacts)
}

func validateArchivedSource(ctx context.Context, spec *BootstrapSpec, artifacts map[string][]byte) error {
	archive, err := collectBootstrapArchive(spec, artifacts)
	if err != nil {
		return err
	}
	files, err := decodeBootstrapArchive(ctx, archive)
	if err != nil {
		return err
	}
	if bootstrapSourceDigest(files) != spec.SourceSHA256 {
		return errors.New("bootstrap archive source identity mismatch")
	}
	return nil
}

func collectBootstrapArchive(spec *BootstrapSpec, files map[string][]byte) ([]byte, error) {
	var encoded []byte
	for i := 0; i < spec.ArchiveParts && i < maxBootstrapParts; i++ {
		data := files[bootstrapPartName(i)]
		if len(data) == 0 || len(data) > bootstrapPartBytes || (i < spec.ArchiveParts-1 && len(data) != bootstrapPartBytes) {
			return nil, errors.New("bootstrap archive frame missing or out of bounds")
		}
		encoded = append(encoded, data...)
	}
	archive, err := base64.StdEncoding.Strict().DecodeString(string(encoded))
	if err != nil {
		return nil, err
	}
	if bootstrapDigest(archive) != spec.ArchiveSHA256 {
		return nil, errors.New("bootstrap archive digest mismatch")
	}
	return archive, nil
}

func verifyRecordedBootstrap(ctx context.Context, path string, raw []byte, actual, expected *DevContainer) error {
	spec := (&Bundle{Config: actual}).Spec()
	if err := validateBootstrapSpec(spec); err != nil {
		return err
	}
	expectedCopy, err := expectedBootstrapConfig(expected, spec)
	if err != nil {
		return err
	}
	expectedBytes, err := Render(expectedCopy)
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, expectedBytes) {
		return errors.New("recorded bootstrap configuration differs from declared profiles or contains unrecognized edits")
	}
	artifacts, err := readBootstrapCompanions(ctx, path, spec)
	if err != nil {
		return err
	}
	bundle := &Bundle{Config: actual, Artifacts: artifacts}
	if err := validateBundleContents(ctx, bundle); err != nil {
		return err
	}
	if spec.State == BootstrapUnavailable {
		return fmt.Errorf("%w: %s", ErrBootstrapUnavailable, spec.Reason)
	}
	return nil
}

func readBootstrapConfig(ctx context.Context, path string) ([]byte, *DevContainer, error) {
	data, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read devcontainer file %s: %w", path, err)
	}
	var dc DevContainer
	if err := json.Unmarshal(data, &dc); err != nil {
		return nil, nil, err
	}
	return data, &dc, nil
}

func validateBootstrapProjection(dc *DevContainer, spec *BootstrapSpec) error {
	expected := *dc
	custom := *dc.Customizations
	expected.Customizations = &custom
	applyBootstrap(&expected, spec)
	actualData, err := Render(dc)
	if err != nil {
		return err
	}
	expectedData, err := Render(&expected)
	if err != nil {
		return err
	}
	if !bytes.Equal(actualData, expectedData) {
		return errors.New("bootstrap-owned image, build or startup fields were modified")
	}
	return nil
}

func planBootstrapWrites(path string, bundle *Bundle, data []byte) ([]bootstrapWrite, error) {
	if len(data) > contextopt.MaxSourceBytes {
		return nil, errors.New("bootstrap JSON exceeds byte bound")
	}
	writes := make([]bootstrapWrite, 0, len(bundle.Artifacts)+1)
	for _, artifact := range bundle.Artifacts {
		target := filepath.Join(filepath.Dir(path), artifact.Name)
		if filepath.Clean(target) == filepath.Clean(path) {
			return nil, errors.New("bootstrap config collides with its companion")
		}
		writes = append(writes, bootstrapWrite{path: target, data: bytes.Clone(artifact.Content)})
	}
	writes = append(writes, bootstrapWrite{path: path, data: data})
	return writes, nil
}

func bootstrapArtifactMap(files []BootstrapArtifact) (map[string][]byte, error) {
	artifacts := make(map[string][]byte)
	for _, file := range files {
		if _, exists := artifacts[file.Name]; exists {
			return nil, errors.New("duplicate bootstrap companion")
		}
		if len(file.Content) == 0 || len(file.Content) > contextopt.MaxSourceBytes {
			return nil, errors.New("bootstrap companion exceeds byte bounds")
		}
		artifacts[file.Name] = file.Content
	}
	return artifacts, nil
}

func expectedBootstrapConfig(expected *DevContainer, spec *BootstrapSpec) (*DevContainer, error) {
	if expected == nil {
		return nil, errors.New("expected DevContainer is absent")
	}
	expectedCopy := *expected
	if expected.Customizations != nil {
		custom := *expected.Customizations
		expectedCopy.Customizations = &custom
	}
	selected := (&Bundle{Config: expected}).Spec()
	if selected == nil {
		selected = spec
	}
	applyBootstrap(&expectedCopy, selected)
	return &expectedCopy, nil
}

func readBootstrapCompanions(ctx context.Context, path string, spec *BootstrapSpec) ([]BootstrapArtifact, error) {
	var artifacts []BootstrapArtifact
	if spec.State == BootstrapReady {
		for i := 0; i < spec.ArchiveParts+1 && i < maxBootstrapParts+1; i++ {
			name := bootstrapDockerfile
			if i < spec.ArchiveParts {
				name = bootstrapPartName(i)
			}
			data, err := contextopt.ReadSnapshot(ctx, filepath.Join(filepath.Dir(path), name))
			if err != nil {
				return nil, err
			}
			artifacts = append(artifacts, BootstrapArtifact{Name: name, Content: data})
		}
	}
	return artifacts, nil
}
