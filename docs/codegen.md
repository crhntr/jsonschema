# JSON Schema 2020-12 → Go code generator

Status: design — Phases 1 (vocabulary parsing), 2 (type resolver),
3 (struct IR + emit), 4 (required vs optional), 5 (marshaler
emission), and 6 (scalar root with length / range / enum / pattern
constraints) implemented in `internal/generate/`. End-to-end CLI is
wired and covered by scripttest fixtures under
`cmd/go-jsonschema/testdata/generate/`. Phases 7–15 pending.

## Context

The package validates JSON Schema 2020-12 documents but the only Go
shapes available to callers are the hand-rolled `Schema` /
`SchemaObject` types. The code generator takes a JSON Schema 2020-12
document (and optional override config) and emits idiomatic Go: types
whose `UnmarshalJSONFrom` / `MarshalJSONTo` accept exactly the JSON
the schema accepts and reject everything else.

The self-hosting goal: regenerate `Schema` from the 2020-12
meta-schema and ultimately replace the hand-rolled type.

The generator extends JSON Schema with a small Go-codegen vocabulary
so authors can steer naming, type choice, optionality, map shape, and
docs without modifying the surrounding spec. Override blocks
(`$ref → {goIdent, goType, fields}`) handle the cases where
annotations on the referenced schema itself can't be edited (e.g. the
published 2020-12 meta-schema).

## Vocabulary

URI: `https://crhntr.github.io/jsonschema/vocab/go-codegen` (final URI
TBD). Keywords are siblings of standard ones; absence is a no-op.

### Schema-level (any subschema)

| Keyword        | Type             | Purpose                                                                                                                                                                                       |
| -------------- | ---------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `goIdent`      | string           | Exported Go type/field identifier override.                                                                                                                                                   |
| `goType`       | string           | Explicit Go type expression (e.g. `int8`, `time.Time`, `*big.Rat`, `[]netip.Addr`). Wins over derived type. Parsed as a Go type expression and resolved against `goImports` plus the stdlib. |
| `goImports`    | array of string  | Package paths whose identifiers may appear in `goType` / `mapKeyType` / `mapValueType`. Loaded once via `golang.org/x/tools/go/packages.Load`.                                                |
| `goDoc`        | string           | Doc comment for the generated declaration. Falls back to `description`.                                                                                                                       |
| `mapKeyType`   | string           | Go type for object map keys when the schema generates a map. Must be `string`, a stdlib-`strconv`-convertible primitive, or a type implementing `encoding.TextMarshaler` + `TextUnmarshaler`. |
| `mapValueType` | string           | Go type for map values; same rules.                                                                                                                                                           |
| `goJSONTags`   | array of string  | Extra struct-tag flags spliced verbatim into the `json:"…"` tag after the json name. e.g. `omitempty`, `omitzero`, `format:RFC3339`, `inline`. The generator does not validate flag spelling. |

Optional fields are represented as `*T` and absent values as `nil` —
no `Optional[T]` runtime type.

Example:

```json
{
  "type": "string",
  "format": "date-time",
  "goType": "time.Time",
  "goImports": ["time"],
  "goJSONTags": ["format:RFC3339Nano", "omitempty"]
}
```

emits:

```go
Created time.Time `json:"created,format:RFC3339Nano,omitempty"`
```

### Document-level (root only)

- `goOverrides` (object) — top-level config keyed by JSON Pointer or
  ref string; values are `{goIdent, goType, fields: {jsonName:
  GoFieldName}}`. Refs must resolve to an in-scope subschema.

### Override resolution order (last write wins)

1. Annotation on the subschema itself (`goIdent`, `goType`, …).
2. `goOverrides` block on the entrypoint schema's root.
3. `--overrides path/to/file.json` from the CLI (or
   `generate.Options.OverridesFile`). Same shape as `goOverrides`.

This way schemas you control are annotated inline; refs into schemas
you don't control (e.g. the 2020-12 meta-schema) get a sidecar
overrides file.

## Generated code imports

The generated package may **only** import:

- The Go standard library, including `encoding/json/v2` and
  `encoding/json/jsontext` (go1.26 + `GOEXPERIMENT=jsonv2`).
- `github.com/go-json-experiment/json` only as a fallback when the
  user opts out of the experiment; default is the stdlib path.
- `golang.org/x/*` (e.g. `golang.org/x/net/idna` for hostname
  validation).

No `optional/` runtime sibling package. If a helper type is
unavoidable for a specific schema, emit it into the generated package
directly (one copy per output directory). The generator must reject
`goType` values that resolve outside this allowlist with a clear
error.

## Architecture

The generator lives in `./internal/generate` (package `generate`). It
is internal because the public surface is the CLI
(`go-jsonschema generate`) and the generated Go output — callers
should not import the generator directly.

```
internal/generate/
  generate.go       — public Generate entry; multi-pass orchestrator
  file.go           — File struct: implements ImportManager, holds *ast.File set
  imports.go        — ImportManager interface + dedup/collision logic
  types.go          — packages.Load wrapper; resolve goType strings
                      against loaded *types.Package set
  vocab.go          — vocab URI + Annotations decoded from SchemaObject.Extra
  ir.go             — intermediate representation
  derive.go         — schema → IR (the "best-effort" type algorithm)
  emit.go           — IR → []*ast.Decl (struct/slice/map/composite)
  emit_marshal.go   — generates UnmarshalJSONFrom / MarshalJSONTo
  literals.go       — Int/String/Bool/Nil literal AST helpers
  call.go           — Call(im, pkgName, pkgPath, ident, args...) helpers
  validate_ast.go   — emit fragments for enum/range/regex/pattern/dependent
  format.go         — go/printer + golang.org/x/tools/imports.Process
  warn.go           — constraint/type incompatibility warnings
```

`golang.org/x/tools/imports` is fine for the **generator's** own
build; it is *not* used in generated output.

Decompose along the same axes as `typelate/muxt/internal/astgen/`
(one file per concern). Patterns to mirror:

- **`ImportManager` interface** (à la `muxt/internal/astgen/gen.go`):
  `Import(pkgIdent, pkgPath string) string` returns the local name
  to use, lazily registers an `*ast.ImportSpec`, and SHA1-hashes the
  pkg path on identifier collisions. `ImportSpecs()` returns a sorted
  deduplicated slice at emit time.
- **`ExportedIdentifier(im, pkgName, pkgPath, ident)`** returns
  `*ast.SelectorExpr` and registers the import as a side effect.
  Every helper that emits `time.Time`, `netip.Addr`, `*url.URL`,
  `regexp.MustCompile`, etc. routes through this so we never write
  imports by hand.
- **Thin domain helpers** (`Call`, literal constructors,
  `EmptyStructType`) that take `ImportManager` + values → return
  AST node. Compose freely; no context struct.
- **Format pass**: `printer.Fprint` →
  `golang.org/x/tools/imports.Process` with
  `Fragment: true, Comments: true`. Imports accumulate during
  generation; let `imports.Process` prune unused ones at the end
  rather than tracking liveness.
- **Doc comments**: attach via `TypeSpec.Doc = &ast.CommentGroup{...}`
  using `goDoc` (fallback `description`).

## Generation pipeline

The generator is multi-pass:

0. **Type-load.** Collect every distinct package path from
   `goImports` annotations across the schema, plus stdlib defaults
   (`time`, `net/netip`, `net/url`, `math/big`, …) and
   `encoding/json/v2` / `encoding/json/jsontext`. Load them once via
   `packages.Load` with mode
   `NeedName | NeedTypes | NeedTypesInfo | NeedDeps`. Build a
   `map[pkgPath]*types.Package` consulted whenever a `goType` /
   `mapKeyType` / `mapValueType` string is parsed.
1. **Walk** every subschema; fold standard keywords + the vocab into
   per-schema annotations (parsed from `SchemaObject.Extra`).
2. **Identify** named types: each subschema with `goIdent` (or backed
   into a `$defs`/`properties` slot whose name we can use) becomes a
   type. Schemas used only as constraint composition (e.g. allOf
   members with no `goIdent`) don't emit a type — their constraints
   are merged into types that reference them.
3. **Derive** Go shape per type from constraints (algorithm below).
4. **Resolve** cross-type references (`$ref` targets pick up the
   target's IR). Emit warnings for incompatible constraint/type
   combos.
5. **Emit** `*ast.File` per output filename via `go/ast` +
   `go/printer` — never `text/template`.

### Type derivation

- `goType` set → parse as `ast.Expr`, resolve every selector
  (`pkg.Ident`) against the loaded packages, and emit using the
  ImportManager so the package is registered. Reject if any selector
  is unresolved or its target type is not exported.
- `type` is a single primitive → standard mapping (string→`string`,
  integer→`int`, number→`float64`, boolean→`bool`, null→`*struct{}`
  or drop, array→slice, object→struct/map).
- `type` is missing → infer from siblings (presence of `properties`
  implies object, `items` implies array, `enum` of homogeneous types
  implies that primitive).
- `type` is a multi-element array → composite type (struct with
  setters/getters mirroring how `Schema` handles object|bool).
- `format` may refine a `string` to `time.Time` (`date-time`),
  `netip.Addr` (`ipv4`/`ipv6`), `*url.URL` (`uri`), etc., but only
  when the keyword is asserting (caller opts in via the format
  vocabulary or generator flag).
- Numbers: prefer `int` for integer schemas, `float64` for number
  schemas. Fall back to `*big.Rat` only when constraints make int /
  float lossy (e.g. `multipleOf: 0.1`, very large min/max), or when
  `goType` says so.
- Optional: properties not in `required` are emitted as `*T`. Absent
  values are `nil`. The generated `UnmarshalJSONFrom` allocates on
  presence; `MarshalJSONTo` omits `nil` fields.

### Marshal / Unmarshal

- Implement `UnmarshalJSONFrom(*jsontext.Decoder, ...json.Options)`
  and `MarshalJSONTo(*jsontext.Encoder, ...json.Options)`.
- Skip generating either when the type is unconstrained — the stdlib
  marshaler already round-trips correctly.
- Otherwise the emitted code re-validates: rejects unknown keys when
  `additionalProperties: false`, enforces `enum`/`const`, range
  checks, length checks, regex (raw, no shim), `dependentSchemas`,
  etc. Use jsonv2 options (`RejectUnknownMembers`, etc.) where they
  fit.

## Critical files in this repo

- `schema.go` — `SchemaObject.Extra` already captures unknown
  keywords; codegen reads vocab from there.
- `validate.go` — reuse `validateNumber`, `validateString`,
  `compileECMA262`, `compareRat`, etc. for the runtime side; the
  generator emits inline equivalents (or calls into a shared runtime
  helper package).
- `cmd/go-jsonschema/main.go::generate` — currently a stub; wire to
  `internal/generate.Generate`.
- `internal/generate/vocab.go` — Phase 1 (Annotations type +
  ParseAnnotations).
- `internal/generate/types.go` — Phase 2 (Resolver +
  allowedImportPath).
- `internal/generate/ir.go`, `derive.go`, `emit.go` — Phase 3
  (struct IR + Derive + Emit).

## Test discipline

Test discipline mirrors muxt's CLI testdata pattern (see
`typelate/muxt/cmd/muxt/testdata/`, e.g.
`howto_template_with_no_call.txt`): each phase ships **one
script-test fixture** in
`cmd/go-jsonschema/testdata/generate/<phase>.txt`. Each `.txt`
contains the input schema, a `go.mod`, and a hand-written `_test.go`
separated by `-- name --` markers. The script:

1. Runs `go-jsonschema generate ...` to emit Go into the script's
   working directory.
2. Optionally checks shape with `exec go doc <pkg>.<Ident>` (compare
   exact output — never substring-grep generated source).
3. Runs `exec go test -cover` to exercise the generated code against
   the hand-written assertions.

The fixture authors the assertions; the generator only has to produce
a package that compiles and behaves. Example skeleton:

```
env GOEXPERIMENT=jsonv2
go-jsonschema generate --schema schema.json --package model

exec go test -cover

-- schema.json --
{"type": "object", "properties": {"name": {"type": "string"}}}
-- go.mod --
module example.com/model

go 1.26
-- model_test.go --
package model

import (
	"encoding/json/v2"
	"testing"
)

func TestRoundTrip(t *testing.T) { /* ... */ }
```

Lower-level package tests in `internal/generate/*_test.go` use
`t.TempDir()` for unit coverage of derive/emit functions where the
script harness would be overkill.

The scripttest harness in `cmd/go-jsonschema/main_test.go` sets
`GOEXPERIMENT=jsonv2` on `script.Engine.Env` (and propagates it into
the `go-jsonschema` cmd handler) so:

- `exec go test` / `exec go build` see the v2 stdlib package.
- The generator's own `packages.Load` (which shells `go list`) can
  resolve `encoding/json/v2`.

Fixture `go.mod` files declare `go 1.26`. Hand-written `_test.go`
imports `encoding/json/v2` and `encoding/json/jsontext` directly.

Run `go test ./...` after every iteration so the existing 2061
conformance cases never regress.

## Phases

Each phase lands as its own commit.

1. **Vocabulary parsing.** ✅ Implemented
   (`internal/generate/vocab.go`). Decode `SchemaObject.Extra` into
   a typed `generate.Annotations` struct (`GoIdent`, `GoType`,
   `GoImports`, `GoDoc`, `MapKeyType`, `MapValueType`, `GoJSONTags`).

2. **Type resolver.** ✅ Implemented (`internal/generate/types.go`).
   `Resolver` loads the configured `goImports` via `packages.Load`
   and exposes `Resolve(src string) (ast.Expr, types.Type, error)`,
   which parses a `goType` string, walks selectors / pointers /
   slices / maps, and resolves identifiers against the universe
   scope or the loaded package set. Imports outside the allowed set
   (stdlib / `github.com/go-json-experiment/json` / `golang.org/x/*`)
   are rejected at resolver construction.

3. **IR for a single struct schema.** ✅ Implemented
   (`internal/generate/ir.go`, `derive.go`, `emit.go`).
   `Derive(name, *jsonschema.SchemaObject)` produces an
   `ir.Type` from `{type: object, properties: {...}, required:
   [...]}`. Emit only the Go struct (no marshalers). Test: `go
   build`, then `go doc` confirms the struct + its fields.

4. **Required vs optional.** ✅ Implemented (`derive.go`, `emit.go`).
   Optional fields are `*T`; required fields are bare `T`. Optional
   fields' json tag carries `,omitzero` so `nil` round-trips as a
   missing key under jsonv2. End-to-end presence/absence round-trip
   covered by the scripttest fixture in Phase 14.

5. **Marshal / Unmarshal generation for the simple struct.** ✅
   Implemented (`marshal.go`). `EmitMarshal` delegates to jsonv2 via
   a local alias type; `EmitUnmarshal` decodes into a pointer-shadow
   struct, rejects nil for required fields, and propagates
   `json.RejectUnknownMembers(true)` when the schema declares
   `additionalProperties: false`. End-to-end round-trip is left to
   the Phase 14 scripttest fixture. Original spec: emit that
   enforces required +
   `additionalProperties: false`. Test: run a generated test that
   feeds invalid + valid instances; expects the right errors.

6. **Scalar shapes.** Schemas whose root is a single primitive
   (string with `minLength`/`maxLength`/`pattern`, integer with
   `minimum`/`maximum`, number similarly, enum). Test: round trip
   passes / fails per range.

7. **Slice + map types.** `type: array`, `items: {...}` → slice.
   `type: object`, `additionalProperties: {...}` (no `properties`)
   → `map[KEY]VALUE`, with `mapKeyType` / `mapValueType` honored.
   Test: round trips + a key-type with a `TextMarshaler` impl.

8. **Composite types (`type: ["object","boolean"]`).** Generated
   accessors mirror the hand-rolled `Schema` API (`TypeBool`,
   `TypeObject`). Test: golden round-trip across both branches.

9. **`$ref` between $defs.** Each `$defs` entry becomes its own
   type; refs become Go pointers/values. Test: a schema with two
   `$defs` that reference each other (recursive) compiles and
   round-trips.

10. **`dependentSchemas` + `oneOf`/`anyOf`/`allOf` validation in
    generated marshalers.** Test: instances that fail the dependent
    rule are rejected by the generated `UnmarshalJSONFrom`.

11. **Override config and JSON tag flags.** Document-level
    `goOverrides` + per-schema `goIdent`/`goType`/`fields`. Add the
    `goJSONTags` annotation (string list of jsonv2 tag flags like
    `omitempty`, `omitzero`, `format:RFC3339`, `string`, `inline`)
    and a per-field equivalent inside the override `fields` map. The
    generator splices them into the emitted struct tag verbatim,
    after the json name. Test: same input schema, two different
    override files, two different generated APIs (one using
    `omitempty`, one using `format:RFC3339Nano` on a `time.Time`
    field) — `_test.go` round-trips exercise both.

12. **Constraint/type mismatch warnings.** `goType: int8` +
    `minimum: 1000` → returns a structured warning the CLI surfaces
    on stderr; generation still produces something compilable. Test:
    capture warnings list. Also reject `goType` values outside the
    allowed import set (stdlib / jsonv2 / golang.org/x).

13. **Self-host the 2020-12 meta-schema.** Generate types from
    `testdata/schema/json-schema.org/draft/2020-12/*.json` into a
    `t.TempDir()`. Run `go vet` + `go build` + `go doc` and log
    output for manual side-by-side comparison with the hand-rolled
    `Schema`. (No assertion that it matches yet.)

14. **CLI wiring.** `go-jsonschema generate --schema X --out
    PKG_DIR [--package NAME] [--overrides FILE]`. Test: scripttest
    harness invokes the CLI, then `exec go test` on the produced
    package.

15. **Replace hand-rolled `Schema`** (separate, opt-in commit). Run
    the full conformance suite against the generated types.

Each phase ends with `go test ./...` plus the conformance suite
green before moving on. Phases 7–9 are the high-risk ones (composite
types and applicator validation in generated code) — explicitly
carve extra review time there.

## Verification

End-to-end:

- `go test ./internal/generate/...` — phase-by-phase tests, each
  shelling out to `go test`/`go build`/`go vet`/`go doc` on a
  `t.TempDir()`.
- `go test ./...` — conformance suite (2061/2061) stays green
  throughout; no regression in the validator from any shared
  refactors.
- `go test ./cmd/go-jsonschema` — scripttest covers the `generate`
  subcommand once Phase 14 lands; `exec go test` inside the script
  exercises the produced package.
- Manual: `go doc github.com/crhntr/jsonschema/internal/generate`
  after Phase 13 — compare against the hand-rolled API listed by
  `go doc github.com/crhntr/jsonschema`.
