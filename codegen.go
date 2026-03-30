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
	functions      map[string]FnDecl   // arca name -> fn decl
	ctorTypes      map[string]string   // constructor name -> type name
	tmpCounter     int
}

func NewCodeGen(prog *Program) *CodeGen {
	cg := &CodeGen{
		types:        make(map[string]TypeDecl),
		usedBuiltins: make(map[string]bool),
		fnNames:      make(map[string]string),
		functions:    make(map[string]FnDecl),
		ctorTypes:    make(map[string]string),
	}
	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case TypeDecl:
			cg.types[d.Name] = d
			for _, ctor := range d.Constructors {
				cg.ctorTypes[ctor.Name] = d.Name
			}
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

func (cg *CodeGen) preScan(prog *Program) {
	// Scan for features that need imports
	for _, decl := range prog.Decls {
		if td, ok := decl.(TypeDecl); ok {
			if cg.hasConstraints(td) {
				for _, f := range td.Constructors[0].Fields {
					if nt, ok := f.Type.(NamedType); ok {
						for _, c := range nt.Constraints {
							if c.Key == "pattern" {
								cg.usedBuiltins["regexp"] = true
							}
						}
					}
				}
			}
		}
	}
}

func (cg *CodeGen) Generate(prog *Program) string {
	cg.preScan(prog)
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
		if cg.usedBuiltins["regexp"] {
			cg.writeln("\t\"regexp\"")
		}
		cg.writeln(")")
		cg.writeln("")
	}

	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case TypeDecl:
			cg.genTypeDecl(d)
			cg.writeln("")
		case TypeAliasDecl:
			// Type aliases are resolved at use site, no Go code needed
		case FnDecl:
			cg.genFnDecl(d)
			cg.writeln("")
		}
	}
	cg.genBuiltins()
	return cg.buf.String()
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

func goTypeParams(td TypeDecl) string {
	if len(td.Params) == 0 {
		return ""
	}
	params := make([]string, len(td.Params))
	for i, p := range td.Params {
		params[i] = p + " any"
	}
	return "[" + strings.Join(params, ", ") + "]"
}

func goTypeParamsNames(td TypeDecl) string {
	if len(td.Params) == 0 {
		return ""
	}
	return "[" + strings.Join(td.Params, ", ") + "]"
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

func (cg *CodeGen) hasConstraints(td TypeDecl) bool {
	if len(td.Constructors) != 1 {
		return false
	}
	for _, f := range td.Constructors[0].Fields {
		if nt, ok := f.Type.(NamedType); ok && len(nt.Constraints) > 0 {
			return true
		}
	}
	return false
}

func (cg *CodeGen) genStructType(td TypeDecl) {
	ctor := td.Constructors[0]
	cg.writeln(fmt.Sprintf("type %s%s struct {", td.Name, goTypeParams(td)))
	for _, f := range ctor.Fields {
		cg.writeln(fmt.Sprintf("\t%s %s", capitalize(f.Name), cg.goType(f.Type)))
	}
	cg.writeln("}")

	if cg.hasConstraints(td) {
		cg.genValidatingConstructor(td)
	}
}

func (cg *CodeGen) genValidatingConstructor(td TypeDecl) {
	ctor := td.Constructors[0]
	// func NewUser(id int, name string, age int) (User, error) {
	params := make([]string, len(ctor.Fields))
	for i, f := range ctor.Fields {
		params[i] = fmt.Sprintf("%s %s", snakeToCamel(f.Name), cg.goType(f.Type))
	}
	cg.writeln("")
	cg.writeln(fmt.Sprintf("func New%s(%s) (%s, error) {", td.Name, strings.Join(params, ", "), td.Name))

	// Generate validation checks
	for _, f := range ctor.Fields {
		nt, ok := f.Type.(NamedType)
		if !ok || len(nt.Constraints) == 0 {
			continue
		}
		fieldVar := snakeToCamel(f.Name)
		for _, c := range nt.Constraints {
			valStr := cg.genExprStr(c.Value)
			switch c.Key {
			case "min":
				cg.writeln(fmt.Sprintf("\tif %s < %s {", fieldVar, valStr))
				cg.writeln(fmt.Sprintf("\t\treturn %s{}, fmt.Errorf(\"%s: must be >= %s\")", td.Name, f.Name, valStr))
				cg.writeln("\t}")
			case "max":
				cg.writeln(fmt.Sprintf("\tif %s > %s {", fieldVar, valStr))
				cg.writeln(fmt.Sprintf("\t\treturn %s{}, fmt.Errorf(\"%s: must be <= %s\")", td.Name, f.Name, valStr))
				cg.writeln("\t}")
			case "min_length":
				cg.writeln(fmt.Sprintf("\tif len(%s) < %s {", fieldVar, valStr))
				cg.writeln(fmt.Sprintf("\t\treturn %s{}, fmt.Errorf(\"%s: min length %s\")", td.Name, f.Name, valStr))
				cg.writeln("\t}")
			case "max_length":
				cg.writeln(fmt.Sprintf("\tif len(%s) > %s {", fieldVar, valStr))
				cg.writeln(fmt.Sprintf("\t\treturn %s{}, fmt.Errorf(\"%s: max length %s\")", td.Name, f.Name, valStr))
				cg.writeln("\t}")
			case "pattern":
				cg.usedBuiltins["regexp"] = true
				cg.writeln(fmt.Sprintf("\tif !regexp.MustCompile(%s).MatchString(%s) {", valStr, fieldVar))
				cg.writeln(fmt.Sprintf("\t\treturn %s{}, fmt.Errorf(\"%s: must match pattern\")", td.Name, f.Name))
				cg.writeln("\t}")
			case "validate":
				cg.writeln(fmt.Sprintf("\tif !%s(%s) {", valStr, fieldVar))
				cg.writeln(fmt.Sprintf("\t\treturn %s{}, fmt.Errorf(\"%s: validation failed\")", td.Name, f.Name))
				cg.writeln("\t}")
			}
		}
	}

	// Return constructed value
	fields := make([]string, len(ctor.Fields))
	for i, f := range ctor.Fields {
		fields[i] = fmt.Sprintf("%s: %s", capitalize(f.Name), snakeToCamel(f.Name))
	}
	cg.writeln(fmt.Sprintf("\treturn %s{%s}, nil", td.Name, strings.Join(fields, ", ")))
	cg.writeln("}")
}

func (cg *CodeGen) genSumType(td TypeDecl) {
	tp := goTypeParams(td)
	cg.writeln(fmt.Sprintf("type %s%s interface {", td.Name, tp))
	cg.writeln(fmt.Sprintf("\tis%s()", td.Name))
	cg.writeln("}")
	cg.writeln("")
	for _, c := range td.Constructors {
		variantName := td.Name + c.Name
		if len(c.Fields) == 0 {
			cg.writeln(fmt.Sprintf("type %s%s struct{}", variantName, tp))
		} else {
			cg.writeln(fmt.Sprintf("type %s%s struct {", variantName, tp))
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
			cg.usedBuiltins["result"] = true
			if len(tt.Params) >= 2 {
				return "Result_[" + cg.goType(tt.Params[0]) + ", " + cg.goType(tt.Params[1]) + "]"
			}
			if len(tt.Params) == 1 {
				return "Result_[" + cg.goType(tt.Params[0]) + ", error]"
			}
			return "Result_[interface{}, error]"
		default:
			return tt.Name
		}
	case PointerType:
		return "*" + cg.goType(tt.Inner)
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
		// All built-in constructors (Ok, Error, Some, None) handled by genExprStr
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
			cg.genVoidBody(e.Expr, indent)
		}
	case MatchExpr:
		cg.genMatchExpr(e, indent, false)
	default:
		cg.writeln(fmt.Sprintf("%s%s", indent, cg.genExprStr(expr)))
	}
}

func (cg *CodeGen) genStmt(stmt Stmt, indent string) {
	switch s := stmt.(type) {
	case LetStmt:
		// Destructuring: let [first, ..rest] = expr
		if s.Pattern != nil {
			cg.genLetDestructure(s.Pattern, s.Value, indent)
			return
		}
		// Check for ? operator: let x = expr?
		if call, ok := s.Value.(FnCall); ok && cg.isTriCall(call) {
			cg.genTryLetStmt(s.Name, call.Args[0], indent)
			return
		}
		cg.writeln(fmt.Sprintf("%s%s := %s", indent, snakeToCamel(s.Name), cg.genExprStr(s.Value)))
	case AssertStmt:
		exprStr := cg.genExprStr(s.Expr)
		cg.writeln(fmt.Sprintf("%sif !(%s) {", indent, exprStr))
		cg.writeln(fmt.Sprintf("%s\tpanic(%q)", indent, "assertion failed: "+exprStr))
		cg.writeln(fmt.Sprintf("%s}", indent))
	case ExprStmt:
		switch e := s.Expr.(type) {
		case ForExpr:
			cg.genForExpr(e, indent)
		case MatchExpr:
			cg.genMatchExpr(e, indent, false)
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
			case "to_bytes":
				if len(e.Args) == 1 {
					return fmt.Sprintf("[]byte(%s)", cg.genExprStr(e.Args[0]))
				}
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
		// Built-in Result constructors
		if e.Name == "Ok" && len(e.Fields) == 1 {
			cg.usedBuiltins["result"] = true
			val := cg.genExprStr(e.Fields[0].Value)
			typeArgs := cg.resultTypeArgs()
			return fmt.Sprintf("Ok_%s(%s)", typeArgs, val)
		}
		if e.Name == "Error" && len(e.Fields) == 1 {
			cg.usedBuiltins["result"] = true
			val := cg.genExprStr(e.Fields[0].Value)
			typeArgs := cg.resultTypeArgs()
			return fmt.Sprintf("Err_%s(%s)", typeArgs, val)
		}
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
	case ListLit:
		return cg.genListLit(e)
	case BinaryExpr:
		return fmt.Sprintf("%s %s %s", cg.genExprStr(e.Left), e.Op, cg.genExprStr(e.Right))
	case RangeExpr:
		return cg.genRange(e)
	default:
		return "/* unsupported expr */"
	}
}

func (cg *CodeGen) genExprWithContext(expr Expr, call FnCall, argIndex int) string {
	// Resolve empty list type from function parameter
	if ll, ok := expr.(ListLit); ok && len(ll.Elements) == 0 && ll.Spread == nil {
		if fnIdent, ok := call.Fn.(Ident); ok {
			if fn, ok := cg.functions[fnIdent.Name]; ok && argIndex < len(fn.Params) {
				paramType := fn.Params[argIndex].Type
				if nt, ok := paramType.(NamedType); ok && nt.Name == "List" && len(nt.Params) > 0 {
					return fmt.Sprintf("[]%s{}", cg.goType(nt.Params[0]))
				}
			}
		}
	}
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

func (cg *CodeGen) inferGoType(expr Expr) string {
	switch expr.(type) {
	case IntLit:
		return "int"
	case FloatLit:
		return "float64"
	case StringLit, StringInterp:
		return "string"
	case BoolLit:
		return "bool"
	default:
		return "interface{}"
	}
}

func (cg *CodeGen) genTuple(t TupleExpr) string {
	if len(t.Elements) == 2 {
		t1 := cg.inferGoType(t.Elements[0])
		t2 := cg.inferGoType(t.Elements[1])
		return fmt.Sprintf("struct{ First %s; Second %s }{%s, %s}",
			t1, t2, cg.genExprStr(t.Elements[0]), cg.genExprStr(t.Elements[1]))
	}
	elems := make([]string, len(t.Elements))
	for i, e := range t.Elements {
		elems[i] = cg.genExprStr(e)
	}
	return fmt.Sprintf("/* tuple(%s) */", strings.Join(elems, ", "))
}

func (cg *CodeGen) genListLit(l ListLit) string {
	if len(l.Elements) == 0 && l.Spread == nil {
		return "[]interface{}{}"
	}
	// Spread: [a, b, ..rest] → append([]T{a, b}, rest...)
	if l.Spread != nil {
		if len(l.Elements) == 0 {
			return cg.genExprStr(l.Spread)
		}
		elems := make([]string, len(l.Elements))
		for i, e := range l.Elements {
			elems[i] = cg.genExprStr(e)
		}
		elemType := cg.inferGoElemType(l.Elements[0])
		return fmt.Sprintf("append([]%s{%s}, %s...)", elemType, strings.Join(elems, ", "), cg.genExprStr(l.Spread))
	}
	elems := make([]string, len(l.Elements))
	for i, e := range l.Elements {
		elems[i] = cg.genExprStr(e)
	}
	elemType := cg.inferGoElemType(l.Elements[0])
	return fmt.Sprintf("[]%s{%s}", elemType, strings.Join(elems, ", "))
}

func (cg *CodeGen) inferGoElemType(expr Expr) string {
	switch expr.(type) {
	case IntLit:
		return "int"
	case FloatLit:
		return "float64"
	case StringLit, StringInterp:
		return "string"
	case BoolLit:
		return "bool"
	default:
		return "interface{}"
	}
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
				// Constrained type: use NewType() constructor
				if cg.hasConstraints(td) {
					args := make([]string, len(cc.Fields))
					for i, f := range cc.Fields {
						args[i] = cg.genExprStr(f.Value)
					}
					return fmt.Sprintf("New%s(%s)", goName, strings.Join(args, ", "))
				}
				fields := make([]string, len(cc.Fields))
				for i, f := range cc.Fields {
					if f.Name != "" {
						fields[i] = fmt.Sprintf("%s: %s", capitalize(f.Name), cg.genExprStr(f.Value))
					} else {
						fields[i] = cg.genExprStr(f.Value)
					}
				}
				// Add type parameters if generic
				typeArgs := ""
				if len(td.Params) > 0 {
					args := make([]string, len(cc.Fields))
					for i, f := range cc.Fields {
						args[i] = cg.inferGoType(f.Value)
					}
					typeArgs = "[" + strings.Join(args, ", ") + "]"
				}
				return fmt.Sprintf("%s%s{%s}", goName, typeArgs, strings.Join(fields, ", "))
			}
		}
	}
	return fmt.Sprintf("%s{/* unknown */}", cc.Name)
}


// --- Helpers ---


func (cg *CodeGen) genLetDestructure(pat Pattern, value Expr, indent string) {
	valStr := cg.genExprStr(value)
	switch p := pat.(type) {
	case TuplePattern:
		cg.tmpCounter++
		tmp := fmt.Sprintf("__tuple%d", cg.tmpCounter)
		cg.writeln(fmt.Sprintf("%s%s := %s", indent, tmp, valStr))
		for i, elemPat := range p.Elements {
			if bp, ok := elemPat.(BindPattern); ok {
				field := fmt.Sprintf("First")
				if i == 1 {
					field = "Second"
				}
				cg.writeln(fmt.Sprintf("%s%s := %s.%s", indent, snakeToCamel(bp.Name), tmp, field))
			}
		}
	case ListPattern:
		cg.tmpCounter++
		tmp := fmt.Sprintf("__list%d", cg.tmpCounter)
		cg.writeln(fmt.Sprintf("%s%s := %s", indent, tmp, valStr))
		for i, elemPat := range p.Elements {
			if bp, ok := elemPat.(BindPattern); ok {
				cg.writeln(fmt.Sprintf("%s%s := %s[%d]", indent, snakeToCamel(bp.Name), tmp, i))
			}
		}
		if p.Rest != "" {
			cg.writeln(fmt.Sprintf("%s%s := %s[%d:]", indent, snakeToCamel(p.Rest), tmp, len(p.Elements)))
		}
	}
}

func (cg *CodeGen) resultTypeArgs() string {
	if cg.currentRetType == nil {
		return ""
	}
	if nt, ok := cg.currentRetType.(NamedType); ok && nt.Name == "Result" {
		if len(nt.Params) >= 2 {
			return "[" + cg.goType(nt.Params[0]) + ", " + cg.goType(nt.Params[1]) + "]"
		}
		if len(nt.Params) == 1 {
			return "[" + cg.goType(nt.Params[0]) + ", error]"
		}
	}
	return ""
}

func (cg *CodeGen) isTriCall(call FnCall) bool {
	if ident, ok := call.Fn.(Ident); ok && ident.Name == "__try" && len(call.Args) == 1 {
		return true
	}
	return false
}

func (cg *CodeGen) genTryLetStmt(name string, expr Expr, indent string) {
	cg.tmpCounter++
	tmpVal := fmt.Sprintf("__try_val%d", cg.tmpCounter)
	tmpErr := fmt.Sprintf("__try_err%d", cg.tmpCounter)
	cg.writeln(fmt.Sprintf("%s%s, %s := %s", indent, tmpVal, tmpErr, cg.genExprStr(expr)))
	cg.writeln(fmt.Sprintf("%sif %s != nil {", indent, tmpErr))
	if cg.currentRetType != nil && isResultType(cg.currentRetType) {
		cg.usedBuiltins["result"] = true
		typeArgs := cg.resultTypeArgs()
		cg.writeln(fmt.Sprintf("%s\treturn Err_%s(%s)", indent, typeArgs, tmpErr))
	} else {
		cg.writeln(fmt.Sprintf("%s\tpanic(%s)", indent, tmpErr))
	}
	cg.writeln(fmt.Sprintf("%s}", indent))
	cg.writeln(fmt.Sprintf("%s%s := %s", indent, snakeToCamel(name), tmpVal))
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
