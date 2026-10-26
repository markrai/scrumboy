package mcp_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestCustomFieldMCPMutationHandlersDoNotCallStoreMutations(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "custom_fields_tools.go", nil, 0)
	if err != nil {
		t.Fatalf("parse custom_fields_tools.go: %v", err)
	}
	for _, name := range []string{"handleCustomFieldsCreate", "handleCustomFieldsDelete"} {
		fn := findCustomFieldFuncDecl(file, name)
		if fn == nil {
			t.Fatalf("missing %s", name)
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "CreateCustomField", "DeleteCustomField", "GetProjectRole":
				t.Errorf("%s calls %s at %s", name, sel.Sel.Name, fset.Position(sel.Sel.Pos()))
			}
			return true
		})
	}
}

func findCustomFieldFuncDecl(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}
