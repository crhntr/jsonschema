---
name: jsonhandler-forms
description: Serve an HTML form for any JSON Schema and decode submissions to valid JSON with github.com/crhntr/jsonschema/jsonhandler. Use when adding a schema-driven form to a Go web app.
---

# Schema forms with jsonhandler

Mount the partial routes (`GET {prefix}/form`, `POST {prefix}/form`,
`POST {prefix}/form/validate`):

```go
h, err := jsonhandler.New(ctx, schemaJSON) // resolves $ref
if err != nil { return err }
h.Value = current // optional starting value
h.OnSubmit = func(ctx context.Context, v jsontext.Value) (string, error) {
	return "Saved.", store.Save(ctx, v) // only called with valid values
}
jsonhandler.TemplateRoutes(mux, h, "/settings")
```

Or render it in your own page: `t, _ := jsonhandler.Templates()`, parse your
templates into `t`, call `h.Form(ctx)` or `h.SubmitForm(ctx, r.PostForm)`,
then `form.WithActions(postURL, validateURL)` and execute `jsonhandler-form`.
Build a `Handler` per request when the schema or value varies.

- Works without JavaScript. With htmx, configure `responseHandling` to swap
  422 and 413 (see `jsonhandler/README.md`).
- Serve `jsonhandler.Stylesheet()` or write your own CSS; markup is documented
  in the README.
- `readOnly` values are restored server-side; `writeOnly` values are never
  sent to the browser.
- `if`/`then`/`else`, `not`, `patternProperties` and `unevaluated*` do not
  change the form, but are still validated.

Worked example: `docs/tutorials/versioned-config-editor.md`.
