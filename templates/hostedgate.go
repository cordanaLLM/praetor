// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package templates

import (
	"text/template"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

// The hosted gate functions render the trigger and draft handling every CI workflow flavor apply
// scaffolds shares with the hosted gates adoption emits: the shape internal/ghworkflow/hostedgate.go
// defines once (HISS-19), which ghworkflow.HostedGateFault and the HISS-18 workflow trigger audit
// read (#817). A body calls them instead of spelling the shape out, so the three cannot drift:
//
//   - hostedGateOn is the whole 'on' block (ghworkflow.HostedGateOn): pull_request on the
//     activity types that can change what merges, ready_for_review among them, and push on the
//     default branch only. Placed on a line of its own, it is followed by one blank line.
//   - hostedGateDraftStep is a job's first step (ghworkflow.HostedGateDraftStep), at step
//     indentation: it fails a draft with the annotation the checkpoint planner reads as draft
//     pending. Placed on a line of its own, it is followed by one blank line.
//   - hostedGateStepIf is the condition every later step carries (ghworkflow.HostedGateStepIf),
//     led by its line break: a body appends it to the step's name line.
const (
	hostedGateOnFunc        = "hostedGateOn"
	hostedGateDraftStepFunc = "hostedGateDraftStep"
	hostedGateStepIfFunc    = "hostedGateStepIf"
)

// hostedGateFuncs is the function map execute hands every template.
var hostedGateFuncs = template.FuncMap{
	hostedGateOnFunc:        func() string { return ghworkflow.HostedGateOn },
	hostedGateDraftStepFunc: func() string { return ghworkflow.HostedGateDraftStep },
	hostedGateStepIfFunc:    func() string { return ghworkflow.HostedGateStepIf },
}
