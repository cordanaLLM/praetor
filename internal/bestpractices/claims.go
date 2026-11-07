// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package bestpractices

import (
	"encoding/json/v2"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/clientjson"
)

const (
	statusSuffix        = "_status"
	justificationSuffix = "_justification"
	maxObjectMembers    = 2048
)

type statusClaim struct {
	criterion   string
	status      string
	validString bool
}

// extractClaims parses status claims and corresponding justifications from a clientjson object.
func extractClaims(object clientjson.Object) ([]statusClaim, map[string]string, error) {
	if len(object) > maxObjectMembers {
		return nil, nil, fmt.Errorf("%s has %d members (max %d)", File, len(object), maxObjectMembers)
	}
	claims := make([]statusClaim, 0, len(object)/2)
	justifications := make(map[string]string, len(object)/2)
	for i := 0; i < len(object) && i < maxObjectMembers; i++ {
		member := object[i]
		if strings.HasSuffix(member.Name, statusSuffix) {
			claims = append(claims, parseStatusMember(member))
			continue
		}
		if strings.HasSuffix(member.Name, justificationSuffix) {
			name := strings.TrimSuffix(member.Name, justificationSuffix)
			text, err := parseJustificationMember(member)
			if err != nil {
				return nil, nil, err
			}
			justifications[name] = text
		}
	}
	return claims, justifications, nil
}

// parseStatusMember parses a status claim member into a statusClaim structure.
func parseStatusMember(member clientjson.Member) statusClaim {
	criterion := strings.TrimSuffix(member.Name, statusSuffix)
	var status string
	if err := json.Unmarshal(member.Value, &status); err != nil {
		return statusClaim{
			criterion:   criterion,
			status:      strings.TrimSpace(string(member.Value)),
			validString: false,
		}
	}
	return statusClaim{criterion: criterion, status: status, validString: true}
}

// parseJustificationMember parses a justification member value into a string.
func parseJustificationMember(member clientjson.Member) (string, error) {
	var text *string
	if err := json.Unmarshal(member.Value, &text); err != nil {
		return "", fmt.Errorf("%s: %s must be a string or null: %w", File, member.Name, err)
	}
	if text == nil {
		return "", nil
	}
	return *text, nil
}
