package config

import (
	"fmt"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// Bounds of the documentation block. The locked documentation gate
// (tools/markdownlint/verify.mjs) runs in hosted CI without praetorctl and validates the same
// block itself; TestDocumentationSettingsMirrorConfig in tools/markdownlint keeps its constants
// equal to these, so audit and the gate apply the same ranges and glob rules.
const (
	// DefaultDocumentationMaxFiles is the Markdown inventory bound when max_files is unset.
	DefaultDocumentationMaxFiles = 4096
	// DocumentationMaxFilesCeiling is the largest max_files a repository may declare.
	DocumentationMaxFilesCeiling = 16384
	// DefaultDocumentationMaxFileBytes is the per-file bound when max_file_bytes is unset.
	DefaultDocumentationMaxFileBytes = 1 << 20
	// DocumentationMaxFileBytesCeiling is the largest max_file_bytes a repository may declare.
	// It is a memory bound for the gate's Markdown parsers; tools/markdownlint/verify.mjs records
	// the measurement.
	DocumentationMaxFileBytesCeiling = 4 << 20
	// MaxDocumentationStyleExclusions bounds the style_exclude list.
	MaxDocumentationStyleExclusions = 64
	// MaxDocumentationStyleExclusionBytes bounds one style_exclude glob.
	MaxDocumentationStyleExclusionBytes = 256
)

// DocumentationPolicy tunes the locked documentation gate for one repository, within bounds:
// MaxFiles and MaxFileBytes raise the Markdown inventory's file-count and per-file bounds from
// their defaults up to their ceilings (a nil field keeps the default), and StyleExclude lists
// repository-relative globs of partial, generated or fixture Markdown the style rules skip. The
// private-link rule still reads every file, and the gate fails when the globs leave no file to
// style. The gate reads this block from .standards.yaml at run time, so a declaration changes
// no locked asset.
type DocumentationPolicy struct {
	MaxFiles     *int     `yaml:"max_files,omitempty"`
	MaxFileBytes *int     `yaml:"max_file_bytes,omitempty"`
	StyleExclude []string `yaml:"style_exclude,omitempty"`
}

// UnmarshalYAML decodes the block strictly, as the gate reads it: known keys once each, integer
// bounds written as integers, and globs written as strings. A custom unmarshaler receives the raw
// node, where the decoder's KnownFields and its lenient scalar conversions do not apply.
func (p *DocumentationPolicy) UnmarshalYAML(node *yaml.Node) error {
	var maxFiles, maxFileBytes int
	present, err := registerIntFields(node, "documentation", map[string]*int{
		"max_files": &maxFiles, "max_file_bytes": &maxFileBytes, "style_exclude": nil,
	})
	if err != nil {
		return err
	}
	var policy DocumentationPolicy
	if present["max_files"] {
		policy.MaxFiles = &maxFiles
	}
	if present["max_file_bytes"] {
		policy.MaxFileBytes = &maxFileBytes
	}
	if present["style_exclude"] {
		if policy.StyleExclude, err = styleExcludeGlobs(policyMember(node, "style_exclude")); err != nil {
			return err
		}
	}
	*p = policy
	return nil
}

func styleExcludeGlobs(node *yaml.Node) ([]string, error) {
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("documentation.style_exclude must be a list of globs")
	}
	if len(node.Content) > MaxDocumentationStyleExclusions {
		return nil, fmt.Errorf("documentation.style_exclude has %d globs; maximum is %d", len(node.Content), MaxDocumentationStyleExclusions)
	}
	globs := make([]string, 0, len(node.Content))
	for index := 0; index < len(node.Content) && index < MaxDocumentationStyleExclusions; index++ {
		glob, err := stringScalar(node.Content[index], fmt.Sprintf("documentation.style_exclude[%d]", index))
		if err != nil {
			return nil, err
		}
		globs = append(globs, glob)
	}
	return globs, nil
}

// EffectiveMaxFiles returns the declared file-count bound, or the default when none is declared.
func (p *DocumentationPolicy) EffectiveMaxFiles() int {
	if p == nil || p.MaxFiles == nil {
		return DefaultDocumentationMaxFiles
	}
	return *p.MaxFiles
}

// EffectiveMaxFileBytes returns the declared per-file bound, or the default when none is declared.
func (p *DocumentationPolicy) EffectiveMaxFileBytes() int {
	if p == nil || p.MaxFileBytes == nil {
		return DefaultDocumentationMaxFileBytes
	}
	return *p.MaxFileBytes
}

func validateManifestDocumentation(m *Manifest) error {
	if m == nil || m.Documentation == nil {
		return nil
	}
	return m.Documentation.validate()
}

func (p *DocumentationPolicy) validate() error {
	if err := validateDocumentationBound("max_files", p.MaxFiles, DefaultDocumentationMaxFiles, DocumentationMaxFilesCeiling); err != nil {
		return err
	}
	if err := validateDocumentationBound("max_file_bytes", p.MaxFileBytes, DefaultDocumentationMaxFileBytes, DocumentationMaxFileBytesCeiling); err != nil {
		return err
	}
	return validateStyleExclusions(p.StyleExclude)
}

func validateDocumentationBound(key string, value *int, defaultValue, ceiling int) error {
	if value == nil || (*value >= defaultValue && *value <= ceiling) {
		return nil
	}
	return fmt.Errorf("documentation.%s must be an integer from %d to %d; got %d", key, defaultValue, ceiling, *value)
}

func validateStyleExclusions(globs []string) error {
	if len(globs) > MaxDocumentationStyleExclusions {
		return fmt.Errorf("documentation.style_exclude has %d globs; maximum is %d", len(globs), MaxDocumentationStyleExclusions)
	}
	seen := make(map[string]struct{}, len(globs))
	for index := 0; index < len(globs) && index < MaxDocumentationStyleExclusions; index++ {
		if problem := StyleExclusionProblem(globs[index]); problem != "" {
			return fmt.Errorf("documentation.style_exclude[%d] %s", index, problem)
		}
		if _, repeated := seen[globs[index]]; repeated {
			return fmt.Errorf("documentation.style_exclude[%d] repeats %q", index, globs[index])
		}
		seen[globs[index]] = struct{}{}
	}
	return nil
}

// StyleExclusionProblem names why a style_exclude glob is refused, or returns "". A glob must be
// a repository-relative path pattern: no absolute, drive-letter or negated form, no empty, "."
// or ".." segment, no backslash, and at least one letter or digit, so wildcards alone ("**",
// "*/**") cannot stand in for every file. verify.mjs's styleExclusionProblem applies the same
// rules.
func StyleExclusionProblem(glob string) string {
	switch {
	case glob == "":
		return "must be a non-empty string"
	case len(glob) > MaxDocumentationStyleExclusionBytes:
		return fmt.Sprintf("exceeds %d bytes", MaxDocumentationStyleExclusionBytes)
	case strings.ContainsAny(glob, "\x00\r\n\\"):
		return "must not contain NUL, a line break or a backslash"
	case strings.HasPrefix(glob, "/") || strings.HasPrefix(glob, "!") || hasDriveLetter(glob):
		return "must be repository-relative, not absolute or negated"
	case hasUnsafeSegment(glob):
		return "must not contain an empty, . or .. segment; write dir/** to exclude a directory"
	case !strings.ContainsFunc(glob, isLetterOrNumber):
		return "must name a path: wildcards alone would match every file"
	}
	return ""
}

func hasDriveLetter(glob string) bool {
	if len(glob) < 2 || glob[1] != ':' {
		return false
	}
	return ('a' <= glob[0] && glob[0] <= 'z') || ('A' <= glob[0] && glob[0] <= 'Z')
}

// hasUnsafeSegment reports an empty, "." or ".." segment. Callers bound glob to
// MaxDocumentationStyleExclusionBytes first, which bounds the segment count too.
func hasUnsafeSegment(glob string) bool {
	segments := strings.Split(glob, "/")
	for index := 0; index < len(segments) && index <= MaxDocumentationStyleExclusionBytes; index++ {
		if segment := segments[index]; segment == "" || segment == "." || segment == ".." {
			return true
		}
	}
	return false
}

func isLetterOrNumber(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r)
}
