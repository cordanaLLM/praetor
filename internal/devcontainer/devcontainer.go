package devcontainer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// Invariant bounds and defaults.
const (
	MaxLoopLimit          = 1000
	DefaultRemoteUser     = "vscode"
	DefaultGoVersion      = "1.27"
	DefaultContextDir     = ".."
	DefaultDockerfilePath = "../docker/dev/Dockerfile"
	GoFeatureRef          = "ghcr.io/devcontainers/features/go:1"
	CommonUtilsFeature    = "ghcr.io/devcontainers/features/common-utils:2"
	NativeGPUProfile      = "native-gpu-systems"
)

// BuildConfig defines the container build context and dockerfile location.
type BuildConfig struct {
	Dockerfile string            `json:"dockerfile"`
	Context    string            `json:"context"`
	Args       map[string]string `json:"args,omitempty"`
}

// VSCodeCustomization specifies IDE extensions and settings for VS Code.
type VSCodeCustomization struct {
	Extensions []string               `json:"extensions"`
	Settings   map[string]interface{} `json:"settings"`
}

// Customizations encapsulates vendor/IDE specific configurations.
type Customizations struct {
	Praetor *PraetorCustomization `json:"praetor,omitempty"`
	VSCode  *VSCodeCustomization  `json:"vscode,omitempty"`
}

// DevContainer represents the standardized .devcontainer/devcontainer.json schema.
type DevContainer struct {
	Image             string                 `json:"image,omitempty"`
	Name              string                 `json:"name"`
	Build             *BuildConfig           `json:"build,omitempty"`
	Features          map[string]interface{} `json:"features,omitempty"`
	Customizations    *Customizations        `json:"customizations,omitempty"`
	RemoteUser        string                 `json:"remoteUser,omitempty"`
	PostCreateCommand string                 `json:"postCreateCommand,omitempty"`
	ForwardPorts      []int                  `json:"forwardPorts,omitempty"`
}

// Synthesize produces the legacy profile baseline for checking existing configs.
// New portable generation uses PrepareBundle with explicit bootstrap inputs.
func Synthesize(m *config.Manifest) (*DevContainer, error) {
	if m == nil {
		return nil, errors.New("manifest cannot be nil")
	}

	repoName := m.Repository.Name
	if repoName == "" {
		repoName = "workspace"
	}
	if m.Repository.Owner != "" {
		repoName = m.Repository.Owner + "/" + repoName
	}

	return SynthesizeFromProfiles(repoName, m.Profiles, m.Facets)
}

// SynthesizeWithFeatures applies the selected catalog feature set to a manifest.
func SynthesizeWithFeatures(m *config.Manifest, selected []config.DevContainerFeature) (*DevContainer, error) {
	if m == nil {
		return nil, errors.New("manifest cannot be nil")
	}
	repoName := m.Repository.Name
	if repoName == "" {
		repoName = "workspace"
	}
	if m.Repository.Owner != "" {
		repoName = m.Repository.Owner + "/" + repoName
	}
	return SynthesizeFromProfilesWithFeatures(repoName, m.Profiles, m.Facets, selected)
}

// SynthesizeFromProfiles returns the legacy profile baseline. New writers must
// use PrepareBundle so adopted repositories receive complete bootstrap artifacts.
func SynthesizeFromProfiles(name string, profiles []string, facets []string) (*DevContainer, error) {
	return synthesize(name, profiles, facets, nil)
}

// SynthesizeFromProfilesWithFeatures synthesizes using only features resolved
// from the selected, pinned catalog entries.
func SynthesizeFromProfilesWithFeatures(name string, profiles, facets []string, selected []config.DevContainerFeature) (*DevContainer, error) {
	return synthesize(name, profiles, facets, selected)
}

func synthesize(name string, profiles []string, facets []string, selected []config.DevContainerFeature) (*DevContainer, error) {
	if len(selected) > MaxLoopLimit {
		return nil, errors.New("selected DevContainer features exceed bounds")
	}
	containerName := strings.TrimSpace(name)
	if containerName == "" {
		containerName = "workspace"
	}

	features := synthesizeFeatures(selected, profiles, facets)
	extensions := synthesizeExtensions(profiles, facets, selected)
	settings := synthesizeSettings(profiles, facets, selected)
	postCmd := synthesizePostCreateCommand(profiles)

	dc := &DevContainer{
		Name: containerName,
		Build: &BuildConfig{
			Dockerfile: DefaultDockerfilePath,
			Context:    DefaultContextDir,
		},
		Features: features,
		Customizations: &Customizations{
			VSCode: &VSCodeCustomization{
				Extensions: extensions,
				Settings:   settings,
			},
		},
		RemoteUser:        DefaultRemoteUser,
		PostCreateCommand: postCmd,
	}

	return dc, nil
}

// synthesizeFeatures resolves devcontainer features for profiles and facets.
func synthesizeFeatures(selected []config.DevContainerFeature, profiles []string, facets []string) map[string]interface{} {
	features := make(map[string]interface{})
	if selected != nil {
		for i := 0; i < len(selected) && i < MaxLoopLimit; i++ {
			features[selected[i].Ref] = selected[i].Options
		}
		return features
	}

	// The Go feature follows the declared profiles: the native toolchain rejects
	// Go tooling in its extensions and settings, so it receives no Go runtime.
	if !hasNativeGPUProfile(profiles) {
		features[GoFeatureRef] = map[string]interface{}{
			"version": DefaultGoVersion,
		}
	}

	for i := 0; i < len(facets) && i < MaxLoopLimit; i++ {
		f := strings.ToLower(strings.TrimSpace(facets[i]))
		if f == "security:high" {
			features[CommonUtilsFeature] = map[string]interface{}{
				"installZsh":      false,
				"upgradePackages": true,
			}
		}
	}

	return features
}

// hasNativeGPUProfile reports whether the declared profiles select the native
// toolchain, whose container carries C/C++ tooling instead of Go.
func hasNativeGPUProfile(profiles []string) bool {
	for i := 0; i < len(profiles) && i < MaxLoopLimit; i++ {
		if strings.ToLower(strings.TrimSpace(profiles[i])) == NativeGPUProfile {
			return true
		}
	}
	return false
}

// synthesizeExtensions constructs deduplicated IDE extensions based on profiles and facets.
func synthesizeExtensions(profiles []string, facets []string, selected []config.DevContainerFeature) []string {
	extList := []string{
		"GitHub.vscode-pull-request-github",
		"eamodio.gitlens",
	}

	if hasNativeGPUProfile(profiles) {
		extList = append(extList,
			"llvm-vs-code-extensions.vscode-clangd",
			"mesonbuild.mesonbuild",
			"ms-vscode.cmake-tools",
			"ms-python.python",
		)
	} else if shouldAddGoTooling(selected) {
		extList = append(extList, "golang.go")
	}

	for i := 0; i < len(facets) && i < MaxLoopLimit; i++ {
		f := strings.ToLower(strings.TrimSpace(facets[i]))
		switch f {
		case "security:high":
			extList = append(extList, "aquasecurity.trivy-vulnerability-scanner")
		case "api:public-contract":
			extList = append(extList, "redhat.vscode-yaml", "ms-azuretools.vscode-docker")
		case "docs:seo-portal":
			extList = append(extList, "davidanson.vscode-markdownlint")
		case "agent:sandboxed":
			extList = append(extList, "github.copilot")
		}
	}

	return dedupeAndSort(extList)
}

// synthesizeSettings builds standard and facet-driven editor settings.
func synthesizeSettings(profiles []string, facets []string, selected []config.DevContainerFeature) map[string]interface{} {
	settings := map[string]interface{}{
		"editor.formatOnSave": true,
	}

	if hasNativeGPUProfile(profiles) {
		settings["clangd.path"] = "clangd"
		settings["clangd.arguments"] = []string{
			"--compile-commands-dir=core/build",
			"--header-insertion=never",
		}
	} else if shouldAddGoTooling(selected) {
		settings["go.toolsManagement.autoUpdate"] = true
		settings["go.useLanguageServer"] = true
		settings["go.lintTool"] = "golangci-lint"
		settings["go.lintOnSave"] = "package"
	}

	for i := 0; i < len(facets) && i < MaxLoopLimit; i++ {
		f := strings.ToLower(strings.TrimSpace(facets[i]))
		if f == "agent:sandboxed" {
			settings["security.workspace.trust.enabled"] = true
		}
	}

	return settings
}

func hasGoFeature(selected []config.DevContainerFeature) bool {
	for i := 0; i < len(selected) && i < MaxLoopLimit; i++ {
		if strings.HasSuffix(selected[i].Identity(), "/go") {
			return true
		}
	}
	return false
}

func shouldAddGoTooling(selected []config.DevContainerFeature) bool {
	return selected == nil || hasGoFeature(selected)
}

// synthesizePostCreateCommand determines appropriate startup command. Profiles
// is a list, so framework and native-gpu-systems can both be declared; the Go
// startup command is emitted only when the same gate that selects the Go
// feature, extensions and settings also selected them, because a container
// built without a Go toolchain cannot run "go run ./cmd/standardsctl".
func synthesizePostCreateCommand(profiles []string) string {
	if hasNativeGPUProfile(profiles) {
		return "make verify-all"
	}
	for i := 0; i < len(profiles) && i < MaxLoopLimit; i++ {
		p := strings.ToLower(strings.TrimSpace(profiles[i]))
		if p == "framework" {
			return "go run ./cmd/standardsctl compile-context && make verify-all"
		}
	}
	return "make verify-all"
}

// dedupeAndSort removes duplicates and sorts extensions alphabetically.
func dedupeAndSort(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	result := make([]string, 0, len(items))

	for i := 0; i < len(items) && i < MaxLoopLimit; i++ {
		item := strings.TrimSpace(items[i])
		if item == "" {
			continue
		}
		if _, ok := seen[item]; !ok {
			seen[item] = struct{}{}
			result = append(result, item)
		}
	}

	sort.Strings(result)
	return result
}

// Render marshals the DevContainer structure to indented JSON without HTML escaping.
func Render(dc *DevContainer) ([]byte, error) {
	if dc == nil {
		return nil, errors.New("devcontainer configuration cannot be nil")
	}

	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")

	if err := enc.Encode(dc); err != nil {
		return nil, fmt.Errorf("failed to marshal devcontainer configuration: %w", err)
	}

	return []byte(buf.String()), nil
}

// WriteDevContainer writes the rendered devcontainer configuration with context timeout.
func WriteDevContainer(ctx context.Context, path string, dc *DevContainer) error {
	if ctx == nil {
		return errors.New("context cannot be nil")
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("context cancelled prior to writing devcontainer: %w", ctx.Err())
	default:
	}

	if (&Bundle{Config: dc}).Spec() != nil {
		return errors.New("recorded bootstrap configs require WriteBundle and exact companions")
	}

	data, err := Render(dc)
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := contextopt.EnsureDirectory(ctx, dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	before, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// The generated configuration remains publicly readable; existing stricter modes stay intact.
	if err := contextopt.ReplaceSnapshot(ctx, path, data, contextopt.ReplaceOptions{Expected: before, Exists: err == nil, Mode: 0644}); err != nil {
		return fmt.Errorf("failed to write devcontainer file %s: %w", path, err)
	}

	return nil
}

// LoadDevContainer reads and unmarshals a devcontainer configuration with context timeout.
func LoadDevContainer(ctx context.Context, path string) (*DevContainer, error) {
	if ctx == nil {
		return nil, errors.New("context cannot be nil")
	}
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("context cancelled prior to reading devcontainer: %w", ctx.Err())
	default:
	}

	data, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("failed to read devcontainer file %s: %w", path, err)
	}

	// The same decoder readBootstrapConfig uses. Two decoders for one schema
	// drift apart the next time the schema changes (HISS-19).
	return decodeManagedConfig(data, path)
}

// Verify validates that the devcontainer file at path matches expected configuration.
func Verify(ctx context.Context, path string, expected *DevContainer) error {
	raw, actual, err := readBootstrapConfig(ctx, path)
	if err != nil {
		return err
	}
	if (&Bundle{Config: actual}).Spec() != nil {
		return verifyRecordedBootstrap(ctx, path, raw, actual, expected)
	}

	identical, err := rendersExactly(raw, expected)
	if err != nil {
		return err
	}
	if !identical {
		return fmt.Errorf("devcontainer at %s does not match expected configuration", path)
	}

	return verifyLegacyBootstrapInputs(ctx, path, actual)
}
