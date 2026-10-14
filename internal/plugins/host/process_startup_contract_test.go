package host

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"
)

func TestHostStartupDoesNotSynchronouslyWaitForUnregisteredProcess(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate host source")
	}
	source, err := parser.ParseFile(token.NewFileSet(), filepath.Join(filepath.Dir(filename), "host.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range source.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "StartExternalProcesses" {
			continue
		}
		var owned, attached, finalized token.Pos
		finalizers := 0
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			method, ok := call.Fun.(*ast.SelectorExpr)
			if ok && method.Sel.Name == "Wait" {
				t.Error("startup synchronously waits before the bounded shutdown owner can join the process")
			}
			if ok {
				switch method.Sel.Name {
				case "ownStartedProcess":
					owned = call.Pos()
				case "Attach":
					attached = call.Pos()
				case "finishProcessStartup":
					finalized = call.Pos()
					finalizers++
				}
			}
			return true
		})
		if owned == token.NoPos || attached <= owned || finalized <= attached || finalizers != 1 {
			t.Fatalf("ownership must precede Attach and reach one startup/reaper finalizer: own=%v attach=%v final=%v count=%d", owned, attached, finalized, finalizers)
		}
		return
	}
	t.Fatal("StartExternalProcesses not found")
}
