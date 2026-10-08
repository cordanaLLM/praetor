// Package clientschema is the one reader of the vendored upstream schemas for the coding-client
// formats Praetor renders and the protocol messages it serves (#909, epic #910). The schemas sit
// under vendor/ with a manifest that records, per source, the upstream repository, the pin, the
// licence and the sha256 of every file. The loader refuses a file that differs from its pin, so a
// hand edit or a partial bump cannot pass for the published schema.
//
// The package performs no network I/O and validates no document: validation needs a JSON Schema
// implementation, which lives in the test-only module tools/schemacheck so the production module
// stays free of the dependency (docs/guides/client-schemas.md).
package clientschema

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/strictjson"
)

//go:embed vendor
var vendored embed.FS

// Bounds of one read (HISS-02).
const (
	// ManifestFile is the manifest beside the vendored files.
	ManifestFile = "manifest.json"
	// MaxSchemaBytes bounds one vendored schema file.
	MaxSchemaBytes = 4 << 20
	// MaxSources and MaxFilesPerSource bound the manifest.
	MaxSources        = 64
	MaxFilesPerSource = 128
	maxManifestBytes  = 1 << 20
	maxJSONDepth      = 256
)

// Pin kinds. A tag or a commit is an immutable upstream reference. A hosted copy has no
// immutable reference upstream; the sha256 of the file is its pin and Pin names the release the
// copy was observed beside.
const (
	PinTag    = "tag"
	PinCommit = "commit"
	PinHosted = "hosted"
)

// File is one vendored schema file.
type File struct {
	// Path is relative to the vendor directory, with forward slashes.
	Path string `json:"path"`
	// Upstream is the path of the file under the source's URL base.
	Upstream string `json:"upstream"`
	// SHA256 is the lower-case hex digest of the vendored bytes.
	SHA256 string `json:"sha256"`
}

// Source is one upstream origin pinned at one reference.
type Source struct {
	ID        string `json:"id"`
	Client    string `json:"client"`
	Kind      string `json:"kind"`
	Repo      string `json:"repo"`
	PinKind   string `json:"pin_kind"`
	Branch    string `json:"branch,omitempty"`
	Pin       string `json:"pin"`
	Version   string `json:"version"`
	URLBase   string `json:"url_base"`
	License   string `json:"license"`
	Copyright string `json:"copyright"`
	Files     []File `json:"files"`
}

// Manifest is the pin file of the vendor directory.
type Manifest struct {
	Version int      `json:"version"`
	Sources []Source `json:"sources"`
}

// ErrDrift marks a vendored file that differs from its pin.
var ErrDrift = errors.New("vendored schema differs from its pin")

// LoadManifest reads and checks the embedded manifest: strict JSON, no duplicate member, known
// pin kinds, relative file paths and well-formed digests.
func LoadManifest() (*Manifest, error) {
	raw, err := vendored.ReadFile(path.Join("vendor", ManifestFile))
	if err != nil {
		return nil, fmt.Errorf("read client schema manifest: %w", err)
	}
	return ParseManifest(raw)
}

// ParseManifest checks raw as a manifest; LoadManifest and the tests share it.
func ParseManifest(raw []byte) (*Manifest, error) {
	var manifest Manifest
	opts := strictjson.Options{MaxBytes: maxManifestBytes, MaxDepth: maxJSONDepth, Names: strictjson.ExactNames}
	if err := strictjson.Decode(raw, &manifest, opts); err != nil {
		return nil, fmt.Errorf("client schema manifest: %w", err)
	}
	if err := manifest.check(); err != nil {
		return nil, fmt.Errorf("client schema manifest: %w", err)
	}
	return &manifest, nil
}

func (m *Manifest) check() error {
	if m.Version != 1 || len(m.Sources) == 0 || len(m.Sources) > MaxSources {
		return errors.New("version 1 and 1..64 sources required")
	}
	ids := make(map[string]bool, len(m.Sources))
	paths := make(map[string]bool)
	for i := range m.Sources {
		source := &m.Sources[i]
		if err := source.check(); err != nil {
			return fmt.Errorf("source %q: %w", source.ID, err)
		}
		if ids[source.ID] {
			return fmt.Errorf("source %q repeats", source.ID)
		}
		ids[source.ID] = true
		for _, file := range source.Files {
			if paths[file.Path] {
				return fmt.Errorf("file %q is listed twice", file.Path)
			}
			paths[file.Path] = true
		}
	}
	return nil
}

func (s *Source) check() error {
	if s.ID == "" || s.Client == "" || s.Kind == "" || s.Repo == "" || s.Pin == "" || s.Version == "" {
		return errors.New("id, client, kind, repo, pin and version are required")
	}
	if s.License == "" || s.Copyright == "" || !strings.HasPrefix(s.URLBase, "https://") {
		return errors.New("licence, copyright and an https URL base are required")
	}
	if !slices.Contains([]string{PinTag, PinCommit, PinHosted}, s.PinKind) {
		return fmt.Errorf("unknown pin kind %q", s.PinKind)
	}
	if s.PinKind == PinCommit && (len(s.Pin) != 40 || s.Branch == "") {
		return errors.New("a commit pin needs 40 hex digits and a branch")
	}
	if len(s.Files) == 0 || len(s.Files) > MaxFilesPerSource {
		return errors.New("1..128 files required")
	}
	for _, file := range s.Files {
		if err := file.check(); err != nil {
			return fmt.Errorf("file %q: %w", file.Path, err)
		}
	}
	return nil
}

func (f File) check() error {
	if !fs.ValidPath(f.Path) || f.Path == "." || f.Upstream == "" {
		return errors.New("a relative path and an upstream path are required")
	}
	if raw, err := hex.DecodeString(f.SHA256); err != nil || len(raw) != sha256.Size || f.SHA256 != strings.ToLower(f.SHA256) {
		return errors.New("sha256 must be 64 lower-case hex digits")
	}
	return nil
}

// Verify checks data against the pin of file and names the drift.
func (f File) Verify(data []byte) error {
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != f.SHA256 {
		return fmt.Errorf("%w: %s has sha256 %s, the pin says %s", ErrDrift, f.Path, got, f.SHA256)
	}
	return nil
}

// PinPlaceholder stands for the source's pin in URLBase and in a file's Upstream path, so a
// bump of Pin moves every URL of the source with it (Renovate rewrites Pin only).
const PinPlaceholder = "{pin}"

// URL is where the pinned upstream copy of the file is fetched from.
func (s Source) URL(f File) string {
	return strings.ReplaceAll(s.URLBase+f.Upstream, PinPlaceholder, s.Pin)
}

// Read returns the vendored bytes of file after verifying them against the pin.
func Read(file File) ([]byte, error) {
	data, err := vendored.ReadFile(path.Join("vendor", file.Path))
	if err != nil {
		return nil, fmt.Errorf("read vendored schema %s: %w", file.Path, err)
	}
	if len(data) > MaxSchemaBytes {
		return nil, fmt.Errorf("vendored schema %s exceeds %d bytes", file.Path, MaxSchemaBytes)
	}
	if err := file.Verify(data); err != nil {
		return nil, err
	}
	return data, nil
}

// Source returns the source with id.
func (m *Manifest) Source(id string) (Source, bool) {
	for _, source := range m.Sources {
		if source.ID == id {
			return source, true
		}
	}
	return Source{}, false
}

// ForClient returns the sources of one client in manifest order.
func (m *Manifest) ForClient(client string) []Source {
	var found []Source
	for _, source := range m.Sources {
		if source.Client == client {
			found = append(found, source)
		}
	}
	return found
}

// Schema returns the verified bytes of the one file at relPath, a Path of the manifest.
func (m *Manifest) Schema(relPath string) ([]byte, error) {
	for _, source := range m.Sources {
		for _, file := range source.Files {
			if file.Path == relPath {
				return Read(file)
			}
		}
	}
	return nil, fmt.Errorf("no vendored schema %q in the manifest", relPath)
}

// EmbeddedPaths lists every file under vendor/ except the manifest, sorted. The coverage test
// compares it with the manifest, so a file nobody pinned cannot sit in the directory.
func EmbeddedPaths() ([]string, error) {
	var found []string
	err := fs.WalkDir(vendored, "vendor", func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel := strings.TrimPrefix(p, "vendor/")
		if !entry.IsDir() && rel != ManifestFile {
			found = append(found, rel)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list vendored schemas: %w", err)
	}
	slices.Sort(found)
	return found, nil
}

// PinnedVersion is the label a capabilities report shows for a client: the version of the
// first "settings" source, or the first source of any kind, and empty when the client has none.
func (m *Manifest) PinnedVersion(client string) string {
	sources := m.ForClient(client)
	for _, source := range sources {
		if source.Kind == "settings" {
			return source.Version
		}
	}
	if len(sources) > 0 {
		return sources[0].Version
	}
	return ""
}
