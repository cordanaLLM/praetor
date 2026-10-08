// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package radar

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// Release listing bounds (HISS-02).
const (
	// MaxReleaseBytes caps one releases listing.
	MaxReleaseBytes = 4 << 20
	// MaxReleases caps the releases one listing may carry; a longer listing fails rather than
	// being cut.
	MaxReleases = 1000
)

// githubRelease is the part of a GitHub REST release object
// (GET /repos/{owner}/{repo}/releases) the radar reads. The fixture files of a github_repo
// source carry the same JSON the API returns, so one decoder serves both.
type githubRelease struct {
	TagName     string     `json:"tag_name"`
	Name        string     `json:"name"`
	HTMLURL     string     `json:"html_url"`
	PublishedAt *time.Time `json:"published_at"`
	Draft       bool       `json:"draft"`
	Prerelease  bool       `json:"prerelease"`
}

// ParseReleases reads a GitHub REST releases listing: a JSON array of release objects. A draft
// is not published and is left out; a release without a publication time is counted as undated.
// Anything but one array, such as the API's error object, is an error, never an empty listing.
func ParseReleases(data []byte) (Entries, error) {
	if len(data) > MaxReleaseBytes {
		return Entries{}, fmt.Errorf("radar releases: %d bytes exceed the cap of %d", len(data), MaxReleaseBytes)
	}
	var releases []githubRelease
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&releases); err != nil {
		return Entries{}, fmt.Errorf("radar releases: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return Entries{}, errors.New("radar releases: data after the release array")
	}
	if releases == nil {
		return Entries{}, errors.New("radar releases: the listing is not a JSON array")
	}
	if len(releases) > MaxReleases {
		return Entries{}, fmt.Errorf("radar releases: %d releases exceed the cap of %d", len(releases), MaxReleases)
	}
	var out Entries
	for i := 0; i < len(releases); i++ {
		release := releases[i]
		switch {
		case release.Draft:
			continue
		case release.PublishedAt == nil:
			out.Undated++
		default:
			out.Items = append(out.Items, Entry{Title: releaseTitle(release), Link: release.HTMLURL, Published: release.PublishedAt.UTC()})
		}
	}
	return out, nil
}

// releaseTitle names a release by its name and tag, and marks a prerelease.
func releaseTitle(release githubRelease) string {
	title := release.TagName
	if release.Name != "" && release.Name != release.TagName {
		title = release.Name + " (" + release.TagName + ")"
	}
	if release.Prerelease {
		title += " [prerelease]"
	}
	return title
}
