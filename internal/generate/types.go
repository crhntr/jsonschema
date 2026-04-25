package generate

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/types"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Resolver parses and validates Go type expressions used in goType
// annotations. It resolves selector expressions against a set of
// packages loaded with go/packages, and primitive identifiers against
// the universe scope.
type Resolver struct {
	pkgs map[string]*packages.Package
}

// NewResolver loads the given import paths so their exported types
// can be resolved. The list may be nil for resolvers that only need
// to handle primitive types.
func NewResolver(imports []string) (*Resolver, error) {
	r := &Resolver{pkgs: map[string]*packages.Package{}}
	if len(imports) == 0 {
		return r, nil
	}
	for _, p := range imports {
		if !allowedImportPath(p) {
			return nil, fmt.Errorf("import %q is not in the allowed set (stdlib, github.com/go-json-experiment/json, golang.org/x/*)", p)
		}
	}
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedDeps,
	}
	loaded, err := packages.Load(cfg, imports...)
	if err != nil {
		return nil, fmt.Errorf("packages.Load: %w", err)
	}
	for _, pkg := range loaded {
		if len(pkg.Errors) > 0 {
			return nil, fmt.Errorf("load %s: %v", pkg.PkgPath, pkg.Errors[0])
		}
		r.pkgs[pkg.PkgPath] = pkg
	}
	for _, p := range imports {
		if _, ok := r.pkgs[p]; !ok {
			return nil, fmt.Errorf("package %q did not load", p)
		}
	}
	return r, nil
}

// Resolve parses src as a Go type expression and verifies that every
// referenced selector resolves to an exported type from a loaded
// package. The returned ast.Expr is suitable for splicing into
// generated code.
func (r *Resolver) Resolve(src string) (ast.Expr, types.Type, error) {
	expr, err := parser.ParseExpr(src)
	if err != nil {
		return nil, nil, fmt.Errorf("parse %q: %w", src, err)
	}
	typ, err := r.resolveExpr(expr)
	if err != nil {
		return nil, nil, err
	}
	return expr, typ, nil
}

func (r *Resolver) resolveExpr(expr ast.Expr) (types.Type, error) {
	switch e := expr.(type) {
	case *ast.Ident:
		obj := types.Universe.Lookup(e.Name)
		if obj == nil {
			return nil, fmt.Errorf("unknown identifier %q (unqualified identifiers must be Go builtins)", e.Name)
		}
		return obj.Type(), nil
	case *ast.SelectorExpr:
		pkgIdent, ok := e.X.(*ast.Ident)
		if !ok {
			return nil, fmt.Errorf("selector base must be a package identifier, got %T", e.X)
		}
		pkg, path := r.lookupPackageByName(pkgIdent.Name)
		if pkg == nil {
			return nil, fmt.Errorf("package %q not in goImports", pkgIdent.Name)
		}
		obj := pkg.Types.Scope().Lookup(e.Sel.Name)
		if obj == nil {
			return nil, fmt.Errorf("%s.%s: not found", path, e.Sel.Name)
		}
		if !obj.Exported() {
			return nil, fmt.Errorf("%s.%s: not exported", path, e.Sel.Name)
		}
		_, ok = obj.(*types.TypeName)
		if !ok {
			return nil, fmt.Errorf("%s.%s: not a type", path, e.Sel.Name)
		}
		return obj.Type(), nil
	case *ast.StarExpr:
		elem, err := r.resolveExpr(e.X)
		if err != nil {
			return nil, err
		}
		return types.NewPointer(elem), nil
	case *ast.ArrayType:
		elem, err := r.resolveExpr(e.Elt)
		if err != nil {
			return nil, err
		}
		if e.Len != nil {
			return nil, fmt.Errorf("fixed-size arrays are not supported in goType")
		}
		return types.NewSlice(elem), nil
	case *ast.MapType:
		key, err := r.resolveExpr(e.Key)
		if err != nil {
			return nil, err
		}
		val, err := r.resolveExpr(e.Value)
		if err != nil {
			return nil, err
		}
		return types.NewMap(key, val), nil
	default:
		return nil, fmt.Errorf("unsupported type expression %T", expr)
	}
}

func (r *Resolver) lookupPackageByName(name string) (*packages.Package, string) {
	for path, pkg := range r.pkgs {
		if pkg.Name == name {
			return pkg, path
		}
	}
	return nil, ""
}

// allowedImportPath reports whether path may appear in goImports.
// The generated code must only depend on the standard library,
// github.com/go-json-experiment/json (and its jsontext sibling),
// or golang.org/x/*.
func allowedImportPath(path string) bool {
	if path == "" {
		return false
	}
	if path == "github.com/go-json-experiment/json" ||
		strings.HasPrefix(path, "github.com/go-json-experiment/json/") {
		return true
	}
	if strings.HasPrefix(path, "golang.org/x/") {
		return true
	}
	first, _, _ := strings.Cut(path, "/")
	if strings.Contains(first, ".") {
		return false
	}
	return true
}
