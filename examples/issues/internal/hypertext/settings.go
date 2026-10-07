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

	"github.com/crhntr/jsonschema/examples/issues/internal/database"
	"github.com/crhntr/jsonschema/examples/issues/internal/model"
)

// Settings shows the settings form.
func (s *Server) Settings(ctx context.Context) (FormPage, error) {
	spec, err := s.settingsSpec(ctx)
	if err != nil {
		return FormPage{}, err
	}
	return s.formPage(ctx, spec)
}

// SubmitSettings handles posts of the settings form.
func (s *Server) SubmitSettings(ctx context.Context, form url.Values) (FormResponse, error) {
	spec, err := s.settingsSpec(ctx)
	if err != nil {
		return FormResponse{}, err
	}
	return s.submit(ctx, spec, form)
}

// ValidateSettings checks the settings form as it is typed.
func (s *Server) ValidateSettings(ctx context.Context, form url.Values) (template.HTML, error) {
	spec, err := s.settingsSpec(ctx)
	if err != nil {
		return "", err
	}
	return s.validate(ctx, spec, form)
}

func (s *Server) settingsSpec(ctx context.Context) (formSpec, error) {
	spec := formSpec{
		title:    "Settings",
		section:  "settings",
		handler:  s.settingsForm,
		action:   "/settings/form",
		validate: "/settings/form/validate",
		save: func(ctx context.Context, v jsontext.Value) (string, error) {
			var settings model.Settings
			if err := json.Unmarshal(v, &settings); err != nil {
				return "", fmt.Errorf("decode settings: %w", err)
			}
			return "/", s.q.SaveSettings(ctx, database.SaveSettingsParams{Project: settings.Project, Body: string(v)})
		},
	}
	row, err := s.q.GetSettings(ctx)
	switch {
	case err == nil:
		spec.value = jsontext.Value(row.Body)
	case !errors.Is(err, sql.ErrNoRows):
		return formSpec{}, s.publish(err)
	}
	return spec, nil
}
