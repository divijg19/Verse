package tests

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestEveryInternalErrorResponseIsLogged closes the last way to be quiet about a failure.
//
// The rule was written down after the poem-view handlers were fixed, and it says a database outage
// must not present as an empty page with nothing in the log. It was then honored by the two
// handlers that had been fixed and by nothing else. Eight paths answered 500 without logging: the
// library list, the library search fragment, the heatmap fragment, the dashboard, and both export
// failures. Seven of those are the two surfaces an author looks at most.
//
// A comment stating the rule did not prevent it, because there was nothing to enforce. Every one of
// the eight was correct code in isolation: http.Error with a generic message, no information leaked,
// no test failed. The omission is invisible from any single file.
//
// So the enforcement is structural rather than behavioral. handlers.fail500 is the only function in
// internal/handlers permitted to write a 500, and it logs before it writes. This test parses the
// package and fails if any other function calls http.Error with StatusInternalServerError.
//
// Parsing rather than grepping, because the naive textual match also finds the mention in this
// file's own comment and the pass-through inside fail500. Working on the AST means the check is
// about what the code does, not about how it is spelled, and a reworded comment cannot change the
// result.
func TestEveryInternalErrorResponseIsLogged(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine this file's path")
	}
	handlersDir := filepath.Join(filepath.Dir(thisFile), "..", "internal", "handlers")

	entries, err := os.ReadDir(handlersDir)
	if err != nil {
		t.Fatalf("read %s: %v", handlersDir, err)
	}

	// The one permitted writer. Naming it here rather than pattern-matching its body keeps the
	// exception explicit: if fail500 is ever renamed, this test says so instead of silently
	// starting to reject a function whose name no longer matches.
	const allowlisted = "fail500"

	found := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		path := filepath.Join(handlersDir, entry.Name())
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, source, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name == allowlisted {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !isHTTPError(call) {
					return true
				}
				if !mentionsStatusInternalServerError(call) {
					return true
				}
				found++
				pos := fset.Position(call.Pos())
				t.Errorf("%s:%d: %s writes a 500 directly.\n"+
					"  Every 500 in this package must go through handlers.%s, which logs the cause and the\n"+
					"  request id before responding. A 500 written here returns to the client with no\n"+
					"  record on the server, which is the exact failure this rule exists to prevent:\n"+
					"  a database outage presenting as an empty page and nothing in the log.",
					entry.Name(), pos.Line, fn.Name.Name, allowlisted)
				return true
			})
		}
	}

	if found == 0 {
		// Not an error. A handler package with no 500s of its own is a strictly better state, and
		// failing here would punish the very refactor this test is written to enable.
		t.Log("no handler writes a 500 outside " + allowlisted + "; nothing to check")
	}
}

// isHTTPError reports whether a call expression is a call to http.Error.
func isHTTPError(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Error" {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == "http"
}

// mentionsStatusInternalServerError reports whether any argument of the call names the 500 constant.
//
// The constant may be written bare or qualified, and either is a real 500, so the test accepts both
// rather than only the form this repository currently happens to use.
func mentionsStatusInternalServerError(call *ast.CallExpr) bool {
	found := false
	for _, arg := range call.Args {
		ast.Inspect(arg, func(n ast.Node) bool {
			ident, ok := n.(*ast.Ident)
			if ok && ident.Name == "StatusInternalServerError" {
				found = true
			}
			return !found
		})
	}
	return found
}
