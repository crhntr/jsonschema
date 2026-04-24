package docs

//go:generate mkdir -p 2020-12
//go:generate curl -sS --fail --skip-existing -o 2020-12/index.html             https://json-schema.org/draft/2020-12/json-schema-core
//go:generate curl -sS --fail --skip-existing -o 2020-12/schema.json              https://json-schema.org/draft/2020-12/schema
//go:generate curl -sS --fail --skip-existing -o 2020-12/core.json              https://json-schema.org/draft/2020-12/meta/core
//go:generate curl -sS --fail --skip-existing -o 2020-12/applicator.json        https://json-schema.org/draft/2020-12/meta/applicator
//go:generate curl -sS --fail --skip-existing -o 2020-12/unevaluated.json       https://json-schema.org/draft/2020-12/meta/unevaluated
//go:generate curl -sS --fail --skip-existing -o 2020-12/validation.json        https://json-schema.org/draft/2020-12/meta/validation
//go:generate curl -sS --fail --skip-existing -o 2020-12/meta-data.json         https://json-schema.org/draft/2020-12/meta/meta-data
//go:generate curl -sS --fail --skip-existing -o 2020-12/format-annotation.json https://json-schema.org/draft/2020-12/meta/format-annotation
//go:generate curl -sS --fail --skip-existing -o 2020-12/content.json           https://json-schema.org/draft/2020-12/meta/content
