package dogfood

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/harvester"
	"github.com/cordanaLLM/praetor/internal/util"
)

const scheduleTranscriptDir = "transcripts"

func (s *scheduleSnapshot) snapshotSuiteTranscripts(ctx context.Context, stateRoot *os.Root) (data []byte, err error) {
	if len(s.suite.Transcripts) == 0 {
		return s.files["suite.json"], nil
	}
	store, err := openScheduleTranscriptStore(stateRoot)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, store.Close()) }()
	suite := *s.suite
	suite.Transcripts = append([]SuiteTranscript(nil), s.suite.Transcripts...)
	for i := 0; i < len(suite.Transcripts) && i < MaxSuiteCases; i++ {
		path, resolveErr := resolveScheduleTranscript(ctx, store, suite.Transcripts[i])
		if resolveErr != nil {
			return nil, resolveErr
		}
		suite.Transcripts[i].SourcePath = path
	}
	data, err = json.Marshal(&suite)
	if err != nil {
		return nil, fmt.Errorf("encode stored transcript suite: %w", err)
	}
	return data, nil
}

func openScheduleTranscriptStore(stateRoot *os.Root) (*os.Root, error) {
	return openPrivateScheduleDirectory(stateRoot, scheduleTranscriptDir, "schedule transcript store")
}

func openPrivateScheduleDirectory(parent *os.Root, name, label string) (*os.Root, error) {
	err := parent.Mkdir(name, 0o700)
	if err != nil && !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("create %s: %w", label, err)
	}
	info, err := parent.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", label, err)
	}
	private, unverifiable := util.ArtefactPrivacy(info)
	util.NotePrivacyLimitation(unverifiable)
	if !info.IsDir() || !private || info.Mode().Perm() != 0o700 {
		return nil, fmt.Errorf("%s must be a private owner-accessible directory (0700)", label)
	}
	dir, err := parent.OpenRoot(name)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", label, err)
	}
	return dir, nil
}

func resolveScheduleTranscript(ctx context.Context, store *os.Root, source SuiteTranscript) (path string, err error) {
	name := filepath.Base(source.SourcePath)
	if err := validateScheduleTranscriptBaseName(source, name); err != nil {
		return "", err
	}
	dirName := source.ID + "-" + source.SHA256
	storePath := filepath.Join(store.Name(), dirName, name)
	dir, err := openPrivateScheduleDirectory(
		store,
		dirName,
		fmt.Sprintf("stored transcript %q directory", source.ID),
	)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	_, exists, err := readStoredScheduleTranscript(ctx, dir, name, source, storePath)
	if err != nil {
		return "", err
	}
	if exists {
		return storePath, nil
	}
	data, err := util.ReadFileLimited(source.SourcePath, harvester.MaxTranscriptSourceBytes)
	if err != nil {
		return "", transcriptSourceError(source, storePath, err)
	}
	if transcriptSHA256(data) != source.SHA256 {
		return "", transcriptSourceError(source, storePath, errors.New("source SHA256 does not match declaration"))
	}
	if err := writeScheduleTranscript(ctx, dir, name, data); err != nil {
		return "", fmt.Errorf("store transcript %q at %q: %w", source.ID, storePath, err)
	}
	return storePath, nil
}

func validateScheduleTranscriptBaseName(source SuiteTranscript, name string) error {
	invalid := name == "" || name == "." || name == ".."
	if invalid || len(name) > 255 || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("transcript %q source base name %q is not one safe file name of at most 255 bytes", source.ID, name)
	}
	return nil
}

func readStoredScheduleTranscript(ctx context.Context, dir *os.Root, name string, source SuiteTranscript, path string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	info, err := dir.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspect stored transcript %q at %q: %w", source.ID, path, err)
	}
	private, unverifiable := util.ArtefactPrivacy(info)
	util.NotePrivacyLimitation(unverifiable)
	if !info.Mode().IsRegular() || !private || info.Mode().Perm() != 0o600 {
		return nil, false, fmt.Errorf("stored transcript %q at %q must be a private regular file (0600)", source.ID, path)
	}
	data, err := util.ReadLimitedIn(dir, name, harvester.MaxTranscriptSourceBytes)
	if err != nil {
		return nil, false, fmt.Errorf("read stored transcript %q at %q: %w", source.ID, path, err)
	}
	if transcriptSHA256(data) != source.SHA256 {
		return nil, false, fmt.Errorf("stored transcript %q at %q does not match declared SHA256", source.ID, path)
	}
	return data, true, nil
}

func transcriptSourceError(source SuiteTranscript, storePath string, cause error) error {
	return fmt.Errorf("transcript %q has no declared bytes at store %q or source %q: %w", source.ID, storePath, source.SourcePath, cause)
}

func transcriptSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeScheduleTranscript(ctx context.Context, dir *os.Root, name string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	pending := ".pending"
	file, err := dir.OpenFile(pending, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create transcript temp file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		return errors.Join(fmt.Errorf("set transcript temp mode: %w", err), file.Close())
	}
	_, writeErr := file.Write(data)
	if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		return fmt.Errorf("persist transcript temp file: %w", err)
	}
	if err := dir.Rename(pending, name); err != nil {
		return fmt.Errorf("publish transcript store file: %w", err)
	}
	return contextopt.SyncDirectory(ctx, dir)
}
