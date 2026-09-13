package flavor

import (
	"fmt"
	"sync"

	"github.com/cordanaLLM/praetor/internal/util"
)

// TemplateItem defines a template file required by a flavor.
type TemplateItem struct {
	Path        string                                     `json:"path"`
	Description string                                     `json:"description"`
	ContentFunc func(repoName string, owner string) string `json:"-"`
}

// SettingItem defines a configuration setting required by a flavor.
type SettingItem struct {
	Name        string                    `json:"name"`
	Path        string                    `json:"path"`
	Description string                    `json:"description"`
	Validator   func(content []byte) bool `json:"-"`
}

// ToolchainItem defines an external CLI tool or compiler required by a flavor.
type ToolchainItem struct {
	Binary       string `json:"binary"`
	Purpose      string `json:"purpose"`
	InstallGuide string `json:"install_guide"`
}

// Flavor represents an authoritative repository engineering archetype.
type Flavor interface {
	Name() string
	Description() string
	Detect(repoPath string) bool
	RequiredTemplates() []TemplateItem
	RequiredSettings() []SettingItem
	RequiredToolchains() []ToolchainItem
	HISSProfile() string
}

var (
	registryMu sync.RWMutex
	registry   = builtinFlavors()
)

// Register registers a flavor archetype into the global registry.
func Register(f Flavor) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[f.Name()] = f
}

// Get returns the flavor by name, or an error if unregistered.
func Get(name string) (Flavor, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	f, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown flavor: %q", name)
	}
	return f, nil
}

// List returns all registered flavors.
func List() []Flavor {
	registryMu.RLock()
	defer registryMu.RUnlock()
	res := make([]Flavor, 0, len(registry))
	for _, f := range registry {
		res = append(res, f)
	}
	return res
}

// DetectFlavor inspects repository markers and returns the best-matching flavor name.
func DetectFlavor(repoPath string) string {
	registryMu.RLock()
	defer registryMu.RUnlock()

	// High-precedence checks
	for _, name := range []string{
		"native-gpu-systems",
		"rust-systems",
		"frontend-svelte",
		"typescript-node",
		"python-ml",
		"jvm-service",
		"mobile-flutter",
		"go-service",
		"go-library",
		"infra-k8s",
		"agentic-autonomous",
	} {
		if f, ok := registry[name]; ok && f.Detect(repoPath) {
			return f.Name()
		}
	}

	// Fallback to any matching flavor
	for _, f := range registry {
		if f.Detect(repoPath) {
			return f.Name()
		}
	}
	return "go-library"
}

// CheckFileExists is an internal helper for flavor detection.
func CheckFileExists(path string) bool {
	return util.PathExists(path)
}
