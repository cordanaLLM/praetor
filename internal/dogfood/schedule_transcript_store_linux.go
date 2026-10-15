// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build linux

package dogfood

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/cordanaLLM/praetor/internal/harvester"
	"github.com/cordanaLLM/praetor/internal/util"
)

// The schedule transcript store judges its directory and files by POSIX mode, the protection
// on Linux, where dogfood scheduling runs. schedule_transcript_store_other.go says elsewhere
// that the store is unsupported (#132).

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
