# jsonschema

A spec-compliant JSON Schema 2020-12 toolkit for Go, built on
[`encoding/json/v2`](https://pkg.go.dev/encoding/json/v2).

## Status

**Early development**; APIs may change. `Schema.Validate` passes the official
[JSON-Schema-Test-Suite](https://github.com/json-schema-org/JSON-Schema-Test-Suite)
for 2020-12. `jsch generate` emits Go types for unmarshaling JSON shaped by
the schema.

### Caveats for Generated Go Code

[`Schema.Validate`](https://pkg.go.dev/github.com/crhntr/jsonschema#Schema.Validate)
checks the whole schema. The generated types encode shape — field names, nesting,
JSON types — and generated unmarshalers enforce `required`, `enum`, `pattern`,
`minLength`/`maxLength`, `minimum`/`maximum`, `dependentRequired`, and
`additionalProperties: false`.

Other validations — `oneOf`, `not`, `multipleOf`, `uniqueItems`, and the rest —
are currently the validator's job: validate first, then unmarshal. An Go value of a
generated type can also hold values the schema rejects, like a zero `int` where
the schema says `minimum: 1`. Nothing constrains a value built in Go code
(setters that return an error are not implemented or planned for development).

## Requirements

Go 1.26 with `GOEXPERIMENT=jsonv2` set. This is required for building and
if you use this as a dependency. With Go 1.27 this requirement will be removed.

## Quick start

### Library

```go
package main

import (
	"fmt"
	"os"

	"github.com/crhntr/jsonschema"
)

func main() {
	schemaJSON := []byte(`{"type": "object", "required": ["name"]}`)
	instanceJSON := []byte(`{"name": "alice"}`)

	schema, err := jsonschema.Parse(schemaJSON)
	if err != nil {
		fmt.Fprintln(os.Stderr, "malformed schema:", err)
		os.Exit(1)
	}
	output := schema.Validate("instance.json", instanceJSON)
	if err := output.AsError(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("valid")
}
```

`Validate` returns the verbose output tree; derive the other spec formats
with `output.Flag()`, `output.Basic()`, or `output.Detailed()`. For schemas
with external `$ref`s, use `jsonschema.Resolve` (HTTP) or `NewResolver` with
`Load`/`LoadFS` (offline).

### CLI

```sh
go install github.com/crhntr/jsonschema/cmd/jsch@latest

# Check if
# a schema parses against the 2020-12 meta-schema.
jsch validate --schema-2020-12 my-schema.json

# Validate an instance against your schema.
jsch validate --schema my-schema.json instance.json

# Stream an instance through stdin and emit a spec output document.
echo '{"name":"alice"}' | jsch validate --schema my-schema.json --output basic -

# Generate Go types from a schema.
jsch generate --schema my-schema.json --out ./model --package model --type Config
```

## License

See [LICENSE.txt](LICENSE.txt).
