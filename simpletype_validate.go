package jsonschema

import (
	"errors"
	"fmt"
	"slices"

	"github.com/go-json-experiment/json"
)

// typeEnumStrings returns the legal values of the JSON Schema "type"
// keyword in 2020-12.
func typeEnumStrings() []SimpleType {
	return []SimpleType{
		"array",
		"boolean",
		"integer",
		"null",
		"number",
		"object",
		"string",
	}
}

// Validate reports an error if the Type's payload does not satisfy
// the structural constraints of the JSON Schema type keyword: either
// a single SimpleType from the enum, or a non-empty array of them.
func (m *Type) Validate() error {
	if m.isArray {
		for _, item := range m.array {
			if err := item.Validate(); err != nil {
				return err
			}
		}
		if len(m.array) < 1 {
			return errors.New("type array must have at least one item")
		}
	} else if m.isString {
		return m.string.Validate()
	}
	return nil
}

// Validate reports an error when st is not one of the seven JSON
// Schema simple types.
func (st SimpleType) Validate() error {
	if !slices.Contains(typeEnumStrings(), st) {
		exp, _ := json.Marshal(typeEnumStrings())
		return fmt.Errorf("invalid SimpleType: unexpected enum value %s expected one of %s", string(st), string(exp))
	}
	return nil
}
