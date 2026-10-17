// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"

	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/gating"
	apiassets "github.com/cordanaLLM/praetor/tools/apicompat"
)

// maxFlavorTemplates bounds the walk over flv.RequiredTemplates (HISS-02).
const maxFlavorTemplates = 64

// reconcileGoSecurityConfig plans .gosec.json from go/.gosec.json.tmpl whenever git tracks at
// least one go.mod in the repository, unless flv already emits it.
func (s *adoptSession) reconcileGoSecurityConfig(ctx context.Context, flv flavor.Flavor) {
	if flavorEmitsGosec(flv) {
		return
	}
	tracksGo, err := apiassets.TracksModule(ctx, s.repoPath)
	if err != nil {
		s.report.addError("discover tracked Go modules: %v", err)
		return
	}
	if !tracksGo {
		return
	}
	applied, err := flavor.ApplyTemplates(ctx, s.repoPath, "go", []flavor.TemplateItem{flavor.GosecTemplate()},
		flavor.ApplyOptions{TemplatesOnly: true, DryRun: s.opts.DryRun})
	s.recordFlavorReport(applied)
	if err != nil {
		s.report.addError("apply gosec configuration: %v", err)
	}
}

// flavorEmitsGosec reports whether flv declares the pinned gosec configuration template.
func flavorEmitsGosec(flv flavor.Flavor) bool {
	if flv == nil {
		return false
	}
	tmpls := flv.RequiredTemplates()
	for i := 0; i < len(tmpls) && i < maxFlavorTemplates; i++ {
		if tmpls[i].Path == gating.GosecConfigFile {
			return true
		}
	}
	return false
}
