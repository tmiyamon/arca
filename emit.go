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
	// usesGoError is set by emit sites that wrap a raw Go error value into
	// __goError (currently: match Err bindings whose Arca Err type is the
	// Error trait). When true, emitBuiltins outputs the helper definition.
	usesGoError bool
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
		em.emitFn(fd)
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
	case IRTraitDecl:
		em.emitTraitDecl(d)
	}
}

func (em *Emitter) emitTraitDecl(d IRTraitDecl) {
	w := em.w
	w.Interface(d.GoName, func() {
		for _, m := range d.Methods {
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

func (em *Emitter) emitFn(fd IRFn) {
	w := em.w
	// Params are already expanded by expandFuncParams in lower.go.
	params := make([]string, len(fd.Params))
	for i, p := range fd.Params {
		params[i] = fmt.Sprintf("%s %s", p.GoName, em.irTypeStr(p.Type))
	}

	retType := ""
	if fd.Ret != nil {
		retType = em.irReturnTypeStr(fd.Ret)
	}

	body := func() {
		em.emitFnBody(fd.Body)
	}

	// Special-case: `fun main() -> Result[_, _]`. Go's `main` takes no args
	// and returns nothing, but Arca lets users write `fun main() -> Result[...]`
	// so `?` works at the top level. Wrap the Result-valued body in an IIFE,
	// exit non-zero on Err. Mirrors Rust's `fn main() -> Result<(), Error>`.
	if fd.GoName == "main" && fd.Receiver == nil {
		if rt, ok := fd.Ret.(IRResultType); ok {
			em.emitResultMainWrapper(fd, rt)
			return
		}
	}

	if fd.Receiver != nil {
		w.Method(fmt.Sprintf("%s %s", fd.Receiver.GoName, fd.Receiver.Type),
			fd.GoName, strings.Join(params, ", "), retType, body)
	} else {
		w.Func(fd.GoName, strings.Join(params, ", "), retType, body)
	}
}

// emitResultMainWrapper emits a Go `main()` that runs the Arca body as
// an inner IIFE returning the Result-shaped multi-return, then exits
// with the error printed to stderr when the inner returned Err. Ok
// returns normally.
func (em *Emitter) emitResultMainWrapper(fd IRFn, rt IRResultType) {
	w := em.w
	// Imports needed by the wrapper (fmt.Fprintln, os.Stderr, os.Exit) are
	// registered on the Lowerer at the point the main-Result shape is
	// detected — see lowerFnDecl.
	innerRet := em.irReturnTypeStr(rt)
	w.Func("main", "", "", func() {
		// Error-only shape (Ok is Unit): inner returns bare `error`.
		if isUnitType(rt.Ok) {
			w.Stmt(fmt.Sprintf("if err := func() %s {", innerRet))
			em.indentAndEmitBody(fd.Body)
			w.Stmt("}(); err != nil {")
			w.Indent()
			w.Stmt(`fmt.Fprintln(os.Stderr, err)`)
			w.Stmt(`os.Exit(1)`)
			w.Dedent()
			w.Stmt("}")
			return
		}
		// `(T, error)` shape: discard Ok value on success.
		w.Stmt(fmt.Sprintf("if _, err := func() %s {", innerRet))
		em.indentAndEmitBody(fd.Body)
		w.Stmt("}(); err != nil {")
		w.Indent()
		w.Stmt(`fmt.Fprintln(os.Stderr, err)`)
		w.Stmt(`os.Exit(1)`)
		w.Dedent()
		w.Stmt("}")
	})
}

// emitFnBody walks a stage2-lowered function body. After stage2Lower
// every fn body is an IRBlock whose Stmts list contains the flat
// sequence of statements (including the tail-position GoReturn for
// return-typed functions).
func (em *Emitter) emitFnBody(body IRExpr) {
	if blk, ok := body.(IRBlock); ok {
		for _, s := range blk.Stmts {
			em.emitStmt(s)
		}
		return
	}
	if body != nil {
		em.w.Stmt(em.emitExpr(body))
	}
}

func (em *Emitter) indentAndEmitBody(e IRExpr) {
	em.w.Indent()
	em.emitFnBody(e)
	em.w.Dedent()
}

// --- Expressions ---

// emitArgs lowers a slice of IRExprs to a comma-separated argument string.
func (em *Emitter) emitArgs(args []IRExpr) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = em.emitExpr(a)
	}
	return strings.Join(parts, ", ")
}

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
		args := em.emitArgs(expr.Args)
		if expr.Multiline && !strings.Contains(expr.Format, "`") {
			return fmt.Sprintf("fmt.Sprintf(`%s`, %s)", expr.Format, args)
		}
		return fmt.Sprintf("fmt.Sprintf(%q, %s)", expr.Format, args)
	case IRFnCall:
		return fmt.Sprintf("%s%s(%s)", em.emitExpr(expr.Fn), expr.TypeArgs, em.emitArgs(expr.Args))
	case IRMethodCall:
		return fmt.Sprintf("%s.%s(%s)", em.emitExpr(expr.Receiver), expr.Method, em.emitArgs(expr.Args))
	case IRFieldAccess:
		return fmt.Sprintf("%s.%s", em.emitExpr(expr.Expr), expr.Field)
	case IRIndexAccess:
		return fmt.Sprintf("%s[%s]", em.emitExpr(expr.Expr), em.emitExpr(expr.Index))
	case IRConstructorCall:
		return em.emitConstructorCall(expr)
	case IROkCall:
		// Value-position Ok: emit the value (error side is implicit nil).
		// stage2 wraps tail-position Ok in GoReturn before reaching emit.
		return em.emitExpr(expr.Value)
	case IRErrorCall:
		return em.emitExpr(expr.Value)
	case IRFn:
		return em.emitLambda(expr)
	case IRBinaryExpr:
		return fmt.Sprintf("%s %s %s", em.emitExpr(expr.Left), expr.Op, em.emitExpr(expr.Right))
	case IRListLit:
		return em.emitListLit(expr)
	case IRMapLit:
		return em.emitMapLit(expr)
	case IRTupleLit:
		return em.emitTupleLit(expr)
	case IRRefExpr:
		return "&" + em.emitExpr(expr.Expr)
	// --- Stage 2 expressions ---
	case GoErrorWrap:
		em.usesGoError = true
		return fmt.Sprintf("__goError{inner: %s}", em.emitExpr(expr.Inner))
	case GoDeref:
		return "*" + em.emitExpr(expr.Inner)
	case GoPtrOf:
		return fmt.Sprintf("__ptrOf(%s)", em.emitExpr(expr.Inner))
	case GoOptFromCall:
		return fmt.Sprintf("__optFrom(%s)", em.emitExpr(expr.Call))
	case GoTypedNil:
		return fmt.Sprintf("(*%s)(nil)", expr.GoType)
	case GoIIFE:
		return em.emitGoIIFE(expr)
	default:
		return "/* unsupported expr */"
	}
}

// emitGoIIFE renders an IIFE expression: `func() RetType { Body }()`.
func (em *Emitter) emitGoIIFE(g GoIIFE) string {
	sub := &Emitter{w: NewGoWriter()}
	for _, stmt := range g.Body.Stmts {
		sub.emitStmt(stmt)
	}
	body := strings.TrimRight(sub.w.String(), "\n")
	return fmt.Sprintf("func() %s {\n%s\n}()", irReturnTypeStr(g.RetType), body)
}

func (em *Emitter) emitConstructorCall(cc IRConstructorCall) string {
	if cc.GoMultiReturn {
		// Constrained constructor: NewType(args...)
		fieldValues := make([]IRExpr, len(cc.Fields))
		for i, f := range cc.Fields {
			fieldValues[i] = f.Value
		}
		return fmt.Sprintf("%s(%s)", cc.GoName, em.emitArgs(fieldValues))
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

func (em *Emitter) emitLambda(l IRFn) string {
	// Params are already expanded by the expandResultOption post-pass.
	params := make([]string, len(l.Params))
	for i, p := range l.Params {
		if p.Type != nil {
			params[i] = fmt.Sprintf("%s %s", p.GoName, em.irTypeStr(p.Type))
		} else {
			params[i] = p.GoName
		}
	}
	retType := ""
	if l.Ret != nil {
		retType = " " + em.irReturnTypeStr(l.Ret)
	}
	// Body has been stage2-lowered (walkLambdasInExpr) into an IRBlock
	// of Stage 2 stmts; emit just walks the list. emitFnBody handles
	// both return-typed and void lambdas uniformly.
	w := em.w
	bodyWriter := NewGoWriter()
	em.w = bodyWriter
	em.emitFnBody(l.Body)
	bodyStr := em.w.String()
	em.w = w
	if l.Ret != nil {
		return fmt.Sprintf("func(%s)%s {\n%s}", strings.Join(params, ", "), retType, bodyStr)
	}
	return fmt.Sprintf("func(%s) {\n%s}", strings.Join(params, ", "), bodyStr)
}

func (em *Emitter) emitListLit(l IRListLit) string {
	if len(l.Elements) == 0 && l.Spread == nil {
		return fmt.Sprintf("[]%s{}", l.ElemType)
	}
	if l.Spread != nil && len(l.Elements) == 0 {
		return em.emitExpr(l.Spread)
	}
	elems := em.emitArgs(l.Elements)
	if l.Spread != nil {
		return fmt.Sprintf("append([]%s{%s}, %s...)", l.ElemType, elems, em.emitExpr(l.Spread))
	}
	return fmt.Sprintf("[]%s{%s}", l.ElemType, elems)
}

func (em *Emitter) emitMapLit(m IRMapLit) string {
	if len(m.Entries) == 0 {
		return fmt.Sprintf("map[%s]%s{}", m.KeyType, m.ValueType)
	}
	parts := make([]string, len(m.Entries))
	for i, e := range m.Entries {
		parts[i] = em.emitExpr(e.Key) + ": " + em.emitExpr(e.Value)
	}
	return fmt.Sprintf("map[%s]%s{%s}", m.KeyType, m.ValueType, strings.Join(parts, ", "))
}

func (em *Emitter) emitTupleLit(t IRTupleLit) string {
	if len(t.Elements) == 2 {
		t1 := em.inferGoTypeFromIR(t.Elements[0])
		t2 := em.inferGoTypeFromIR(t.Elements[1])
		return fmt.Sprintf("struct{ First %s; Second %s }{%s, %s}",
			t1, t2, em.emitExpr(t.Elements[0]), em.emitExpr(t.Elements[1]))
	}
	return fmt.Sprintf("/* tuple(%s) */", em.emitArgs(t.Elements))
}

// --- Body Emission Modes ---
//
// bodyMode and its leaf callbacks were retired in slice S4b. Stage 2
// lowering now wraps every leaf statement in the right form (GoReturn /
// GoReassign / GoExprStmt) so emit walks IRBlock.Stmts directly.

// --- Statements ---

func (em *Emitter) emitStmt(s IRStmt) {
	w := em.w
	switch stmt := s.(type) {
	case IRLetStmt:
		em.emitLetStmt(stmt)
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
	// --- Stage 2 nodes ---
	case GoIfElse:
		em.emitGoIfElse(stmt)
	case GoMultiAssign:
		em.emitGoMultiAssign(stmt)
	case GoVarDecl:
		em.emitGoVarDecl(stmt)
	case GoReassign:
		em.emitGoReassign(stmt)
	case GoReturn:
		em.emitGoReturn(stmt)
	case GoExprStmt:
		w.Stmt(em.emitExpr(stmt.Expr))
	case GoSwitch:
		em.emitGoSwitch(stmt)
	case GoTypeSwitch:
		em.emitGoTypeSwitch(stmt)
	case GoUnreachable:
		w.Unreachable()
	}
}

// --- Stage 2 emit handlers ---

func (em *Emitter) emitGoIfElse(g GoIfElse) {
	if g.Init != nil {
		em.emitStmt(g.Init)
	}
	branches, def := em.collectIfChain(g)
	if def == nil && len(branches) == 1 {
		em.w.If(branches[0].Cond, branches[0].Body)
		return
	}
	var defBody func()
	if def != nil {
		stmts := def.Stmts
		defBody = func() {
			for _, s := range stmts {
				em.emitStmt(s)
			}
		}
	}
	em.w.IfChain(branches, defBody)
}

// collectIfChain walks a GoIfElse-only chain (each Else.Stmts being a
// single GoIfElse with no Init) and gathers the branches plus the
// terminal else body. Only follows the chain when each link's ChainElse
// is set; otherwise stops so IRIfExpr-converted GoIfElses preserve their
// nested-block else form.
func (em *Emitter) collectIfChain(g GoIfElse) ([]GoIfBranch, *GoBlock) {
	var branches []GoIfBranch
	cur := g
	for {
		condStr := em.emitExpr(cur.Cond)
		thenStmts := cur.Then.Stmts
		branches = append(branches, GoIfBranch{
			Cond: condStr,
			Body: func() {
				for _, s := range thenStmts {
					em.emitStmt(s)
				}
			},
		})
		if cur.ChainElse && len(cur.Else.Stmts) == 1 {
			if next, ok := cur.Else.Stmts[0].(GoIfElse); ok && next.Init == nil {
				cur = next
				continue
			}
		}
		if len(cur.Else.Stmts) > 0 {
			elseCopy := cur.Else
			return branches, &elseCopy
		}
		return branches, nil
	}
}

func (em *Emitter) emitGoMultiAssign(g GoMultiAssign) {
	em.w.AssignMulti(strings.Join(g.Names, ", "), em.emitExpr(g.Value))
}

func (em *Emitter) emitGoVarDecl(g GoVarDecl) {
	w := em.w
	if g.Init == nil {
		w.Var(g.Name, em.irTypeStr(g.Type))
		return
	}
	if g.Type == nil {
		w.Assign(g.Name, em.emitExpr(g.Init))
		return
	}
	w.VarAssign(g.Name, em.irTypeStr(g.Type), em.emitExpr(g.Init))
}

func (em *Emitter) emitGoReassign(g GoReassign) {
	parts := make([]string, len(g.Values))
	for i, v := range g.Values {
		parts[i] = em.emitExpr(v)
	}
	em.w.Set(strings.Join(g.Targets, ", "), strings.Join(parts, ", "))
}

func (em *Emitter) emitGoReturn(g GoReturn) {
	parts := make([]string, len(g.Values))
	for i, v := range g.Values {
		parts[i] = em.emitExpr(v)
	}
	em.w.Return(strings.Join(parts, ", "))
}

func (em *Emitter) emitGoSwitch(g GoSwitch) {
	w := em.w
	w.Switch(em.emitExpr(g.Subject), func() {
		for _, c := range g.Cases {
			parts := make([]string, len(c.Vals))
			for i, v := range c.Vals {
				parts[i] = em.emitExpr(v)
			}
			w.Case(strings.Join(parts, ", "), func() {
				for _, s := range c.Body.Stmts {
					em.emitStmt(s)
				}
			})
		}
		if g.Default != nil {
			w.Default(func() {
				for _, s := range g.Default.Stmts {
					em.emitStmt(s)
				}
			})
		}
	})
}

func (em *Emitter) emitGoTypeSwitch(g GoTypeSwitch) {
	w := em.w
	w.SwitchType(g.BindVar, em.emitExpr(g.Subject), func() {
		for _, c := range g.Cases {
			w.Case(em.irTypeStr(c.Type), func() {
				for _, s := range c.Body.Stmts {
					em.emitStmt(s)
				}
			})
		}
		if g.Default != nil {
			w.Default(func() {
				for _, s := range g.Default.Stmts {
					em.emitStmt(s)
				}
			})
		}
	})
}


func (em *Emitter) emitLetStmt(stmt IRLetStmt) {
	w := em.w
	if stmt.GoName == "_" {
		w.Stmt(fmt.Sprintf("_ = %s", em.emitExpr(stmt.Value)))
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

// isControlFlowValue reports whether the given IR expression is a
// control-flow construct that must be emitted as Go statements rather than
// a single expression. Used by stage2 to decide whether an IRLetStmt's
// value needs predeclare-then-walk treatment.
func isControlFlowValue(e IRExpr) bool {
	switch e.(type) {
	case IRIfExpr, IRMatch:
		return true
	}
	return false
}

// isUnitType checks if an IRType is the Unit type (struct{}).
func isUnitType(t IRType) bool {
	if named, ok := t.(IRNamedType); ok {
		return named.GoName == "struct{}"
	}
	return false
}

// isMultiReturnType checks if an IR type will be emitted as Go multi-return.
// Used by expandResultOption to detect when a let-bound value needs split
// names. Both Result and Option are listed historically; under the
// pointer-backed Option scheme only Result actually multi-returns, but
// expandLetToMultiLet handles only Result and the predicate stays
// permissive.
func isMultiReturnType(t IRType) bool {
	switch t.(type) {
	case IRResultType, IROptionType:
		return true
	}
	return false
}


func (em *Emitter) emitExprStmt(stmt IRExprStmt) {
	w := em.w
	switch e := stmt.Expr.(type) {
	case IRForRange:
		em.emitForRange(e)
	case IRForEach:
		em.emitForEach(e)
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
		em.emitForBodyStmts(fr.Body)
	})
}

func (em *Emitter) emitForEach(fe IRForEach) {
	w := em.w
	w.For(fmt.Sprintf("_, %s := range %s", fe.Binding, em.emitExpr(fe.Iter)), func() {
		em.emitForBodyStmts(fe.Body)
	})
}

// emitForBodyStmts walks a stage2-lowered for-loop body. Body is an
// IRBlock whose Stmts list is a flat sequence of stage2 statements (the
// stage2 walker sets it up via foldBody in s2Void mode).
func (em *Emitter) emitForBodyStmts(body IRExpr) {
	if blk, ok := body.(IRBlock); ok {
		for _, s := range blk.Stmts {
			em.emitStmt(s)
		}
	}
}

// isPointerBackedOption reports whether an Option's inner type is itself
// pointer-like (Ref or Ptr), so outer and inner share the same Go `*T`
// encoding. Stage 2 uses this for Option-match binding decisions
// (pass-through vs deref).
func isPointerBackedOption(t IRType) bool {
	opt, ok := t.(IROptionType)
	if !ok {
		return false
	}
	switch opt.Inner.(type) {
	case IRRefType, IRPointerType:
		return true
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

	if em.usesGoError {
		w.Line("type __goError struct{ inner error }")
		w.Line("")
		w.Method("e __goError", "Message", "", "string", func() {
			w.Return("e.inner.Error()")
		})
		w.Line("")
		w.Method("e __goError", "Error", "", "string", func() {
			w.Return("e.inner.Error()")
		})
		w.Line("")
		w.Method("e __goError", "Unwrap", "", "error", func() {
			w.Return("e.inner")
		})
		w.Line("")
	}

	// Result stays as native Go multi-return. Option is uniformly
	// pointer-backed: `*T` where nil = None. __ptrOf is the helper that
	// wraps a value into a heap pointer — Go doesn't allow `&10` on literals
	// and naive `&v` wouldn't cover non-addressable cases uniformly.
	_ = set["result"]
	if set["option"] {
		w.Func("__ptrOf[T any]", "v T", "*T", func() {
			w.Return("&v")
		})
		w.Line("")
		w.Func("__optFrom[T any]", "v T, ok bool", "*T", func() {
			w.If("ok", func() {
				w.Return("&v")
			})
			w.Return("nil")
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
	if set["take"] {
		w.Func("Take_[T any]", "list []T, n int", "[]T", func() {
			w.If("n > len(list)", func() {
				w.Stmt("n = len(list)")
			})
			w.Return("list[:n]")
		})
		w.Line("")
	}
	if set["takeWhile"] {
		w.Func("TakeWhile_[T any]", "list []T, f func(T) bool", "[]T", func() {
			w.Var("result", "[]T")
			w.For("_, v := range list", func() {
				w.If("!f(v)", func() {
					w.Return("result")
				})
				w.Stmt("result = append(result, v)")
			})
			w.Return("result")
		})
		w.Line("")
	}
}

// --- Type Rendering ---

func (em *Emitter) irTypeStr(t IRType) string { return irTypeStr(t) }

// irTypeStr renders an IRType as its Go-side string. Pure function of the
// type — no emitter / printer state used — so stage2 can call it to
// fully resolve type strings before emit runs.
func irTypeStr(t IRType) string {
	if t == nil {
		return ""
	}
	switch tt := t.(type) {
	case IRNamedType:
		if len(tt.Params) > 0 {
			params := make([]string, len(tt.Params))
			for i, p := range tt.Params {
				params[i] = irTypeStr(p)
			}
			return tt.GoName + "[" + strings.Join(params, ", ") + "]"
		}
		return tt.GoName
	case IRPointerType:
		return "*" + irTypeStr(tt.Inner)
	case IRRefType:
		return "*" + irTypeStr(tt.Inner)
	case IRTupleType:
		if len(tt.Elements) == 2 {
			return fmt.Sprintf("struct{ First %s; Second %s }", irTypeStr(tt.Elements[0]), irTypeStr(tt.Elements[1]))
		}
		return "interface{}"
	case IRListType:
		return "[]" + irTypeStr(tt.Elem)
	case IRMapType:
		return "map[" + irTypeStr(tt.Key) + "]" + irTypeStr(tt.Value)
	case IRResultType:
		// Returns and params are expanded by the post-pass. Struct fields
		// and other value positions fall through here.
		return fmt.Sprintf("(%s, %s)", irTypeStr(tt.Ok), irTypeStr(tt.Err))
	case IROptionType:
		// Option in struct field position → Go pointer (nil = None).
		return "*" + irTypeStr(tt.Inner)
	case IRInterfaceType:
		return "interface{}"
	case IRTraitType:
		// Error maps to Go's stdlib `error` interface for interop with FFI
		// returns. Method calls on Error-typed values are wrapped at the
		// call site.
		if tt.Name == "Error" {
			return "error"
		}
		return traitGoName(tt.Name)
	case IRFnType:
		params := make([]string, len(tt.Params))
		for i, p := range tt.Params {
			params[i] = irTypeStr(p)
		}
		if tt.Ret == nil {
			return "func(" + strings.Join(params, ", ") + ")"
		}
		return "func(" + strings.Join(params, ", ") + ") " + irTypeStr(tt.Ret)
	case IRTypeVar:
		return "interface{}" // unresolved type variable
	default:
		return "interface{}"
	}
}

func (em *Emitter) irReturnTypeStr(t IRType) string { return irReturnTypeStr(t) }

// irReturnTypeStr renders an IR type as a Go function return type. Result
// still uses multi-return (Go-idiomatic), Option is uniformly pointer-backed
// so it falls through to irTypeStr and emits as `*T`. Pure function so
// stage2 can use it before emit runs.
func irReturnTypeStr(t IRType) string {
	switch tt := t.(type) {
	case IRResultType:
		if isUnitType(tt.Ok) {
			return "error"
		}
		return fmt.Sprintf("(%s, error)", irTypeStr(tt.Ok))
	}
	return irTypeStr(t)
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
