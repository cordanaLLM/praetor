// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package govuln

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// The bounds of one govulncheck stream (HISS-02). The runner already caps the output at
// util.MaxCommandOutputBytes.
const (
	maxMessages = 100000
	maxAliases  = 64
)

// The shape of govulncheck's JSON output, protocol v1 (golang.org/x/vuln/internal/govulncheck,
// v1.8.0): a sequence of objects, each holding one message. The config message comes first; the
// progress, SBOM and other members are not read. A field this reader does not name is ignored, so
// a later minor protocol version that adds one still reads.
type message struct {
	Config  *scanConfig `json:"config"`
	OSV     *osvEntry   `json:"osv"`
	Finding *rawFinding `json:"finding"`
}

type scanConfig struct {
	ProtocolVersion string `json:"protocol_version"`
	ScannerName     string `json:"scanner_name"`
	ScannerVersion  string `json:"scanner_version"`
	ScanLevel       string `json:"scan_level"`
	ScanMode        string `json:"scan_mode"`
}

// osvEntry is the part of an OSV entry the gate reads: the other names of the advisory, so a
// statement naming its CVE covers its Go identifier.
type osvEntry struct {
	ID      string   `json:"id"`
	Aliases []string `json:"aliases"`
}

type rawFinding struct {
	OSV          string  `json:"osv"`
	FixedVersion string  `json:"fixed_version"`
	Trace        []frame `json:"trace"`
}

// frame is one frame of a finding's trace. The first frame is the vulnerable code: a module alone
// for a module-level finding, a package for a package-level one, a function for a called symbol.
type frame struct {
	Module   string `json:"module"`
	Version  string `json:"version"`
	Package  string `json:"package"`
	Function string `json:"function"`
	Receiver string `json:"receiver"`
}

// scanResult is what one completed scan reported: the scanner that ran, the deepest finding of
// each advisory and the advisories' other names.
type scanResult struct {
	scanner    string
	configured bool
	findings   map[string]rawFinding
	aliases    map[string][]string
}

// parseStream reads govulncheck's stream. It fails closed: a stream that does not open with a
// protocol v1 configuration of a symbol-level source scan, that does not decode, or that holds a
// finding without an advisory or a trace is no verdict.
func parseStream(out string) (*scanResult, error) {
	result := &scanResult{findings: map[string]rawFinding{}, aliases: map[string][]string{}}
	decoder := json.NewDecoder(strings.NewReader(out))
	for index := 0; index <= maxMessages; index++ {
		var msg message
		err := decoder.Decode(&msg)
		if errors.Is(err, io.EOF) {
			return result.complete()
		}
		if err != nil {
			return nil, fmt.Errorf("%w: message %d of its output is no JSON object: %w", ErrScanIncomplete, index+1, err)
		}
		if index == maxMessages {
			return nil, fmt.Errorf("%w: its output holds more than %d messages", ErrScanIncomplete, maxMessages)
		}
		if err := result.add(index, msg); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("%w: its output holds more than %d messages", ErrScanIncomplete, maxMessages)
}

// add records one message; the first must be the configuration.
func (r *scanResult) add(index int, msg message) error {
	switch {
	case index == 0:
		return r.configure(msg.Config)
	case msg.Finding != nil:
		return r.addFinding(*msg.Finding)
	case msg.OSV != nil && msg.OSV.ID != "":
		r.aliases[msg.OSV.ID] = msg.OSV.Aliases[:min(len(msg.OSV.Aliases), maxAliases)]
	}
	return nil
}

// configure checks the configuration message: the protocol this reader implements, and a
// symbol-level scan of source, the only scan whose package and module findings mean "not called".
func (r *scanResult) configure(c *scanConfig) error {
	if c == nil {
		return fmt.Errorf("%w: its output does not open with govulncheck's config message", ErrScanIncomplete)
	}
	if !strings.HasPrefix(c.ProtocolVersion, "v1.") {
		return fmt.Errorf("%w: it speaks JSON protocol %q; the gate reads v1", ErrScanIncomplete, c.ProtocolVersion)
	}
	if c.ScanLevel != "symbol" || c.ScanMode != "source" {
		return fmt.Errorf("%w: it ran a %s scan of %s; the gate judges a symbol scan of source only",
			ErrScanIncomplete, c.ScanLevel, c.ScanMode)
	}
	r.scanner = strings.TrimSpace(c.ScannerName + " " + c.ScannerVersion)
	r.configured = true
	return nil
}

// addFinding keeps the deepest finding of each advisory: govulncheck reports one per level, and
// the first called symbol it reports stands for the advisory.
func (r *scanResult) addFinding(f rawFinding) error {
	if f.OSV == "" || len(f.Trace) == 0 {
		return fmt.Errorf("%w: it reported a finding without an advisory or a trace", ErrScanIncomplete)
	}
	if kept, ok := r.findings[f.OSV]; ok && levelOf(kept).depth() >= levelOf(f).depth() {
		return nil
	}
	r.findings[f.OSV] = f
	return nil
}

// complete returns the result of a stream that ended, or no verdict when it held no
// configuration at all.
func (r *scanResult) complete() (*scanResult, error) {
	if !r.configured {
		return nil, fmt.Errorf("%w: it printed nothing", ErrScanIncomplete)
	}
	return r, nil
}
