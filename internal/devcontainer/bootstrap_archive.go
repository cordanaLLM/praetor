package devcontainer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

func encodeBootstrapArchive(files []bootstrapSourceFile) ([]byte, error) {
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	writer := tar.NewWriter(gzipWriter)
	for _, file := range files {
		header := &tar.Header{Name: file.Name, Size: int64(len(file.Data)), Mode: 0644, ModTime: time.Unix(0, 0), Format: tar.FormatUSTAR}
		if err := writer.WriteHeader(header); err != nil {
			return nil, errors.Join(err, writer.Close(), gzipWriter.Close())
		}
		if _, err := writer.Write(file.Data); err != nil {
			return nil, errors.Join(err, writer.Close(), gzipWriter.Close())
		}
	}
	if err := errors.Join(writer.Close(), gzipWriter.Close()); err != nil {
		return nil, err
	}
	if base64.StdEncoding.EncodedLen(compressed.Len()) > maxBootstrapParts*bootstrapPartBytes {
		return nil, errors.New("compressed bootstrap source exceeds four bounded archive frames")
	}
	return compressed.Bytes(), nil
}

func frameBootstrapArchive(data []byte) ([]BootstrapArtifact, error) {
	encoded := base64.StdEncoding.EncodeToString(data)
	if len(encoded) == 0 || len(encoded) > maxBootstrapParts*bootstrapPartBytes {
		return nil, errors.New("bootstrap archive framing exceeds bounds")
	}
	var files []BootstrapArtifact
	for i := 0; i < maxBootstrapParts && i*bootstrapPartBytes < len(encoded); i++ {
		start := i * bootstrapPartBytes
		end := min(start+bootstrapPartBytes, len(encoded))
		files = append(files, BootstrapArtifact{Name: bootstrapPartName(i), Content: []byte(encoded[start:end])})
	}
	return files, nil
}

func bootstrapPartName(index int) string { return fmt.Sprintf("praetor-source.%03d.b64", index) }

func decodeBootstrapArchive(ctx context.Context, data []byte) (files []bootstrapSourceFile, err error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, reader.Close()) }()
	bounded := &io.LimitedReader{R: reader, N: maxBootstrapSourceBytes + maxBootstrapFiles*1024 + 1}
	archive := tar.NewReader(bounded)
	seen := make(map[string]bool)
	total := int64(0)
	for i := 0; i <= maxBootstrapFiles; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return finishBootstrapArchive(bounded, files)
		}
		if err != nil {
			return nil, err
		}
		total += header.Size
		if err := validateBootstrapHeader(header, i, total, seen); err != nil {
			return nil, err
		}
		content, err := io.ReadAll(archive)
		if err != nil {
			return nil, err
		}
		if err := validateBootstrapSourceFile(header.Name, content); err != nil {
			return nil, err
		}
		seen[header.Name] = true
		files = append(files, bootstrapSourceFile{Name: header.Name, Data: content})
	}
	return nil, errors.New("bootstrap archive exceeds entry limit")
}

func validateBootstrapHeader(header *tar.Header, count int, total int64, seen map[string]bool) error {
	if count == maxBootstrapFiles || header.Size < 0 || header.Size > contextopt.MaxSourceBytes || total > maxBootstrapSourceBytes || seen[header.Name] || header.Typeflag != tar.TypeReg {
		return errors.New("invalid or excessive bootstrap archive entry")
	}
	return nil
}

func finishBootstrapArchive(reader *io.LimitedReader, files []bootstrapSourceFile) ([]bootstrapSourceFile, error) {
	remaining, err := io.Copy(io.Discard, reader)
	if err != nil {
		return nil, err
	}
	if reader.N == 0 || remaining != 0 {
		return nil, errors.New("bootstrap archive has trailing or excessive expanded content")
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	if err := validateBootstrapSourceSet(files); err != nil {
		return nil, err
	}
	return files, nil
}
