package jsonschema

import (
	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
)

// Dependency represents a single entry of the legacy dependencies
// keyword: either a list of required properties or a subschema.
type Dependency struct {
	required []string
	schema   *Schema
}

func (d *Dependency) Required() ([]string, bool) { return d.required, d.required != nil }
func (d *Dependency) Schema() *Schema            { return d.schema }

func (d *Dependency) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	switch dec.PeekKind() {
	case jsontext.KindBeginArray:
		var req []string
		if err := json.UnmarshalDecode(dec, &req); err != nil {
			return err
		}
		d.required = req
		return nil
	default:
		var m Schema
		if err := json.UnmarshalDecode(dec, &m); err != nil {
			return err
		}
		d.schema = &m
		return nil
	}
}
