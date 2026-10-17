// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package compiler

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
	// ModelTier is set when the front matter is complete except for its model tier, the case
	// a caller can name a remedy for (#1426).
	ModelTier bool
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
		if problem, tier := personaFrontMatterProblemKind(data); problem != "" {
			problems = append(problems, PersonaFrontMatterProblem{Rel: CanonicalAgentsRel + "/" + names[i], Problem: problem, ModelTier: tier})
		}
	}
	return problems, nil
}

// ErrPersonaModelTier marks a persona whose front matter is complete except for its model tier:
// the one refusal adoption may get past when the persona is an unedited copy of a template it is
// about to replace with the tiered one (priorLegacyAgentSurfaces).
var ErrPersonaModelTier = errors.New("persona model tier missing or invalid")

// personaFrontMatterProblemKind says why the front matter that opens data fails, or returns "",
// and whether the problem is the model tier alone.
func personaFrontMatterProblemKind(data []byte) (string, bool) {
	front, problem := decodePersonaFront(data)
	if problem != "" {
		return problem, false
	}
	problem = personaModelProblem(front)
	return problem, problem != ""
}

// decodePersonaFront decodes the front matter that opens data and checks the keys every persona
// declares (PersonaRequiredKeys), returning the decoded map, or why it cannot.
func decodePersonaFront(data []byte) (map[string]any, string) {
	block, opened, err := frontMatterBlock(data)
	if err != nil {
		return nil, err.Error()
	}
	if !opened {
		return nil, "opens with no front matter; a persona starts with a --- block declaring " +
			strings.Join(PersonaRequiredKeys, " and ")
	}
	var front map[string]any
	if err := util.DecodeYAMLDocument(block, &front, util.YAMLDocumentOptions{AllowEmpty: true}); err != nil {
		return nil, "front matter is not valid YAML: " + err.Error()
	}
	var missing []string
	for _, key := range PersonaRequiredKeys {
		if value, ok := front[key].(string); !ok || strings.TrimSpace(value) == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return nil, "front matter lacks the required key(s) " + strings.Join(missing, ", ") + " (each a non-empty string)"
	}
	return front, ""
}

// PersonaLacksModelTier reports whether data is a persona whose front matter decodes and holds
// every required key but no model key at all, the one gap adoption repairs in a persona the
// repository edited (#1426). A model value outside the tiers is the repository's own and is not
// reported here.
func PersonaLacksModelTier(data []byte) bool {
	front, problem := decodePersonaFront(data)
	if problem != "" {
		return false
	}
	_, declared := front[PersonaModelKey]
	return !declared
}

// PersonaModelTier returns the model tier the persona data declares, or an error naming why it
// declares none of the allowed tiers.
func PersonaModelTier(data []byte) (string, error) {
	front, problem := decodePersonaFront(data)
	if problem == "" {
		problem = personaModelProblem(front)
	}
	if problem != "" {
		return "", errors.New(problem)
	}
	tier, ok := front[PersonaModelKey].(string)
	if !ok {
		return "", errors.New("front matter model is not a string")
	}
	return strings.TrimSpace(tier), nil
}

// AddPersonaModelTier returns data with the line "model: <tier>" added as the last line of its
// front matter, in data's own line-ending style, and every other byte unchanged. It reports false,
// and returns nil, when data opens no closed front matter or mixes line endings, since then the
// line has no place that keeps the rest intact.
func AddPersonaModelTier(data []byte, tier string) ([]byte, bool) {
	text, crlf, err := util.NormalizeLineEndingsStrict(string(data))
	if err != nil {
		return nil, false
	}
	lines := strings.Split(text, "\n")
	end, opened := util.FrontMatterEnd(lines, maxFrontMatterLines)
	if !opened || end == 0 {
		return nil, false
	}
	out := slices.Concat(lines[:end-1], []string{PersonaModelKey + ": " + tier}, lines[end-1:])
	return []byte(util.RestoreLineEndings(strings.Join(out, "\n"), crlf)), true
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
