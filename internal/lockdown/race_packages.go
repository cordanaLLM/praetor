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
	line      string
	seenStage bool
}

// checkRaceOutput validates scoped evidence through the shared pinned and unpinned verifiers.
// Historical full runs carry no scope header. A not-applicable race stage always needs a reason.
func checkRaceOutput(output string) error {
	lines := strings.Split(output, "\n")
	if len(lines) > 256 {
		return errors.New("gate output exceeds 256 lines")
	}
	var evidence raceEvidence
	for i := 0; i < len(lines) && i < 256; i++ {
		if err := evidence.readLine(lines[i]); err != nil {
			return err
		}
	}
	return checkRaceEvidence(evidence.selection, evidence.line)
}

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
		if e.line != "" {
			return errors.New("duplicate certified race stage")
		}
		e.line = line
	}
	return nil
}

func checkRaceEvidence(selection, line string) error {
	if selection == "" && line == "" {
		return nil
	}
	fields := strings.SplitN(line, "\t", 4)
	if len(fields) != 4 {
		return errors.New("malformed certified race stage")
	}
	if selection == "" {
		return checkUnscopedRaceStage(fields[2], fields[3])
	}
	if selection == "none" {
		return checkEmptyRaceStage(fields[2], fields[3])
	}
	packages, err := util.ParseRacePackages(selection)
	if err != nil {
		return err
	}
	if !strings.Contains(fields[3], RacePackagesReason(packages)) {
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
