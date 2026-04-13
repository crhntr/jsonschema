package spec

//go:generate mkdir -p drafts/2019-09
//go:generate curl -sS --fail --skip-existing -o drafts/2019-09/index.html https://json-schema.org/draft/2019-09/draft-handrews-json-schema-02
//go:generate curl -sS --fail --skip-existing -o drafts/2019-09/meta.json https://json-schema.org/draft/2019-09/schema

//go:generate mkdir -p drafts/2020-12
//go:generate curl -sS --fail --skip-existing -o drafts/2020-12/index.html https://json-schema.org/draft/2020-12/json-schema-core
//go:generate curl -sS --fail --skip-existing -o drafts/2020-12/meta.json https://json-schema.org/draft/2020-12/schema
