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
	// Params are already expanded by expandFuncParams in lower.go.
	params := make([]string, len(fd.Params))
	for i, p := range fd.Params {
		params[i] = fmt.Sprintf("%s %s", p.GoName, em.irTypeStr(p.Type))
	}

	retType := ""
	if fd.ReturnType != nil {
		retType = em.irReturnTypeStr(fd.ReturnType)
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
		return fmt.Sprintf("%s%s(%s)", expr.Func, expr.TypeArgs, em.emitArgs(expr.Args))
	case IRMethodCall:
		return fmt.Sprintf("%s.%s(%s)", em.emitExpr(expr.Receiver), expr.Method, em.emitArgs(expr.Args))
	case IRFieldAccess:
		return fmt.Sprintf("%s.%s", em.emitExpr(expr.Expr), expr.Field)
	case IRIndexAccess:
		return fmt.Sprintf("%s[%s]", em.emitExpr(expr.Expr), em.emitExpr(expr.Index))
	case IRConstructorCall:
		return em.emitConstructorCall(expr)
	case IROkCall:
		// Value-position Ok: emit the value (error side is implicit nil)
		return em.emitExpr(expr.Value)
	case IRErrorCall:
		return em.emitExpr(expr.Value)
	case IRSomeCall:
		// Option in value position → pointer (Some(v) = &v)
		return "&" + em.emitExpr(expr.Value)
	case IRNoneExpr:
		return "nil"
	case IRLambda:
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
	case IRTryBlock:
		return em.emitTryBlockExpr(expr)
	default:
		return "/* unsupported expr */"
	}
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

func (em *Emitter) emitLambda(l IRLambda) string {
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
	if l.ReturnType != nil {
		retType = " " + em.irReturnTypeStr(l.ReturnType)
	}
	if l.ReturnType != nil {
		// Use emitReturnExpr for proper multi-return handling
		w := em.w
		bodyWriter := NewGoWriter()
		em.w = bodyWriter
		em.emitReturnExpr(l.Body)
		bodyStr := em.w.String()
		em.w = w
		return fmt.Sprintf("func(%s)%s {\n%s}", strings.Join(params, ", "), retType, bodyStr)
	}
	return fmt.Sprintf("func(%s) { %s }", strings.Join(params, ", "), em.emitExpr(l.Body))
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
// Control-flow constructs like `if` and `match` can appear in three contexts:
// (1) as a tail expression whose value is returned from the enclosing function,
// (2) as a statement in void context where the value is discarded, and
// (3) as a value-producing expression assigned to a variable.
//
// emitBody walks the common IRBlock / IRMatch / IRIfExpr / IRFor* structure
// once and defers leaf handling to a callback. The three callers
// (returnLeaf, voidLeaf, assignLeaf) differ only in what they emit when the
// traversal reaches a non-control-flow expression.

type emitLeaf func(e IRExpr)

// bodyMode describes how a control-flow construct's leaves should be emitted.
// - leaf: how to emit each non-control-flow expression at the bottom
// - valueCtx: true when the surrounding context needs every branch to yield a
//   value (return or assign). Drives whether a non-exhaustive match gets a
//   `panic("unreachable")` fallback to satisfy Go's definite-return analysis.
type bodyMode struct {
	leaf     emitLeaf
	valueCtx bool
}

func (em *Emitter) returnMode() bodyMode {
	return bodyMode{leaf: em.returnLeaf, valueCtx: true}
}

func (em *Emitter) voidMode() bodyMode {
	return bodyMode{leaf: em.voidLeaf, valueCtx: false}
}

func (em *Emitter) assignMode(goName string) bodyMode {
	return bodyMode{leaf: em.assignLeaf(goName), valueCtx: true}
}

// assignMultiMode assigns expanded Result/Option values to multiple split names.
// Used when a control-flow expression (match/if) appears as the value of a
// multi-return let binding.
func (em *Emitter) assignMultiMode(names []string) bodyMode {
	return bodyMode{leaf: em.assignMultiLeaf(names), valueCtx: true}
}

func (em *Emitter) emitBody(e IRExpr, mode bodyMode) {
	if e == nil {
		return
	}
	switch expr := e.(type) {
	case IRVoidExpr:
		// nothing to emit
	case IRBlock:
		for _, stmt := range expr.Stmts {
			em.emitStmt(stmt)
		}
		if expr.Expr != nil {
			em.emitBody(expr.Expr, mode)
		}
	case IRMatch:
		em.emitMatch(expr, mode)
	case IRIfExpr:
		em.emitIfExpr(expr, mode)
	case IRForRange:
		em.emitForRange(expr)
	case IRForEach:
		em.emitForEach(expr)
	default:
		mode.leaf(e)
	}
}

func (em *Emitter) returnLeaf(e IRExpr) {
	// Expanded Result/Option constructors: emit as multi-value return.
	if vals := expandedValues(e); len(vals) > 0 {
		parts := make([]string, len(vals))
		for i, v := range vals {
			parts[i] = em.emitExpr(v)
		}
		em.w.Return(strings.Join(parts, ", "))
		return
	}
	em.w.Return(em.emitExpr(e))
}

// expandedValues returns the pre-computed ExpandedValues from the
// expandResultOption post-pass, or nil if not expanded.
func expandedValues(e IRExpr) []IRExpr {
	switch expr := e.(type) {
	case IROkCall:
		return expr.ExpandedValues
	case IRErrorCall:
		return expr.ExpandedValues
	case IRSomeCall:
		return expr.ExpandedValues
	case IRNoneExpr:
		return expr.ExpandedValues
	}
	return nil
}

func (em *Emitter) voidLeaf(e IRExpr) {
	em.w.Stmt(em.emitExpr(e))
}

func (em *Emitter) assignLeaf(goName string) emitLeaf {
	return func(e IRExpr) {
		em.w.Set(goName, em.emitExpr(e))
	}
}

// assignMultiLeaf reads ExpandedValues from a leaf Result/Option constructor
// (Ok/Error/Some/None) and assigns each to its split name.
func (em *Emitter) assignMultiLeaf(names []string) emitLeaf {
	return func(e IRExpr) {
		vals := expandedValues(e)
		if len(vals) != len(names) {
			// Leaf isn't an expanded Result/Option constructor — fall back
			// so partial output is still visible rather than silently dropped.
			em.w.Set(strings.Join(names, ", "), em.emitExpr(e))
			return
		}
		parts := make([]string, len(vals))
		for i, v := range vals {
			parts[i] = em.emitExpr(v)
		}
		em.w.Set(strings.Join(names, ", "), strings.Join(parts, ", "))
	}
}

// declareSplitVars emits `var name T` for each split name, typed from the
// outer multi-return type (Result/Option).
func (em *Emitter) declareSplitVars(names []string, multiType IRType) {
	types := em.splitVarTypes(multiType, len(names))
	for i, name := range names {
		em.w.Var(name, types[i])
	}
}

// splitVarTypes returns the Go types for each split name given the outer
// multi-return type. For Result[T, E]: [T, E]. For Option[T]: [T, bool].
func (em *Emitter) splitVarTypes(t IRType, n int) []string {
	switch tt := t.(type) {
	case IRResultType:
		return []string{em.irTypeStr(tt.Ok), em.irTypeStr(tt.Err)}
	case IROptionType:
		return []string{em.irTypeStr(tt.Inner), "bool"}
	}
	// Fallback: all interface{} (shouldn't hit in practice)
	types := make([]string, n)
	for i := range types {
		types[i] = "interface{}"
	}
	return types
}

// Backwards-compatible wrappers so existing call sites stay short.
func (em *Emitter) emitReturnExpr(e IRExpr) { em.emitBody(e, em.returnMode()) }
func (em *Emitter) emitVoidBody(e IRExpr)   { em.emitBody(e, em.voidMode()) }

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
	// Multi-return calls with control-flow value: declare all split names,
	// then walk the body so each Ok/Error/Some/None leaf assigns to all
	// split names via expanded values. Covers `let x = match r { ... }`
	// where arms produce Result/Option values.
	if len(stmt.SplitNames) > 0 && isControlFlowValue(stmt.Value) {
		em.declareSplitVars(stmt.SplitNames, stmt.Value.irType())
		em.emitBody(stmt.Value, em.assignMultiMode(stmt.SplitNames))
		return
	}
	// Multi-return calls: SplitNames populated by expandResultOption post-pass.
	if len(stmt.SplitNames) > 0 {
		names := strings.Join(stmt.SplitNames, ", ")
		w.AssignMulti(names, em.emitExpr(stmt.Value))
		return
	}
	// Value-position control flow (`let x = if ... { a } else { b }`, same for
	// match): declare the var first, then walk the control-flow body so each
	// leaf expression assigns to the declared var.
	if isControlFlowValue(stmt.Value) {
		typeStr := em.irTypeStr(em.letStmtType(stmt))
		w.Var(stmt.GoName, typeStr)
		em.emitBody(stmt.Value, em.assignMode(stmt.GoName))
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
// a single expression.
func isControlFlowValue(e IRExpr) bool {
	switch e.(type) {
	case IRIfExpr, IRMatch:
		return true
	}
	return false
}


// letStmtType returns the declared or inferred Go type for a let binding.
// Prefers the explicit annotation when present; otherwise falls back to the
// value's own IR type so control-flow values get a concrete var declaration.
func (em *Emitter) letStmtType(stmt IRLetStmt) IRType {
	if stmt.Type != nil {
		return stmt.Type
	}
	if t := stmt.Value.irType(); t != nil {
		return t
	}
	return IRInterfaceType{}
}



// isMultiReturnType checks if an IR type will be emitted as Go multi-return.
func isMultiReturnType(t IRType) bool {
	switch t.(type) {
	case IRResultType, IROptionType:
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

func (em *Emitter) emitTryLetStmt(stmt IRTryLetStmt) {
	w := em.w

	// SplitNames, ErrorReturnValues, and ValueName are pre-computed by
	// the expandResultOption post-pass. Emit is mechanical.
	names := strings.Join(stmt.SplitNames, ", ")
	if isControlFlowValue(stmt.CallExpr) {
		// `let x = match ... { Ok(...) => Ok(...); Error(...) => Error(...) }?`
		// — the match produces a Result whose multi-return values must be
		// assigned across the split names. Declare split vars, then walk
		// the body leaves with a multi-assign mode.
		em.declareSplitVars(stmt.SplitNames, stmt.CallExpr.irType())
		em.emitBody(stmt.CallExpr, em.assignMultiMode(stmt.SplitNames))
	} else if len(stmt.SplitNames) == 1 {
		w.Assign(names, em.emitExpr(stmt.CallExpr))
	} else {
		w.AssignMulti(names, em.emitExpr(stmt.CallExpr))
	}

	errName := stmt.SplitNames[len(stmt.SplitNames)-1]
	w.If(fmt.Sprintf("%s != nil", errName), func() {
		parts := make([]string, len(stmt.ErrorReturnValues))
		for i, v := range stmt.ErrorReturnValues {
			parts[i] = em.emitExpr(v)
		}
		w.Return(strings.Join(parts, ", "))
	})

	// Nil check for pointer Option: (*T, error) where val==nil && err==nil
	if len(stmt.NilCheckReturnValues) > 0 && stmt.ValueName != "" {
		w.If(fmt.Sprintf("%s == nil", stmt.ValueName), func() {
			parts := make([]string, len(stmt.NilCheckReturnValues))
			for i, v := range stmt.NilCheckReturnValues {
				parts[i] = em.emitExpr(v)
			}
			w.Return(strings.Join(parts, ", "))
		})
	}

	if stmt.GoName != "_" && stmt.ValueName != "" {
		w.Assign(stmt.GoName, stmt.ValueName)
	}
}

// emitTryBlockExpr emits try { ... } as a Go IIFE: func() (T, error) { ... }()
func (em *Emitter) emitTryBlockExpr(rb IRTryBlock) string {
	sub := &Emitter{w: NewGoWriter()}
	for _, stmt := range rb.Stmts {
		sub.emitStmt(stmt)
	}
	if rb.Expr != nil {
		sub.emitReturnExpr(rb.Expr)
	}
	body := strings.TrimRight(sub.w.String(), "\n")
	okStr := em.irTypeStr(rb.OkType)
	return fmt.Sprintf("func() (%s, error) {\n%s\n}()", okStr, body)
}

func (em *Emitter) emitExprStmt(stmt IRExprStmt) {
	w := em.w
	switch e := stmt.Expr.(type) {
	case IRForRange:
		em.emitForRange(e)
	case IRForEach:
		em.emitForEach(e)
	case IRMatch:
		em.emitMatch(e, em.voidMode())
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

func (em *Emitter) emitIfExpr(e IRIfExpr, mode bodyMode) {
	w := em.w
	if e.Else != nil {
		w.IfElse(em.emitExpr(e.Cond), func() {
			em.emitBody(e.Then, mode)
		}, func() {
			em.emitBody(e.Else, mode)
		})
	} else {
		w.If(em.emitExpr(e.Cond), func() {
			em.emitBody(e.Then, mode)
		})
	}
}

func (em *Emitter) emitMatch(m IRMatch, mode bodyMode) {
	if len(m.Arms) == 0 {
		return
	}
	switch m.Arms[0].Pattern.(type) {
	case IRResultOkPattern, IRResultErrorPattern:
		em.emitMatchResult(m, mode)
	case IROptionSomePattern, IROptionNonePattern:
		em.emitMatchOption(m, mode)
	case IREnumPattern:
		em.emitMatchEnum(m, mode)
	case IRSumTypePattern, IRSumTypeWildcardPattern:
		em.emitMatchSumType(m, mode)
	case IRListEmptyPattern, IRListExactPattern, IRListConsPattern, IRListDefaultPattern:
		em.emitMatchList(m, mode)
	case IRLiteralPattern, IRLiteralDefaultPattern:
		em.emitMatchLiteral(m, mode)
	}
}

func (em *Emitter) emitMatchResult(m IRMatch, mode bodyMode) {
	w := em.w
	subject := em.emitExpr(m.Subject)

	// Resolve the error condition variable from the match arm bindings.
	// The post-pass (resolveMatchBindings) has already rewritten binding
	// Sources to actual Go variable names. For params (no post-pass),
	// the naming convention subject + "_err" is used as fallback.
	errVar := subject + "_err"
	if rt, ok := m.Subject.irType().(IRResultType); ok && isUnitType(rt.Ok) {
		errVar = subject // error-only: subject IS the error
	}
	// Check if the Error arm has a resolved Source — use it if available.
	for _, arm := range m.Arms {
		if p, ok := arm.Pattern.(IRResultErrorPattern); ok && p.Binding != nil {
			if p.Binding.Source != "" && p.Binding.Source != ".Err" {
				errVar = p.Binding.Source
				break
			}
		}
	}
	errCond := fmt.Sprintf("%s == nil", errVar)
	errCondNeg := fmt.Sprintf("%s != nil", errVar)

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
	// Helper: emit binding assignment from the resolved Source.
	emitBinding := func(p interface{ GetBinding() *IRBinding }) {
		if b := p.GetBinding(); b != nil && b.Source != "" {
			w.Assign(b.GoName, b.Source)
		}
	}

	if okVoid {
		w.If(errCondNeg, func() {
			emitBinding(errorArm.Pattern.(IRResultErrorPattern))
			em.emitBody(errorArm.Body, mode)
		})
		return
	}
	if errorVoid {
		w.If(errCond, func() {
			emitBinding(okArm.Pattern.(IRResultOkPattern))
			em.emitBody(okArm.Body, mode)
		})
		return
	}
	w.IfElse(errCond, func() {
		if okArm != nil {
			emitBinding(okArm.Pattern.(IRResultOkPattern))
			em.emitBody(okArm.Body, mode)
		}
	}, func() {
		if errorArm != nil {
			emitBinding(errorArm.Pattern.(IRResultErrorPattern))
			em.emitBody(errorArm.Body, mode)
		}
	})
}

func (em *Emitter) emitMatchOption(m IRMatch, mode bodyMode) {
	w := em.w
	subject := em.emitExpr(m.Subject)

	// Discriminator: `_ok` flag for value-backed Option (T, bool) style,
	// or `!= nil` for pointer-backed Option (Option[Ref[T]] from Go *T).
	// The subject's IR type tells us which emit form applies.
	okVar := subject + "_ok"
	cond := okVar
	negCond := "!" + okVar
	if isPointerBackedOption(m.Subject.irType()) {
		cond = subject + " != nil"
		negCond = subject + " == nil"
	}
	// The Some arm's binding Source points to the value var (resolved by post-pass).
	// The ok var is always subject + "_ok" (set by expandLetToMultiLet / param expansion).
	// No need to re-derive from convention — just use the fallback for params.

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
		w.If(negCond, func() {
			em.emitBody(noneArm.Body, mode)
		})
		return
	}
	emitSomeBinding := func() {
		if someArm == nil { return }
		p := someArm.Pattern.(IROptionSomePattern)
		if p.Binding != nil && p.Binding.Source != "" && p.Binding.Source != ".Value" {
			w.Assign(p.Binding.GoName, p.Binding.Source)
		} else if p.Binding != nil {
			w.Assign(p.Binding.GoName, subject) // fallback for params
		}
	}

	if noneVoid {
		w.If(cond, func() {
			emitSomeBinding()
			em.emitBody(someArm.Body, mode)
		})
		return
	}
	w.IfElse(cond, func() {
		if someArm != nil {
			emitSomeBinding()
			em.emitBody(someArm.Body, mode)
		}
	}, func() {
		if noneArm != nil {
			em.emitBody(noneArm.Body, mode)
		}
	})
}

// isPointerBackedOption reports whether an Option's inner type emits as a
// Go pointer, where nil naturally represents None. Applies to Option[Ref[T]]
// (user-written) and Option[*T] remnants (FFI internal).
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

func (em *Emitter) emitMatchEnum(m IRMatch, mode bodyMode) {
	w := em.w
	subject := em.emitExpr(m.Subject)
	w.Switch(subject, func() {
		for _, arm := range m.Arms {
			switch p := arm.Pattern.(type) {
			case IREnumPattern:
				w.Case(p.GoValue, func() {
					em.emitBody(arm.Body, mode)
				})
			case IRWildcardPattern:
				w.Default(func() {
					em.emitBody(arm.Body, mode)
				})
			}
		}
		if mode.valueCtx && !em.hasWildcard(m) {
			w.Default(func() {
				w.Unreachable()
			})
		}
	})
}

func (em *Emitter) emitMatchSumType(m IRMatch, mode bodyMode) {
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
					em.emitBody(arm.Body, mode)
				})
			case IRSumTypeWildcardPattern:
				w.Default(func() {
					if p.Binding != nil {
						w.Assign(p.Binding.GoName, "v")
					}
					em.emitBody(arm.Body, mode)
				})
			}
		}
	})
	if mode.valueCtx && !em.hasWildcard(m) {
		w.Unreachable()
	}
}

func (em *Emitter) emitMatchList(m IRMatch, mode bodyMode) {
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
		em.emitBody(arm.Body, mode)
		w.Dedent()
		first = false
	}
	w.Line("}")
	if mode.valueCtx {
		w.Unreachable()
	}
}

func (em *Emitter) emitMatchLiteral(m IRMatch, mode bodyMode) {
	w := em.w
	subject := em.emitExpr(m.Subject)
	w.Switch(subject, func() {
		for _, arm := range m.Arms {
			switch p := arm.Pattern.(type) {
			case IRLiteralPattern:
				w.Case(p.Value, func() {
					em.emitBody(arm.Body, mode)
				})
			case IRLiteralDefaultPattern:
				w.Default(func() {
					em.emitBody(arm.Body, mode)
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

	// Result_/Option_ synthetic types fully eliminated. Result/Option are
	// emitted as native Go multi-return at all positions: function returns,
	// parameters, try, match, and call arguments.
	_ = set["result"]
	_ = set["option"]
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
	case IRRefType:
		return "*" + em.irTypeStr(tt.Inner)
	case IRTupleType:
		if len(tt.Elements) == 2 {
			return fmt.Sprintf("struct{ First %s; Second %s }", em.irTypeStr(tt.Elements[0]), em.irTypeStr(tt.Elements[1]))
		}
		return "interface{}"
	case IRListType:
		return "[]" + em.irTypeStr(tt.Elem)
	case IRMapType:
		return "map[" + em.irTypeStr(tt.Key) + "]" + em.irTypeStr(tt.Value)
	case IRResultType:
		// Returns and params are expanded by the post-pass. Struct fields
		// and other value positions fall through here.
		return fmt.Sprintf("(%s, %s)", em.irTypeStr(tt.Ok), em.irTypeStr(tt.Err))
	case IROptionType:
		// Option in struct field position → Go pointer (nil = None).
		return "*" + em.irTypeStr(tt.Inner)
	case IRInterfaceType:
		return "interface{}"
	case IRTypeVar:
		return "interface{}" // unresolved type variable
	default:
		return "interface{}"
	}
}

// irReturnTypeStr renders an IR type as a Go function return type. This is
// the only position where Go multi-return syntax is valid. Result/Option
// become native multi-return: (T, error), (T, bool), or bare error.
func (em *Emitter) irReturnTypeStr(t IRType) string {
	switch tt := t.(type) {
	case IRResultType:
		if isUnitType(tt.Ok) {
			return "error"
		}
		return fmt.Sprintf("(%s, error)", em.irTypeStr(tt.Ok))
	case IROptionType:
		return fmt.Sprintf("(%s, bool)", em.irTypeStr(tt.Inner))
	}
	return em.irTypeStr(t)
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
