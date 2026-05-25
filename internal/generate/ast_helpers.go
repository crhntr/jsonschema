package generate

import (
	"go/ast"
	"go/token"
	"strconv"
)

// ident returns *ast.Ident{Name: name}.
func ident(name string) *ast.Ident { return &ast.Ident{Name: name} }

// sel returns *ast.SelectorExpr for `pkg.Name`.
func sel(pkg, name string) *ast.SelectorExpr {
	return &ast.SelectorExpr{X: ident(pkg), Sel: ident(name)}
}

// stringLit returns a quoted string BasicLit.
func stringLit(s string) *ast.BasicLit {
	return &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(s)}
}

// rawNumLit returns a numeric BasicLit whose Value is the raw JSON
// text of the number. The token kind is INT if the text has no
// decimal point or exponent, else FLOAT.
func rawNumLit(raw string) *ast.BasicLit {
	kind := token.INT
	for _, c := range raw {
		if c == '.' || c == 'e' || c == 'E' {
			kind = token.FLOAT
			break
		}
	}
	return &ast.BasicLit{Kind: kind, Value: raw}
}

// intLit returns a decimal integer BasicLit.
func intLit(n int) *ast.BasicLit {
	return &ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(n)}
}

// callExpr returns `fn(args...)`.
func callExpr(fn ast.Expr, args ...ast.Expr) *ast.CallExpr {
	return &ast.CallExpr{Fun: fn, Args: args}
}

// binOp returns `left <op> right`.
func binOp(left ast.Expr, op token.Token, right ast.Expr) *ast.BinaryExpr {
	return &ast.BinaryExpr{X: left, Op: op, Y: right}
}

// returnStmt returns a `return <results>` statement.
func returnStmt(results ...ast.Expr) *ast.ReturnStmt {
	return &ast.ReturnStmt{Results: results}
}

// fmtErrorfCall returns `fmt.Errorf(format, args...)`.
func fmtErrorfCall(format string, args ...ast.Expr) *ast.CallExpr {
	return callExpr(sel("fmt", "Errorf"), append([]ast.Expr{stringLit(format)}, args...)...)
}

// ifReturnFmtErrorf returns `if cond { return fmt.Errorf(format, args...) }`.
func ifReturnFmtErrorf(cond ast.Expr, format string, args ...ast.Expr) *ast.IfStmt {
	return &ast.IfStmt{
		Cond: cond,
		Body: &ast.BlockStmt{List: []ast.Stmt{returnStmt(fmtErrorfCall(format, args...))}},
	}
}

// ifErrReturn returns `if err := <call>; err != nil { return err }`.
func ifErrReturn(call ast.Expr) *ast.IfStmt {
	return &ast.IfStmt{
		Init: &ast.AssignStmt{
			Lhs: []ast.Expr{ident("err")},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{call},
		},
		Cond: binOp(ident("err"), token.NEQ, ident("nil")),
		Body: &ast.BlockStmt{List: []ast.Stmt{returnStmt(ident("err"))}},
	}
}

// jsontextStringCall returns `jsontext.String(<lit>)`.
func jsontextStringCall(s string) *ast.CallExpr {
	return callExpr(sel("jsontext", "String"), stringLit(s))
}

// encWriteToken returns `enc.WriteToken(<arg>)`.
func encWriteToken(arg ast.Expr) *ast.CallExpr {
	return callExpr(sel("enc", "WriteToken"), arg)
}
