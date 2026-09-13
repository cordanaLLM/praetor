// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

// Package contextopt builds review candidates from explicitly selected local text
// files. It never rewrites policy, discovers additional sources, or activates a pack.
package contextopt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
)

const (
	MaxSources     = 64
	MaxSourceBytes = 1 << 20
	MaxTotalBytes  = 8 << 20
	MaxDuration    = 30 * time.Second
	MaxPathBytes   = 4096
	MaxPathDepth   = 128
)

const reviewNotice = "Review candidate only: document bytes are preserved, but combining loading scopes or removing repeated instructions can change agent behavior. Review source aliases and boundaries before use. Sources and active configuration are unchanged."

// Options selects files relative to one explicit root. Paths must be unique,
// clean, relative names; symlinks and non-regular files are rejected.
type Options struct {
	Root    string
	Sources []string
}

// Source records provenance, including aliases whose original scopes still need
// review. Different content with the same basename is always retained separately.
type Source struct {
	Path         string `json:"path"`
	SHA256       string `json:"sha256"`
	Bytes        int    `json:"bytes"`
	RetainedPath string `json:"retained_path"`
	Reason       string `json:"reason"`
}

// Document gives an exact byte range in context.md and all source names it serves.
type Document struct {
	SourcePaths []string `json:"source_paths"`
	SHA256      string   `json:"sha256"`
	Offset      int      `json:"offset"`
	Bytes       int      `json:"bytes"`
}

// Report contains metadata only. Savings measure context.md including separators;
// manifest storage is excluded and no token or behavioral-equivalence claim is made.
type Report struct {
	Format         string     `json:"format"`
	ReviewRequired bool       `json:"review_required"`
	Notice         string     `json:"notice"`
	Sources        []Source   `json:"sources"`
	Documents      []Document `json:"documents"`
	InputBytes     int        `json:"input_bytes"`
	PayloadBytes   int        `json:"payload_bytes"`
	PackBytes      int        `json:"pack_bytes"`
	SavedBytes     int        `json:"saved_bytes"`
	PackSHA256     string     `json:"pack_sha256"`
}

// Plan owns an immutable snapshot; only WriteCandidate exposes its contents, in a
// new private directory. Metadata is safe for ordinary command/MCP output.
type Plan struct {
	options Options
	report  Report
	pack    []byte
}

// Metadata returns a detached copy, without source contents.
func (p *Plan) Metadata() Report {
	r := p.report
	r.Sources = append([]Source(nil), r.Sources...)
	r.Documents = append([]Document(nil), r.Documents...)
	for i := 0; i < len(r.Documents); i++ {
		r.Documents[i].SourcePaths = append([]string(nil), r.Documents[i].SourcePaths...)
	}
	return r
}

// Analyze snapshots every source or fails without a partial success. Exact file
// duplicates and exact current compiler projections of selected root AGENTS.md
// share a retained document; all other bytes remain intact and in selection order.
func Analyze(ctx context.Context, options Options) (*Plan, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}
	ctx, cancel := context.WithTimeout(ctx, MaxDuration)
	defer cancel()
	selected, err := validateOptions(options)
	if err != nil {
		return nil, err
	}
	data, err := snapshots(ctx, selected)
	if err != nil {
		return nil, err
	}
	p := buildPlan(selected, data)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return p, nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func projectionOwners(options Options, data [][]byte) map[int]int {
	owners := make(map[int]int)
	for i := 0; i < len(data); i++ {
		if options.Sources[i] != "AGENTS.md" {
			continue
		}
		compiled, err := agentcontext.NewTranspiler().CompileContent(string(data[i]))
		if err != nil {
			// Over-budget or empty canonical text cannot prove a projection. Retain it.
			continue
		}
		for j := 0; j < len(data); j++ {
			for k := 0; k < len(compiled.Files); k++ {
				target := compiled.Files[k]
				if options.Sources[j] == target.RelativePath && bytes.Equal(data[j], []byte(target.Content)) {
					owners[j] = i
				}
			}
		}
	}
	return owners
}

func buildPlan(options Options, data [][]byte) *Plan {
	p := &Plan{options: options, report: Report{
		Format: "praetor-context-pack-v1", ReviewRequired: true, Notice: reviewNotice,
	}}
	owners := projectionOwners(options, data)
	for i := 0; i < len(data); i++ {
		owner, reason := selectOwner(i, options, data, owners)
		owners[i] = owner
		p.report.Sources = append(p.report.Sources, Source{Path: options.Sources[i],
			SHA256: digest(data[i]), Bytes: len(data[i]), RetainedPath: options.Sources[owner], Reason: reason})
		p.report.InputBytes += len(data[i])
	}
	for i := 0; i < len(data); i++ {
		if owners[i] == i {
			p.appendDocument(i, data[i])
		}
	}
	p.report.PackBytes = len(p.pack)
	p.report.SavedBytes = p.report.InputBytes - p.report.PackBytes
	p.report.PackSHA256 = digest(p.pack)
	return p
}

func selectOwner(index int, options Options, data [][]byte, owners map[int]int) (int, string) {
	if owner, ok := owners[index]; ok {
		return owner, "verified_compiler_projection"
	}
	for i := 0; i < len(data); i++ {
		if options.Sources[i] == "AGENTS.md" && bytes.Equal(data[i], data[index]) && index != i {
			return i, "exact_duplicate"
		}
	}
	for i := 0; i < index; i++ {
		if owners[i] == i && bytes.Equal(data[i], data[index]) {
			return i, "exact_duplicate"
		}
	}
	return index, "retained"
}

func (p *Plan) appendDocument(index int, data []byte) {
	doc := Document{SHA256: digest(data), Bytes: len(data)}
	for i := 0; i < len(p.report.Sources); i++ {
		source := p.report.Sources[i]
		if source.RetainedPath == p.options.Sources[index] {
			doc.SourcePaths = append(doc.SourcePaths, source.Path)
		}
	}
	separator := fmt.Sprintf("\n\n<!-- context-document:%d; source scopes and byte ranges: manifest.json -->\n\n", len(p.report.Documents)+1)
	p.pack = append(p.pack, separator...)
	doc.Offset = len(p.pack)
	p.pack = append(p.pack, data...)
	p.report.PayloadBytes += len(data)
	p.report.Documents = append(p.report.Documents, doc)
}
