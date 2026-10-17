package forge

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/util"
)

// maxCollaboratorPages bounds the collaborator listing (HISS-02): ten pages of issuesPerPage
// collaborators is far past the one-maintainer question WriteCollaborators answers.
const maxCollaboratorPages = 10

// ghCollaborator is the part of one collaborator entry WriteCollaborators reads.
type ghCollaborator struct {
	Permissions struct {
		Admin    bool `json:"admin"`
		Maintain bool `json:"maintain"`
		Push     bool `json:"push"`
	} `json:"permissions"`
}

// WriteCollaborators returns how many collaborators of the repository can push to it, outside
// collaborators and organisation members included (affiliation=all). They are the accounts that
// can approve a pull request, so a ruleset asking for more approvals than this minus the author
// cannot be satisfied (#1075).
func (g *GitHubDriver) WriteCollaborators(ctx context.Context) (int, error) {
	base, err := g.repoPath("collaborators")
	if err != nil {
		return 0, err
	}
	writers := 0
	err = g.walkPages(ctx, base, "affiliation=all&", "repository collaborators", maxCollaboratorPages, func(body []byte) (int, bool, error) {
		var collaborators []ghCollaborator
		if err := json.Unmarshal(body, &collaborators); err != nil {
			return 0, false, fmt.Errorf("failed parsing repository collaborators (raw: %q): %w", util.BodyPreview(body), err)
		}
		if len(collaborators) > issuesPerPage {
			return 0, false, fmt.Errorf("collaborator response exceeds %d entries", issuesPerPage)
		}
		for i := 0; i < len(collaborators); i++ {
			if p := collaborators[i].Permissions; p.Push || p.Maintain || p.Admin {
				writers++
			}
		}
		return len(collaborators), false, nil
	})
	if err != nil {
		return 0, err
	}
	return writers, nil
}
