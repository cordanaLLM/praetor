package adopt

import (
	"context"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/buildid"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/gomanifest"
)

// toolsModuleFile is the module an adopter pins its audit engine in, the one CI runs with
// `go tool -modfile=tools/go/go.mod standardsctl`.
const toolsModuleFile = "tools/go/go.mod"

// engineToolPackage is the tool directive that makes toolsModuleFile pin the audit engine.
const engineToolPackage = gomanifest.EngineModulePath + "/cmd/standardsctl"

// reportStaleEnginePin warns when the tools module pins an audit engine other than the one
// running the adoption (#1054). The files this run refreshed are that engine's texts; an older
// pinned engine does not know them, so CI's audit refuses them ("differs from the locked Praetor
// asset") until the pin moves. The warning names the file, the pinned version, the running
// engine and the command that moves the pin with its go.sum. Nothing is checked when the
// repository has no such tools module, when it pins no engine tool, or when either version
// identifies no build.
func reportStaleEnginePin(ctx context.Context, s *adoptSession) error {
	data, exists, err := contextopt.ObserveSnapshotIn(ctx, s.repoPath, toolsModuleFile)
	if err != nil {
		return fmt.Errorf("read %s: %w", toolsModuleFile, err)
	}
	if !exists {
		return nil
	}
	pinned, ok := pinnedEngineVersion(data)
	if !ok {
		return nil
	}
	if target, stale := staleEnginePin(pinned, s.opts.Engine); stale {
		s.report.addWarning("%s pins the audit engine %s at %s, but this adoption ran engine %s and refreshed its "+
			"files: CI's audit with the pinned engine refuses them as differing from its locked assets. Move the pin in "+
			"the same change: go get -modfile=%s %s@%s", toolsModuleFile, gomanifest.EngineModulePath, pinned,
			s.opts.Engine, toolsModuleFile, gomanifest.EngineModulePath, target)
	}
	return nil
}

// pinnedEngineVersion returns the engine version a tools module requires, when it declares the
// engine tool (engineToolPackage) and requires the engine module.
func pinnedEngineVersion(data []byte) (string, bool) {
	if !declaresEngineTool(data) {
		return "", false
	}
	for _, requirement := range gomanifest.ParseManifest(data).Requires {
		if requirement.Path == gomanifest.EngineModulePath {
			return requirement.Version, true
		}
	}
	return "", false
}

// declaresEngineTool reports whether data, a go.mod, carries a tool directive for the engine.
func declaresEngineTool(data []byte) bool {
	lines := strings.Split(string(gomanifest.TrimBOM(data)), "\n")
	inBlock := false
	for i := 0; i < len(lines) && i < maxGoModLines; i++ {
		tool, ok := gomanifest.ToolLine(lines[i], &inBlock)
		if code, _, _ := strings.Cut(tool, "//"); ok && strings.TrimSpace(code) == engineToolPackage {
			return true
		}
	}
	return false
}

// maxGoModLines bounds the lines declaresEngineTool reads (HISS-02).
const maxGoModLines = 1 << 16

// staleEnginePin returns the version the pin should move to and whether pinned, a module version
// a tools module requires, names another build than running. A release is compared as a
// release and a revision as a revision; a pin or engine that identifies no build is never stale.
func staleEnginePin(pinned string, running buildid.Identity) (string, bool) {
	pin := buildid.FromModuleVersion(pinned)
	switch {
	case running.Release != "" && pin.Release != "":
		return running.Release, pin.Release != running.Release
	case running.Revision != "" && pin.Revision != "":
		return running.Revision, pin.Revision != running.Revision
	case running.Release != "" && pin.Revision != "":
		return running.Release, true
	case running.Revision != "" && pin.Release != "":
		return running.Revision, true
	}
	return "", false
}
