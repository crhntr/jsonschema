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
