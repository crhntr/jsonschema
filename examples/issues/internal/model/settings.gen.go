package model

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
)

type Settings struct {
	DefaultSeverity *string           `json:"defaultSeverity,omitzero"`
	Labels          map[string]string `json:"labels,omitzero"`
	Notifications   *any              `json:"notifications,omitzero"`
	Project         string            `json:"project"`
}

func (r Settings) MarshalJSONTo(enc *jsontext.Encoder) error {
	type alias Settings
	return json.MarshalEncode(enc, alias(r))
}
func (r *Settings) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var shadow struct {
		DefaultSeverity *string           `json:"defaultSeverity"`
		Labels          map[string]string `json:"labels"`
		Notifications   *any              `json:"notifications"`
		Project         *string           `json:"project"`
	}
	opts := []json.Options{json.RejectUnknownMembers(true)}
	if err := json.UnmarshalDecode(dec, &shadow, opts...); err != nil {
		return err
	}
	if shadow.Project == nil {
		return fmt.Errorf("missing required field %q", "project")
	}
	r.DefaultSeverity = shadow.DefaultSeverity
	r.Labels = shadow.Labels
	r.Notifications = shadow.Notifications
	r.Project = *shadow.Project
	return nil
}

var (
	_ json.MarshalerTo     = (*Settings)(nil)
	_ json.UnmarshalerFrom = (*Settings)(nil)
)
