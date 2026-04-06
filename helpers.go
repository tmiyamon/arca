package main

import "strings"

type goImportEntry struct {
	path       string
	sideEffect bool
}

// GoPackage represents a Go package imported via FFI.
// Centralizes import path parsing so version suffix logic is in one place.
type GoPackage struct {
	ShortName string // "echo", "http", "fmt"
	FullPath  string // "github.com/labstack/echo/v5", "net/http", "fmt"
}

// NewGoPackage creates a GoPackage from a Go import path.
// Handles version suffixes: "github.com/labstack/echo/v5" → ShortName "echo".
func NewGoPackage(importPath string) *GoPackage {
	parts := strings.Split(importPath, "/")
	shortName := parts[len(parts)-1]
	if len(parts) >= 2 && isVersionSuffix(shortName) {
		shortName = parts[len(parts)-2]
	}
	return &GoPackage{ShortName: shortName, FullPath: importPath}
}

// isVersionSuffix checks if a path segment is a Go module version suffix (v2, v5, etc.).
func isVersionSuffix(s string) bool {
	return len(s) >= 2 && s[0] == 'v' && s[1] >= '0' && s[1] <= '9'
}

func capitalize(s string) string {
	if len(s) == 0 {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func snakeToCamel(s string) string {
	// With camelCase convention, identifiers pass through as-is
	return s
}

func snakeToPascal(s string) string {
	// pub functions: capitalize first letter
	return capitalize(s)
}

func camelToSnake(s string) string {
	var result []byte
	for i, c := range s {
		if c >= 'A' && c <= 'Z' {
			if i > 0 {
				result = append(result, '_')
			}
			result = append(result, byte(c+'a'-'A'))
		} else {
			result = append(result, byte(c))
		}
	}
	return string(result)
}

func camelToKebab(s string) string {
	var result []byte
	for i, c := range s {
		if c >= 'A' && c <= 'Z' {
			if i > 0 {
				result = append(result, '-')
			}
			result = append(result, byte(c+'a'-'A'))
		} else {
			result = append(result, byte(c))
		}
	}
	return string(result)
}

// findField looks up a field by name across all constructors of a type.
func findField(td TypeDecl, name string) *Field {
	for _, ctor := range td.Constructors {
		for i, f := range ctor.Fields {
			if f.Name == name {
				return &ctor.Fields[i]
			}
		}
	}
	return nil
}

func isEnum(td TypeDecl) bool {
	for _, c := range td.Constructors {
		if len(c.Fields) > 0 {
			return false
		}
	}
	return true
}

func typeZeroValue(typeName string, goBase string) string {
	switch goBase {
	case "int", "float64":
		return "0"
	case "string":
		return `""`
	case "bool":
		return "false"
	default:
		return typeName + "{}"
	}
}

// replaceTrailingUnit replaces Unit (struct{}{}) at the end of an expression with IRVoidExpr.
// Handles blocks: if the block's final expression is Unit, replace it.
func replaceTrailingUnit(expr IRExpr) IRExpr {
	if isIRUnit(expr) {
		return IRVoidExpr{}
	}
	if block, ok := expr.(IRBlock); ok && block.Expr != nil {
		if isIRUnit(block.Expr) {
			block.Expr = IRVoidExpr{}
			return block
		}
	}
	return expr
}

// isIRUnit checks if an IR expression is the Unit value (struct{}{}).
func isIRUnit(expr IRExpr) bool {
	if ident, ok := expr.(IRIdent); ok {
		return ident.GoName == "struct{}{}"
	}
	return false
}

func collectUsedIdents(expr Expr) map[string]bool {
	used := make(map[string]bool)
	collectIdents(expr, used)
	return used
}

func collectIdents(expr Expr, used map[string]bool) {
	switch e := expr.(type) {
	case Ident:
		used[e.Name] = true
	case FnCall:
		collectIdents(e.Fn, used)
		for _, a := range e.Args {
			collectIdents(a, used)
		}
	case FieldAccess:
		collectIdents(e.Expr, used)
	case MatchExpr:
		collectIdents(e.Subject, used)
		for _, arm := range e.Arms {
			collectIdents(arm.Body, used)
		}
	case Block:
		for _, s := range e.Stmts {
			switch st := s.(type) {
			case LetStmt:
				collectIdents(st.Value, used)
			case ExprStmt:
				collectIdents(st.Expr, used)
			}
		}
		if e.Expr != nil {
			collectIdents(e.Expr, used)
		}
	case ConstructorCall:
		for _, f := range e.Fields {
			collectIdents(f.Value, used)
		}
	case StringInterp:
		for _, p := range e.Parts {
			collectIdents(p, used)
		}
	case RefExpr:
		collectIdents(e.Expr, used)
	case ListLit:
		for _, el := range e.Elements {
			collectIdents(el, used)
		}
	case BinaryExpr:
		collectIdents(e.Left, used)
		collectIdents(e.Right, used)
	case Lambda:
		collectIdents(e.Body, used)
	}
}

// stripCommonIndent removes common leading whitespace from multiline strings.
// Trailing whitespace-only line (indentation before closing """) is removed.
func stripCommonIndent(s string) string {
	lines := strings.Split(s, "\n")

	// Remove trailing whitespace-only line
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	// Find minimum indentation
	minIndent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if minIndent < 0 || indent < minIndent {
			minIndent = indent
		}
	}
	if minIndent <= 0 {
		return strings.Join(lines, "\n")
	}

	for i, line := range lines {
		if len(line) >= minIndent {
			lines[i] = line[minIndent:]
		}
	}
	return strings.Join(lines, "\n")
}

// stripMultilineInterpIndent strips common indentation from string tokens
// in an interpolated multiline string.
func stripMultilineInterpIndent(tokens []Token) []Token {
	// Collect all literal content to compute common indent
	var allText strings.Builder
	for _, t := range tokens {
		if t.Kind == TkString {
			allText.WriteString(t.Lit)
		}
	}
	combined := allText.String()
	lines := strings.Split(combined, "\n")

	// Remove trailing whitespace-only line
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	minIndent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if minIndent < 0 || indent < minIndent {
			minIndent = indent
		}
	}
	if minIndent <= 0 {
		return tokens
	}

	for i := range tokens {
		if tokens[i].Kind != TkString {
			continue
		}
		tLines := strings.Split(tokens[i].Lit, "\n")
		for j, line := range tLines {
			if len(line) >= minIndent {
				tLines[j] = line[minIndent:]
			}
		}
		tokens[i].Lit = strings.Join(tLines, "\n")
	}
	return tokens
}
