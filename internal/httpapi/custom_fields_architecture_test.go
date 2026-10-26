package httpapi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCustomFieldRESTMutationHandlerDoesNotCallStoreMutations(t *testing.T) {
	assertHandlerOmitsCalls(t, "routing_custom_fields.go", "handleBoardCustomFieldRoutes", "CreateCustomField", "DeleteCustomField", "GetProjectRole", "Authorize")
	assertHandlerCalls(t, "routing_custom_fields.go", "handleBoardCustomFieldRoutes", "Prepare")
}

func TestCustomFieldApplicationOmitsProjectRoleLookup(t *testing.T) {
	dir := filepath.Join("..", "application", "customfield")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read customfield package: %v", err)
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		assertTreeOmitsCalls(t, fset, file, "GetProjectRole")
	}
}

func assertHandlerOmitsCalls(t *testing.T, filename, funcName string, names ...string) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}
	fn := findFuncDecl(file, funcName)
	if fn == nil {
		t.Fatalf("%s: missing %s", filename, funcName)
	}
	assertTreeOmitsCalls(t, fset, fn, names...)
}

func findFuncDecl(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}

func assertHandlerCalls(t *testing.T, filename, funcName, name string) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}
	fn := findFuncDecl(file, funcName)
	if fn == nil {
		t.Fatalf("%s: missing %s", filename, funcName)
	}
	found := false
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name == name {
			found = true
		}
		return true
	})
	if !found {
		t.Errorf("%s does not call %s", funcName, name)
	}
}

func assertTreeOmitsCalls(t *testing.T, fset *token.FileSet, node ast.Node, names ...string) {
	t.Helper()
	forbidden := map[string]struct{}{}
	for _, name := range names {
		forbidden[name] = struct{}{}
	}
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if _, hit := forbidden[sel.Sel.Name]; hit {
			t.Errorf("forbidden call %s at %s", sel.Sel.Name, fset.Position(sel.Sel.Pos()))
		}
		return true
	})
}
