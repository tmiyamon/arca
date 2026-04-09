package main

import (
	"fmt"
	"strings"
)

// Emitter converts an IRProgram into a Go source string.
// All complex logic (name resolution, constructor resolution, match classification)
// was handled in lower.go. The Emitter is simple and mechanical.
type Emitter struct {
	w          *GoWriter
	tmpCounter int
}

// Emit is the main entry point. It produces a complete Go source file.
func (em *Emitter) Emit(prog IRProgram) string {
	// Generate body first so we know what's needed
	bodyWriter := NewGoWriter()
	em.w = bodyWriter

	for _, td := range prog.Types {
		em.emitTypeDecl(td)
		em.w.Line("")
	}
	for _, fd := range prog.Funcs {
		em.emitFuncDecl(fd)
		em.w.Line("")
	}
	em.emitBuiltins(prog.Builtins)
	bodyStr := em.w.String()

	em.w = NewGoWriter()

	w := em.w
	pkg := prog.Package
	if pkg == "" {
		pkg = "main"
	}
	w.Line("package %s", pkg)
	w.Line("")

	em.emitImports(prog.Imports)

	w.Raw(bodyStr)

	return w.String()
}

func (em *Emitter) emitImports(imports []IRImport) {
	if len(imports) == 0 {
		return
	}
	w := em.w
	w.Line("import (")
	w.Indent()
	for _, imp := range imports {
		if imp.SideEffect {
			w.Line("_ %q", imp.Path)
		} else {
			w.Line("%q", imp.Path)
		}
	}
	w.Dedent()
	w.Line(")")
	w.Line("")
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
	w := em.w
	w.TypeAlias(d.GoName, "int")
	w.Line("")
	w.Const(func() {
		for i, v := range d.Variants {
			if i == 0 {
				w.Line("%s%s %s = iota", d.GoName, v, d.GoName)
			} else {
				w.Line("%s%s", d.GoName, v)
			}
		}
	})
	w.Line("")
	w.Method(fmt.Sprintf("v %s", d.GoName), "String", "", "string", func() {
		w.Switch("v", func() {
			for _, v := range d.Variants {
				w.Case(fmt.Sprintf("%s%s", d.GoName, v), func() {
					w.Return(fmt.Sprintf("%q", v))
				})
			}
			w.Default(func() {
				w.Return(fmt.Sprintf("\"Unknown%s\"", d.GoName))
			})
		})
	})
}

func (em *Emitter) emitStructDecl(d IRStructDecl) {
	w := em.w
	typeParams := em.goTypeParams(d.TypeParams)
	w.Struct(d.GoName+typeParams, func() {
		for _, f := range d.Fields {
			w.Field(f.GoName, em.irTypeStr(f.Type), f.Tag)
		}
	})

	if d.Validator != nil {
		em.emitStructValidator(d)
		em.emitValidateMethod(d)
	}
}

func (em *Emitter) emitStructValidator(d IRStructDecl) {
	w := em.w
	params := make([]string, len(d.Fields))
	for i, f := range d.Fields {
		params[i] = fmt.Sprintf("%s %s", lowerFirst(f.GoName), em.irTypeStr(f.Type))
	}
	w.Line("")
	w.Func("New"+d.GoName, strings.Join(params, ", "), fmt.Sprintf("(%s, error)", d.GoName), func() {
		em.emitValidator(d.Validator)

		fields := make([]string, len(d.Fields))
		for i, f := range d.Fields {
			fields[i] = fmt.Sprintf("%s: %s", f.GoName, lowerFirst(f.GoName))
		}
		w.Return(fmt.Sprintf("%s{%s}, nil", d.GoName, strings.Join(fields, ", ")))
	})
}

func (em *Emitter) emitValidateMethod(d IRStructDecl) {
	w := em.w
	fields := make([]string, len(d.Fields))
	for i, f := range d.Fields {
		fields[i] = "v." + f.GoName
	}
	w.Line("")
	w.Method(fmt.Sprintf("v %s", d.GoName), "ArcaValidate", "", "error", func() {
		w.Assign("_, err", fmt.Sprintf("New%s(%s)", d.GoName, strings.Join(fields, ", ")))
		w.Return("err")
	})
}

func (em *Emitter) emitSumTypeDecl(d IRSumTypeDecl) {
	w := em.w
	tp := em.goTypeParams(d.TypeParams)
	w.Interface(d.GoName+tp, func() {
		w.Line("is%s()", d.GoName)
		for _, m := range d.InterfaceMethods {
			params := make([]string, len(m.Params))
			for i, p := range m.Params {
				params[i] = fmt.Sprintf("%s %s", p.GoName, em.irTypeStr(p.Type))
			}
			retStr := ""
			if m.ReturnType != nil {
				retStr = " " + em.irTypeStr(m.ReturnType)
			}
			w.Line("%s(%s)%s", m.Name, strings.Join(params, ", "), retStr)
		}
	})
	w.Line("")
	for _, v := range d.Variants {
		if len(v.Fields) == 0 {
			w.Line("type %s%s struct{}", v.GoName, tp)
		} else {
			w.Struct(v.GoName+tp, func() {
				for _, f := range v.Fields {
					w.Field(f.GoName, em.irTypeStr(f.Type), "")
				}
			})
		}
		w.Line("func (%s) is%s() {}", v.GoName, d.GoName)
		w.Line("")
	}
}

func (em *Emitter) emitTypeAliasDecl(d IRTypeAliasDecl) {
	w := em.w
	w.TypeAlias(d.GoName, d.GoBase)

	if d.Validator == nil {
		return
	}

	zeroVal := typeZeroValue(d.GoName, d.GoBase)
	w.Line("")
	w.Func("New"+d.GoName, fmt.Sprintf("v %s", d.GoBase), fmt.Sprintf("(%s, error)", d.GoName), func() {
		em.emitValidatorAlias(d.Validator, d.GoName, zeroVal, d.GoBase)
		w.Return(fmt.Sprintf("%s(v), nil", d.GoName))
	})

	w.Line("")
	w.Method(fmt.Sprintf("v %s", d.GoName), "ArcaValidate", "", "error", func() {
		w.Assign("_, err", fmt.Sprintf("New%s(%s(v))", d.GoName, d.GoBase))
		w.Return("err")
	})
}

func (em *Emitter) emitValidator(v *IRValidator) {
	w := em.w
	for _, check := range v.Checks {
		switch check.Kind {
		case "min":
			w.If(fmt.Sprintf("%s < %s", check.Field, check.Value), func() {
				w.Return(fmt.Sprintf("%s, fmt.Errorf(\"%s: must be >= %s\")", check.ZeroVal, check.TypeName, check.Value))
			})
		case "max":
			w.If(fmt.Sprintf("%s > %s", check.Field, check.Value), func() {
				w.Return(fmt.Sprintf("%s, fmt.Errorf(\"%s: must be <= %s\")", check.ZeroVal, check.TypeName, check.Value))
			})
		case "min_length":
			w.If(fmt.Sprintf("len(%s) < %s", check.Field, check.Value), func() {
				w.Return(fmt.Sprintf("%s, fmt.Errorf(\"%s: min length %s\")", check.ZeroVal, check.TypeName, check.Value))
			})
		case "max_length":
			w.If(fmt.Sprintf("len(%s) > %s", check.Field, check.Value), func() {
				w.Return(fmt.Sprintf("%s, fmt.Errorf(\"%s: max length %s\")", check.ZeroVal, check.TypeName, check.Value))
			})
		case "pattern":
			w.If(fmt.Sprintf("!regexp.MustCompile(%s).MatchString(%s)", check.Value, check.Field), func() {
				w.Return(fmt.Sprintf("%s, fmt.Errorf(\"%s: must match pattern\")", check.ZeroVal, check.TypeName))
			})
		case "validate":
			w.If(fmt.Sprintf("!%s(%s)", check.Value, check.Field), func() {
				w.Return(fmt.Sprintf("%s, fmt.Errorf(\"%s: validation failed\")", check.ZeroVal, check.TypeName))
			})
		}
	}
}

func (em *Emitter) emitValidatorAlias(v *IRValidator, typeName, zeroVal, goBase string) {
	w := em.w
	for _, check := range v.Checks {
		field := check.Field // "v" for aliases
		switch check.Kind {
		case "min":
			w.If(fmt.Sprintf("%s < %s", field, check.Value), func() {
				w.Return(fmt.Sprintf("%s, fmt.Errorf(\"must be >= %s\")", zeroVal, check.Value))
			})
		case "max":
			w.If(fmt.Sprintf("%s > %s", field, check.Value), func() {
				w.Return(fmt.Sprintf("%s, fmt.Errorf(\"must be <= %s\")", zeroVal, check.Value))
			})
		case "min_length":
			w.If(fmt.Sprintf("len(%s) < %s", field, check.Value), func() {
				w.Return(fmt.Sprintf("%s, fmt.Errorf(\"min length %s\")", zeroVal, check.Value))
			})
		case "max_length":
			w.If(fmt.Sprintf("len(%s) > %s", field, check.Value), func() {
				w.Return(fmt.Sprintf("%s, fmt.Errorf(\"max length %s\")", zeroVal, check.Value))
			})
		case "pattern":
			w.If(fmt.Sprintf("!regexp.MustCompile(%s).MatchString(string(%s))", check.Value, field), func() {
				w.Return(fmt.Sprintf("%s, fmt.Errorf(\"must match pattern\")", zeroVal))
			})
		case "validate":
			w.If(fmt.Sprintf("!%s(%s)", check.Value, field), func() {
				w.Return(fmt.Sprintf("%s, fmt.Errorf(\"validation failed\")", zeroVal))
			})
		}
	}
}

// --- Function Declarations ---

func (em *Emitter) emitFuncDecl(fd IRFuncDecl) {
	w := em.w
	params := make([]string, len(fd.Params))
	for i, p := range fd.Params {
		params[i] = fmt.Sprintf("%s %s", p.GoName, em.irTypeStr(p.Type))
	}

	retType := ""
	if fd.ReturnType != nil {
		retType = em.irTypeStr(fd.ReturnType)
	}

	body := func() {
		if fd.ReturnType != nil {
			em.emitReturnExpr(fd.Body)
		} else {
			em.emitVoidBody(fd.Body)
		}
	}

	if fd.Receiver != nil {
		w.Method(fmt.Sprintf("%s %s", fd.Receiver.GoName, fd.Receiver.Type),
			fd.GoName, strings.Join(params, ", "), retType, body)
	} else {
		w.Func(fd.GoName, strings.Join(params, ", "), retType, body)
	}
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
		if expr.Multiline && !strings.Contains(expr.Value, "`") {
			return "`" + expr.Value + "`"
		}
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
		if expr.Multiline && !strings.Contains(expr.Format, "`") {
			return fmt.Sprintf("fmt.Sprintf(`%s`, %s)", expr.Format, strings.Join(args, ", "))
		}
		return fmt.Sprintf("fmt.Sprintf(%q, %s)", expr.Format, strings.Join(args, ", "))
	case IRFnCall:
		args := make([]string, len(expr.Args))
		for i, a := range expr.Args {
			args[i] = em.emitExpr(a)
		}
		return fmt.Sprintf("%s%s(%s)", expr.Func, expr.TypeArgs, strings.Join(args, ", "))
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
		valStr := em.emitExpr(expr.Value)
		// Wrap string values in fmt.Errorf to produce error type
		if _, ok := expr.Value.(IRStringLit); ok {
			valStr = fmt.Sprintf("fmt.Errorf(%s)", valStr)
		} else if _, ok := expr.Value.(IRStringInterp); ok {
			valStr = fmt.Sprintf("fmt.Errorf(\"%%v\", %s)", valStr)
		}
		return fmt.Sprintf("Err_%s(%s)", expr.TypeArgs, valStr)
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
	if cc.GoMultiReturn {
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

func (em *Emitter) emitReturnExpr(e IRExpr) {
	if e == nil {
		return
	}
	w := em.w
	switch expr := e.(type) {
	case IRBlock:
		for _, stmt := range expr.Stmts {
			em.emitStmt(stmt)
		}
		if expr.Expr != nil {
			em.emitReturnExpr(expr.Expr)
		}
	case IRMatch:
		em.emitMatch(expr, true)
	default:
		w.Return(em.emitExpr(e))
	}
}

func (em *Emitter) emitVoidBody(e IRExpr) {
	if e == nil {
		return
	}
	w := em.w
	switch expr := e.(type) {
	case IRVoidExpr:
		// nothing to emit
	case IRBlock:
		for _, stmt := range expr.Stmts {
			em.emitStmt(stmt)
		}
		if expr.Expr != nil {
			em.emitVoidBody(expr.Expr)
		}
	case IRMatch:
		em.emitMatch(expr, false)
	case IRForRange:
		em.emitForRange(expr)
	case IRForEach:
		em.emitForEach(expr)
	default:
		w.Stmt(em.emitExpr(e))
	}
}

func (em *Emitter) emitArmBody(body IRExpr, isReturn bool) {
	if isReturn {
		em.emitReturnExpr(body)
	} else {
		em.emitVoidBody(body)
	}
}

// --- Statements ---

func (em *Emitter) emitStmt(s IRStmt) {
	w := em.w
	switch stmt := s.(type) {
	case IRLetStmt:
		em.emitLetStmt(stmt)
	case IRTryLetStmt:
		em.emitTryLetStmt(stmt)
	case IRExprStmt:
		em.emitExprStmt(stmt)
	case IRDeferStmt:
		w.Defer(em.emitExpr(stmt.Expr))
	case IRAssertStmt:
		w.If(fmt.Sprintf("!(%s)", em.emitExpr(stmt.Expr)), func() {
			w.Panic(fmt.Sprintf("%q", "assertion failed: "+stmt.ExprStr))
		})
	case IRDestructureStmt:
		em.emitDestructureStmt(stmt)
	}
}

func (em *Emitter) emitLetStmt(stmt IRLetStmt) {
	w := em.w
	if stmt.GoName == "_" {
		w.Stmt(fmt.Sprintf("_ = %s", em.emitExpr(stmt.Value)))
		return
	}
	if isGoMultiReturn(stmt.Value) {
		em.emitGoMultiReturnLet(stmt.GoName, stmt.Value)
		return
	}
	if stmt.Type != nil {
		if ll, ok := stmt.Value.(IRListLit); ok && len(ll.Elements) == 0 && ll.Spread == nil {
			w.Var(stmt.GoName, em.irTypeStr(stmt.Type))
			return
		}
		w.VarAssign(stmt.GoName, em.irTypeStr(stmt.Type), em.emitExpr(stmt.Value))
		return
	}
	w.Assign(stmt.GoName, em.emitExpr(stmt.Value))
}


// emitGoMultiReturnLet emits a let statement for a Go call that returns multiple values.
// The call's IRType determines the wrapping: IRResultType → Result, IROptionType → Option.
func (em *Emitter) emitGoMultiReturnLet(goName string, callExpr IRExpr) {
	w := em.w
	em.tmpCounter++
	callStr := em.emitExpr(callExpr)
	irType := callExpr.irType()

	switch rt := irType.(type) {
	case IRResultType:
		tmpErr := fmt.Sprintf("__cerr%d", em.tmpCounter)
		okType := em.irTypeStr(rt.Ok)
		isErrorOnly := isUnitType(rt.Ok)
		if isErrorOnly {
			w.Assign(tmpErr, callStr)
			w.Var(goName, fmt.Sprintf("Result_[%s, error]", okType))
			w.IfElse(fmt.Sprintf("%s != nil", tmpErr), func() {
				w.Stmt(fmt.Sprintf("%s = Err_[%s, error](%s)", goName, okType, tmpErr))
			}, func() {
				w.Stmt(fmt.Sprintf("%s = Ok_[%s, error](%s{})", goName, okType, okType))
			})
		} else {
			tmpVal := fmt.Sprintf("__cval%d", em.tmpCounter)
			w.AssignMulti(fmt.Sprintf("%s, %s", tmpVal, tmpErr), callStr)
			w.Var(goName, fmt.Sprintf("Result_[%s, error]", okType))
			w.IfElse(fmt.Sprintf("%s != nil", tmpErr), func() {
				w.Stmt(fmt.Sprintf("%s = Err_[%s, error](%s)", goName, okType, tmpErr))
			}, func() {
				w.Stmt(fmt.Sprintf("%s = Ok_[%s, error](%s)", goName, okType, tmpVal))
			})
		}
	case IROptionType:
		tmpVal := fmt.Sprintf("__oval%d", em.tmpCounter)
		tmpOk := fmt.Sprintf("__ook%d", em.tmpCounter)
		innerType := em.irTypeStr(rt.Inner)
		w.AssignMulti(fmt.Sprintf("%s, %s", tmpVal, tmpOk), callStr)
		w.Var(goName, fmt.Sprintf("Option_[%s]", innerType))
		w.If(tmpOk, func() {
			w.Stmt(fmt.Sprintf("%s = Some_[%s](%s)", goName, innerType, tmpVal))
		})
	default:
		w.Assign(goName, callStr)
	}
}

// isUnitType checks if an IRType is the Unit type (struct{}).
func isUnitType(t IRType) bool {
	if named, ok := t.(IRNamedType); ok {
		return named.GoName == "struct{}"
	}
	return false
}

func (em *Emitter) emitTryLetStmt(stmt IRTryLetStmt) {
	w := em.w
	em.tmpCounter++

	// Arca Result (single value): unwrap via .IsOk / .Value / .Err
	if !isGoMultiReturn(stmt.CallExpr) {
		tmpResult := fmt.Sprintf("__try_result%d", em.tmpCounter)
		w.Assign(tmpResult, em.emitExpr(stmt.CallExpr))
		w.If(fmt.Sprintf("!%s.IsOk", tmpResult), func() {
			if isIRResultType(stmt.ReturnType) {
				typeArgs := irResultTypeArgs(stmt.ReturnType)
				w.Return(fmt.Sprintf("Err_%s(%s.Err)", typeArgs, tmpResult))
			} else {
				w.Panic(fmt.Sprintf("%s.Err", tmpResult))
			}
		})
		if stmt.GoName != "_" {
			w.Assign(stmt.GoName, fmt.Sprintf("%s.Value", tmpResult))
		}
		return
	}

	// Go FFI multi-return: unwrap via val, err := f()
	tmpErr := fmt.Sprintf("__try_err%d", em.tmpCounter)

	errorOnly := false
	if rt, ok := stmt.CallExpr.irType().(IRResultType); ok {
		errorOnly = isUnitType(rt.Ok)
	}

	if errorOnly {
		w.Assign(tmpErr, em.emitExpr(stmt.CallExpr))
	} else {
		tmpVal := "_"
		if stmt.GoName != "_" {
			tmpVal = fmt.Sprintf("__try_val%d", em.tmpCounter)
		}
		w.AssignMulti(fmt.Sprintf("%s, %s", tmpVal, tmpErr), em.emitExpr(stmt.CallExpr))
		defer func() {
			if stmt.GoName != "_" {
				w.Assign(stmt.GoName, tmpVal)
			}
		}()
	}

	w.If(fmt.Sprintf("%s != nil", tmpErr), func() {
		if isIRResultType(stmt.ReturnType) {
			typeArgs := irResultTypeArgs(stmt.ReturnType)
			w.Return(fmt.Sprintf("Err_%s(%s)", typeArgs, tmpErr))
		} else {
			w.Panic(tmpErr)
		}
	})
}

func (em *Emitter) emitExprStmt(stmt IRExprStmt) {
	w := em.w
	switch e := stmt.Expr.(type) {
	case IRForRange:
		em.emitForRange(e)
	case IRForEach:
		em.emitForEach(e)
	case IRMatch:
		em.emitMatch(e, false)
	default:
		w.Stmt(em.emitExpr(stmt.Expr))
	}
}

func (em *Emitter) emitDestructureStmt(stmt IRDestructureStmt) {
	w := em.w
	em.tmpCounter++
	prefix := "__list"
	if stmt.Kind == IRDestructureTuple {
		prefix = "__tuple"
	}
	tmp := fmt.Sprintf("%s%d", prefix, em.tmpCounter)
	w.Assign(tmp, em.emitExpr(stmt.Value))
	for _, b := range stmt.Bindings {
		if b.Slice {
			w.Assign(b.GoName, fmt.Sprintf("%s[%d:]", tmp, b.Index))
		} else if stmt.Kind == IRDestructureTuple {
			field := "First"
			if b.Index == 1 {
				field = "Second"
			}
			w.Assign(b.GoName, fmt.Sprintf("%s.%s", tmp, field))
		} else {
			w.Assign(b.GoName, fmt.Sprintf("%s[%d]", tmp, b.Index))
		}
	}
}

// --- For Loops ---

func (em *Emitter) emitForRange(fr IRForRange) {
	w := em.w
	w.For(fmt.Sprintf("%s := %s; %s < %s; %s++",
		fr.Binding, em.emitExpr(fr.Start),
		fr.Binding, em.emitExpr(fr.End), fr.Binding), func() {
		em.emitVoidBody(fr.Body)
	})
}

func (em *Emitter) emitForEach(fe IRForEach) {
	w := em.w
	w.For(fmt.Sprintf("_, %s := range %s", fe.Binding, em.emitExpr(fe.Iter)), func() {
		em.emitVoidBody(fe.Body)
	})
}

// --- Match ---

func (em *Emitter) emitMatch(m IRMatch, isReturn bool) {
	if len(m.Arms) == 0 {
		return
	}
	switch m.Arms[0].Pattern.(type) {
	case IRResultOkPattern, IRResultErrorPattern:
		em.emitMatchResult(m, isReturn)
	case IROptionSomePattern, IROptionNonePattern:
		em.emitMatchOption(m, isReturn)
	case IREnumPattern:
		em.emitMatchEnum(m, isReturn)
	case IRSumTypePattern, IRSumTypeWildcardPattern:
		em.emitMatchSumType(m, isReturn)
	case IRListEmptyPattern, IRListExactPattern, IRListConsPattern, IRListDefaultPattern:
		em.emitMatchList(m, isReturn)
	case IRLiteralPattern, IRLiteralDefaultPattern:
		em.emitMatchLiteral(m, isReturn)
	}
}

func (em *Emitter) emitMatchResult(m IRMatch, isReturn bool) {
	w := em.w
	subject := em.emitExpr(m.Subject)
	var okArm, errorArm *IRMatchArm
	for i := range m.Arms {
		switch m.Arms[i].Pattern.(type) {
		case IRResultOkPattern:
			okArm = &m.Arms[i]
		case IRResultErrorPattern:
			errorArm = &m.Arms[i]
		}
	}
	okVoid := okArm != nil && isVoidBody(okArm.Body)
	errorVoid := errorArm != nil && isVoidBody(errorArm.Body)

	if okVoid && errorVoid {
		return
	}
	if okVoid {
		w.If(fmt.Sprintf("!%s.IsOk", subject), func() {
			if p := errorArm.Pattern.(IRResultErrorPattern); p.Binding != nil {
				w.Assign(p.Binding.GoName, fmt.Sprintf("%s%s", subject, p.Binding.Source))
			}
			em.emitArmBody(errorArm.Body, isReturn)
		})
		return
	}
	if errorVoid {
		w.If(fmt.Sprintf("%s.IsOk", subject), func() {
			if p := okArm.Pattern.(IRResultOkPattern); p.Binding != nil {
				w.Assign(p.Binding.GoName, fmt.Sprintf("%s%s", subject, p.Binding.Source))
			}
			em.emitArmBody(okArm.Body, isReturn)
		})
		return
	}
	w.IfElse(fmt.Sprintf("%s.IsOk", subject), func() {
		if okArm != nil {
			if p := okArm.Pattern.(IRResultOkPattern); p.Binding != nil {
				w.Assign(p.Binding.GoName, fmt.Sprintf("%s%s", subject, p.Binding.Source))
			}
			em.emitArmBody(okArm.Body, isReturn)
		}
	}, func() {
		if errorArm != nil {
			if p := errorArm.Pattern.(IRResultErrorPattern); p.Binding != nil {
				w.Assign(p.Binding.GoName, fmt.Sprintf("%s%s", subject, p.Binding.Source))
			}
			em.emitArmBody(errorArm.Body, isReturn)
		}
	})
}

func (em *Emitter) emitMatchOption(m IRMatch, isReturn bool) {
	w := em.w
	subject := em.emitExpr(m.Subject)
	var someArm, noneArm *IRMatchArm
	for i := range m.Arms {
		switch m.Arms[i].Pattern.(type) {
		case IROptionSomePattern:
			someArm = &m.Arms[i]
		case IROptionNonePattern:
			noneArm = &m.Arms[i]
		}
	}
	someVoid := someArm != nil && isVoidBody(someArm.Body)
	noneVoid := noneArm != nil && isVoidBody(noneArm.Body)

	if someVoid && noneVoid {
		return
	}
	if someVoid {
		w.If(fmt.Sprintf("!%s.Valid", subject), func() {
			em.emitArmBody(noneArm.Body, isReturn)
		})
		return
	}
	if noneVoid {
		w.If(fmt.Sprintf("%s.Valid", subject), func() {
			if p := someArm.Pattern.(IROptionSomePattern); p.Binding != nil {
				w.Assign(p.Binding.GoName, fmt.Sprintf("%s%s", subject, p.Binding.Source))
			}
			em.emitArmBody(someArm.Body, isReturn)
		})
		return
	}
	w.IfElse(fmt.Sprintf("%s.Valid", subject), func() {
		if someArm != nil {
			if p := someArm.Pattern.(IROptionSomePattern); p.Binding != nil {
				w.Assign(p.Binding.GoName, fmt.Sprintf("%s%s", subject, p.Binding.Source))
			}
			em.emitArmBody(someArm.Body, isReturn)
		}
	}, func() {
		if noneArm != nil {
			em.emitArmBody(noneArm.Body, isReturn)
		}
	})
}

func (em *Emitter) emitMatchEnum(m IRMatch, isReturn bool) {
	w := em.w
	subject := em.emitExpr(m.Subject)
	w.Switch(subject, func() {
		for _, arm := range m.Arms {
			switch p := arm.Pattern.(type) {
			case IREnumPattern:
				w.Case(p.GoValue, func() {
					em.emitArmBody(arm.Body, isReturn)
				})
			case IRWildcardPattern:
				w.Default(func() {
					em.emitArmBody(arm.Body, isReturn)
				})
			}
		}
		if isReturn && !em.hasWildcard(m) {
			w.Default(func() {
				w.Panic("\"unreachable\"")
			})
		}
	})
}

func (em *Emitter) emitMatchSumType(m IRMatch, isReturn bool) {
	w := em.w
	subject := em.emitExpr(m.Subject)
	w.SwitchType("v", subject, func() {
		for _, arm := range m.Arms {
			switch p := arm.Pattern.(type) {
			case IRSumTypePattern:
				w.Case(p.GoType, func() {
					for _, b := range p.Bindings {
						w.Assign(b.GoName, fmt.Sprintf("v%s", b.Source))
					}
					em.emitArmBody(arm.Body, isReturn)
				})
			case IRSumTypeWildcardPattern:
				w.Default(func() {
					if p.Binding != nil {
						w.Assign(p.Binding.GoName, "v")
					}
					em.emitArmBody(arm.Body, isReturn)
				})
			}
		}
	})
	if isReturn && !em.hasWildcard(m) {
		w.Panic("\"unreachable\"")
	}
}

func (em *Emitter) emitMatchList(m IRMatch, isReturn bool) {
	w := em.w
	subject := em.emitExpr(m.Subject)
	first := true
	for _, arm := range m.Arms {
		switch p := arm.Pattern.(type) {
		case IRListEmptyPattern:
			if first {
				w.Line("if len(%s) == 0 {", subject)
			} else {
				w.Line("} else if len(%s) == 0 {", subject)
			}
		case IRListExactPattern:
			if first {
				w.Line("if len(%s) == %d {", subject, p.MinLen)
			} else {
				w.Line("} else if len(%s) == %d {", subject, p.MinLen)
			}
			w.Indent()
			for _, b := range p.Elements {
				w.Assign(b.GoName, fmt.Sprintf("%s%s", subject, b.Source))
			}
			w.Dedent()
		case IRListConsPattern:
			if first {
				w.Line("if len(%s) >= %d {", subject, p.MinLen)
			} else {
				w.Line("} else if len(%s) >= %d {", subject, p.MinLen)
			}
			w.Indent()
			for _, b := range p.Elements {
				w.Assign(b.GoName, fmt.Sprintf("%s%s", subject, b.Source))
			}
			if p.Rest != nil {
				w.Assign(p.Rest.GoName, fmt.Sprintf("%s%s", subject, p.Rest.Source))
			}
			w.Dedent()
		case IRListDefaultPattern:
			if first {
				w.Line("{")
			} else {
				w.Line("} else {")
			}
		}
		w.Indent()
		em.emitArmBody(arm.Body, isReturn)
		w.Dedent()
		first = false
	}
	w.Line("}")
	if isReturn {
		w.Panic("\"unreachable\"")
	}
}

func (em *Emitter) emitMatchLiteral(m IRMatch, isReturn bool) {
	w := em.w
	subject := em.emitExpr(m.Subject)
	w.Switch(subject, func() {
		for _, arm := range m.Arms {
			switch p := arm.Pattern.(type) {
			case IRLiteralPattern:
				w.Case(p.Value, func() {
					em.emitArmBody(arm.Body, isReturn)
				})
			case IRLiteralDefaultPattern:
				w.Default(func() {
					em.emitArmBody(arm.Body, isReturn)
				})
			}
		}
	})
}

func (em *Emitter) hasWildcard(m IRMatch) bool {
	for _, arm := range m.Arms {
		switch arm.Pattern.(type) {
		case IRWildcardPattern, IRSumTypeWildcardPattern:
			return true
		}
	}
	return false
}

func isVoidBody(expr IRExpr) bool {
	if _, ok := expr.(IRVoidExpr); ok {
		return true
	}
	if block, ok := expr.(IRBlock); ok && block.Expr != nil && len(block.Stmts) == 0 {
		if _, ok := block.Expr.(IRVoidExpr); ok {
			return true
		}
	}
	return false
}

// --- Builtins ---

func (em *Emitter) emitBuiltins(builtins []string) {
	w := em.w
	set := make(map[string]bool, len(builtins))
	for _, b := range builtins {
		set[b] = true
	}

	if set["result"] {
		w.Struct("Result_[T any, E any]", func() {
			w.Field("Value", "T", "")
			w.Field("Err", "  E", "")
			w.Field("IsOk", " bool", "")
		})
		w.Line("")
		w.Func("Ok_[T any, E any]", "v T", "Result_[T, E]", func() {
			w.Return("Result_[T, E]{Value: v, IsOk: true}")
		})
		w.Line("")
		w.Func("Err_[T any, E any]", "e E", "Result_[T, E]", func() {
			w.Return("Result_[T, E]{Err: e}")
		})
		w.Line("")
	}
	if set["option"] {
		w.Struct("Option_[T any]", func() {
			w.Field("Value", "T", "")
			w.Field("Valid", "bool", "")
		})
		w.Line("")
		w.Func("Some_[T any]", "v T", "Option_[T]", func() {
			w.Return("Option_[T]{Value: v, Valid: true}")
		})
		w.Line("")
		w.Func("None_[T any]", "", "Option_[T]", func() {
			w.Return("Option_[T]{}")
		})
		w.Line("")
	}
	if set["map"] {
		w.Func("Map_[T any, U any]", "list []T, f func(T) U", "[]U", func() {
			w.Assign("result", "make([]U, len(list))")
			w.For("i, v := range list", func() {
				w.Stmt("result[i] = f(v)")
			})
			w.Return("result")
		})
		w.Line("")
	}
	if set["filter"] {
		w.Func("Filter_[T any]", "list []T, f func(T) bool", "[]T", func() {
			w.Var("result", "[]T")
			w.For("_, v := range list", func() {
				w.If("f(v)", func() {
					w.Stmt("result = append(result, v)")
				})
			})
			w.Return("result")
		})
		w.Line("")
	}
	if set["fold"] {
		w.Func("Fold_[T any, U any]", "list []T, init U, f func(U, T) U", "U", func() {
			w.Assign("acc", "init")
			w.For("_, v := range list", func() {
				w.Stmt("acc = f(acc, v)")
			})
			w.Return("acc")
		})
		w.Line("")
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
	case IRTypeVar:
		return "interface{}" // unresolved type variable
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
	em := &Emitter{w: NewGoWriter()}
	return "[" + em.irTypeStr(rt.Ok) + ", " + em.irTypeStr(rt.Err) + "]"
}
