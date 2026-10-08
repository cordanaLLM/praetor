// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	_ "embed" // engineLauncherScript is the launcher text written into adopted repositories.
)

// engineLauncherFile is the launcher every governance job of the generated lefthook.yml and the
// fallback pre-commit hook run (lefthookGovernedCommand). It chooses the engine that judges the
// repository: the one the repository pins with PRAETOR_REF in a workflow under .github/workflows,
// installed once per pin into a cache keyed by the pin, and only where the repository pins none the
// binary on PATH (#906). Without it a hook judged a repository pinned to an older engine by
// whatever newer binary came first on PATH.
const engineLauncherFile = ".config/lefthook/engine.sh"

// engineLauncherScript is the launcher text. It is a POSIX shell script because a hook must
// resolve its engine before any Praetor binary runs, and it runs under Git Bash on Windows.
// engine_launcher_test.go runs it against stub binaries.
//
//go:embed engine_launcher.sh
var engineLauncherScript string

// priorEngineLauncherDigests are the digests (priorRendering) of every text a Praetor release wrote
// at engineLauncherFile, keyed to what produced it; the current text is one of them. Audit does not
// read the launcher, so --force keeps an edited copy: these texts are what adoption refreshes to
// the current one without --force, in the file's own line-ending style.
// TestPriorEngineLauncherDigests_Boundary_CurrentTextRecorded fails until a changed launcher is
// recorded here, so the next release still refreshes it.
var priorEngineLauncherDigests = map[string]string{
	"918eae4d632b02621b6727d942e1b90199987f19fb6d91aa47c4ed86d0fae44e": "engine pinned by PRAETOR_REF (#906)",
}

// reconcileEngineLauncher scaffolds the launcher beside the lefthook.yml jobs that run it. An
// existing copy that is the current text is verified, an unedited earlier text is refreshed, and
// an edited one is the repository's and is kept with a warning, --force included.
func reconcileEngineLauncher(ctx context.Context, s *adoptSession) error {
	_, err := s.scaffoldFile(ctx, scaffold{
		rel:       engineLauncherFile,
		perm:      filePerm,
		content:   []byte(engineLauncherScript),
		created:   "Installed the hook engine launcher: hooks run the engine PRAETOR_REF pins, cached per pin, and the binary on PATH only where no pin is declared",
		verified:  "Existing hook engine launcher verified present",
		refreshed: "Refreshed an unedited earlier Praetor hook engine launcher to the current text",
		prior:     priorEngineLauncherDigests,
	})
	return err
}
