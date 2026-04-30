# jsonschema

A spec-compliant JSON Schema 2020-12 toolkit for Go, built on
[`github.com/go-json-experiment/json`](https://github.com/go-json-experiment/json).

## Status

Early development; APIs may change. Code generation is on the roadmap
but **not yet implemented** — `jsch generate` is a stub that does
nothing today.

## What's shipped

- **Library** — `Parse`, `Resolver` and the package-level `Resolve`,
  `(*Schema).Validate`, `(*Schema).ValidateWithFormatAssertion`, and a
  spec-§12.4 `Output` tree with `Flag()`, `Basic()`, `Detailed()`, and
  `Verbose()` projections plus `AsError()` for Go's error idioms.
- **`jsch` CLI** — a `validate` subcommand that reads instance JSON
  from files or stdin (`-`) and accepts:
  - `--schema PATH-OR-URL` / `--schema-2020-12` (sugar for the
    canonical meta-schema URL),
  - `--skip-schema-validation` to bypass the default-on pre-flight
    check that `--schema` itself is a valid 2020-12 document,
  - `--strict` to fail on unknown schema keywords,
  - `--format-assert` to treat `format` as an assertion,
  - `--quiet` to suppress per-instance success lines,
  - `--output {flag,basic,detailed,verbose}` to emit the
    spec-defined output document per instance.
- **Embedded 2020-12 meta-schema** — the root meta-schema and all
  seven vocabulary documents are bundled via `//go:embed` so
  `jsch` can validate against the canonical meta-schema URLs
  with no network access. The pre-flight check that runs before
  every `validate` invocation uses the bundled copy.
- **Annotations across every vocabulary** — core, applicator,
  validation, meta-data, format-annotation, format-assertion,
  content, plus unknown-keyword pass-through. Vocabulary activation
  follows the resource's declared `$vocabulary`.
- **`jsonptr` sub-package** — RFC 6901 JSON Pointer with `Pointer`,
  `Find`, and `Builder`. Operates on raw `jsontext.Value` bytes so
  source positions survive the trip.
- **Conformance** — the official
  [JSON-Schema-Test-Suite](https://github.com/json-schema-org/JSON-Schema-Test-Suite)
  for draft 2020-12 is vendored under
  `testdata/JSON-Schema-Test-Suite/` and runs as part of
  `go test ./...`.

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

### Library

```go
package main

import (
    "context"
    "fmt"
    "os"

    "github.com/crhntr/jsonschema"
)

func main() {
    schema, err := jsonschema.Resolve(context.Background(), nil, "file:///etc/my-schema.json")
    if err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }

    instance, err := os.ReadFile("instance.json")
    if err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }

    if err := schema.Validate("instance.json", instance).AsError(); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}
```

## CI

GitHub Actions runs `go vet ./...`, `go build ./...`, and
`go test -race ./...` (with `GOEXPERIMENT=jsonv2`) on every push to
`main` and every pull request. See
[`.github/workflows/ci.yaml`](.github/workflows/ci.yaml).

## License

See [LICENSE.txt](LICENSE.txt).
