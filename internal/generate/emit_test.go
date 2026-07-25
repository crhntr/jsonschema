package generate

import (
	"go/ast"
	"strings"
	"testing"
)

// mustEmit calls Emit and fails the test on error, returning the type
// declaration. Most emit tests build IR Types with valid goTypes, so
// the error is not the behavior under test.
func mustEmit(t *testing.T, typ Type) ast.Decl {
	t.Helper()
	decl, err := Emit(typ)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	return decl
}

// TestEmit_OptionalFieldWithTagsGetsOmitzero guards that an optional
// field carrying explicit goJSONTags (e.g. a format flag) still gets
// omitzero appended, so its zero value does not leak into the marshaled
// output. Without this, an optional time.Time field emits
// "0001-01-01T00:00:00Z" instead of being omitted.
func TestEmit_OptionalFieldWithTagsGetsOmitzero(t *testing.T) {
	typ := Type{
		Name: "Event",
		Fields: []Field{
			{
				GoName:   "CreatedAt",
				JSONName: "createdAt",
				TypeExpr: &ast.SelectorExpr{X: &ast.Ident{Name: "time"}, Sel: &ast.Ident{Name: "Time"}},
				Required: false,
				JSONTags: []string{"case:ignore"},
			},
		},
	}
	src, err := formatFile("model", []ast.Decl{mustEmit(t, typ)})
	if err != nil {
		t.Fatalf("formatFile: %v", err)
	}
	// omitzero leads the option list; author-supplied options keep
	// their position after it.
	want := "json:\"createdAt,omitzero,case:ignore\""
	if !strings.Contains(src, want) {
		t.Errorf("optional field tag wrong\nwant substring: %s\nsrc:\n%s", want, src)
	}
}

// TestEmit_OptionalFieldWithOmitemptyKeepsTag guards that an optional
// field whose author already chose omitempty is left as-is: omitzero is
// not also appended.
func TestEmit_OptionalFieldWithOmitemptyKeepsTag(t *testing.T) {
	typ := Type{
		Name: "Event",
		Fields: []Field{
			{
				GoName:   "Id",
				JSONName: "id",
				TypeExpr: &ast.Ident{Name: "string"},
				Required: false,
				JSONTags: []string{"omitempty"},
			},
		},
	}
	src, err := formatFile("model", []ast.Decl{mustEmit(t, typ)})
	if err != nil {
		t.Fatalf("formatFile: %v", err)
	}
	if strings.Contains(src, "omitzero") {
		t.Errorf("omitempty field should not also get omitzero\nsrc:\n%s", src)
	}
	if !strings.Contains(src, "json:\"id,omitempty\"") {
		t.Errorf("expected json:\"id,omitempty\"\nsrc:\n%s", src)
	}
}

// TestEmit_InvalidAdditionalFieldGoType guards that a malformed
// additional-field goType surfaces as a returned error rather than a
// panic deep in the emit pass. AdditionalFields can originate from
// user-provided overrides, so a bad expression must be a normal
// diagnostic.
func TestEmit_InvalidAdditionalFieldGoType(t *testing.T) {
	typ := Type{
		Name:             "Doc",
		AdditionalFields: []GoAdditionalField{{GoIdent: stringList{"bad"}, GoType: "chan chan"}},
	}
	_, err := Emit(typ)
	if err == nil {
		t.Fatalf("Emit: err = nil, want error for invalid additional-field goType")
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Errorf("Emit: err = %v, want mention of the offending field", err)
	}
}
