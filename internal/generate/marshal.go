package generate

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

// EmitMarshal returns the MarshalJSONTo method for t. By default it
// delegates to encoding/json/v2 via a local alias type so generated
// code does not recurse into itself. Structs that carry
// NullProperties switch to manual token-by-token writing because
// null-only members have no Go field for the alias to encode.
func EmitMarshal(t Type) ast.Decl {
	if len(t.NullProperties) > 0 && t.Underlying == nil && len(t.Variants) == 0 {
		return emitManualStructMarshal(t)
	}
	src := fmt.Sprintf(`package _

func (r %[1]s) MarshalJSONTo(enc *jsontext.Encoder) error {
	type alias %[1]s
	return json.MarshalEncode(enc, alias(r))
}
`, t.Name)
	return parseDecl(src)
}

// emitManualStructMarshal writes the struct as a sequence of
// jsontext tokens so wire-only NullProperty members are emitted
// alongside the regular Go fields.
func emitManualStructMarshal(t Type) ast.Decl {
	var body strings.Builder
	body.WriteString("\tif err := enc.WriteToken(jsontext.BeginObject); err != nil {\n\t\treturn err\n\t}\n")

	for _, f := range t.Fields {
		if f.Required {
			fmt.Fprintf(&body, "\tif err := enc.WriteToken(jsontext.String(%q)); err != nil {\n\t\treturn err\n\t}\n", f.JSONName)
			fmt.Fprintf(&body, "\tif err := json.MarshalEncode(enc, r.%s); err != nil {\n\t\treturn err\n\t}\n", f.GoName)
		} else {
			fmt.Fprintf(&body, "\tif r.%s != nil {\n", f.GoName)
			fmt.Fprintf(&body, "\t\tif err := enc.WriteToken(jsontext.String(%q)); err != nil {\n\t\t\treturn err\n\t\t}\n", f.JSONName)
			fmt.Fprintf(&body, "\t\tif err := json.MarshalEncode(enc, *r.%s); err != nil {\n\t\t\treturn err\n\t\t}\n", f.GoName)
			body.WriteString("\t}\n")
		}
	}
	for _, np := range t.NullProperties {
		fmt.Fprintf(&body, "\tif err := enc.WriteToken(jsontext.String(%q)); err != nil {\n\t\treturn err\n\t}\n", np.JSONName)
		body.WriteString("\tif err := enc.WriteToken(jsontext.Null); err != nil {\n\t\treturn err\n\t}\n")
	}

	body.WriteString("\treturn enc.WriteToken(jsontext.EndObject)\n")

	src := fmt.Sprintf(`package _

func (r %[1]s) MarshalJSONTo(enc *jsontext.Encoder) error {
%[2]s}
`, t.Name, body.String())
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
	for _, np := range t.NullProperties {
		// Use jsontext.Value (a []byte), not *jsontext.Value: jsonv2
		// sets pointer-typed fields to nil when the JSON value is
		// null, which would erase presence/absence distinction.
		// A bare []byte length 0 means absent; "null" means present.
		fmt.Fprintf(&shadowFields, "\t\t%s jsontext.Value `json:%q`\n", nullShadowFieldName(np.JSONName), np.JSONName)
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
	for _, np := range t.NullProperties {
		field := nullShadowFieldName(np.JSONName)
		if np.Required {
			fmt.Fprintf(&checks, "\tif len(shadow.%s) == 0 {\n\t\treturn fmt.Errorf(\"missing required field %%q\", %q)\n\t}\n", field, np.JSONName)
		}
		fmt.Fprintf(&checks, "\tif len(shadow.%[1]s) != 0 && string(shadow.%[1]s) != \"null\" {\n\t\treturn fmt.Errorf(\"field %%q must be null, got %%s\", %[2]q, shadow.%[1]s)\n\t}\n", field, np.JSONName)
	}
	emitDependentRequiredChecks(&checks, t)

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
// The function body is built from go/ast nodes (not string templates)
// so the constraint checks are individually inspectable AST and the
// compiler validates each at generation time.
func emitScalarUnmarshal(t Type) ast.Decl {
	body := []ast.Stmt{
		// var v <underlying>
		&ast.DeclStmt{Decl: &ast.GenDecl{
			Tok: token.VAR,
			Specs: []ast.Spec{&ast.ValueSpec{
				Names: []*ast.Ident{ident("v")},
				Type:  t.Underlying,
			}},
		}},
		// if err := json.UnmarshalDecode(dec, &v); err != nil { return err }
		&ast.IfStmt{
			Init: &ast.AssignStmt{
				Lhs: []ast.Expr{ident("err")},
				Tok: token.DEFINE,
				Rhs: []ast.Expr{callExpr(
					sel("json", "UnmarshalDecode"),
					ident("dec"),
					&ast.UnaryExpr{Op: token.AND, X: ident("v")},
				)},
			},
			Cond: binOp(ident("err"), token.NEQ, ident("nil")),
			Body: &ast.BlockStmt{List: []ast.Stmt{returnStmt(ident("err"))}},
		},
	}
	body = append(body, scalarConstraintChecks(t)...)
	body = append(body,
		// *r = T(v)
		&ast.AssignStmt{
			Lhs: []ast.Expr{&ast.StarExpr{X: ident("r")}},
			Tok: token.ASSIGN,
			Rhs: []ast.Expr{callExpr(ident(t.Name), ident("v"))},
		},
		// return nil
		returnStmt(ident("nil")),
	)

	return &ast.FuncDecl{
		Recv: &ast.FieldList{List: []*ast.Field{{
			Names: []*ast.Ident{ident("r")},
			Type:  &ast.StarExpr{X: ident(t.Name)},
		}}},
		Name: ident("UnmarshalJSONFrom"),
		Type: &ast.FuncType{
			Params: &ast.FieldList{List: []*ast.Field{{
				Names: []*ast.Ident{ident("dec")},
				Type:  &ast.StarExpr{X: sel("jsontext", "Decoder")},
			}}},
			Results: &ast.FieldList{List: []*ast.Field{{Type: ident("error")}}},
		},
		Body: &ast.BlockStmt{List: body},
	}
}

// scalarConstraintChecks builds AST statements that enforce the
// constraint keywords on t. Each check looks like:
//
//	if <cond> { return fmt.Errorf(...) }
//
// and runs against the locally-bound variable v.
func scalarConstraintChecks(t Type) []ast.Stmt {
	var stmts []ast.Stmt
	c := t.Constraints
	lenV := callExpr(ident("len"), ident("v"))

	if c.MinLength != nil {
		stmts = append(stmts, ifReturnFmtErrorf(
			binOp(lenV, token.LSS, intLit(*c.MinLength)),
			t.Name+": length %d below minimum "+strconv.Itoa(*c.MinLength),
			lenV,
		))
	}
	if c.MaxLength != nil {
		stmts = append(stmts, ifReturnFmtErrorf(
			binOp(lenV, token.GTR, intLit(*c.MaxLength)),
			t.Name+": length %d above maximum "+strconv.Itoa(*c.MaxLength),
			lenV,
		))
	}
	if c.Minimum != nil {
		stmts = append(stmts, ifReturnFmtErrorf(
			binOp(ident("v"), token.LSS, rawNumLit(*c.Minimum)),
			t.Name+": value %v below minimum "+*c.Minimum,
			ident("v"),
		))
	}
	if c.Maximum != nil {
		stmts = append(stmts, ifReturnFmtErrorf(
			binOp(ident("v"), token.GTR, rawNumLit(*c.Maximum)),
			t.Name+": value %v above maximum "+*c.Maximum,
			ident("v"),
		))
	}
	if c.Pattern != "" {
		stmts = append(stmts, ifReturnFmtErrorf(
			&ast.UnaryExpr{
				Op: token.NOT,
				X: callExpr(
					&ast.SelectorExpr{X: ident(patternVarName(t.Name)), Sel: ident("MatchString")},
					ident("v"),
				),
			},
			t.Name+": value %q does not match pattern "+c.Pattern,
			ident("v"),
		))
	}
	if len(c.Enum) > 0 {
		stmts = append(stmts, enumCheckStmt(t.Name, c.Enum))
	}
	return stmts
}

// patternVarName is the package-level identifier holding the
// compiled *regexp.Regexp for type t.
func patternVarName(typeName string) string {
	return strings.ToLower(typeName[:1]) + typeName[1:] + "Pattern"
}

// EmitPatternVar returns `var <typeNameLower>Pattern =
// regexp.MustCompile("<pattern>")` for t, or nil if t has no pattern.
func EmitPatternVar(t Type) ast.Decl {
	if t.Constraints.Pattern == "" {
		return nil
	}
	return &ast.GenDecl{
		Tok: token.VAR,
		Specs: []ast.Spec{&ast.ValueSpec{
			Names: []*ast.Ident{ident(patternVarName(t.Name))},
			Values: []ast.Expr{callExpr(
				sel("regexp", "MustCompile"),
				stringLit(t.Constraints.Pattern),
			)},
		}},
	}
}

// enumCheckStmt builds:
//
//	switch v {
//	case <e1>, <e2>, …:
//	default:
//		return fmt.Errorf("T: value %v not in enum", v)
//	}
//
// where each <eN> is a Go literal whose source is the raw JSON text
// of the enum entry.
func enumCheckStmt(typeName string, enum []string) ast.Stmt {
	cases := make([]ast.Expr, 0, len(enum))
	for _, raw := range enum {
		cases = append(cases, enumLiteral(raw))
	}
	return &ast.SwitchStmt{
		Tag: ident("v"),
		Body: &ast.BlockStmt{List: []ast.Stmt{
			&ast.CaseClause{List: cases}, // matched: no body
			&ast.CaseClause{Body: []ast.Stmt{returnStmt(
				fmtErrorfCall(typeName+": value %v not in enum", ident("v")),
			)}},
		}},
	}
}

// enumLiteral converts a raw JSON enum entry to a Go expression.
// JSON strings stay quoted; numbers / true / false / null splice
// through as their raw text.
func enumLiteral(raw string) ast.Expr {
	if len(raw) > 0 && raw[0] == '"' {
		// JSON's interior-string escapes (\", \\, \n, \uXXXX, …) are
		// a subset of Go's, so the JSON text doubles as a valid Go
		// double-quoted string literal for the values seen in
		// schemas. \/ would need translation if it ever appeared.
		return &ast.BasicLit{Kind: token.STRING, Value: raw}
	}
	switch raw {
	case "true", "false", "null":
		return ident(raw)
	}
	return rawNumLit(raw)
}

// emitDependentRequiredChecks appends a guard per (parent, dep) pair
// in t.DependentRequired: if the parent property was present and the
// dep was not, return an error. Required properties never trigger
// the check because the missing-required guard above would have
// fired first.
func emitDependentRequiredChecks(buf *strings.Builder, t Type) {
	if len(t.DependentRequired) == 0 {
		return
	}
	parents := make([]string, 0, len(t.DependentRequired))
	for k := range t.DependentRequired {
		parents = append(parents, k)
	}
	sort.Strings(parents)

	jsonToShadow := map[string]string{}
	for _, f := range t.Fields {
		jsonToShadow[f.JSONName] = "shadow." + f.GoName
	}
	for _, np := range t.NullProperties {
		jsonToShadow[np.JSONName] = "shadow." + nullShadowFieldName(np.JSONName)
	}

	for _, parent := range parents {
		parentExpr, ok := jsonToShadow[parent]
		if !ok {
			continue
		}
		parentPresence := parentExpr + " != nil"
		if isNullShadow(t, parent) {
			parentPresence = "len(" + parentExpr + ") != 0"
		}
		for _, dep := range t.DependentRequired[parent] {
			depExpr, ok := jsonToShadow[dep]
			if !ok {
				continue
			}
			depAbsence := depExpr + " == nil"
			if isNullShadow(t, dep) {
				depAbsence = "len(" + depExpr + ") == 0"
			}
			fmt.Fprintf(buf, "\tif %s && %s {\n\t\treturn fmt.Errorf(\"property %%q requires %%q\", %q, %q)\n\t}\n",
				parentPresence, depAbsence, parent, dep)
		}
	}
}

// isNullShadow reports whether a JSON property is represented by
// the jsontext.Value-typed shadow field used for null properties.
func isNullShadow(t Type, jsonName string) bool {
	for _, np := range t.NullProperties {
		if np.JSONName == jsonName {
			return true
		}
	}
	return false
}

// nullShadowFieldName is the shadow-struct field name carrying a
// jsontext.Value for a wire-only null property. The leading
// underscore prefix avoids collisions with regular Go fields.
func nullShadowFieldName(jsonName string) string {
	return "Null_" + exportedIdent(jsonName)
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
