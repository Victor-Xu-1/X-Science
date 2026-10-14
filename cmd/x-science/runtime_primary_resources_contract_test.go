package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"
)

// Source wiring is checked without compiling the server graph. Runtime helper
// tests separately prove the join/failure gate and actual resource lifetime.
func TestRuntimePersistentResourcesHaveOneQuiescenceGate(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate main source")
	}
	source, err := parser.ParseFile(token.NewFileSet(), filepath.Join(filepath.Dir(filename), "main.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	registered := map[string]bool{"runtimeState": false, "workspaceStore": false, "operationalLog": false}
	groups := 0
	for _, declaration := range source.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "runServer" {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			if deferred, ok := node.(*ast.DeferStmt); ok {
				for name := range registered {
					if startupHasCall(deferred, name, "Close") {
						t.Errorf("%s has an unconditional deferred Close beneath unjoined writers", name)
					}
				}
				if startupHasCall(deferred, "primaryResources", "CloseAfter") {
					groups++
				}
			}
			call, ok := node.(*ast.CallExpr)
			if !ok || !startupHasCall(call, "primaryResources", "Add") || len(call.Args) != 2 {
				return true
			}
			method, ok := call.Args[1].(*ast.SelectorExpr)
			if !ok || method.Sel.Name != "Close" {
				return true
			}
			if receiver, ok := method.X.(*ast.Ident); ok {
				if _, tracked := registered[receiver.Name]; tracked {
					registered[receiver.Name] = true
				}
			}
			return true
		})
	}
	if groups != 1 {
		t.Errorf("expected one returned-error-aware resource close group, got %d", groups)
	}
	for name, added := range registered {
		if !added {
			t.Errorf("persistent resource %s has no owned close registration", name)
		}
	}
}
