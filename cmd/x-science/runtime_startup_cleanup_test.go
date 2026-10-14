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
	coordinator, firstOwner, coordinatorCount := -1, -1, 0
	for index, statement := range body.List {
		if assignment, ok := statement.(*ast.AssignStmt); ok && len(assignment.Lhs) == 1 && len(assignment.Rhs) == 1 {
			name, named := assignment.Lhs[0].(*ast.Ident)
			if named && name.Name == "app" && startupHasCall(assignment.Rhs[0], "server", "New") {
				constructor = index
			}
		}
		if startupHasCall(statement, "primaryOwners", "Add") {
			ast.Inspect(statement, func(node ast.Node) bool {
				method, ok := node.(*ast.SelectorExpr)
				if !ok || method.Sel.Name != "Close" {
					return true
				}
				if name, ok := method.X.(*ast.Ident); ok && name.Name == "app" {
					cleanup, cleanupCount = index, cleanupCount+1
				}
				return true
			})
		}
		if startupHasNamedCall(statement, "newIMOutboundDispatcher") {
			firstOwner = index
		}
		if deferred, ok := statement.(*ast.DeferStmt); ok {
			if startupHasNamedCall(deferred, "shutdownPrimaryRuntime") {
				coordinator, coordinatorCount = index, coordinatorCount+1
			}
			if startupHasCall(deferred, "app", "Close") || startupHasCall(deferred, "imOutbound", "Close") || startupHasCall(deferred, "plugins", "StopExternalProcesses") {
				t.Error("owner must use the shared error-aware shutdown coordinator")
			}
		}
		if conditional, ok := statement.(*ast.IfStmt); ok && startupHasCall(conditional.Init, "app", "RecoverIMOutbound") {
			recovery = index
		}
	}
	if constructor < 0 || recovery < 0 || cleanupCount != 1 || cleanup != constructor+1 || cleanup >= recovery {
		t.Fatalf("cleanup must be registered immediately after construction and before recovery: constructor=%d cleanup=%d recovery=%d cleanupCount=%d", constructor, cleanup, recovery, cleanupCount)
	}
	if coordinatorCount != 1 || coordinator < 0 || firstOwner <= coordinator {
		t.Fatalf("one shutdown coordinator must precede owned construction: coordinator=%d count=%d firstOwner=%d", coordinator, coordinatorCount, firstOwner)
	}
}

func startupHasNamedCall(node ast.Node, function string) bool {
	found := false
	ast.Inspect(node, func(current ast.Node) bool {
		call, ok := current.(*ast.CallExpr)
		if !ok {
			return true
		}
		name, ok := call.Fun.(*ast.Ident)
		found = found || ok && name.Name == function
		return true
	})
	return found
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
