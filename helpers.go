package main

import "strings"

type goImportEntry struct {
	path       string
	sideEffect bool
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
