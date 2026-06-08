package generate

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"
)

// variantTitle returns the title-case name of a JSON Schema simple
// type used in exported Go identifiers (e.g. "string" -> "String").
func variantTitle(kind string) string {
	if kind == "" {
		return kind
	}
	return strings.ToUpper(kind[:1]) + kind[1:]
}

// variantFlagName returns the unexported is<Kind> field that signals
// which variant of a composite struct holds the value.
func variantFlagName(kind string) string { return "is" + variantTitle(kind) }

// variantValueName returns the unexported field that carries the
// decoded value for kind. The "null" kind has no value field.
func variantValueName(kind string) string { return kind }

// emitCompositeStructType builds the `struct { isX bool; x T; ... }`
// underlying a composite type declaration. Any goAdditionalFields
// configured on the IR Type are appended after the variant fields,
// each with json:"-" so promoted members never leak into JSON.
func emitCompositeStructType(t Type) *ast.StructType {
	var fields []*ast.Field
	for _, v := range t.Variants {
		fields = append(fields, &ast.Field{
			Names: []*ast.Ident{ident(variantFlagName(v.Kind))},
			Type:  ident("bool"),
		})
		if v.GoTypeExpr != nil {
			fields = append(fields, &ast.Field{
				Names: []*ast.Ident{ident(variantValueName(v.Kind))},
				Type:  v.GoTypeExpr,
			})
		}
	}
	for _, af := range t.AdditionalFields {
		field, err := emitAdditionalField(af)
		if err != nil {
			panic(fmt.Errorf("composite additional field %+v: %w", af, err))
		}
		fields = append(fields, field)
	}
	return &ast.StructType{Fields: &ast.FieldList{List: fields}}
}

// emitCompositeAccessors returns the public Type<Kind>() / Set<Kind>()
// methods on a composite type.
func emitCompositeAccessors(t Type) []ast.Decl {
	var out []ast.Decl
	for _, v := range t.Variants {
		out = append(out, emitCompositeAccessor(t, v))
		out = append(out, emitCompositeSetter(t, v))
	}
	return out
}

// emitCompositeAccessor returns:
//
//	func (r T) Type<Kind>() (V, bool) { return r.<value>, r.<flag> }
//
// or for the null variant:
//
//	func (r T) TypeNull() bool { return r.isNull }
func emitCompositeAccessor(t Type, v Variant) *ast.FuncDecl {
	flag := &ast.SelectorExpr{X: ident("r"), Sel: ident(variantFlagName(v.Kind))}
	if v.GoTypeExpr == nil {
		return &ast.FuncDecl{
			Recv: receiverList("r", t.Name, false),
			Name: ident("Type" + variantTitle(v.Kind)),
			Type: &ast.FuncType{
				Params:  &ast.FieldList{},
				Results: &ast.FieldList{List: []*ast.Field{{Type: ident("bool")}}},
			},
			Body: &ast.BlockStmt{List: []ast.Stmt{returnStmt(flag)}},
		}
	}
	value := &ast.SelectorExpr{X: ident("r"), Sel: ident(variantValueName(v.Kind))}
	return &ast.FuncDecl{
		Recv: receiverList("r", t.Name, false),
		Name: ident("Type" + variantTitle(v.Kind)),
		Type: &ast.FuncType{
			Params: &ast.FieldList{},
			Results: &ast.FieldList{List: []*ast.Field{
				{Type: v.GoTypeExpr},
				{Type: ident("bool")},
			}},
		},
		Body: &ast.BlockStmt{List: []ast.Stmt{returnStmt(value, flag)}},
	}
}

// emitCompositeSetter returns Set<Kind> mutator methods that clear
// the receiver and assign the requested variant.
func emitCompositeSetter(t Type, v Variant) *ast.FuncDecl {
	resetStmt := &ast.AssignStmt{
		Lhs: []ast.Expr{&ast.StarExpr{X: ident("r")}},
		Tok: token.ASSIGN,
		Rhs: []ast.Expr{&ast.CompositeLit{Type: ident(t.Name)}},
	}
	flagStmt := &ast.AssignStmt{
		Lhs: []ast.Expr{&ast.SelectorExpr{X: ident("r"), Sel: ident(variantFlagName(v.Kind))}},
		Tok: token.ASSIGN,
		Rhs: []ast.Expr{ident("true")},
	}

	body := []ast.Stmt{resetStmt, flagStmt}
	var params *ast.FieldList
	if v.GoTypeExpr == nil {
		params = &ast.FieldList{}
	} else {
		params = &ast.FieldList{List: []*ast.Field{{
			Names: []*ast.Ident{ident("v")},
			Type:  v.GoTypeExpr,
		}}}
		body = append(body, &ast.AssignStmt{
			Lhs: []ast.Expr{&ast.SelectorExpr{X: ident("r"), Sel: ident(variantValueName(v.Kind))}},
			Tok: token.ASSIGN,
			Rhs: []ast.Expr{ident("v")},
		})
	}

	return &ast.FuncDecl{
		Recv: receiverList("r", t.Name, true),
		Name: ident("Set" + variantTitle(v.Kind)),
		Type: &ast.FuncType{
			Params:  params,
			Results: &ast.FieldList{},
		},
		Body: &ast.BlockStmt{List: body},
	}
}

// emitCompositeMarshal generates MarshalJSONTo for a composite type.
// The body is a switch on the active variant flag that delegates to
// json.MarshalEncode (or jsontext.Null for the null variant).
func emitCompositeMarshal(t Type) *ast.FuncDecl {
	cases := make([]ast.Stmt, 0, len(t.Variants)+1)
	for _, v := range t.Variants {
		flag := &ast.SelectorExpr{X: ident("r"), Sel: ident(variantFlagName(v.Kind))}
		var ret ast.Expr
		if v.Kind == "null" {
			ret = callExpr(
				&ast.SelectorExpr{X: ident("enc"), Sel: ident("WriteToken")},
				sel("jsontext", "Null"),
			)
		} else {
			ret = callExpr(
				sel("json", "MarshalEncode"),
				ident("enc"),
				&ast.SelectorExpr{X: ident("r"), Sel: ident(variantValueName(v.Kind))},
			)
		}
		cases = append(cases, &ast.CaseClause{
			List: []ast.Expr{flag},
			Body: []ast.Stmt{returnStmt(ret)},
		})
	}
	cases = append(cases, &ast.CaseClause{
		Body: []ast.Stmt{returnStmt(fmtErrorfCall(t.Name + ": no variant set"))},
	})

	return &ast.FuncDecl{
		Recv: receiverList("r", t.Name, false),
		Name: ident("MarshalJSONTo"),
		Type: &ast.FuncType{
			Params: &ast.FieldList{List: []*ast.Field{{
				Names: []*ast.Ident{ident("enc")},
				Type:  &ast.StarExpr{X: sel("jsontext", "Encoder")},
			}}},
			Results: &ast.FieldList{List: []*ast.Field{{Type: ident("error")}}},
		},
		Body: &ast.BlockStmt{List: []ast.Stmt{
			&ast.SwitchStmt{Body: &ast.BlockStmt{List: cases}},
		}},
	}
}

// emitCompositeUnmarshal generates UnmarshalJSONFrom for a composite
// type. The body switches on dec.PeekKind() and dispatches to a
// per-variant decode-and-assign.
func emitCompositeUnmarshal(t Type) (*ast.FuncDecl, error) {
	cases := make([]ast.Stmt, 0, len(t.Variants)+1)
	for _, v := range t.Variants {
		kinds, err := jsontextKinds(v.Kind, t.Variants)
		if err != nil {
			return nil, err
		}
		if kinds == nil {
			continue
		}
		body, err := compositeDecodeStmts(t, v)
		if err != nil {
			return nil, err
		}
		caseExprs := make([]ast.Expr, 0, len(kinds))
		for _, k := range kinds {
			caseExprs = append(caseExprs, sel("jsontext", k))
		}
		cases = append(cases, &ast.CaseClause{
			List: caseExprs,
			Body: body,
		})
	}
	cases = append(cases, &ast.CaseClause{
		Body: []ast.Stmt{returnStmt(fmtErrorfCall(
			t.Name+": unexpected JSON kind %v",
			callExpr(&ast.SelectorExpr{X: ident("dec"), Sel: ident("PeekKind")}),
		))},
	})

	return &ast.FuncDecl{
		Recv: receiverList("r", t.Name, true),
		Name: ident("UnmarshalJSONFrom"),
		Type: &ast.FuncType{
			Params: &ast.FieldList{List: []*ast.Field{{
				Names: []*ast.Ident{ident("dec")},
				Type:  &ast.StarExpr{X: sel("jsontext", "Decoder")},
			}}},
			Results: &ast.FieldList{List: []*ast.Field{{Type: ident("error")}}},
		},
		Body: &ast.BlockStmt{List: []ast.Stmt{
			&ast.SwitchStmt{
				Tag:  callExpr(&ast.SelectorExpr{X: ident("dec"), Sel: ident("PeekKind")}),
				Body: &ast.BlockStmt{List: cases},
			},
		}},
	}, nil
}

// jsontextKinds returns the jsontext.Kind constant names that map to
// the given JSON Schema simple-type kind.  Returns an error if the
// composite mixes "integer" and "number" (currently unsupported
// because both share jsontext.KindNumber).
func jsontextKinds(kind string, all []Variant) ([]string, error) {
	switch kind {
	case "string":
		return []string{"KindString"}, nil
	case "integer", "number":
		// Reject ambiguous int+float combos for now.
		seen := 0
		for _, v := range all {
			if v.Kind == "integer" || v.Kind == "number" {
				seen++
			}
		}
		if seen > 1 {
			return nil, fmt.Errorf("composite types cannot include both \"integer\" and \"number\"")
		}
		return []string{"KindNumber"}, nil
	case "boolean":
		return []string{"KindTrue", "KindFalse"}, nil
	case "null":
		return []string{"KindNull"}, nil
	case "array":
		return []string{"KindBeginArray"}, nil
	case "object":
		return []string{"KindBeginObject"}, nil
	default:
		return nil, fmt.Errorf("unsupported variant kind %q", kind)
	}
}

// compositeDecodeStmts builds the case body that decodes one variant
// and assigns *r to the matching value.
func compositeDecodeStmts(t Type, v Variant) ([]ast.Stmt, error) {
	if v.Kind == "null" {
		return []ast.Stmt{
			// _, err := dec.ReadToken(); if err != nil { return err }
			&ast.IfStmt{
				Init: &ast.AssignStmt{
					Lhs: []ast.Expr{ident("_"), ident("err")},
					Tok: token.DEFINE,
					Rhs: []ast.Expr{callExpr(&ast.SelectorExpr{X: ident("dec"), Sel: ident("ReadToken")})},
				},
				Cond: binOp(ident("err"), token.NEQ, ident("nil")),
				Body: &ast.BlockStmt{List: []ast.Stmt{returnStmt(ident("err"))}},
			},
			// *r = T{isNull: true}
			&ast.AssignStmt{
				Lhs: []ast.Expr{&ast.StarExpr{X: ident("r")}},
				Tok: token.ASSIGN,
				Rhs: []ast.Expr{&ast.CompositeLit{
					Type: ident(t.Name),
					Elts: []ast.Expr{&ast.KeyValueExpr{
						Key:   ident(variantFlagName(v.Kind)),
						Value: ident("true"),
					}},
				}},
			},
			returnStmt(ident("nil")),
		}, nil
	}
	return []ast.Stmt{
		// var v <type>
		&ast.DeclStmt{Decl: &ast.GenDecl{
			Tok: token.VAR,
			Specs: []ast.Spec{&ast.ValueSpec{
				Names: []*ast.Ident{ident("v")},
				Type:  v.GoTypeExpr,
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
		// *r = T{isX: true, x: v}
		&ast.AssignStmt{
			Lhs: []ast.Expr{&ast.StarExpr{X: ident("r")}},
			Tok: token.ASSIGN,
			Rhs: []ast.Expr{&ast.CompositeLit{
				Type: ident(t.Name),
				Elts: []ast.Expr{
					&ast.KeyValueExpr{
						Key:   ident(variantFlagName(v.Kind)),
						Value: ident("true"),
					},
					&ast.KeyValueExpr{
						Key:   ident(variantValueName(v.Kind)),
						Value: ident("v"),
					},
				},
			}},
		},
		returnStmt(ident("nil")),
	}, nil
}

// receiverList returns a method receiver `(name T)` or `(name *T)`.
func receiverList(name, typeName string, ptr bool) *ast.FieldList {
	var typ ast.Expr = ident(typeName)
	if ptr {
		typ = &ast.StarExpr{X: ident(typeName)}
	}
	return &ast.FieldList{List: []*ast.Field{{
		Names: []*ast.Ident{ident(name)},
		Type:  typ,
	}}}
}
