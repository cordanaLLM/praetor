// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package harvester

import (
	"bufio"
	"context"
	"fmt"
	"io"
)

// ScanLimits bounds one JSONL scan. MaxRecords counts physical lines and MaxBytes counts
// the bytes read; a zero MaxBytes leaves the byte count to the caller, which already holds
// a bounded snapshot. Overflow of either bound is an error, never a silent stop.
type ScanLimits struct {
	MaxRecords int
	MaxBytes   int64
}

// CountingReader counts the bytes read through it, so a bound can be checked after the fact.
type CountingReader struct {
	Reader io.Reader
	Count  int64
}

// Read implements io.Reader.
func (c *CountingReader) Read(buffer []byte) (int, error) {
	n, err := c.Reader.Read(buffer)
	c.Count += int64(n)
	return n, err
}

// ScanTranscriptLines is the one JSONL line reader of the harvester: lines up to
// MaxTranscriptLineBytes, the record bound and the context are enforced, and visit
// receives the 1-based line number with the raw bytes (valid until the next call).
func ScanTranscriptLines(ctx context.Context, reader io.Reader, limits ScanLimits, visit func(line int, raw []byte) error) error {
	counter := &CountingReader{Reader: reader}
	if limits.MaxBytes > 0 {
		counter.Reader = io.LimitReader(reader, limits.MaxBytes+1)
	}
	scanner := bufio.NewScanner(counter)
	scanner.Buffer(make([]byte, 64*1024), MaxTranscriptLineBytes+1)
	for line := 1; scanner.Scan(); line++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if line > limits.MaxRecords {
			return fmt.Errorf("transcript exceeds %d record bound", limits.MaxRecords)
		}
		if err := limits.byteOverflow(counter.Count); err != nil {
			return err
		}
		if err := visit(line, scanner.Bytes()); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan transcript: %w", err)
	}
	return limits.byteOverflow(counter.Count)
}

func (l ScanLimits) byteOverflow(count int64) error {
	if l.MaxBytes > 0 && count > l.MaxBytes {
		return fmt.Errorf("transcript exceeds %d byte bound", l.MaxBytes)
	}
	return nil
}
