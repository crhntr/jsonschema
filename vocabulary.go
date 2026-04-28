package jsonschema

// Vocabulary URIs defined by JSON Schema 2020-12.
const (
	VocabCore             = "https://json-schema.org/draft/2020-12/vocab/core"
	VocabApplicator       = "https://json-schema.org/draft/2020-12/vocab/applicator"
	VocabValidation       = "https://json-schema.org/draft/2020-12/vocab/validation"
	VocabMetaData         = "https://json-schema.org/draft/2020-12/vocab/meta-data"
	VocabFormatAnnotation = "https://json-schema.org/draft/2020-12/vocab/format-annotation"
	VocabFormatAssertion  = "https://json-schema.org/draft/2020-12/vocab/format-assertion"
	VocabContent          = "https://json-schema.org/draft/2020-12/vocab/content"
	VocabUnevaluated      = "https://json-schema.org/draft/2020-12/vocab/unevaluated"
)

// defaultVocabularies is the implicit vocabulary set when a schema does
// not declare $schema or its metaschema has no $vocabulary. Per the
// 2020-12 standard, the format-assertion vocabulary is opt-in: it
// requires explicit declaration in the metaschema's $vocabulary.
var defaultVocabularies = map[string]bool{
	VocabCore:             true,
	VocabApplicator:       true,
	VocabValidation:       true,
	VocabMetaData:         true,
	VocabFormatAnnotation: true,
	VocabContent:          true,
	VocabUnevaluated:      true,
}

// vocabEnabled reports whether vocab is enabled for resource. nil
// vocabularies means "implicit default" — every 2020-12 vocab except
// format-assertion is on. A populated map represents the explicit
// $vocabulary declaration from the resource's metaschema.
func vocabEnabled(vocabularies map[string]bool, vocab string) bool {
	if vocabularies == nil {
		return defaultVocabularies[vocab]
	}
	_, ok := vocabularies[vocab]
	return ok
}
