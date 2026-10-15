# Designing schemas for clear error messages

JSON Schema often offers several ways to state the same constraint, and they
are not equal when a document fails. Some produce one message at the field
that is wrong. Others produce a message about the schema's structure, or a
message from every branch the validator tried. This page lists the choices
that matter, with the exact output of `Schema.Validate` in this module for
each, so you can pick the spelling whose failure a person can act on.

Every message below comes from `Validate(...).AsError()` on the document
shown, and `jsch validate` prints the same text.

## How a message reads

```
config.json:1:0: /owner: missing required property "email"
```

A message starts with the document name and a position, then the failing
location as a [JSON Pointer](https://www.rfc-editor.org/rfc/rfc6901) when it
is not the root, then the message for the keyword that failed. Several
failures are joined with `; `.

The location is the only part of a message that comes from your data, so the
property names you choose are the names people see. A property called
`replicas` explains itself in `/replicas: less than minimum 1`, and one called
`r` does not. The validator never puts `title` or `description` in a message.
Those keywords still help, because forms rendered by
[`jsonhandler`](../../jsonhandler/README.md) show the description next to the
field that has the error, and editors that read schemas show them as hints.

## Choose the spelling with the clearer failure

### One value from a fixed set

| Schema | Message for `"pink"` |
|---|---|
| `{"enum": ["red", "green", "blue"]}` | `value is not in enum` |
| `{"type": "string", "pattern": "^(red\|green\|blue)$"}` | `string does not match pattern "^(red\|green\|blue)$"` |
| `{"oneOf": [{"const": "red"}, {"const": "green"}, {"const": "blue"}]}` | `oneOf: 0 subschemas matched, want exactly 1; value does not equal const; value does not equal const; value does not equal const` |
| `{"anyOf": [{"const": "red"}, ...]}` | `no anyOf subschema matched; value does not equal const; ...` |

Use `enum`. Its message is short and points at the value, while `oneOf` or
`anyOf` over `const` repeats one line per option without saying what any of
them were, and adding a `title` to each option does not change that. The
`enum` message does not list the allowed values either, so list them in the
field's `description`. A form avoids the problem entirely, because
`jsonhandler` renders an `enum` as a select or radio buttons.

A single allowed value reads the same either way: `{"const": "v1"}` reports
`value does not equal const` and `{"enum": ["v1"]}` reports
`value is not in enum`.

### A value that may have more than one type

| Schema | Message for `"3"` |
|---|---|
| `{"type": ["integer", "null"]}` | `type integer\|null does not match string` |
| `{"anyOf": [{"type": "integer"}, {"type": "null"}]}` | `no anyOf subschema matched; type integer does not match string; type null does not match string` |

Use a list of types. The message states the whole expectation in one line.

### A string that must not be empty

| Schema | Message for `""` |
|---|---|
| `{"type": "string", "minLength": 1}` | `string length 0 less than minLength 1` |
| `{"type": "string", "pattern": "."}` | `string does not match pattern "."` |
| `{"type": "string", "not": {"const": ""}}` | `not: subschema unexpectedly matched` |

Use `minLength`. Of the three, it is the only message that describes the
string.

### Avoid `not` when a positive keyword exists

| Schema | Message for `0` |
|---|---|
| `{"type": "number", "exclusiveMinimum": 0}` | `not strictly greater than exclusiveMinimum 0` |
| `{"type": "number", "minimum": 0, "not": {"const": 0}}` | `not: subschema unexpectedly matched` |

`not` can only report that its subschema matched, which tells the reader
nothing about the value. Reach for `exclusiveMinimum`, `exclusiveMaximum`,
`minLength`, `propertyNames` or `enum` first, and keep `not` for constraints
none of them can express.

### Fields that must appear together

| Schema | Message for `{"start": 1}` |
|---|---|
| `{"dependentRequired": {"start": ["end"]}}` | `property "start" requires "end"` |
| `{"if": {"required": ["start"]}, "then": {"required": ["end"]}}` | `missing required property "end"` |
| `{"oneOf": [{"required": ["start", "end"]}, {"not": {"anyOf": [{"required": ["start"]}, {"required": ["end"]}]}}]}` | `oneOf: 0 subschemas matched, want exactly 1; missing required property "end"; not: subschema unexpectedly matched` |

Use `dependentRequired`. Its message names both properties and the relation
between them. To require the pair in both directions, list each property
under the other.

### Variants chosen by a tag

A tagged union is an object whose `kind` (or `type`, or `version`) decides
which other properties it needs. Take `{"kind": "dog", "barks": "yes"}`,
where a dog's `barks` must be a boolean:

| Schema | Message |
|---|---|
| `oneOf` of one object schema per kind | `oneOf: 0 subschemas matched, want exactly 1; /barks: type boolean does not match string; /kind: value does not equal const; missing required property "lives"` |
| `anyOf` of the same schemas | `no anyOf subschema matched; /barks: type boolean does not match string; /kind: value does not equal const; missing required property "lives"` |
| `kind` as an `enum`, plus one `if`/`then` per kind under `allOf` | `/barks: type boolean does not match string` |

With `oneOf` or `anyOf`, the validator reports why the document failed every
branch, including the cat branch it was never meant to match, so the one real
problem is mixed in with complaints about `lives`. With `if`/`then`, only the
branch whose condition matched reports anything:

```json
{
  "type": "object",
  "required": ["kind"],
  "properties": {"kind": {"enum": ["dog", "cat"]}},
  "allOf": [
    {
      "if": {"properties": {"kind": {"const": "dog"}}},
      "then": {"required": ["barks"], "properties": {"barks": {"type": "boolean"}}}
    },
    {
      "if": {"properties": {"kind": {"const": "cat"}}},
      "then": {"required": ["lives"], "properties": {"lives": {"type": "integer"}}}
    }
  ]
}
```

An unknown tag also reads better this way. For `{"kind": "cow"}` the
`if`/`then` schema reports `/kind: value is not in enum`, and the `oneOf`
schema reports two `const` failures and two missing properties.

Forms are the exception. `jsonhandler` renders `oneOf` and `anyOf` as a
variant picker and shows only the chosen variant's errors, so the noise above
never reaches the person filling in the form, while `if`/`then` does not
change the form at all. Use `oneOf` with a constant tag in each branch when
people edit the data through a form, and `if`/`then` when they read the
validator's messages directly, as in CI or an API response.

### Closed objects

| Schema | Document | Message |
|---|---|---|
| `{"properties": {"a": {...}}, "additionalProperties": false}` | `{"a": "x", "b": 1}` | `/b: nothing allowed here` |
| `{"allOf": [{"properties": {"a": {...}}}], "unevaluatedProperties": false}` | `{"a": "x", "b": 1}` | `/b: nothing allowed here` |
| `{"allOf": [{"properties": {"a": {...}}}], "additionalProperties": false}` | `{"a": "x"}` | `/a: nothing allowed here` |

Both working spellings name the unexpected property. The third row is a trap:
`additionalProperties` only sees `properties` in its own schema object, so
when the properties come from `allOf` or a `$ref`, it rejects the properties
you declared. Put `additionalProperties: false` next to `properties`, or use
`unevaluatedProperties: false` when you compose the object from pieces.

### Rules for property names

| Schema | Message for `{"OK": "1", "bad": "2"}` |
|---|---|
| `{"propertyNames": {"pattern": "^[A-Z_]+$"}, "additionalProperties": {"type": "string"}}` | `string does not match pattern "^[A-Z_]+$"` |
| `{"patternProperties": {"^[A-Z_]+$": {"type": "string"}}, "additionalProperties": false}` | `/bad: nothing allowed here` |

Neither is complete. `propertyNames` explains the rule but reports it at the
object, so the message does not say which name broke it. `patternProperties`
with `additionalProperties: false` names the property but not the rule. For a
small object the first is usually clearer. For a large one, where finding the
offending key is the hard part, the second is.

### Formats are not checked unless you ask

| Schema | How it is validated | Message for `"ada"` |
|---|---|---|
| `{"type": "string", "format": "email"}` | `Validate` | none; the document is valid |
| `{"type": "string", "format": "email"}` | `ValidateWithFormatAssertion` | `not a valid email: "ada"` |
| `{"type": "string", "pattern": "^[^@]+@[^@]+$"}` | `Validate` | `string does not match pattern "^[^@]+@[^@]+$"` |

In 2020-12, `format` is an annotation unless the schema's vocabulary or the
validator opts in. Its message is the clearest of the three when it is
checked, so call `ValidateWithFormatAssertion` if you rely on formats.
Otherwise use `pattern`, and keep the pattern simple enough to read in the
message.

### Resolve references before validating

`jsonschema.Parse` and `Resolver.Load` return a schema whose `$ref`s are not
yet linked, and validating with it skips them without an error. With
`{"properties": {"http": {"$ref": "#/$defs/port"}}}` and a `port` definition
of `{"minimum": 1}`, such a schema accepts `{"http": 0}`. Calling
`Resolver.Resolve` with the URI you passed to `Load` links the references,
and the same schema then reports `/http: less than minimum 1`, as it does
through `jsonschema.Resolve`, `jsonhandler.New` and `jsch validate`:

```go
r := jsonschema.NewResolver(nil)
if _, err := r.Load("file:///config.schema.json", schemaJSON); err != nil {
	return err
}
schema, err := r.Resolve(ctx, "file:///config.schema.json")
```

## Message for each keyword

| Keyword | Message |
|---|---|
| `type` | `type integer does not match string` |
| `required` | `missing required property "email"` |
| `dependentRequired` | `property "start" requires "end"` |
| `enum` | `value is not in enum` |
| `const` | `value does not equal const` |
| `minLength` / `maxLength` | `string length 0 less than minLength 1`, `string length 4 greater than maxLength 3` |
| `pattern` | `string does not match pattern "^[a-z]+$"` |
| `format` (when asserted) | `not a valid email: "ada"` |
| `minimum` / `maximum` | `less than minimum 1`, `greater than maximum 150` |
| `exclusiveMinimum` / `exclusiveMaximum` | `not strictly greater than exclusiveMinimum 0`, `not strictly less than exclusiveMaximum 1` |
| `multipleOf` | `not a multiple of 0.5` |
| `minItems` / `maxItems` | `array has 0 items, minItems 1` |
| `uniqueItems` | `array items 0 and 2 are equal`, at the later item |
| `contains` | `contains matched 0 items, minContains 1`, followed by why each item failed |
| `minProperties` / `maxProperties` | `object has 0 properties, minProperties 1` |
| `additionalProperties` / `unevaluatedProperties: false` | `nothing allowed here`, at the extra property |
| `not` | `not: subschema unexpectedly matched` |
| `oneOf` | `oneOf: 0 subschemas matched, want exactly 1`, followed by every branch's errors (or a count above 1 when several match) |
| `anyOf` | `no anyOf subschema matched`, followed by every branch's errors |

## Summary

- Prefer keywords that describe the value (`enum`, `minLength`,
  `exclusiveMinimum`, `dependentRequired`) over combinators that describe the
  schema (`oneOf`, `anyOf`, `not`).
- Use a list in `type` instead of `anyOf` over types.
- For tagged unions, use `if`/`then` on the tag when people read raw messages,
  and `oneOf` when they use a form.
- Put `additionalProperties: false` beside `properties`, or use
  `unevaluatedProperties: false` when composing.
- Name properties the way people talk about them, and put the allowed values
  and units in `description`.
- Assert `format` explicitly or use `pattern`, and resolve `$ref`s before
  validating.
