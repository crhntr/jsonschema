package generate

import (
	"fmt"

	"github.com/crhntr/jsonschema"
)

// flatten returns a SchemaObject equivalent to obj with every allOf
// member's object-level keywords folded into the parent. The
// returned schema has no allOf branch — every property, required
// entry, and $defs entry it described is now a sibling on the
// returned object.
//
// Cycle detection: flatten refuses to revisit any *jsonschema.Schema
// pointer already on the resolution stack, so a self-referential
// allOf chain returns an error instead of looping forever.
func flatten(obj jsonschema.SchemaObject) (jsonschema.SchemaObject, error) {
	return flattenWithStack(obj, map[*jsonschema.Schema]bool{})
}

func flattenWithStack(obj jsonschema.SchemaObject, stack map[*jsonschema.Schema]bool) (jsonschema.SchemaObject, error) {
	out := obj
	out.AllOf = nil

	for i, member := range obj.AllOf {
		memberObj, err := resolveMember(member, stack)
		if err != nil {
			return jsonschema.SchemaObject{}, fmt.Errorf("flatten allOf[%d]: %w", i, err)
		}
		flat, err := flattenWithStack(memberObj, stack)
		if err != nil {
			return jsonschema.SchemaObject{}, fmt.Errorf("flatten allOf[%d]: %w", i, err)
		}
		mergeInto(&out, flat)
	}

	return out, nil
}

// resolveMember unwraps an allOf member to its SchemaObject. If the
// member is a $ref to an already-resolved schema, the resolved
// target is followed (and added to the cycle-detection stack).
func resolveMember(s *jsonschema.Schema, stack map[*jsonschema.Schema]bool) (jsonschema.SchemaObject, error) {
	target := s
	if r := s.Resolved(); r != nil {
		target = r
	}
	if stack[target] {
		return jsonschema.SchemaObject{}, fmt.Errorf("cycle through allOf at %p", target)
	}
	stack[target] = true
	// We do not delete on return — flattening is conjunctive and a
	// repeated $ref inside the same chain is still a cycle.

	obj, ok := target.TypeObject()
	if !ok {
		return jsonschema.SchemaObject{}, fmt.Errorf("allOf member is a boolean schema")
	}
	return obj, nil
}

// mergeInto folds src's object-level keywords into dst. allOf is
// conjunctive — every member must be satisfied — so collections
// union and scalars resolve dst-wins (which keeps the parent
// schema's explicit values stable).
func mergeInto(dst *jsonschema.SchemaObject, src jsonschema.SchemaObject) {
	if len(src.Properties) > 0 {
		if dst.Properties == nil {
			dst.Properties = map[string]*jsonschema.Schema{}
		}
		for k, v := range src.Properties {
			if _, ok := dst.Properties[k]; !ok {
				dst.Properties[k] = v
			}
		}
	}

	if len(src.Required) > 0 {
		seen := make(map[string]bool, len(dst.Required))
		for _, r := range dst.Required {
			seen[r] = true
		}
		for _, r := range src.Required {
			if !seen[r] {
				dst.Required = append(dst.Required, r)
				seen[r] = true
			}
		}
	}

	if len(src.Defs) > 0 {
		if dst.Defs == nil {
			dst.Defs = map[string]*jsonschema.Schema{}
		}
		for k, v := range src.Defs {
			if _, ok := dst.Defs[k]; !ok {
				dst.Defs[k] = v
			}
		}
	}

	if len(src.PatternProperties) > 0 {
		if dst.PatternProperties == nil {
			dst.PatternProperties = map[string]*jsonschema.Schema{}
		}
		for k, v := range src.PatternProperties {
			if _, ok := dst.PatternProperties[k]; !ok {
				dst.PatternProperties[k] = v
			}
		}
	}

	if dst.AdditionalProperties == nil && src.AdditionalProperties != nil {
		dst.AdditionalProperties = src.AdditionalProperties
	}
	if dst.Type == nil && src.Type != nil {
		dst.Type = src.Type
	}
	if dst.Description == "" {
		dst.Description = src.Description
	}
	if dst.Title == "" {
		dst.Title = src.Title
	}
}
