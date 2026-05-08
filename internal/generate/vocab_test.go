package generate

import (
	"encoding/json/jsontext"
	"reflect"
	"testing"
)

func TestParseAnnotations(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra map[string]jsontext.Value
		want  Annotations
	}{
		{
			name:  "empty",
			extra: nil,
			want:  Annotations{},
		},
		{
			name: "goIdent",
			extra: map[string]jsontext.Value{
				"goIdent": jsontext.Value(`"MyType"`),
			},
			want: Annotations{GoIdent: "MyType"},
		},
		{
			name: "goType with goImports",
			extra: map[string]jsontext.Value{
				"goType":    jsontext.Value(`"*big.Rat"`),
				"goImports": jsontext.Value(`["math/big"]`),
			},
			want: Annotations{GoType: "*big.Rat", GoImports: []string{"math/big"}},
		},
		{
			name: "all fields",
			extra: map[string]jsontext.Value{
				"goIdent":      jsontext.Value(`"User"`),
				"goType":       jsontext.Value(`"time.Time"`),
				"goImports":    jsontext.Value(`["time"]`),
				"goDoc":        jsontext.Value(`"a user record"`),
				"mapKeyType":   jsontext.Value(`"string"`),
				"mapValueType": jsontext.Value(`"int"`),
				"goJSONTags":   jsontext.Value(`["omitempty","format:RFC3339"]`),
				"goAdditionalFields": jsontext.Value(`[
					{"goIdent": "resolved", "goType": "*Schema"},
					{"goType": "schemaResolution"}
				]`),
			},
			want: Annotations{
				GoIdent:      "User",
				GoType:       "time.Time",
				GoImports:    []string{"time"},
				GoDoc:        "a user record",
				MapKeyType:   "string",
				MapValueType: "int",
				GoJSONTags:   []string{"omitempty", "format:RFC3339"},
				GoAdditionalFields: []GoAdditionalField{
					{GoIdent: stringList{"resolved"}, GoType: "*Schema"},
					{GoType: "schemaResolution"},
				},
			},
		},
		{
			name: "additional fields with goIdent array",
			extra: map[string]jsontext.Value{
				"goAdditionalFields": jsontext.Value(`[
					{"goIdent": ["isBool", "isObject"], "goType": "bool"}
				]`),
			},
			want: Annotations{
				GoAdditionalFields: []GoAdditionalField{
					{GoIdent: stringList{"isBool", "isObject"}, GoType: "bool"},
				},
			},
		},
		{
			name: "ignores unknown keys",
			extra: map[string]jsontext.Value{
				"goIdent":     jsontext.Value(`"X"`),
				"x-vendor":    jsontext.Value(`"ignored"`),
				"description": jsontext.Value(`"not a vocab key"`),
			},
			want: Annotations{GoIdent: "X"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseAnnotations(tc.extra)
			if err != nil {
				t.Fatalf("ParseAnnotations(%v) error = %v, want nil", tc.extra, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParseAnnotations(%v) = %+v, want %+v", tc.extra, got, tc.want)
			}
		})
	}
}

func TestParseAnnotations_TypeError(t *testing.T) {
	extra := map[string]jsontext.Value{
		"goIdent": jsontext.Value(`123`),
	}
	if _, err := ParseAnnotations(extra); err == nil {
		t.Errorf("ParseAnnotations(%v) error = nil, want non-nil", extra)
	}
}
