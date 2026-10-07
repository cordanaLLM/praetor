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

// maxHostLabels bounds the labels matchDomain walks (HISS-02): httpendpoint.CanonicalHost, which
// every source URL passes first, admits at most 127.
const maxHostLabels = 127

// The host tables below name registrable domains. matchDomain matches a host and every parent
// domain of it, so one entry covers each spelling a service answers on: www., mobile., m. and
// country subdomains such as de.linkedin.com. A subdomain the service uses for something other
// than accounts is refused with the rest; refusing a source by mistake is the side this check
// errs on.

// personHosts are domains whose pages are accounts or person identifiers.
var personHosts = map[string]string{
	"linkedin.com":       "a social network profile",
	"twitter.com":        "a social network profile",
	"x.com":              "a social network profile",
	"facebook.com":       "a social network profile",
	"instagram.com":      "a social network profile",
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
// organisations is not supported yet. hf.co is the short domain of huggingface.co.
var forgeHosts = map[string]bool{
	"github.com": true, "gitlab.com": true, "codeberg.org": true, "gitea.com": true,
	"bitbucket.org": true, "huggingface.co": true, "hf.co": true,
}

// accountSegments are first path segments that introduce an account page on any host.
var accountSegments = map[string]bool{
	"user": true, "users": true, "u": true, "people": true, "person": true, "profile": true,
	"profiles": true, "author": true, "authors": true, "member": true, "members": true,
	"account": true, "accounts": true,
}

// hostAccountSegments are first path segments that introduce a person page on one domain.
var hostAccountSegments = map[string]map[string]string{
	"arxiv.org": {"a": "an arXiv author listing"},
	"dblp.org":  {"pid": "a dblp person page"},
}

// matchDomain returns the entry table holds for host or for the nearest parent domain of host.
func matchDomain[V any](table map[string]V, host string) (V, bool) {
	for i := 0; i < maxHostLabels; i++ {
		if value, ok := table[host]; ok {
			return value, true
		}
		_, parent, found := strings.Cut(host, ".")
		if !found {
			break
		}
		host = parent
	}
	var none V
	return none, false
}

// personURL reports whether host and the path segments address one account, and why.
func personURL(host string, segments []string) (string, bool) {
	if reason, ok := matchDomain(personHosts, host); ok {
		return reason, true
	}
	if _, forge := matchDomain(forgeHosts, host); forge && len(segments) == 1 {
		return "a single path segment on a code host is an account page or an account's activity feed", true
	}
	if len(segments) == 0 {
		return "", false
	}
	return accountPath(host, segments)
}

// accountPath reports whether a path of at least one segment addresses an account on host.
func accountPath(host string, segments []string) (string, bool) {
	if prefixes, ok := matchDomain(hostAccountSegments, host); ok && prefixes[segments[0]] != "" {
		return prefixes[segments[0]], true
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
