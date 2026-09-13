package dogfood

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	if !os.SameFile(before, opened) || !opened.Mode().IsRegular() {
		return nil, errors.New("schedule input changed during open")
	}
	data, err = io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("schedule input exceeded byte bound")
	}
	if err := checkPublicFileStable(root, name, file, before, len(data)); err != nil {
		return nil, err
	}
	return data, ctx.Err()
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
	if err == nil && info.Mode().Perm()&0o077 != 0 {
		err = errors.New("schedule state directory must be private (0700 or stricter)")
	}
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	return root, nil
}

// Atomic replace plus fsync of both file and directory keeps attempts durable.
// A failed staging file is retained and blocks reuse instead of being deleted.
func saveScheduleJSON(root *os.Root, name string, value any) (err error) {
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
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func readScheduleState(ctx context.Context, root *os.Root) (*scheduleState, error) {
	data, err := readScheduleFile(ctx, root, "state.json", maxSuiteConfigBytes, true)
	if errors.Is(err, os.ErrNotExist) {
		return &scheduleState{Version: 1}, nil
	}
	if err != nil {
		return nil, err
	}
	fields, err := scheduleObject(data, []string{"version", "attempts", "consecutive_failures", "last_attempt"})
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
	if state.Version != 1 || state.Attempts < 0 || state.Attempts > 1024 || state.ConsecutiveFailures < 0 || state.ConsecutiveFailures > state.Attempts {
		return errors.New("invalid schedule state counters")
	}
	if state.Attempts == 0 {
		if state.LastAttempt != nil || state.ConsecutiveFailures != 0 {
			return errors.New("empty schedule has an attempt")
		}
		return nil
	}
	return validateScheduleAttempt(state, dir)
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
	if a.Status == "verified" && state.ConsecutiveFailures == 0 {
		return nil
	}
	if (a.Status == "failed" || a.Status == "interrupted") && state.ConsecutiveFailures > 0 {
		return nil
	}
	return errors.New("invalid schedule attempt status")
}

type scheduleUsage struct {
	bytes         int64
	runs, entries int
	maxRun        int
	dirs          []string
}

func scanScheduleUsage(ctx context.Context, root *os.Root) (*scheduleUsage, error) {
	usage := &scheduleUsage{dirs: []string{"."}}
	for i := 0; i < len(usage.dirs) && i < maxScheduleEntries; i++ {
		if err := usage.add(ctx, root, usage.dirs[i]); err != nil {
			return nil, err
		}
	}
	if usage.maxRun != usage.runs {
		return nil, errors.New("retained attempt directory sequence has gaps")
	}
	if len(usage.dirs) > maxScheduleEntries {
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
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return errors.New("existing schedule state must permit owner read/write only (0600)")
	}
	return nil
}

func validateScheduleFileInfo(info os.FileInfo, limit int64, private bool) error {
	if !info.Mode().IsRegular() || info.Size() > limit {
		return errors.New("schedule input must be a bounded regular file")
	}
	if private && info.Mode().Perm()&0o077 != 0 {
		return errors.New("schedule metadata must be private (0600 or stricter)")
	}
	return nil
}

func (u *scheduleUsage) addEntry(root *os.Root, dir, name string) error {
	u.entries++
	if u.entries > maxScheduleEntries {
		return errors.New("retained evidence exceeds 200000 entries")
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
	if info.IsDir() {
		u.dirs = append(u.dirs, rel)
	}
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
	if number > u.maxRun {
		u.maxRun = number
	}
	return nil
}
