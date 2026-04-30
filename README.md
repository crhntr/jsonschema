# jsonschema

A spec-compliant JSON Schema 2020-12 toolkit for Go, built on
[`github.com/go-json-experiment/json`](https://github.com/go-json-experiment/json).

## Status

Early development; APIs may change. The CLI command `jsch validete` works.
Code generation is on the roadmap but **not yet implemented** — `jsch generate` is a stub that does nothing.

## Quick start

### CLI

```sh
go install github.com/crhntr/jsonschema/cmd/jsch@latest

# Sanity-check that a schema parses against the 2020-12 meta-schema.
jsch validate --schema-2020-12 my-schema.json

# Validate an instance against your schema.
jsch validate --schema my-schema.json instance.json

# Stream an instance through stdin and emit a spec output document.
echo '{"name":"alice"}' | jsch validate --schema my-schema.json --output basic -
```

## License

See [LICENSE.txt](LICENSE.txt).
