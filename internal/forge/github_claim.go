// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/cordanaLLM/praetor/internal/util"
)

// MaxIssueCommentsLimit bounds the comments one claim operation reads from an issue (HISS-02).
// An issue with more comments fails closed: a claim comment beyond the bound could hold the
// issue unseen.
const (
	MaxIssueCommentsLimit = 1000
	maxIssueCommentPages  = MaxIssueCommentsLimit / issuesPerPage
)

var _ ClaimForge = (*GitHubDriver)(nil)

type ghCommentRaw struct {
	ID   int64 `json:"id"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	Association string `json:"author_association"`
	Body        string `json:"body"`
}

func (r ghCommentRaw) comment() IssueComment {
	return IssueComment{ID: r.ID, Author: r.User.Login, Association: r.Association, Body: r.Body}
}

// Viewer returns the login of the account the token authenticates.
func (g *GitHubDriver) Viewer(ctx context.Context) (string, error) {
	body, status, err := g.sendRequest(ctx, http.MethodGet, "/user", nil)
	if err != nil {
		return "", fmt.Errorf("failed reading the authenticated account: %w", err)
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d reading the authenticated account: %s", status, util.BodyPreview(body))
	}
	var raw struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(body, &raw); err != nil || raw.Login == "" {
		return "", fmt.Errorf("authenticated account has no login (raw: %q)", util.BodyPreview(body))
	}
	return raw.Login, nil
}

// ListIssueComments returns the comments of an issue in creation order, following pages up
// to MaxIssueCommentsLimit. A longer thread is an error, never a truncated listing.
func (g *GitHubDriver) ListIssueComments(ctx context.Context, number int) ([]IssueComment, error) {
	if number <= 0 {
		return nil, fmt.Errorf("list comments: issue number must be positive, got %d", number)
	}
	base, err := g.repoPath(fmt.Sprintf("issues/%d/comments", number))
	if err != nil {
		return nil, fmt.Errorf("list comments of issue #%d: %w", number, err)
	}
	var all []IssueComment
	err = g.walkPages(ctx, base, "", fmt.Sprintf("comments of issue #%d", number), maxIssueCommentPages, func(body []byte) (int, bool, error) {
		var raw []ghCommentRaw
		if err := json.Unmarshal(body, &raw); err != nil {
			return 0, false, fmt.Errorf("failed parsing comments of issue #%d (raw: %q): %w", number, util.BodyPreview(body), err)
		}
		for _, r := range raw {
			all = append(all, r.comment())
		}
		return len(raw), false, nil
	})
	if errors.Is(err, errPageCeiling) {
		return nil, fmt.Errorf("issue #%d has more than %d comments: %w", number, MaxIssueCommentsLimit, err)
	}
	if err != nil {
		return nil, err
	}
	return all, nil
}

// CreateIssueComment posts a comment on an issue.
func (g *GitHubDriver) CreateIssueComment(ctx context.Context, number int, body string) (IssueComment, error) {
	if number <= 0 {
		return IssueComment{}, fmt.Errorf("create comment: issue number must be positive, got %d", number)
	}
	path, err := g.repoPath(fmt.Sprintf("issues/%d/comments", number))
	if err != nil {
		return IssueComment{}, fmt.Errorf("create comment on issue #%d: %w", number, err)
	}
	resp, status, err := g.sendRequest(ctx, http.MethodPost, path, map[string]string{"body": body})
	if err != nil {
		return IssueComment{}, fmt.Errorf("failed commenting on issue #%d: %w", number, err)
	}
	if status != http.StatusCreated {
		return IssueComment{}, fmt.Errorf("unexpected status %d commenting on issue #%d: %s", status, number, util.BodyPreview(resp))
	}
	var raw ghCommentRaw
	if err := json.Unmarshal(resp, &raw); err != nil || raw.ID == 0 {
		return IssueComment{}, fmt.Errorf("comment on issue #%d came back without an id (raw: %q)", number, util.BodyPreview(resp))
	}
	return raw.comment(), nil
}

// EditIssueComment replaces the body of an existing issue comment.
func (g *GitHubDriver) EditIssueComment(ctx context.Context, commentID int64, body string) error {
	if commentID <= 0 {
		return fmt.Errorf("edit comment: comment id must be positive, got %d", commentID)
	}
	path, err := g.repoPath(fmt.Sprintf("issues/comments/%d", commentID))
	if err != nil {
		return fmt.Errorf("edit comment %d: %w", commentID, err)
	}
	resp, status, err := g.sendRequest(ctx, http.MethodPatch, path, map[string]string{"body": body})
	if err != nil {
		return fmt.Errorf("failed editing comment %d: %w", commentID, err)
	}
	if status != http.StatusOK {
		return fmt.Errorf("unexpected status %d editing comment %d: %s", status, commentID, util.BodyPreview(resp))
	}
	return nil
}

// EnsureLabel creates the label when the repository has none of that name. An existing label
// is left exactly as it is: its colour and description belong to whoever made it.
func (g *GitHubDriver) EnsureLabel(ctx context.Context, label Label) error {
	if label.Name == "" {
		return errors.New("ensure label: label name cannot be empty")
	}
	path, err := g.repoPath("labels/" + url.PathEscape(label.Name))
	if err != nil {
		return fmt.Errorf("ensure label %s: %w", label.Name, err)
	}
	body, status, err := g.sendRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return fmt.Errorf("failed reading label %s: %w", label.Name, err)
	}
	switch status {
	case http.StatusOK:
		return nil
	case http.StatusNotFound:
		return g.createLabel(ctx, label.Name, map[string]string{"name": label.Name, "color": label.Color, "description": label.Description})
	}
	return fmt.Errorf("unexpected status %d reading label %s: %s", status, label.Name, util.BodyPreview(body))
}

// AddAssignees assigns accounts to an issue without removing the ones it has.
func (g *GitHubDriver) AddAssignees(ctx context.Context, number int, logins []string) error {
	if number <= 0 {
		return fmt.Errorf("add assignees: issue number must be positive, got %d", number)
	}
	if len(logins) == 0 {
		return errors.New("add assignees: login set cannot be empty")
	}
	path, err := g.repoPath(fmt.Sprintf("issues/%d/assignees", number))
	if err != nil {
		return fmt.Errorf("add assignees to issue #%d: %w", number, err)
	}
	body, status, err := g.sendRequest(ctx, http.MethodPost, path, map[string]any{"assignees": logins})
	if err != nil {
		return fmt.Errorf("failed assigning issue #%d: %w", number, err)
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return fmt.Errorf("unexpected status %d assigning issue #%d: %s", status, number, util.BodyPreview(body))
	}
	return nil
}
