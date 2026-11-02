// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hisscatalog"
)

func TestHarnessFooterDoesNotPromiseApplicationTests(t *testing.T) {
	for _, status := range []string{verificationNotApplicable, verificationPreserved, verificationDeclared, verificationUnavailable} {
		t.Run(status, func(t *testing.T) {
			plan := &VerificationPlan{Status: status, Build: [][]string{}, Test: [][]string{}}
			facts := adoptedFacts("acme", "widget", "framework", plan)
			facts.pipelines = hisscatalog.PipelineVerifyAll
			harness, err := buildAgentHarness(facts)
			if err != nil {
				t.Fatal(err)
			}
			footer := harness[strings.Index(harness, harnessFooterHeading):]
			if strings.Contains(footer, "Fast local test suite") {
				t.Error("footer promises a test suite for unverified or inapplicable commands")
			}
			if !strings.Contains(footer, "Project verification commands; availability and execution unverified") {
				t.Error("footer must identify commands as declarations, with availability and execution unverified")
			}
		})
	}
}
