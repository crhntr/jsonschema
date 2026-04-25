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
