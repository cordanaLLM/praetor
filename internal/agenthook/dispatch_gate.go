// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/cordanaLLM/praetor/internal/clientjson"
	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// NativeHooks turns client's registration rows for event into the handlers clientjson merges
// into, or finds in, the client's hook file, with each timeout in the unit of that file and the
// file's matcher reading (HookFile.ExactLiteral). Adoption registers the pre-tool rows through
// it, and DispatchGateRegistered looks the pre-dispatch rows up through it, so the two read one
// handler shape.
func NativeHooks(client string, file HookFile, event Event) []clientjson.Hook {
	var hooks []clientjson.Hook
	for _, row := range Registrations(client) {
		if row.Event != event {
			continue
		}
		hooks = append(hooks, clientjson.Hook{
			Event:        row.NativeEvent,
			Matcher:      row.Matcher,
			Command:      row.Command(),
			Timeout:      int64(row.Timeout / file.TimeoutUnit),
			ExactLiteral: file.ExactLiteral,
			ServedBy:     row.ServedBy,
		})
	}
	return hooks
}

// dispatchGateClients are the clients whose repository hook file can register the pre-dispatch
// row, in name order: a native hook file (NativeHookFile) and a pre-dispatch row in the
// registration table. AGY registers through its installed plugin, outside the repository.
func dispatchGateClients() []string {
	var clients []string
	for _, row := range registrationTable {
		if _, ok := NativeHookFile(row.Client); ok && row.Event == EventPreDispatch && !slices.Contains(clients, row.Client) {
			clients = append(clients, row.Client)
		}
	}
	slices.Sort(clients)
	return clients
}

// DispatchGateRegistered reports whether the repository at root registers the pre-dispatch row
// (`praetorctl hook <client> pre-dispatch`) in the hook file of any client that has one. Only
// then does a hook deny a subagent brief without a `task:` label, so only then may the text
// register block say one does (config.RenderRegisterBlock, #504). The row counts as registered
// where adoption's merge would find it already present (clientjson.PlanHooks leaves the file
// unchanged): a handler the row serves (Registration.ServedBy, the skew guard form included),
// in a group whose matcher covers the row's. A file that is absent, refused by the confined
// read, or not parseable proves no registration, so it counts as none rather than failing the
// render; only a cancelled context is an error.
func DispatchGateRegistered(ctx context.Context, root string) (bool, error) {
	if ctx == nil {
		return false, fmt.Errorf("dispatch gate lookup requires a context")
	}
	for _, client := range dispatchGateClients() {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		file, _ := NativeHookFile(client)
		data, exists, err := contextopt.ObserveSnapshotIn(ctx, root, filepath.FromSlash(file.Path))
		if err != nil || !exists {
			continue
		}
		plan, err := clientjson.PlanHooks(ctx, data, NativeHooks(client, file, EventPreDispatch))
		if err == nil && !plan.Changed {
			return true, nil
		}
	}
	return false, ctx.Err()
}
