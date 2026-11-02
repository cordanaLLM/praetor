// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// harnessTurnEndIntro opens the turn-end verification block in the head band.
	harnessTurnEndIntro = "Before concluding any turn:\n"
	// adoptedCheckLegendPrefix opens the managed legend line above a generated invariant table.
	adoptedCheckLegendPrefix = "Adopted check = "
	// generatedInvariantsHeader is the header row of the generated invariant table.
	generatedInvariantsHeader = "| Invariant | Rule | Adopted check | On fail |"
	// legacyGeneratedInvariantsHeader is the header row earlier adoption versions generated
	// before the Adopted-check legend and column rename.
	legacyGeneratedInvariantsHeader = "| Invariant | Rule | Enforcement | On fail |"
	// harnessOperationalHeading opens the operational rules section after the invariant table.
	harnessOperationalHeading = "## Operational Rules"
)

// repositoryVerifyParagraph returns the repository-owned verify-all paragraph between the
// turn-end code block and the invariant heading when the repository owns the verify-all target
// and wrote its own wording outside the generated summary and receipt block.
func repositoryVerifyParagraph(head, harness string) string {
	if !strings.Contains(harness, repositoryOwnedVerifySummary) {
		return ""
	}
	intro := strings.Index(head, harnessTurnEndIntro)
	if intro < 0 {
		return ""
	}
	fenceEnd := footerEnd(head, intro)
	if fenceEnd < 0 {
		return ""
	}
	invRel := headingLineOffset(head[fenceEnd:], hisscatalog.GatedInvariantsHeading)
	if invRel < 0 {
		return ""
	}
	para := strings.TrimSpace(head[fenceEnd : fenceEnd+invRel])
	if para == "" || isGeneratedVerifyParagraph(para) {
		return ""
	}
	return para
}

// isGeneratedVerifyParagraph reports whether para matches the template text of a generated verify paragraph.
func isGeneratedVerifyParagraph(para string) bool {
	return strings.Contains(para, strings.TrimSpace(harnessReceiptLine)) ||
		strings.HasPrefix(para, "`"+verifyCommand+"` = repository-owned gate;") ||
		strings.HasPrefix(para, "`"+verifyCommand+"` = repository gate.") ||
		strings.Contains(para, "Ed25519 Exit-0 receipt")
}

// replaceVerifyParagraph replaces the generated verify paragraph in harness with verifyParagraph.
func replaceVerifyParagraph(harness, verifyParagraph string) string {
	if verifyParagraph == "" {
		return harness
	}
	return strings.Replace(harness, repositoryOwnedVerifySummary+harnessReceiptLine, verifyParagraph+"\n\n", 1)
}

// repositoryInvariantsSection returns head's invariant section (from its GatedInvariantsHeading
// line up to Operational Rules) when it carries invariant rows and is not the generated
// Adopted-check table.
func repositoryInvariantsSection(head string) (string, bool, error) {
	invStart := headingLineOffset(head, hisscatalog.GatedInvariantsHeading)
	if invStart < 0 {
		return "", false, nil
	}
	opRel := headingLineOffset(head[invStart:], harnessOperationalHeading)
	if opRel < 0 {
		return "", false, nil
	}
	section := strings.TrimSpace(head[invStart : invStart+opRel])
	if isGeneratedInvariantsSection(section) {
		return "", false, nil
	}
	rows, err := hisscatalog.InvariantTableRows(section)
	if err != nil {
		return "", false, fmt.Errorf("read the invariant table of %s: %w", agentsFile, err)
	}
	if len(rows) == 0 {
		return "", false, nil
	}
	return section, true, nil
}

// isGeneratedInvariantsSection reports whether section matches the markers of a generated invariants section.
func isGeneratedInvariantsSection(section string) bool {
	return strings.Contains(section, generatedInvariantsHeader) ||
		strings.Contains(section, legacyGeneratedInvariantsHeader) ||
		strings.Contains(section, adoptedCheckLegendPrefix)
}

// replaceInvariantsSection replaces the invariants section in harness with the provided section.
func replaceInvariantsSection(harness, section string) string {
	invStart := strings.Index(harness, hisscatalog.GatedInvariantsHeading)
	opStart := strings.Index(harness, harnessOperationalHeading)
	if invStart < 0 || opStart <= invStart {
		return harness
	}
	return harness[:invStart] + section + "\n\n" + harness[opStart:]
}

// repositoryConfigBand splices harness's rendered register block into head's config band
// (<!-- praetor:config --> .. <!-- praetor:tail -->) and returns the spliced band when head
// carries repository text outside the register markers (such as a social or register override).
func repositoryConfigBand(head, harness string) (string, error) {
	cfgStart, tailStart := strings.Index(head, agentcontext.BandConfigMarker), strings.Index(head, agentcontext.BandTailMarker)
	hCfgStart, hTailStart := strings.Index(harness, agentcontext.BandConfigMarker), strings.Index(harness, agentcontext.BandTailMarker)
	if cfgStart < 0 || tailStart <= cfgStart || hCfgStart < 0 || hTailStart <= hCfgStart {
		return "", nil
	}
	existingCfg := head[cfgStart:tailStart]
	if strings.TrimSpace(strings.TrimPrefix(existingCfg, agentcontext.BandConfigMarker)) == "" {
		return "", nil
	}
	generatedCfg := harness[hCfgStart:hTailStart]
	regStart := strings.Index(generatedCfg, config.RegisterBlockStart)
	regEnd := strings.Index(generatedCfg, config.RegisterBlockEnd)
	if regStart < 0 || regEnd < regStart {
		return "", nil
	}
	block := generatedCfg[regStart : regEnd+len(config.RegisterBlockEnd)]
	spliced, _, err := compiler.SpliceRegisterBlock(existingCfg, block)
	if err != nil {
		return "", fmt.Errorf("%s: text register block: %w", agentsFile, err)
	}
	if spliced == generatedCfg {
		return "", nil
	}
	return spliced, nil
}

// replaceConfigBand replaces the config band in harness with configBand.
func replaceConfigBand(harness, configBand string) string {
	if configBand == "" {
		return harness
	}
	hCfgStart, hTailStart := strings.Index(harness, agentcontext.BandConfigMarker), strings.Index(harness, agentcontext.BandTailMarker)
	if hCfgStart < 0 || hTailStart <= hCfgStart {
		return harness
	}
	return harness[:hCfgStart] + configBand + harness[hTailStart:]
}

// headingLineOffset returns the byte offset of the first line outside fenced code whose trimmed
// text starts with prefix, or -1 when none exists within maxHarnessLines.
func headingLineOffset(text, prefix string) int {
	lines := strings.SplitN(text, "\n", maxHarnessLines)
	var fence util.MarkdownFence
	offset := 0
	for i := 0; i < len(lines) && i < maxHarnessLines; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if !fence.Inside(trimmed) && strings.HasPrefix(trimmed, prefix) {
			return offset
		}
		offset += len(lines[i]) + 1
	}
	return -1
}
