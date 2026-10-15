// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/cordanaLLM/praetor/internal/clientjson"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Codex runs a project hook in the working directory of the tool call, not of the project
// whose .codex/hooks.json registered it, and exports no project root (openai/codex
// codex-rs/core/src/hook_runtime.rs tool_hook_cwd; codex-rs/hooks/src/engine/discovery.rs sets
// PLUGIN_ROOT for plugin hooks only). A tool call in another repository therefore named that
// repository, or none (#1347). SessionStart does run in the project: its row records the
// project root under the user's cache, keyed by the session_id every later payload carries, and
// the pre-tool and pre-dispatch rows judge against that root. A session without a record, such
// as one in a repository rendered before the SessionStart row existed, is judged against the
// root of the payload's working directory when that root is governed, and the notice names the
// missing record and praetorctl adopt; a call whose working directory is no governed repository
// is refused, so the guard fails closed where #1347 let a foreign repository decide.

// codexSessionDir is the directory of session records below the user's cache.
const codexSessionDir = "praetor/sessions/codex"

// maxCodexSessionRecordBytes bounds one record, a root path and a newline.
const maxCodexSessionRecordBytes = 4096

// codexSessionID is the form of a session id that may name a record file.
var codexSessionID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// codexSessionRooted reports whether row resolves its root from the session record.
func codexSessionRooted(row Registration) bool {
	return row.Client == "codex" && (row.Event == EventPreTool || row.Event == EventPreDispatch)
}

// callRoot is the repository root one call is judged against: the session's recorded root for
// a Codex pre-tool or pre-dispatch call, the payload's workspace otherwise (ResolveRoot). The
// notice is set when a Codex call falls back to its working directory's governed root.
func callRoot(ctx context.Context, row Registration, canonical Canonical, in Invocation) (string, string, error) {
	if !codexSessionRooted(row) {
		root, err := ResolveRoot(ctx, canonical.Workspaces, in.WorkDir)
		return root, "", err
	}
	root, err := codexSessionRoot(canonical.ConversationID, in.Getenv)
	if err == nil {
		return root, "", nil
	}
	if !errors.Is(err, errNoCodexSessionRecord) {
		return "", "", err
	}
	fallback, fallbackErr := ResolveRoot(ctx, canonical.Workspaces, in.WorkDir)
	if fallbackErr != nil || fallback == "" || !Governed(fallback) {
		return "", "", err
	}
	return fallback, "judged against the working directory's repository " + fallback + ": " + err.Error() +
		"; praetorctl adopt renders the codex SessionStart row that keeps it", nil
}

// errNoCodexSessionRecord marks a session whose SessionStart recorded no root.
var errNoCodexSessionRecord = errors.New("no codex session record")

// codexSessionRecord is the record file of session id, or an error naming why it has none.
func codexSessionRecord(id string, getenv func(string) string) (string, error) {
	if id == "" {
		return "", errors.New("the payload names no codex session_id")
	}
	if !codexSessionID.MatchString(id) {
		return "", fmt.Errorf("codex session id %q cannot name a session record", boundReason(id))
	}
	base := lookup(getenv, "XDG_CACHE_HOME")
	if base == "" {
		home := lookup(getenv, "HOME")
		if home == "" && runtime.GOOS == "windows" {
			home = lookup(getenv, "USERPROFILE")
		}
		if home == "" {
			return "", errors.New("neither XDG_CACHE_HOME nor HOME is set, so no codex session record can be kept")
		}
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, filepath.FromSlash(codexSessionDir), id), nil
}

func lookup(getenv func(string) string, key string) string {
	if getenv == nil {
		return ""
	}
	return getenv(key)
}

// codexSessionRoot reads the root the session's SessionStart recorded.
func codexSessionRoot(id string, getenv func(string) string) (string, error) {
	path, err := codexSessionRecord(id, getenv)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errNoCodexSessionRecord, err)
	}
	data, err := util.ReadFileLimited(path, maxCodexSessionRecordBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%w %s: its SessionStart hook did not run in an adopted repository; "+
			"start the session again in the repository whose .codex/hooks.json registers this hook", errNoCodexSessionRecord, id)
	}
	if err != nil {
		return "", fmt.Errorf("read the record of codex session %s: %w", id, err)
	}
	root := strings.TrimSpace(string(data))
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("the record of codex session %s names no absolute root", id)
	}
	return root, nil
}

// recordCodexSession keeps the governed root of the workspace a SessionStart payload names
// under the session's id. A session outside an adopted repository records nothing: its tool
// calls are then refused, which only a hooks file registered there can cause. It never blocks
// the session start.
func recordCodexSession(ctx context.Context, canonical Canonical, in Invocation) Verdict {
	root, err := ResolveRoot(ctx, canonical.Workspaces, in.WorkDir)
	if err != nil || root == "" || !Governed(root) {
		return Verdict{Outcome: Skip, Reason: "codex session started outside an adopted repository; no session record kept"}
	}
	path, err := codexSessionRecord(canonical.ConversationID, in.Getenv)
	if err != nil {
		return Verdict{Outcome: Skip, Reason: "codex session record not kept: " + err.Error()}
	}
	if err := util.MkdirSecure(filepath.Dir(path), util.SecureDirPerm); err != nil {
		return Verdict{Outcome: Skip, Reason: "codex session record not kept: " + err.Error()}
	}
	if err := util.WriteFileAtomic(path, []byte(root+"\n"), util.SecureFilePerm); err != nil {
		return Verdict{Outcome: Skip, Reason: "codex session record not kept: " + err.Error()}
	}
	return Verdict{Outcome: Allow}
}

// CodexSessionStartMissing reports whether the repository at root registers a Codex pre-tool or
// pre-dispatch row in .codex/hooks.json without the SessionStart row that records the session's
// root (#1347): such a rendering predates the row, and its calls fall back to the working
// directory's repository until praetorctl adopt renders it again. A missing or unparseable file
// registers nothing, so nothing is missing.
func CodexSessionStartMissing(ctx context.Context, root string) (bool, error) {
	file, ok := NativeHookFile("codex")
	if !ok {
		return false, nil
	}
	data, exists, err := contextopt.ObserveSnapshotIn(ctx, root, filepath.FromSlash(file.Path))
	if err != nil || !exists {
		return false, ctx.Err()
	}
	registered := false
	for _, event := range []Event{EventPreTool, EventPreDispatch} {
		plan, err := clientjson.PlanHooks(ctx, data, NativeHooks("codex", file, event))
		if err == nil && !plan.Changed {
			registered = true
		}
	}
	if !registered {
		return false, ctx.Err()
	}
	plan, err := clientjson.PlanHooks(ctx, data, NativeHooks("codex", file, EventSessionStart))
	return err == nil && plan.Changed, ctx.Err()
}
