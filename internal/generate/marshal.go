package generate

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// EmitMarshal returns the MarshalJSONTo method for t. The body
// delegates to encoding/json/v2 via a local alias type so generated
// code does not recurse into itself.
func EmitMarshal(t Type) ast.Decl {
	src := fmt.Sprintf(`package _

func (r %[1]s) MarshalJSONTo(enc *jsontext.Encoder) error {
	type alias %[1]s
	return json.MarshalEncode(enc, alias(r))
}
`, t.Name)
	return parseDecl(src)
}

// EmitUnmarshal returns the UnmarshalJSONFrom method for t. For
// struct types it decodes into a pointer-shadow struct, rejects nil
// values for required fields, and copies the rest into the receiver.
// For scalar types it decodes the underlying primitive and enforces
// length / range constraints before assigning the receiver.
// additionalProperties: false is enforced via
// json.RejectUnknownMembers when t.RejectUnknown is set.
func EmitUnmarshal(t Type) ast.Decl {
	if t.Underlying != nil {
		return emitScalarUnmarshal(t)
	}
	var shadowFields strings.Builder
	for _, f := range t.Fields {
		shadowFields.WriteString("\t\t")
		shadowFields.WriteString(f.GoName)
		shadowFields.WriteString(" ")
		shadowFields.WriteString(shadowFieldType(f))
		shadowFields.WriteString(" `json:")
		shadowFields.WriteString(fmt.Sprintf("%q", f.JSONName))
		shadowFields.WriteString("`\n")
	}

	var optsExtra string
	if t.RejectUnknown {
		optsExtra = "\topts := []json.Options{json.RejectUnknownMembers(true)}\n"
	} else {
		optsExtra = "\tvar opts []json.Options\n"
	}

	var checks strings.Builder
	for _, f := range t.Fields {
		if f.Required {
			fmt.Fprintf(&checks, "\tif shadow.%s == nil {\n\t\treturn fmt.Errorf(\"missing required field %%q\", %q)\n\t}\n", f.GoName, f.JSONName)
		}
	}

	var assigns strings.Builder
	for _, f := range t.Fields {
		if f.Required {
			fmt.Fprintf(&assigns, "\tr.%s = *shadow.%s\n", f.GoName, f.GoName)
		} else {
			fmt.Fprintf(&assigns, "\tr.%s = shadow.%s\n", f.GoName, f.GoName)
		}
	}

	src := fmt.Sprintf(`package _

func (r *%[1]s) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var shadow struct {
%[2]s	}
%[3]s	if err := json.UnmarshalDecode(dec, &shadow, opts...); err != nil {
		return err
	}
%[4]s%[5]s	return nil
}
`, t.Name, shadowFields.String(), optsExtra, checks.String(), assigns.String())
	return parseDecl(src)
}

// emitScalarUnmarshal generates UnmarshalJSONFrom for a scalar
// alias type, enforcing the constraints attached to the IR Type.
func emitScalarUnmarshal(t Type) ast.Decl {
	underlying := exprString(t.Underlying)
	var checks strings.Builder
	if t.Constraints.MinLength != nil {
		fmt.Fprintf(&checks, "\tif len(v) < %d {\n\t\treturn fmt.Errorf(\"%s: length %%d below minimum %d\", len(v))\n\t}\n", *t.Constraints.MinLength, t.Name, *t.Constraints.MinLength)
	}
	if t.Constraints.MaxLength != nil {
		fmt.Fprintf(&checks, "\tif len(v) > %d {\n\t\treturn fmt.Errorf(\"%s: length %%d above maximum %d\", len(v))\n\t}\n", *t.Constraints.MaxLength, t.Name, *t.Constraints.MaxLength)
	}
	src := fmt.Sprintf(`package _

func (r *%[1]s) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var v %[2]s
	if err := json.UnmarshalDecode(dec, &v); err != nil {
		return err
	}
%[3]s	*r = %[1]s(v)
	return nil
}
`, t.Name, underlying, checks.String())
	return parseDecl(src)
}

// shadowFieldType returns the type expression for the corresponding
// field on the unmarshal shadow struct. Required fields are pointed
// to (so a missing key is observable), optional fields keep their
// emitted type (already *T from Phase 4).
func shadowFieldType(f Field) string {
	expr := exprString(f.TypeExpr)
	if f.Required {
		return "*" + expr
	}
	return expr
}

func parseDecl(src string) ast.Decl {
	file, err := parser.ParseFile(token.NewFileSet(), "", src, 0)
	if err != nil {
		panic(fmt.Errorf("parse generated decl: %w\nsrc:\n%s", err, src))
	}
	return file.Decls[0]
}

// exprString prints a type expression to its Go source form. Used
// to embed field types into the templated shadow struct.
func exprString(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.StarExpr:
		return "*" + exprString(x.X)
	case *ast.SelectorExpr:
		return exprString(x.X) + "." + x.Sel.Name
	case *ast.ArrayType:
		return "[]" + exprString(x.Elt)
	case *ast.MapType:
		return "map[" + exprString(x.Key) + "]" + exprString(x.Value)
	default:
		panic(fmt.Errorf("unsupported type expression %T", e))
	}
}
