//go:build ignore

// This stdlib-only parser proves namespace-only source equivalence. It is a
// build-time selector helper, not another product runtime or Go package.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
)

type request struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

var namespaces = strings.NewReplacer(
	"Synon Biomed", "X-Science", "synon-biomed", "x-science", "synon-ai", "x-science",
	"synon-go", "x-science", "SynonBiomed", "XScience", "synonBiomed", "xScience",
	"synonbiomed", "xscience", "SynonAI", "XScience", "SynonAi", "XScience",
	"synon_biomed", "x_science", "synon_host", "x_science_host", "SYNON_", "X_SCIENCE_",
	"synon_", "x_science_", "synon-", "x-science-", "Synon", "XScience", "synon", "xscience",
)

func name(value string) string {
	if strings.Contains(strings.ToLower(value), "synonym") {
		return value
	}
	return namespaces.Replace(value)
}

func directives(source string) string {
	var result strings.Builder
	for line, value := range strings.Split(source, "\n") {
		trimmed := strings.TrimSpace(value)
		if strings.HasPrefix(trimmed, "//go:") || strings.HasPrefix(trimmed, "// +build") ||
			strings.HasPrefix(trimmed, "//line ") || strings.Contains(value, "#cgo") {
			fmt.Fprintf(&result, "%d:%s\n", line, value)
		}
	}
	return result.String()
}

func normalized(source string) ([]byte, bool, error) {
	file, err := parser.ParseFile(token.NewFileSet(), "source.go", source, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, false, err
	}
	changed := false
	originalNames := map[string]string{}
	var collision bool
	ast.Inspect(file, func(node ast.Node) bool {
		if identifier, ok := node.(*ast.Ident); ok {
			canonical := name(identifier.Name)
			if prior, exists := originalNames[canonical]; exists && prior != identifier.Name {
				collision = true
			}
			originalNames[canonical] = identifier.Name
		}
		return true
	})
	if collision {
		return nil, false, fmt.Errorf("namespace normalization would collapse distinct identifiers")
	}
	for _, imported := range file.Imports {
		value, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			return nil, false, err
		}
		if value == "C" {
			// A C preamble is executable source stored in comments, not prose.
			return nil, false, fmt.Errorf("C import is not namespace-only proof")
		}
		if value == "synon-go" || strings.HasPrefix(value, "synon-go/") {
			value = "x-science" + strings.TrimPrefix(value, "synon-go")
			changed = true
		}
		imported.Path.Value = strconv.Quote(value)
	}
	ast.Inspect(file, func(node ast.Node) bool {
		if identifier, ok := node.(*ast.Ident); ok {
			canonical := name(identifier.Name)
			changed = changed || canonical != identifier.Name
			identifier.Name = canonical
		}
		return true
	})
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if strings.HasPrefix(comment.Text, "//go:") || strings.HasPrefix(comment.Text, "// +build") ||
				strings.HasPrefix(comment.Text, "//line ") || strings.Contains(comment.Text, "#cgo") {
				continue
			}
			canonical := name(comment.Text)
			changed = changed || canonical != comment.Text
			comment.Text = canonical
		}
	}
	var result bytes.Buffer
	err = ast.Fprint(&result, token.NewFileSet(), file, func(field string, value reflect.Value) bool {
		return value.Type() != reflect.TypeOf(token.Pos(0)) && field != "Scope" && field != "Obj" && field != "Unresolved"
	})
	return result.Bytes(), changed, err
}

func equivalent(input request) bool {
	if directives(input.Before) != directives(input.After) {
		return false
	}
	before, _, oldError := normalized(input.Before)
	after, _, newError := normalized(input.After)
	return oldError == nil && newError == nil && bytes.Equal(before, after)
}

func samePackage(input request) bool {
	if directives(input.Before) != directives(input.After) {
		return false
	}
	before, oldError := parser.ParseFile(token.NewFileSet(), "before.go", input.Before, parser.ParseComments|parser.SkipObjectResolution)
	after, newError := parser.ParseFile(token.NewFileSet(), "after.go", input.After, parser.ParseComments|parser.SkipObjectResolution)
	return oldError == nil && newError == nil && name(before.Name.Name) == name(after.Name.Name)
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var input request
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			fmt.Fprintln(os.Stderr, "invalid namespace proof input")
			os.Exit(1)
		}
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			fmt.Fprintln(os.Stderr, "multiple namespace proof values on one line")
			os.Exit(1)
		}
		if err := encoder.Encode(map[string]bool{"equivalent": equivalent(input), "same_package": samePackage(input)}); err != nil {
			os.Exit(1)
		}
	}
	if scanner.Err() != nil {
		fmt.Fprintln(os.Stderr, "namespace proof input exceeds its bound")
		os.Exit(1)
	}
}
