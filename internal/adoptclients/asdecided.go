package adoptclients

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/clientsetup"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/decisions"
)

var asDecidedRegistry = clientsetup.Registry{Version: 1, Servers: []clientsetup.Server{{
	Name: "asdecided", Command: "praetorctl", Args: []string{"decisions", "serve"},
}}}

var asDecidedClients = []clientsetup.Client{
	clientsetup.Claude, clientsetup.Gemini, clientsetup.OpenCodeV1, clientsetup.Continue,
}

// ReconcileAsDecided exports repository ADRs and merges the AsDecided server into supported project configs.
func ReconcileAsDecided(ctx context.Context, repo string, dryRun bool) ([]adopt.ActionDetail, error) {
	if ctx == nil {
		return nil, errors.New("AsDecided adoption requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if dryRun {
		return []adopt.ActionDetail{skipAsDecided("asdecided", "Dry run: ADR export and project MCP merges are not written"),
			skipAsDecided(string(clientsetup.Codex), "Codex CLI owns its native MCP configuration"),
			skipAsDecided(string(clientsetup.AGY), "AGY MCP configuration is host-local and git-ignored")}, nil
	}
	report, err := decisions.Export(ctx, repo, "")
	if err != nil {
		return nil, fmt.Errorf("export repository ADR records: %w", err)
	}
	actions := []adopt.ActionDetail{{Path: ".standards/cache/decisions", Action: "reconcile",
		Details: fmt.Sprintf("Exported %d repository ADR records", len(report.Written))}}
	actions = append(actions, skipAsDecided(string(clientsetup.Codex), "Codex CLI owns its native MCP configuration"),
		skipAsDecided(string(clientsetup.AGY), "AGY MCP configuration is host-local and git-ignored"))
	if len(report.Written) == 0 {
		actions = append(actions, skipAsDecided("asdecided", "No ADR records found; no project MCP server was added"))
		return actions, nil
	}
	merged, err := mergeAsDecidedClients(ctx, repo)
	if err != nil {
		return nil, err
	}
	return append(actions, merged...), nil
}

func skipAsDecided(path, detail string) adopt.ActionDetail {
	return adopt.ActionDetail{Path: path, Action: "skip", Details: detail}
}

func mergeAsDecidedClients(ctx context.Context, repo string) ([]adopt.ActionDetail, error) {
	actions := make([]adopt.ActionDetail, 0, len(asDecidedClients)*2)
	for i := 0; i < len(asDecidedClients) && i < 4; i++ {
		clientActions, err := mergeAsDecidedClient(ctx, repo, asDecidedClients[i])
		if err != nil {
			return nil, err
		}
		actions = append(actions, clientActions...)
	}
	return actions, nil
}

func mergeAsDecidedClient(ctx context.Context, repo string, client clientsetup.Client) ([]adopt.ActionDetail, error) {
	plan, err := clientsetup.BuildProjectPlan(ctx, asDecidedRegistry, client, nil)
	if err != nil {
		return nil, fmt.Errorf("plan %s project config: %w", client, err)
	}
	rel := plan.RelativePath
	full := filepath.Join(repo, filepath.FromSlash(rel))
	before, exists, err := contextopt.ObserveSnapshot(ctx, full)
	if err != nil {
		return nil, fmt.Errorf("read %s project config: %w", client, err)
	}
	plan, err = clientsetup.BuildProjectPlan(ctx, asDecidedRegistry, client, before)
	if errors.Is(err, clientsetup.ErrConflict) {
		return []adopt.ActionDetail{skipAsDecided(rel, "User-owned asdecided entry conflicts; left unchanged")}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("merge %s project config: %w", client, err)
	}
	return reconcileClientTarget(ctx, client, full, rel, before, exists, plan)
}

func reconcileClientTarget(ctx context.Context, client clientsetup.Client, full, rel string, before []byte, exists bool, plan *clientsetup.Plan) ([]adopt.ActionDetail, error) {
	var actions []adopt.ActionDetail
	adopterKept := false
	var err error
	if plan.Changed {
		adopterKept, err = writeDecisionClientConfig(ctx, full, before, exists, plan.Content)
		if err != nil {
			return nil, fmt.Errorf("write %s project config: %w", client, err)
		}
		actions = append(actions, adopt.ActionDetail{Path: rel, Action: "merge", Details: "Added pinned AsDecided decision-recall MCP server"})
	} else {
		if strings.HasSuffix(full, ".json") {
			adopterKept, err = syncJSONSidecar(ctx, full)
			if err != nil {
				return nil, fmt.Errorf("sync sidecar for %s: %w", client, err)
			}
		}
		actions = append(actions, adopt.ActionDetail{Path: rel, Action: "reconcile", Details: "AsDecided project MCP server already matches"})
	}
	if adopterKept {
		actions = append(actions, skipAsDecided(clientsetup.SidecarPath(rel), "Adopter-owned licence sidecar preserved; left unchanged"))
	}
	return actions, nil
}

func writeDecisionClientConfig(ctx context.Context, full string, before []byte, exists bool, content []byte) (bool, error) {
	if err := contextopt.EnsureDirectory(ctx, filepath.Dir(full), 0o755); err != nil {
		return false, err
	}
	if err := contextopt.ReplaceSnapshot(ctx, full, content, contextopt.ReplaceOptions{Expected: before, Exists: exists, Mode: 0o644}); err != nil {
		return false, err
	}
	actual, err := contextopt.ReadSnapshot(ctx, full)
	if err != nil {
		return false, err
	}
	if !bytes.Equal(actual, content) {
		return false, fmt.Errorf("%s differs after write/readback", full)
	}
	if strings.HasSuffix(full, ".json") {
		return syncJSONSidecar(ctx, full)
	}
	return false, nil
}

func syncJSONSidecar(ctx context.Context, full string) (bool, error) {
	sidecarPath := clientsetup.SidecarPath(full)
	before, exists, err := contextopt.ObserveSnapshot(ctx, sidecarPath)
	if err != nil {
		return false, fmt.Errorf("observe sidecar %s: %w", sidecarPath, err)
	}
	if exists && clientsetup.IsAdopterOwnedSidecar(before) {
		return true, nil
	}
	wanted := []byte(clientsetup.LicenseSidecar)
	if exists && bytes.Equal(before, wanted) {
		return false, nil
	}
	options := contextopt.ReplaceOptions{Expected: before, Exists: exists, Mode: 0o644}
	if err := contextopt.ReplaceSnapshot(ctx, sidecarPath, wanted, options); err != nil {
		return false, fmt.Errorf("write sidecar %s: %w", sidecarPath, err)
	}
	actual, err := contextopt.ReadSnapshot(ctx, sidecarPath)
	if err != nil {
		return false, fmt.Errorf("read back sidecar %s: %w", sidecarPath, err)
	}
	if !bytes.Equal(actual, wanted) {
		return false, fmt.Errorf("%s differs after write/readback", sidecarPath)
	}
	return false, nil
}
