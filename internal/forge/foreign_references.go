// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// ResolveOwnOrganizations returns the trusted forge organisations from extra, the manifest at
// manifestPath, and the repository owner from GITHUB_REPOSITORY (via getenv), the origin remote
// of repo, or the manifest's repository.owner. A missing manifest or origin adds no extra owner;
// invalid manifests and failed reads are errors.
func ResolveOwnOrganizations(ctx context.Context, repo, manifestPath string, extra []string, getenv func(string) string) ([]string, error) {
	resolved, err := ResolveOwnOrganizationSources(ctx, repo, manifestPath, extra, getenv)
	if resolved.ManifestProblem != nil && !errors.Is(resolved.ManifestProblem, os.ErrNotExist) {
		return resolved.Organizations, resolved.ManifestProblem
	}
	return resolved.Organizations, err
}

// OrganizationSources records independently trusted policy and a manifest finding. An invalid
// manifest contributes no organisations; callers deciding writes can still inspect plain text
// and use the operator layer or repository identity without accepting invalid policy.
type OrganizationSources struct {
	Organizations   []string
	Sources         []string
	ManifestProblem error
}

// ResolveOwnOrganizationSources shares the strict resolver's sources, retaining manifest
// failures as findings. Identity lookup errors remain errors, never trusted policy.
func ResolveOwnOrganizationSources(ctx context.Context, repo, manifestPath string, extra []string, getenv func(string) string) (OrganizationSources, error) {
	ctx, cancel := context.WithTimeout(ctx, util.GitProbeTimeout)
	defer cancel()
	resolved := OrganizationSources{Organizations: slices.Clone(extra)}
	if err := ctx.Err(); err != nil {
		return resolved, fmt.Errorf("resolve organisation sources: %w", err)
	}
	if len(extra) > 0 {
		resolved.Sources = append(resolved.Sources, "operator layer")
	}
	var manifest *config.Manifest
	if manifestPath != "" {
		var err error
		manifest, err = config.LoadManifest(manifestPath)
		if err != nil {
			resolved.ManifestProblem = err
		}
	}
	if manifest != nil && manifest.Forge != nil {
		resolved.Organizations = append(resolved.Organizations, manifest.Forge.OwnOrganizations...)
		resolved.Sources = append(resolved.Sources, "valid manifest")
	}
	owner, source, err := resolveReferenceOwner(ctx, repo, manifest, getenv)
	if err != nil {
		return resolved, err
	}
	if owner != "" {
		resolved.Organizations = append(resolved.Organizations, owner)
		resolved.Sources = append(resolved.Sources, source)
	}
	return resolved, nil
}

// resolveReferenceOwner resolves the repository owner from GITHUB_REPOSITORY (via getenv),
// the origin remote of repo, or manifest.Repository.Owner, refusing malformed
// GITHUB_REPOSITORY values and unexpected remote-lookup errors.
func resolveReferenceOwner(ctx context.Context, repo string, manifest *config.Manifest, getenv func(string) string) (string, string, error) {
	if identity := lookupEnv(getenv, "GITHUB_REPOSITORY"); identity != "" {
		owner, name, _ := strings.Cut(identity, "/")
		if err := util.ValidateGitHubRepositoryIdentity(owner, name); err != nil {
			return "", "", fmt.Errorf("GITHUB_REPOSITORY: %w", err)
		}
		return owner, "GITHUB_REPOSITORY", nil
	}
	if repo != "" {
		owner, _, err := util.ResolveRemoteIdentity(ctx, repo)
		if err == nil {
			return owner, "git remote owner", nil
		}
		if !errors.Is(err, util.ErrRepoIdentityUnresolved) {
			return "", "", err
		}
	}
	if manifest != nil {
		return manifest.Repository.Owner, "valid manifest repository.owner", nil
	}
	return "", "", nil
}

// lookupEnv reads key through getenv, returning an empty string when getenv is nil.
func lookupEnv(getenv func(string) string, key string) string {
	if getenv == nil {
		return ""
	}
	return getenv(key)
}

// Order: engine-fr-rule-checks-20261027. Use the closing-reference grammar and decoder.
var textReferenceRegex = regexp.MustCompile(`(?i)^(` + referencePattern("issues|pulls?|discussions", "[#!]") + `)\b`)

const (
	maxReferenceTextBytes = 1 << 20
	maxReferenceOwners    = config.MaxOwnOrganizations + 1
)

// ForeignReferenceFindings refuses active references outside ownOrganizations. Bare numbers
// remain local; code spans and fenced blocks cannot post a cross-repository reference.
func ForeignReferenceFindings(text, where string, ownOrganizations []string) []string {
	if len(ownOrganizations) > maxReferenceOwners {
		return []string{where + ": own organizations exceeds 65 entries including repository owner"}
	}
	if len(text) > maxReferenceTextBytes {
		return []string{fmt.Sprintf("%s exceeds %d bytes", where, maxReferenceTextBytes)}
	}
	lines := strings.SplitN(text, "\n", maxCommitMessageLines+1)
	if len(lines) > maxCommitMessageLines {
		return []string{fmt.Sprintf("%s exceeds %d lines", where, maxCommitMessageLines)}
	}
	visible := referenceProse(lines)
	var findings []string
	line := 1
	for pos := 0; pos < len(visible) && pos < maxReferenceTextBytes; pos++ {
		if visible[pos] == '\n' {
			line++
			continue
		}
		if pos > 0 && referenceWordByte(visible[pos-1]) {
			continue
		}
		match := textReferenceRegex.FindStringSubmatchIndex(visible[pos:])
		if match == nil {
			continue
		}
		ref := referenceFromMatch(visible[pos:], match, 1)
		if foreignReference(ref, ownOrganizations) {
			findings = append(findings, fmt.Sprintf("%s line %d: foreign reference %q; put it in inline code or a code fence", where, line, ref.ref))
		}
		pos += match[1] - 1
	}
	return findings
}

func referenceWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.ContainsRune("_./-", rune(b))
}

func foreignReference(ref closingReference, own []string) bool {
	if ref.owner == "" {
		return false
	}
	if strings.Contains(ref.ref, "://") && !strings.HasPrefix(strings.ToLower(ref.ref), "https://github.com/") {
		return false
	}
	for i := 0; i < len(own) && i < maxReferenceOwners; i++ {
		if strings.EqualFold(ref.owner, own[i]) {
			return false
		}
	}
	return true
}

// referenceProse preserves newlines while masking fences and inline code using the shared
// Markdown readers. Code spans may cross physical lines in a paragraph.
func referenceProse(lines []string) string {
	fence := util.MarkdownFence{}
	kept := make([]string, len(lines))
	for i := 0; i < len(lines) && i < maxCommitMessageLines; i++ {
		if !fence.Inside(strings.TrimSpace(lines[i])) {
			kept[i] = strings.TrimSuffix(lines[i], "\r")
		}
	}
	paragraphs := strings.Split(strings.Join(kept, "\n"), "\n\n")
	for i := 0; i < len(paragraphs) && i < maxCommitMessageLines; i++ {
		paragraphs[i] = maskReferenceCodeSpans(paragraphs[i])
	}
	return strings.Join(paragraphs, "\n\n")
}

func maskReferenceCodeSpans(text string) string {
	masked := []byte(text)
	spans := util.MarkdownCodeSpans(text, maxReferenceTextBytes)
	for i := 0; i < len(spans) && i < maxReferenceTextBytes; i++ {
		for j := spans[i].Start; j < spans[i].End && j < maxReferenceTextBytes; j++ {
			if masked[j] != '\n' {
				masked[j] = ' '
			}
		}
	}
	return string(masked)
}
