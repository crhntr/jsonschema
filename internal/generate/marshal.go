package generate

import (
	"fmt"
	"go/ast"
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
	body := []ast.Stmt{
		// type alias T
		&ast.DeclStmt{Decl: &ast.GenDecl{
			Tok: token.TYPE,
			Specs: []ast.Spec{&ast.TypeSpec{
				Name: ident("alias"),
				Type: ident(t.Name),
			}},
		}},
		// return json.MarshalEncode(enc, alias(r))
		returnStmt(callExpr(
			sel("json", "MarshalEncode"),
			ident("enc"),
			callExpr(ident("alias"), ident("r")),
		)),
	}
	return marshalFuncDecl(t.Name, body)
}

// marshalFuncDecl builds `func (r <typeName>) MarshalJSONTo(enc *jsontext.Encoder) error`
// with the supplied body.
func marshalFuncDecl(typeName string, body []ast.Stmt) *ast.FuncDecl {
	return &ast.FuncDecl{
		Recv: &ast.FieldList{List: []*ast.Field{{
			Names: []*ast.Ident{ident("r")},
			Type:  ident(typeName),
		}}},
		Name: ident("MarshalJSONTo"),
		Type: &ast.FuncType{
			Params: &ast.FieldList{List: []*ast.Field{{
				Names: []*ast.Ident{ident("enc")},
				Type:  &ast.StarExpr{X: sel("jsontext", "Encoder")},
			}}},
			Results: &ast.FieldList{List: []*ast.Field{{Type: ident("error")}}},
		},
		Body: &ast.BlockStmt{List: body},
	}
}

// emitManualStructMarshal writes the struct as a sequence of
// jsontext tokens so wire-only NullProperty members are emitted
// alongside the regular Go fields.
func emitManualStructMarshal(t Type) ast.Decl {
	body := []ast.Stmt{
		// if err := enc.WriteToken(jsontext.BeginObject); err != nil { return err }
		ifErrReturn(encWriteToken(sel("jsontext", "BeginObject"))),
	}

	for _, f := range t.Fields {
		fieldRef := &ast.SelectorExpr{X: ident("r"), Sel: ident(f.GoName)}
		writeKey := ifErrReturn(encWriteToken(jsontextStringCall(f.JSONName)))
		if f.Required {
			body = append(body,
				writeKey,
				ifErrReturn(callExpr(sel("json", "MarshalEncode"), ident("enc"), fieldRef)),
			)
			continue
		}
		// if r.Field != nil { writeKey; json.MarshalEncode(enc, *r.Field) }
		body = append(body, &ast.IfStmt{
			Cond: binOp(fieldRef, token.NEQ, ident("nil")),
			Body: &ast.BlockStmt{List: []ast.Stmt{
				writeKey,
				ifErrReturn(callExpr(
					sel("json", "MarshalEncode"),
					ident("enc"),
					&ast.StarExpr{X: fieldRef},
				)),
			}},
		})
	}
	for _, np := range t.NullProperties {
		body = append(body,
			ifErrReturn(encWriteToken(jsontextStringCall(np.JSONName))),
			ifErrReturn(encWriteToken(sel("jsontext", "Null"))),
		)
	}

	// return enc.WriteToken(jsontext.EndObject)
	body = append(body, returnStmt(encWriteToken(sel("jsontext", "EndObject"))))

	return marshalFuncDecl(t.Name, body)
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

	shadowFields := make([]*ast.Field, 0, len(t.Fields)+len(t.NullProperties))
	for _, f := range t.Fields {
		shadowFields = append(shadowFields, &ast.Field{
			Names: []*ast.Ident{ident(f.GoName)},
			Type:  shadowFieldType(f),
			Tag:   jsonStructTag(f.JSONName),
		})
	}
	for _, np := range t.NullProperties {
		// Use jsontext.Value (a []byte), not *jsontext.Value: jsonv2
		// sets pointer-typed fields to nil when the JSON value is
		// null, which would erase presence/absence distinction.
		// A bare []byte length 0 means absent; "null" means present.
		shadowFields = append(shadowFields, &ast.Field{
			Names: []*ast.Ident{ident(nullShadowFieldName(np.JSONName))},
			Type:  sel("jsontext", "Value"),
			Tag:   jsonStructTag(np.JSONName),
		})
	}

	body := []ast.Stmt{
		// var shadow struct { ... }
		&ast.DeclStmt{Decl: &ast.GenDecl{
			Tok: token.VAR,
			Specs: []ast.Spec{&ast.ValueSpec{
				Names: []*ast.Ident{ident("shadow")},
				Type:  &ast.StructType{Fields: &ast.FieldList{List: shadowFields}},
			}},
		}},
		optsDeclStmt(t.RejectUnknown),
		// if err := json.UnmarshalDecode(dec, &shadow, opts...); err != nil { return err }
		ifErrReturn(&ast.CallExpr{
			Fun: sel("json", "UnmarshalDecode"),
			Args: []ast.Expr{
				ident("dec"),
				&ast.UnaryExpr{Op: token.AND, X: ident("shadow")},
				ident("opts"),
			},
			Ellipsis: token.Pos(1), // emits opts...
		}),
	}

	// Required-field nil checks.
	for _, f := range t.Fields {
		if !f.Required {
			continue
		}
		body = append(body, ifReturnFmtErrorf(
			binOp(shadowSel(f.GoName), token.EQL, ident("nil")),
			"missing required field %q", stringLit(f.JSONName),
		))
	}
	// Null-property checks.
	for _, np := range t.NullProperties {
		field := nullShadowFieldName(np.JSONName)
		fieldExpr := shadowSel(field)
		if np.Required {
			body = append(body, ifReturnFmtErrorf(
				binOp(callExpr(ident("len"), fieldExpr), token.EQL, intLit(0)),
				"missing required field %q", stringLit(np.JSONName),
			))
		}
		// if len(shadow.X) != 0 && string(shadow.X) != "null" { return fmt.Errorf("field %q must be null, got %s", "x", shadow.X) }
		body = append(body, ifReturnFmtErrorf(
			binOp(
				binOp(callExpr(ident("len"), fieldExpr), token.NEQ, intLit(0)),
				token.LAND,
				binOp(callExpr(ident("string"), fieldExpr), token.NEQ, stringLit("null")),
			),
			"field %q must be null, got %s", stringLit(np.JSONName), fieldExpr,
		))
	}
	body = append(body, dependentRequiredCheckStmts(t)...)

	// Assignments back into the receiver.
	for _, f := range t.Fields {
		rhs := ast.Expr(shadowSel(f.GoName))
		if f.Required {
			rhs = &ast.StarExpr{X: rhs}
		}
		body = append(body, &ast.AssignStmt{
			Lhs: []ast.Expr{&ast.SelectorExpr{X: ident("r"), Sel: ident(f.GoName)}},
			Tok: token.ASSIGN,
			Rhs: []ast.Expr{rhs},
		})
	}
	body = append(body, returnStmt(ident("nil")))

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

// optsDeclStmt builds the `opts` declaration: an empty `[]json.Options`
// by default, or one preloaded with `json.RejectUnknownMembers(true)`
// when the struct rejects unknown members.
func optsDeclStmt(reject bool) ast.Stmt {
	if !reject {
		return &ast.DeclStmt{Decl: &ast.GenDecl{
			Tok: token.VAR,
			Specs: []ast.Spec{&ast.ValueSpec{
				Names: []*ast.Ident{ident("opts")},
				Type:  &ast.ArrayType{Elt: sel("json", "Options")},
			}},
		}}
	}
	return &ast.AssignStmt{
		Lhs: []ast.Expr{ident("opts")},
		Tok: token.DEFINE,
		Rhs: []ast.Expr{&ast.CompositeLit{
			Type: &ast.ArrayType{Elt: sel("json", "Options")},
			Elts: []ast.Expr{callExpr(sel("json", "RejectUnknownMembers"), ident("true"))},
		}},
	}
}

// shadowSel returns `shadow.<name>`.
func shadowSel(name string) *ast.SelectorExpr {
	return &ast.SelectorExpr{X: ident("shadow"), Sel: ident(name)}
}

// jsonStructTag returns a struct tag basicLit holding `json:"<name>"`.
func jsonStructTag(name string) *ast.BasicLit {
	return &ast.BasicLit{Kind: token.STRING, Value: fmt.Sprintf("`json:%s`", strconv.Quote(name))}
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

// dependentRequiredCheckStmts returns one guard per (parent, dep)
// pair in t.DependentRequired: if the parent property was present and
// the dep was not, return an error. Required properties never trigger
// the check because the missing-required guard above would have fired
// first.
func dependentRequiredCheckStmts(t Type) []ast.Stmt {
	if len(t.DependentRequired) == 0 {
		return nil
	}
	parents := make([]string, 0, len(t.DependentRequired))
	for k := range t.DependentRequired {
		parents = append(parents, k)
	}
	sort.Strings(parents)

	jsonToShadow := map[string]*ast.SelectorExpr{}
	for _, f := range t.Fields {
		jsonToShadow[f.JSONName] = shadowSel(f.GoName)
	}
	for _, np := range t.NullProperties {
		jsonToShadow[np.JSONName] = shadowSel(nullShadowFieldName(np.JSONName))
	}

	var stmts []ast.Stmt
	for _, parent := range parents {
		parentExpr, ok := jsonToShadow[parent]
		if !ok {
			continue
		}
		var parentPresent ast.Expr
		if isNullShadow(t, parent) {
			parentPresent = binOp(callExpr(ident("len"), parentExpr), token.NEQ, intLit(0))
		} else {
			parentPresent = binOp(parentExpr, token.NEQ, ident("nil"))
		}
		for _, dep := range t.DependentRequired[parent] {
			depExpr, ok := jsonToShadow[dep]
			if !ok {
				continue
			}
			var depAbsent ast.Expr
			if isNullShadow(t, dep) {
				depAbsent = binOp(callExpr(ident("len"), depExpr), token.EQL, intLit(0))
			} else {
				depAbsent = binOp(depExpr, token.EQL, ident("nil"))
			}
			stmts = append(stmts, ifReturnFmtErrorf(
				binOp(parentPresent, token.LAND, depAbsent),
				"property %q requires %q",
				stringLit(parent), stringLit(dep),
			))
		}
	}
	return stmts
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
func shadowFieldType(f Field) ast.Expr {
	if f.Required {
		return &ast.StarExpr{X: f.TypeExpr}
	}
	return f.TypeExpr
}
