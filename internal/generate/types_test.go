package generate

import (
	"go/ast"
	"strings"
	"testing"
)

func TestResolver_Resolve_Builtin(t *testing.T) {
	r, err := NewResolver(nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	expr, typ, err := r.Resolve("int")
	if err != nil {
		t.Fatalf("Resolve(int): %v", err)
	}
	id, ok := expr.(*ast.Ident)
	if !ok {
		t.Fatalf("Resolve(int) expr = %T, want *ast.Ident", expr)
	}
	if id.Name != "int" {
		t.Errorf("Resolve(int).Name = %q, want %q", id.Name, "int")
	}
	if typ == nil {
		t.Errorf("Resolve(int) types.Type = nil, want non-nil")
	}
}

func TestResolver_Resolve(t *testing.T) {
	r, err := NewResolver([]string{"math/big", "time", "net/netip"})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	for _, tc := range []struct {
		name    string
		src     string
		wantTyp string
	}{
		{"pointer to selector", "*big.Rat", "*math/big.Rat"},
		{"slice of selector", "[]time.Time", "[]time.Time"},
		{"map of selectors", "map[string]netip.Addr", "map[string]net/netip.Addr"},
		{"primitive slice", "[]byte", "[]byte"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, typ, err := r.Resolve(tc.src)
			if err != nil {
				t.Fatalf("Resolve(%q): %v", tc.src, err)
			}
			if got := typ.String(); got != tc.wantTyp {
				t.Errorf("Resolve(%q) typ = %q, want %q", tc.src, got, tc.wantTyp)
			}
		})
	}
}

func TestResolver_Resolve_Errors(t *testing.T) {
	r, err := NewResolver([]string{"math/big"})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	for _, tc := range []struct {
		name    string
		src     string
		wantErr string
	}{
		{"unimported package", "time.Time", `package "time" not in goImports`},
		{"unexported ident", "big.nat", "not exported"},
		{"unknown builtin", "frobnicate", "unknown identifier"},
		{"unknown selector", "big.Nope", "not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := r.Resolve(tc.src)
			if err == nil {
				t.Fatalf("Resolve(%q): err = nil, want error containing %q", tc.src, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Resolve(%q): err = %v, want containing %q", tc.src, err, tc.wantErr)
			}
		})
	}
}

func TestNewResolver_DisallowedImport(t *testing.T) {
	_, err := NewResolver([]string{"github.com/some/random/pkg"})
	if err == nil {
		t.Fatalf("NewResolver: err = nil, want error for disallowed import")
	}
	if !strings.Contains(err.Error(), "allowed set") {
		t.Errorf("NewResolver: err = %v, want mention of allowed set", err)
	}
}

func TestParseAndValidateGoType(t *testing.T) {
	for _, tc := range []struct {
		name      string
		src       string
		goImports []string
		wantErr   string
	}{
		{name: "primitive", src: "int"},
		{name: "qualified declared", src: "time.Time", goImports: []string{"time"}},
		{name: "slice qualified", src: "[]big.Rat", goImports: []string{"math/big"}},
		{name: "pointer qualified", src: "*big.Rat", goImports: []string{"math/big"}},
		{name: "map qualified", src: "map[string]time.Time", goImports: []string{"time"}},
		{name: "qualified missing", src: "time.Time", wantErr: "missing from goImports"},
		{name: "disallowed import", src: "thing.Thing", goImports: []string{"example.com/thing"}, wantErr: "allowed set"},
		{name: "syntax error", src: "][]", wantErr: "parse"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseAndValidateGoType(tc.src, tc.goImports)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("parseAndValidateGoType(%q, %v): %v", tc.src, tc.goImports, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("parseAndValidateGoType(%q, %v) err = %v, want containing %q", tc.src, tc.goImports, err, tc.wantErr)
			}
		})
	}
}

func TestAllowedImportPath(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"time", true},
		{"net/netip", true},
		{"math/big", true},
		{"encoding/json/v2", true},
		{"golang.org/x/net/idna", true},
		{"encoding/json/v2", true},
		{"encoding/json/jsontext", true},
		{"github.com/crhntr/jsonschema", false},
		{"example.com/foo", false},
		{"", false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			if got := allowedImportPath(tc.path); got != tc.want {
				t.Errorf("allowedImportPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}
