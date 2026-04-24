# testdata

Read-only fixtures used by the package tests.

## Layout

- `schema/` — JSON Schema documents served by the local httptest harness in
  `testdata_test.go`. The test client rewrites every request URL to the
  harness, forwards the original Host as `X-Original-Host`, and serves
  `schema/<host><path>.json` from disk.
  - `schema/json-schema.org/draft/2020-12/` — the official 2020-12
    meta-schema and its seven vocabulary documents, served at
    `https://json-schema.org/draft/2020-12/...`.
  - `schema/example.com/` — small synthetic schemas used to exercise
    fragment resolution kinds: JSON Pointer, `$anchor`, embedded
    resources, and `$dynamicRef` bookending.
