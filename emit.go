package main

import (
	"fmt"
	"strings"
)

// Emitter converts an IRProgram into a Go source string.
// All complex logic (name resolution, constructor resolution, match classification)
// was handled in lower.go. The Emitter is simple and mechanical.
type Emitter struct {
	buf        strings.Builder
	tmpCounter int
}

// Emit is the main entry point. It produces a complete Go source file.
func (em *Emitter) Emit(prog IRProgram) string {
	// Generate body first so we know what's needed
	var body strings.Builder
	prevBuf := em.buf
	em.buf = body

	for _, td := range prog.Types {
		em.emitTypeDecl(td)
		em.writeln("")
	}
	for _, fd := range prog.Funcs {
		em.emitFuncDecl(fd)
		em.writeln("")
	}
	em.emitBuiltins(prog.Builtins)
	bodyStr := em.buf.String()

	em.buf = prevBuf
	em.buf.Reset()

	// Package declaration
	pkg := prog.Package
	if pkg == "" {
		pkg = "main"
	}
	em.writeln(fmt.Sprintf("package %s", pkg))
	em.writeln("")

	// Imports
	em.emitImports(prog.Imports)

	// Body
	em.buf.WriteString(bodyStr)

	return em.buf.String()
}

func (em *Emitter) emitImports(imports []IRImport) {
	if len(imports) == 0 {
		return
	}
	em.writeln("import (")
	for _, imp := range imports {
		if imp.SideEffect {
			em.writeln(fmt.Sprintf("\t_ %q", imp.Path))
		} else {
			em.writeln(fmt.Sprintf("\t%q", imp.Path))
		}
	}
	em.writeln(")")
	em.writeln("")
}

// --- Type Declarations ---

func (em *Emitter) emitTypeDecl(td IRTypeDecl) {
	switch d := td.(type) {
	case IREnumDecl:
		em.emitEnumDecl(d)
	case IRStructDecl:
		em.emitStructDecl(d)
	case IRSumTypeDecl:
		em.emitSumTypeDecl(d)
	case IRTypeAliasDecl:
		em.emitTypeAliasDecl(d)
	}
}

func (em *Emitter) emitEnumDecl(d IREnumDecl) {
	em.writeln(fmt.Sprintf("type %s int", d.GoName))
	em.writeln("")
	em.writeln("const (")
	for i, v := range d.Variants {
		if i == 0 {
			em.writeln(fmt.Sprintf("\t%s%s %s = iota", d.GoName, v, d.GoName))
		} else {
			em.writeln(fmt.Sprintf("\t%s%s", d.GoName, v))
		}
	}
	em.writeln(")")
	em.writeln("")
	em.writeln(fmt.Sprintf("func (v %s) String() string {", d.GoName))
	em.writeln("\tswitch v {")
	for _, v := range d.Variants {
		em.writeln(fmt.Sprintf("\tcase %s%s:", d.GoName, v))
		em.writeln(fmt.Sprintf("\t\treturn %q", v))
	}
	em.writeln("\tdefault:")
	em.writeln(fmt.Sprintf("\t\treturn \"Unknown%s\"", d.GoName))
	em.writeln("\t}")
	em.writeln("}")
}

func (em *Emitter) emitStructDecl(d IRStructDecl) {
	typeParams := em.goTypeParams(d.TypeParams)
	em.writeln(fmt.Sprintf("type %s%s struct {", d.GoName, typeParams))
	for _, f := range d.Fields {
		typeStr := em.irTypeStr(f.Type)
		if f.Tag != "" {
			em.writeln(fmt.Sprintf("\t%s %s %s", f.GoName, typeStr, f.Tag))
		} else {
			em.writeln(fmt.Sprintf("\t%s %s", f.GoName, typeStr))
		}
	}
	em.writeln("}")

	if d.Validator != nil {
		em.emitStructValidator(d)
	}
}

func (em *Emitter) emitStructValidator(d IRStructDecl) {
	// func NewType(params...) (Type, error) {
	params := make([]string, len(d.Fields))
	for i, f := range d.Fields {
		params[i] = fmt.Sprintf("%s %s", lowerFirst(f.GoName), em.irTypeStr(f.Type))
	}
	em.writeln("")
	em.writeln(fmt.Sprintf("func New%s(%s) (%s, error) {", d.GoName, strings.Join(params, ", "), d.GoName))

	em.emitValidator(d.Validator)

	// Return constructed value
	fields := make([]string, len(d.Fields))
	for i, f := range d.Fields {
		fields[i] = fmt.Sprintf("%s: %s", f.GoName, lowerFirst(f.GoName))
	}
	em.writeln(fmt.Sprintf("\treturn %s{%s}, nil", d.GoName, strings.Join(fields, ", ")))
	em.writeln("}")
}

func (em *Emitter) emitSumTypeDecl(d IRSumTypeDecl) {
	tp := em.goTypeParams(d.TypeParams)
	em.writeln(fmt.Sprintf("type %s%s interface {", d.GoName, tp))
	em.writeln(fmt.Sprintf("\tis%s()", d.GoName))
	for _, m := range d.InterfaceMethods {
		params := make([]string, len(m.Params))
		for i, p := range m.Params {
			params[i] = fmt.Sprintf("%s %s", p.GoName, em.irTypeStr(p.Type))
		}
		retStr := ""
		if m.ReturnType != nil {
			retStr = " " + em.irTypeStr(m.ReturnType)
		}
		em.writeln(fmt.Sprintf("\t%s(%s)%s", m.Name, strings.Join(params, ", "), retStr))
	}
	em.writeln("}")
	em.writeln("")
	for _, v := range d.Variants {
		if len(v.Fields) == 0 {
			em.writeln(fmt.Sprintf("type %s%s struct{}", v.GoName, tp))
		} else {
			em.writeln(fmt.Sprintf("type %s%s struct {", v.GoName, tp))
			for _, f := range v.Fields {
				em.writeln(fmt.Sprintf("\t%s %s", f.GoName, em.irTypeStr(f.Type)))
			}
			em.writeln("}")
		}
		em.writeln(fmt.Sprintf("func (%s) is%s() {}", v.GoName, d.GoName))
		em.writeln("")
	}
}

func (em *Emitter) emitTypeAliasDecl(d IRTypeAliasDecl) {
	em.writeln(fmt.Sprintf("type %s %s", d.GoName, d.GoBase))

	if d.Validator == nil {
		return
	}

	zeroVal := typeZeroValue(d.GoName, d.GoBase)
	em.writeln("")
	em.writeln(fmt.Sprintf("func New%s(v %s) (%s, error) {", d.GoName, d.GoBase, d.GoName))
	em.emitValidatorAlias(d.Validator, d.GoName, zeroVal, d.GoBase)
	em.writeln(fmt.Sprintf("\treturn %s(v), nil", d.GoName))
	em.writeln("}")
}

func (em *Emitter) emitValidator(v *IRValidator) {
	for _, check := range v.Checks {
		switch check.Kind {
		case "min":
			em.writeln(fmt.Sprintf("\tif %s < %s {", check.Field, check.Value))
			em.writeln(fmt.Sprintf("\t\treturn %s, fmt.Errorf(\"%s: must be >= %s\")", check.ZeroVal, check.TypeName, check.Value))
			em.writeln("\t}")
		case "max":
			em.writeln(fmt.Sprintf("\tif %s > %s {", check.Field, check.Value))
			em.writeln(fmt.Sprintf("\t\treturn %s, fmt.Errorf(\"%s: must be <= %s\")", check.ZeroVal, check.TypeName, check.Value))
			em.writeln("\t}")
		case "min_length":
			em.writeln(fmt.Sprintf("\tif len(%s) < %s {", check.Field, check.Value))
			em.writeln(fmt.Sprintf("\t\treturn %s, fmt.Errorf(\"%s: min length %s\")", check.ZeroVal, check.TypeName, check.Value))
			em.writeln("\t}")
		case "max_length":
			em.writeln(fmt.Sprintf("\tif len(%s) > %s {", check.Field, check.Value))
			em.writeln(fmt.Sprintf("\t\treturn %s, fmt.Errorf(\"%s: max length %s\")", check.ZeroVal, check.TypeName, check.Value))
			em.writeln("\t}")
		case "pattern":
			em.writeln(fmt.Sprintf("\tif !regexp.MustCompile(%s).MatchString(%s) {", check.Value, check.Field))
			em.writeln(fmt.Sprintf("\t\treturn %s, fmt.Errorf(\"%s: must match pattern\")", check.ZeroVal, check.TypeName))
			em.writeln("\t}")
		case "validate":
			em.writeln(fmt.Sprintf("\tif !%s(%s) {", check.Value, check.Field))
			em.writeln(fmt.Sprintf("\t\treturn %s, fmt.Errorf(\"%s: validation failed\")", check.ZeroVal, check.TypeName))
			em.writeln("\t}")
		}
	}
}

func (em *Emitter) emitValidatorAlias(v *IRValidator, typeName, zeroVal, goBase string) {
	for _, check := range v.Checks {
		field := check.Field // "v" for aliases
		switch check.Kind {
		case "min":
			em.writeln(fmt.Sprintf("\tif %s < %s {", field, check.Value))
			em.writeln(fmt.Sprintf("\t\treturn %s, fmt.Errorf(\"must be >= %s\")", zeroVal, check.Value))
			em.writeln("\t}")
		case "max":
			em.writeln(fmt.Sprintf("\tif %s > %s {", field, check.Value))
			em.writeln(fmt.Sprintf("\t\treturn %s, fmt.Errorf(\"must be <= %s\")", zeroVal, check.Value))
			em.writeln("\t}")
		case "min_length":
			em.writeln(fmt.Sprintf("\tif len(%s) < %s {", field, check.Value))
			em.writeln(fmt.Sprintf("\t\treturn %s, fmt.Errorf(\"min length %s\")", zeroVal, check.Value))
			em.writeln("\t}")
		case "max_length":
			em.writeln(fmt.Sprintf("\tif len(%s) > %s {", field, check.Value))
			em.writeln(fmt.Sprintf("\t\treturn %s, fmt.Errorf(\"max length %s\")", zeroVal, check.Value))
			em.writeln("\t}")
		case "pattern":
			em.writeln(fmt.Sprintf("\tif !regexp.MustCompile(%s).MatchString(string(%s)) {", check.Value, field))
			em.writeln(fmt.Sprintf("\t\treturn %s, fmt.Errorf(\"must match pattern\")", zeroVal))
			em.writeln("\t}")
		case "validate":
			em.writeln(fmt.Sprintf("\tif !%s(%s) {", check.Value, field))
			em.writeln(fmt.Sprintf("\t\treturn %s, fmt.Errorf(\"validation failed\")", zeroVal))
			em.writeln("\t}")
		}
	}
}

// --- Function Declarations ---

func (em *Emitter) emitFuncDecl(fd IRFuncDecl) {
	params := make([]string, len(fd.Params))
	for i, p := range fd.Params {
		params[i] = fmt.Sprintf("%s %s", p.GoName, em.irTypeStr(p.Type))
	}

	retType := ""
	if fd.ReturnType != nil {
		retType = " " + em.irTypeStr(fd.ReturnType)
	}

	if fd.Receiver != nil {
		em.writeln(fmt.Sprintf("func (%s %s) %s(%s)%s {",
			fd.Receiver.GoName, fd.Receiver.Type,
			fd.GoName, strings.Join(params, ", "), retType))
	} else {
		em.writeln(fmt.Sprintf("func %s(%s)%s {",
			fd.GoName, strings.Join(params, ", "), retType))
	}

	if fd.ReturnType != nil {
		em.emitReturnExpr(fd.Body, "\t")
	} else {
		em.emitVoidBody(fd.Body, "\t")
	}
	em.writeln("}")
}

// --- Expressions ---

func (em *Emitter) emitExpr(e IRExpr) string {
	if e == nil {
		return ""
	}
	switch expr := e.(type) {
	case IRIntLit:
		return fmt.Sprintf("%d", expr.Value)
	case IRFloatLit:
		return fmt.Sprintf("%g", expr.Value)
	case IRStringLit:
		return fmt.Sprintf("%q", expr.Value)
	case IRBoolLit:
		if expr.Value {
			return "true"
		}
		return "false"
	case IRIdent:
		return expr.GoName
	case IRStringInterp:
		args := make([]string, len(expr.Args))
		for i, a := range expr.Args {
			args[i] = em.emitExpr(a)
		}
		return fmt.Sprintf("fmt.Sprintf(%q, %s)", expr.Format, strings.Join(args, ", "))
	case IRFnCall:
		args := make([]string, len(expr.Args))
		for i, a := range expr.Args {
			args[i] = em.emitExpr(a)
		}
		return fmt.Sprintf("%s(%s)", expr.Func, strings.Join(args, ", "))
	case IRMethodCall:
		args := make([]string, len(expr.Args))
		for i, a := range expr.Args {
			args[i] = em.emitExpr(a)
		}
		return fmt.Sprintf("%s.%s(%s)", em.emitExpr(expr.Receiver), expr.Method, strings.Join(args, ", "))
	case IRFieldAccess:
		return fmt.Sprintf("%s.%s", em.emitExpr(expr.Expr), expr.Field)
	case IRConstructorCall:
		return em.emitConstructorCall(expr)
	case IROkCall:
		return fmt.Sprintf("Ok_%s(%s)", expr.TypeArgs, em.emitExpr(expr.Value))
	case IRErrorCall:
		return fmt.Sprintf("Err_%s(%s)", expr.TypeArgs, em.emitExpr(expr.Value))
	case IRSomeCall:
		return fmt.Sprintf("Some_(%s)", em.emitExpr(expr.Value))
	case IRNoneExpr:
		return fmt.Sprintf("None_%s()", expr.TypeArg)
	case IRLambda:
		return em.emitLambda(expr)
	case IRBinaryExpr:
		return fmt.Sprintf("%s %s %s", em.emitExpr(expr.Left), expr.Op, em.emitExpr(expr.Right))
	case IRListLit:
		return em.emitListLit(expr)
	case IRTupleLit:
		return em.emitTupleLit(expr)
	case IRRefExpr:
		return "&" + em.emitExpr(expr.Expr)
	default:
		return "/* unsupported expr */"
	}
}

func (em *Emitter) emitConstructorCall(cc IRConstructorCall) string {
	if cc.ReturnsResult {
		// Constrained constructor: NewType(args...)
		args := make([]string, len(cc.Fields))
		for i, f := range cc.Fields {
			args[i] = em.emitExpr(f.Value)
		}
		return fmt.Sprintf("%s(%s)", cc.GoName, strings.Join(args, ", "))
	}

	// Struct literal (named or positional)
	fields := make([]string, len(cc.Fields))
	for i, f := range cc.Fields {
		if f.GoName != "" {
			fields[i] = fmt.Sprintf("%s: %s", f.GoName, em.emitExpr(f.Value))
		} else {
			fields[i] = em.emitExpr(f.Value)
		}
	}
	return fmt.Sprintf("%s%s{%s}", cc.GoName, cc.TypeArgs, strings.Join(fields, ", "))
}

func (em *Emitter) emitLambda(l IRLambda) string {
	params := make([]string, len(l.Params))
	for i, p := range l.Params {
		if p.Type != nil {
			params[i] = fmt.Sprintf("%s %s", p.GoName, em.irTypeStr(p.Type))
		} else {
			params[i] = p.GoName
		}
	}
	retType := ""
	if l.ReturnType != nil {
		retType = " " + em.irTypeStr(l.ReturnType)
	}
	body := em.emitExpr(l.Body)
	if l.ReturnType != nil {
		return fmt.Sprintf("func(%s)%s { return %s }", strings.Join(params, ", "), retType, body)
	}
	return fmt.Sprintf("func(%s) { %s }", strings.Join(params, ", "), body)
}

func (em *Emitter) emitListLit(l IRListLit) string {
	if len(l.Elements) == 0 && l.Spread == nil {
		return fmt.Sprintf("[]%s{}", l.ElemType)
	}
	// Spread: append([]T{elems}, spread...)
	if l.Spread != nil {
		if len(l.Elements) == 0 {
			return em.emitExpr(l.Spread)
		}
		elems := make([]string, len(l.Elements))
		for i, e := range l.Elements {
			elems[i] = em.emitExpr(e)
		}
		return fmt.Sprintf("append([]%s{%s}, %s...)", l.ElemType, strings.Join(elems, ", "), em.emitExpr(l.Spread))
	}
	elems := make([]string, len(l.Elements))
	for i, e := range l.Elements {
		elems[i] = em.emitExpr(e)
	}
	return fmt.Sprintf("[]%s{%s}", l.ElemType, strings.Join(elems, ", "))
}

func (em *Emitter) emitTupleLit(t IRTupleLit) string {
	if len(t.Elements) == 2 {
		t1 := em.inferGoTypeFromIR(t.Elements[0])
		t2 := em.inferGoTypeFromIR(t.Elements[1])
		return fmt.Sprintf("struct{ First %s; Second %s }{%s, %s}",
			t1, t2, em.emitExpr(t.Elements[0]), em.emitExpr(t.Elements[1]))
	}
	elems := make([]string, len(t.Elements))
	for i, e := range t.Elements {
		elems[i] = em.emitExpr(e)
	}
	return fmt.Sprintf("/* tuple(%s) */", strings.Join(elems, ", "))
}

// --- Return/Void Body Modes ---

func (em *Emitter) emitReturnExpr(e IRExpr, indent string) {
	if e == nil {
		return
	}
	switch expr := e.(type) {
	case IRBlock:
		for _, stmt := range expr.Stmts {
			em.emitStmt(stmt, indent)
		}
		if expr.Expr != nil {
			em.emitReturnExpr(expr.Expr, indent)
		}
	case IRResultMatch:
		em.emitResultMatch(expr, indent, true)
	case IROptionMatch:
		em.emitOptionMatch(expr, indent, true)
	case IREnumMatch:
		em.emitEnumMatch(expr, indent, true)
	case IRSumTypeMatch:
		em.emitSumTypeMatch(expr, indent, true)
	case IRListMatch:
		em.emitListMatch(expr, indent, true)
	case IRLiteralMatch:
		em.emitLiteralMatch(expr, indent, true)
	default:
		em.writeln(fmt.Sprintf("%sreturn %s", indent, em.emitExpr(e)))
	}
}

func (em *Emitter) emitVoidBody(e IRExpr, indent string) {
	if e == nil {
		return
	}
	switch expr := e.(type) {
	case IRBlock:
		for _, stmt := range expr.Stmts {
			em.emitStmt(stmt, indent)
		}
		if expr.Expr != nil {
			em.emitVoidBody(expr.Expr, indent)
		}
	case IRResultMatch:
		em.emitResultMatch(expr, indent, false)
	case IROptionMatch:
		em.emitOptionMatch(expr, indent, false)
	case IREnumMatch:
		em.emitEnumMatch(expr, indent, false)
	case IRSumTypeMatch:
		em.emitSumTypeMatch(expr, indent, false)
	case IRListMatch:
		em.emitListMatch(expr, indent, false)
	case IRLiteralMatch:
		em.emitLiteralMatch(expr, indent, false)
	case IRForRange:
		em.emitForRange(expr, indent)
	case IRForEach:
		em.emitForEach(expr, indent)
	default:
		em.writeln(fmt.Sprintf("%s%s", indent, em.emitExpr(e)))
	}
}

func (em *Emitter) emitArmBody(body IRExpr, indent string, isReturn bool) {
	if isReturn {
		em.emitReturnExpr(body, indent)
	} else {
		em.emitVoidBody(body, indent)
	}
}

// --- Statements ---

func (em *Emitter) emitStmt(s IRStmt, indent string) {
	switch stmt := s.(type) {
	case IRLetStmt:
		em.emitLetStmt(stmt, indent)
	case IRConstrainedLetStmt:
		em.emitConstrainedLetStmt(stmt, indent)
	case IRTryLetStmt:
		em.emitTryLetStmt(stmt, indent)
	case IRExprStmt:
		em.emitExprStmt(stmt, indent)
	case IRDeferStmt:
		em.writeln(fmt.Sprintf("%sdefer %s", indent, em.emitExpr(stmt.Expr)))
	case IRAssertStmt:
		exprStr := em.emitExpr(stmt.Expr)
		em.writeln(fmt.Sprintf("%sif !(%s) {", indent, exprStr))
		em.writeln(fmt.Sprintf("%s\tpanic(%q)", indent, "assertion failed: "+stmt.ExprStr))
		em.writeln(fmt.Sprintf("%s}", indent))
	case IRDestructureStmt:
		em.emitDestructureStmt(stmt, indent)
	}
}

func (em *Emitter) emitLetStmt(stmt IRLetStmt, indent string) {
	if stmt.GoName == "_" {
		em.writeln(fmt.Sprintf("%s_ = %s", indent, em.emitExpr(stmt.Value)))
		return
	}
	if stmt.Type != nil {
		// Check for empty list: var x []Type
		if ll, ok := stmt.Value.(IRListLit); ok && len(ll.Elements) == 0 && ll.Spread == nil {
			em.writeln(fmt.Sprintf("%svar %s %s", indent, stmt.GoName, em.irTypeStr(stmt.Type)))
			return
		}
		em.writeln(fmt.Sprintf("%svar %s %s = %s", indent, stmt.GoName, em.irTypeStr(stmt.Type), em.emitExpr(stmt.Value)))
		return
	}
	em.writeln(fmt.Sprintf("%s%s := %s", indent, stmt.GoName, em.emitExpr(stmt.Value)))
}

func (em *Emitter) emitConstrainedLetStmt(stmt IRConstrainedLetStmt, indent string) {
	em.tmpCounter++
	tmpErr := fmt.Sprintf("__cerr%d", em.tmpCounter)

	if stmt.ErrorOnly {
		// Go func returns error only: err := f()
		em.writeln(fmt.Sprintf("%s%s := %s", indent, tmpErr, em.emitExpr(stmt.CallExpr)))
		em.writeln(fmt.Sprintf("%svar %s Result_[%s, error]", indent, stmt.GoName, stmt.GoType))
		em.writeln(fmt.Sprintf("%sif %s != nil {", indent, tmpErr))
		em.writeln(fmt.Sprintf("%s\t%s = Err_[%s, error](%s)", indent, stmt.GoName, stmt.GoType, tmpErr))
		em.writeln(fmt.Sprintf("%s} else {", indent))
		em.writeln(fmt.Sprintf("%s\t%s = Ok_[%s, error](%s{})", indent, stmt.GoName, stmt.GoType, stmt.GoType))
		em.writeln(fmt.Sprintf("%s}", indent))
	} else {
		// Go func returns (T, error): val, err := f()
		tmpVal := fmt.Sprintf("__cval%d", em.tmpCounter)
		em.writeln(fmt.Sprintf("%s%s, %s := %s", indent, tmpVal, tmpErr, em.emitExpr(stmt.CallExpr)))
		em.writeln(fmt.Sprintf("%svar %s Result_[%s, error]", indent, stmt.GoName, stmt.GoType))
		em.writeln(fmt.Sprintf("%sif %s != nil {", indent, tmpErr))
		em.writeln(fmt.Sprintf("%s\t%s = Err_[%s, error](%s)", indent, stmt.GoName, stmt.GoType, tmpErr))
		em.writeln(fmt.Sprintf("%s} else {", indent))
		em.writeln(fmt.Sprintf("%s\t%s = Ok_[%s, error](%s)", indent, stmt.GoName, stmt.GoType, tmpVal))
		em.writeln(fmt.Sprintf("%s}", indent))
	}
}

func (em *Emitter) emitTryLetStmt(stmt IRTryLetStmt, indent string) {
	em.tmpCounter++
	tmpErr := fmt.Sprintf("__try_err%d", em.tmpCounter)

	if stmt.ErrorOnly {
		// Go func returns error only: err := f()
		em.writeln(fmt.Sprintf("%s%s := %s", indent, tmpErr, em.emitExpr(stmt.CallExpr)))
	} else {
		// Go func returns (T, error): val, err := f()
		tmpVal := "_"
		if stmt.GoName != "_" {
			tmpVal = fmt.Sprintf("__try_val%d", em.tmpCounter)
		}
		em.writeln(fmt.Sprintf("%s%s, %s := %s", indent, tmpVal, tmpErr, em.emitExpr(stmt.CallExpr)))
		defer func() {
			if stmt.GoName != "_" {
				em.writeln(fmt.Sprintf("%s%s := %s", indent, stmt.GoName, tmpVal))
			}
		}()
	}

	em.writeln(fmt.Sprintf("%sif %s != nil {", indent, tmpErr))
	if isIRResultType(stmt.ReturnType) {
		typeArgs := irResultTypeArgs(stmt.ReturnType)
		em.writeln(fmt.Sprintf("%s\treturn Err_%s(%s)", indent, typeArgs, tmpErr))
	} else {
		em.writeln(fmt.Sprintf("%s\tpanic(%s)", indent, tmpErr))
	}
	em.writeln(fmt.Sprintf("%s}", indent))
}

func (em *Emitter) emitExprStmt(stmt IRExprStmt, indent string) {
	switch e := stmt.Expr.(type) {
	case IRForRange:
		em.emitForRange(e, indent)
	case IRForEach:
		em.emitForEach(e, indent)
	case IRResultMatch:
		em.emitResultMatch(e, indent, false)
	case IROptionMatch:
		em.emitOptionMatch(e, indent, false)
	case IREnumMatch:
		em.emitEnumMatch(e, indent, false)
	case IRSumTypeMatch:
		em.emitSumTypeMatch(e, indent, false)
	case IRListMatch:
		em.emitListMatch(e, indent, false)
	case IRLiteralMatch:
		em.emitLiteralMatch(e, indent, false)
	default:
		em.writeln(fmt.Sprintf("%s%s", indent, em.emitExpr(stmt.Expr)))
	}
}

func (em *Emitter) emitDestructureStmt(stmt IRDestructureStmt, indent string) {
	em.tmpCounter++
	prefix := "__list"
	if stmt.Kind == IRDestructureTuple {
		prefix = "__tuple"
	}
	tmp := fmt.Sprintf("%s%d", prefix, em.tmpCounter)
	em.writeln(fmt.Sprintf("%s%s := %s", indent, tmp, em.emitExpr(stmt.Value)))
	for _, b := range stmt.Bindings {
		if b.Slice {
			em.writeln(fmt.Sprintf("%s%s := %s[%d:]", indent, b.GoName, tmp, b.Index))
		} else if stmt.Kind == IRDestructureTuple {
			field := "First"
			if b.Index == 1 {
				field = "Second"
			}
			em.writeln(fmt.Sprintf("%s%s := %s.%s", indent, b.GoName, tmp, field))
		} else {
			em.writeln(fmt.Sprintf("%s%s := %s[%d]", indent, b.GoName, tmp, b.Index))
		}
	}
}

// --- For Loops ---

func (em *Emitter) emitForRange(fr IRForRange, indent string) {
	em.writeln(fmt.Sprintf("%sfor %s := %s; %s < %s; %s++ {",
		indent, fr.Binding, em.emitExpr(fr.Start),
		fr.Binding, em.emitExpr(fr.End), fr.Binding))
	em.emitVoidBody(fr.Body, indent+"\t")
	em.writeln(fmt.Sprintf("%s}", indent))
}

func (em *Emitter) emitForEach(fe IRForEach, indent string) {
	em.writeln(fmt.Sprintf("%sfor _, %s := range %s {", indent, fe.Binding, em.emitExpr(fe.Iter)))
	em.emitVoidBody(fe.Body, indent+"\t")
	em.writeln(fmt.Sprintf("%s}", indent))
}

// --- Match Expressions ---

func (em *Emitter) emitResultMatch(m IRResultMatch, indent string, isReturn bool) {
	subject := em.emitExpr(m.Subject)
	em.writeln(fmt.Sprintf("%sif %s.IsOk {", indent, subject))
	if m.OkArm.Binding != nil {
		em.writeln(fmt.Sprintf("%s\t%s := %s%s", indent, m.OkArm.Binding.GoName, subject, m.OkArm.Binding.Source))
	}
	em.emitArmBody(m.OkArm.Body, indent+"\t", isReturn)
	em.writeln(fmt.Sprintf("%s} else {", indent))
	if m.ErrorArm.Binding != nil {
		em.writeln(fmt.Sprintf("%s\t%s := %s%s", indent, m.ErrorArm.Binding.GoName, subject, m.ErrorArm.Binding.Source))
	}
	em.emitArmBody(m.ErrorArm.Body, indent+"\t", isReturn)
	em.writeln(fmt.Sprintf("%s}", indent))
}

func (em *Emitter) emitOptionMatch(m IROptionMatch, indent string, isReturn bool) {
	subject := em.emitExpr(m.Subject)
	em.writeln(fmt.Sprintf("%sif %s.Valid {", indent, subject))
	if m.SomeArm.Binding != nil {
		em.writeln(fmt.Sprintf("%s\t%s := %s%s", indent, m.SomeArm.Binding.GoName, subject, m.SomeArm.Binding.Source))
	}
	em.emitArmBody(m.SomeArm.Body, indent+"\t", isReturn)
	em.writeln(fmt.Sprintf("%s} else {", indent))
	em.emitArmBody(m.NoneArm, indent+"\t", isReturn)
	em.writeln(fmt.Sprintf("%s}", indent))
}

func (em *Emitter) emitEnumMatch(m IREnumMatch, indent string, isReturn bool) {
	subject := em.emitExpr(m.Subject)
	em.writeln(fmt.Sprintf("%sswitch %s {", indent, subject))
	for _, arm := range m.Arms {
		em.writeln(fmt.Sprintf("%scase %s:", indent, arm.GoValue))
		em.emitArmBody(arm.Body, indent+"\t", isReturn)
	}
	if m.Wildcard != nil {
		em.writeln(fmt.Sprintf("%sdefault:", indent))
		em.emitArmBody(*m.Wildcard, indent+"\t", isReturn)
	} else if isReturn {
		em.writeln(fmt.Sprintf("%sdefault:", indent))
		em.writeln(fmt.Sprintf("%s\tpanic(\"unreachable\")", indent))
	}
	em.writeln(fmt.Sprintf("%s}", indent))
}

func (em *Emitter) emitSumTypeMatch(m IRSumTypeMatch, indent string, isReturn bool) {
	subject := em.emitExpr(m.Subject)
	em.writeln(fmt.Sprintf("%sswitch v := %s.(type) {", indent, subject))
	for _, arm := range m.Arms {
		em.writeln(fmt.Sprintf("%scase %s:", indent, arm.GoType))
		for _, b := range arm.Bindings {
			em.writeln(fmt.Sprintf("%s\t%s := v%s", indent, b.GoName, b.Source))
		}
		em.emitArmBody(arm.Body, indent+"\t", isReturn)
	}
	if m.Wildcard != nil {
		em.writeln(fmt.Sprintf("%sdefault:", indent))
		if m.Wildcard.Binding != nil {
			em.writeln(fmt.Sprintf("%s\t%s := v", indent, m.Wildcard.Binding.GoName))
		}
		em.emitArmBody(m.Wildcard.Body, indent+"\t", isReturn)
	}
	em.writeln(fmt.Sprintf("%s}", indent))
	if isReturn && m.Wildcard == nil {
		em.writeln(fmt.Sprintf("%spanic(\"unreachable\")", indent))
	}
}

func (em *Emitter) emitListMatch(m IRListMatch, indent string, isReturn bool) {
	subject := em.emitExpr(m.Subject)
	first := true
	for _, arm := range m.Arms {
		switch arm.Kind {
		case IRListEmpty:
			keyword := "if"
			if !first {
				keyword = "} else if"
			}
			em.writeln(fmt.Sprintf("%s%s len(%s) == 0 {", indent, keyword, subject))
		case IRListExact:
			keyword := "if"
			if !first {
				keyword = "} else if"
			}
			em.writeln(fmt.Sprintf("%s%s len(%s) == %d {", indent, keyword, subject, arm.MinLen))
			for _, b := range arm.Elements {
				em.writeln(fmt.Sprintf("%s\t%s := %s%s", indent, b.GoName, subject, b.Source))
			}
		case IRListCons:
			keyword := "if"
			if !first {
				keyword = "} else if"
			}
			em.writeln(fmt.Sprintf("%s%s len(%s) >= %d {", indent, keyword, subject, arm.MinLen))
			for _, b := range arm.Elements {
				em.writeln(fmt.Sprintf("%s\t%s := %s%s", indent, b.GoName, subject, b.Source))
			}
			if arm.Rest != nil {
				em.writeln(fmt.Sprintf("%s\t%s := %s%s", indent, arm.Rest.GoName, subject, arm.Rest.Source))
			}
		case IRListDefault:
			if first {
				em.writeln(fmt.Sprintf("%s{", indent))
			} else {
				em.writeln(fmt.Sprintf("%s} else {", indent))
			}
		}
		em.emitArmBody(arm.Body, indent+"\t", isReturn)
		first = false
	}
	em.writeln(fmt.Sprintf("%s}", indent))
	if isReturn {
		em.writeln(fmt.Sprintf("%spanic(\"unreachable\")", indent))
	}
}

func (em *Emitter) emitLiteralMatch(m IRLiteralMatch, indent string, isReturn bool) {
	subject := em.emitExpr(m.Subject)
	em.writeln(fmt.Sprintf("%sswitch %s {", indent, subject))
	for _, arm := range m.Arms {
		em.writeln(fmt.Sprintf("%scase %s:", indent, arm.Value))
		em.emitArmBody(arm.Body, indent+"\t", isReturn)
	}
	if m.Default != nil {
		em.writeln(fmt.Sprintf("%sdefault:", indent))
		em.emitArmBody(*m.Default, indent+"\t", isReturn)
	}
	em.writeln(fmt.Sprintf("%s}", indent))
}

// --- Builtins ---

func (em *Emitter) emitBuiltins(builtins []string) {
	set := make(map[string]bool, len(builtins))
	for _, b := range builtins {
		set[b] = true
	}

	if set["result"] {
		em.writeln("type Result_[T any, E any] struct {")
		em.writeln("\tValue T")
		em.writeln("\tErr   E")
		em.writeln("\tIsOk  bool")
		em.writeln("}")
		em.writeln("")
		em.writeln("func Ok_[T any, E any](v T) Result_[T, E] {")
		em.writeln("\treturn Result_[T, E]{Value: v, IsOk: true}")
		em.writeln("}")
		em.writeln("")
		em.writeln("func Err_[T any, E any](e E) Result_[T, E] {")
		em.writeln("\treturn Result_[T, E]{Err: e}")
		em.writeln("}")
		em.writeln("")
	}
	if set["option"] {
		em.writeln("type Option_[T any] struct {")
		em.writeln("\tValue T")
		em.writeln("\tValid bool")
		em.writeln("}")
		em.writeln("")
		em.writeln("func Some_[T any](v T) Option_[T] {")
		em.writeln("\treturn Option_[T]{Value: v, Valid: true}")
		em.writeln("}")
		em.writeln("")
		em.writeln("func None_[T any]() Option_[T] {")
		em.writeln("\treturn Option_[T]{}")
		em.writeln("}")
		em.writeln("")
	}
	if set["map"] {
		em.writeln("func Map_[T any, U any](list []T, f func(T) U) []U {")
		em.writeln("\tresult := make([]U, len(list))")
		em.writeln("\tfor i, v := range list {")
		em.writeln("\t\tresult[i] = f(v)")
		em.writeln("\t}")
		em.writeln("\treturn result")
		em.writeln("}")
		em.writeln("")
	}
	if set["filter"] {
		em.writeln("func Filter_[T any](list []T, f func(T) bool) []T {")
		em.writeln("\tvar result []T")
		em.writeln("\tfor _, v := range list {")
		em.writeln("\t\tif f(v) {")
		em.writeln("\t\t\tresult = append(result, v)")
		em.writeln("\t\t}")
		em.writeln("\t}")
		em.writeln("\treturn result")
		em.writeln("}")
		em.writeln("")
	}
	if set["fold"] {
		em.writeln("func Fold_[T any, U any](list []T, init U, f func(U, T) U) U {")
		em.writeln("\tacc := init")
		em.writeln("\tfor _, v := range list {")
		em.writeln("\t\tacc = f(acc, v)")
		em.writeln("\t}")
		em.writeln("\treturn acc")
		em.writeln("}")
		em.writeln("")
	}
}

// --- Type Rendering ---

func (em *Emitter) irTypeStr(t IRType) string {
	if t == nil {
		return ""
	}
	switch tt := t.(type) {
	case IRNamedType:
		if len(tt.Params) > 0 {
			params := make([]string, len(tt.Params))
			for i, p := range tt.Params {
				params[i] = em.irTypeStr(p)
			}
			return tt.GoName + "[" + strings.Join(params, ", ") + "]"
		}
		return tt.GoName
	case IRPointerType:
		return "*" + em.irTypeStr(tt.Inner)
	case IRTupleType:
		if len(tt.Elements) == 2 {
			return fmt.Sprintf("struct{ First %s; Second %s }", em.irTypeStr(tt.Elements[0]), em.irTypeStr(tt.Elements[1]))
		}
		return "interface{}"
	case IRListType:
		return "[]" + em.irTypeStr(tt.Elem)
	case IRResultType:
		return fmt.Sprintf("Result_[%s, %s]", em.irTypeStr(tt.Ok), em.irTypeStr(tt.Err))
	case IROptionType:
		return fmt.Sprintf("Option_[%s]", em.irTypeStr(tt.Inner))
	case IRInterfaceType:
		return "interface{}"
	default:
		return "interface{}"
	}
}

func (em *Emitter) goTypeParams(params []string) string {
	if len(params) == 0 {
		return ""
	}
	parts := make([]string, len(params))
	for i, p := range params {
		parts[i] = p + " any"
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// inferGoTypeFromIR infers a Go type string from an IR expression (for tuple literals).
func (em *Emitter) inferGoTypeFromIR(e IRExpr) string {
	if e == nil {
		return "interface{}"
	}
	t := e.irType()
	if t == nil {
		return "interface{}"
	}
	s := em.irTypeStr(t)
	if s == "interface{}" {
		// For named types that have a GoName, use that
		if nt, ok := t.(IRNamedType); ok && nt.GoName != "" && nt.GoName != "interface{}" {
			return nt.GoName
		}
	}
	return s
}

// --- Helpers ---

func (em *Emitter) write(s string) {
	em.buf.WriteString(s)
}

func (em *Emitter) writeln(s string) {
	em.buf.WriteString(s)
	em.buf.WriteString("\n")
}

// lowerFirst lowercases the first character of a string.
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// isIRResultType checks if an IR type is a Result type.
func isIRResultType(t IRType) bool {
	if t == nil {
		return false
	}
	_, ok := t.(IRResultType)
	return ok
}

// irResultTypeArgs extracts "[T, E]" from an IRResultType.
func irResultTypeArgs(t IRType) string {
	rt, ok := t.(IRResultType)
	if !ok {
		return ""
	}
	em := &Emitter{}
	return "[" + em.irTypeStr(rt.Ok) + ", " + em.irTypeStr(rt.Err) + "]"
}
