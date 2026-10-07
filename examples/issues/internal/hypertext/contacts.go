package hypertext

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"html/template"
	"net/url"

	"github.com/crhntr/jsonschema/examples/issues/internal/database"
	"github.com/crhntr/jsonschema/examples/issues/internal/model"
)

// ContactList is the contacts page.
type ContactList struct {
	Layout
	Contacts []database.ListContactsRow
}

// Contacts lists every contact with how many open issues they have.
func (s *Server) Contacts(ctx context.Context) (ContactList, error) {
	layout, err := s.layout(ctx, "contacts")
	if err != nil {
		return ContactList{}, err
	}
	contacts, err := s.q.ListContacts(ctx)
	return ContactList{Layout: layout, Contacts: contacts}, s.publish(err)
}

// ContactPage shows one contact, their issues, and the form that edits
// the contact.
type ContactPage struct {
	FormPage
	Contact database.Contact
	Doc     model.Contact
	Issues  []database.ListIssuesForAssigneeRow
}

// Contact shows a contact with their issues and edit form.
func (s *Server) Contact(ctx context.Context, id int64) (ContactPage, error) {
	contact, err := s.q.GetContact(ctx, id)
	if err != nil {
		return ContactPage{}, s.publish(err)
	}
	var doc model.Contact
	if err := json.Unmarshal([]byte(contact.Body), &doc); err != nil {
		return ContactPage{}, s.publish(fmt.Errorf("decode contact %d: %w", id, err))
	}
	issues, err := s.q.ListIssuesForAssignee(ctx, sql.NullString{String: contact.Email, Valid: true})
	if err != nil {
		return ContactPage{}, s.publish(err)
	}
	page, err := s.formPage(ctx, s.contactSpec(contact))
	return ContactPage{FormPage: page, Contact: contact, Doc: doc, Issues: issues}, err
}

// NewContact shows an empty contact form.
func (s *Server) NewContact(ctx context.Context) (FormPage, error) {
	return s.formPage(ctx, s.newContactSpec())
}

// SubmitNewContact handles posts of the new contact form.
func (s *Server) SubmitNewContact(ctx context.Context, form url.Values) (FormResponse, error) {
	return s.submit(ctx, s.newContactSpec(), form)
}

// ValidateNewContact checks the new contact form as it is typed.
func (s *Server) ValidateNewContact(ctx context.Context, form url.Values) (template.HTML, error) {
	return s.validate(ctx, s.newContactSpec(), form)
}

// SubmitContact handles posts of a contact's edit form.
func (s *Server) SubmitContact(ctx context.Context, id int64, form url.Values) (FormResponse, error) {
	contact, err := s.q.GetContact(ctx, id)
	if err != nil {
		return FormResponse{}, s.publish(err)
	}
	return s.submit(ctx, s.contactSpec(contact), form)
}

// ValidateContact checks a contact's edit form as it is typed.
func (s *Server) ValidateContact(ctx context.Context, id int64, form url.Values) (template.HTML, error) {
	contact, err := s.q.GetContact(ctx, id)
	if err != nil {
		return "", s.publish(err)
	}
	return s.validate(ctx, s.contactSpec(contact), form)
}

// DeleteContact deletes a contact. Their issues keep the email address.
func (s *Server) DeleteContact(ctx context.Context, id int64) error {
	n, err := s.q.DeleteContact(ctx, id)
	if err == nil && n == 0 {
		err = sql.ErrNoRows
	}
	return s.publish(err)
}

func (s *Server) newContactSpec() formSpec {
	return formSpec{
		title:    "New contact",
		section:  "contacts",
		handler:  s.contactForm,
		action:   "/contacts/new/form",
		validate: "/contacts/new/form/validate",
		save: func(ctx context.Context, v jsontext.Value) (string, error) {
			doc, err := decodeContact(v)
			if err != nil {
				return "", err
			}
			id, err := s.q.CreateContact(ctx, database.CreateContactParams{
				Name:  doc.Name,
				Email: doc.Email,
				Role:  nullString(doc.Role),
				Body:  string(v),
			})
			return idPath("/contacts", id), err
		},
	}
}

func (s *Server) contactSpec(contact database.Contact) formSpec {
	path := idPath("/contacts", contact.ID)
	return formSpec{
		title:    "Edit contact",
		section:  "contacts",
		handler:  s.contactForm,
		value:    jsontext.Value(contact.Body),
		action:   path + "/form",
		validate: path + "/form/validate",
		save: func(ctx context.Context, v jsontext.Value) (string, error) {
			doc, err := decodeContact(v)
			if err != nil {
				return "", err
			}
			return path, s.q.UpdateContact(ctx, database.UpdateContactParams{
				ID:    contact.ID,
				Name:  doc.Name,
				Email: doc.Email,
				Role:  nullString(doc.Role),
				Body:  string(v),
			})
		},
	}
}

func decodeContact(v jsontext.Value) (model.Contact, error) {
	var doc model.Contact
	if err := json.Unmarshal(v, &doc); err != nil {
		return model.Contact{}, fmt.Errorf("decode contact: %w", err)
	}
	return doc, nil
}
