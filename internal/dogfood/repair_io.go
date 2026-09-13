package dogfood

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

// LoadRepairReport reads exactly one bounded, stable, symlink-free report file.
// Referenced sources, logs and checkout paths are never opened.
func LoadRepairReport(ctx context.Context, path string) (_ *SuiteReport, err error) {
	if ctx == nil {
		return nil, errors.New("repair report requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := validateSuitePath(path); err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	root, err := openSuiteDirectory(ctx, filepath.Dir(absolute))
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	data, err := readRepairFile(ctx, root, filepath.Base(absolute))
	if err != nil {
		return nil, err
	}
	report, err := decodeRepairReport(data)
	if err != nil {
		return nil, err
	}
	if _, err := validateRepairReport(ctx, report); err != nil {
		return nil, err
	}
	return report, nil
}

func readRepairFile(ctx context.Context, root *os.Root, name string) (data []byte, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > maxRepairReportBytes {
		return nil, errors.New("repair report must be a regular file at most 8 MiB")
	}
	file, err := openSuiteConfigFile(root, name)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	if err := validateRepairOpened(file, before); err != nil {
		return nil, err
	}
	data, err = io.ReadAll(io.LimitReader(file, maxRepairReportBytes+1))
	if err != nil {
		return nil, err
	}
	after, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if err := validateRepairSnapshot(before, after, data); err != nil {
		return nil, err
	}
	return data, ctx.Err()
}

func validateRepairOpened(file *os.File, before os.FileInfo) error {
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(before, opened) || !opened.Mode().IsRegular() {
		return errors.New("repair report changed during open")
	}
	return nil
}

func validateRepairSnapshot(before, after os.FileInfo, data []byte) error {
	if !os.SameFile(before, after) || before.Size() != int64(len(data)) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || len(data) > maxRepairReportBytes {
		return errors.New("repair report changed during bounded snapshot")
	}
	return nil
}

// SaveRepairPlan creates a private review artifact in a new directory only.
// The parent must already exist and every path component must be symlink-free.
func SaveRepairPlan(ctx context.Context, directory string, plan *RepairPlan) (err error) {
	if ctx == nil || plan == nil {
		return errors.New("saving a repair plan requires context and plan")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	data, err := encodeRepairPlan(plan)
	if err != nil {
		return err
	}
	return writeRepairPlan(ctx, directory, data)
}

func encodeRepairPlan(plan *RepairPlan) ([]byte, error) {
	if plan.Version != 1 || len(plan.Jobs) > MaxSuiteCases || !suiteSHA.MatchString(plan.ReportSHA256) {
		return nil, errors.New("invalid repair plan envelope")
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return nil, err
	}
	if len(data) > maxRepairReportBytes {
		return nil, errors.New("repair plan exceeds 8 MiB")
	}
	return data, nil
}

func writeRepairPlan(ctx context.Context, directory string, data []byte) (err error) {
	if err := validateSuitePath(directory); err != nil {
		return err
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	parent, err := openSuiteDirectory(ctx, filepath.Dir(absolute))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	name := filepath.Base(absolute)
	if err := parent.Mkdir(name, 0o700); err != nil {
		return err
	}
	root, err := openSuiteChild(ctx, parent, name)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	return writeRepairPlanFile(ctx, root, data)
}

func writeRepairPlanFile(ctx context.Context, root *os.Root, data []byte) (err error) {
	file, err := root.OpenFile("plan.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	return ctx.Err()
}
