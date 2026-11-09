// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
)

const (
	releaseOwnPublisherRule   = "release-own-publisher"
	maxReleasePackages        = 1024
	skipReleaseOwnNoConfig    = "[SKIP] " + releaseOwnPublisherRule + " not checked: no release-please configuration at the paths read."
	skipReleaseOwnNoPublisher = "[SKIP] " + releaseOwnPublisherRule + " not checked: no workflow under .github/workflows publishes a GitHub release."
)

// AuditOwnReleasePublisher requires release-please to leave release creation to an own publisher.
// Repositories without a release-please configuration or publishing workflow skip the check and say so.
func AuditOwnReleasePublisher(ctx context.Context, root string) (string, error) {
	if ctx == nil {
		return "", errors.New("[FAIL] release-own-publisher: the audit requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	configs, err := ownReleaseConfigs(ctx, root)
	if err != nil {
		return "", fmt.Errorf("[FAIL] %s: %w", releaseOwnPublisherRule, err)
	}
	if len(configs) == 0 {
		return skipReleaseOwnNoConfig, nil
	}
	publishers, err := forge.MeasureReleasePublishers(ctx, root)
	if err != nil {
		return "", fmt.Errorf("[FAIL] %s: %w", releaseOwnPublisherRule, err)
	}
	if len(publishers) == 0 {
		return skipReleaseOwnNoPublisher, nil
	}
	return judgeOwnReleaseConfigs(configs, publishers)
}
