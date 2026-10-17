// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// PrivateNamesFileName is the optional per-user operator file holding protected names.
	PrivateNamesFileName = "private-names.txt"
	// maxPrivateNamesBytes bounds reading private-names.txt (HISS-02).
	maxPrivateNamesBytes = 65536
	// maxGuardedTexts bounds the number of texts checked in one guard call (HISS-02).
	maxGuardedTexts = 32
	// maxPrivateNamesCount bounds the number of private names checked (HISS-02).
	maxPrivateNamesCount = 1000
)

var (
	// ErrProtectedText is the base error returned when public forge text carries a protected value.
	ErrProtectedText = errors.New("forge text contains protected value")

	// ErrTooManyGuardedTexts is returned when the number of texts passed to Check exceeds maxGuardedTexts.
	ErrTooManyGuardedTexts = errors.New("guard: too many texts to check")
	// ErrTooManyPrivateNames is returned when private-names.txt contains more than maxPrivateNamesCount entries.
	ErrTooManyPrivateNames = errors.New("guard: too many private names")
	// ErrPrivateNamesFile is returned when private-names.txt exists but cannot be read or exceeds byte limit.
	ErrPrivateNamesFile = errors.New("guard: failed to read private names file")

	globalNoPrivateNamesOnce sync.Once
)

// ProtectedTextError names the kind of protected value found in public forge text without revealing the value.
type ProtectedTextError struct {
	Kind string
}

func (e *ProtectedTextError) Error() string {
	return fmt.Sprintf("forge text contains protected %s", e.Kind)
}

func (e *ProtectedTextError) Is(target error) bool {
	return target == ErrProtectedText
}

type protectedIdentityKey struct{}

// ProtectedIdentity carries the session and lane of the current claim operation.
type ProtectedIdentity struct {
	Session string
	Lane    string
}

// WithProtectedIdentity binds session and lane to ctx for public text guarding.
func WithProtectedIdentity(ctx context.Context, session, lane string) context.Context {
	return context.WithValue(ctx, protectedIdentityKey{}, ProtectedIdentity{Session: session, Lane: lane})
}

func protectedIdentityFromContext(ctx context.Context) (ProtectedIdentity, bool) {
	if ctx == nil {
		return ProtectedIdentity{}, false
	}
	id, ok := ctx.Value(protectedIdentityKey{}).(ProtectedIdentity)
	return id, ok
}

// PublicTextGuard inspects outgoing forge text to prevent protected identities from leaking.
type PublicTextGuard struct {
	mu               sync.Mutex
	Session          string
	Lane             string
	hostname         string
	privateNamesPath string
	stderr           io.Writer
	stderrOnce       *sync.Once
	privateNames     []string
	loadErr          error
	namesLoaded      bool
}

// NewPublicTextGuard creates a guard checking against privateNamesPath (or the default if empty).
func NewPublicTextGuard(privateNamesPath string) *PublicTextGuard {
	return &PublicTextGuard{
		privateNamesPath: privateNamesPath,
	}
}

var defaultGuardInstance = &PublicTextGuard{}

// DefaultPublicTextGuard returns the singleton default public text guard.
func DefaultPublicTextGuard() *PublicTextGuard {
	return defaultGuardInstance
}

// SetStderr overrides the destination and once-trigger for missing private-names reporting.
func (g *PublicTextGuard) SetStderr(w io.Writer, once *sync.Once) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stderr = w
	g.stderrOnce = once
}

// SetHostname overrides the detected hostname for testing.
func (g *PublicTextGuard) SetHostname(h string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.hostname = h
}

// SetPrivateNames overrides the loaded private names for testing.
func (g *PublicTextGuard) SetPrivateNames(names []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.privateNames = names
	g.namesLoaded = true
	g.loadErr = nil
}

// Check inspects texts and returns a ProtectedTextError if any text carries a protected value,
// or an error if input limits are exceeded or the private names file failed to load.
func (g *PublicTextGuard) Check(ctx context.Context, texts ...string) error {
	limit := len(texts)
	if limit > maxGuardedTexts {
		return fmt.Errorf("%w: count %d exceeds limit %d", ErrTooManyGuardedTexts, limit, maxGuardedTexts)
	}
	names, err := g.getPrivateNames()
	if err != nil {
		return err
	}
	session, lane := g.resolveIdentity(ctx)
	hostname := g.resolveHostname()

	for i := 0; i < len(texts) && i < maxGuardedTexts; i++ {
		text := texts[i]
		if text == "" {
			continue
		}
		if err := checkProtectedText(text, session, lane, hostname, names); err != nil {
			return err
		}
	}
	return nil
}

func (g *PublicTextGuard) resolveIdentity(ctx context.Context) (string, string) {
	session, lane := g.Session, g.Lane
	if id, ok := protectedIdentityFromContext(ctx); ok {
		if id.Session != "" {
			session = id.Session
		}
		if id.Lane != "" {
			lane = id.Lane
		}
	}
	return session, lane
}

func (g *PublicTextGuard) resolveHostname() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.hostname != "" {
		return g.hostname
	}
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(h)
}

func (g *PublicTextGuard) getPrivateNames() ([]string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.namesLoaded {
		return g.privateNames, g.loadErr
	}
	g.privateNames, g.loadErr = g.loadPrivateNames()
	g.namesLoaded = true
	return g.privateNames, g.loadErr
}

func (g *PublicTextGuard) loadPrivateNames() ([]string, error) {
	path := g.privateNamesPath
	if path == "" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("%w: resolve user config dir: %w", ErrPrivateNamesFile, err)
		}
		path = filepath.Join(configDir, ClaimKeyDirName, PrivateNamesFileName)
	}
	data, err := util.ReadFileLimited(path, maxPrivateNamesBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			g.reportNoNames()
			return nil, nil
		}
		return nil, fmt.Errorf("%w: %s: %w", ErrPrivateNamesFile, path, err)
	}
	return parsePrivateNames(data, g.reportNoNames)
}

func parsePrivateNames(data []byte, onEmpty func()) ([]string, error) {
	var names []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for i := 0; i < maxPrivateNamesBytes && scanner.Scan(); i++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(names) >= maxPrivateNamesCount {
			return nil, fmt.Errorf("%w: count exceeds limit %d", ErrTooManyPrivateNames, maxPrivateNamesCount)
		}
		names = append(names, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%w: scan: %w", ErrPrivateNamesFile, err)
	}
	if len(names) == 0 {
		onEmpty()
		return nil, nil
	}
	return names, nil
}

func (g *PublicTextGuard) reportNoNames() {
	once := g.stderrOnce
	if once == nil {
		once = &globalNoPrivateNamesOnce
	}
	out := g.stderr
	if out == nil {
		out = os.Stderr
	}
	once.Do(func() {
		if _, err := fmt.Fprintln(out, "praetor: public text guard read no private names"); err != nil {
			return
		}
	})
}

func checkProtectedText(text, session, lane, hostname string, names []string) error {
	if session != "" && containsWord(text, session) {
		return &ProtectedTextError{Kind: "session"}
	}
	if lane != "" && containsWord(text, lane) {
		return &ProtectedTextError{Kind: "lane"}
	}
	if hostname != "" && containsWord(text, hostname) {
		return &ProtectedTextError{Kind: "hostname"}
	}
	limit := len(names)
	if limit > maxPrivateNamesCount {
		limit = maxPrivateNamesCount
	}
	for i := 0; i < limit; i++ {
		if containsWord(text, names[i]) {
			return &ProtectedTextError{Kind: "private name"}
		}
	}
	return nil
}

// containsWord reports whether word occurs in text as an isolated token on word boundaries.
// Matching is case-insensitive.
func containsWord(text, word string) bool {
	if word == "" || len(text) < len(word) {
		return false
	}
	target := strings.ToLower(word)
	lower := strings.ToLower(text)
	wLen := len(target)
	start := 0
	maxAttempts := len(lower) + 1
	for attempts := 0; attempts < maxAttempts && start <= len(lower)-wLen; attempts++ {
		idx := strings.Index(lower[start:], target)
		if idx < 0 {
			break
		}
		pos := start + idx
		if isWordBoundary(lower, pos, wLen) {
			return true
		}
		start = pos + 1
	}
	return false
}

func isWordBoundary(s string, pos, wLen int) bool {
	if pos > 0 {
		prev := rune(s[pos-1])
		first := rune(s[pos])
		if isWordRune(prev) && isWordRune(first) {
			return false
		}
	}
	end := pos + wLen
	if end < len(s) {
		last := rune(s[end-1])
		next := rune(s[end])
		if isWordRune(last) && isWordRune(next) {
			return false
		}
	}
	return true
}

func isWordRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_'
}
