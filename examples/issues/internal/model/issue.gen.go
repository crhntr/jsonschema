package model

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
)

type Issue struct {
	Assignee    *string  `json:"assignee,omitzero"`
	Description *string  `json:"description,omitzero"`
	Due         *string  `json:"due,omitzero"`
	Environment *any     `json:"environment,omitzero"`
	Severity    string   `json:"severity"`
	Status      string   `json:"status"`
	Steps       []Step   `json:"steps,omitzero"`
	Tags        []string `json:"tags,omitzero"`
	Title       string   `json:"title"`
}

func (r Issue) MarshalJSONTo(enc *jsontext.Encoder) error {
	type alias Issue
	return json.MarshalEncode(enc, alias(r))
}
func (r *Issue) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var shadow struct {
		Assignee    *string  `json:"assignee"`
		Description *string  `json:"description"`
		Due         *string  `json:"due"`
		Environment *any     `json:"environment"`
		Severity    *string  `json:"severity"`
		Status      *string  `json:"status"`
		Steps       []Step   `json:"steps"`
		Tags        []string `json:"tags"`
		Title       *string  `json:"title"`
	}
	opts := []json.Options{json.RejectUnknownMembers(true)}
	if err := json.UnmarshalDecode(dec, &shadow, opts...); err != nil {
		return err
	}
	if shadow.Severity == nil {
		return fmt.Errorf("missing required field %q", "severity")
	}
	if shadow.Status == nil {
		return fmt.Errorf("missing required field %q", "status")
	}
	if shadow.Title == nil {
		return fmt.Errorf("missing required field %q", "title")
	}
	r.Assignee = shadow.Assignee
	r.Description = shadow.Description
	r.Due = shadow.Due
	r.Environment = shadow.Environment
	r.Severity = *shadow.Severity
	r.Status = *shadow.Status
	r.Steps = shadow.Steps
	r.Tags = shadow.Tags
	r.Title = *shadow.Title
	return nil
}

var (
	_ json.MarshalerTo     = (*Issue)(nil)
	_ json.UnmarshalerFrom = (*Issue)(nil)
)

type Browser struct {
	Kind    string  `json:"kind"`
	Name    string  `json:"name"`
	Version *string `json:"version,omitzero"`
}

func (r Browser) MarshalJSONTo(enc *jsontext.Encoder) error {
	type alias Browser
	return json.MarshalEncode(enc, alias(r))
}
func (r *Browser) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var shadow struct {
		Kind    *string `json:"kind"`
		Name    *string `json:"name"`
		Version *string `json:"version"`
	}
	opts := []json.Options{json.RejectUnknownMembers(true)}
	if err := json.UnmarshalDecode(dec, &shadow, opts...); err != nil {
		return err
	}
	if shadow.Kind == nil {
		return fmt.Errorf("missing required field %q", "kind")
	}
	if shadow.Name == nil {
		return fmt.Errorf("missing required field %q", "name")
	}
	r.Kind = *shadow.Kind
	r.Name = *shadow.Name
	r.Version = shadow.Version
	return nil
}

var (
	_ json.MarshalerTo     = (*Browser)(nil)
	_ json.UnmarshalerFrom = (*Browser)(nil)
)

type Server struct {
	Arch *string `json:"arch,omitzero"`
	Kind string  `json:"kind"`
	Os   string  `json:"os"`
}

func (r Server) MarshalJSONTo(enc *jsontext.Encoder) error {
	type alias Server
	return json.MarshalEncode(enc, alias(r))
}
func (r *Server) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var shadow struct {
		Arch *string `json:"arch"`
		Kind *string `json:"kind"`
		Os   *string `json:"os"`
	}
	opts := []json.Options{json.RejectUnknownMembers(true)}
	if err := json.UnmarshalDecode(dec, &shadow, opts...); err != nil {
		return err
	}
	if shadow.Kind == nil {
		return fmt.Errorf("missing required field %q", "kind")
	}
	if shadow.Os == nil {
		return fmt.Errorf("missing required field %q", "os")
	}
	r.Arch = shadow.Arch
	r.Kind = *shadow.Kind
	r.Os = *shadow.Os
	return nil
}

var (
	_ json.MarshalerTo     = (*Server)(nil)
	_ json.UnmarshalerFrom = (*Server)(nil)
)

type Step struct {
	Action   string  `json:"action"`
	Expected *string `json:"expected,omitzero"`
}

func (r Step) MarshalJSONTo(enc *jsontext.Encoder) error {
	type alias Step
	return json.MarshalEncode(enc, alias(r))
}
func (r *Step) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var shadow struct {
		Action   *string `json:"action"`
		Expected *string `json:"expected"`
	}
	opts := []json.Options{json.RejectUnknownMembers(true)}
	if err := json.UnmarshalDecode(dec, &shadow, opts...); err != nil {
		return err
	}
	if shadow.Action == nil {
		return fmt.Errorf("missing required field %q", "action")
	}
	r.Action = *shadow.Action
	r.Expected = shadow.Expected
	return nil
}

var (
	_ json.MarshalerTo     = (*Step)(nil)
	_ json.UnmarshalerFrom = (*Step)(nil)
)
