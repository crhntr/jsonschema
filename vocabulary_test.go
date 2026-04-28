package jsonschema_test

import (
	"strings"
	"testing"

	"github.com/crhntr/jsonschema"
)

// loadResolverWithMeta sets up a Resolver containing a custom
// metaschema (under metaURI) plus a schema (under docURI) that
// declares it via $schema. Returns the resolved doc schema.
func loadResolverWithMeta(t *testing.T, metaURI string, meta []byte, docURI string, doc []byte) *jsonschema.Schema {
	t.Helper()
	var r jsonschema.Resolver
	if _, err := r.Load(metaURI, meta); err != nil {
		t.Fatalf("Load meta: %v", err)
	}
	if _, err := r.Load(docURI, doc); err != nil {
		t.Fatalf("Load doc: %v", err)
	}
	resolved, err := r.Resolve(t.Context(), docURI)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return resolved
}

// metaWithVocabs produces a minimal metaschema that declares the
// given vocabulary URIs as enabled. The resource id is metaURI. We
// omit $schema on the metaschema so the resolver doesn't try to
// fetch a meta-meta-schema; that detail is irrelevant to the
// vocabulary-gating test.
func metaWithVocabs(metaURI string, vocabs ...string) []byte {
	var b strings.Builder
	b.WriteString(`{"$id":` + jsonString(metaURI) + `,"$vocabulary":{`)
	for i, v := range vocabs {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(jsonString(v) + `:true`)
	}
	b.WriteString(`}}`)
	return []byte(b.String())
}

func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func TestVocabularyValidationOff(t *testing.T) {
	metaURI := "https://example.com/meta/no-validation"
	// Metaschema declares core+applicator only (no validation).
	meta := metaWithVocabs(metaURI, jsonschema.VocabCore, jsonschema.VocabApplicator)
	docURI := "https://example.com/doc/no-validation"
	doc := []byte(`{
		"$id": ` + jsonString(docURI) + `,
		"$schema": ` + jsonString(metaURI) + `,
		"type": "string"
	}`)
	schema := loadResolverWithMeta(t, metaURI, meta, docURI, doc)

	// Without validation vocab, /type is inactive — a number is "valid".
	out := schema.Validate("inst", []byte(`42`))
	if !out.Valid {
		t.Errorf("expected valid (validation vocab off); got tree: %+v", out)
	}
}

func TestVocabularyApplicatorOff(t *testing.T) {
	metaURI := "https://example.com/meta/no-applicator"
	// Validation only, no applicator.
	meta := metaWithVocabs(metaURI, jsonschema.VocabCore, jsonschema.VocabValidation)
	docURI := "https://example.com/doc/no-applicator"
	doc := []byte(`{
		"$id": ` + jsonString(docURI) + `,
		"$schema": ` + jsonString(metaURI) + `,
		"allOf": [ { "type": "integer" } ]
	}`)
	schema := loadResolverWithMeta(t, metaURI, meta, docURI, doc)

	// Without applicator vocab, /allOf is inactive — string passes.
	out := schema.Validate("inst", []byte(`"hi"`))
	if !out.Valid {
		t.Errorf("expected valid (applicator vocab off); got tree: %+v", out)
	}
}

func TestVocabularyFormatAssertionFromMetaschema(t *testing.T) {
	metaURI := "https://example.com/meta/format-assert"
	// Declare format-assertion (instead of format-annotation).
	meta := metaWithVocabs(metaURI,
		jsonschema.VocabCore, jsonschema.VocabValidation, jsonschema.VocabFormatAssertion)
	docURI := "https://example.com/doc/format-assert"
	doc := []byte(`{
		"$id": ` + jsonString(docURI) + `,
		"$schema": ` + jsonString(metaURI) + `,
		"format": "uuid"
	}`)
	schema := loadResolverWithMeta(t, metaURI, meta, docURI, doc)

	// /format becomes an assertion via metaschema declaration alone —
	// no API toggle.
	out := schema.Validate("inst", []byte(`"not-a-uuid"`))
	if out.Valid {
		t.Errorf("expected invalid (format-assertion vocab declared); got valid")
	}
}

func TestVocabularyDefaultsAllEnabledExceptAssertion(t *testing.T) {
	// No $schema declared → implicit default vocabs (everything except
	// format-assertion).
	doc := []byte(`{
		"$id": "https://example.com/doc/default",
		"format": "uuid",
		"type": "string"
	}`)
	var r jsonschema.Resolver
	if _, err := r.Load("https://example.com/doc/default", doc); err != nil {
		t.Fatalf("Load: %v", err)
	}
	schema, err := r.Resolve(t.Context(), "https://example.com/doc/default")
	if err != nil {
		t.Fatal(err)
	}
	// Bad uuid is valid (format is annotation-only by default).
	out := schema.Validate("inst", []byte(`"not-a-uuid"`))
	if !out.Valid {
		t.Errorf("expected valid (format is annotation-only by default); tree: %+v", out)
	}
	// Wrong type should still fail (validation vocab is on by default).
	out = schema.Validate("inst", []byte(`42`))
	if out.Valid {
		t.Errorf("expected invalid (validation vocab on by default)")
	}
}
