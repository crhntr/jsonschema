---
name: jsonschema-generate-go
description: Generate Go types from a JSON Schema with jsch generate (crhntr/jsonschema). Use when adding or regenerating schema-derived Go types.
---

# Generate Go types

```sh
jsch generate --schema schema.json --out ./model --package model --type Config
```

Pin it as a tool and regenerate with `go generate`:

```sh
go get -tool github.com/crhntr/jsonschema/cmd/jsch@main
```

```go
//go:generate go tool jsch generate --schema config.json --out ../model --package model --type Config
```

- Generated code imports only the standard library.
- Unmarshalers enforce `required`, `enum`, `pattern`, `minLength`/`maxLength`,
  `minimum`/`maximum`, `dependentRequired` and `additionalProperties: false`.
  Everything else (`oneOf`, `not`, `multipleOf`, `uniqueItems`, …) needs
  `Schema.Validate` first; see the jsonschema-validate skill.
- `$defs` become named types. `oneOf`/`anyOf`, enums without `"type"`, and
  `allOf` of types become `any`; reshape the schema before reaching for
  `--overrides`.
- Never edit `*.gen.go`; change the schema and regenerate.
