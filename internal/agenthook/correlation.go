package agenthook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	MaxCorrelationEntries   = 128
	MaxCorrelationIDBytes   = 1024
	correlationFileMaxBytes = 4096
	correlationLockDelay    = 10 * time.Millisecond
	// correlationLockWait bounds how long one call waits for the store lock. Holders
	// serialise, so a caller waits behind every holder queued before it: n concurrent
	// dispatches wait up to n-1 holds, and one hold is a directory sweep plus an fsynced
	// atomic write. On the Windows runner 16 concurrent reservations outlasted the earlier
	// fixed 400 ms (40 attempts of 10 ms), a wait the 10 s dispatch budget had room for
	// (#558). Every row that reaches the store runs under at least dispatchBudget; half of
	// it leaves the other half to the hold and the rest of the evaluation, and the caller's
	// own deadline still ends the wait earlier.
	correlationLockWait = dispatchBudget / 2
	correlationLockTTL  = 2 * time.Minute
	// correlationLockName is the lock file's name inside the store.
	correlationLockName   = ".lock"
	correlationPendingTTL = 5 * time.Minute
	correlationActiveTTL  = 24 * time.Hour
	correlationDirRel     = "praetor/agenthook-correlations"
	// correlationScanLimit bounds one sweep's directory read (HISS-02). Only live rows count
	// against MaxCorrelationEntries, and eviction keeps them there; the rest of the bound is
	// room for the lock, temporary files the sweep removes once stale, and entries Praetor
	// did not write, which the sweep leaves alone. More entries than this fail closed with
	// the bound named instead of being read in part.
	correlationScanLimit = 1024
)

// errNoCorrelation reports an agent without a Praetor-owned dispatch binding: launched
// without a gated brief, already completed, or expired. Its text is not a Praetor-owned
// boundary, so callers report it as unenforceable instead of denying it.
var errNoCorrelation = errors.New("agent correlation missing")

type correlationEntry struct {
	Resolution     config.Resolution `json:"resolution"`
	CreatedAt      int64             `json:"created_at"`
	HandbackDigest string            `json:"handback_digest,omitempty"`
	// LaunchDigest is the SHA-256 of the prompt a read-only dispatch was rewritten to; the
	// dispatch receipt compares the prompt the agent launched with against it.
	LaunchDigest      string `json:"launch_digest,omitempty"`
	HandbackDelivered bool   `json:"-"`
}

type correlationStore struct {
	dir string
	// lockWait bounds the wait for the store lock; zero means correlationLockWait.
	lockWait time.Duration
}

func newCorrelationStore(ctx context.Context, root, override string) (correlationStore, error) {
	if override != "" {
		if !filepath.IsAbs(override) {
			return correlationStore{}, errors.New("correlation directory override must be absolute")
		}
		return correlationStore{dir: filepath.Clean(override)}, nil
	}
	result, err := util.RunGitProbe(ctx, root, maxRootBytes, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return correlationStore{}, fmt.Errorf("resolve shared Git directory: %w", err)
	}
	commonDir := strings.TrimSpace(string(result.Stdout))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(root, commonDir)
	}
	dir, err := util.ConfinePath(commonDir, correlationDirRel)
	if err != nil {
		return correlationStore{}, fmt.Errorf("resolve correlation directory: %w", err)
	}
	return correlationStore{dir: dir}, nil
}

// reserve stores the pending row of one gated dispatch. At MaxCorrelationEntries it evicts
// the oldest rows instead of refusing: a leaked binding (an agent killed before SubagentStop)
// must not shut off every later launch until its TTL runs out. An evicted agent's text is
// then unowned, a stated skip, never a hold.
func (s correlationStore) reserve(ctx context.Context, client, session, toolID string, resolution config.Resolution, launchDigest string) error {
	name, err := correlationName("pending", client, session, toolID)
	if err != nil {
		return err
	}
	return s.withLock(ctx, func() error {
		live, cleanupErr := s.cleanup()
		if cleanupErr != nil {
			return cleanupErr
		}
		if absentErr := s.requireAbsent(name, "dispatch correlation already exists"); absentErr != nil {
			return absentErr
		}
		if evictErr := s.evictOldest(live); evictErr != nil {
			return evictErr
		}
		return s.write(name, correlationEntry{Resolution: resolution, CreatedAt: time.Now().UTC().Unix(), LaunchDigest: launchDigest})
	})
}

// promote binds the pending row of a dispatch to the agent it launched and returns that row.
func (s correlationStore) promote(ctx context.Context, client, session, toolID, agentID string) (correlationEntry, error) {
	pending, err := correlationName("pending", client, session, toolID)
	if err != nil {
		return correlationEntry{}, err
	}
	active, err := correlationName("active", client, session, agentID)
	if err != nil {
		return correlationEntry{}, err
	}
	var entry correlationEntry
	err = s.withLock(ctx, func() error {
		if _, cleanupErr := s.cleanup(); cleanupErr != nil {
			return cleanupErr
		}
		var readErr error
		if entry, readErr = s.read(pending); readErr != nil {
			return fmt.Errorf("dispatch correlation missing: %w", readErr)
		}
		if absentErr := s.requireAbsent(active, "agent correlation already exists"); absentErr != nil {
			return absentErr
		}
		return s.rename(pending, active, "bind dispatch correlation")
	})
	return entry, err
}

func (s correlationStore) cancelPending(ctx context.Context, client, session, toolID string) error {
	name, err := correlationName("pending", client, session, toolID)
	if err != nil {
		return err
	}
	return s.withLock(ctx, func() error {
		err := os.Remove(filepath.Join(s.dir, name))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove pending correlation: %w", err)
		}
		return nil
	})
}

func (s correlationStore) active(ctx context.Context, client, session, agentID string) (correlationEntry, error) {
	active, err := correlationName("active", client, session, agentID)
	if err != nil {
		return correlationEntry{}, err
	}
	delivered, err := correlationName("delivered", client, session, agentID)
	if err != nil {
		return correlationEntry{}, err
	}
	var found correlationEntry
	err = s.withLock(ctx, func() error {
		if _, cleanupErr := s.cleanup(); cleanupErr != nil {
			return cleanupErr
		}
		entry, readErr := s.read(delivered)
		if readErr == nil {
			entry.HandbackDelivered = true
			found = entry
			return nil
		}
		if !errors.Is(readErr, os.ErrNotExist) {
			return fmt.Errorf("read delivered correlation: %w", readErr)
		}
		entry, readErr = s.readActive(active)
		if readErr != nil {
			return readErr
		}
		found = entry
		return nil
	})
	return found, err
}

func (s correlationStore) markHandbackValidated(ctx context.Context, client, session, agentID, toolID, text string) error {
	active, err := correlationName("active", client, session, agentID)
	if err != nil {
		return err
	}
	delivered, err := correlationName("delivered", client, session, agentID)
	if err != nil {
		return err
	}
	return s.withLock(ctx, func() error {
		if deliveredErr := s.requireUndelivered(delivered); deliveredErr != nil {
			return deliveredErr
		}
		entry, readErr := s.readActive(active)
		if readErr != nil {
			return readErr
		}
		entry.HandbackDigest = correlationHandbackDigest(toolID, text)
		return s.write(active, entry)
	})
}

func (s correlationStore) markHandbackDelivered(ctx context.Context, client, session, agentID, toolID, text string) error {
	active, err := correlationName("active", client, session, agentID)
	if err != nil {
		return err
	}
	delivered, err := correlationName("delivered", client, session, agentID)
	if err != nil {
		return err
	}
	return s.withLock(ctx, func() error {
		if deliveredErr := s.requireUndelivered(delivered); deliveredErr != nil {
			return deliveredErr
		}
		entry, readErr := s.readActive(active)
		if readErr != nil {
			return readErr
		}
		if !entry.handbackMatches(toolID, text) {
			return errors.New("handback receipt does not match a validated report")
		}
		return s.rename(active, delivered, "mark handback delivered")
	})
}

func (s correlationStore) cancelHandback(ctx context.Context, client, session, agentID, toolID, text string) error {
	active, err := correlationName("active", client, session, agentID)
	if err != nil {
		return err
	}
	return s.withLock(ctx, func() error {
		entry, readErr := s.readActive(active)
		if readErr != nil {
			return readErr
		}
		if !entry.handbackMatches(toolID, text) {
			return errors.New("handback abort does not match a validated report")
		}
		entry.HandbackDigest = ""
		return s.write(active, entry)
	})
}

// handbackMatches reports whether entry holds the provisional digest of exactly this
// validated report and tool use.
func (e correlationEntry) handbackMatches(toolID, text string) bool {
	return e.HandbackDigest != "" && e.HandbackDigest == correlationHandbackDigest(toolID, text)
}

// requireAbsent fails when the named row exists; exists names that conflict.
func (s correlationStore) requireAbsent(name, exists string) error {
	_, err := os.Lstat(filepath.Join(s.dir, name))
	if err == nil {
		return errors.New(exists)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect correlation: %w", err)
	}
	return nil
}

// requireUndelivered fails when the agent's handback was already delivered, or when the
// delivered row cannot be read.
func (s correlationStore) requireUndelivered(delivered string) error {
	_, err := s.read(delivered)
	if err == nil {
		return errors.New("subagent handback already delivered")
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read delivered correlation: %w", err)
	}
	return nil
}

// rename moves one row to another name inside the store; action names the step in errors.
func (s correlationStore) rename(from, to, action string) error {
	fromPath, err := util.ConfinePath(s.dir, from)
	if err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	toPath, err := util.ConfinePath(s.dir, to)
	if err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	if err := os.Rename(fromPath, toPath); err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	return nil
}

func (s correlationStore) complete(ctx context.Context, client, session, agentID string) error {
	active, err := correlationName("active", client, session, agentID)
	if err != nil {
		return err
	}
	delivered, err := correlationName("delivered", client, session, agentID)
	if err != nil {
		return err
	}
	return s.withLock(ctx, func() error {
		if removeErr := os.Remove(filepath.Join(s.dir, delivered)); removeErr == nil {
			return nil
		} else if !errors.Is(removeErr, os.ErrNotExist) {
			return fmt.Errorf("remove delivered correlation: %w", removeErr)
		}
		if removeErr := os.Remove(filepath.Join(s.dir, active)); removeErr != nil {
			return fmt.Errorf("remove agent correlation: %w", removeErr)
		}
		return nil
	})
}

func (s correlationStore) withLock(ctx context.Context, action func() error) (resultErr error) {
	if err := util.MkdirSecure(s.dir, util.SecureDirPerm); err != nil {
		return fmt.Errorf("create correlation store: %w", err)
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return fmt.Errorf("open correlation store: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	if err := acquireCorrelationLock(ctx, root, s.lockWaitBound()); err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, removeCorrelationLock(root, correlationLockName)) }()
	return action()
}

// lockWaitBound is how long withLock waits for the lock: lockWait, or correlationLockWait
// when unset.
func (s correlationStore) lockWaitBound() time.Duration {
	if s.lockWait > 0 {
		return s.lockWait
	}
	return correlationLockWait
}

// acquireCorrelationLock creates the lock file in root, retrying every correlationLockDelay
// while another caller holds it. It tries at least once, and at most wait/correlationLockDelay
// times (HISS-02); ctx ends the wait earlier. A lock older than correlationLockTTL is a
// holder that died and is reclaimed.
func acquireCorrelationLock(ctx context.Context, root *os.Root, wait time.Duration) error {
	attempts := max(1, int(wait/correlationLockDelay))
	for attempt := 0; attempt < attempts; attempt++ {
		if held, err := tryCorrelationLock(root); err != nil || held {
			return err
		}
		if staleErr := removeStaleCorrelationLock(root, correlationLockName, time.Now().UTC()); staleErr != nil {
			return staleErr
		}
		timer := time.NewTimer(correlationLockDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return fmt.Errorf("correlation store remained locked for %s", wait)
}

// tryCorrelationLock creates the lock file once and reports whether this call now holds it.
// A lock another caller holds is not an error.
func tryCorrelationLock(root *os.Root) (bool, error) {
	file, err := root.OpenFile(correlationLockName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, util.SecureFilePerm)
	if lockContended(err, runtime.GOOS) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("acquire correlation lock: %w", err)
	}
	if closeErr := file.Close(); closeErr != nil {
		return false, errors.Join(closeErr, removeCorrelationLock(root, correlationLockName))
	}
	return true, nil
}

// windowsPendingDelete reports whether err is Windows refusing access to the lock file while
// its deletion is still pending, which is another caller's Remove a moment earlier: Windows
// answers a create, a stat or a remove of such a file with ERROR_ACCESS_DENIED, reported as
// os.ErrPermission, where other platforms report the file as existing or already gone.
// TestCorrelationStoreConcurrentReservations failed on windows-latest with "openat .lock:
// Access is denied" and "statat .lock: Access is denied" before this.
func windowsPendingDelete(err error, goos string) bool {
	return goos == "windows" && errors.Is(err, os.ErrPermission)
}

// lockContended reports whether an exclusive create of the lock file failed because another
// caller holds the lock: the file exists, or on Windows its deletion is still pending, which is
// the same contention one release later. Either is retried.
func lockContended(err error, goos string) bool {
	return errors.Is(err, os.ErrExist) || windowsPendingDelete(err, goos)
}

func removeCorrelationLock(root *os.Root, name string) error {
	// A Windows lock file whose deletion is pending is already on its way out.
	if err := root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) && !windowsPendingDelete(err, runtime.GOOS) {
		return fmt.Errorf("remove correlation lock: %w", err)
	}
	return nil
}

func removeStaleCorrelationLock(root *os.Root, name string, now time.Time) error {
	info, err := root.Lstat(name)
	// Gone, or on Windows being deleted: either way no stale lock is left to reclaim.
	if errors.Is(err, os.ErrNotExist) || windowsPendingDelete(err, runtime.GOOS) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect correlation lock: %w", err)
	}
	if info.ModTime().Before(now.Add(-correlationLockTTL)) {
		return removeCorrelationLock(root, name)
	}
	return nil
}

// correlationFile is one live row the sweep kept, with its last write time.
type correlationFile struct {
	name     string
	modified time.Time
}

// correlationFileState classifies one directory entry for the sweep.
type correlationFileState int

const (
	correlationOther correlationFileState = iota // the lock, a fresh temporary file, or a foreign file: left alone
	correlationLive
	correlationStale
)

// cleanup removes expired rows and abandoned temporary files, and returns the live rows.
// The directory read is bounded by correlationScanLimit, not by the row cap: abandoned
// temporary files and foreign entries never count against MaxCorrelationEntries, so they
// cannot wedge the store below the scan bound, and the sweep removes the stale ones before
// the caller evicts or reserves.
func (s correlationStore) cleanup() ([]correlationFile, error) {
	entries, err := s.list()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	live := make([]correlationFile, 0, min(len(entries), MaxCorrelationEntries+1))
	for index := 0; index < len(entries) && index < correlationScanLimit; index++ {
		entry := entries[index]
		switch correlationFileStateOf(entry, now) {
		case correlationLive:
			live = append(live, correlationFile{name: entry.Name(), modified: entry.ModTime()})
		case correlationStale:
			if err := os.Remove(filepath.Join(s.dir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("remove stale correlation: %w", err)
			}
		case correlationOther:
		}
	}
	return live, nil
}

// list reads at most correlationScanLimit entries of the store directory.
func (s correlationStore) list() ([]os.FileInfo, error) {
	dir, err := os.Open(s.dir)
	if err != nil {
		return nil, fmt.Errorf("open correlation store: %w", err)
	}
	entries, readErr := dir.Readdir(correlationScanLimit + 1)
	closeErr := dir.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, errors.Join(fmt.Errorf("read correlation store: %w", readErr), closeErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close correlation store: %w", closeErr)
	}
	if len(entries) > correlationScanLimit {
		return nil, fmt.Errorf("correlation store %s holds more than %d entries; remove what Praetor did not write there, or reset the store", s.dir, correlationScanLimit)
	}
	return entries, nil
}

// evictOldest removes the least recently written rows until one more row fits under
// MaxCorrelationEntries. Ties break by name, so the choice does not depend on the
// directory order or the file system's timestamp resolution.
func (s correlationStore) evictOldest(live []correlationFile) error {
	excess := len(live) - MaxCorrelationEntries + 1
	if excess <= 0 {
		return nil
	}
	slices.SortFunc(live, func(a, b correlationFile) int {
		if order := a.modified.Compare(b.modified); order != 0 {
			return order
		}
		return strings.Compare(a.name, b.name)
	})
	for index := 0; index < excess && index < len(live); index++ {
		if err := os.Remove(filepath.Join(s.dir, live[index].name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("evict correlation: %w", err)
		}
	}
	return nil
}

// correlationFileStateOf classifies entry. A temporary file older than the lock lease was
// left by a writer killed mid-write: no live writer outlives the lease.
func correlationFileStateOf(entry os.FileInfo, now time.Time) correlationFileState {
	name := entry.Name()
	switch {
	case strings.HasPrefix(name, util.AtomicTempPrefix):
		if entry.ModTime().Before(now.Add(-correlationLockTTL)) {
			return correlationStale
		}
		return correlationOther
	case filepath.Ext(name) != ".json":
		return correlationOther
	case correlationExpired(entry, now):
		return correlationStale
	}
	return correlationLive
}

func correlationExpired(entry os.FileInfo, now time.Time) bool {
	ttl := correlationActiveTTL
	if strings.HasPrefix(entry.Name(), "pending-") {
		ttl = correlationPendingTTL
	}
	return entry.ModTime().Before(now.Add(-ttl))
}

func (s correlationStore) write(name string, entry correlationEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode correlation: %w", err)
	}
	path, err := util.ConfinePath(s.dir, name)
	if err != nil {
		return fmt.Errorf("resolve correlation row: %w", err)
	}
	if err := util.WriteFileAtomic(path, append(data, '\n'), util.SecureFilePerm); err != nil {
		return fmt.Errorf("write correlation row: %w", err)
	}
	return nil
}

// readActive reads one active agent row. A missing row is errNoCorrelation; any other
// failure stays a store error.
func (s correlationStore) readActive(name string) (correlationEntry, error) {
	entry, err := s.read(name)
	if errors.Is(err, os.ErrNotExist) {
		return correlationEntry{}, fmt.Errorf("%w: %w", errNoCorrelation, err)
	}
	if err != nil {
		return correlationEntry{}, fmt.Errorf("read agent correlation: %w", err)
	}
	return entry, nil
}

func (s correlationStore) read(name string) (correlationEntry, error) {
	data, err := util.ReadConfinedLimited(s.dir, name, correlationFileMaxBytes)
	if err != nil {
		return correlationEntry{}, err
	}
	var entry correlationEntry
	if err := json.Unmarshal(data, &entry); err != nil || entry.CreatedAt <= 0 || !validCorrelationDigest(entry.HandbackDigest) ||
		!validCorrelationDigest(entry.LaunchDigest) {
		return correlationEntry{}, errors.New("invalid correlation record")
	}
	return entry, nil
}

func correlationHandbackDigest(toolID, text string) string {
	digest := sha256.Sum256([]byte(toolID + "\x00" + text))
	return hex.EncodeToString(digest[:])
}

// correlationLaunchDigest is the LaunchDigest of a rewritten prompt; empty for none.
func correlationLaunchDigest(prompt string) string {
	if prompt == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(digest[:])
}

func validCorrelationDigest(value string) bool {
	if value == "" {
		return true
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func correlationName(kind, client, session, identifier string) (string, error) {
	for _, value := range []string{client, session, identifier} {
		if value == "" || len(value) > MaxCorrelationIDBytes {
			return "", errors.New("correlation identifiers must be bounded nonempty text")
		}
	}
	digest := sha256.Sum256([]byte(client + "\x00" + session + "\x00" + identifier))
	return kind + "-" + hex.EncodeToString(digest[:]) + ".json", nil
}
