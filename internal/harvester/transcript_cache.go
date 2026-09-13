// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package harvester

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

func storeTranscriptEvent(root *os.Root, event TranscriptEvent, report *TranscriptIngestReport) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	name := event.ID + ".json"
	present, err := matchTranscriptCache(root, name, data)
	if err != nil {
		return err
	}
	if present {
		report.AlreadyPresent++
		return nil
	}
	stored, err := publishTranscriptCache(root, name, data)
	if stored {
		report.Stored++
	} else if err == nil {
		report.AlreadyPresent++
	}
	return err
}

func matchTranscriptCache(root *os.Root, name string, expected []byte) (bool, error) {
	before, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !before.Mode().IsRegular() || before.Mode().Perm()&0o077 != 0 || before.Size() != int64(len(expected)) {
		return false, fmt.Errorf("existing cache record has unsafe type, mode, or conflicting size")
	}
	file, err := openStableTranscriptFile(root, name, before)
	if err != nil {
		return false, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, int64(len(expected))+1))
	if err := errors.Join(readErr, file.Close()); err != nil {
		return false, err
	}
	if !bytes.Equal(data, expected) {
		return false, fmt.Errorf("existing cache record conflicts with deterministic event")
	}
	return true, nil
}

func publishTranscriptCache(root *os.Root, name string, data []byte) (stored bool, err error) {
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return false, err
	}
	temporary := ".ingest-" + hex.EncodeToString(nonce[:]) + ".tmp"
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, root.Remove(temporary)) }()
	_, writeErr := file.Write(data)
	if err = errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		return false, err
	}
	if err = root.Link(temporary, name); errors.Is(err, os.ErrExist) {
		present, matchErr := matchTranscriptCache(root, name, data)
		if matchErr == nil && !present {
			matchErr = fmt.Errorf("cache record vanished during concurrent publication")
		}
		return false, matchErr
	}
	return err == nil, err
}

func hasTranscriptPayload(event TranscriptEvent) bool {
	return event.Content != "" || len(event.ToolCalls) > 0 || len(event.ToolResults) > 0 || event.Error != "" || event.ExitCode != nil
}

func verifyTranscriptPrefix(ctx context.Context, root *os.Root, events []TranscriptEvent, end int) error {
	for index := 0; index < end; index++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		event := events[index]
		if !hasTranscriptPayload(event) {
			continue
		}
		data, err := json.Marshal(event)
		if err != nil {
			return err
		}
		present, err := matchTranscriptCache(root, event.ID+".json", data)
		if err != nil {
			return fmt.Errorf("verify cursor prefix line %d: %w", event.Line, err)
		}
		if !present {
			return fmt.Errorf("cursor skips uncached transcript line %d", event.Line)
		}
	}
	return nil
}
