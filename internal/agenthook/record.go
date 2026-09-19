package agenthook

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// RecordDirEnv is the record-mode environment variable of the rollout spec's hook
// contract (3.2.6): set it to a private directory and every hook call writes its raw,
// bounded stdin payload there instead of judging it, then answers a neutral allow. It
// exists to build fixtures a client's own docs do not cover
// (testdata/<client>/recorded-<os>/*.json once redacted, HISS-20) from a real session,
// without ever reading or writing that session's own operator config.
const RecordDirEnv = "PRAETOR_HOOK_RECORD_DIR"

// recordFilePerm is the permission of one recorded fixture: owner read-write only, since
// a raw payload may carry a workspace path or other operator-local detail before it is
// redacted into a tracked testdata file.
const recordFilePerm = 0o600

// recordAndAllow reads the invocation's bounded stdin (nothing to read for the
// environment event, which carries none), writes it verbatim to one new file under dir,
// and answers the client's dialect-correct neutral allow. A read or write failure denies
// closed rather than losing the payload silently and calling it recorded.
func recordAndAllow(ctx context.Context, dir string, row Registration, dialect Dialect, in Invocation) Response {
	canonical := Canonical{Event: row.Event}
	if row.Event == EventEnvironment {
		return dialect.Encode(canonical, Verdict{Outcome: Allow})
	}
	payload, err := readBounded(ctx, in.Stdin)
	if err != nil {
		return dialect.Encode(canonical, Verdict{Deny, "[BLOCKED BY HISS] record mode: " + err.Error()})
	}
	if err := writeRecording(dir, row.Client, row.Event, payload); err != nil {
		return dialect.Encode(canonical, Verdict{Deny, "[BLOCKED BY HISS] record mode: " + err.Error()})
	}
	return dialect.Encode(canonical, Verdict{Outcome: Allow})
}

// writeRecording creates dir if needed (owner-only) and writes payload to one new file
// named for client, event and the instant of the call, so repeated events of the same
// kind never collide or overwrite an earlier capture.
func writeRecording(dir, client string, event Event, payload []byte) error {
	if err := util.MkdirSecure(dir, util.SecureDirPerm); err != nil {
		return err
	}
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%s-%s-%s-%s.json", client, event, time.Now().UTC().Format("20060102T150405.000000000"), suffix)
	path, err := util.ConfinePath(dir, name)
	if err != nil {
		return fmt.Errorf("record mode: %w", err)
	}
	if err := util.WriteFileSecure(path, payload, recordFilePerm); err != nil {
		return fmt.Errorf("record mode: %w", err)
	}
	return nil
}

// randomSuffix guards writeRecording's filename against a same-nanosecond collision; the
// timestamp already makes one vanishingly unlikely, this makes it impossible in practice.
func randomSuffix() (string, error) {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("record mode: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
