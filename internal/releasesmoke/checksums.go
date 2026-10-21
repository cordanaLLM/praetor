// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package releasesmoke

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/forge"
)

const maxChecksumLine = 4096

type checksumEntry struct{ name, digest string }

// checksumParser buffers at most one bounded line, never a whole checksum asset.
type checksumParser struct {
	line    []byte
	entries []checksumEntry
	names   map[string]bool
}

func newChecksumParser() *checksumParser { return &checksumParser{names: make(map[string]bool)} }

func (p *checksumParser) Write(data []byte) (int, error) {
	total := len(data)
	for consumed := 0; consumed < total; {
		end := bytes.IndexByte(data[consumed:], '\n')
		if end < 0 {
			end = total - consumed
		}
		if len(p.line)+end > maxChecksumLine {
			return consumed, errors.New("SHA256SUMS: line exceeds 4096 bytes")
		}
		p.line = append(p.line, data[consumed:consumed+end]...)
		consumed += end
		if consumed < total {
			if err := p.parseLine(); err != nil {
				return consumed, fmt.Errorf("SHA256SUMS: %w", err)
			}
			consumed++
			p.line = p.line[:0]
		}
	}
	return total, nil
}

func (p *checksumParser) parseLine() error {
	line := strings.TrimSuffix(string(p.line), "\r")
	if line == "" {
		return errors.New("empty checksum line")
	}
	if len(p.entries) >= forge.MaxReleaseAssets {
		return errors.New("checksum entry count exceeds asset cap")
	}
	if len(line) < 67 || line[64] != ' ' || (line[65] != ' ' && line[65] != '*') {
		return errors.New("malformed checksum line (expected SHA-256 and filename)")
	}
	if _, err := hex.DecodeString(line[:64]); err != nil {
		return fmt.Errorf("invalid SHA-256: %w", err)
	}
	name := line[66:]
	if !validName(name) {
		return fmt.Errorf("invalid checksum filename %q", name)
	}
	if p.names[name] {
		return fmt.Errorf("duplicate checksum filename %s", name)
	}
	p.names[name] = true
	p.entries = append(p.entries, checksumEntry{name: name, digest: strings.ToLower(line[:64])})
	return nil
}

func (p *checksumParser) finish() error {
	if len(p.line) > 0 {
		if err := p.parseLine(); err != nil {
			return err
		}
	}
	if len(p.entries) == 0 {
		return errors.New("no checksum entries")
	}
	return nil
}

func validName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	for i := 0; i < len(name); i++ {
		if name[i] < 32 || name[i] == 127 || name[i] == '/' || name[i] == '\\' {
			return false
		}
	}
	return true
}
