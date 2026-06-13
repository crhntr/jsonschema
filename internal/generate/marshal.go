package generate

import (
	"fmt"
	"go/ast"
	"go/token"
	"sort"
	"strconv"
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
		// Optional fields are *T for scalars/structs (so absent vs
		// zero is preserved) but bare slice/map for collections
		// (whose nil zero already encodes absence). Dereference only
		// the pointer case so the wire output is the underlying
		// value either way.
		var encodeArg ast.Expr = fieldRef
		if _, isPtr := f.TypeExpr.(*ast.StarExpr); isPtr {
			encodeArg = &ast.StarExpr{X: fieldRef}
		}
		body = append(body, &ast.IfStmt{
			Cond: binOp(fieldRef, token.NEQ, ident("nil")),
			Body: &ast.BlockStmt{List: []ast.Stmt{
				writeKey,
				ifErrReturn(callExpr(
					sel("json", "MarshalEncode"),
					ident("enc"),
					encodeArg,
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

// EmitUnmarshal returns the UnmarshalJSONFrom method for a struct
// type t. It decodes into a pointer-shadow struct, rejects nil values
// for required fields, and copies the rest into the receiver.
// additionalProperties: false is enforced via
// json.RejectUnknownMembers when t.RejectUnknown is set. Scalar types
// carry no UnmarshalJSONFrom — they decode structurally as their
// underlying primitive (see emitTypeDecls).
func EmitUnmarshal(t Type) ast.Decl {
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
