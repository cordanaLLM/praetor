// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package forgetest holds the in-memory forge the claim tests of several packages share, so
// the claim protocol is exercised against one fake rather than one copy per package.
package forgetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
)

// ClaimFake is an in-memory forge for one repository that implements forge.ClaimForge.
// FailOn names a method that fails with an error.
type ClaimFake struct {
	mu        sync.Mutex
	PR        bool
	Comments  []forge.IssueComment
	NextID    int64
	Labels    map[string]bool // labels the repository has
	OnIssue   map[string]bool // labels on the issue
	Assignees []string
	FailOn    string
	Calls     []string
	// BeforeList runs before ListIssueComments answers, to stage a concurrent writer.
	BeforeList func(f *ClaimFake, call int)
	// AfterList runs after ListIssueComments captures comments, before answering, to stage a concurrent writer.
	AfterList func(f *ClaimFake, call int)
	listCalls int
}

var _ forge.ClaimForge = (*ClaimFake)(nil)

// NewClaimFake returns an empty fake issue.
func NewClaimFake() *ClaimFake {
	return &ClaimFake{NextID: 100, Labels: map[string]bool{}, OnIssue: map[string]bool{}}
}

// Opener returns an opener that serves this fake for every repository.
func (f *ClaimFake) Opener() forge.ClaimForgeOpener {
	return func(string, string) (forge.ClaimForge, error) { return f, nil }
}

func (f *ClaimFake) record(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, name)
	if f.FailOn == name {
		return fmt.Errorf("fake forge: %s failed", name)
	}
	return nil
}

// GetIssue answers an issue, or fails when PR marks the number as a pull request.
func (f *ClaimFake) GetIssue(_ context.Context, number int) (forge.IssueSpec, error) {
	if err := f.record("GetIssue"); err != nil {
		return forge.IssueSpec{}, err
	}
	if f.PR {
		return forge.IssueSpec{}, errors.New("fake forge: number is a pull request")
	}
	return forge.IssueSpec{ID: number}, nil
}

// Viewer answers the login "operator".
func (f *ClaimFake) Viewer(context.Context) (string, error) {
	if err := f.record("Viewer"); err != nil {
		return "", err
	}
	return "operator", nil
}

// ListIssueComments answers a copy of the comments.
func (f *ClaimFake) ListIssueComments(context.Context, int) ([]forge.IssueComment, error) {
	f.listCalls++
	if f.BeforeList != nil {
		f.BeforeList(f, f.listCalls)
	}
	if err := f.record("ListIssueComments"); err != nil {
		return nil, err
	}
	comments := append([]forge.IssueComment(nil), f.Comments...)
	if f.AfterList != nil {
		f.AfterList(f, f.listCalls)
	}
	return comments, nil
}

// CreateIssueComment appends a comment authored by an OWNER.
func (f *ClaimFake) CreateIssueComment(_ context.Context, _ int, body string) (forge.IssueComment, error) {
	if err := f.record("CreateIssueComment"); err != nil {
		return forge.IssueComment{}, err
	}
	f.NextID++
	c := forge.IssueComment{ID: f.NextID, Author: "operator", Association: "OWNER", Body: body}
	f.Comments = append(f.Comments, c)
	return c, nil
}

// EditIssueComment replaces the body of a comment.
func (f *ClaimFake) EditIssueComment(_ context.Context, id int64, body string) error {
	if err := f.record("EditIssueComment"); err != nil {
		return err
	}
	for i := range f.Comments {
		if f.Comments[i].ID == id {
			f.Comments[i].Body = body
			return nil
		}
	}
	return fmt.Errorf("fake forge: no comment %d", id)
}

// EnsureLabel creates the label on the repository.
func (f *ClaimFake) EnsureLabel(_ context.Context, l forge.Label) error {
	if err := f.record("EnsureLabel"); err != nil {
		return err
	}
	f.Labels[l.Name] = true
	return nil
}

// AddLabels adds labels to the issue; a label the repository lacks is an error, as on GitHub.
func (f *ClaimFake) AddLabels(_ context.Context, _ int, labels []string) error {
	if err := f.record("AddLabels"); err != nil {
		return err
	}
	for _, l := range labels {
		if !f.Labels[l] {
			return fmt.Errorf("fake forge: label %s does not exist", l)
		}
		f.OnIssue[l] = true
	}
	return nil
}

// RemoveLabel removes a label from the issue.
func (f *ClaimFake) RemoveLabel(_ context.Context, _ int, label string) error {
	if err := f.record("RemoveLabel"); err != nil {
		return err
	}
	delete(f.OnIssue, label)
	return nil
}

// AddAssignees records the assigned logins.
func (f *ClaimFake) AddAssignees(_ context.Context, _ int, logins []string) error {
	if err := f.record("AddAssignees"); err != nil {
		return err
	}
	f.Assignees = append(f.Assignees, logins...)
	return nil
}

// Seed adds a comment holding claim c, written by an author with the given association, and
// returns its id.
func (f *ClaimFake) Seed(tb testing.TB, c forge.Claim, association string) int64 {
	tb.Helper()
	marker, err := forge.RenderClaimMarker(c)
	if err != nil {
		tb.Fatalf("seed claim: %v", err)
	}
	f.NextID++
	f.Comments = append(f.Comments, forge.IssueComment{ID: f.NextID, Author: "someone", Association: association, Body: marker + "\nhuman text\n"})
	return f.NextID
}
