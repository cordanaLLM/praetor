// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package docstrings

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

// ExceptionEntry identifies the disposition of a declared exception for a counted file.
type ExceptionEntry struct {
	Path     string `json:"path"`
	Category string `json:"category"`
	Expires  string `json:"expires"`
	Status   string `json:"status"`
}

type exceptionRun struct {
	entries []config.Exception
	today   time.Time
}

// loadExceptionRun reads docstring exceptions through the shared manifest loader.
func loadExceptionRun(ctx context.Context, root string) (*exceptionRun, error) {
	entries, err := config.LoadExceptionsFor(ctx, root, config.ExceptionRuleDocstrings)
	if err != nil {
		return nil, fmt.Errorf("load docstrings exceptions: %w", err)
	}
	return &exceptionRun{entries: entries, today: time.Now().UTC()}, nil
}

// excuses records stale and expired entries and excuses only a live content-matching file.
func (r *exceptionRun) excuses(rel string, data []byte, rep *Report) bool {
	if len(r.entries) == 0 {
		return false
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	var matching []config.Exception
	for i := 0; i < len(r.entries) && i < config.MaxExceptions; i++ {
		entry := r.entries[i]
		if !entry.Matches(rel) {
			continue
		}
		if !strings.HasPrefix(hash, strings.ToLower(entry.Fingerprint)) {
			rep.recordException(entry, "stale")
			continue
		}
		matching = append(matching, entry)
	}
	live, expired := config.ExceptionFor(matching, rel, r.today, nil)
	if expired != nil {
		rep.recordException(*expired, "expired")
	}
	if live == nil {
		return false
	}
	rep.recordException(*live, "excepted")
	return true
}

// recordException appends one visible disposition, preserving the declared category and expiry.
func (r *Report) recordException(entry config.Exception, status string) {
	r.Exceptions = append(r.Exceptions, ExceptionEntry{
		Path: entry.Path, Category: entry.ExpiryCategory(), Expires: entry.Expires, Status: status,
	})
}
