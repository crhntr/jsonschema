# issues

A small issue tracker that shows the pieces of this repository working
together. Three JSON Schemas in [`schemas`](schemas) describe its documents:
an issue, a contact, and the project settings. Everything else is generated
from them or from SQL.

```sh
go run . -addr localhost:8080 -db issues.db
```

## How it fits together

| Piece | Made by | From |
|---|---|---|
| The forms | [`jsonhandler`](../../jsonhandler), at run time | `schemas/*.json` |
| Go types (`internal/model`) | `jsch generate` | `schemas/*.json` |
| Queries (`internal/database`) | `sqlc generate` | `schema.sql`, `query.sql` |
| Page routes (`internal/hypertext`) | `muxt generate` | `pages.gohtml` |

`go generate ./...` reruns all three generators. `jsch` and `muxt` are tool
dependencies in `go.mod`, so `go tool` runs the pinned versions; `sqlc` must
be on your `PATH`.

Each saved document is stored twice over: the JSON exactly as the form
produced it, and the few fields the app lists, sorts and joins on, in
columns of their own. The handler that saves an issue decodes the validated
JSON into the generated `model.Issue` to fill those columns, so the schema,
the Go type and the table stay in step through the generators rather than
by hand.

The pages are muxt templates. Rather than loading the forms with htmx, the
receiver methods render a `jsonhandler.Form` into the page with
`jsonhandler.Templates()`, so every page works without JavaScript. Each form
posts to a muxt route that calls `Handler.SubmitForm`:

- an edit (adding a step, choosing an environment) answers htmx with the
  form alone and a browser with the whole page;
- a submission with errors does the same with status 422;
- a valid submission is saved through `OnSubmit` and answers with
  `HX-Redirect` for htmx or a 303 redirect for a browser.

`POST …/form/validate` routes back validation as you type.

## What to look at

- [`schemas/issue.json`](schemas/issue.json) uses most of what the form
  understands: required fields, enums as a select and as radio buttons
  (`x-form`), a date, a textarea, a checkbox group, an array of objects, and a
  `oneOf` between a browser and a server environment. Its `$defs` give `jsch`
  named types to generate (`Step`, `Browser`, `Server`).
- [`schemas/settings.json`](schemas/settings.json) has a map of label colors
  (`additionalProperties` with the color widget) and a `maxProperties` limit,
  which the form explains when it is reached.
- [`internal/hypertext/server.go`](internal/hypertext/server.go) has the one
  helper every form goes through, and turns database errors into messages
  that are safe to show; a contact's duplicate email is the only one worth
  telling the person about.
- [`internal/hypertext/app_test.go`](internal/hypertext/app_test.go) drives the
  whole app against an in-memory SQLite database.
