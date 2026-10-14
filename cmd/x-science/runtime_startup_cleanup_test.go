package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"
)

// This registration-order contract can run without compiling the server graph.
// Runtime shutdown/join semantics remain covered by their owning components.
func TestRuntimeCleanupRegisteredBeforeFallibleRecovery(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate startup source")
	}
	source, err := parser.ParseFile(token.NewFileSet(), filepath.Join(filepath.Dir(testFile), "main.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var body *ast.BlockStmt
	for _, declaration := range source.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok && function.Name.Name == "runServer" {
			body = function.Body
		}
	}
	if body == nil {
		t.Fatal("runServer not found")
	}
	constructor, cleanup, recovery, cleanupCount := -1, -1, -1, 0
	for index, statement := range body.List {
		if assignment, ok := statement.(*ast.AssignStmt); ok && len(assignment.Lhs) == 1 && len(assignment.Rhs) == 1 {
			name, named := assignment.Lhs[0].(*ast.Ident)
			if named && name.Name == "app" && startupHasCall(assignment.Rhs[0], "server", "New") {
				constructor = index
			}
		}
		if deferred, ok := statement.(*ast.DeferStmt); ok && startupHasCall(deferred, "app", "Close") {
			cleanup, cleanupCount = index, cleanupCount+1
		}
		if conditional, ok := statement.(*ast.IfStmt); ok && startupHasCall(conditional.Init, "app", "RecoverIMOutbound") {
			recovery = index
		}
	}
	if constructor < 0 || recovery < 0 || cleanupCount != 1 || cleanup != constructor+1 || cleanup >= recovery {
		t.Fatalf("cleanup must be registered immediately after construction and before recovery: constructor=%d cleanup=%d recovery=%d cleanupCount=%d", constructor, cleanup, recovery, cleanupCount)
	}
}

func startupHasCall(node ast.Node, receiver, method string) bool {
	if node == nil {
		return false
	}
	found := false
	ast.Inspect(node, func(current ast.Node) bool {
		call, ok := current.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		name, ok := selector.X.(*ast.Ident)
		found = found || ok && name.Name == receiver && selector.Sel.Name == method
		return true
	})
	return found
}
