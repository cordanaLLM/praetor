// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// ownReleaseConfigs reuses the release-e2e inventory and bounded, no-symlink snapshot reader.
// A manifest without a configuration still requires a configuration declaring the skip setting.
func ownReleaseConfigs(ctx context.Context, root string) (map[string][]byte, error) {
	configs := map[string][]byte{}
	manifest := ""
	for i := 0; i < len(releasePleaseConfigPaths); i++ {
		path := releasePleaseConfigPaths[i]
		data, exists, err := contextopt.ObserveSnapshotIn(ctx, root, filepath.FromSlash(path))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		if !exists {
			continue
		}
		if strings.HasSuffix(path, "manifest.json") {
			manifest = path
		} else {
			configs[path] = data
		}
	}
	if len(configs) == 0 && manifest != "" {
		configs[manifest] = nil
	}
	return configs, nil
}
