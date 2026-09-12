package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/cordanaLLM/praetor/internal/harvester"
	"github.com/cordanaLLM/praetor/internal/mcp"
)

func (s *Server) createTranscriptIngestTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{Type: "object", Required: []string{"source_path", "cache_dir"},
		Properties: map[string]mcp.PropertySchema{
			"source_path":     {Type: "string", Description: "Explicit local Antigravity JSONL source; full counterpart preferred"},
			"cache_dir":       {Type: "string", Description: "Explicit private local destination for observed events"},
			"cursor":          {Type: "string", Description: "Opaque resume cursor from the previous page"},
			"expected_sha256": {Type: "string", Description: "Optional required SHA256 of the selected full source"},
			"max_records":     {Type: "integer", Description: "Maximum records in this page, 1..10000 (default 1000)"},
		}}
	return mcp.NewMutatingTool("standards_transcript_ingest",
		"Ingest a bounded page of observed transcript events into private local storage; return metadata and continuation, never upload or execute content",
		schema, s.ingestTranscript, false, true)
}

func (s *Server) ingestTranscript(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	opts, err := s.transcriptArguments(args)
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
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
	response := mcp.TextResult(string(data))
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
		{"cursor", &opts.Cursor}, {"expected_sha256", &opts.ExpectedSHA256},
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
	value, ok := args["max_records"]
	if !ok {
		return 1000, nil
	}
	number, ok := value.(float64)
	if integer, isInt := value.(int); isInt {
		number, ok = float64(integer), true
	}
	if !ok || number < 1 || number > harvester.MaxTranscriptBatchRecords || number != math.Trunc(number) {
		return 0, fmt.Errorf("max_records must be an integer between 1 and %d", harvester.MaxTranscriptBatchRecords)
	}
	return int(number), nil
}
