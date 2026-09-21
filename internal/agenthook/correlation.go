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
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	MaxCorrelationEntries   = 128
	MaxCorrelationIDBytes   = 1024
	correlationFileMaxBytes = 4096
	correlationLockAttempts = 40
	correlationLockDelay    = 10 * time.Millisecond
	correlationLockTTL      = 2 * time.Minute
	correlationPendingTTL   = 5 * time.Minute
	correlationActiveTTL    = 24 * time.Hour
	correlationDirRel       = "praetor/agenthook-correlations"
	correlationDirOverhead  = 3
)

type correlationEntry struct {
	Resolution        config.Resolution `json:"resolution"`
	CreatedAt         int64             `json:"created_at"`
	HandbackDigest    string            `json:"handback_digest,omitempty"`
	HandbackDelivered bool              `json:"-"`
}

type correlationStore struct {
	dir string
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

func (s correlationStore) reserve(ctx context.Context, client, session, toolID string, resolution config.Resolution) error {
	name, err := correlationName("pending", client, session, toolID)
	if err != nil {
		return err
	}
	return s.withLock(ctx, func() error {
		count, cleanupErr := s.cleanup()
		if cleanupErr != nil {
			return cleanupErr
		}
		if count >= MaxCorrelationEntries {
			return fmt.Errorf("correlation store reached %d entries", MaxCorrelationEntries)
		}
		if _, statErr := os.Lstat(filepath.Join(s.dir, name)); statErr == nil {
			return errors.New("dispatch correlation already exists")
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		return s.write(name, correlationEntry{Resolution: resolution, CreatedAt: time.Now().UTC().Unix()})
	})
}

func (s correlationStore) promote(ctx context.Context, client, session, toolID, agentID string) error {
	pending, err := correlationName("pending", client, session, toolID)
	if err != nil {
		return err
	}
	active, err := correlationName("active", client, session, agentID)
	if err != nil {
		return err
	}
	return s.withLock(ctx, func() error {
		if _, cleanupErr := s.cleanup(); cleanupErr != nil {
			return cleanupErr
		}
		_, readErr := s.read(pending)
		if readErr != nil {
			return fmt.Errorf("dispatch correlation missing: %w", readErr)
		}
		if _, statErr := os.Lstat(filepath.Join(s.dir, active)); statErr == nil {
			return errors.New("agent correlation already exists")
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		if renameErr := os.Rename(filepath.Join(s.dir, pending), filepath.Join(s.dir, active)); renameErr != nil {
			return fmt.Errorf("bind dispatch correlation: %w", renameErr)
		}
		return nil
	})
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
		entry, readErr = s.read(active)
		if readErr != nil {
			return fmt.Errorf("agent correlation missing: %w", readErr)
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
		if _, readErr := s.read(delivered); readErr == nil {
			return errors.New("subagent handback already delivered")
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return fmt.Errorf("read delivered correlation: %w", readErr)
		}
		entry, readErr := s.read(active)
		if readErr != nil {
			return fmt.Errorf("agent correlation missing: %w", readErr)
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
		if _, readErr := s.read(delivered); readErr == nil {
			return errors.New("subagent handback already delivered")
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return fmt.Errorf("read delivered correlation: %w", readErr)
		}
		entry, readErr := s.read(active)
		if readErr != nil {
			return fmt.Errorf("agent correlation missing: %w", readErr)
		}
		if entry.HandbackDigest == "" || entry.HandbackDigest != correlationHandbackDigest(toolID, text) {
			return errors.New("handback receipt does not match a validated report")
		}
		activePath, pathErr := util.ConfinePath(s.dir, active)
		if pathErr != nil {
			return pathErr
		}
		deliveredPath, pathErr := util.ConfinePath(s.dir, delivered)
		if pathErr != nil {
			return pathErr
		}
		if renameErr := os.Rename(activePath, deliveredPath); renameErr != nil {
			return fmt.Errorf("mark handback delivered: %w", renameErr)
		}
		return nil
	})
}

func (s correlationStore) cancelHandback(ctx context.Context, client, session, agentID, toolID, text string) error {
	active, err := correlationName("active", client, session, agentID)
	if err != nil {
		return err
	}
	return s.withLock(ctx, func() error {
		entry, readErr := s.read(active)
		if readErr != nil {
			return fmt.Errorf("agent correlation missing: %w", readErr)
		}
		if entry.HandbackDigest == "" || entry.HandbackDigest != correlationHandbackDigest(toolID, text) {
			return errors.New("handback abort does not match a validated report")
		}
		entry.HandbackDigest = ""
		return s.write(active, entry)
	})
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
	const lock = ".lock"
	for attempt := 0; attempt < correlationLockAttempts; attempt++ {
		file, err := root.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, util.SecureFilePerm)
		if err == nil {
			if closeErr := file.Close(); closeErr != nil {
				return errors.Join(closeErr, removeCorrelationLock(root, lock))
			}
			defer func() { resultErr = errors.Join(resultErr, removeCorrelationLock(root, lock)) }()
			return action()
		}
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("acquire correlation lock: %w", err)
		}
		if staleErr := removeStaleCorrelationLock(root, lock, time.Now().UTC()); staleErr != nil {
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
	return errors.New("correlation store remained locked")
}

func removeCorrelationLock(root *os.Root, name string) error {
	if err := root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove correlation lock: %w", err)
	}
	return nil
}

func removeStaleCorrelationLock(root *os.Root, name string, now time.Time) error {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
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

func (s correlationStore) cleanup() (int, error) {
	dir, err := os.Open(s.dir)
	if err != nil {
		return 0, err
	}
	limit := MaxCorrelationEntries + correlationDirOverhead
	entries, readErr := dir.Readdir(limit + 1)
	closeErr := dir.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return 0, errors.Join(readErr, closeErr)
	}
	if len(entries) > limit {
		return 0, errors.Join(fmt.Errorf("correlation store exceeds %d directory entries", limit), closeErr)
	}
	now, count := time.Now().UTC(), 0
	for index := 0; index < len(entries) && index < limit; index++ {
		entry := entries[index]
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		if correlationExpired(entry, now) {
			if err := os.Remove(filepath.Join(s.dir, entry.Name())); err != nil {
				return 0, errors.Join(err, closeErr)
			}
			continue
		}
		count++
	}
	if count > MaxCorrelationEntries {
		return count, errors.Join(fmt.Errorf("correlation store exceeds %d entries", MaxCorrelationEntries), closeErr)
	}
	return count, closeErr
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
		return err
	}
	return util.WriteFileAtomic(path, append(data, '\n'), util.SecureFilePerm)
}

func (s correlationStore) read(name string) (correlationEntry, error) {
	data, err := util.ReadConfinedLimited(s.dir, name, correlationFileMaxBytes)
	if err != nil {
		return correlationEntry{}, err
	}
	var entry correlationEntry
	if err := json.Unmarshal(data, &entry); err != nil || entry.CreatedAt <= 0 || !validCorrelationDigest(entry.HandbackDigest) {
		return correlationEntry{}, errors.New("invalid correlation record")
	}
	return entry, nil
}

func correlationHandbackDigest(toolID, text string) string {
	digest := sha256.Sum256([]byte(toolID + "\x00" + text))
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
