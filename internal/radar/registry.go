// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package radar watches public research and upstream sources for a repository (#818): a
// schema-versioned source registry, a pure collection window, a feed and release reader, and a
// digest whose untrusted text is neutralised before anyone reads it. Collection keeps no state:
// the window is an input, so consecutive runs partition time exactly.
package radar

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cordanaLLM/praetor/internal/httpendpoint"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Registry bounds (HISS-02). Every source field and the file itself has a cap, so a registry
// can neither exhaust memory nor smuggle a document into a field.
const (
	// RegistryVersion is the one schema version this package reads.
	RegistryVersion = 1
	// MaxRegistryBytes caps the registry file.
	MaxRegistryBytes = 256 << 10
	// MaxSources caps the number of sources one registry lists.
	MaxSources = 256
	// MaxIDBytes caps a source id.
	MaxIDBytes = 64
	// MaxURLBytes caps a source URL and an item link.
	MaxURLBytes = 2048
	// MaxWhyBytes caps the reason a source is watched.
	MaxWhyBytes = 512
)

// Kind is the type of a source.
type Kind string

// The source kinds this version reads.
const (
	// KindFeed is an RSS or Atom feed over HTTPS.
	KindFeed Kind = "feed"
	// KindGitHubRepo is the releases of one public repository on github.com.
	KindGitHubRepo Kind = "github_repo"
)

var (
	// ErrUnsupportedKind is a kind the radar design names but this version does not read yet.
	ErrUnsupportedKind = errors.New("kind is not supported yet")
	// ErrUnknownKind is a kind the radar design does not name.
	ErrUnknownKind = errors.New("unknown kind")
	// ErrPersonSource is a source that names a person: the registry and the digest name
	// sources, never individuals.
	ErrPersonSource = errors.New("source names a person")
	// ErrNoSources is a registry that lists nothing to watch.
	ErrNoSources = errors.New("at least one source is required")
)

// plannedKinds are the kinds the design names for later versions (#818). They are refused with
// ErrUnsupportedKind rather than ErrUnknownKind, so the error says the kind is coming.
var plannedKinds = map[Kind]bool{"github_owner": true, "query": true, "manual": true}

// idPattern is a source id: ASCII letters, digits, '.', '_' and '-', starting with a letter or
// digit. Ids name fixture files, so they hold no path separator.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Source is one watched source.
type Source struct {
	// ID names the source in the digest. Ids are unique without regard to case.
	ID string `yaml:"id"`
	// Kind selects how the source is read.
	Kind Kind `yaml:"kind"`
	// URL is the canonical HTTPS address of the source.
	URL string `yaml:"url"`
	// Why says what the source matters for.
	Why string `yaml:"why"`
}

// Registry is a repository's source registry.
type Registry struct {
	Version int      `yaml:"version"`
	Sources []Source `yaml:"sources"`
}

// LoadRegistry reads the registry at rel below root, through the bounded regular-file read every
// repository configuration reader uses (util.ReadConfinedLimited), and parses it.
func LoadRegistry(root, rel string) (*Registry, error) {
	data, err := util.ReadConfinedLimited(root, rel, MaxRegistryBytes)
	if err != nil {
		return nil, fmt.Errorf("radar registry %s: %w", rel, err)
	}
	registry, err := ParseRegistry(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	return registry, nil
}

// ParseRegistry decodes one registry document strictly (util.DecodeYAMLDocument with known
// fields, so an unknown or misspelled key is refused, a licence note included) and validates it.
func ParseRegistry(data []byte) (*Registry, error) {
	if len(data) > MaxRegistryBytes {
		return nil, fmt.Errorf("radar registry: %d bytes exceed the cap of %d", len(data), MaxRegistryBytes)
	}
	var registry Registry
	if err := util.DecodeYAMLDocument(data, &registry, util.YAMLDocumentOptions{KnownFields: true}); err != nil {
		return nil, fmt.Errorf("radar registry: %w", err)
	}
	if err := registry.Validate(); err != nil {
		return nil, fmt.Errorf("radar registry: %w", err)
	}
	return &registry, nil
}

// Validate reports every problem of the registry at once, each naming its field.
func (r *Registry) Validate() error {
	if r == nil {
		return errors.New("no registry")
	}
	var problems []error
	if r.Version != RegistryVersion {
		problems = append(problems, fmt.Errorf("version: %d is not the supported schema version %d", r.Version, RegistryVersion))
	}
	if len(r.Sources) == 0 {
		problems = append(problems, fmt.Errorf("sources: %w", ErrNoSources))
	}
	if len(r.Sources) > MaxSources {
		return errors.Join(append(problems, fmt.Errorf("sources: %d entries exceed the cap of %d", len(r.Sources), MaxSources))...)
	}
	seen := make(map[string]int, len(r.Sources))
	for i := 0; i < len(r.Sources); i++ {
		problems = append(problems, validateSource(i, r.Sources[i])...)
		key := strings.ToLower(r.Sources[i].ID)
		if first, duplicate := seen[key]; duplicate && key != "" {
			problems = append(problems, fmt.Errorf("sources[%d].id: %q duplicates sources[%d].id %q (ids compare without case)",
				i, r.Sources[i].ID, first, r.Sources[first].ID))
			continue
		}
		seen[key] = i
	}
	return errors.Join(problems...)
}

// validateSource returns the problems of source i, each prefixed with its field.
func validateSource(i int, source Source) []error {
	checks := [...]struct {
		field string
		err   error
	}{
		{"id", validateID(source.ID)},
		{"kind", validateKind(source.Kind)},
		{"url", validateURL(source.Kind, source.URL)},
		{"why", validateWhy(source.Why)},
	}
	var problems []error
	for _, check := range checks {
		if check.err != nil {
			problems = append(problems, fmt.Errorf("sources[%d].%s: %w", i, check.field, check.err))
		}
	}
	return problems
}

func validateID(id string) error {
	switch {
	case id == "":
		return errors.New("required")
	case len(id) > MaxIDBytes:
		return fmt.Errorf("%d bytes exceed the cap of %d", len(id), MaxIDBytes)
	case !idPattern.MatchString(id):
		return fmt.Errorf("%q must be ASCII letters, digits, '.', '_' or '-', starting with a letter or digit", id)
	}
	return nil
}

func validateKind(kind Kind) error {
	switch {
	case kind == "":
		return errors.New("required")
	case kind == KindFeed || kind == KindGitHubRepo:
		return nil
	case plannedKinds[kind]:
		return fmt.Errorf("%q: %w (supported: feed, github_repo)", kind, ErrUnsupportedKind)
	}
	return fmt.Errorf("%q: %w (supported: feed, github_repo)", kind, ErrUnknownKind)
}

// validateURL holds a source URL to a canonical credential-free HTTPS address without query or
// fragment (httpendpoint.ValidateHTTPS), refuses the account-page patterns personURL knows, and
// holds a github_repo URL to the repository shape.
func validateURL(kind Kind, raw string) error {
	if raw == "" {
		return errors.New("required")
	}
	if len(raw) > MaxURLBytes {
		return fmt.Errorf("%d bytes exceed the cap of %d", len(raw), MaxURLBytes)
	}
	if err := httpendpoint.ValidateHTTPS(raw); err != nil {
		return fmt.Errorf("%q: %w", raw, err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%q: %w", raw, err)
	}
	segments := pathSegments(parsed.Path)
	if reason, person := personURL(parsed.Hostname(), segments); person {
		return fmt.Errorf("%q: %w: %s", raw, ErrPersonSource, reason)
	}
	if kind == KindGitHubRepo {
		return validateGitHubRepoURL(raw, parsed.Hostname(), segments)
	}
	return nil
}

// validateGitHubRepoURL holds a github_repo URL to https://github.com/<owner>/<repository>.
func validateGitHubRepoURL(raw, host string, segments []string) error {
	if host != "github.com" || len(segments) != 2 {
		return fmt.Errorf("%q: a github_repo URL must be https://github.com/<owner>/<repository>", raw)
	}
	if err := util.ValidateGitHubRepositoryIdentity(segments[0], segments[1]); err != nil {
		return fmt.Errorf("%q: %w", raw, err)
	}
	if strings.EqualFold(segments[0], segments[1]) {
		return fmt.Errorf("%q: %w: a repository named after its owner is the owner's profile page", raw, ErrPersonSource)
	}
	return nil
}

// validateWhy holds the reason to one line of text that names no account or address: an '@'
// mentions an account or spells an e-mail address, and either names a person.
func validateWhy(why string) error {
	switch {
	case strings.TrimSpace(why) == "":
		return errors.New("required")
	case len(why) > MaxWhyBytes:
		return fmt.Errorf("%d bytes exceed the cap of %d", len(why), MaxWhyBytes)
	case !utf8.ValidString(why):
		return errors.New("must be UTF-8")
	case strings.ContainsRune(why, '@'):
		return fmt.Errorf("%w: an '@' mentions an account or spells an address", ErrPersonSource)
	case strings.IndexFunc(why, unicode.IsControl) >= 0:
		return errors.New("must be one line without control characters")
	}
	return nil
}
