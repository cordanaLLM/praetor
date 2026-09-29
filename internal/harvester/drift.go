// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package harvester

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// MaxDriftPaths bounds the path prefixes one drift survey reads (HISS-02).
	MaxDriftPaths = 32
	// MaxDriftFilesPerRepository bounds the committed files one repository contributes under
	// the surveyed paths; a repository holding more is recorded as unreadable, not truncated.
	MaxDriftFilesPerRepository = 5000
	// MaxDriftSharedPaths bounds the paths held by two or more repositories that one survey
	// compares; the rest are left out and the report says so.
	MaxDriftSharedPaths = 500
	// MaxDriftBlobBytes bounds one copy the survey reads; a larger copy is recorded as unread.
	MaxDriftBlobBytes = 1 << 20
	// driftListingBytes bounds one repository's committed-file listing.
	driftListingBytes = 2 << 20
	// driftRevisionBytes bounds the HEAD revision probe.
	driftRevisionBytes = 256
)

// Drift repository states: surveyed (its HEAD was listed), unborn (no commit, so nothing is
// tracked to compare) and unreadable (a probe failed; the report is incomplete).
const (
	DriftRepositorySurveyed   = "surveyed"
	DriftRepositoryUnborn     = "unborn"
	DriftRepositoryUnreadable = "unreadable"
)

// DefaultDriftPaths returns the path prefixes SurveyDrift reads when the caller names none: the
// directories that conventionally hold governance and privacy scripts copied by hand between
// repositories (agent hook scripts, Git hook scripts and repository scripts).
func DefaultDriftPaths() []string {
	return []string{".agents/hooks-scripts/", ".githooks/", "scripts/"}
}

// DriftOptions selects what SurveyDrift compares. Root is the dev root repository names are
// reported relative to; Paths are repository-relative path prefixes in slash form, and an
// empty list selects DefaultDriftPaths.
type DriftOptions struct {
	Root  string
	Paths []string
}

// DriftReport is the read-only result of one fleet drift survey. Drifted and Identical hold
// every path two or more repositories carry, split by whether their contents agree. Complete
// is false when a repository or a copy could not be read or a bound was reached, and Errors
// says which; InventoryComplete repeats the underlying workstation inventory's completeness.
type DriftReport struct {
	Paths             []string          `json:"paths"`
	Repositories      []DriftRepository `json:"repositories"`
	Drifted           []SharedFile      `json:"drifted"`
	Identical         []SharedFile      `json:"identical"`
	Complete          bool              `json:"complete"`
	Truncated         bool              `json:"truncated"`
	InventoryComplete bool              `json:"inventory_complete"`
	Errors            []string          `json:"errors,omitempty"`
}

// DriftRepository is one repository the survey considered: its name relative to the dev root
// in slash form, the HEAD commit it read, and its state.
type DriftRepository struct {
	Name     string `json:"name"`
	Revision string `json:"revision,omitempty"`
	State    string `json:"state"`
}

// SharedFile is one path two or more repositories carry, with one variant per distinct content.
type SharedFile struct {
	Path     string        `json:"path"`
	Variants []FileVariant `json:"variants"`
}

// FileVariant is one content of a shared path and the repositories that carry it. Variants are
// ordered by how many repositories carry them, most first; LinesRemoved and LinesAdded compare a
// variant with the first one (util.LineDeltaOf), so the first variant has zero in both.
type FileVariant struct {
	Digest       string   `json:"digest"`
	Lines        int      `json:"lines"`
	Repositories []string `json:"repositories"`
	LinesRemoved int      `json:"lines_removed"`
	LinesAdded   int      `json:"lines_added"`
}

// driftCheckout is one repository the survey reads, chosen per Git identity.
type driftCheckout struct {
	name, path string
	rank       int
}

// driftCopy is one repository's committed copy of a path: the checkout index, blob and size.
type driftCopy struct {
	checkout int
	oid      string
	size     int64
}

// driftVariant pairs a reported variant with the content it was measured from.
type driftVariant struct {
	variant FileVariant
	content string
}

// SurveyDrift compares the files committed under the selected paths across the repositories
// inventory found, and reports every path two or more repositories carry: as drifted when the
// copies differ, as identical when they agree. A path one repository carries alone is not a
// finding. Each repository is read at its HEAD commit through isolated read-only Git probes
// (util.RunGitProbe), so no working-tree edit, filter or hook takes part, and linked worktrees
// and bare clones of an already surveyed repository are not counted again.
//
// The survey never writes and never fails on drift: the report is an observation, and
// enforcement is a later unit (docs/plans/fleet-drift-and-schema-conformance.md). A repository
// whose identity, revision or listing cannot be read, a copy above MaxDriftBlobBytes and a
// bound that was reached leave the report incomplete with the reason recorded, so a partial
// comparison is never published as an exhaustive one.
func SurveyDrift(ctx context.Context, inventory *WorkstationReport, opts DriftOptions) (*DriftReport, error) {
	if ctx == nil {
		return nil, errors.New("drift survey requires a context")
	}
	if inventory == nil {
		return nil, errors.New("drift survey requires a repository inventory")
	}
	paths, err := NormalizeDriftPaths(opts.Paths)
	if err != nil {
		return nil, err
	}
	report := newDriftReport(paths, inventory)
	checkouts := driftCheckouts(inventory, opts.Root, report)
	copies := make(map[string][]driftCopy)
	for i := 0; i < len(checkouts); i++ {
		if err := ctx.Err(); err != nil {
			return report, report.interrupted(err)
		}
		report.Repositories = append(report.Repositories, listDriftCopies(ctx, i, checkouts[i], paths, copies, report))
	}
	compareSharedPaths(ctx, checkouts, copies, report)
	if err := ctx.Err(); err != nil {
		return report, report.interrupted(err)
	}
	return report, nil
}

// NormalizeDriftPaths validates and normalizes the path prefixes a survey reads: each is a
// repository-relative path in slash form (a backslash is read as a separator), cleaned, with a
// trailing slash kept. An absolute path, a parent traversal, the whole tree ("."), a pathspec
// magic prefix or a drive letter is refused, as is more than MaxDriftPaths prefixes. An empty
// list selects DefaultDriftPaths. Duplicates collapse and the result is sorted.
func NormalizeDriftPaths(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return DefaultDriftPaths(), nil
	}
	if len(paths) > MaxDriftPaths {
		return nil, fmt.Errorf("drift survey reads at most %d paths, got %d", MaxDriftPaths, len(paths))
	}
	seen := make(map[string]bool, len(paths))
	normalized := make([]string, 0, len(paths))
	for i := 0; i < len(paths); i++ {
		clean, err := normalizeDriftPath(paths[i])
		if err != nil {
			return nil, err
		}
		if !seen[clean] {
			seen[clean] = true
			normalized = append(normalized, clean)
		}
	}
	sort.Strings(normalized)
	return normalized, nil
}

// normalizeDriftPath normalizes one path prefix for NormalizeDriftPaths.
func normalizeDriftPath(raw string) (string, error) {
	slashed := util.NormalizeSlashes(strings.TrimSpace(raw))
	if slashed == "" {
		return "", errors.New("drift survey path must not be empty")
	}
	if strings.ContainsAny(slashed, ":\x00") || strings.HasPrefix(slashed, "-") || path.IsAbs(slashed) {
		return "", fmt.Errorf("drift survey path %q must be relative to the repository root", raw)
	}
	clean := path.Clean(slashed)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("drift survey path %q must name a directory or file inside the repository", raw)
	}
	if strings.HasSuffix(slashed, "/") {
		clean += "/"
	}
	return clean, nil
}

// newDriftReport starts a complete report, marking it incomplete when the inventory it reads
// was truncated: repositories beyond the inventory bounds are not surveyed.
func newDriftReport(paths []string, inventory *WorkstationReport) *DriftReport {
	report := &DriftReport{
		Paths:             paths,
		Repositories:      make([]DriftRepository, 0),
		Drifted:           make([]SharedFile, 0),
		Identical:         make([]SharedFile, 0),
		Complete:          true,
		InventoryComplete: inventory.RepositoryInventoryComplete,
	}
	if inventory.RepositoryInventoryTruncated {
		report.Truncated = true
		report.fail("repository inventory truncated; repositories beyond its bounds are not surveyed")
	}
	return report
}

// fail marks the report incomplete and records why, within appendBoundedError's bound.
func (r *DriftReport) fail(message string) {
	r.Complete = false
	r.Errors = appendBoundedError(r.Errors, message)
}

// interrupted marks the report incomplete for a cancelled survey and returns the wrapped cause.
func (r *DriftReport) interrupted(cause error) error {
	r.fail("drift survey canceled")
	return fmt.Errorf("drift survey canceled: %w", cause)
}

// driftCheckouts picks one checkout per Git identity (GitCommonDir), preferring a main checkout
// over a linked worktree and a linked worktree over a bare repository, and orders them by name.
// An observation without a known identity cannot be matched with its other checkouts, so it is
// recorded as not surveyed rather than guessed at.
func driftCheckouts(inventory *WorkstationReport, root string, report *DriftReport) []driftCheckout {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = root
	}
	byIdentity := make(map[string]int)
	checkouts := make([]driftCheckout, 0, len(inventory.RepositoryObservations))
	for i := 0; i < len(inventory.RepositoryObservations) && i < MaxRepositoryObservations; i++ {
		observation := inventory.RepositoryObservations[i]
		candidate := driftCheckout{
			name: driftRepositoryName(absRoot, observation.Path),
			path: observation.Path,
			rank: driftCheckoutRank(observation.Classification),
		}
		if observation.GitCommonDir == "" {
			report.fail(candidate.name + ": repository identity unknown; not surveyed")
			continue
		}
		index, seen := byIdentity[observation.GitCommonDir]
		if !seen {
			byIdentity[observation.GitCommonDir] = len(checkouts)
			checkouts = append(checkouts, candidate)
		} else if candidate.rank < checkouts[index].rank {
			checkouts[index] = candidate
		}
	}
	sort.SliceStable(checkouts, func(a, b int) bool { return checkouts[a].name < checkouts[b].name })
	return checkouts
}

// driftRepositoryName names a checkout by its path relative to root in slash form, or by its
// own path when it lies outside root.
func driftRepositoryName(root, repoPath string) string {
	rel, err := filepath.Rel(root, repoPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return util.NormalizeSlashes(repoPath)
	}
	return util.NormalizeSlashes(rel)
}

// driftCheckoutRank orders the checkouts of one repository: a main checkout first.
func driftCheckoutRank(classification string) int {
	switch classification {
	case "main", "local-only":
		return 0
	case "linked-worktree":
		return 1
	default:
		return 2
	}
}

// listDriftCopies records checkout's committed files under paths in copies and returns the
// repository's state for the report.
func listDriftCopies(ctx context.Context, index int, checkout driftCheckout, paths []string, copies map[string][]driftCopy, report *DriftReport) DriftRepository {
	repository := DriftRepository{Name: checkout.name, State: DriftRepositoryUnreadable}
	revision, born, err := driftRevision(ctx, checkout.path)
	if err != nil {
		report.fail(checkout.name + ": HEAD revision unreadable: " + err.Error())
		return repository
	}
	if !born {
		repository.State = DriftRepositoryUnborn
		return repository
	}
	repository.Revision = revision
	listing, err := util.RunGitProbe(ctx, checkout.path, driftListingBytes,
		append([]string{"ls-tree", "-r", "-l", "-z", "--full-tree", revision, "--"}, paths...)...)
	if err != nil {
		report.fail(checkout.name + ": committed files unreadable: " + err.Error())
		return repository
	}
	entries, err := parseDriftListing(listing.Stdout)
	if err != nil {
		report.fail(checkout.name + ": " + err.Error())
		return repository
	}
	repository.State = DriftRepositorySurveyed
	for name, held := range entries {
		held.checkout = index
		copies[name] = append(copies[name], held)
	}
	return repository
}

// driftRevision returns the commit HEAD names, or born=false when the repository has no commit
// yet: git rev-parse --verify --quiet answers that with exit status 1.
func driftRevision(ctx context.Context, repoPath string) (revision string, born bool, err error) {
	result, status, err := util.RunGitProbeStatus(ctx, repoPath, driftRevisionBytes, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		return "", false, err
	}
	if status == 1 {
		return "", false, nil
	}
	revision = strings.TrimSpace(string(result.Stdout))
	if revision == "" {
		return "", false, errors.New("git answered an empty HEAD revision")
	}
	return revision, true, nil
}

// parseDriftListing reads the NUL-terminated records of git ls-tree -r -l -z, each
// "<mode> <type> <object> <size>\t<path>", and keeps the regular files by path: a symbolic
// link (mode 120000) records its target rather than content, and a submodule (type commit) is
// another repository.
func parseDriftListing(listing []byte) (map[string]driftCopy, error) {
	records := strings.Split(string(listing), "\x00")
	entries := make(map[string]driftCopy)
	for i := 0; i < len(records); i++ {
		if records[i] == "" {
			continue
		}
		name, held, keep, err := parseDriftRecord(records[i])
		if err != nil {
			return nil, err
		}
		if !keep {
			continue
		}
		if len(entries) >= MaxDriftFilesPerRepository {
			return nil, fmt.Errorf("more than %d committed files under the surveyed paths", MaxDriftFilesPerRepository)
		}
		entries[name] = held
	}
	return entries, nil
}

// parseDriftRecord parses one ls-tree record and reports whether it is a regular file.
func parseDriftRecord(record string) (name string, held driftCopy, keep bool, err error) {
	meta, name, found := strings.Cut(record, "\t")
	fields := strings.Fields(meta)
	if !found || name == "" || len(fields) != 4 {
		return "", driftCopy{}, false, fmt.Errorf("unexpected ls-tree record %q", record)
	}
	if fields[1] != "blob" || fields[0] == "120000" {
		return name, driftCopy{}, false, nil
	}
	size, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil {
		return "", driftCopy{}, false, fmt.Errorf("ls-tree size of %s: %w", name, err)
	}
	return name, driftCopy{oid: fields[2], size: size}, true, nil
}

// compareSharedPaths compares every path two or more checkouts carry, in path order and up to
// MaxDriftSharedPaths, and files each under Drifted or Identical.
func compareSharedPaths(ctx context.Context, checkouts []driftCheckout, copies map[string][]driftCopy, report *DriftReport) {
	shared := make([]string, 0)
	for name, held := range copies {
		if len(held) > 1 {
			shared = append(shared, name)
		}
	}
	sort.Strings(shared)
	if len(shared) > MaxDriftSharedPaths {
		report.Truncated = true
		report.fail(fmt.Sprintf("%d paths are carried by two or more repositories; only the first %d are compared",
			len(shared), MaxDriftSharedPaths))
		shared = shared[:MaxDriftSharedPaths]
	}
	for i := 0; i < len(shared) && ctx.Err() == nil; i++ {
		file, ok := compareCopies(ctx, shared[i], checkouts, copies[shared[i]], report)
		switch {
		case !ok:
		case len(file.Variants) == 1:
			report.Identical = append(report.Identical, file)
		default:
			report.Drifted = append(report.Drifted, file)
		}
	}
}

// compareCopies reads every copy of name, groups them by content digest and measures each
// variant against the most common one. It reports ok=false when fewer than two copies could be
// read, because a single copy compares with nothing.
func compareCopies(ctx context.Context, name string, checkouts []driftCheckout, held []driftCopy, report *DriftReport) (SharedFile, bool) {
	variants := make([]driftVariant, 0, 2)
	readCopies := 0
	for i := 0; i < len(held); i++ {
		checkout := checkouts[held[i].checkout]
		content, err := readDriftCopy(ctx, checkout.path, held[i])
		if err != nil {
			report.fail(fmt.Sprintf("%s: %s unread: %v", checkout.name, name, err))
			continue
		}
		readCopies++
		variants = addDriftVariant(variants, checkout.name, content)
	}
	if readCopies < 2 {
		return SharedFile{}, false
	}
	sort.SliceStable(variants, func(a, b int) bool {
		left, right := variants[a].variant.Repositories, variants[b].variant.Repositories
		if len(left) != len(right) {
			return len(left) > len(right)
		}
		return left[0] < right[0]
	})
	file := SharedFile{Path: name, Variants: make([]FileVariant, len(variants))}
	for i := 0; i < len(variants); i++ {
		file.Variants[i] = variants[i].variant
		if i > 0 {
			delta := util.LineDeltaOf(variants[0].content, variants[i].content, 0)
			file.Variants[i].LinesRemoved, file.Variants[i].LinesAdded = delta.Removed, delta.Added
		}
	}
	return file, true
}

// addDriftVariant files repository's copy under the variant with the same digest, or starts a
// new variant for a content not seen yet.
func addDriftVariant(variants []driftVariant, repository string, content []byte) []driftVariant {
	sum := sha256.Sum256(content)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	for i := 0; i < len(variants); i++ {
		if variants[i].variant.Digest == digest {
			variants[i].variant.Repositories = append(variants[i].variant.Repositories, repository)
			return variants
		}
	}
	text := string(content)
	return append(variants, driftVariant{
		variant: FileVariant{Digest: digest, Lines: util.CountLines(text), Repositories: []string{repository}},
		content: text,
	})
}

// readDriftCopy reads one committed copy through git cat-file, refusing a copy above
// MaxDriftBlobBytes before reading it.
func readDriftCopy(ctx context.Context, repoPath string, held driftCopy) ([]byte, error) {
	if held.size > MaxDriftBlobBytes {
		return nil, fmt.Errorf("%d bytes exceed the %d-byte bound", held.size, MaxDriftBlobBytes)
	}
	result, err := util.RunGitProbe(ctx, repoPath, MaxDriftBlobBytes, "cat-file", "blob", held.oid)
	if err != nil {
		return nil, err
	}
	return result.Stdout, nil
}
