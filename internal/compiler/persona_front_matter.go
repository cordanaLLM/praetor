// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package compiler

import (
	"context"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// PersonaRequiredKeys are the front matter keys every canonical persona declares, each as a
// non-empty string: the agent clients the personas are projected into name a subagent by name
// and choose it by description, and the personas adoption writes carry both.
var PersonaRequiredKeys = []string{"name", "description"}

const (
	// PersonaModelKey is the canonical front matter key naming an agent's model tier.
	PersonaModelKey = "model"
	// PersonaModelFlash is the tier for mechanical runs, fuzzing, dogfooding, and packaging.
	PersonaModelFlash = "flash"
	// PersonaModelPro is the tier for review and complex reasoning.
	PersonaModelPro = "pro"
	// PersonaAllowedModels lists the allowed canonical model tiers.
	PersonaAllowedModels = "flash, pro"
)

// PersonaFrontMatterProblem names one canonical persona whose front matter fails, and why.
type PersonaFrontMatterProblem struct {
	// Rel is the persona's slash path below the repository root.
	Rel string
	// Problem says what is wrong, naming the missing key where one is missing.
	Problem string
}

// CheckPersonaFrontMatter decodes the front matter of each persona in names, the file names
// ReadAgentInventory lists, below rootDir with the strict YAML reader (util.DecodeYAMLDocument,
// as the provenance read uses) and returns every persona whose front matter is absent, does not
// close, is not YAML, or lacks a required key, in the order of names (#1280). A persona that
// cannot be read is an error.
func CheckPersonaFrontMatter(ctx context.Context, rootDir string, names []string) ([]PersonaFrontMatterProblem, error) {
	var problems []PersonaFrontMatterProblem
	for i := 0; i < len(names) && i < maxAgentProjections; i++ {
		data, err := readCanonicalAgent(ctx, rootDir, names[i])
		if err != nil {
			return nil, err
		}
		if problem := personaFrontMatterProblem(data); problem != "" {
			problems = append(problems, PersonaFrontMatterProblem{Rel: CanonicalAgentsRel + "/" + names[i], Problem: problem})
		}
	}
	return problems, nil
}

// personaFrontMatterProblem says why the front matter that opens data fails, or returns "".
func personaFrontMatterProblem(data []byte) string {
	block, opened, err := frontMatterBlock(data)
	if err != nil {
		return err.Error()
	}
	if !opened {
		return "opens with no front matter; a persona starts with a --- block declaring " +
			strings.Join(PersonaRequiredKeys, " and ")
	}
	var front map[string]any
	if err := util.DecodeYAMLDocument(block, &front, util.YAMLDocumentOptions{AllowEmpty: true}); err != nil {
		return "front matter is not valid YAML: " + err.Error()
	}
	var missing []string
	for _, key := range PersonaRequiredKeys {
		if value, ok := front[key].(string); !ok || strings.TrimSpace(value) == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return "front matter lacks the required key(s) " + strings.Join(missing, ", ") + " (each a non-empty string)"
	}
	return personaModelProblem(front)
}

// personaModelProblem validates the model tier in front matter.
func personaModelProblem(front map[string]any) string {
	modelVal, hasModel := front[PersonaModelKey].(string)
	modelVal = strings.TrimSpace(modelVal)
	if !hasModel || modelVal == "" {
		return "front matter lacks the required key \"" + PersonaModelKey + "\" (allowed values: " + PersonaAllowedModels + ")"
	}
	if modelVal != PersonaModelFlash && modelVal != PersonaModelPro {
		return fmt.Sprintf("front matter model %q is invalid (allowed values: %s)", modelVal, PersonaAllowedModels)
	}
	return ""
}
