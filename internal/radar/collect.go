// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package radar

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"

	"github.com/cordanaLLM/praetor/internal/util"
)

// ErrAllSourcesFailed is a collection in which no source could be read. One failed source is
// named in the digest and the run succeeds; when every source fails there is nothing to report
// but failures, and the run fails.
var ErrAllSourcesFailed = errors.New("every radar source failed")

// Reader returns every entry one source published; Collect applies the window.
type Reader interface {
	Read(ctx context.Context, source Source) (Entries, error)
}

// FixtureReader reads planted source files from Dir instead of the network: <id>.xml holds a
// feed source's document and <id>.json a github_repo source's GitHub REST releases listing. A
// missing or unreadable file fails that source, as an unreachable source would.
type FixtureReader struct {
	Dir string
}

// fixtureFormat is how FixtureReader stores and parses one source kind.
type fixtureFormat struct {
	extension string
	limit     int64
	parse     func([]byte) (Entries, error)
}

// fixtureFormats are the planted file formats of the kinds this version reads.
var fixtureFormats = map[Kind]fixtureFormat{
	KindFeed:       {extension: ".xml", limit: MaxFeedBytes, parse: ParseFeed},
	KindGitHubRepo: {extension: ".json", limit: MaxReleaseBytes, parse: ParseReleases},
}

// fixtureReasons are the read failures FixtureReader tells apart, each with the words the digest
// prints for it; any other failure reads "could not be read".
var fixtureReasons = [...]struct {
	cause  error
	reason string
}{
	{fs.ErrNotExist, "is missing"},
	{util.ErrFileTooLarge, "exceeds the read limit"},
	{util.ErrNotRegularFile, "is not a regular file"},
	{util.ErrPathEscapesRoot, "resolves outside the fixture directory"},
	{fs.ErrPermission, "is not readable"},
}

// fixtureFailure is a planted file FixtureReader could not read. Its text is the file name and a
// fixed reason from fixtureReasons, never the cause's text: a read error carries local paths,
// the fixture directory's and a link target's, and the digest that prints the text is meant to
// be published. The cause stays reachable through errors.Is and errors.As.
type fixtureFailure struct {
	name   string
	reason string
	cause  error
}

func (e *fixtureFailure) Error() string {
	return "fixture " + e.name + " " + e.reason
}

func (e *fixtureFailure) Unwrap() error {
	return e.cause
}

// newFixtureFailure names the failure to read the planted file name by name and reason only.
func newFixtureFailure(name string, cause error) *fixtureFailure {
	for i := 0; i < len(fixtureReasons); i++ {
		if errors.Is(cause, fixtureReasons[i].cause) {
			return &fixtureFailure{name: name, reason: fixtureReasons[i].reason, cause: cause}
		}
	}
	return &fixtureFailure{name: name, reason: "could not be read", cause: cause}
}

// Read reads source's planted file through the bounded regular-file read confined to Dir. A
// file that cannot be read fails with a fixtureFailure, which names the file and not where it
// lies.
func (f FixtureReader) Read(ctx context.Context, source Source) (Entries, error) {
	if err := ctx.Err(); err != nil {
		return Entries{}, err
	}
	format, ok := fixtureFormats[source.Kind]
	if !ok {
		return Entries{}, fmt.Errorf("kind %q: %w", source.Kind, ErrUnsupportedKind)
	}
	name := source.ID + format.extension
	data, err := util.ReadConfinedLimited(f.Dir, name, format.limit)
	if err != nil {
		return Entries{}, newFixtureFailure(name, err)
	}
	return format.parse(data)
}

// Section is what one source published inside the window.
type Section struct {
	Source Source
	// Items are the entries inside the window, newest first.
	Items []Entry
	// Undated counts the source's entries no window can place.
	Undated int
}

// Failure is a source that could not be read, and why.
type Failure struct {
	Source Source
	Reason string
}

// Digest is the outcome of one collection: the window, a section for every source with an entry
// inside the window, in registry order, and every source that failed.
type Digest struct {
	Window   Window
	Read     int
	Sections []Section
	Failed   []Failure
	// Undated counts the entries without a readable date across every source read, the ones in
	// no section among them, for the caller to report beside the digest.
	Undated int
}

// Collect reads every source of the registry through reader and keeps what falls in window. It
// keeps no state and reads no clock. A failed source is recorded in the digest; when every source
// failed the digest is returned with ErrAllSourcesFailed, so the caller can still write it. A
// cancelled context ends the collection with no digest.
//
// A source gets a section only when an entry of it falls inside the window, and the section
// counts the source's undated entries. Undated entries alone add no section: without state the
// collection cannot tell a new undated entry from one an earlier digest counted, and a section
// for them would make every digest of an unchanged feed non-empty. Digest.Undated still counts
// them all.
func Collect(ctx context.Context, registry *Registry, reader Reader, window Window) (Digest, error) {
	if reader == nil {
		return Digest{}, errors.New("radar collect: no reader")
	}
	if err := registry.Validate(); err != nil {
		return Digest{}, fmt.Errorf("radar collect: %w", err)
	}
	digest := Digest{Window: window}
	for i := 0; i < len(registry.Sources) && i < MaxSources; i++ {
		if err := ctx.Err(); err != nil {
			return Digest{}, fmt.Errorf("radar collect: %w", err)
		}
		source := registry.Sources[i]
		entries, err := reader.Read(ctx, source)
		if err != nil {
			digest.Failed = append(digest.Failed, Failure{Source: source, Reason: err.Error()})
			continue
		}
		digest.Read++
		digest.Undated += entries.Undated
		if section := windowSection(source, entries, window); len(section.Items) > 0 {
			digest.Sections = append(digest.Sections, section)
		}
	}
	if digest.Read == 0 {
		return digest, fmt.Errorf("radar collect: %w (%d of %d)", ErrAllSourcesFailed, len(digest.Failed), len(registry.Sources))
	}
	return digest, nil
}

// windowSection keeps the entries inside window, newest first; ties order by title, then link,
// so the digest is the same bytes for the same input.
func windowSection(source Source, entries Entries, window Window) Section {
	section := Section{Source: source, Undated: entries.Undated}
	for i := 0; i < len(entries.Items); i++ {
		entry := entries.Items[i]
		inside := window.Contains(entry.Published)
		if entry.DateOnly {
			inside = window.ContainsDate(entry.Published)
		}
		if inside {
			section.Items = append(section.Items, entry)
		}
	}
	sort.SliceStable(section.Items, func(a, b int) bool {
		left, right := section.Items[a], section.Items[b]
		if !left.Published.Equal(right.Published) {
			return left.Published.After(right.Published)
		}
		if left.Title != right.Title {
			return left.Title < right.Title
		}
		return left.Link < right.Link
	})
	return section
}
