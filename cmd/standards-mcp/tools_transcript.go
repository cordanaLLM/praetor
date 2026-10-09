package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/harvester"
	"github.com/cordanaLLM/praetor/internal/mcp"
)

func (s *Server) createTranscriptIngestTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{Type: "object", Required: []string{"source_path", "cache_dir"},
		Properties: map[string]mcp.PropertySchema{
			"format":          {Type: "string", Description: "format: antigravity-jsonl-v1 (default) | claude-code-jsonl-v1."},
			"source_path":     {Type: "string", Description: "source_path: explicit local JSONL. prefer: Antigravity full counterpart."},
			"cache_dir":       {Type: "string", Description: "cache_dir: explicit private local event destination."},
			"cursor":          {Type: "string", Description: "cursor: opaque resume value from prior page."},
			"expected_sha256": {Type: "string", Description: "expected_sha256: optional required SHA256 for selected full source."},
			"max_records":     {Type: "integer", Description: "max_records: page record limit 1..10000 (default 1000)."},
		}}
	return mcp.NewMutatingTool("standards_transcript_ingest",
		"action: ingest bounded transcript page into private local storage. return: metadata + continuation. never upload or execute content.",
		schema, s.ingestTranscript, false, true)
}

func (s *Server) ingestTranscript(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	opts, err := s.transcriptArguments(args)
	if err != nil {
		return mcpErrorResult(err.Error(), mcpTextUntrusted), nil
	}
	report, ingestErr := harvester.IngestTranscript(ctx, opts)
	result := struct {
		Report *harvester.TranscriptIngestReport `json:"report,omitempty"`
		Error  string                            `json:"error,omitempty"`
	}{Report: report}
	if ingestErr != nil {
		result.Error = ingestErr.Error()
	}
	data, err := json.Marshal(result)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("encode transcript report: %v", err)), nil
	}
	response := mcpTextResult(string(data), mcpTextStructuredJSON)
	response.IsError = ingestErr != nil
	return response, nil
}

func (s *Server) transcriptArguments(args map[string]any) (harvester.TranscriptIngestOptions, error) {
	var opts harvester.TranscriptIngestOptions
	fields := []struct {
		key   string
		value *string
	}{
		{"source_path", &opts.SourcePath}, {"cache_dir", &opts.CacheDir},
		{"format", &opts.Format}, {"cursor", &opts.Cursor}, {"expected_sha256", &opts.ExpectedSHA256},
	}
	for _, field := range fields {
		value, err := argString(args, field.key)
		if err != nil {
			return opts, err
		}
		*field.value = value
	}
	if opts.SourcePath == "" || opts.CacheDir == "" {
		return opts, fmt.Errorf("source_path and cache_dir are required")
	}
	var err error
	if opts.SourcePath, err = s.confinePath(opts.SourcePath); err != nil {
		return opts, err
	}
	if opts.CacheDir, err = s.confinePath(opts.CacheDir); err != nil {
		return opts, err
	}
	opts.MaxRecords, err = transcriptBatchArgument(args)
	return opts, err
}

func transcriptBatchArgument(args map[string]any) (int, error) {
	value, present, whole := intArg(args, "max_records")
	if !present {
		return 1000, nil
	}
	if !whole || value < 1 || value > harvester.MaxTranscriptBatchRecords {
		return 0, fmt.Errorf("max_records must be an integer between 1 and %d", harvester.MaxTranscriptBatchRecords)
	}
	return value, nil
}
