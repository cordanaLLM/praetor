// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

// Package releasesmoke verifies the assets attached to a published release.
package releasesmoke

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/forge"
)

// DefaultMaxBytes is the total asset download budget (4 GiB).
const DefaultMaxBytes int64 = 4 << 30

// Options declares the release and its download budget. MaxBytes must be positive.
type Options struct {
	Tag           string
	AllowNoAssets bool
	MaxBytes      int64
}

// Result retains named observations, including unverified signatures, on failure too.
type Result struct {
	Lines []string
	Bytes int64
}

// Run verifies shipped bytes using the existing forge driver's read-only REST access.
func Run(ctx context.Context, driver *forge.GitHubDriver, opts Options) (Result, error) {
	var result Result
	if ctx == nil || driver == nil {
		return result, errors.New("release smoke: context and forge driver required")
	}
	if opts.MaxBytes <= 0 {
		return result, errors.New("release smoke: max-bytes must be positive")
	}
	release, err := driver.ReleaseByTag(ctx, opts.Tag)
	if err != nil {
		return result, err
	}
	if len(release.Assets) == 0 {
		return assetless(opts)
	}
	assets, err := indexAssets(release.Assets)
	if err != nil {
		return result, fmt.Errorf("release %s: %w", opts.Tag, err)
	}
	recordUnverified(&result, release.Assets)
	sums, found, err := manifest(release.Assets)
	if err == nil && found {
		err = verifyChecksums(ctx, driver, opts, &result, release.Assets, assets, sums)
	} else if err == nil {
		err = verifyPlain(ctx, driver, opts, &result, release.Assets)
	}
	if err != nil {
		return result, fmt.Errorf("release %s: %w", opts.Tag, err)
	}
	result.Lines = append(result.Lines, fmt.Sprintf("release %s: asset checks passed (%d bytes)", opts.Tag, result.Bytes))
	return result, nil
}

func assetless(opts Options) (Result, error) {
	line := fmt.Sprintf("release %s: zero assets", opts.Tag)
	if !opts.AllowNoAssets {
		return Result{}, errors.New(line + " (use --allow-no-assets only for an explicitly asset-less release)")
	}
	return Result{Lines: []string{line + "; explicitly allowed by --allow-no-assets"}}, nil
}

func indexAssets(assets []forge.ReleaseAsset) (map[string]forge.ReleaseAsset, error) {
	byName := make(map[string]forge.ReleaseAsset, len(assets))
	ids := make(map[int64]bool, len(assets))
	for i := 0; i < len(assets); i++ {
		a := assets[i]
		if a.ID <= 0 || a.Size < 0 || !validName(a.Name) {
			return nil, fmt.Errorf("invalid asset metadata: %q", a.Name)
		}
		if _, exists := byName[a.Name]; exists || ids[a.ID] {
			return nil, fmt.Errorf("duplicate release asset: %s", a.Name)
		}
		byName[a.Name] = a
		ids[a.ID] = true
	}
	return byName, nil
}

func recordUnverified(result *Result, assets []forge.ReleaseAsset) {
	for i := 0; i < len(assets); i++ {
		if signature(assets[i].Name) {
			result.Lines = append(result.Lines, assets[i].Name+": not verified: no verifier configured")
		}
	}
}

// signatureSuffixes name the signature and attestation files a release ships beside its assets: a sigstore bundle
// (.bundle, .sigstore, .sigstore.json), a detached signature (.sig, .asc) or a certificate (.pem). A checksum manifest
// does not list them; they are reported as not verified until a verifier is configured.
var signatureSuffixes = [...]string{".bundle", ".sig", ".asc", ".pem", ".sigstore", ".sigstore.json"}

func signature(name string) bool {
	for i := 0; i < len(signatureSuffixes); i++ {
		if strings.HasSuffix(name, signatureSuffixes[i]) {
			return true
		}
	}
	return false
}

// manifestName matches the checksum manifests fleet release tools write: SHA256SUMS, <name>-SHA256SUMS,
// checksums.txt and <name>_checksums.txt (goreleaser's default), in any case.
var manifestName = regexp.MustCompile(`(?i)(^|[-_.])(sha256sums|checksums)(\.txt)?$`)

// manifest returns the release's one checksum manifest; several are refused by name, since a check that picks one
// would leave the others unread.
func manifest(assets []forge.ReleaseAsset) (forge.ReleaseAsset, bool, error) {
	var found []forge.ReleaseAsset
	for i := 0; i < len(assets); i++ {
		if manifestName.MatchString(assets[i].Name) && !signature(assets[i].Name) {
			found = append(found, assets[i])
		}
	}
	switch len(found) {
	case 0:
		return forge.ReleaseAsset{}, false, nil
	case 1:
		return found[0], true, nil
	}
	names := make([]string, 0, len(found))
	for i := 0; i < len(found); i++ {
		names = append(names, found[i].Name)
	}
	return forge.ReleaseAsset{}, false, fmt.Errorf("several checksum manifests: %s", strings.Join(names, ", "))
}

func download(ctx context.Context, driver *forge.GitHubDriver, opts Options, result *Result, a forge.ReleaseAsset, dst io.Writer) error {
	if a.Size == 0 {
		return fmt.Errorf("%s: empty asset", a.Name)
	}
	remaining := opts.MaxBytes - result.Bytes
	if a.Size > remaining {
		return fmt.Errorf("%s: max-bytes %d exceeded", a.Name, opts.MaxBytes)
	}
	n, err := driver.StreamReleaseAsset(ctx, a.ID, remaining, dst)
	result.Bytes += n
	if err != nil {
		return fmt.Errorf("%s: %w", a.Name, err)
	}
	if n != a.Size {
		return fmt.Errorf("%s: size mismatch: API %d, downloaded %d", a.Name, a.Size, n)
	}
	return nil
}

func verifyPlain(ctx context.Context, driver *forge.GitHubDriver, opts Options, result *Result, assets []forge.ReleaseAsset) error {
	var failures []error
	for i := 0; i < len(assets); i++ {
		a := assets[i]
		if err := download(ctx, driver, opts, result, a, io.Discard); err != nil {
			failures = append(failures, err)
		} else {
			result.Lines = append(result.Lines, a.Name+": downloaded; non-empty size matches API")
		}
	}
	return errors.Join(failures...)
}

func verifyChecksums(ctx context.Context, driver *forge.GitHubDriver, opts Options, result *Result, assets []forge.ReleaseAsset, byName map[string]forge.ReleaseAsset, sums forge.ReleaseAsset) error {
	parser := newChecksumParser()
	if err := download(ctx, driver, opts, result, sums, parser); err != nil {
		return err
	}
	if err := parser.finish(); err != nil {
		return fmt.Errorf("%s: %w", sums.Name, err)
	}
	failures := coverageFailures(assets, byName, parser, sums.Name)
	for i := 0; i < len(parser.entries); i++ {
		entry := parser.entries[i]
		a, exists := byName[entry.name]
		if !exists {
			continue
		}
		if err := verifyDigest(ctx, driver, opts, result, a, entry.digest); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func coverageFailures(assets []forge.ReleaseAsset, byName map[string]forge.ReleaseAsset, parser *checksumParser, sums string) []error {
	var failures []error
	for i := 0; i < len(parser.entries); i++ {
		name := parser.entries[i].name
		if _, ok := byName[name]; !ok {
			failures = append(failures, fmt.Errorf("%s: listed in %s but missing from release", name, sums))
		}
	}
	for i := 0; i < len(assets); i++ {
		name := assets[i].Name
		if name != sums && !signature(name) && !parser.names[name] {
			failures = append(failures, fmt.Errorf("%s: missing from %s", name, sums))
		}
	}
	return failures
}

func verifyDigest(ctx context.Context, driver *forge.GitHubDriver, opts Options, result *Result, a forge.ReleaseAsset, want string) error {
	hash := sha256.New()
	if err := download(ctx, driver, opts, result, a, hash); err != nil {
		return err
	}
	if got := fmt.Sprintf("%x", hash.Sum(nil)); got != want {
		return fmt.Errorf("%s: SHA-256 mismatch: want %s, got %s", a.Name, want, got)
	}
	result.Lines = append(result.Lines, a.Name+": SHA-256 matches")
	return nil
}
