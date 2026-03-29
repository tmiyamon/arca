package main

import (
	"fmt"
	"strings"
)

type CodeGen struct {
	buf            strings.Builder
	types          map[string]TypeDecl
	imports        []string
	currentRetType Type
	usedBuiltins   map[string]bool   // track which builtins are used
	fnNames        map[string]string  // arca name -> go name (for pub functions)
	functions      map[string]FnDecl // arca name -> fn decl
}

func NewCodeGen(prog *Program) *CodeGen {
	cg := &CodeGen{
		types:        make(map[string]TypeDecl),
		usedBuiltins: make(map[string]bool),
		fnNames:      make(map[string]string),
		functions:    make(map[string]FnDecl),
	}
	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case TypeDecl:
			cg.types[d.Name] = d
		case ImportDecl:
			cg.imports = append(cg.imports, d.Path)
		case FnDecl:
			cg.functions[d.Name] = d
			if d.Public {
				cg.fnNames[d.Name] = snakeToPascal(d.Name)
			} else if strings.Contains(d.Name, "_") {
				cg.fnNames[d.Name] = snakeToCamel(d.Name)
			}
		}
	}
	return cg
}

func (cg *CodeGen) Generate(prog *Program) string {
	cg.writeln("package main")
	cg.writeln("")

	// Generate imports
	if len(cg.imports) > 0 {
		cg.writeln("import (")
		for _, imp := range cg.imports {
			// Strip "go/" prefix for Go standard library
			goImp := imp
			if strings.HasPrefix(goImp, "go/") {
				goImp = goImp[3:]
			}
			cg.writeln(fmt.Sprintf("\t%q", goImp))
		}
		cg.writeln(")")
		cg.writeln("")
	}

	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case TypeDecl:
			cg.genTypeDecl(d)
			cg.writeln("")
		case FnDecl:
			cg.genFnDecl(d)
			cg.writeln("")
		}
	}
	cg.genBuiltins()
	return cg.buf.String()
}

func (cg *CodeGen) genBuiltins() {
	if cg.usedBuiltins["option"] {
		cg.writeln("type Option_[T any] struct {")
		cg.writeln("\tValue T")
		cg.writeln("\tValid bool")
		cg.writeln("}")
		cg.writeln("")
		cg.writeln("func Some_[T any](v T) Option_[T] {")
		cg.writeln("\treturn Option_[T]{Value: v, Valid: true}")
		cg.writeln("}")
		cg.writeln("")
		cg.writeln("func None_[T any]() Option_[T] {")
		cg.writeln("\treturn Option_[T]{}")
		cg.writeln("}")
		cg.writeln("")
	}
	if cg.usedBuiltins["map"] {
		cg.writeln("func Map_[T any, U any](list []T, f func(T) U) []U {")
		cg.writeln("\tresult := make([]U, len(list))")
		cg.writeln("\tfor i, v := range list {")
		cg.writeln("\t\tresult[i] = f(v)")
		cg.writeln("\t}")
		cg.writeln("\treturn result")
		cg.writeln("}")
		cg.writeln("")
	}
	if cg.usedBuiltins["filter"] {
		cg.writeln("func Filter_[T any](list []T, f func(T) bool) []T {")
		cg.writeln("\tvar result []T")
		cg.writeln("\tfor _, v := range list {")
		cg.writeln("\t\tif f(v) {")
		cg.writeln("\t\t\tresult = append(result, v)")
		cg.writeln("\t\t}")
		cg.writeln("\t}")
		cg.writeln("\treturn result")
		cg.writeln("}")
		cg.writeln("")
	}
	if cg.usedBuiltins["fold"] {
		cg.writeln("func Fold_[T any, U any](list []T, init U, f func(U, T) U) U {")
		cg.writeln("\tacc := init")
		cg.writeln("\tfor _, v := range list {")
		cg.writeln("\t\tacc = f(acc, v)")
		cg.writeln("\t}")
		cg.writeln("\treturn acc")
		cg.writeln("}")
		cg.writeln("")
	}
}

func (cg *CodeGen) write(s string) {
	cg.buf.WriteString(s)
}

func (cg *CodeGen) writeln(s string) {
	cg.buf.WriteString(s)
	cg.buf.WriteString("\n")
}

// --- Type Generation ---

func (cg *CodeGen) genTypeDecl(td TypeDecl) {
	if isEnum(td) {
		cg.genEnumType(td)
	} else if len(td.Constructors) == 1 {
		cg.genStructType(td)
	} else {
		cg.genSumType(td)
	}
}

func isEnum(td TypeDecl) bool {
	for _, c := range td.Constructors {
		if len(c.Fields) > 0 {
			return false
		}
	}
	return true
}

func (cg *CodeGen) genEnumType(td TypeDecl) {
	cg.writeln(fmt.Sprintf("type %s int", td.Name))
	cg.writeln("")
	cg.writeln("const (")
	for i, c := range td.Constructors {
		if i == 0 {
			cg.writeln(fmt.Sprintf("\t%s%s %s = iota", td.Name, c.Name, td.Name))
		} else {
			cg.writeln(fmt.Sprintf("\t%s%s", td.Name, c.Name))
		}
	}
	cg.writeln(")")
	cg.writeln("")
	cg.writeln(fmt.Sprintf("func (v %s) String() string {", td.Name))
	cg.writeln("\tswitch v {")
	for _, c := range td.Constructors {
		cg.writeln(fmt.Sprintf("\tcase %s%s:", td.Name, c.Name))
		cg.writeln(fmt.Sprintf("\t\treturn %q", c.Name))
	}
	cg.writeln("\tdefault:")
	cg.writeln(fmt.Sprintf("\t\treturn \"Unknown%s\"", td.Name))
	cg.writeln("\t}")
	cg.writeln("}")
}

func (cg *CodeGen) genStructType(td TypeDecl) {
	ctor := td.Constructors[0]
	cg.writeln(fmt.Sprintf("type %s struct {", td.Name))
	for _, f := range ctor.Fields {
		cg.writeln(fmt.Sprintf("\t%s %s", capitalize(f.Name), cg.goType(f.Type)))
	}
	cg.writeln("}")
}

func (cg *CodeGen) genSumType(td TypeDecl) {
	cg.writeln(fmt.Sprintf("type %s interface {", td.Name))
	cg.writeln(fmt.Sprintf("\tis%s()", td.Name))
	cg.writeln("}")
	cg.writeln("")
	for _, c := range td.Constructors {
		variantName := td.Name + c.Name
		if len(c.Fields) == 0 {
			cg.writeln(fmt.Sprintf("type %s struct{}", variantName))
		} else {
			cg.writeln(fmt.Sprintf("type %s struct {", variantName))
			for _, f := range c.Fields {
				cg.writeln(fmt.Sprintf("\t%s %s", capitalize(f.Name), cg.goType(f.Type)))
			}
			cg.writeln("}")
		}
		cg.writeln(fmt.Sprintf("func (%s) is%s() {}", variantName, td.Name))
		cg.writeln("")
	}
}

func (cg *CodeGen) goType(t Type) string {
	switch tt := t.(type) {
	case NamedType:
		switch tt.Name {
		case "Int":
			return "int"
		case "Float":
			return "float64"
		case "String":
			return "string"
		case "Bool":
			return "bool"
		case "List":
			if len(tt.Params) > 0 {
				return "[]" + cg.goType(tt.Params[0])
			}
			return "[]interface{}"
		case "Option":
			if len(tt.Params) > 0 {
				cg.usedBuiltins["option"] = true
				return "Option_[" + cg.goType(tt.Params[0]) + "]"
			}
			return "interface{}"
		case "Result":
			if len(tt.Params) > 0 {
				return "(" + cg.goType(tt.Params[0]) + ", error)"
			}
			return "(interface{}, error)"
		default:
			return tt.Name
		}
	case TupleType:
		// Generate a tuple struct or use a generic approach
		// For now, use a simple struct
		if len(tt.Elements) == 2 {
			return fmt.Sprintf("struct{ First %s; Second %s }", cg.goType(tt.Elements[0]), cg.goType(tt.Elements[1]))
		}
		return "interface{}"
	default:
		return "interface{}"
	}
}

// --- Function Generation ---

func (cg *CodeGen) genFnDecl(fd FnDecl) {
	name := fd.Name
	if fd.Public {
		name = snakeToPascal(name)
	} else {
		name = snakeToCamel(name)
	}
	params := make([]string, len(fd.Params))
	for i, p := range fd.Params {
		params[i] = fmt.Sprintf("%s %s", snakeToCamel(p.Name), cg.goType(p.Type))
	}

	retType := ""
	if fd.ReturnType != nil {
		retType = " " + cg.goType(fd.ReturnType)
	}

	cg.currentRetType = fd.ReturnType
	cg.writeln(fmt.Sprintf("func %s(%s)%s {", name, strings.Join(params, ", "), retType))
	if fd.ReturnType != nil {
		cg.genReturnExpr(fd.Body, "\t")
	} else {
		cg.genVoidBody(fd.Body, "\t")
	}
	cg.writeln("}")
	cg.currentRetType = nil
}

func (cg *CodeGen) genReturnExpr(expr Expr, indent string) {
	switch e := expr.(type) {
	case MatchExpr:
		cg.genMatchExpr(e, indent, true)
	case Block:
		for _, stmt := range e.Stmts {
			cg.genStmt(stmt, indent)
		}
		if e.Expr != nil {
			cg.genReturnExpr(e.Expr, indent)
		}
	case ConstructorCall:
		if cg.currentRetType != nil && isResultType(cg.currentRetType) {
			if e.Name == "Ok" && len(e.Fields) == 1 {
				cg.writeln(fmt.Sprintf("%sreturn %s, nil", indent, cg.genExprStr(e.Fields[0].Value)))
				return
			}
			if e.Name == "Error" && len(e.Fields) == 1 {
				okType := resultOkType(cg.currentRetType)
				cg.writeln(fmt.Sprintf("%sreturn %s, %s", indent, cg.goZeroValue(okType), cg.genExprStr(e.Fields[0].Value)))
				return
			}
		}
		// Some/None handled by genExprStr
		cg.writeln(fmt.Sprintf("%sreturn %s", indent, cg.genExprStr(expr)))
	default:
		cg.writeln(fmt.Sprintf("%sreturn %s", indent, cg.genExprStr(expr)))
	}
}

func (cg *CodeGen) genVoidBody(expr Expr, indent string) {
	switch e := expr.(type) {
	case Block:
		for _, stmt := range e.Stmts {
			cg.genStmt(stmt, indent)
		}
		if e.Expr != nil {
			cg.writeln(fmt.Sprintf("%s%s", indent, cg.genExprStr(e.Expr)))
		}
	default:
		cg.writeln(fmt.Sprintf("%s%s", indent, cg.genExprStr(expr)))
	}
}

func (cg *CodeGen) genStmt(stmt Stmt, indent string) {
	switch s := stmt.(type) {
	case LetStmt:
		// Check for ? operator: let x = expr?
		if call, ok := s.Value.(FnCall); ok && cg.isTriCall(call) {
			cg.genTryLetStmt(s.Name, call.Args[0], indent)
			return
		}
		cg.writeln(fmt.Sprintf("%s%s := %s", indent, snakeToCamel(s.Name), cg.genExprStr(s.Value)))
	case ExprStmt:
		switch e := s.Expr.(type) {
		case ForExpr:
			cg.genForExpr(e, indent)
		default:
			cg.writeln(fmt.Sprintf("%s%s", indent, cg.genExprStr(s.Expr)))
		}
	}
}

func (cg *CodeGen) genExprStr(expr Expr) string {
	switch e := expr.(type) {
	case IntLit:
		return fmt.Sprintf("%d", e.Value)
	case FloatLit:
		return fmt.Sprintf("%g", e.Value)
	case StringLit:
		return fmt.Sprintf("%q", e.Value)
	case StringInterp:
		return cg.genStringInterp(e)
	case BoolLit:
		if e.Value {
			return "true"
		}
		return "false"
	case Ident:
		// Built-in constants
		if e.Name == "None" {
			cg.usedBuiltins["option"] = true
			return "None_[any]()"
		}
		if typeName := cg.findTypeName(e.Name); typeName != "" {
			if td, ok := cg.types[typeName]; ok && isEnum(td) {
				return fmt.Sprintf("%s%s", typeName, e.Name)
			}
		}
		if goName, ok := cg.fnNames[e.Name]; ok {
			return goName
		}
		// Don't transform qualified names (Go FFI like fmt.Println)
		if strings.Contains(e.Name, ".") {
			return e.Name
		}
		return snakeToCamel(e.Name)
	case FnCall:
		// Track builtin usage
		if ident, ok := e.Fn.(Ident); ok {
			switch ident.Name {
			case "map":
				cg.usedBuiltins["map"] = true
				args := make([]string, len(e.Args))
				for i, a := range e.Args {
					args[i] = cg.genExprStr(a)
				}
				return fmt.Sprintf("Map_(%s)", strings.Join(args, ", "))
			case "filter":
				cg.usedBuiltins["filter"] = true
				args := make([]string, len(e.Args))
				for i, a := range e.Args {
					args[i] = cg.genExprStr(a)
				}
				return fmt.Sprintf("Filter_(%s)", strings.Join(args, ", "))
			case "fold":
				cg.usedBuiltins["fold"] = true
				args := make([]string, len(e.Args))
				for i, a := range e.Args {
					args[i] = cg.genExprStr(a)
				}
				return fmt.Sprintf("Fold_(%s)", strings.Join(args, ", "))
			}
		}
		args := make([]string, len(e.Args))
		for i, a := range e.Args {
			args[i] = cg.genExprWithContext(a, e, i)
		}
		return fmt.Sprintf("%s(%s)", cg.genExprStr(e.Fn), strings.Join(args, ", "))
	case FieldAccess:
		return fmt.Sprintf("%s.%s", cg.genExprStr(e.Expr), capitalize(e.Field))
	case ConstructorCall:
		// Built-in Option constructors
		if e.Name == "Some" && len(e.Fields) == 1 {
			cg.usedBuiltins["option"] = true
			val := cg.genExprStr(e.Fields[0].Value)
			return fmt.Sprintf("Some_(%s)", val)
		}
		if e.Name == "None" {
			cg.usedBuiltins["option"] = true
			return "None_[any]()"
		}
		return cg.genConstructorCall(e)
	case Lambda:
		return cg.genLambda(e)
	case TupleExpr:
		return cg.genTuple(e)
	case BinaryExpr:
		return fmt.Sprintf("%s %s %s", cg.genExprStr(e.Left), e.Op, cg.genExprStr(e.Right))
	case RangeExpr:
		return cg.genRange(e)
	default:
		return "/* unsupported expr */"
	}
}

func (cg *CodeGen) genExprWithContext(expr Expr, call FnCall, argIndex int) string {
	// Resolve None type from function parameter
	if ident, ok := expr.(Ident); ok && ident.Name == "None" {
		if fnIdent, ok := call.Fn.(Ident); ok {
			if fn, ok := cg.functions[fnIdent.Name]; ok && argIndex < len(fn.Params) {
				paramType := fn.Params[argIndex].Type
				if nt, ok := paramType.(NamedType); ok && nt.Name == "Option" && len(nt.Params) > 0 {
					cg.usedBuiltins["option"] = true
					return fmt.Sprintf("None_[%s]()", cg.goType(nt.Params[0]))
				}
			}
		}
	}
	return cg.genExprStr(expr)
}

func (cg *CodeGen) genStringInterp(si StringInterp) string {
	var fmtParts []string
	var args []string
	for _, part := range si.Parts {
		if lit, ok := part.(StringLit); ok {
			fmtParts = append(fmtParts, lit.Value)
		} else {
			fmtParts = append(fmtParts, "%v")
			args = append(args, cg.genExprStr(part))
		}
	}
	fmtStr := strings.Join(fmtParts, "")
	if len(args) == 0 {
		return fmt.Sprintf("%q", fmtStr)
	}
	return fmt.Sprintf("fmt.Sprintf(%q, %s)", fmtStr, strings.Join(args, ", "))
}

func (cg *CodeGen) genLambda(l Lambda) string {
	params := make([]string, len(l.Params))
	for i, p := range l.Params {
		if p.Type != nil {
			params[i] = fmt.Sprintf("%s %s", p.Name, cg.goType(p.Type))
		} else {
			params[i] = p.Name
		}
	}
	body := cg.genExprStr(l.Body)
	retType := ""
	if l.ReturnType != nil {
		retType = " " + cg.goType(l.ReturnType)
	}
	return fmt.Sprintf("func(%s)%s { return %s }", strings.Join(params, ", "), retType, body)
}

func (cg *CodeGen) genTuple(t TupleExpr) string {
	if len(t.Elements) == 2 {
		return fmt.Sprintf("struct{ First interface{}; Second interface{} }{%s, %s}",
			cg.genExprStr(t.Elements[0]), cg.genExprStr(t.Elements[1]))
	}
	elems := make([]string, len(t.Elements))
	for i, e := range t.Elements {
		elems[i] = cg.genExprStr(e)
	}
	return fmt.Sprintf("/* tuple(%s) */", strings.Join(elems, ", "))
}

func (cg *CodeGen) genRange(r RangeExpr) string {
	return fmt.Sprintf("__range(%s, %s)", cg.genExprStr(r.Start), cg.genExprStr(r.End))
}

func (cg *CodeGen) genForExpr(fe ForExpr, indent string) {
	switch iter := fe.Iter.(type) {
	case RangeExpr:
		b := snakeToCamel(fe.Binding)
		cg.writeln(fmt.Sprintf("%sfor %s := %s; %s < %s; %s++ {",
			indent, b, cg.genExprStr(iter.Start),
			b, cg.genExprStr(iter.End), b))
	default:
		cg.writeln(fmt.Sprintf("%sfor _, %s := range %s {", indent, snakeToCamel(fe.Binding), cg.genExprStr(fe.Iter)))
	}
	cg.genVoidBody(fe.Body, indent+"\t")
	cg.writeln(fmt.Sprintf("%s}", indent))
}

func (cg *CodeGen) genConstructorCall(cc ConstructorCall) string {
	for typeName, td := range cg.types {
		for _, ctor := range td.Constructors {
			if ctor.Name == cc.Name {
				if isEnum(td) {
					return fmt.Sprintf("%s%s", typeName, cc.Name)
				}
				goName := typeName
				if len(td.Constructors) > 1 {
					goName = typeName + cc.Name
				}
				fields := make([]string, len(cc.Fields))
				for i, f := range cc.Fields {
					if f.Name != "" {
						fields[i] = fmt.Sprintf("%s: %s", capitalize(f.Name), cg.genExprStr(f.Value))
					} else {
						fields[i] = cg.genExprStr(f.Value)
					}
				}
				return fmt.Sprintf("%s{%s}", goName, strings.Join(fields, ", "))
			}
		}
	}
	return fmt.Sprintf("%s{/* unknown */}", cc.Name)
}

// --- Match Expression ---

func (cg *CodeGen) isOptionMatch(me MatchExpr) bool {
	for _, arm := range me.Arms {
		if cp, ok := arm.Pattern.(ConstructorPattern); ok {
			if cp.Name == "Some" || cp.Name == "None" {
				return true
			}
		}
	}
	return false
}

func (cg *CodeGen) genOptionMatch(me MatchExpr, indent string, isReturn bool) {
	subject := cg.genExprStr(me.Subject)
	for _, arm := range me.Arms {
		cp, ok := arm.Pattern.(ConstructorPattern)
		if !ok {
			continue
		}
		if cp.Name == "Some" {
			cg.writeln(fmt.Sprintf("%sif %s.Valid {", indent, subject))
			if len(cp.Fields) > 0 {
				cg.writeln(fmt.Sprintf("%s\t%s := %s.Value", indent, snakeToCamel(cp.Fields[0].Binding), subject))
			}
			if isReturn {
				cg.writeln(fmt.Sprintf("%s\treturn %s", indent, cg.genExprStr(arm.Body)))
			} else {
				cg.writeln(fmt.Sprintf("%s\t%s", indent, cg.genExprStr(arm.Body)))
			}
		}
		if cp.Name == "None" {
			cg.writeln(fmt.Sprintf("%s} else {", indent))
			if isReturn {
				cg.writeln(fmt.Sprintf("%s\treturn %s", indent, cg.genExprStr(arm.Body)))
			} else {
				cg.writeln(fmt.Sprintf("%s\t%s", indent, cg.genExprStr(arm.Body)))
			}
		}
	}
	cg.writeln(fmt.Sprintf("%s}", indent))
}

func (cg *CodeGen) genMatchExpr(me MatchExpr, indent string, isReturn bool) {
	subject := cg.genExprStr(me.Subject)

	if cg.isOptionMatch(me) {
		cg.genOptionMatch(me, indent, isReturn)
		return
	}

	if cg.isEnumMatch(me) {
		cg.writeln(fmt.Sprintf("%sswitch %s {", indent, subject))
		for _, arm := range me.Arms {
			cp, ok := arm.Pattern.(ConstructorPattern)
			if ok {
				typeName := cg.findTypeName(cp.Name)
				cg.writeln(fmt.Sprintf("%scase %s%s:", indent, typeName, cp.Name))
			} else {
				cg.writeln(fmt.Sprintf("%sdefault:", indent))
			}
			if isReturn {
				cg.writeln(fmt.Sprintf("%s\treturn %s", indent, cg.genExprStr(arm.Body)))
			} else {
				cg.writeln(fmt.Sprintf("%s\t%s", indent, cg.genExprStr(arm.Body)))
			}
		}
		if isReturn {
			cg.writeln(fmt.Sprintf("%sdefault:", indent))
			cg.writeln(fmt.Sprintf("%s\tpanic(\"unreachable\")", indent))
		}
		cg.writeln(fmt.Sprintf("%s}", indent))
		return
	}

	cg.writeln(fmt.Sprintf("%sswitch v := %s.(type) {", indent, subject))
	for _, arm := range me.Arms {
		switch pat := arm.Pattern.(type) {
		case ConstructorPattern:
			typeName := cg.findTypeName(pat.Name)
			variantName := typeName + pat.Name
			cg.writeln(fmt.Sprintf("%scase %s:", indent, variantName))
			usedVars := collectUsedIdents(arm.Body)
			for _, fp := range pat.Fields {
				if _, used := usedVars[fp.Binding]; used {
					cg.writeln(fmt.Sprintf("%s\t%s := v.%s", indent, snakeToCamel(fp.Binding), capitalize(fp.Name)))
				}
			}
			if isReturn {
				cg.writeln(fmt.Sprintf("%s\treturn %s", indent, cg.genExprStr(arm.Body)))
			}
		case WildcardPattern:
			cg.writeln(fmt.Sprintf("%sdefault:", indent))
			if isReturn {
				cg.writeln(fmt.Sprintf("%s\treturn %s", indent, cg.genExprStr(arm.Body)))
			}
		case BindPattern:
			cg.writeln(fmt.Sprintf("%sdefault:", indent))
			cg.writeln(fmt.Sprintf("%s\t%s := v", indent, snakeToCamel(pat.Name)))
			if isReturn {
				cg.writeln(fmt.Sprintf("%s\treturn %s", indent, cg.genExprStr(arm.Body)))
			}
		}
	}
	cg.writeln(fmt.Sprintf("%s}", indent))
	if isReturn {
		cg.writeln(fmt.Sprintf("%spanic(\"unreachable\")", indent))
	}
}

func (cg *CodeGen) isEnumMatch(me MatchExpr) bool {
	for _, arm := range me.Arms {
		if cp, ok := arm.Pattern.(ConstructorPattern); ok {
			typeName := cg.findTypeName(cp.Name)
			if td, ok := cg.types[typeName]; ok {
				return isEnum(td)
			}
		}
	}
	return false
}

func (cg *CodeGen) findTypeName(ctorName string) string {
	for typeName, td := range cg.types {
		for _, c := range td.Constructors {
			if c.Name == ctorName {
				return typeName
			}
		}
	}
	return ""
}

// --- Helpers ---

func (cg *CodeGen) isTriCall(call FnCall) bool {
	if ident, ok := call.Fn.(Ident); ok && ident.Name == "__try" && len(call.Args) == 1 {
		return true
	}
	return false
}

func (cg *CodeGen) genTryLetStmt(name string, expr Expr, indent string) {
	cg.writeln(fmt.Sprintf("%s%s, err := %s", indent, snakeToCamel(name), cg.genExprStr(expr)))
	cg.writeln(fmt.Sprintf("%sif err != nil {", indent))
	if cg.currentRetType != nil && isResultType(cg.currentRetType) {
		okType := resultOkType(cg.currentRetType)
		cg.writeln(fmt.Sprintf("%s\treturn %s, err", indent, cg.goZeroValue(okType)))
	} else {
		cg.writeln(fmt.Sprintf("%s\tpanic(err)", indent))
	}
	cg.writeln(fmt.Sprintf("%s}", indent))
}

func isResultType(t Type) bool {
	if nt, ok := t.(NamedType); ok {
		return nt.Name == "Result"
	}
	return false
}

func resultOkType(t Type) Type {
	if nt, ok := t.(NamedType); ok && nt.Name == "Result" && len(nt.Params) > 0 {
		return nt.Params[0]
	}
	return nil
}

func (cg *CodeGen) goZeroValue(t Type) string {
	switch tt := t.(type) {
	case NamedType:
		switch tt.Name {
		case "Int", "Float":
			return "0"
		case "String":
			return `""`
		case "Bool":
			return "false"
		case "List":
			return "nil"
		default:
			return tt.Name + "{}"
		}
	default:
		return "nil"
	}
}

func capitalize(s string) string {
	if len(s) == 0 {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func snakeToCamel(s string) string {
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if i > 0 {
			parts[i] = capitalize(p)
		}
	}
	return strings.Join(parts, "")
}

func snakeToPascal(s string) string {
	parts := strings.Split(s, "_")
	for i, p := range parts {
		parts[i] = capitalize(p)
	}
	return strings.Join(parts, "")
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
			if ls, ok := s.(LetStmt); ok {
				collectIdents(ls.Value, used)
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
	case BinaryExpr:
		collectIdents(e.Left, used)
		collectIdents(e.Right, used)
	case Lambda:
		collectIdents(e.Body, used)
	}
}
