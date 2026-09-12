// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package harvester

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func selectTranscript(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("transcript source path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	base := filepath.Base(absolute)
	if base != "transcript.jsonl" && base != "transcript_full.jsonl" {
		return "", fmt.Errorf("unsupported transcript format: expected Antigravity transcript[_full].jsonl")
	}
	if err := rejectBundleLinks(absolute); err != nil {
		return "", err
	}
	full := filepath.Join(filepath.Dir(absolute), "transcript_full.jsonl")
	if _, err := os.Lstat(full); err == nil {
		absolute = full
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := rejectBundleLinks(absolute); err != nil {
		return "", err
	}
	return absolute, nil
}

func openTranscript(path string) (file *os.File, info os.FileInfo, err error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	info, err = root.Lstat(filepath.Base(path))
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("transcript source must be a regular file")
	}
	file, err = root.Open(filepath.Base(path))
	if err != nil {
		return nil, nil, err
	}
	opened, err := file.Stat()
	if err == nil && !os.SameFile(info, opened) {
		err = fmt.Errorf("transcript source changed while opening")
	}
	if err != nil {
		return nil, nil, errors.Join(err, file.Close())
	}
	return file, info, nil
}

type transcriptContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r transcriptContextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

func snapshotTranscript(ctx context.Context, path string) (data []byte, source TranscriptSource, err error) {
	selected, err := selectTranscript(path)
	if err != nil {
		return nil, source, err
	}
	file, before, err := openTranscript(selected)
	if err != nil {
		return nil, source, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	if before.Size() > MaxTranscriptSourceBytes {
		return nil, source, fmt.Errorf("transcript exceeds %d byte source bound", MaxTranscriptSourceBytes)
	}
	reader := transcriptContextReader{ctx: ctx, reader: file}
	data, err = io.ReadAll(io.LimitReader(reader, MaxTranscriptSourceBytes+1))
	if err != nil {
		return nil, source, fmt.Errorf("read transcript: %w", err)
	}
	after, err := file.Stat()
	if err != nil {
		return nil, source, err
	}
	if len(data) > MaxTranscriptSourceBytes || int64(len(data)) != before.Size() || !before.ModTime().Equal(after.ModTime()) || before.Size() != after.Size() {
		return nil, source, fmt.Errorf("transcript changed during snapshot or exceeded source bound")
	}
	sum := sha256.Sum256(data)
	source = TranscriptSource{Path: selected, SHA256: hex.EncodeToString(sum[:]), Bytes: len(data), Format: "antigravity-jsonl-v1", Conversation: filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(selected))))}
	return data, source, nil
}
