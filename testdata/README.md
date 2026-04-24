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

- `JSON-Schema-Test-Suite/` — selected files copied from
  [json-schema-org/JSON-Schema-Test-Suite](https://github.com/json-schema-org/JSON-Schema-Test-Suite)
  (MIT-licensed; full text in `JSON-Schema-Test-Suite/LICENSE`).
  - `tests-draft2020-12/` — the draft 2020-12 conformance tests
    (`tests/draft2020-12/` upstream). Driven by `TestConformanceLoadsCleanly`.
  - `remotes/` — schemas the conformance tests fetch over HTTP under the
    fictitious base `http://localhost:1234/`. Mirror these via the test
    harness when wiring full $ref-resolving conformance later.

## Refreshing the conformance suite

```
git clone --depth 1 https://github.com/json-schema-org/JSON-Schema-Test-Suite /tmp/JSON-Schema-Test-Suite
rm -rf testdata/JSON-Schema-Test-Suite/{tests-draft2020-12,remotes,LICENSE}
cp -R /tmp/JSON-Schema-Test-Suite/tests/draft2020-12 testdata/JSON-Schema-Test-Suite/tests-draft2020-12
cp -R /tmp/JSON-Schema-Test-Suite/remotes           testdata/JSON-Schema-Test-Suite/remotes
cp    /tmp/JSON-Schema-Test-Suite/LICENSE           testdata/JSON-Schema-Test-Suite/LICENSE
```
