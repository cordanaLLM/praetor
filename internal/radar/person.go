// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package radar

import "strings"

// maxPathSegments bounds the path segments personURL inspects (HISS-02). MaxURLBytes already
// bounds the path; this bounds the loop.
const maxPathSegments = MaxURLBytes / 2

// The person refusal is structural, not a judgement of content: it refuses the URL shapes that
// address one account. A URL that names a person in a shape not listed here passes, which the
// radar guide states (docs/guides/radar.md#what-the-registry-refuses).

// personHosts are hosts whose pages are accounts or person identifiers.
var personHosts = map[string]string{
	"linkedin.com":       "a social network profile",
	"www.linkedin.com":   "a social network profile",
	"twitter.com":        "a social network profile",
	"x.com":              "a social network profile",
	"facebook.com":       "a social network profile",
	"www.facebook.com":   "a social network profile",
	"instagram.com":      "a social network profile",
	"www.instagram.com":  "a social network profile",
	"bsky.app":           "a social network profile",
	"orcid.org":          "a researcher identifier",
	"scholar.google.com": "an author citation profile",
	"gist.github.com":    "gists belong to one account",
	"keybase.io":         "an identity profile",
	"about.me":           "an identity profile",
	"gravatar.com":       "an identity profile",
}

// forgeHosts are code hosts where a single path segment is an account: a user or organisation
// page, or an account's activity feed such as https://github.com/<login>.atom. An organisation
// cannot be told from a user offline, so both are refused; the github_owner kind that will read
// organisations is not supported yet.
var forgeHosts = map[string]bool{
	"github.com": true, "gitlab.com": true, "codeberg.org": true, "gitea.com": true,
	"bitbucket.org": true, "huggingface.co": true,
}

// accountSegments are first path segments that introduce an account page on any host.
var accountSegments = map[string]bool{
	"user": true, "users": true, "u": true, "people": true, "person": true, "profile": true,
	"profiles": true, "author": true, "authors": true, "member": true, "members": true,
	"account": true, "accounts": true,
}

// hostAccountPrefixes are first path segments that introduce a person page on one host.
var hostAccountPrefixes = map[string]string{
	"arxiv.org/a":        "an arXiv author listing",
	"export.arxiv.org/a": "an arXiv author listing",
	"dblp.org/pid":       "a dblp person page",
}

// personURL reports whether host and the path segments address one account, and why.
func personURL(host string, segments []string) (string, bool) {
	if reason, ok := personHosts[host]; ok {
		return reason, true
	}
	if forgeHosts[host] && len(segments) == 1 {
		return "a single path segment on a code host is an account page or an account's activity feed", true
	}
	if len(segments) == 0 {
		return "", false
	}
	if reason, ok := hostAccountPrefixes[host+"/"+segments[0]]; ok {
		return reason, true
	}
	if accountSegments[strings.ToLower(segments[0])] {
		return "the path segment " + segments[0] + " introduces an account page", true
	}
	if handleSegment(segments) {
		return "a path segment starting with @ or ~ is an account page", true
	}
	return "", false
}

// handleSegment reports whether a path segment starts with '@' (a fediverse or blog handle) or
// '~' (a home directory page).
func handleSegment(segments []string) bool {
	for i := 0; i < len(segments) && i < maxPathSegments; i++ {
		if strings.HasPrefix(segments[i], "@") || strings.HasPrefix(segments[i], "~") {
			return true
		}
	}
	return false
}

// pathSegments returns the non-empty segments of a URL path, at most maxPathSegments of them.
func pathSegments(path string) []string {
	parts := strings.Split(path, "/")
	segments := make([]string, 0, len(parts))
	for i := 0; i < len(parts) && len(segments) < maxPathSegments; i++ {
		if parts[i] != "" {
			segments = append(segments, parts[i])
		}
	}
	return segments
}
