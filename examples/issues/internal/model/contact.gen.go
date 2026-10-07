package model

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
)

type Contact struct {
	Email string  `json:"email"`
	Name  string  `json:"name"`
	Notes *string `json:"notes,omitzero"`
	Phone *string `json:"phone,omitzero"`
	Role  *string `json:"role,omitzero"`
}

func (r Contact) MarshalJSONTo(enc *jsontext.Encoder) error {
	type alias Contact
	return json.MarshalEncode(enc, alias(r))
}
func (r *Contact) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var shadow struct {
		Email *string `json:"email"`
		Name  *string `json:"name"`
		Notes *string `json:"notes"`
		Phone *string `json:"phone"`
		Role  *string `json:"role"`
	}
	opts := []json.Options{json.RejectUnknownMembers(true)}
	if err := json.UnmarshalDecode(dec, &shadow, opts...); err != nil {
		return err
	}
	if shadow.Email == nil {
		return fmt.Errorf("missing required field %q", "email")
	}
	if shadow.Name == nil {
		return fmt.Errorf("missing required field %q", "name")
	}
	r.Email = *shadow.Email
	r.Name = *shadow.Name
	r.Notes = shadow.Notes
	r.Phone = shadow.Phone
	r.Role = shadow.Role
	return nil
}

var (
	_ json.MarshalerTo     = (*Contact)(nil)
	_ json.UnmarshalerFrom = (*Contact)(nil)
)
