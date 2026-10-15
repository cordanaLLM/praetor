// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Bounds for the skills section (HISS-02).
const (
	MaxUpstreamSkillSources    = 64
	MaxUpstreamSkillsPerSource = 128
	MaxUpstreamSkillNameBytes  = 64
	MaxUpstreamTagBytes        = 128
	MaxUpstreamRepoBytes       = 512
)

var (
	// sha1CommitRe matches a 40-character hexadecimal git commit SHA.
	sha1CommitRe = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
	// skillNameRe matches a valid skill directory name: one path segment of [a-z0-9][a-z0-9._-]*.
	skillNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
)

// SkillsPolicy represents the `skills` section of .standards.yaml.
type SkillsPolicy struct {
	Upstream []UpstreamSkillSource `yaml:"upstream,omitempty"`
}

// UpstreamSkillSource declares one pinned upstream repository of skills.
type UpstreamSkillSource struct {
	Repo   string   `yaml:"repo"`
	Tag    string   `yaml:"tag"`
	Commit string   `yaml:"commit"`
	Skills []string `yaml:"skills"`
}

// UpstreamSources returns the declared upstream skill sources; safe on nil.
func (p *SkillsPolicy) UpstreamSources() []UpstreamSkillSource {
	if p == nil {
		return nil
	}
	return p.Upstream
}

// UpstreamSkillSourceFor returns the declared upstream source for skillName, and whether it was found.
func (p *SkillsPolicy) UpstreamSkillSourceFor(skillName string) (UpstreamSkillSource, bool) {
	if p == nil {
		return UpstreamSkillSource{}, false
	}
	for i := 0; i < len(p.Upstream) && i < MaxUpstreamSkillSources; i++ {
		src := p.Upstream[i]
		for j := 0; j < len(src.Skills) && j < MaxUpstreamSkillsPerSource; j++ {
			if src.Skills[j] == skillName {
				return src, true
			}
		}
	}
	return UpstreamSkillSource{}, false
}

// LoadSkillsPolicy reads .standards.yaml in rootDir and returns the declared SkillsPolicy.
// If .standards.yaml does not exist, it returns nil, nil.
func LoadSkillsPolicy(rootDir string) (*SkillsPolicy, error) {
	manifestPath := filepath.Join(rootDir, ManifestFileName)
	manifest, err := LoadManifest(manifestPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return manifest.Skills, nil
}

// ValidCommitSHA reports whether s is exactly 40 hexadecimal characters.
func ValidCommitSHA(s string) bool {
	return len(s) == 40 && sha1CommitRe.MatchString(s)
}

// ValidSkillName reports whether name is a valid skill directory name: exactly one
// path segment matching [a-z0-9][a-z0-9._-]*, at most MaxUpstreamSkillNameBytes.
func ValidSkillName(name string) bool {
	if len(name) == 0 || len(name) > MaxUpstreamSkillNameBytes {
		return false
	}
	if filepath.Base(name) != name || strings.ContainsAny(name, "/\\") {
		return false
	}
	return skillNameRe.MatchString(name)
}

// ValidRepoLocation reports whether repo is a non-empty, bounded repository URL or path.
func ValidRepoLocation(repo string) bool {
	if len(repo) == 0 || len(repo) > MaxUpstreamRepoBytes {
		return false
	}
	if strings.ContainsAny(repo, "\r\n\t\x00") {
		return false
	}
	if strings.HasPrefix(repo, "https://") || strings.HasPrefix(repo, "http://") || strings.HasPrefix(repo, "file://") {
		_, err := url.Parse(repo)
		return err == nil
	}
	return strings.HasPrefix(repo, "/") || strings.HasPrefix(repo, "./") || strings.HasPrefix(repo, "../")
}

// validateManifestSkills validates the `skills` section of the manifest.
func validateManifestSkills(m *Manifest) error {
	if m == nil || m.Skills == nil {
		return nil
	}
	sources := m.Skills.UpstreamSources()
	if len(sources) == 0 {
		return nil
	}
	if len(sources) > MaxUpstreamSkillSources {
		return fmt.Errorf("skills.upstream lists %d repositories; maximum is %d", len(sources), MaxUpstreamSkillSources)
	}
	seenSkills := make(map[string]int, len(sources))
	for i := 0; i < len(sources) && i < MaxUpstreamSkillSources; i++ {
		if err := validateOneSource(sources[i], i, seenSkills); err != nil {
			return err
		}
	}
	return nil
}

func validateOneSource(src UpstreamSkillSource, i int, seenSkills map[string]int) error {
	if !ValidRepoLocation(src.Repo) {
		return fmt.Errorf("skills.upstream[%d].repo %q must be a valid repository URL or path", i, src.Repo)
	}
	if src.Tag == "" || len(src.Tag) > MaxUpstreamTagBytes || strings.ContainsAny(src.Tag, "\r\n\t\x00 ") {
		return fmt.Errorf("skills.upstream[%d].tag %q must be a non-empty tag name (at most %d bytes)", i, src.Tag, MaxUpstreamTagBytes)
	}
	if !ValidCommitSHA(src.Commit) {
		return fmt.Errorf("skills.upstream[%d].commit %q must be 40 hex characters", i, src.Commit)
	}
	if len(src.Skills) == 0 {
		return fmt.Errorf("skills.upstream[%d].skills must name at least one skill", i)
	}
	if len(src.Skills) > MaxUpstreamSkillsPerSource {
		return fmt.Errorf("skills.upstream[%d].skills lists %d skills; maximum is %d", i, len(src.Skills), MaxUpstreamSkillsPerSource)
	}
	return validateSourceSkillNames(src.Skills, i, seenSkills)
}

func validateSourceSkillNames(skills []string, i int, seenSkills map[string]int) error {
	for j := 0; j < len(skills) && j < MaxUpstreamSkillsPerSource; j++ {
		name := skills[j]
		if !ValidSkillName(name) {
			return fmt.Errorf("skills.upstream[%d].skills[%d] %q is not a valid skill name", i, j, name)
		}
		if prev, exists := seenSkills[name]; exists {
			return fmt.Errorf("skills.upstream[%d].skills[%d] %q duplicate skill name (already declared in source %d)", i, j, name, prev)
		}
		seenSkills[name] = i
	}
	return nil
}
