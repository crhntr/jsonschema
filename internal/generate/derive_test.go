package generate

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/crhntr/jsonschema"
)

func TestDeriveAndEmit_SimpleStruct(t *testing.T) {
	src := `{
		"type": "object",
		"description": "A user record.",
		"properties": {
			"name": {"type": "string"},
			"age": {"type": "integer"},
			"score": {"type": "number"},
			"active": {"type": "boolean"}
		},
		"required": ["name", "age", "score", "active"]
	}`
	s, err := jsonschema.Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	obj, _ := s.TypeObject()

	typ, err := Derive("User", &obj)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if typ.Name != "User" {
		t.Errorf("Name = %q, want %q", typ.Name, "User")
	}
	if typ.Doc != "A user record." {
		t.Errorf("Doc = %q, want %q", typ.Doc, "A user record.")
	}
	if len(typ.Fields) != 4 {
		t.Fatalf("len(Fields) = %d, want 4", len(typ.Fields))
	}

	wantFields := map[string]struct {
		goName   string
		typeName string
	}{
		"active": {"Active", "bool"},
		"age":    {"Age", "int"},
		"name":   {"Name", "string"},
		"score":  {"Score", "float64"},
	}
	for _, f := range typ.Fields {
		want, ok := wantFields[f.JSONName]
		if !ok {
			t.Errorf("unexpected field json=%q", f.JSONName)
			continue
		}
		if f.GoName != want.goName {
			t.Errorf("field %q GoName = %q, want %q", f.JSONName, f.GoName, want.goName)
		}
		id, ok := f.TypeExpr.(*ast.Ident)
		if !ok {
			t.Errorf("field %q TypeExpr = %T, want *ast.Ident", f.JSONName, f.TypeExpr)
			continue
		}
		if id.Name != want.typeName {
			t.Errorf("field %q TypeExpr.Name = %q, want %q", f.JSONName, id.Name, want.typeName)
		}
		if !f.Required {
			t.Errorf("field %q Required = false, want true", f.JSONName)
		}
	}
}

func TestDerive_OptionalFields(t *testing.T) {
	src := `{
		"type": "object",
		"properties": {
			"name": {"type": "string"},
			"nickname": {"type": "string"}
		},
		"required": ["name"]
	}`
	s, err := jsonschema.Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	obj, _ := s.TypeObject()
	typ, err := Derive("User", &obj)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}

	byJSON := map[string]Field{}
	for _, f := range typ.Fields {
		byJSON[f.JSONName] = f
	}

	if name := byJSON["name"]; name.Required == false {
		t.Errorf("name.Required = false, want true")
	} else if id, ok := name.TypeExpr.(*ast.Ident); !ok || id.Name != "string" {
		t.Errorf("name.TypeExpr = %#v, want *ast.Ident{Name:\"string\"}", name.TypeExpr)
	}

	nick := byJSON["nickname"]
	if nick.Required {
		t.Errorf("nickname.Required = true, want false")
	}
	star, ok := nick.TypeExpr.(*ast.StarExpr)
	if !ok {
		t.Fatalf("nickname.TypeExpr = %T, want *ast.StarExpr", nick.TypeExpr)
	}
	if id, ok := star.X.(*ast.Ident); !ok || id.Name != "string" {
		t.Errorf("nickname pointer base = %#v, want *ast.Ident{Name:\"string\"}", star.X)
	}
}

func TestEmit_OptionalGetsOmitzero(t *testing.T) {
	typ := Type{
		Name: "User",
		Fields: []Field{
			{GoName: "Name", JSONName: "name", TypeExpr: &ast.Ident{Name: "string"}, Required: true},
			{GoName: "Nickname", JSONName: "nickname", TypeExpr: &ast.StarExpr{X: &ast.Ident{Name: "string"}}, Required: false},
		},
	}
	src, err := formatFile("model", []ast.Decl{Emit(typ)})
	if err != nil {
		t.Fatalf("formatFile: %v", err)
	}
	for _, want := range []string{
		"Name     string  `json:\"name\"`",
		"Nickname *string `json:\"nickname,omitzero\"`",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("emitted source missing %q\nsrc:\n%s", want, src)
		}
	}
}

func TestDerive_AdditionalPropertiesFalse(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want bool
	}{
		{"explicit false", `{"type":"object","additionalProperties":false}`, true},
		{"absent", `{"type":"object"}`, false},
		{"explicit true", `{"type":"object","additionalProperties":true}`, false},
		{"schema (not bool)", `{"type":"object","additionalProperties":{"type":"string"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := jsonschema.Parse([]byte(tc.src))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			obj, _ := s.TypeObject()
			typ, err := Derive("X", &obj)
			if err != nil {
				t.Fatalf("Derive: %v", err)
			}
			if typ.RejectUnknown != tc.want {
				t.Errorf("RejectUnknown = %v, want %v", typ.RejectUnknown, tc.want)
			}
		})
	}
}

func TestEmit_FormatsAsValidGo(t *testing.T) {
	typ := Type{
		Name: "User",
		Doc:  "A user record.",
		Fields: []Field{
			{GoName: "Name", JSONName: "name", TypeExpr: &ast.Ident{Name: "string"}, Required: true},
			{GoName: "Age", JSONName: "age", TypeExpr: &ast.Ident{Name: "int"}, Required: true},
		},
	}
	src, err := formatFile("model", []ast.Decl{Emit(typ)})
	if err != nil {
		t.Fatalf("formatFile: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "out.go", src, parser.ParseComments); err != nil {
		t.Fatalf("emitted source did not parse: %v\nsrc:\n%s", err, src)
	}
	want := []string{
		"package model",
		"type User struct {",
		"Name string `json:\"name\"`",
		"Age  int    `json:\"age\"`",
	}
	for _, line := range want {
		if !strings.Contains(src, line) {
			t.Errorf("emitted source missing %q\nsrc:\n%s", line, src)
		}
	}
}

// formatFile renders decls into a Go source file under packageName,
// running gofmt over the result. Used by tests so we don't have to
// manage a FileSet by hand.
func formatFile(packageName string, decls []ast.Decl) (string, error) {
	file := &ast.File{
		Name:  &ast.Ident{Name: packageName},
		Decls: decls,
	}
	var buf bytes.Buffer
	if err := format.Node(&buf, token.NewFileSet(), file); err != nil {
		return "", err
	}
	return buf.String(), nil
}
