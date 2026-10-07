// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package radar

import (
	"context"
	"errors"
	"fmt"
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

// Read reads source's planted file through the bounded regular-file read confined to Dir.
func (f FixtureReader) Read(ctx context.Context, source Source) (Entries, error) {
	if err := ctx.Err(); err != nil {
		return Entries{}, err
	}
	switch source.Kind {
	case KindFeed:
		data, err := util.ReadConfinedLimited(f.Dir, source.ID+".xml", MaxFeedBytes)
		if err != nil {
			return Entries{}, fmt.Errorf("read fixture %s.xml: %w", source.ID, err)
		}
		return ParseFeed(data)
	case KindGitHubRepo:
		data, err := util.ReadConfinedLimited(f.Dir, source.ID+".json", MaxReleaseBytes)
		if err != nil {
			return Entries{}, fmt.Errorf("read fixture %s.json: %w", source.ID, err)
		}
		return ParseReleases(data)
	}
	return Entries{}, fmt.Errorf("kind %q: %w", source.Kind, ErrUnsupportedKind)
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

// Digest is the outcome of one collection: the window, a section for every source that has
// something to report, in registry order, and every source that failed.
type Digest struct {
	Window   Window
	Read     int
	Sections []Section
	Failed   []Failure
}

// Collect reads every source of the registry through reader and keeps what falls in window. It
// keeps no state and reads no clock. A failed source is recorded in the digest; when every source
// failed the digest is returned with ErrAllSourcesFailed, so the caller can still write it. A
// cancelled context ends the collection with no digest.
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
		if section := windowSection(source, entries, window); len(section.Items) > 0 || section.Undated > 0 {
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
