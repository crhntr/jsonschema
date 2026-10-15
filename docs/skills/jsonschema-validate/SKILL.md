---
name: jsonschema-validate
description: Validate JSON against a JSON Schema 2020-12 with github.com/crhntr/jsonschema (jsch CLI or Go API). Use when checking documents or schemas, or wiring validation into Go code.
---

# Validate JSON with crhntr/jsonschema

CLI:

```sh
jsch validate --schema-2020-12 schema.json          # is the schema itself valid?
jsch validate --schema schema.json a.json b.json    # one line of errors per failing file
cat a.json | jsch validate --schema schema.json --output basic -
```

Add `--format-assert` to check `format` (it is an annotation by default) and
`--strict` to fail on unknown keywords or unresolvable `$ref`s.

Go:

```go
r := jsonschema.NewResolver(nil)
if _, err := r.Load("file:///schema.json", schemaJSON); err != nil { return err }
schema, err := r.Resolve(ctx, "file:///schema.json") // links $ref
if err != nil { return err }
if err := schema.Validate("doc.json", docJSON).AsError(); err != nil { return err }
```

- Never validate with a schema from `jsonschema.Parse` or `Load` alone: their
  `$ref`s are not linked and are skipped silently.
- Use `ValidateWithFormatAssertion` when `format` must be enforced.
- Validate before unmarshaling into types from `jsch generate`; they only
  enforce part of the schema.
