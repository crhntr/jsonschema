package jsonschema

//go:generate mkdir -p testdata/schema/json-schema.org/draft/2020-12 testdata/schema/json-schema.org/draft/2020-12/meta
//go:generate curl -sS --fail --skip-existing -o testdata/schema/json-schema.org/draft/2020-12/schema.json            https://json-schema.org/draft/2020-12/schema
//go:generate curl -sS --fail --skip-existing -o testdata/schema/json-schema.org/draft/2020-12/meta/core.json              https://json-schema.org/draft/2020-12/meta/core
//go:generate curl -sS --fail --skip-existing -o testdata/schema/json-schema.org/draft/2020-12/meta/applicator.json        https://json-schema.org/draft/2020-12/meta/applicator
//go:generate curl -sS --fail --skip-existing -o testdata/schema/json-schema.org/draft/2020-12/meta/unevaluated.json       https://json-schema.org/draft/2020-12/meta/unevaluated
//go:generate curl -sS --fail --skip-existing -o testdata/schema/json-schema.org/draft/2020-12/meta/validation.json        https://json-schema.org/draft/2020-12/meta/validation
//go:generate curl -sS --fail --skip-existing -o testdata/schema/json-schema.org/draft/2020-12/meta/meta-data.json         https://json-schema.org/draft/2020-12/meta/meta-data
//go:generate curl -sS --fail --skip-existing -o testdata/schema/json-schema.org/draft/2020-12/meta/format-annotation.json https://json-schema.org/draft/2020-12/meta/format-annotation
//go:generate curl -sS --fail --skip-existing -o testdata/schema/json-schema.org/draft/2020-12/meta/content.json           https://json-schema.org/draft/2020-12/meta/content
