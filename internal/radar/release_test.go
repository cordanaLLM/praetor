// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package radar

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const releaseListing = `[
 {"tag_name": "v2.1.0-rc.1", "name": "", "html_url": "https://github.com/example-org/example-project/releases/tag/v2.1.0-rc.1",
  "published_at": "2026-10-04T12:00:00Z", "draft": false, "prerelease": true, "body": "ignored"},
 {"tag_name": "v2.0.0", "name": "Spring", "html_url": "https://github.com/example-org/example-project/releases/tag/v2.0.0",
  "published_at": "2026-10-01T08:00:00Z", "draft": false, "prerelease": false},
 {"tag_name": "v3.0.0", "name": "draft", "html_url": "", "published_at": null, "draft": true, "prerelease": false},
 {"tag_name": "v1.9.9", "name": "v1.9.9", "html_url": "", "published_at": null, "draft": false, "prerelease": false}
]`

// Positive: a GitHub REST releases listing yields one entry per published release, named by name
// and tag and marked when it is a prerelease; a draft is left out and a release without a
// publication time is counted as undated.
func TestParseReleases_Positive_Listing(t *testing.T) {
	got, err := ParseReleases([]byte(releaseListing))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 2 || got.Undated != 1 {
		t.Fatalf("entries = %+v", got)
	}
	if rc := got.Items[0]; rc.Title != "v2.1.0-rc.1 [prerelease]" || !rc.Published.Equal(time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("prerelease = %+v", rc)
	}
	if spring := got.Items[1]; spring.Title != "Spring (v2.0.0)" || !strings.HasSuffix(spring.Link, "/v2.0.0") {
		t.Errorf("release = %+v", spring)
	}
}

// Negative: the API's error object, null, malformed JSON, trailing data, an empty file and an
// oversized listing are errors, never an empty listing.
func TestParseReleases_Negative_Refusals(t *testing.T) {
	for _, body := range []string{
		`{"message": "Not Found"}`,
		`null`,
		`[{"tag_name": 1}]`,
		`[] []`,
		``,
		`[{"published_at": "yesterday"}]`,
	} {
		if _, err := ParseReleases([]byte(body)); err == nil {
			t.Errorf("%q accepted", body)
		}
	}
	if _, err := ParseReleases(make([]byte, MaxReleaseBytes+1)); err == nil || !strings.Contains(err.Error(), "exceed the cap") {
		t.Errorf("oversize: %v", err)
	}
}

// releases renders a listing of count published releases.
func releases(count int) string {
	parts := make([]string, count)
	for i := range parts {
		parts[i] = fmt.Sprintf(`{"tag_name":"v%d","published_at":"2026-10-01T00:00:00Z"}`, i)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// Boundary: an empty array is a valid listing with no entries; MaxReleases releases are read and
// one more is refused.
func TestParseReleases_Boundary_Caps(t *testing.T) {
	if got, err := ParseReleases([]byte(" [ ] ")); err != nil || len(got.Items) != 0 || got.Undated != 0 {
		t.Errorf("empty array: %+v, %v", got, err)
	}
	if got, err := ParseReleases([]byte(releases(MaxReleases))); err != nil || len(got.Items) != MaxReleases {
		t.Errorf("%d releases: %d, %v", MaxReleases, len(got.Items), err)
	}
	if _, err := ParseReleases([]byte(releases(MaxReleases + 1))); err == nil || !strings.Contains(err.Error(), "exceed the cap") {
		t.Errorf("%d releases: %v", MaxReleases+1, err)
	}
}
