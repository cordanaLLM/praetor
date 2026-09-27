// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package docsref

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/operationalsync"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Bounds on the repository inventory (HISS-02).
const (
	maxInventoryBytes   = 16 << 20
	maxInventoryEntries = 1 << 17
	maxIgnoreQuery      = 4096
)

// repositoryIndex is the tree a path reference is checked against: tracked files plus the
// untracked files the ignore rules do not hide. It comes from git rather than from the working
// directory, so a file that exists only on one machine never satisfies the check there and
// fails it in CI.
type repositoryIndex struct {
	files map[string]bool
	dirs  map[string]bool
	tops  map[string]bool
}

// loadIndex lists the repository at root, which must be the top of its work tree: the
// documents name paths relative to it.
func loadIndex(ctx context.Context, root string) (*repositoryIndex, []string, error) {
	prefix, err := util.RunGitProbe(ctx, root, 4096, "rev-parse", "--show-prefix")
	if err != nil {
		return nil, nil, fmt.Errorf("locate repository root: %w", err)
	}
	if strings.TrimSpace(string(prefix.Stdout)) != "" {
		return nil, nil, fmt.Errorf("%s is not the top of its git work tree", root)
	}
	out, err := util.RunGitProbe(ctx, root, maxInventoryBytes,
		"ls-files", "--cached", "--others", "--exclude-standard", "--deduplicate", "-z", "--", ".")
	if err != nil {
		return nil, nil, fmt.Errorf("list repository files: %w", err)
	}
	entries := bytes.Split(bytes.TrimSuffix(out.Stdout, []byte{0}), []byte{0})
	if len(entries) > maxInventoryEntries {
		return nil, nil, fmt.Errorf("repository lists more than %d files", maxInventoryEntries)
	}
	index := &repositoryIndex{files: map[string]bool{}, dirs: map[string]bool{}, tops: map[string]bool{}}
	inventory := make([]string, 0, len(entries))
	for _, entry := range entries {
		if rel := string(entry); rel != "" {
			index.add(rel)
			inventory = append(inventory, rel)
		}
	}
	slices.Sort(inventory)
	return index, inventory, nil
}

// add records one file and every directory above it.
func (idx *repositoryIndex) add(rel string) {
	idx.files[rel] = true
	segments := strings.Split(rel, "/")
	idx.tops[segments[0]] = true
	for depth := 1; depth < len(segments) && depth <= maxPathSegments; depth++ {
		idx.dirs[strings.Join(segments[:depth], "/")] = true
	}
}

// maxPathSegments bounds the directories recorded for one path (HISS-02).
const maxPathSegments = 64

// pathReference is one repository path a document names.
type pathReference struct {
	// path is the cleaned slash path without a trailing slash.
	path string
	// dir reports that the reference must be a directory: written with a trailing slash, or
	// the literal prefix of a glob or placeholder.
	dir bool
	// symbol is the Go identifier written after a package path (internal/state.Verify), or
	// empty.
	symbol string
	// text is the reference as written.
	text string
}

// lineSuffix matches a ":12", ":12-40" or ":12,40-44" line reference after a path.
var lineSuffix = regexp.MustCompile(`:\d+(?:-\d+)?(?:,\d+(?:-\d+)?)*$`)

// goSymbol matches a package directory's last element followed by an exported identifier.
var goSymbol = regexp.MustCompile(`^([a-z0-9_]+)\.([A-Z][A-Za-z0-9_]*)(?:\.[A-Za-z0-9_]+)*$`)

// pathText strips the punctuation, flag name, anchor and line reference around a path word
// and reports whether what remains is shaped like a relative path.
func pathText(token string) (string, bool) {
	text := strings.TrimRight(strings.TrimLeft(token, `"'(`), `"'),.;:`)
	if match := flagPattern.FindStringSubmatch(text); match != nil && match[2] != "" {
		text = strings.Trim(match[2][1:], `"'`)
	}
	text = strings.TrimPrefix(text, "./")
	text, _, _ = strings.Cut(text, "#")
	text = lineSuffix.ReplaceAllString(text, "")
	if !strings.Contains(text, "/") || strings.Contains(text, "://") || strings.ContainsAny(text, " @=%\\") {
		return "", false
	}
	for _, reject := range pathRejects {
		if strings.HasPrefix(text, reject) {
			return "", false
		}
	}
	return text, true
}

// pathRejects are prefixes that make a word something other than a repository path: an
// absolute or home path, a variable, a flag, a relative path out of the tree.
var pathRejects = []string{"/", "~", "$", "-", "@", "#", "..", `\`}

// pathReferences returns the repository paths named in one candidate. A word counts only when
// it has a slash and its first element is a top-level entry of the repository, so a path in
// another repository, a URL or a module path is not mistaken for one of ours.
func pathReferences(candidate Candidate, tops map[string]bool) []pathReference {
	var refs []pathReference
	for _, token := range tokenize(candidate.Text) {
		if ref, ok := pathReferenceOf(token, tops); ok {
			refs = append(refs, ref)
		}
	}
	return refs
}

// pathReferenceOf reads one word as a repository path.
func pathReferenceOf(token string, tops map[string]bool) (pathReference, bool) {
	text, ok := pathText(token)
	if !ok {
		return pathReference{}, false
	}
	segments := strings.Split(strings.TrimSuffix(text, "/"), "/")
	if !tops[segments[0]] || len(segments) > maxPathSegments {
		return pathReference{}, false
	}
	ref := pathReference{text: text, dir: strings.HasSuffix(text, "/")}
	for index, segment := range segments {
		if strings.ContainsAny(segment, "*?[]{}<>") || isPlaceholder(segment) {
			segments, ref.dir = segments[:index], true
			break
		}
	}
	if last := len(segments) - 1; last > 0 {
		if match := goSymbol.FindStringSubmatch(segments[last]); match != nil {
			segments[last], ref.symbol, ref.dir = match[1], match[2], true
		}
	}
	ref.path = path.Join(segments...)
	return ref, ref.path != "" && ref.path != "."
}

// pathProblem describes why ref does not resolve, or returns "" when it does. An absent path
// is returned with absent=true so the caller can ask whether it is operator data or ignored.
func (idx *repositoryIndex) pathProblem(ctx context.Context, tree *sourceTree, ref pathReference) (problem string, absent bool) {
	exists := idx.dirs[ref.path] || (!ref.dir && idx.files[ref.path])
	if !exists {
		return fmt.Sprintf("path %q is not in the repository", ref.text), true
	}
	if ref.symbol == "" {
		return "", false
	}
	decls, err := tree.load(ctx, ref.path)
	if err != nil {
		return fmt.Sprintf("cannot read package %s for %q: %v", ref.path, ref.text, err), false
	}
	if decls[ref.symbol] == nil && decls[methodKey+ref.symbol] == nil {
		return fmt.Sprintf("package %s declares no %s (in %q)", ref.path, ref.symbol, ref.text), false
	}
	return "", false
}

// localOnly returns the subset of absent paths that legitimately exist outside the public
// tree. Two kinds do:
//
//   - operator-owned paths (internal/operationalsync owner-only prefixes such as deploy/arc/
//     and .config/operator/): the operational fork supplies them and the engine never tracks
//     them, so a guide describing them names real operator data;
//   - paths the repository's own ignore rules hide: build output (bin/), private state
//     (.workingdir/), agent worktrees. A document that names one describes a local artefact
//     the reader's checkout creates, which git by design never lists.
//
// The ignore rules are the repository's own (.gitignore and .git/info/exclude through
// util.GitIgnoredPaths), never the operator's global excludes, so the answer is the same on
// every machine.
func localOnly(ctx context.Context, root string, absent []string) (map[string]bool, error) {
	allowed := map[string]bool{}
	var queries []string
	for _, rel := range absent {
		if operationalsync.IsOwnerOnlyReference(rel) {
			allowed[rel] = true
			continue
		}
		queries = append(queries, strings.TrimSuffix(rel, "/"), strings.TrimSuffix(rel, "/")+"/")
	}
	for start := 0; start < len(queries); start += maxIgnoreQuery {
		batch := queries[start:min(start+maxIgnoreQuery, len(queries))]
		ignored, err := util.GitIgnoredPaths(ctx, root, batch, true)
		if err != nil {
			return nil, err
		}
		for _, rel := range ignored {
			allowed[strings.TrimSuffix(rel, "/")] = true
		}
	}
	return allowed, nil
}
