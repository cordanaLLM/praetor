// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/mcp"
	"github.com/cordanaLLM/praetor/internal/wishes"
)

func (s *Server) createWishesStatusTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{Type: "object", Properties: map[string]mcp.PropertySchema{
		"store": {Type: "string", Description: "Local ledger path; defaults to .workingdir/wishes.json, confined to server root"},
	}}
	return mcp.NewReadOnlyTool("standards_wishes_status",
		"Read the explicitly selected private wish and poll ledger, revision and tallies. Returns private wish contents and asserted local voter identities to this client; never initializes or contacts platforms.",
		schema, s.readWishes)
}

func (s *Server) createWishesUpdateTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{Type: "object", Required: []string{"request_json"}, Properties: map[string]mcp.PropertySchema{
		"store":        {Type: "string", Description: "Local ledger path; defaults to .workingdir/wishes.json, confined to server root"},
		"request_json": {Type: "string", Description: "Strict JSON request: action init, add-wish, set-status, open-poll, vote, withdraw or close-poll. expected_revision required except init. Local actors are trusted assertions, not authenticated platform users. See wishes-and-polls guide."},
	}}
	return mcp.NewMutatingTool("standards_wishes_update",
		"Apply one revision-checked private wish or single-choice poll operation and return the ledger. Explicit init only; local ballots never approve wishes. No GitHub, Discord, network calls or publication.",
		schema, s.updateWishes, true, false)
}

func wishesToolArguments(args map[string]any, update bool) error {
	if len(args) > 2 {
		return fmt.Errorf("only store and request_json arguments are accepted")
	}
	for key, value := range args {
		if key != "store" && (!update || key != "request_json") {
			return fmt.Errorf("unsupported wish tool argument %q", key)
		}
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s must be a string", key)
		}
	}
	return nil
}

func (s *Server) readWishes(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if err := wishesToolArguments(args, false); err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	path, err := s.resolvePath(args, "store", ".workingdir/wishes.json")
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	ledger, err := wishes.Read(ctx, path)
	return wishesToolResult(ledger, err)
}

func (s *Server) updateWishes(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if err := wishesToolArguments(args, true); err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	path, err := s.resolvePath(args, "store", ".workingdir/wishes.json")
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	input, err := argString(args, "request_json")
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	request, err := wishes.DecodeRequest([]byte(input))
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	ledger, err := wishes.Apply(ctx, path, request)
	return wishesToolResult(ledger, err)
}

func wishesToolResult(ledger *wishes.Ledger, err error) (*mcp.ToolResult, error) {
	if err != nil {
		return mcp.ErrorResult(err.Error()), nil
	}
	data, err := json.Marshal(ledger)
	if err != nil {
		return nil, fmt.Errorf("encode wish ledger: %w", err)
	}
	return mcp.TextResult(string(data)), nil
}
