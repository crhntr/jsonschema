# Generate a JSON Schema from a CUE definition

This guide turns a [CUE](https://cuelang.org) definition into a JSON Schema
2020-12 document that `jsch`, `Schema.Validate` and `jsonhandler` can use. It
assumes you already describe your data in CUE and want a schema file you can
check in, validate against, generate Go types from, or render as a form.

CUE writes JSON Schema with `cue def --out jsonschema`. The CUE project marks
that output as experimental, so the exact shape of the generated schema can
change between CUE releases. Pin the CUE version you generate with, and review
the generated file in code review the way you would any other generated code.
Everything below was checked with CUE v0.17.1.

## Export a definition

Given a CUE package in `./spec`:

```cue
package spec

import "strings"

// A person who can sign in.
#User: {
	// Display name.
	name!:  string & strings.MinRunes(1)
	email!: =~"^[^@]+@[^@]+$"
	age?:   int & >=0 & <=150
	role!:  string & ("admin" | "editor" | "viewer")
	tags?: [...string]
	address?: #Address
}

#Address: {
	street!: string
	city!:   string
}
```

export one definition with `-e` and write it with an `-o jsonschema:` file
prefix:

```sh
cue def ./spec -e '#User' -o jsonschema:user.schema.json
```

Leave off `-o` to print the schema instead. Comments on fields become
`description`, referenced definitions such as `#Address` land in `$defs`, and
because CUE definitions are closed, every object gets
`"additionalProperties": false`.

## Check the result

Confirm the file is a valid 2020-12 schema, then try it on a document you
expect to fail:

```sh
jsch validate --schema-2020-12 user.schema.json
jsch validate --schema user.schema.json bad-user.json
```

With `{"name": "", "email": "ada", "age": 200, "role": "owner", "nickname": "a"}`
in `bad-user.json`, the second command reports one problem per field:

```
bad-user.json:1:0: /name: string length 0 less than minLength 1; /email: string does not match pattern "^[^@]+@[^@]+$"; /age: greater than maximum 150; /role: value is not in enum; /nickname: nothing allowed here
```

From here the schema works anywhere a hand-written one does, including
`jsch generate`, `jsch form --schema user.schema.json` and `jsonhandler.New`.

## Regenerate it with go generate

Put the directive in a Go file next to the CUE package, so the schema is
rebuilt with the same CUE version on every machine:

```go
package spec

//go:generate go run cuelang.org/go/cmd/cue@v0.17.1 def . -e #User -o jsonschema:user.schema.json
```

`go generate` does not use a shell, so `#User` needs no quoting there. If you
need to generate several definitions or adjust the schema in Go before writing
it, the same conversion is available as a library call,
[`jsonschema.Generate`](https://pkg.go.dev/cuelang.org/go/encoding/jsonschema#Generate):

```go
ctx := cuecontext.New()
inst := load.Instances([]string{"./spec"}, nil)[0]
if err := inst.Err; err != nil {
	return err
}
v := ctx.BuildInstance(inst).LookupPath(cue.ParsePath("#User"))
if err := v.Err(); err != nil {
	return err
}
expr, err := jsonschema.Generate(v, nil)
if err != nil {
	return err
}
schema, err := ctx.BuildExpr(expr).MarshalJSON()
```

The imports are `cuelang.org/go/cue`, `cuelang.org/go/cue/cuecontext`,
`cuelang.org/go/cue/load` and `cuelang.org/go/encoding/jsonschema`.

## Write CUE that exports well

A few CUE spellings lose information on the way to JSON Schema. Each row below
shows what CUE v0.17.1 produced and what to write instead.

| You write | The schema gets | Write this instead |
|---|---|---|
| `kind: "dog"` (a regular field with a concrete value) | `"const": "dog"`, but `kind` is left out of `required` | `kind!: "dog"`, which keeps it in `required`. Discriminator fields in a disjunction need this, or a document without `kind` can match the wrong branch. |
| `port: *8080 \| int` | `{"type": "integer"}`, with no `default` and `port` left out of `required` | Mark it `port!:` if it must be present. Add `"default": 8080` after generating if a form should start with it. |
| `role: "admin" \| "editor"` | `{"enum": [...]}` with no `type` | `role: string & ("admin" \| "editor")`, which adds `"type": "string"`, so `jsch generate` makes a `string` field instead of `any`. |
| `age: int & >=0 & <=150` | an `allOf` of `{"type": "number"}` and `{"type": "integer", "minimum": 0, "maximum": 150}` | Nothing better is available yet. It validates correctly, but `jsch generate` makes the field `any`. |
| `pet: #Dog \| #Cat` | `anyOf` of two `$ref`s | Fine for validation. Give each branch a required, constant discriminator (see the first row) so a value can only match one of them. |
| `when: time.Time` | a `$defs/Time` that is only `{"type": "string"}`, with no `format` | Add `"format": "date-time"` after generating if you want forms to render a date-time input. |
| `labels: [string]: string` | `"additionalProperties": {"type": "string"}` | This maps cleanly. |
| `env: [=~"^[A-Z_]+$"]: string` | `patternProperties` and `"additionalProperties": false` | This maps cleanly. |
| `{a: int, ...}` | `"additionalProperties": true` | This maps cleanly. |
| `x: string \| null` | `anyOf` of `string` and `null` | This maps cleanly. |

## Add what CUE cannot say

`default`, `format`, `examples` and the `x-form` annotations that
[jsonhandler](../../jsonhandler/README.md#annotations) reads have no CUE
spelling that survives the export. Add them in a step after generation, either
in the Go program above before marshaling or with a JSON tool such as `jq`:

```sh
jq '.properties.email.format = "email" | .properties.role.default = "viewer"' \
  user.schema.json > user.schema.json.tmp && mv user.schema.json.tmp user.schema.json
```

Keep that step in the same `go generate` chain, so a regenerated schema never
silently drops it.
