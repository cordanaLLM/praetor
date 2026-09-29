// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
)

const (
	lookupTaggedSHA = "0dceb95e7c4cad8cc7422aee3885998f5cab9c79"
	lookupOtherSHA  = "ccfb013c15c8afb7bf2b7c028fb74dc5a068cccc"
)

// Positive: CommitAt asks the commits endpoint for a qualified tag or a SHA and returns the
// commit the forge names; TagsAt walks the tag listing and returns every tag on the commit,
// none for a commit no tag carries.
func TestCommitAtAndTagsAtPositive(t *testing.T) {
	gh, fake := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		switch r.URL.Path {
		case "/repos/acme/widgets/commits/refs/tags/v1.4.0", "/repos/acme/widgets/commits/" + lookupTaggedSHA:
			writeJSON(t, w, http.StatusOK, map[string]any{"sha": lookupTaggedSHA, "files": []any{}})
		case "/repos/acme/widgets/tags":
			writeJSON(t, w, http.StatusOK, []map[string]any{
				{"name": "v1.4.0", "commit": map[string]string{"sha": lookupTaggedSHA}},
				{"name": "v1.3.4", "commit": map[string]string{"sha": lookupOtherSHA}},
				{"name": "v1", "commit": map[string]string{"sha": lookupTaggedSHA}},
			})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	})
	for _, ref := range []string{"refs/tags/v1.4.0", lookupTaggedSHA} {
		if sha, err := gh.CommitAt(t.Context(), ref); err != nil || sha != lookupTaggedSHA {
			t.Fatalf("CommitAt(%s) = %q, %v", ref, sha, err)
		}
	}
	tags, err := gh.TagsAt(t.Context(), lookupTaggedSHA)
	if err != nil || !slices.Equal(tags, []string{"v1.4.0", "v1"}) {
		t.Fatalf("TagsAt = %v, %v", tags, err)
	}
	if last := fake.requests[len(fake.requests)-1]; last.Query != "per_page=100&page=1" {
		t.Fatalf("tag listing query = %q", last.Query)
	}
	if untagged, err := gh.TagsAt(t.Context(), strings.Repeat("a", 40)); err != nil || len(untagged) != 0 {
		t.Fatalf("TagsAt(untagged) = %v, %v", untagged, err)
	}
}

// Negative: a 422 "No commit found" is ErrCommitNotFound, a 404 is ErrRepositoryNotVisible,
// and a rate limit, a spam 422, a malformed body and a missing token are plain errors, never
// a missing commit.
func TestCommitAtNegative(t *testing.T) {
	answers := map[string]func(http.ResponseWriter){
		"missing": func(w http.ResponseWriter) {
			writeJSON(t, w, http.StatusUnprocessableEntity, map[string]string{"message": "No commit found for SHA: " + lookupOtherSHA})
		},
		"hidden": func(w http.ResponseWriter) {
			writeJSON(t, w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		},
		"limited": func(w http.ResponseWriter) {
			writeJSON(t, w, http.StatusForbidden, map[string]string{"message": "API rate limit exceeded"})
		},
		"spam": func(w http.ResponseWriter) {
			writeJSON(t, w, http.StatusUnprocessableEntity, map[string]string{"message": "Validation Failed"})
		},
		"garbled": func(w http.ResponseWriter) { writeJSON(t, w, http.StatusOK, map[string]string{"sha": "HEAD"}) },
	}
	gh, _ := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		answers[strings.TrimPrefix(r.URL.Path, "/repos/acme/widgets/commits/")](w)
	})
	if _, err := gh.CommitAt(t.Context(), "missing"); !errors.Is(err, ErrCommitNotFound) {
		t.Fatalf("missing commit: %v", err)
	}
	if _, err := gh.CommitAt(t.Context(), "hidden"); !errors.Is(err, ErrRepositoryNotVisible) {
		t.Fatalf("hidden repository: %v", err)
	}
	for _, ref := range []string{"limited", "spam", "garbled"} {
		if sha, err := gh.CommitAt(t.Context(), ref); err == nil || errors.Is(err, ErrCommitNotFound) || errors.Is(err, ErrRepositoryNotVisible) || sha != "" {
			t.Fatalf("%s: CommitAt = %q, %v; want a plain error", ref, sha, err)
		}
	}
	gh.Token = ""
	if _, err := gh.CommitAt(t.Context(), "missing"); err == nil || errors.Is(err, ErrCommitNotFound) {
		t.Fatalf("no token: %v", err)
	}
}

// Boundary: an empty or dot segment never reaches the forge, a tag segment is path-escaped,
// and a tag listing past its page bound is an error.
func TestCommitAtAndTagsAtBoundary(t *testing.T) {
	pages := 0
	gh, fake := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		if r.URL.Path == "/repos/acme/widgets/tags" {
			pages++
			full := make([]map[string]any, 100)
			for index := range full {
				full[index] = map[string]any{"name": fmt.Sprintf("v0.%d.%d", pages, index), "commit": map[string]string{"sha": lookupOtherSHA}}
			}
			writeJSON(t, w, http.StatusOK, full)
			return
		}
		writeJSON(t, w, http.StatusOK, map[string]string{"sha": lookupTaggedSHA})
	})
	for _, ref := range []string{"", "refs/tags/..", "refs//v1", "refs/tags/."} {
		if _, err := gh.CommitAt(t.Context(), ref); err == nil {
			t.Fatalf("ref %q reached the forge", ref)
		}
	}
	if len(fake.requests) != 0 {
		t.Fatalf("refused refs sent %d requests", len(fake.requests))
	}
	if _, err := gh.CommitAt(t.Context(), "refs/tags/v1?x#y"); err != nil {
		t.Fatalf("escaped tag: %v", err)
	}
	if escaped := fake.requests[0].Escaped; escaped != "/repos/acme/widgets/commits/refs/tags/v1%3Fx%23y" {
		t.Fatalf("escaped path = %q", escaped)
	}
	if _, err := gh.TagsAt(t.Context(), lookupTaggedSHA); !errors.Is(err, errPageCeiling) || pages != maxTagPages {
		t.Fatalf("unbounded tag listing: pages=%d err=%v", pages, err)
	}
}
