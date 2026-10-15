# Documentation

## Tutorials

- [Build an editor for JSON configuration kept in git](tutorials/versioned-config-editor.md):
  browse, validate, query across branches and commit edits to JSON files and
  their schema with go-git and `jsonhandler`.

## How-to guides

- [Generate a JSON Schema from a CUE definition](how-to/json-schema-from-cue.md)

## Reference

- [Designing schemas for clear error messages](reference/schema-error-messages.md):
  which spelling of a constraint gives the clearer failure, and the message
  each keyword produces.
- [`jsonhandler`](../jsonhandler/README.md): endpoints, markup and styling
  for the form library.

## Agent skills

Short [skills](https://docs.claude.com/en/docs/claude-code/skills) for coding
agents working with this module. Copy or symlink a directory into a
project's `.claude/skills/` (or `~/.claude/skills/`) to use it.

- [`jsonschema-validate`](skills/jsonschema-validate/SKILL.md): validate documents and schemas with `jsch` or the Go API.
- [`jsonschema-design`](skills/jsonschema-design/SKILL.md): write schemas with clear errors and good codegen.
- [`jsonschema-generate-go`](skills/jsonschema-generate-go/SKILL.md): generate Go types with `jsch generate`.
- [`jsonhandler-forms`](skills/jsonhandler-forms/SKILL.md): serve schema-driven forms.
