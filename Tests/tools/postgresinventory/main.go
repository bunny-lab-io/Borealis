// Discover database-gated tests, including calls through package-local helpers.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func main() {
	root := flag.String("root", "", "Go package source directory")
	flag.Parse()
	files, err := filepath.Glob(filepath.Join(*root, "*_test.go"))
	if err != nil || *root == "" || len(files) == 0 {
		fmt.Fprintln(os.Stderr, "POSTGRES INVENTORY FAIL: test source directory missing or empty")
		os.Exit(1)
	}
	// Package functions, methods and external test packages have distinct scopes.
	// Keep declaration identity so a same-named method cannot replace a helper.
	packages := map[string]map[string]*ast.FuncDecl{}
	functions := map[*ast.FuncDecl]string{}
	for _, path := range files {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if packages[file.Name.Name] == nil {
			packages[file.Name.Name] = map[string]*ast.FuncDecl{}
		}
		for _, declaration := range file.Decls {
			if function, ok := declaration.(*ast.FuncDecl); ok && function.Recv == nil {
				packages[file.Name.Name][function.Name.Name] = function
				functions[function] = file.Name.Name
			}
		}
	}
	database := map[*ast.FuncDecl]bool{}
	for changed := true; changed; {
		changed = false
		for function, packageName := range functions {
			if database[function] {
				continue
			}
			var inspect func(ast.Node) bool
			inspect = func(node ast.Node) bool {
				switch value := node.(type) {
				case *ast.BasicLit:
					if value.Kind == token.STRING {
						literal, _ := strconv.Unquote(value.Value)
						if literal == "BOREALIS_TEST_DATABASE_URL" {
							database[function] = true
						}
					}
				case *ast.Ident:
					var target *ast.FuncDecl
					if value.Obj != nil {
						// Parser resolves local bindings, including shadowing variables.
						target, _ = value.Obj.Decl.(*ast.FuncDecl)
					} else {
						// A package helper may be declared in another test file.
						target = packages[packageName][value.Name]
					}
					if database[target] {
						database[function] = true
					}
				case *ast.SelectorExpr:
					// Selector names belong to a receiver or imported package, never
					// to this package's function scope. Still inspect the receiver.
					ast.Inspect(value.X, inspect)
					return false
				}
				return true
			}
			ast.Inspect(function.Body, inspect)
			changed = changed || database[function]
		}
	}
	tests := []string{}
	for function := range database {
		if strings.HasPrefix(function.Name.Name, "Test") {
			tests = append(tests, function.Name.Name)
		}
	}
	sort.Strings(tests)
	if err := json.NewEncoder(os.Stdout).Encode(tests); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
