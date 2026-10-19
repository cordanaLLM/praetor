// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/managedasset"
	"github.com/cordanaLLM/praetor/internal/util"
	"go.yaml.in/yaml/v3"
)

// AuditPluranoLicenceCaller requires an existing plurano REUSE workflow to be a
// single reusable workflow job pinned to a full commit SHA.
func AuditPluranoLicenceCaller(ctx context.Context, root, owner string) error {
	if !isPluranoOwner(owner) {
		return nil
	}
	data, exists, err := contextopt.ObserveSnapshotIn(ctx, root, reuseWorkflowFile)
	if err != nil {
		return fmt.Errorf("[FAIL] %s licence caller not checked: %w", reuseWorkflowFile, err)
	}
	if !exists {
		return nil
	}
	if !pluranoLicenceCaller(data) {
		return fmt.Errorf("[FAIL] %s must call uses: %s@<40-hex commit SHA> (praetorctl adopt renders uses: %s@%s); run praetorctl adopt to repair it",
			reuseWorkflowFile, forge.PluranoHoristesLicenceAction,
			forge.PluranoHoristesLicenceAction, forge.PluranoHoristesLicenceWorkflowSHA)
	}
	return nil
}

// pluranoLicenceCaller accepts only one job that calls the shared workflow at a
// full SHA. A copied gate's step-level uses cannot satisfy this check.
func pluranoLicenceCaller(data []byte) bool {
	if strings.Count(string(data), "\n") >= managedasset.MaxWorkflowLines {
		return false
	}
	var workflow struct {
		Jobs map[string]struct {
			Uses  string `yaml:"uses"`
			Steps []any  `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil || len(workflow.Jobs) != 1 {
		return false
	}
	for _, job := range workflow.Jobs {
		pin, ok := util.ParseSHAPin(job.Uses)
		return ok && pin.Action == forge.PluranoHoristesLicenceAction &&
			forge.IsFullCommitSHA(pin.SHA) && len(job.Steps) == 0
	}
	return false
}
