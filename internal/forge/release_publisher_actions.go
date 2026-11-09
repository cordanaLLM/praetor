// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"fmt"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

// releaseActions create or publish a GitHub release whenever they run.
var releaseActions = []string{
	"jellysin/release-helper/actions/publish", "softprops/action-gh-release", "ncipollo/release-action", "actions/create-release",
}

// goreleaserRootFlags are the arguments GoReleaser's root command runs alone instead of release.
var goreleaserRootFlags = []string{"-h", "--help", "-v", "--version"}

// actionPublishesRelease reports whether step's action creates or publishes a GitHub release.
func actionPublishesRelease(step ghworkflow.Step) (bool, error) {
	action := actionPath(step.Uses)
	if slices.Contains(releaseActions, action) {
		return true, nil
	}
	if action == goreleaserActionPath {
		return goreleaserPublishes(step.With)
	}
	return false, nil
}

// goreleaserPublishes reads the install-only and args inputs of goreleaser-action. GoReleaser
// runs release when no subcommand is given, except for a lone help or version flag, and release
// publishes unless --snapshot or a --skip list holding publish is set (cmd/root.go
// shouldPrependRelease and cmd/release.go of goreleaser/goreleaser at 2c336825258e, read
// 2026-11-05).
func goreleaserPublishes(with map[string]any) (bool, error) {
	if fmt.Sprint(with["install-only"]) == "true" {
		return false, nil
	}
	var fields []string
	if args, ok := with["args"].(string); ok {
		fields = strings.Fields(args)
	}
	if len(fields) > maxRunScriptFields {
		return false, fmt.Errorf("goreleaser args exceed %d fields", maxRunScriptFields)
	}
	if len(fields) == 1 && slices.Contains(goreleaserRootFlags, fields[0]) {
		return false, nil
	}
	if len(fields) > 0 && !strings.HasPrefix(fields[0], "-") && fields[0] != "release" {
		return false, nil
	}
	skips, err := goreleaserSkipsPublish(fields)
	if err != nil {
		return false, err
	}
	return !skips, nil
}

// goreleaserSkipsPublish reports a --snapshot flag or a --skip list, as =value or as the next
// argument, that names publish. It returns an error when fields exceed maxRunScriptFields.
func goreleaserSkipsPublish(fields []string) (bool, error) {
	if len(fields) > maxRunScriptFields {
		return false, fmt.Errorf("goreleaser args exceed %d fields", maxRunScriptFields)
	}
	for i := 0; i < len(fields) && i < maxRunScriptFields; i++ {
		if fields[i] == "--snapshot" || fields[i] == "--snapshot=true" {
			return true, nil
		}
		value, ok := strings.CutPrefix(fields[i], "--skip=")
		if !ok && fields[i] == "--skip" && i+1 < len(fields) {
			value, ok = fields[i+1], true
		}
		if ok && slices.Contains(strings.Split(value, ","), "publish") {
			return true, nil
		}
	}
	return false, nil
}
