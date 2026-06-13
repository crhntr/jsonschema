package generate

import (
	"strings"
	"testing"

	"github.com/crhntr/jsonschema"
)

func deriveObject(t *testing.T, src string) (Type, error) {
	t.Helper()
	s, err := jsonschema.Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	obj, ok := s.TypeObject()
	if !ok {
		t.Fatalf("root schema is not an object")
	}
	return Derive("Model", &obj)
}

// TestDerive_GoTypeDisallowedImport rejects a property goType whose
// declared goImports entry is outside the allowed set (stdlib,
// encoding/json/v2, golang.org/x/*). Without validation the bad import
// would slip through to goimports and pull an arbitrary package into
// the generated file.
func TestDerive_GoTypeDisallowedImport(t *testing.T) {
	src := `{
		"type": "object",
		"properties": {
			"when": {"type": "string", "goType": "bar.Baz", "goImports": ["github.com/foo/bar"]}
		}
	}`
	_, err := deriveObject(t, src)
	if err == nil {
		t.Fatalf("Derive: err = nil, want error for disallowed goImports")
	}
	if !strings.Contains(err.Error(), "allowed set") {
		t.Errorf("Derive: err = %v, want mention of allowed set", err)
	}
}

// TestDerive_GoTypeLocalTypeAllowed guards the relaxation that lets a
// goType reference a locally generated or companion type by bare
// identifier without declaring goImports. Such identifiers cannot be
// resolved against a loaded package, so they must pass through rather
// than be rejected as unknown.
func TestDerive_GoTypeLocalTypeAllowed(t *testing.T) {
	src := `{
		"type": "object",
		"properties": {
			"tags": {"type": "array", "goType": "[]Tag"}
		}
	}`
	if _, err := deriveObject(t, src); err != nil {
		t.Fatalf("Derive: err = %v, want nil for local-type goType", err)
	}
}

// TestDeriveStructShape_BooleanOverrideGoTypeDisallowedImport rejects
// a goType override on a boolean-schema property when its goImports
// fall outside the allowed set. This path is only reachable through
// the sidecar overrides mechanism (parentAnnot.Fields), so the test
// calls deriveStructShape directly with a constructed override.
func TestDeriveStructShape_BooleanOverrideGoTypeDisallowedImport(t *testing.T) {
	s, err := jsonschema.Parse([]byte(`{"type":"object","properties":{"flag":true}}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	obj, ok := s.TypeObject()
	if !ok {
		t.Fatalf("root schema is not an object")
	}
	parentAnnot := Annotations{
		Fields: map[string]Annotations{
			"flag": {GoType: "bar.Baz", GoImports: []string{"github.com/foo/bar"}},
		},
	}
	_, err = deriveStructShape("Model", &obj, refMaps{}, parentAnnot)
	if err == nil {
		t.Fatalf("deriveStructShape: err = nil, want error for disallowed goImports")
	}
	if !strings.Contains(err.Error(), "allowed set") {
		t.Errorf("deriveStructShape: err = %v, want mention of allowed set", err)
	}
}

// TestDerive_GoAdditionalFieldDisallowedImport rejects a
// goAdditionalFields entry whose goType selector references a package
// outside the allowed set. The additional-field goType is validated
// against the parent schema's goImports, the same allowlist the
// property goType uses.
func TestDerive_GoAdditionalFieldDisallowedImport(t *testing.T) {
	src := `{
		"type": "object",
		"goImports": ["github.com/foo/bar"],
		"goAdditionalFields": [{"goIdent": "Extra", "goType": "bar.Baz"}]
	}`
	_, err := deriveObject(t, src)
	if err == nil {
		t.Fatalf("Derive: err = nil, want error for disallowed goImports")
	}
	if !strings.Contains(err.Error(), "allowed set") {
		t.Errorf("Derive: err = %v, want mention of allowed set", err)
	}
}
