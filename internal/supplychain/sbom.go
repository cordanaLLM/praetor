package supplychain

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/gomanifest"
)

// CycloneDXBOM represents a lightweight CycloneDX 1.5 Software Bill of Materials.
type CycloneDXBOM struct {
	BOMFormat    string      `json:"bomFormat"`
	SpecVersion  string      `json:"specVersion"`
	SerialNumber string      `json:"serialNumber"`
	Version      int         `json:"version"`
	Metadata     BOMMetadata `json:"metadata"`
	Components   []Component `json:"components"`
}

// BOMMetadata contains metadata about the bill of materials.
type BOMMetadata struct {
	Timestamp string    `json:"timestamp"`
	Component Component `json:"component"`
}

// Component represents an individual software library or module.
type Component struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Version string `json:"version"`
	PURL    string `json:"purl,omitempty"`
}

// GenerateCycloneDX parses the repository dependencies into a CycloneDX SBOM.
func GenerateCycloneDX(ctx context.Context, repoDir string) (*CycloneDXBOM, error) {
	if ctx == nil {
		return nil, fmt.Errorf("supplychain: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("supplychain: context cancelled: %w", err)
	}

	goModPath := filepath.Join(repoDir, "go.mod")
	data, err := contextopt.ReadSnapshot(ctx, goModPath)
	if err != nil {
		return nil, fmt.Errorf("read go.mod: %w", err)
	}

	content := string(data)
	modulePath, ok := resolveModulePath(content)
	if !ok {
		return nil, fmt.Errorf("go.mod at %s has no module directive", goModPath)
	}

	components, err := parseGoModComponents(content)
	if err != nil {
		return nil, fmt.Errorf("parse components: %w", err)
	}

	return &CycloneDXBOM{
		BOMFormat:    "CycloneDX",
		SpecVersion:  "1.5",
		SerialNumber: fmt.Sprintf("urn:uuid:%d", time.Now().UnixNano()),
		Version:      1,
		Metadata: BOMMetadata{
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Component: Component{
				Type:    "application",
				Name:    modulePath,
				Version: resolveModuleVersion(),
			},
		},
		Components: components,
	}, nil
}

// resolveModulePath reads the module directive out of a go.mod's content, instead of a
// name fixed at compile time that is wrong for every repository but the one it was written
// against.
func resolveModulePath(content string) (string, bool) {
	for _, line := range strings.Split(content, "\n") {
		if path, ok := gomanifest.ModulePath(line); ok {
			return path, true
		}
	}
	return "", false
}

// resolveModuleVersion reports the running binary's module version, so the BOM's metadata
// component matches what was actually built instead of a version string that drifts out of
// sync with every release. "(devel)" is what debug.ReadBuildInfo reports for a build outside
// a tagged module (go run, go test, a local go build) and is returned as-is: still accurate,
// just uninformative, and a fabricated version would be worse.
func resolveModuleVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return "(devel)"
	}
	return info.Main.Version
}

// replacement is one go.mod replace directive's right-hand side.
type replacement struct {
	path    string
	version string
}

func parseGoModComponents(content string) ([]Component, error) {
	var components []Component
	lines := strings.Split(content, "\n")
	inRequire, inReplace := false, false
	replacements := make(map[string]replacement)

	for _, line := range lines {
		if requirement, required := gomanifest.RequirementLine(line, &inRequire); required {
			if comp, ok := parseRequireLine(requirement, true); ok {
				components = append(components, comp)
			}
			continue
		}
		if replaceLine, isReplace := gomanifest.ReplaceLine(line, &inReplace); isReplace {
			if oldPath, newPath, newVersion, ok := gomanifest.ParseReplaceDirective(replaceLine); ok {
				replacements[oldPath] = replacement{path: newPath, version: newVersion}
			}
		}
	}

	applyReplacements(components, replacements)
	return components, nil
}

// applyReplacements rewrites every component whose module path a go.mod replace directive
// names, so the SBOM records what actually builds rather than the pre-replacement require
// line. A local filesystem replacement (no version) clears the PURL: a coordinate-based
// package URL cannot name a path that only makes sense on the machine that built it.
func applyReplacements(components []Component, replacements map[string]replacement) {
	for i := range components {
		rep, ok := replacements[components[i].Name]
		if !ok {
			continue
		}
		components[i].Name = rep.path
		components[i].Version = rep.version
		components[i].PURL = ""
		if rep.version != "" {
			components[i].PURL = fmt.Sprintf("pkg:golang/%s@%s", rep.path, rep.version)
		}
	}
}

func parseRequireLine(line string, inRequire bool) (Component, bool) {
	if strings.HasPrefix(line, "require ") && !inRequire {
		line = strings.TrimPrefix(line, "require ")
	} else if !inRequire {
		return Component{}, false
	}

	fields := strings.Fields(line)
	if len(fields) >= 2 {
		return Component{
			Type:    "library",
			Name:    fields[0],
			Version: fields[1],
			PURL:    fmt.Sprintf("pkg:golang/%s@%s", fields[0], fields[1]),
		}, true
	}
	return Component{}, false
}
