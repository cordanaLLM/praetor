// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// maxSecurityFormLinks bounds the report-form links one policy is read for (HISS-02).
const maxSecurityFormLinks = 64

// securityRemoteTimeout bounds the origin remote read that resolves an undeclared forge (HISS-02).
const securityRemoteTimeout = 30 * time.Second

// advisoryFormLink matches a link to a GitHub repository's private vulnerability report form and
// captures the owner/name path in front of /security/advisories/new.
var advisoryFormLink = regexp.MustCompile(`(?i)https://[^\s/<>()\[\]]+/([^\s/<>()\[\]]+/[^\s/<>()\[\]]+)/security/advisories/new(?:[\s<>()\[\]#?.,;:]|$)`)

// confidentialIssueLink matches a link to a GitLab new-issue form that prefills a confidential
// issue, the query parameter literal or percent-encoded.
var confidentialIssueLink = regexp.MustCompile(`(?i)https://[^\s<>()]+/-/issues/new\?[^\s<>()]*issue(?:\[|%5B)confidential(?:\]|%5D)=true`)

// auditSecurityPolicy extends community health in the existing audit path. The policy is checked
// for the concrete artifact of the channel the repository declares in security.channel, or of the
// migration default when none is declared (config.ResolveSecurityChannel); its prose is never
// interpreted. Declined policies keep their existing contract; unread settings are named and never
// fail offline.
func auditSecurityPolicy(ctx context.Context, manifest *config.Manifest, root string, live actionsForge) error {
	decline, err := adopt.AuditDecline(manifest, "security-policy")
	if err != nil {
		return fmt.Errorf("[FAIL] Community health: SECURITY.md: %w", err)
	}
	if decline.Declined {
		fmt.Println("[INFO] Community health: SECURITY.md declined; private vulnerability reporting setting not read.")
		return nil
	}
	path, content, err := readSecurityPolicy(ctx, root)
	if err != nil {
		return err
	}
	choice, err := securityChannelOf(ctx, manifest, root)
	if err != nil {
		return fmt.Errorf("[FAIL] Community health: %s: %w", path, err)
	}
	if !choice.Declared {
		fmt.Printf("[INFO] Community health: %s undeclared; %s checked as %s (migration default: %s); declare %s in %s.\n",
			config.SecurityChannelKey, path, choice.Channel, choice.Basis, config.SecurityChannelKey, config.ManifestFileName)
	}
	artifact, found := securityChannelArtifact(choice.Channel, content, manifest)
	if !found {
		return fmt.Errorf("[FAIL] Community health: %s lacks %s, the artifact of %s %s", path, artifact, config.SecurityChannelKey, choice.Channel)
	}
	fmt.Printf("[PASS] Community health: %s names the %s channel: %s.\n", path, choice.Channel, artifact)
	if choice.Channel != config.SecurityChannelPrivateReporting {
		return nil
	}
	return auditPrivateVulnerabilityReporting(ctx, path, live)
}

// securityChannelOf resolves the channel the policy is checked against: the manifest's
// security.channel, else the migration default for the repository's forge. A declared
// forge-native channel on a resolved forge it does not run on is an error.
func securityChannelOf(ctx context.Context, manifest *config.Manifest, root string) (config.SecurityChannelChoice, error) {
	var policy *config.SecurityPolicy
	declared := config.Forge("")
	if manifest != nil {
		policy = manifest.Security
		declared = manifest.Repository.Forge
	}
	forge, err := auditedForge(ctx, declared, root)
	if err != nil {
		return config.SecurityChannelChoice{}, err
	}
	choice, err := config.ResolveSecurityChannel(policy, forge)
	if err != nil {
		return choice, err
	}
	if want := config.SecurityChannelForge(choice.Channel); want != "" && forge != "" && forge != want {
		return choice, fmt.Errorf("%s %s runs on forge %s, but the repository's forge is %s", config.SecurityChannelKey, choice.Channel, want, forge)
	}
	return choice, nil
}

// auditedForge is the repository's forge: repository.forge when declared, else the forge the
// origin remote's host names (config.RepositoryForge), or "" when neither resolves one. A remote
// read git did not answer is an error, never an unresolved forge.
func auditedForge(ctx context.Context, declared config.Forge, root string) (config.Forge, error) {
	if declared != "" {
		return config.RepositoryForge(declared, "")
	}
	readCtx, cancel := context.WithTimeout(ctx, securityRemoteTimeout)
	defer cancel()
	remote, err := util.ReadOriginRemote(readCtx, root)
	if err != nil && !errors.Is(err, util.ErrRepoIdentityUnresolved) {
		return "", fmt.Errorf("resolve %s: %w", config.ForgeKey, err)
	}
	forge, err := config.RepositoryForge("", remote.Host)
	if errors.Is(err, config.ErrForgeUndeclared) {
		return "", nil
	}
	return forge, err
}

// securityChannelArtifact names the concrete artifact of channel and reports whether content
// holds it: the repository's report-form link for private-reporting, a confidential new-issue
// link for confidential-issues, and the declared address for contact.
func securityChannelArtifact(channel config.SecurityChannel, content string, manifest *config.Manifest) (string, bool) {
	switch channel {
	case config.SecurityChannelPrivateReporting:
		return advisoryFormArtifact(content, manifest)
	case config.SecurityChannelConfidentialIssues:
		return "a link to the new-issue form with issue[confidential]=true (/-/issues/new?issue[confidential]=true)",
			confidentialIssueLink.MatchString(content)
	}
	contact := ""
	if manifest != nil {
		contact = manifest.Security.SecurityContact()
	}
	return "the security.contact address " + contact, contactNamed(content, contact)
}

// advisoryFormArtifact reports whether content links the repository's private vulnerability
// report form. With repository.owner and repository.name declared, the link must name that
// repository; without them, any report-form link counts.
func advisoryFormArtifact(content string, manifest *config.Manifest) (string, bool) {
	coordinate := ""
	if manifest != nil && manifest.Repository.Owner != "" && manifest.Repository.Name != "" {
		coordinate = manifest.Repository.Owner + "/" + manifest.Repository.Name
	}
	want := "a link to the private vulnerability report form (https://github.com/<owner>/<name>/security/advisories/new)"
	if coordinate != "" {
		want = "a link to https://github.com/" + coordinate + "/security/advisories/new"
	}
	links := advisoryFormLink.FindAllStringSubmatch(content, maxSecurityFormLinks)
	for i := 0; i < len(links) && i < maxSecurityFormLinks; i++ {
		if coordinate == "" || strings.EqualFold(links[i][1], coordinate) {
			return want, true
		}
	}
	return want, false
}

// contactNamed reports whether content names the declared contact verbatim; a mailto: contact
// also counts when its bare address appears.
func contactNamed(content, contact string) bool {
	if contact == "" {
		return false
	}
	if strings.Contains(content, contact) {
		return true
	}
	address, isMailto := strings.CutPrefix(contact, "mailto:")
	return isMailto && address != "" && strings.Contains(content, address)
}

// readSecurityPolicy reads the first policy in GitHub's community-file search order through
// the existing bounded, context-aware reader; an unreadable file never becomes an absence.
func readSecurityPolicy(ctx context.Context, root string) (string, string, error) {
	paths := [...]string{".github/SECURITY.md", "SECURITY.md", "docs/SECURITY.md"}
	for i := 0; i < len(paths); i++ {
		data, exists, err := contextopt.ObserveSnapshotIn(ctx, root, paths[i])
		if err != nil {
			return "", "", fmt.Errorf("[FAIL] Community health: read %s: %w", paths[i], err)
		}
		if exists {
			return paths[i], string(data), nil
		}
	}
	return "", "", fmt.Errorf("[FAIL] Community health: SECURITY.md missing; add .github/SECURITY.md, SECURITY.md or docs/SECURITY.md naming the %s channel", config.SecurityChannelKey)
}

// auditPrivateVulnerabilityReporting reports unread settings separately from a verified
// disabled setting, which contradicts a policy directing reporters to that channel.
func auditPrivateVulnerabilityReporting(ctx context.Context, path string, live actionsForge) error {
	if live.reporting == nil {
		fmt.Printf("[SKIP] %s: private vulnerability reporting setting not read: %s\n", path, live.notMade)
		return nil
	}
	enabled, err := live.reporting.PrivateVulnerabilityReporting(ctx)
	if err != nil {
		fmt.Printf("[SKIP] %s: private vulnerability reporting setting not read: %v\n", path, err)
		return nil
	}
	if !enabled {
		return fmt.Errorf("[FAIL] Community health: %s names private vulnerability reporting, but the forge setting is enabled=false (mismatch)", path)
	}
	fmt.Printf("[PASS] %s: private vulnerability reporting setting enabled=true.\n", path)
	return nil
}
