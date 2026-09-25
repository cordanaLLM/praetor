package supplychain

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/gomanifest"
	"github.com/cordanaLLM/praetor/internal/semver"
	"github.com/cordanaLLM/praetor/internal/util"
)

// maxGitProbeBytes bounds the output of each Git probe that resolves the scanned
// module's release tag (HISS-02).
const maxGitProbeBytes = 4096

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

// Component represents an individual software library or module. Version is omitted
// when it is unknown, which CycloneDX permits, rather than filled with a guess.
type Component struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	PURL    string `json:"purl,omitempty"`
}

// SBOMOptions configures GenerateCycloneDX.
type SBOMOptions struct {
	// ModuleVersion is the scanned module's version to record in the BOM metadata. When
	// empty, the release tag on the checkout's HEAD is used, and the version is omitted
	// when HEAD carries none.
	ModuleVersion string
}

// GenerateCycloneDX parses the repository dependencies into a CycloneDX SBOM whose
// metadata component is the module at repoDir: its name comes from repoDir's go.mod
// and its version from opts or repoDir's own release tag, never from the binary that
// generates the BOM.
func GenerateCycloneDX(ctx context.Context, repoDir string, opts SBOMOptions) (*CycloneDXBOM, error) {
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
	version, err := resolveModuleVersion(ctx, repoDir, opts.ModuleVersion)
	if err != nil {
		return nil, fmt.Errorf("resolve module version: %w", err)
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
				Version: version,
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

// resolveModuleVersion reports the version of the module at repoDir: the explicit
// version when one is given, otherwise the release tag on the checkout's HEAD. It
// returns "" when neither exists, so the BOM omits the version instead of recording
// one that belongs to something else, such as the binary generating the BOM.
func resolveModuleVersion(ctx context.Context, repoDir, explicit string) (string, error) {
	if version := strings.TrimSpace(explicit); version != "" {
		return version, nil
	}
	return releaseTag(ctx, repoDir)
}

// tagGlobEscaper escapes the wildmatch metacharacters a directory name may carry, so a
// module prefix is matched literally in a `git describe --match` pattern.
var tagGlobEscaper = strings.NewReplacer(`\`, `\\`, "*", `\*`, "?", `\?`, "[", `\[`)

// releaseTag returns the SemVer release tag on HEAD that versions the module at
// repoDir. A module in a repository subdirectory is tagged "<dir>/vX.Y.Z", as the go
// command resolves nested module versions, so only tags under that prefix count. It
// returns "" when repoDir is not a Git checkout, HEAD carries no such tag, or Git is
// not installed.
func releaseTag(ctx context.Context, repoDir string) (string, error) {
	dir, ok, err := gitProbe(ctx, repoDir, "rev-parse", "--show-prefix")
	if err != nil || !ok {
		return "", err
	}
	described, ok, err := gitProbe(ctx, repoDir,
		"describe", "--tags", "--exact-match", "--match", tagGlobEscaper.Replace(dir)+"v[0-9]*", "HEAD")
	if err != nil || !ok {
		return "", err
	}
	tag := strings.TrimPrefix(described, dir)
	if _, valid := semver.Parse(tag); !valid || !strings.HasPrefix(tag, "v") {
		return "", nil
	}
	return tag, nil
}

// gitProbe runs one read-only Git inspection in repoDir and returns its trimmed output.
// ok is false when Git ran and refused, as it does outside a checkout or for a tag
// that does not exist, or when Git is not installed: the answer is then unknown, not
// wrong. A timeout, cancellation or oversized output is an error, because the probe
// never got to answer.
func gitProbe(ctx context.Context, repoDir string, args ...string) (string, bool, error) {
	result, err := util.RunGitProbe(ctx, repoDir, maxGitProbeBytes, args...)
	if err == nil {
		return strings.TrimSpace(string(result.Stdout)), true, nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		len(result.Stdout) >= maxGitProbeBytes || len(result.Stderr) >= maxGitProbeBytes {
		return "", false, fmt.Errorf("git %s in %s: %w", args[0], repoDir, err)
	}
	var exitErr *exec.ExitError
	if errors.Is(err, exec.ErrNotFound) || errors.As(err, &exitErr) {
		return "", false, nil
	}
	return "", false, fmt.Errorf("git %s in %s: %w", args[0], repoDir, err)
}

// replaceKey selects the requirements one replace directive applies to. An empty
// version selects every version of the module.
type replaceKey struct {
	path    string
	version string
}

func parseGoModComponents(content string) ([]Component, error) {
	var components []Component
	lines := strings.Split(content, "\n")
	inRequire, inReplace := false, false
	replacements := make(map[replaceKey]gomanifest.ReplaceDirective)

	for _, line := range lines {
		if requirement, required := gomanifest.RequirementLine(line, &inRequire); required {
			if comp, ok := parseRequireLine(requirement, true); ok {
				components = append(components, comp)
			}
			continue
		}
		if replaceLine, isReplace := gomanifest.ReplaceLine(line, &inReplace); isReplace {
			if directive, ok := gomanifest.ParseReplaceDirective(replaceLine); ok {
				replacements[replaceKey{path: directive.OldPath, version: directive.OldVersion}] = directive
			}
		}
	}

	applyReplacements(components, replacements)
	return components, nil
}

// lookupReplacement returns the replace directive the go command applies to path at
// version: one naming that exact version wins over one naming the module alone.
func lookupReplacement(replacements map[replaceKey]gomanifest.ReplaceDirective, path, version string) (gomanifest.ReplaceDirective, bool) {
	if directive, ok := replacements[replaceKey{path: path, version: version}]; ok {
		return directive, true
	}
	directive, ok := replacements[replaceKey{path: path}]
	return directive, ok
}

// applyReplacements rewrites every component a go.mod replace directive applies to, so
// the SBOM records what actually builds rather than the pre-replacement require line. A
// directive whose left side names a version applies only to a requirement of that
// version. A local filesystem replacement (no version) clears the PURL: a
// coordinate-based package URL cannot name a path that only makes sense on the machine
// that built it.
func applyReplacements(components []Component, replacements map[replaceKey]gomanifest.ReplaceDirective) {
	for i := range components {
		rep, ok := lookupReplacement(replacements, components[i].Name, components[i].Version)
		if !ok {
			continue
		}
		components[i].Name = rep.NewPath
		components[i].Version = rep.NewVersion
		components[i].PURL = ""
		if rep.NewVersion != "" {
			components[i].PURL = fmt.Sprintf("pkg:golang/%s@%s", rep.NewPath, rep.NewVersion)
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
