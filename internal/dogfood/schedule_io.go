package dogfood

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

func readSchedulePrivate(ctx context.Context, path string) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("schedule metadata paths must be clean absolute paths")
	}
	root, err := openSuiteDirectory(ctx, filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	data, readErr := readScheduleFile(ctx, root, filepath.Base(path), maxSuiteConfigBytes, true)
	return data, errors.Join(readErr, root.Close())
}

func readScheduleFile(ctx context.Context, root *os.Root, name string, limit int64, private bool) (data []byte, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if err := validateScheduleFileInfo(before, limit, private); err != nil {
		return nil, err
	}
	file, err := openSuiteConfigFile(root, name)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if err := checkScheduleFileOpened(before, opened); err != nil {
		return nil, err
	}
	data, err = readBoundedScheduleFile(file, limit)
	if err != nil {
		return nil, err
	}
	if err := checkPublicFileStable(root, name, file, before, len(data)); err != nil {
		return nil, err
	}
	return data, ctx.Err()
}

// checkScheduleFileOpened verifies that the opened file matches the before-lstat file info.
func checkScheduleFileOpened(before, opened os.FileInfo) error {
	if !os.SameFile(before, opened) || !opened.Mode().IsRegular() {
		return errors.New("schedule input changed during open")
	}
	return nil
}

// readBoundedScheduleFile reads bounded data from file and checks limit.
func readBoundedScheduleFile(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("schedule input exceeded byte bound")
	}
	return data, nil
}

func openScheduleState(ctx context.Context, path string, create bool) (*os.Root, error) {
	root, err := openSuiteDirectory(ctx, path)
	if errors.Is(err, os.ErrNotExist) && create {
		if _, err = createSuiteRun(ctx, path); err != nil {
			return nil, err
		}
		root, err = openSuiteDirectory(ctx, path)
	}
	if err != nil {
		return nil, err
	}
	info, err := root.Stat(".")
	if err == nil {
		if private, unverifiable := util.ArtefactPrivacy(info); !private {
			err = errors.New("schedule state directory must be private (0700 or stricter)")
		} else {
			util.NotePrivacyLimitation(unverifiable)
		}
	}
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	return root, nil
}

// Atomic replace plus fsync of both file and directory keeps attempts durable.
// A failed staging file is retained and blocks reuse instead of being deleted.
func saveScheduleJSON(ctx context.Context, root *os.Root, name string, value any) (err error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > maxSuiteConfigBytes {
		return errors.New("schedule metadata exceeded 64 KiB")
	}
	if err := checkScheduleWriteTarget(root, name); err != nil {
		return err
	}
	file, err := root.OpenFile(name+".pending", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	err = errors.Join(writeErr, file.Sync(), file.Close())
	if err != nil {
		return err
	}
	if err := root.Rename(name+".pending", name); err != nil {
		return err
	}
	// Persisting a rename needs the directory flushed, and that flush is POSIX-only: Windows
	// refuses FlushFileBuffers on a directory handle with access denied, so doing it inline
	// returned an error after the write had already landed (#125). contextopt.SyncDirectory
	// already carries that platform split; a second copy here would be a second thing to get
	// wrong (HISS-19).
	return contextopt.SyncDirectory(ctx, root)
}

func readScheduleState(ctx context.Context, root *os.Root) (*scheduleState, error) {
	data, err := readScheduleFile(ctx, root, "state.json", maxSuiteConfigBytes, true)
	if errors.Is(err, os.ErrNotExist) {
		return &scheduleState{Version: 1}, nil
	}
	if err != nil {
		return nil, err
	}
	fields, err := scheduleObject(data, []string{"version", "attempts", "consecutive_failures", "pruned_through", "last_attempt"})
	if err != nil {
		return nil, err
	}
	if fields["version"] == nil || fields["attempts"] == nil || fields["consecutive_failures"] == nil {
		return nil, errors.New("incomplete schedule state")
	}
	if fields["last_attempt"] != nil {
		_, err = scheduleObject(fields["last_attempt"], []string{"number", "fingerprint", "artifact_dir", "status", "started_at", "finished_at", "error"})
		if err != nil {
			return nil, err
		}
	}
	var state scheduleState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, errors.New("invalid schedule state types")
	}
	return &state, validateScheduleState(&state, root.Name())
}

func validateScheduleState(state *scheduleState, dir string) error {
	if err := validateScheduleCounters(state); err != nil {
		return err
	}
	if state.Attempts == 0 {
		if state.LastAttempt != nil || state.ConsecutiveFailures != 0 {
			return errors.New("empty schedule has an attempt")
		}
		return nil
	}
	return validateScheduleAttempt(state, dir)
}

// validateScheduleCounters verifies version and numeric attempt boundaries.
func validateScheduleCounters(state *scheduleState) error {
	if state.Version != 1 || state.Attempts < 0 || state.Attempts > 1024 || state.ConsecutiveFailures < 0 || state.ConsecutiveFailures > state.Attempts || state.PrunedThrough < 0 || state.PrunedThrough > state.Attempts {
		return errors.New("invalid schedule state counters")
	}
	return nil
}

func validateScheduleAttempt(state *scheduleState, dir string) error {
	a := state.LastAttempt
	if a == nil || a.Number != state.Attempts || !suiteSHA.MatchString(a.Fingerprint) || a.StartedAt.IsZero() {
		return errors.New("invalid persisted schedule attempt")
	}
	if filepath.Dir(a.ArtifactDir) != dir || filepath.Base(a.ArtifactDir) != scheduleRunName(a.Number) {
		return errors.New("persisted attempt is outside expected state path")
	}
	return validateScheduleAttemptStatus(state)
}

func validateScheduleAttemptStatus(state *scheduleState) error {
	a := state.LastAttempt
	if a.Status == "running" && a.FinishedAt.IsZero() && state.ConsecutiveFailures > 0 {
		return nil
	}
	if a.FinishedAt.IsZero() || a.FinishedAt.Before(a.StartedAt) {
		return errors.New("invalid schedule attempt time")
	}
	return validateFinishedScheduleAttempt(a.Status, state.ConsecutiveFailures)
}

// validateFinishedScheduleAttempt checks status and failure consistency of a completed attempt.
func validateFinishedScheduleAttempt(status string, failures int) error {
	if status == "verified" && failures == 0 {
		return nil
	}
	if (status == "failed" || status == "interrupted") && failures > 0 {
		return nil
	}
	return errors.New("invalid schedule attempt status")
}

type scheduleUsage struct {
	bytes          int64
	runs, entries  int
	minRun, maxRun int
	dirs           []string
	runEntries     map[string]int
	runBytes       map[string]int64
}

func scanScheduleUsage(ctx context.Context, root *os.Root) (*scheduleUsage, error) {
	hardBound := 4 * maxScheduleEntries
	usage := &scheduleUsage{
		dirs:       []string{"."},
		runEntries: make(map[string]int),
		runBytes:   make(map[string]int64),
	}
	for i := 0; i < len(usage.dirs) && i < hardBound; i++ {
		if err := usage.add(ctx, root, usage.dirs[i]); err != nil {
			return nil, err
		}
	}
	if usage.runs > 0 && usage.maxRun-usage.minRun+1 != usage.runs {
		return nil, errors.New("retained attempt directory sequence has gaps")
	}
	if len(usage.dirs) > hardBound {
		return nil, errors.New("retained directory scan exceeded bound")
	}
	return usage, nil
}

func (u *scheduleUsage) add(ctx context.Context, root *os.Root, dir string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := root.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("retained directory changed type")
	}
	entries, err := readPublicDirectory(root, dir)
	if err != nil {
		return err
	}
	for i := 0; i < len(entries) && i < maxPublicTreeEntries; i++ {
		if err := u.addEntry(root, dir, entries[i].Name()); err != nil {
			return err
		}
	}
	return nil
}

func checkScheduleWriteTarget(root *os.Root, name string) error {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	existingPrivate, existingUnverifiable := util.ArtefactPrivacy(info)
	util.NotePrivacyLimitation(existingUnverifiable)
	// This target carries two requirements, and only one of them is a privacy question.
	// ArtefactPrivacy answers "no other account can read it". That the owner can still write
	// it is the other half, and unlike the privacy bits the write bit is a property the mode
	// does express on every platform: Windows synthesises 0444 for a read-only file and 0666
	// for a writable one. Dropping it would let the tick rewrite state an operator had
	// deliberately made read-only.
	if !info.Mode().IsRegular() || !existingPrivate || info.Mode().Perm()&0o200 == 0 {
		return errors.New("existing schedule state must permit owner read/write only (0600)")
	}
	return nil
}

func validateScheduleFileInfo(info os.FileInfo, limit int64, private bool) error {
	if !info.Mode().IsRegular() || info.Size() > limit {
		return errors.New("schedule input must be a bounded regular file")
	}
	if private {
		isPrivate, unverifiable := util.ArtefactPrivacy(info)
		util.NotePrivacyLimitation(unverifiable)
		if !isPrivate {
			return errors.New("schedule metadata must be private (0600 or stricter)")
		}
	}
	return nil
}

func (u *scheduleUsage) addEntry(root *os.Root, dir, name string) error {
	u.entries++
	if u.entries > 4*maxScheduleEntries {
		return fmt.Errorf("retained evidence exceeds %d entries", 4*maxScheduleEntries)
	}
	rel := filepath.Join(dir, name)
	info, err := root.Lstat(rel)
	if err != nil {
		return err
	}
	if err := u.addSize(info.Size()); err != nil {
		return err
	}
	if dir == "." {
		if err := u.addRun(info, name); err != nil {
			return err
		}
	}
	u.recordTopLevelRun(dir, name, info.Size())
	if info.IsDir() {
		u.dirs = append(u.dirs, rel)
	}
	return validateRetainedFileMode(info)
}

// recordTopLevelRun tallies entries and bytes under top-level runs.
func (u *scheduleUsage) recordTopLevelRun(dir, name string, size int64) {
	if run := topLevelRun(dir, name); run != "" {
		u.runEntries[run]++
		u.runBytes[run] += size
	}
}

// validateRetainedFileMode ensures the entry is a directory, regular file, or symlink.
func validateRetainedFileMode(info os.FileInfo) error {
	if !info.IsDir() && !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
		return errors.New("unexpected special file in retained evidence")
	}
	return nil
}

func (u *scheduleUsage) addSize(size int64) error {
	if size < 0 {
		return errors.New("negative retained entry size")
	}
	if size > 16<<30 || u.bytes > (16<<30)-size {
		u.bytes = (16 << 30) + 1
	} else {
		u.bytes += size
	}
	return nil
}

func (u *scheduleUsage) addRun(info os.FileInfo, name string) error {
	if !strings.HasPrefix(name, "run-") {
		return nil
	}
	number, err := strconv.Atoi(strings.TrimPrefix(name, "run-"))
	if err != nil || number < 1 || number > 1024 || name != scheduleRunName(number) || !info.IsDir() {
		return errors.New("retained attempt must be a real canonically numbered directory")
	}
	u.runs++
	if u.minRun == 0 || number < u.minRun {
		u.minRun = number
	}
	if number > u.maxRun {
		u.maxRun = number
	}
	return nil
}

func topLevelRun(dir, name string) string {
	top := dir
	if dir == "." {
		top = name
	} else if idx := strings.IndexAny(dir, `/\`); idx >= 0 {
		top = dir[:idx]
	}
	if strings.HasPrefix(top, "run-") {
		return top
	}
	return ""
}
