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

Or handle the requests yourself, which suits an edit page that saves and
redirects:

```go
f, err := h.WithValue(stored).SubmitForm(ctx, r.PostForm) // WithValue copies h
if f.Valid {
	if err := save(f.Value); err == nil { /* redirect */ return }
	f.Reject(errors.New("Could not save. Try again.")) // back to the form, 422
}
html, err := f.WithActions(postURL, validateURL).HTML() // template.HTML
```

`f.ValidationHTML()` answers the validate route. To change the markup instead,
parse your templates into `jsonhandler.Templates()` and execute
`jsonhandler-form`.

- `NewAt(ctx, doc, "/$defs/job")` edits one definition of a larger schema.
- A `Handler` has no per-request state; share it and use `WithValue`.
- Works without JavaScript. With htmx, configure `responseHandling` to swap
  422 and 413 (see `jsonhandler/README.md`).
- Serve `jsonhandler.Stylesheet()` or write your own CSS; markup is documented
  in the README.
- `x-form` adjusts presentation: `widget`, `label`, `placeholder`, `order`,
  `class`, `collapsed`, `rows`, and `messages` (your wording for a failing
  keyword, such as `{"pattern": "Use 30s, 5m or 1h."}`).
- `readOnly` values (and everything inside them) are fixed; `writeOnly` values
  are never sent to the browser.
- `if`/`then`/`else`, `not`, `patternProperties` and `unevaluated*` do not
  change the form, but are still validated.

Worked examples: `docs/tutorials/versioned-config-editor.md`,
`examples/concourse` and `examples/issues`.
