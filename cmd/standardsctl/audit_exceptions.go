// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

// auditExceptionsExpiry warns when any declared exception in the manifest expires within
// config.ExceptionExpiryWarningDays (14 days) of today (Issue #798).
func auditExceptionsExpiry(manifest *config.Manifest, today time.Time) error {
	if manifest == nil {
		return nil
	}
	warnExceptionsExpiring(manifest.Exceptions, today)
	return nil
}

// warnExceptionsExpiring prints a warning for each entry expiring within
// config.ExceptionExpiryWarningDays.
func warnExceptionsExpiring(entries []config.Exception, today time.Time) {
	for index := 0; index < len(entries) && index < config.MaxExceptions; index++ {
		entry := entries[index]
		if entry.ExpiringWithin(today, config.ExceptionExpiryWarningDays) {
			daysLeft, err := entry.DaysLeft(today)
			if err != nil {
				continue
			}
			fmt.Printf("[WARN] Exceptions: the exceptions entry (rule %s, %s) expires on %s (%s): %s\n",
				entry.Rule, entry.Target(), entry.Expires, config.FormatDaysLeft(daysLeft), entry.Reason)
		}
	}
}
