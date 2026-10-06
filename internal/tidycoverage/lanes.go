// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package tidycoverage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Bounds of one lane's input (HISS-02).
const (
	// maxCompileDatabaseBytes bounds one compile database read.
	maxCompileDatabaseBytes = 256 << 20
	// maxCompileDatabaseEntries bounds the entries of one compile database.
	maxCompileDatabaseEntries = 1 << 20
	// maxFileListBytes bounds one lane file list read.
	maxFileListBytes = 16 << 20
	// maxFileListLines bounds the lines of one lane file list.
	maxFileListLines = 1 << 20
)

// compileCommand is the part of a JSON compilation database entry the gate reads
// (https://clang.llvm.org/docs/JSONCompilationDatabase.html): file is the translation unit,
// absolute or relative to directory, the working directory of the compilation.
type compileCommand struct {
	Directory string `json:"directory"`
	File      string `json:"file"`
}

// laneReading is what readLanes found: what each lane read, the union of the repository paths
// the lanes read, and the lanes skipped because their compile database is not written yet.
type laneReading struct {
	lanes   []Lane
	read    map[string]bool
	unbuilt []string
}

// readLanes reads every declared lane. A lane whose input is missing, unreadable, malformed or
// empty is an error: the gate fails closed rather than counting the lane as reading nothing.
// The one exception is opts.SkipUnbuilt, under which a compile database that does not exist
// is recorded in unbuilt, as "name (path)", and the remaining lanes are still read.
func readLanes(ctx context.Context, opts Options, units []string) (laneReading, error) {
	reading := laneReading{read: make(map[string]bool)}
	if opts.Policy == nil {
		return reading, nil
	}
	bases := rootForms(opts.Root)
	for index := 0; index < len(opts.Policy.Lanes) && index < config.MaxClangTidyLanes; index++ {
		if err := ctx.Err(); err != nil {
			return laneReading{}, err
		}
		declared := opts.Policy.Lanes[index]
		paths, err := readLane(opts.Root, bases, declared)
		if err != nil && opts.SkipUnbuilt && isUnbuilt(declared, err) {
			reading.unbuilt = append(reading.unbuilt, fmt.Sprintf("%s (%s)", declared.Name, declared.CompileDatabase))
			continue
		}
		if err != nil {
			return laneReading{}, fmt.Errorf("clang-tidy lane %s: %w", declared.Name, err)
		}
		reading.lanes = append(reading.lanes, countLane(declared, paths, units, reading.read))
	}
	return reading, nil
}

// countLane returns what declared read of units, given the repository paths it reads, and
// marks each such unit in read.
func countLane(declared config.ClangTidyLane, paths map[string]bool, units []string, read map[string]bool) Lane {
	lane := Lane{Name: declared.Name, Source: declared.Source()}
	for unit := 0; unit < len(units); unit++ {
		if paths[units[unit]] {
			lane.Units++
			read[units[unit]] = true
		}
	}
	return lane
}

// isUnbuilt reports whether err, from reading lane, says the lane's compile database does not
// exist. A files list is tracked, not built, so its absence is never unbuilt.
func isUnbuilt(lane config.ClangTidyLane, err error) bool {
	return lane.CompileDatabase != "" && errors.Is(err, fs.ErrNotExist)
}

// readLane returns the repository paths one lane reads.
func readLane(root string, bases []string, lane config.ClangTidyLane) (map[string]bool, error) {
	if lane.CompileDatabase != "" {
		return readCompileDatabase(root, bases, lane.CompileDatabase)
	}
	return readFileList(root, lane.Files)
}

// readCompileDatabase returns the repository paths of the translation units a compile database
// lists. Entries for files outside the repository, such as dependencies built in the same
// tree, are left out.
func readCompileDatabase(root string, bases []string, rel string) (map[string]bool, error) {
	data, err := util.ReadConfinedLimited(root, filepath.FromSlash(rel), maxCompileDatabaseBytes)
	if err != nil {
		return nil, fmt.Errorf("compile database %s cannot be read: %w", rel, err)
	}
	var entries []compileCommand
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("compile database %s is not a JSON compilation database: %w", rel, err)
	}
	if len(entries) == 0 || len(entries) > maxCompileDatabaseEntries {
		return nil, fmt.Errorf("compile database %s lists %d entries; a lane needs 1 to %d", rel, len(entries), maxCompileDatabaseEntries)
	}
	paths := make(map[string]bool, len(entries))
	for index := 0; index < len(entries); index++ {
		if strings.TrimSpace(entries[index].File) == "" {
			return nil, fmt.Errorf("compile database %s entry %d names no file", rel, index)
		}
		if unit, inside := repositoryPath(bases, entries[index]); inside {
			paths[unit] = true
		}
	}
	return paths, nil
}

// readFileList returns the repository paths a lane file list names: one repository-relative
// path per line, blank lines and lines starting with # left out.
func readFileList(root, rel string) (map[string]bool, error) {
	data, err := util.ReadConfinedLimited(root, filepath.FromSlash(rel), maxFileListBytes)
	if err != nil {
		return nil, fmt.Errorf("file list %s cannot be read: %w", rel, err)
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > maxFileListLines {
		return nil, fmt.Errorf("file list %s has more than %d lines", rel, maxFileListLines)
	}
	paths := make(map[string]bool, len(lines))
	for index := 0; index < len(lines); index++ {
		line := strings.TrimSpace(lines[index])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		entry := path.Clean(util.NormalizeSlashes(line))
		if !filepath.IsLocal(filepath.FromSlash(entry)) {
			return nil, fmt.Errorf("file list %s line %d: %q is not a repository-relative path", rel, index+1, line)
		}
		paths[entry] = true
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("file list %s names no file", rel)
	}
	return paths, nil
}

// rootForms returns the absolute forms of root a compile database may record: as given, and
// with symbolic links resolved, since a build records whichever its working directory had
// (macOS's /var and /private/var, for one).
func rootForms(root string) []string {
	forms := make([]string, 0, 2)
	if absolute, err := filepath.Abs(root); err == nil {
		forms = append(forms, absolute)
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		if absolute, err := filepath.Abs(resolved); err == nil && (len(forms) == 0 || absolute != forms[0]) {
			forms = append(forms, absolute)
		}
	}
	return forms
}

// repositoryPath returns the slash-separated repository path of an entry's file, and whether
// the file lies inside the repository under one of bases. A relative file is resolved against
// the entry's directory, and a relative directory against the first base. When the path as
// recorded lies outside every base, the file's resolved path is tried as well.
func repositoryPath(bases []string, entry compileCommand) (string, bool) {
	file := entry.File
	if !filepath.IsAbs(file) {
		file = filepath.Join(entry.Directory, file)
	}
	if !filepath.IsAbs(file) && len(bases) > 0 {
		file = filepath.Join(bases[0], file)
	}
	file = filepath.Clean(file)
	if rel, inside := relativeToAny(bases, file); inside {
		return rel, true
	}
	resolved, err := filepath.EvalSymlinks(file)
	if err != nil || resolved == file {
		return "", false
	}
	return relativeToAny(bases, resolved)
}

// relativeToAny returns file relative to the first of bases that contains it, slash-separated.
func relativeToAny(bases []string, file string) (string, bool) {
	for index := 0; index < len(bases); index++ {
		if rel, err := filepath.Rel(bases[index], file); err == nil && rel != "." && filepath.IsLocal(rel) {
			return util.NormalizeSlashes(rel), true
		}
	}
	return "", false
}
