// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor

import (
	"context"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/gomanifest"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/templates"
)

const (
	govulnModule      = "golang.org/x/vuln"
	govulnToolPackage = govulnModule + "/cmd/govulncheck"
	gosecModule       = "github.com/securego/gosec/v2"
	gosecToolPackage  = gosecModule + "/cmd/gosec"
)

// GosecInstallGuide is the one install advice for a missing gosec (flavor tool lists, the gate's
// security stage). A go install of gosec at any version, @latest included, resolves the
// golang.org/x/tools its own go.mod names, below templates.GoSecurityXToolsFloor; only a module
// that requires the floor raises it, so the advice names the tools module.
const GosecInstallGuide = "go install -modfile=" + gomanifest.ToolModuleFile + " " + gosecToolPackage +
	" (the tools module requires " + gosecModule + " " + templates.GoSecurityGosecVersion +
	" and golang.org/x/tools " + templates.GoSecurityXToolsVersion +
	" or later; a bare go install of gosec builds an x/tools too old for the go1.27.2 export data)"

// securityWorkflowFacts selects the install form for the Security Scan workflow. A tools
// module that declares both scanners remains the adopter's version source; every other repository
// gets the template's pinned throwaway module, including the common case with no tools module.
func securityWorkflowFacts(ctx context.Context, repoPath string) (templates.Context, string) {
	facts := templates.Context{}
	if err := ctx.Err(); err != nil {
		return facts, fmt.Sprintf("cannot inspect %s: %v", gomanifest.ToolModuleFile, err)
	}
	data, err := util.ReadConfinedLimited(repoPath, gomanifest.ToolModuleFile, maxSettingBytes)
	if err == nil {
		facts.GoSecurity.DeclaredScanners = declaresSecurityScanners(data)
	}
	return facts, ""
}

func declaresSecurityScanners(data []byte) bool {
	return gomanifest.DeclaresTool(data, govulnModule, govulnToolPackage) &&
		gomanifest.DeclaresTool(data, gosecModule, gosecToolPackage)
}
