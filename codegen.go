package main

import (
	"fmt"
	"strings"
)

type CodeGen struct {
	buf   strings.Builder
	types map[string]TypeDecl // type name -> decl
}

func NewCodeGen(prog *Program) *CodeGen {
	cg := &CodeGen{types: make(map[string]TypeDecl)}
	// Collect type declarations
	for _, decl := range prog.Decls {
		if td, ok := decl.(TypeDecl); ok {
			cg.types[td.Name] = td
		}
	}
	return cg
}

func (cg *CodeGen) Generate(prog *Program) string {
	cg.writeln("package main")
	cg.writeln("")

	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case TypeDecl:
			cg.genTypeDecl(d)
		case FnDecl:
			cg.genFnDecl(d)
		}
		cg.writeln("")
	}
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

	// String method
	cg.writeln("")
	cg.writeln(fmt.Sprintf("func (v %s) String() string {", td.Name))
	cg.writeln("\tswitch v {")
	for _, c := range td.Constructors {
		cg.writeln(fmt.Sprintf("\tcase %s%s:", td.Name, c.Name))
		cg.writeln(fmt.Sprintf("\t\treturn \"%s\"", c.Name))
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
	// Interface
	ifaceName := td.Name
	cg.writeln(fmt.Sprintf("type %s interface {", ifaceName))
	cg.writeln(fmt.Sprintf("\tis%s()", ifaceName))
	cg.writeln("}")
	cg.writeln("")

	// Variants
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
		cg.writeln(fmt.Sprintf("func (%s) is%s() {}", variantName, ifaceName))
		cg.writeln("")
	}
}

func (cg *CodeGen) goType(t Type) string {
	nt, ok := t.(NamedType)
	if !ok {
		return "interface{}"
	}
	switch nt.Name {
	case "Int":
		return "int64"
	case "Float":
		return "float64"
	case "String":
		return "string"
	case "Bool":
		return "bool"
	case "List":
		if len(nt.Params) > 0 {
			return "[]" + cg.goType(nt.Params[0])
		}
		return "[]interface{}"
	case "Option":
		if len(nt.Params) > 0 {
			return "*" + cg.goType(nt.Params[0])
		}
		return "interface{}"
	default:
		return nt.Name
	}
}

// --- Function Generation ---

func (cg *CodeGen) genFnDecl(fd FnDecl) {
	params := make([]string, len(fd.Params))
	for i, p := range fd.Params {
		params[i] = fmt.Sprintf("%s %s", p.Name, cg.goType(p.Type))
	}
	retType := cg.goType(fd.ReturnType)

	cg.writeln(fmt.Sprintf("func %s(%s) %s {", fd.Name, strings.Join(params, ", "), retType))
	cg.genReturnExpr(fd.Body, "\t")
	cg.writeln("}")
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
	default:
		cg.writeln(fmt.Sprintf("%sreturn %s", indent, cg.genExprStr(expr)))
	}
}

func (cg *CodeGen) genStmt(stmt Stmt, indent string) {
	switch s := stmt.(type) {
	case LetStmt:
		cg.writeln(fmt.Sprintf("%s%s := %s", indent, s.Name, cg.genExprStr(s.Value)))
	}
}

func (cg *CodeGen) genExprStr(expr Expr) string {
	switch e := expr.(type) {
	case IntLit:
		return fmt.Sprintf("%d", e.Value)
	case FloatLit:
		return fmt.Sprintf("%f", e.Value)
	case StringLit:
		return fmt.Sprintf("%q", e.Value)
	case BoolLit:
		if e.Value {
			return "true"
		}
		return "false"
	case Ident:
		// Check if this is an enum constructor
		if typeName := cg.findTypeName(e.Name); typeName != "" {
			if td, ok := cg.types[typeName]; ok && isEnum(td) {
				return fmt.Sprintf("%s%s", typeName, e.Name)
			}
		}
		return e.Name
	case FnCall:
		args := make([]string, len(e.Args))
		for i, a := range e.Args {
			args[i] = cg.genExprStr(a)
		}
		return fmt.Sprintf("%s(%s)", cg.genExprStr(e.Fn), strings.Join(args, ", "))
	case FieldAccess:
		return fmt.Sprintf("%s.%s", cg.genExprStr(e.Expr), capitalize(e.Field))
	case ConstructorCall:
		return cg.genConstructorCall(e)
	default:
		return "/* unsupported expr */"
	}
}

func (cg *CodeGen) genConstructorCall(cc ConstructorCall) string {
	// Check if this is a constructor for a known type
	for typeName, td := range cg.types {
		for _, ctor := range td.Constructors {
			if ctor.Name == cc.Name {
				if isEnum(td) {
					return fmt.Sprintf("%s%s", typeName, cc.Name)
				}
				if len(td.Constructors) == 1 {
					// Single constructor struct
					fields := make([]string, len(cc.Fields))
					for i, f := range cc.Fields {
						if f.Name != "" {
							fields[i] = fmt.Sprintf("%s: %s", capitalize(f.Name), cg.genExprStr(f.Value))
						} else {
							fields[i] = cg.genExprStr(f.Value)
						}
					}
					return fmt.Sprintf("%s{%s}", typeName, strings.Join(fields, ", "))
				}
				// Sum type variant
				variantName := typeName + cc.Name
				fields := make([]string, len(cc.Fields))
				for i, f := range cc.Fields {
					if f.Name != "" {
						fields[i] = fmt.Sprintf("%s: %s", capitalize(f.Name), cg.genExprStr(f.Value))
					} else {
						fields[i] = cg.genExprStr(f.Value)
					}
				}
				return fmt.Sprintf("%s{%s}", variantName, strings.Join(fields, ", "))
			}
		}
	}
	return fmt.Sprintf("%s{/* unknown */}", cc.Name)
}

// --- Match Expression ---

func (cg *CodeGen) genMatchExpr(me MatchExpr, indent string, isReturn bool) {
	subject := cg.genExprStr(me.Subject)

	// Check if matching against an enum
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

	// Sum type match -> type switch
	cg.writeln(fmt.Sprintf("%sswitch v := %s.(type) {", indent, subject))
	for _, arm := range me.Arms {
		switch pat := arm.Pattern.(type) {
		case ConstructorPattern:
			typeName := cg.findTypeName(pat.Name)
			variantName := typeName + pat.Name
			cg.writeln(fmt.Sprintf("%scase %s:", indent, variantName))
			// Bind fields
			usedVars := collectUsedIdents(arm.Body)
			for _, fp := range pat.Fields {
				if _, used := usedVars[fp.Binding]; used {
					cg.writeln(fmt.Sprintf("%s\t%s := v.%s", indent, fp.Binding, capitalize(fp.Name)))
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
			cg.writeln(fmt.Sprintf("%s\t%s := v", indent, pat.Name))
			if isReturn {
				cg.writeln(fmt.Sprintf("%s\treturn %s", indent, cg.genExprStr(arm.Body)))
			}
		}
	}
	cg.writeln(fmt.Sprintf("%s}", indent))
	// unreachable return for compiler
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

func capitalize(s string) string {
	if len(s) == 0 {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
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
	}
}
