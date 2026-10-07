package hypertext

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"html/template"
	"net/url"
	"strings"

	"github.com/crhntr/jsonschema/examples/issues/internal/database"
	"github.com/crhntr/jsonschema/examples/issues/internal/model"
)

// Dashboard is the home page.
type Dashboard struct {
	Layout
	Counts []database.CountIssuesByStatusRow
	Recent []database.ListIssuesRow
}

// Home shows issue counts by status and the most recent open issues.
func (s *Server) Home(ctx context.Context) (Dashboard, error) {
	layout, err := s.layout(ctx, "home")
	if err != nil {
		return Dashboard{}, err
	}
	counts, err := s.q.CountIssuesByStatus(ctx)
	if err != nil {
		return Dashboard{}, s.publish(err)
	}
	issues, err := s.q.ListIssues(ctx)
	if err != nil {
		return Dashboard{}, s.publish(err)
	}
	var recent []database.ListIssuesRow
	for _, issue := range issues {
		if issue.Status != "closed" && len(recent) < 5 {
			recent = append(recent, issue)
		}
	}
	return Dashboard{Layout: layout, Counts: counts, Recent: recent}, nil
}

// IssueList is the issues page.
type IssueList struct {
	Layout
	Issues []database.ListIssuesRow
}

// Issues lists every issue, open ones first.
func (s *Server) Issues(ctx context.Context) (IssueList, error) {
	layout, err := s.layout(ctx, "issues")
	if err != nil {
		return IssueList{}, err
	}
	issues, err := s.q.ListIssues(ctx)
	return IssueList{Layout: layout, Issues: issues}, s.publish(err)
}

// IssuePage shows one issue and the form that edits it.
type IssuePage struct {
	FormPage
	Issue       database.Issue
	Doc         model.Issue
	Assignee    *database.Contact
	Environment string
	Tags        []Tag
}

// Tag is an issue tag with the color the settings give it.
type Tag struct {
	Name, Color string
}

// Issue shows an issue with its edit form.
func (s *Server) Issue(ctx context.Context, id int64) (IssuePage, error) {
	issue, err := s.q.GetIssue(ctx, id)
	if err != nil {
		return IssuePage{}, s.publish(err)
	}
	var doc model.Issue
	if err := json.Unmarshal([]byte(issue.Body), &doc); err != nil {
		return IssuePage{}, s.publish(fmt.Errorf("decode issue %d: %w", id, err))
	}
	page, err := s.formPage(ctx, s.issueSpec(issue))
	if err != nil {
		return IssuePage{}, err
	}
	settings, err := s.settings(ctx)
	if err != nil {
		return IssuePage{}, err
	}
	p := IssuePage{FormPage: page, Issue: issue, Doc: doc, Environment: environment(doc.Environment)}
	for _, tag := range doc.Tags {
		p.Tags = append(p.Tags, Tag{Name: tag, Color: settings.Labels[tag]})
	}
	if issue.Assignee.Valid {
		contact, err := s.q.GetContactByEmail(ctx, issue.Assignee.String)
		switch {
		case err == nil:
			p.Assignee = &contact
		case !errors.Is(err, sql.ErrNoRows):
			return IssuePage{}, s.publish(err)
		}
	}
	return p, nil
}

// NewIssue shows an empty issue form, starting from the default severity.
func (s *Server) NewIssue(ctx context.Context) (FormPage, error) {
	spec, err := s.newIssueSpec(ctx)
	if err != nil {
		return FormPage{}, err
	}
	return s.formPage(ctx, spec)
}

// SubmitNewIssue handles posts of the new issue form.
func (s *Server) SubmitNewIssue(ctx context.Context, form url.Values) (FormResponse, error) {
	spec, err := s.newIssueSpec(ctx)
	if err != nil {
		return FormResponse{}, err
	}
	return s.submit(ctx, spec, form)
}

// ValidateNewIssue checks the new issue form as it is typed.
func (s *Server) ValidateNewIssue(ctx context.Context, form url.Values) (template.HTML, error) {
	spec, err := s.newIssueSpec(ctx)
	if err != nil {
		return "", err
	}
	return s.validate(ctx, spec, form)
}

// SubmitIssue handles posts of an issue's edit form.
func (s *Server) SubmitIssue(ctx context.Context, id int64, form url.Values) (FormResponse, error) {
	issue, err := s.q.GetIssue(ctx, id)
	if err != nil {
		return FormResponse{}, s.publish(err)
	}
	return s.submit(ctx, s.issueSpec(issue), form)
}

// ValidateIssue checks an issue's edit form as it is typed.
func (s *Server) ValidateIssue(ctx context.Context, id int64, form url.Values) (template.HTML, error) {
	issue, err := s.q.GetIssue(ctx, id)
	if err != nil {
		return "", s.publish(err)
	}
	return s.validate(ctx, s.issueSpec(issue), form)
}

// DeleteIssue deletes an issue.
func (s *Server) DeleteIssue(ctx context.Context, id int64) error {
	n, err := s.q.DeleteIssue(ctx, id)
	if err == nil && n == 0 {
		err = sql.ErrNoRows
	}
	return s.publish(err)
}

func (s *Server) newIssueSpec(ctx context.Context) (formSpec, error) {
	settings, err := s.settings(ctx)
	if err != nil {
		return formSpec{}, err
	}
	start, err := json.Marshal(map[string]string{"severity": deref(settings.DefaultSeverity, "medium"), "status": "open"})
	if err != nil {
		return formSpec{}, s.publish(err)
	}
	return formSpec{
		title:    "New issue",
		section:  "issues",
		handler:  s.issueForm,
		value:    start,
		action:   "/issues/new/form",
		validate: "/issues/new/form/validate",
		save: func(ctx context.Context, v jsontext.Value) (string, error) {
			doc, err := decodeIssue(v)
			if err != nil {
				return "", err
			}
			id, err := s.q.CreateIssue(ctx, database.CreateIssueParams{
				Title:    doc.Title,
				Severity: doc.Severity,
				Status:   doc.Status,
				Assignee: nullString(doc.Assignee),
				Body:     string(v),
			})
			return idPath("/issues", id), err
		},
	}, nil
}

func (s *Server) issueSpec(issue database.Issue) formSpec {
	path := idPath("/issues", issue.ID)
	return formSpec{
		title:    "Edit issue",
		section:  "issues",
		handler:  s.issueForm,
		value:    jsontext.Value(issue.Body),
		action:   path + "/form",
		validate: path + "/form/validate",
		save: func(ctx context.Context, v jsontext.Value) (string, error) {
			doc, err := decodeIssue(v)
			if err != nil {
				return "", err
			}
			return path, s.q.UpdateIssue(ctx, database.UpdateIssueParams{
				ID:       issue.ID,
				Title:    doc.Title,
				Severity: doc.Severity,
				Status:   doc.Status,
				Assignee: nullString(doc.Assignee),
				Body:     string(v),
			})
		},
	}
}

// decodeIssue reads a validated issue into the type jsch generated from
// the schema, for the columns that are stored on their own.
func decodeIssue(v jsontext.Value) (model.Issue, error) {
	var doc model.Issue
	if err := json.Unmarshal(v, &doc); err != nil {
		return model.Issue{}, fmt.Errorf("decode issue: %w", err)
	}
	return doc, nil
}

// environment describes where an issue happens, using the generated
// Browser and Server types for the two variants.
func environment(v *any) string {
	if v == nil {
		return ""
	}
	raw, err := json.Marshal(*v)
	if err != nil {
		return ""
	}
	var browser model.Browser
	if json.Unmarshal(raw, &browser) == nil && browser.Kind == "browser" {
		return strings.TrimSpace(browser.Name + " " + deref(browser.Version, ""))
	}
	var server model.Server
	if json.Unmarshal(raw, &server) == nil && server.Kind == "server" {
		return strings.TrimSpace(server.Os + " " + deref(server.Arch, ""))
	}
	return ""
}

func deref(p *string, otherwise string) string {
	if p == nil || *p == "" {
		return otherwise
	}
	return *p
}
