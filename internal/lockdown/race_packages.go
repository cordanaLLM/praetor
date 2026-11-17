// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package lockdown

import (
	"errors"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// NoAffectedGoPackage is the explicit reason for an empty pre-push Go race scope.
const NoAffectedGoPackage = "no affected Go package (--race-packages=none)"

// RaceStageName is the race stage name in the signed receipt format.
const RaceStageName = "Race-Detector Tests"

// RacePackagesLine binds the explicit hook selection into the signed gate output header.
func RacePackagesLine(packages []string) string {
	return "race_packages\t" + util.FormatRacePackages(packages)
}

// RacePackagesReason identifies exactly what the scoped stage was asked to execute.
func RacePackagesReason(packages []string) string {
	return "race packages: " + util.FormatRacePackages(packages) + "; affected Go packages supplied explicitly"
}

type raceEvidence struct {
	selection string
	status    string
	reason    string
	seenStage bool
	seenRace  bool
}

// checkRaceOutput validates scoped evidence through the shared pinned and unpinned verifiers.
// Historical full runs carry no scope header. A not-applicable race stage always needs a reason.
func checkRaceOutput(output string) error {
	_, err := readRaceEvidence(output)
	return err
}

// ReceiptRaceStageStatus reads validated race evidence; an absent historical stage returns an empty status.
// This reader does not verify the receipt's signature or require race execution.
func ReceiptRaceStageStatus(output string) (string, error) {
	evidence, err := readRaceEvidence(output)
	if err != nil {
		return "", err
	}
	return evidence.status, nil
}

// readRaceEvidence shares bounded parsing and validation between receipt verification and reuse.
func readRaceEvidence(output string) (raceEvidence, error) {
	var evidence raceEvidence
	lines := strings.Split(output, "\n")
	if len(lines) > 256 {
		return evidence, errors.New("gate output exceeds 256 lines")
	}
	for i := 0; i < len(lines) && i < 256; i++ {
		if err := evidence.readLine(lines[i]); err != nil {
			return evidence, err
		}
	}
	return evidence, checkRaceEvidence(evidence)
}

// readLine parses the unique race header and stage while preserving their signed order.
func (e *raceEvidence) readLine(line string) error {
	if strings.HasPrefix(line, "race_packages\t") {
		if e.selection != "" || e.seenStage {
			return errors.New("race scope header duplicated or after stages")
		}
		e.selection = strings.TrimPrefix(line, "race_packages\t")
		if _, err := util.ParseRacePackages(e.selection); err != nil {
			return fmt.Errorf("invalid certified race scope: %w", err)
		}
	}
	e.seenStage = e.seenStage || strings.HasPrefix(line, "stage\t")
	if strings.HasPrefix(line, "stage\t"+RaceStageName+"\t") {
		if e.seenRace {
			return errors.New("duplicate certified race stage")
		}
		fields := strings.SplitN(line, "\t", 4)
		if len(fields) != 4 {
			return errors.New("malformed certified race stage")
		}
		e.status, e.reason, e.seenRace = fields[2], fields[3], true
	}
	return nil
}

// checkRaceEvidence validates scope-specific reasons without requiring that race tests executed.
func checkRaceEvidence(e raceEvidence) error {
	if e.selection == "" && !e.seenRace {
		return nil
	}
	if !e.seenRace {
		return errors.New("malformed certified race stage")
	}
	if e.selection == "" {
		return checkUnscopedRaceStage(e.status, e.reason)
	}
	if e.selection == "none" {
		return checkEmptyRaceStage(e.status, e.reason)
	}
	packages, err := util.ParseRacePackages(e.selection)
	if err != nil {
		return err
	}
	if !strings.Contains(e.reason, RacePackagesReason(packages)) {
		return errors.New("certified race stage reason does not match its explicit package scope")
	}
	return nil
}

func checkEmptyRaceStage(status, reason string) error {
	if (status == "not_applicable" || status == "skipped") && strings.Contains(reason, NoAffectedGoPackage) {
		return nil
	}
	return errors.New("empty race scope requires not_applicable with no affected Go package reason")
}

func checkUnscopedRaceStage(status, reason string) error {
	if status == "not_applicable" && strings.TrimSpace(reason) == "" {
		return errors.New("certified not_applicable race stage requires an explicit reason")
	}
	return nil
}
