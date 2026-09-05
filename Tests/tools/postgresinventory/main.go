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
	functions := map[string]*ast.FuncDecl{}
	for _, path := range files {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for _, declaration := range file.Decls {
			if function, ok := declaration.(*ast.FuncDecl); ok {
				functions[function.Name.Name] = function
			}
		}
	}
	database := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for name, function := range functions {
			if database[name] {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				switch value := node.(type) {
				case *ast.BasicLit:
					if value.Kind == token.STRING {
						literal, _ := strconv.Unquote(value.Value)
						if literal == "BOREALIS_TEST_DATABASE_URL" {
							database[name] = true
						}
					}
				case *ast.Ident:
					if database[value.Name] {
						database[name] = true
					}
				}
				return true
			})
			changed = changed || database[name]
		}
	}
	tests := []string{}
	for name := range database {
		if strings.HasPrefix(name, "Test") && functions[name].Recv == nil {
			tests = append(tests, name)
		}
	}
	sort.Strings(tests)
	if err := json.NewEncoder(os.Stdout).Encode(tests); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
