---
name: jsonschema-design
description: Write or review a JSON Schema so its validation errors are clear and it generates good Go types and forms with crhntr/jsonschema. Use when authoring or changing a schema.
---

# Design a schema

Prefer keywords that describe the value over combinators:

| Want | Write | Not |
|---|---|---|
| one of a few strings | `"type": "string", "enum": [...]` (list the values in `description`) | `oneOf`/`anyOf` of `const` |
| non-empty string | `"minLength": 1` | `"not": {"const": ""}` |
| several types | `"type": ["integer", "null"]` | `anyOf` of types |
| fields that go together | `dependentRequired` | `if`/`then`, `oneOf` tricks |
| > 0 | `exclusiveMinimum: 0` | `minimum` + `not` |

- Tagged unions: `oneOf` with a required `const` tag per branch when people
  edit through `jsonhandler` forms; `if`/`then` on the tag when they read raw
  validator messages (CI, APIs).
- Closed objects: `additionalProperties: false` beside `properties`; with
  `allOf`/`$ref` composition use `unevaluatedProperties: false`.
- Name properties as people say them; messages show only the JSON Pointer.
- For Go codegen, put reusable objects in `$defs` (named structs) and give
  enums `"type": "string"`; inline `oneOf`/`anyOf` become `any`.
- Authoring in CUE: see `docs/how-to/json-schema-from-cue.md` (use `!` for
  required constants, `string & (...)` for enums).

Details and exact messages: `docs/reference/schema-error-messages.md`.
