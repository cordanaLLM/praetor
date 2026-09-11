package supplychain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
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
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return nil, fmt.Errorf("read go.mod: %w", err)
	}

	components, err := parseGoModComponents(string(data))
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
				Name:    "praetor",
				Version: "v1.0.0",
			},
		},
		Components: components,
	}, nil
}

func parseGoModComponents(content string) ([]Component, error) {
	var components []Component
	lines := strings.Split(content, "\n")
	inRequire := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "require (" {
			inRequire = true
			continue
		}
		if inRequire && trimmed == ")" {
			inRequire = false
			continue
		}
		if comp, ok := parseRequireLine(trimmed, inRequire); ok {
			components = append(components, comp)
		}
	}
	return components, nil
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
