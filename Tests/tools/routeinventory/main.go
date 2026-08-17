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

type route struct {
	Pattern string `json:"pattern"`
	Source  string `json:"source"`
	Line    int    `json:"line"`
}

func stringConstants(files map[string]*ast.File) map[string]string {
	constants := map[string]string{}
	changed := true
	for changed {
		changed = false
		for _, file := range files {
			for _, declaration := range file.Decls {
				generic, ok := declaration.(*ast.GenDecl)
				if !ok || generic.Tok != token.CONST {
					continue
				}
				for _, specification := range generic.Specs {
					valueSpec, ok := specification.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for index, name := range valueSpec.Names {
						if index >= len(valueSpec.Values) {
							continue
						}
						if value, ok := evalString(valueSpec.Values[index], constants); ok && constants[name.Name] != value {
							constants[name.Name] = value
							changed = true
						}
					}
				}
			}
		}
	}
	return constants
}

func evalString(expression ast.Expr, constants map[string]string) (string, bool) {
	switch value := expression.(type) {
	case *ast.BasicLit:
		if value.Kind != token.STRING {
			return "", false
		}
		decoded, err := strconv.Unquote(value.Value)
		return decoded, err == nil
	case *ast.Ident:
		decoded, ok := constants[value.Name]
		return decoded, ok
	case *ast.BinaryExpr:
		if value.Op != token.ADD {
			return "", false
		}
		left, leftOK := evalString(value.X, constants)
		right, rightOK := evalString(value.Y, constants)
		return left + right, leftOK && rightOK
	case *ast.ParenExpr:
		return evalString(value.X, constants)
	default:
		return "", false
	}
}

func main() {
	root := flag.String("root", "Data/Engine/Containers/api-backend/cmd/api-backend", "Go package root")
	repository := flag.String("repo", ".", "repository root used for relative source paths")
	flag.Parse()

	set := token.NewFileSet()
	packages, err := parser.ParseDir(set, *root, func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "API ROUTE FAIL: parse Go package: %v\n", err)
		os.Exit(1)
	}
	var files map[string]*ast.File
	for _, pkg := range packages {
		files = pkg.Files
		break
	}
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "API ROUTE FAIL: no non-test Go files parsed")
		os.Exit(1)
	}
	constants := stringConstants(files)
	routes := []route{}
	for _, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (selector.Sel.Name != "Handle" && selector.Sel.Name != "HandleFunc") {
				return true
			}
			pattern, ok := evalString(call.Args[0], constants)
			if !ok {
				position := set.Position(call.Pos())
				fmt.Fprintf(os.Stderr, "API ROUTE FAIL: unresolved route pattern at %s:%d\n", position.Filename, position.Line)
				os.Exit(1)
			}
			position := set.Position(call.Pos())
			relative, err := filepath.Rel(*repository, position.Filename)
			if err != nil {
				relative = position.Filename
			}
			routes = append(routes, route{Pattern: pattern, Source: filepath.ToSlash(relative), Line: position.Line})
			return true
		})
	}
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Pattern != routes[j].Pattern {
			return routes[i].Pattern < routes[j].Pattern
		}
		if routes[i].Source != routes[j].Source {
			return routes[i].Source < routes[j].Source
		}
		return routes[i].Line < routes[j].Line
	})
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(routes); err != nil {
		fmt.Fprintf(os.Stderr, "API ROUTE FAIL: encode inventory: %v\n", err)
		os.Exit(1)
	}
}
