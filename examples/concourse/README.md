# Concourse pipeline editor

A browser editor for a [Concourse](https://concourse-ci.org) pipeline file,
built on this module. It reads the pipeline's YAML, converts it to JSON,
checks it against a JSON Schema for pipelines, and edits jobs, resources and
resource types through forms that [`jsonhandler`](../../jsonhandler/README.md)
renders from that schema. Saving a form rewrites only the lines of the item
you edited, so the rest of the file, comments and anchors included, stays as
you wrote it and a `git diff` shows just your change.

```sh
go run . testdata/pipeline.yml            # changes stay in memory; download them
go run . -write path/to/pipeline.yml      # save each change to the file
```

Then open http://localhost:8080/.

## What it shows

- **Overview.** The pipeline drawn the way Concourse draws it: resources and
  jobs in columns, an edge for every version that flows between them (dashed
  when it does not trigger), and one tab per group. Jobs and resources with
  problems are red. Below it are the problems, each linked to the form for
  its job or resource, and lists of everything in the file with its line.
- **A job, resource or resource type.** A form for that one item, with an
  outline for long jobs and validation as you type, next to the item's YAML
  and its problems. Items can be added and deleted.
- **YAML.** The whole file with line numbers and each problem shown on its
  line, and a text area to edit it directly, checked as you type.

## How it works

- `internal/yamljson` converts a YAML node tree to JSON, keeping key order,
  expanding aliases and applying `<<` merge keys, and records where each value
  is written, by JSON Pointer. It also merges an edited JSON value back into
  the original nodes, so unchanged parts keep their comments, quoting and
  aliases.
- `internal/pipeline` checks the JSON against the schema and turns the
  validator's output into problems on YAML lines. At each `oneOf`, it follows
  only the branch the step actually is (judged by the branch's `type` and the
  first property it requires), so a misspelled field in a `get` step is
  reported once, as `unknown field "triger"`, and not as a failure of every
  other kind of step. Pattern failures are explained with the schema's
  `description`. Edits re-render one list item and splice it into the text in
  place of its lines.
- `schema/pipeline.json` is a hand-written schema for pipelines. Its `$defs`
  for `job`, `resource` and `resource_type` are also the roots of the forms.
- `internal/hypertext` serves the pages; muxt generates its routes from
  `pages.gohtml`.

## Limitations

- The schema covers the common parts of the pipeline syntax, not every field
  of every Concourse release, and it accepts `((vars))` only where a string is
  allowed (and in durations).
- Top-level keys outside the schema are allowed, so a pipeline can keep
  anchors there, as the sample does with `slack_alert`.
- An item that is partly defined by an anchor used elsewhere cannot be edited
  through its form, since the change would reach every alias; edit the YAML.
- The page holds one file in memory. Two people editing at once each see
  their own copy, and the last save wins.
