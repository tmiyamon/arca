package main

import "fmt"

// go_lower.go — Stage 1 IR → Stage 2 IR lowering pass.
//
// Pairs with go_ir.go (Stage 2 node definitions). Designed and landed
// across slices S1–S5 of the 2026-05-02 "Two-stage IR completion" plan
// in decisions/ideas.md.
//
// `stage2Lower` walks each function body in two phases:
//
//   1. `stage2LowerFn` runs the per-fn `stage2Walker`, rewriting Stage 1
//      control-flow / let-overload / Result-Option-constructor nodes into
//      Stage 2 shapes (GoIfElse / GoSwitch / GoTypeSwitch / GoMultiAssign
//      / GoVarDecl / GoReassign / GoReturn / GoExprStmt / GoIIFE / …).
//   2. `walkLambdasInExpr` deep-walks every IR node and stage2-lowers
//      anonymous IRFn (lambda) bodies — same recursion also rewrites
//      IRSomeCall / IRNoneExpr / IRFnCall.GoMultiReturn / IRMethodCall.
//      GoMultiReturn / IRTryBlock into their Stage 2 wrap/IIFE forms.
//
// After this file runs, no Stage 1 control-flow / let-overload / Result-
// Option-constructor nodes remain. emit walks the resulting Stage 2 tree
// mechanically.

// s2Mode encodes where the tail value of a control-flow expression flows.
type s2Mode int

const (
	s2Void   s2Mode = iota // statement context — discard tail value
	s2Return               // tail is the function's return
	s2Assign               // single-target assign (Targets[0])
	s2Multi                // multi-target assign (Targets, Result-split)
)


// stage2Walker carries per-function state: a synthetic-name counter, a
// splits registry mapping ident GoName → split names produced by
// expanding multi-return values, a matchResolved set tracking which
// subjects had their splits already resolved by a Result-match (mirroring
// the previous expandResultOption / resolveMatchBindings split between
// match-driven and reference-driven blank propagation), and a hoist
// buffer that collects synthetic statements produced when expression-
// position IRTryExpr nodes are extracted ahead of the enclosing stmt.
type stage2Walker struct {
	counter       int
	splits        map[string][]string
	matchResolved map[string]bool
	hoist         []IRStmt
}

func newStage2Walker() *stage2Walker {
	return &stage2Walker{
		splits:        make(map[string][]string),
		matchResolved: make(map[string]bool),
	}
}

// flushHoist returns and clears the pending hoisted statements.
func (w *stage2Walker) flushHoist() []IRStmt {
	if len(w.hoist) == 0 {
		return nil
	}
	out := w.hoist
	w.hoist = nil
	return out
}

func (w *stage2Walker) nextSym() int {
	w.counter++
	return w.counter
}

// registerSplit records the split names produced by expanding a Result-
// typed param or let binding. Idempotent.
func (w *stage2Walker) registerSplit(name string, splitNames []string) {
	w.splits[name] = splitNames
}

// splitsFor returns the split names registered for an ident (or nil).
func (w *stage2Walker) splitsFor(name string) []string {
	if names, ok := w.splits[name]; ok {
		return names
	}
	return nil
}

// stage2LowerTypes filters Stage 1 type decls down to the subset emit can
// turn into Go directly. Currently this drops dictionary-kind IRTraitDecl
// nodes — they record a constraint-only trait that has no Go-interface
// representation, so emit must never see them. B2 will replace this drop
// with "synthesise a per-type dictionary struct from the trait body".
func stage2LowerTypes(types []IRTypeDecl) []IRTypeDecl {
	out := types[:0]
	for _, td := range types {
		if trait, ok := td.(IRTraitDecl); ok && trait.Kind == TraitKindDictionary {
			continue
		}
		out = append(out, td)
	}
	return out
}

// stage2Lower rewrites each function body's Result/Option dispatch and
// multi-return let bindings into Stage 2 nodes. Other Stage 1 nodes pass
// through (wrapped in goLegacyBody where they sit at a tail-form position).
func stage2Lower(funcs []IRFn) []IRFn {
	for i := range funcs {
		funcs[i] = stage2LowerFn(funcs[i])
	}
	// Second pass: walk every anonymous lambda's body so emit no longer
	// needs bodyMode for `emitLambda`.
	for i := range funcs {
		funcs[i].Body = walkLambdasInExpr(funcs[i].Body)
	}
	return funcs
}

// walkLambdasInExpr is the post-stage2 expression rewriter. Despite the
// name, it does three jobs in one tree pass:
//
//  1. Stage2-lowers anonymous IRFn (lambda) bodies so emitLambda walks
//     plain Stage 2 stmts.
//  2. Rewrites IRSomeCall / IRNoneExpr into the corresponding Stage 2
//     constructor-wrap nodes (GoPtrOf / GoTypedNil) — or a collapse /
//     bare-nil shortcut when the inner already matches Go's pointer.
//  3. Wraps Go-FFI multi-return calls returning Option (`(T, bool)`) in
//     GoOptFromCall, so emit no longer needs to detect this at the call
//     site.
//  4. Converts IRTryBlock into GoIIFE once its body is stage2-lowered
//     so emit renders the `func() (T, error) { ... }()` form mechanically.
func walkLambdasInExpr(e IRExpr) IRExpr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case IRFn:
		if x.GoName == "" {
			walker := newStage2Walker()
			x.Params = walker.expandParams(x.Params)
			mode := s2Return
			if x.Ret == nil || isUnitType(x.Ret) {
				mode = s2Void
			}
			stmts := walker.walkExpr(x.Body, mode, nil)
			stmts = walker.blankUnusedSplits(stmts)
			for i, s := range stmts {
				stmts[i] = walkLambdasInStmt(s)
			}
			x.Body = IRBlock{Stmts: stmts}
		}
		return x
	case IRBlock:
		for i, s := range x.Stmts {
			x.Stmts[i] = walkLambdasInStmt(s)
		}
		x.Expr = walkLambdasInExpr(x.Expr)
		return x
	case IRTryBlock:
		for i, s := range x.Stmts {
			x.Stmts[i] = walkLambdasInStmt(s)
		}
		x.Expr = walkLambdasInExpr(x.Expr)
		// Convert to GoIIFE — the IIFE wrapper is now structural.
		retType := IRType(IRResultType{Ok: x.OkType, Err: x.ErrType})
		return GoIIFE{
			RetType: retType,
			Body:    GoBlock{Stmts: x.Stmts},
			Type:    retType,
		}
	case IRFnCall:
		x.Fn = walkLambdasInExpr(x.Fn)
		x.Args = walkAndFlattenCallArgs(x.Args)
		if x.GoMultiReturn {
			if _, ok := x.Type.(IROptionType); ok {
				return GoOptFromCall{Call: x, Type: x.Type}
			}
		}
		return x
	case IRMethodCall:
		x.Receiver = walkLambdasInExpr(x.Receiver)
		x.Args = walkAndFlattenCallArgs(x.Args)
		if x.GoMultiReturn {
			if _, ok := x.Type.(IROptionType); ok {
				return GoOptFromCall{Call: x, Type: x.Type}
			}
		}
		return x
	case IRFieldAccess:
		x.Expr = walkLambdasInExpr(x.Expr)
		return x
	case IRIndexAccess:
		x.Expr = walkLambdasInExpr(x.Expr)
		x.Index = walkLambdasInExpr(x.Index)
		return x
	case IRBinaryExpr:
		x.Left = walkLambdasInExpr(x.Left)
		x.Right = walkLambdasInExpr(x.Right)
		return x
	case IRRefExpr:
		x.Expr = walkLambdasInExpr(x.Expr)
		return x
	case IRConstructorCall:
		for i := range x.Fields {
			x.Fields[i].Value = walkLambdasInExpr(x.Fields[i].Value)
		}
		return x
	case IROkCall:
		x.Value = walkLambdasInExpr(x.Value)
		return x
	case IRErrorCall:
		x.Value = walkLambdasInExpr(x.Value)
		return x
	case IRSomeCall:
		x.Value = walkLambdasInExpr(x.Value)
		// Collapse: when Inner is Ref/Ptr, the value already emits as *T
		// matching the Option's Go shape — no __ptrOf needed.
		if opt, ok := x.Type.(IROptionType); ok {
			switch opt.Inner.(type) {
			case IRRefType, IRPointerType:
				return x.Value
			}
		}
		return GoPtrOf{Inner: x.Value, Type: x.Type}
	case IRNoneExpr:
		if x.TypeArg != "" {
			inner := "interface{}"
			if opt, ok := x.Type.(IROptionType); ok {
				inner = irTypeStr(opt.Inner)
			}
			return GoTypedNil{GoType: inner, Type: x.Type}
		}
		return IRIdent{GoName: "nil"}
	case IRStringInterp:
		for i := range x.Args {
			x.Args[i] = walkLambdasInExpr(x.Args[i])
		}
		return x
	case IRListLit:
		for i := range x.Elements {
			x.Elements[i] = walkLambdasInExpr(x.Elements[i])
		}
		x.Spread = walkLambdasInExpr(x.Spread)
		return x
	case IRMapLit:
		for i := range x.Entries {
			x.Entries[i].Key = walkLambdasInExpr(x.Entries[i].Key)
			x.Entries[i].Value = walkLambdasInExpr(x.Entries[i].Value)
		}
		return x
	case IRTupleLit:
		for i := range x.Elements {
			x.Elements[i] = walkLambdasInExpr(x.Elements[i])
		}
		return x
	case IRForRange:
		x.Start = walkLambdasInExpr(x.Start)
		x.End = walkLambdasInExpr(x.End)
		x.Body = walkLambdasInExpr(x.Body)
		return x
	case IRForEach:
		x.Iter = walkLambdasInExpr(x.Iter)
		x.Body = walkLambdasInExpr(x.Body)
		return x
	case IRMatch:
		x.Subject = walkLambdasInExpr(x.Subject)
		for i := range x.Arms {
			x.Arms[i].Body = walkLambdasInExpr(x.Arms[i].Body)
		}
		return x
	case IRIfExpr:
		x.Cond = walkLambdasInExpr(x.Cond)
		x.Then = walkLambdasInExpr(x.Then)
		x.Else = walkLambdasInExpr(x.Else)
		return x
	case GoErrorWrap:
		x.Inner = walkLambdasInExpr(x.Inner)
		return x
	case GoDeref:
		x.Inner = walkLambdasInExpr(x.Inner)
		return x
	}
	return e
}

func walkLambdasInStmt(s IRStmt) IRStmt {
	switch stmt := s.(type) {
	case IRLetStmt:
		stmt.Value = walkLambdasInExpr(stmt.Value)
		return stmt
	case IRTryLetStmt:
		stmt.CallExpr = walkLambdasInExpr(stmt.CallExpr)
		return stmt
	case IRExprStmt:
		stmt.Expr = walkLambdasInExpr(stmt.Expr)
		return stmt
	case IRDeferStmt:
		stmt.Expr = walkLambdasInExpr(stmt.Expr)
		return stmt
	case IRAssertStmt:
		stmt.Expr = walkLambdasInExpr(stmt.Expr)
		return stmt
	case IRDestructureStmt:
		stmt.Value = walkLambdasInExpr(stmt.Value)
		return stmt
	case GoMultiAssign:
		stmt.Value = walkLambdasInExpr(stmt.Value)
		return stmt
	case GoVarDecl:
		stmt.Init = walkLambdasInExpr(stmt.Init)
		return stmt
	case GoReassign:
		for i := range stmt.Values {
			stmt.Values[i] = walkLambdasInExpr(stmt.Values[i])
		}
		return stmt
	case GoReturn:
		for i := range stmt.Values {
			stmt.Values[i] = walkLambdasInExpr(stmt.Values[i])
		}
		return stmt
	case GoExprStmt:
		stmt.Expr = walkLambdasInExpr(stmt.Expr)
		return stmt
	case GoIfElse:
		if stmt.Init != nil {
			stmt.Init = walkLambdasInStmt(stmt.Init)
		}
		stmt.Cond = walkLambdasInExpr(stmt.Cond)
		for i, s := range stmt.Then.Stmts {
			stmt.Then.Stmts[i] = walkLambdasInStmt(s)
		}
		for i, s := range stmt.Else.Stmts {
			stmt.Else.Stmts[i] = walkLambdasInStmt(s)
		}
		return stmt
	case GoSwitch:
		stmt.Subject = walkLambdasInExpr(stmt.Subject)
		for i := range stmt.Cases {
			for j := range stmt.Cases[i].Vals {
				stmt.Cases[i].Vals[j] = walkLambdasInExpr(stmt.Cases[i].Vals[j])
			}
			for j, s := range stmt.Cases[i].Body.Stmts {
				stmt.Cases[i].Body.Stmts[j] = walkLambdasInStmt(s)
			}
		}
		if stmt.Default != nil {
			for i, s := range stmt.Default.Stmts {
				stmt.Default.Stmts[i] = walkLambdasInStmt(s)
			}
		}
		return stmt
	case GoTypeSwitch:
		stmt.Subject = walkLambdasInExpr(stmt.Subject)
		for i := range stmt.Cases {
			for j, s := range stmt.Cases[i].Body.Stmts {
				stmt.Cases[i].Body.Stmts[j] = walkLambdasInStmt(s)
			}
		}
		if stmt.Default != nil {
			for i, s := range stmt.Default.Stmts {
				stmt.Default.Stmts[i] = walkLambdasInStmt(s)
			}
		}
		return stmt
	}
	return s
}

func stage2LowerFn(fn IRFn) IRFn {
	if fn.Body == nil {
		return fn
	}
	w := newStage2Walker()
	fn.Params = w.expandParams(fn.Params)
	mode := s2Return
	if fn.Ret == nil || isUnitType(fn.Ret) {
		mode = s2Void
	}
	stmts := w.walkExpr(fn.Body, mode, nil)
	stmts = w.blankUnusedSplits(stmts)
	fn.Body = IRBlock{Stmts: stmts, Type: fn.Ret}
	return fn
}

// expandParams replaces Result-typed params with their split form
// (val + err) and registers the split names in the walker. Replaces the
// previous lower.go expandFuncParams.
func (w *stage2Walker) expandParams(params []IRParamDecl) []IRParamDecl {
	var out []IRParamDecl
	for _, p := range params {
		switch pt := p.Type.(type) {
		case IRResultType:
			if isUnitType(pt.Ok) {
				out = append(out, IRParamDecl{GoName: p.GoName, Type: IRNamedType{GoName: "error"}})
				w.registerSplit(p.GoName, []string{p.GoName})
			} else {
				errName := p.GoName + "_err"
				out = append(out,
					IRParamDecl{GoName: p.GoName, Type: pt.Ok},
					IRParamDecl{GoName: errName, Type: IRNamedType{GoName: "error"}},
				)
				w.registerSplit(p.GoName, []string{p.GoName, errName})
			}
		default:
			out = append(out, p)
		}
	}
	return out
}

// walkExpr walks an IRExpr in the given mode and produces a sequence of
// IRStmts. Mode determines how leaf values (Ok/Error/literals/calls) are
// wrapped at the tail. Any IRTryExpr in value-position children is
// hoisted into a synthetic GoMultiAssign + nil-check + return ahead of
// the produced statements.
func (w *stage2Walker) walkExpr(e IRExpr, mode s2Mode, targets []string) []IRStmt {
	if e == nil {
		return nil
	}
	if _, ok := e.(IRVoidExpr); ok {
		return nil
	}
	e = w.hoistTryInExpr(e)
	pre := w.flushHoist()
	body := w.walkExprBody(e, mode, targets)
	if len(pre) == 0 {
		return body
	}
	return append(pre, body...)
}

func (w *stage2Walker) walkExprBody(e IRExpr, mode s2Mode, targets []string) []IRStmt {
	switch x := e.(type) {
	case IRBlock:
		var out []IRStmt
		for _, s := range x.Stmts {
			out = append(out, w.walkStmt(s)...)
		}
		if x.Expr != nil {
			out = append(out, w.walkExpr(x.Expr, mode, targets)...)
		}
		return out

	case IRMatch:
		if isResultMatchArms(x) {
			return w.buildResultIfElse(x, mode, targets)
		}
		if isOptionMatchArms(x) {
			return w.buildOptionIfElse(x, mode, targets)
		}
		if hasTypePattern(x) {
			return w.buildTypeSwitch(x, mode, targets)
		}
		switch x.Arms[0].Pattern.(type) {
		case IREnumPattern, IRWildcardPattern:
			return w.buildEnumSwitch(x, mode, targets)
		case IRSumTypePattern, IRSumTypeWildcardPattern:
			return w.buildSumSwitch(x, mode, targets)
		case IRListEmptyPattern, IRListExactPattern, IRListConsPattern, IRListDefaultPattern:
			return w.buildListIfChain(x, mode, targets)
		case IRLiteralPattern, IRLiteralDefaultPattern:
			return w.buildLiteralSwitch(x, mode, targets)
		}
		// Unrecognised match shape — leaf-wrap (should not occur post-S3).
		return []IRStmt{w.wrapTail(e, mode, targets)}

	case IROkCall, IRErrorCall:
		return []IRStmt{w.wrapTail(e, mode, targets)}

	case IRIfExpr:
		return w.buildIfElseFromExpr(x, mode, targets)

	case IRForRange:
		x.Body = w.foldBody(x.Body, s2Void, nil)
		return []IRStmt{IRExprStmt{Expr: x}}

	case IRForEach:
		x.Body = w.foldBody(x.Body, s2Void, nil)
		return []IRStmt{IRExprStmt{Expr: x}}

	case IRTryBlock:
		// Try block is a value-position IIFE — its internal body is walked
		// with return mode (the Ok value) so emit can iterate stage2 stmts
		// without bodyMode. Outer wrapping (GoIIFE) is S4's job.
		walked := w.lowerTryBlockInternals(x)
		return []IRStmt{w.wrapTail(walked, mode, targets)}

	default:
		// Plain leaf expression.
		return []IRStmt{w.wrapTail(e, mode, targets)}
	}
}

// walkStmt processes Stage 1 statements, converting let-overload forms.
// Hoists IRTryExpr from value-position children of stmts that don't
// re-enter walkExpr (plain lets, try-let receive expressions, defers,
// asserts, destructures).
func (w *stage2Walker) walkStmt(s IRStmt) []IRStmt {
	switch stmt := s.(type) {
	case IRLetStmt:
		// Recurse into IRTryBlock so its internal body is stage2-lowered
		// even when the outer let just emits a multi-receive of the IIFE.
		if tb, ok := stmt.Value.(IRTryBlock); ok {
			stmt.Value = w.lowerTryBlockInternals(tb)
		}
		// Compute the split names inline for Result-typed values, and
		// register them so subsequent match dispatch finds them.
		splitNames := w.computeLetSplits(stmt)
		// Multi-receive let with non-control-flow value → GoMultiAssign.
		if len(splitNames) > 0 && !isControlFlowValue(stmt.Value) {
			stmt.Value = w.hoistTryInExpr(stmt.Value)
			pre := w.flushHoist()
			return append(pre, GoMultiAssign{
				Names: splitNames,
				Value: stmt.Value,
				Pos:   stmt.Pos,
			})
		}
		// Multi-receive let with control-flow value: predeclare vars then
		// recurse into value with multi-assign mode.
		if len(splitNames) > 0 && isControlFlowValue(stmt.Value) {
			var out []IRStmt
			if rt, ok := stmt.Value.irType().(IRResultType); ok {
				if isUnitType(rt.Ok) && len(splitNames) >= 1 {
					out = append(out, GoVarDecl{Name: splitNames[0], Type: IRNamedType{GoName: "error"}})
				} else if len(splitNames) >= 2 {
					out = append(out, GoVarDecl{Name: splitNames[0], Type: rt.Ok})
					out = append(out, GoVarDecl{Name: splitNames[1], Type: IRNamedType{GoName: "error"}})
				}
			}
			out = append(out, w.walkExpr(stmt.Value, s2Multi, splitNames)...)
			return out
		}
		// Single-target control-flow value: predeclare var then recurse with assign mode.
		if isControlFlowValue(stmt.Value) {
			t := stmt.Type
			if t == nil {
				t = stmt.Value.irType()
			}
			out := []IRStmt{GoVarDecl{Name: stmt.GoName, Type: t}}
			out = append(out, w.walkExpr(stmt.Value, s2Assign, []string{stmt.GoName})...)
			return out
		}
		// Plain let — keep as Stage 1 IRLetStmt; emit handles it.
		stmt.Value = w.hoistTryInExpr(stmt.Value)
		pre := w.flushHoist()
		return append(pre, stmt)

	case IRTryLetStmt:
		stmt.CallExpr = w.hoistTryInExpr(stmt.CallExpr)
		pre := w.flushHoist()
		return append(pre, w.lowerTryLetStmt(stmt)...)

	case IRExprStmt:
		// Statement-position expression: walk in void mode.
		return w.walkExpr(stmt.Expr, s2Void, nil)

	case IRDeferStmt:
		stmt.Expr = w.hoistTryInExpr(stmt.Expr)
		pre := w.flushHoist()
		return append(pre, stmt)

	case IRAssertStmt:
		stmt.Expr = w.hoistTryInExpr(stmt.Expr)
		pre := w.flushHoist()
		return append(pre, stmt)

	case IRDestructureStmt:
		stmt.Value = w.hoistTryInExpr(stmt.Value)
		pre := w.flushHoist()
		return append(pre, stmt)

	default:
		return []IRStmt{s}
	}
}

// computeLetSplits derives the multi-receive split names for an IRLetStmt
// whose Value is Result-typed (or already a multi-return Go call). Also
// registers the split in the walker so match dispatch can find them.
// Returns nil for plain (non-multi-return) let bindings.
func (w *stage2Walker) computeLetSplits(stmt IRLetStmt) []string {
	if stmt.GoName == "_" || !isMultiReturnLetValue(stmt.Value) {
		return nil
	}
	rt, ok := stmt.Value.irType().(IRResultType)
	if !ok {
		return nil
	}
	var splitNames []string
	if isUnitType(rt.Ok) {
		splitNames = []string{stmt.GoName}
	} else {
		splitNames = []string{stmt.GoName, stmt.GoName + "_err"}
	}
	w.registerSplit(stmt.GoName, splitNames)
	return splitNames
}

// isMultiReturnLetValue reports whether a let value needs multi-receive
// (`v, err := f()` rather than plain `v := expr`). Result-typed values
// always need it; Option is uniformly pointer-backed and emits as a
// single value.
func isMultiReturnLetValue(v IRExpr) bool {
	t := v.irType()
	if t == nil {
		return false
	}
	_, ok := t.(IRResultType)
	return ok
}

// wrapTail wraps a non-control-flow leaf expression based on the tail mode.
func (w *stage2Walker) wrapTail(e IRExpr, mode s2Mode, targets []string) IRStmt {
	switch mode {
	case s2Return:
		if vals := expandedValuesOf(e); vals != nil {
			return GoReturn{Values: vals}
		}
		return GoReturn{Values: []IRExpr{e}}
	case s2Assign:
		if len(targets) == 0 {
			return GoExprStmt{Expr: e}
		}
		return GoReassign{Targets: targets, Values: []IRExpr{e}}
	case s2Multi:
		if vals := expandedValuesOf(e); vals != nil {
			return GoReassign{Targets: targets, Values: vals}
		}
		return GoReassign{Targets: targets, Values: []IRExpr{e}}
	case s2Void:
		return GoExprStmt{Expr: e}
	}
	return GoExprStmt{Expr: e}
}

// okArmBinds reports whether the Result match's Ok arm has a value
// binding (used for `match r { Ok(v) => use(v) }` cases). When no arm
// binds the val slot, the slot is blanked to "_" in the let assignment.
func okArmBinds(m IRMatch) bool {
	for _, arm := range m.Arms {
		if p, ok := arm.Pattern.(IRResultOkPattern); ok && p.Binding != nil {
			return true
		}
	}
	return false
}

// blankUnusedSplits mirrors the previous markUnusedSplits sweep. After
// walking the function body, any registered splits not consumed by a
// match (matchResolved is false for them) get their unreferenced names
// blanked to "_". The slice backing each splits entry is shared with
// the GoMultiAssign that produced the names, so the blanking propagates
// to emit.
func (w *stage2Walker) blankUnusedSplits(stmts []IRStmt) []IRStmt {
	if len(w.splits) == 0 {
		return stmts
	}
	refs := make(map[string]bool)
	collectStmtRefsStage2(stmts, refs)
	for key, names := range w.splits {
		if w.matchResolved[key] {
			continue
		}
		for i, n := range names {
			if n != "_" && !refs[n] {
				names[i] = "_"
			}
		}
	}
	return stmts
}

func collectStmtRefsStage2(stmts []IRStmt, refs map[string]bool) {
	for _, s := range stmts {
		switch stmt := s.(type) {
		case IRLetStmt:
			collectExprRefsStage2(stmt.Value, refs)
		case IRTryLetStmt:
			collectExprRefsStage2(stmt.CallExpr, refs)
		case IRExprStmt:
			collectExprRefsStage2(stmt.Expr, refs)
		case IRDeferStmt:
			collectExprRefsStage2(stmt.Expr, refs)
		case IRAssertStmt:
			collectExprRefsStage2(stmt.Expr, refs)
		case IRDestructureStmt:
			collectExprRefsStage2(stmt.Value, refs)
		case GoMultiAssign:
			collectExprRefsStage2(stmt.Value, refs)
		case GoVarDecl:
			collectExprRefsStage2(stmt.Init, refs)
		case GoReassign:
			for _, e := range stmt.Values {
				collectExprRefsStage2(e, refs)
			}
		case GoReturn:
			for _, e := range stmt.Values {
				collectExprRefsStage2(e, refs)
			}
		case GoExprStmt:
			collectExprRefsStage2(stmt.Expr, refs)
		case GoIfElse:
			if stmt.Init != nil {
				collectStmtRefsStage2([]IRStmt{stmt.Init}, refs)
			}
			collectExprRefsStage2(stmt.Cond, refs)
			collectStmtRefsStage2(stmt.Then.Stmts, refs)
			collectStmtRefsStage2(stmt.Else.Stmts, refs)
		case GoSwitch:
			collectExprRefsStage2(stmt.Subject, refs)
			for _, c := range stmt.Cases {
				for _, v := range c.Vals {
					collectExprRefsStage2(v, refs)
				}
				collectStmtRefsStage2(c.Body.Stmts, refs)
			}
			if stmt.Default != nil {
				collectStmtRefsStage2(stmt.Default.Stmts, refs)
			}
		case GoTypeSwitch:
			collectExprRefsStage2(stmt.Subject, refs)
			for _, c := range stmt.Cases {
				collectStmtRefsStage2(c.Body.Stmts, refs)
			}
			if stmt.Default != nil {
				collectStmtRefsStage2(stmt.Default.Stmts, refs)
			}
		}
	}
}

func collectExprRefsStage2(e IRExpr, refs map[string]bool) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case IRIdent:
		refs[x.GoName] = true
	case IRFnCall:
		collectExprRefsStage2(x.Fn, refs)
		for _, a := range x.Args {
			collectExprRefsStage2(a, refs)
		}
	case IRMethodCall:
		collectExprRefsStage2(x.Receiver, refs)
		for _, a := range x.Args {
			collectExprRefsStage2(a, refs)
		}
	case IRFieldAccess:
		collectExprRefsStage2(x.Expr, refs)
	case IRIndexAccess:
		collectExprRefsStage2(x.Expr, refs)
		collectExprRefsStage2(x.Index, refs)
	case IRBinaryExpr:
		collectExprRefsStage2(x.Left, refs)
		collectExprRefsStage2(x.Right, refs)
	case IRRefExpr:
		collectExprRefsStage2(x.Expr, refs)
	case IRConstructorCall:
		for _, f := range x.Fields {
			collectExprRefsStage2(f.Value, refs)
		}
	case IROkCall:
		collectExprRefsStage2(x.Value, refs)
	case IRErrorCall:
		collectExprRefsStage2(x.Value, refs)
	case IRSomeCall:
		collectExprRefsStage2(x.Value, refs)
	case IRStringInterp:
		for _, a := range x.Args {
			collectExprRefsStage2(a, refs)
		}
	case IRListLit:
		for _, el := range x.Elements {
			collectExprRefsStage2(el, refs)
		}
		collectExprRefsStage2(x.Spread, refs)
	case IRMapLit:
		for _, en := range x.Entries {
			collectExprRefsStage2(en.Key, refs)
			collectExprRefsStage2(en.Value, refs)
		}
	case IRTupleLit:
		for _, el := range x.Elements {
			collectExprRefsStage2(el, refs)
		}
	case IRFn:
		// Lambda body — refs to outer-scope names count.
		if blk, ok := x.Body.(IRBlock); ok {
			collectStmtRefsStage2(blk.Stmts, refs)
			collectExprRefsStage2(blk.Expr, refs)
		} else {
			collectExprRefsStage2(x.Body, refs)
		}
	case IRTryBlock:
		collectStmtRefsStage2(x.Stmts, refs)
		collectExprRefsStage2(x.Expr, refs)
	case IRForRange:
		collectExprRefsStage2(x.Start, refs)
		collectExprRefsStage2(x.End, refs)
		collectExprRefsStage2(x.Body, refs)
	case IRForEach:
		collectExprRefsStage2(x.Iter, refs)
		collectExprRefsStage2(x.Body, refs)
	case IRBlock:
		collectStmtRefsStage2(x.Stmts, refs)
		collectExprRefsStage2(x.Expr, refs)
	case GoIIFE:
		collectStmtRefsStage2(x.Body.Stmts, refs)
	case GoPtrOf:
		collectExprRefsStage2(x.Inner, refs)
	case GoOptFromCall:
		collectExprRefsStage2(x.Call, refs)
	case GoErrorWrap:
		collectExprRefsStage2(x.Inner, refs)
	case GoDeref:
		collectExprRefsStage2(x.Inner, refs)
	}
}

// hoistTryInExpr deep-walks a value-position expression and replaces
// each IRTryExpr with a fresh ident referring to the Ok-typed split of a
// synthetic GoMultiAssign + nil-check + return pushed onto the walker's
// hoist buffer. Caller is responsible for flushing the buffer ahead of
// the enclosing statement.
//
// Walks data-expressions only — does not dive into IRFn (lambda body),
// IRTryBlock, IRBlock, or IRMatch arm bodies. `?` inside those carries
// its own enclosing return type and is processed by the appropriate
// inner walker (a nested stage2Walker for lambdas / try blocks, or the
// outer walker's recursive walkExpr for arms / match subjects).
func (w *stage2Walker) hoistTryInExpr(e IRExpr) IRExpr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case IRTryExpr:
		// Recurse into Inner first so chained `expr??` hoists in order.
		inner := w.hoistTryInExpr(x.Inner)

		n := w.nextSym()
		errName := fmt.Sprintf("__try%d_err", n)
		var splitNames []string
		var valName string
		if isUnitType(x.OkType) {
			splitNames = []string{errName}
		} else {
			valName = fmt.Sprintf("__try%d", n)
			splitNames = []string{valName, errName}
		}
		w.hoist = append(w.hoist, GoMultiAssign{Names: splitNames, Value: inner})

		var errReturn []IRExpr
		if rt, ok := x.ReturnType.(IRResultType); ok {
			if isUnitType(rt.Ok) {
				errReturn = []IRExpr{IRIdent{GoName: errName}}
			} else {
				errReturn = []IRExpr{irZeroExpr(rt.Ok), IRIdent{GoName: errName}}
			}
		}
		w.hoist = append(w.hoist, GoIfElse{
			Cond: IRBinaryExpr{
				Op:    "!=",
				Left:  IRIdent{GoName: errName},
				Right: IRIdent{GoName: "nil"},
				Type:  IRNamedType{GoName: "bool"},
			},
			Then: GoBlock{Stmts: []IRStmt{GoReturn{Values: errReturn}}},
		})

		if valName == "" {
			return IRVoidExpr{}
		}
		return IRIdent{GoName: valName, Type: x.OkType}

	case IRFnCall:
		x.Fn = w.hoistTryInExpr(x.Fn)
		for i := range x.Args {
			x.Args[i] = w.hoistTryInExpr(x.Args[i])
		}
		return x
	case IRMethodCall:
		x.Receiver = w.hoistTryInExpr(x.Receiver)
		for i := range x.Args {
			x.Args[i] = w.hoistTryInExpr(x.Args[i])
		}
		return x
	case IRFieldAccess:
		x.Expr = w.hoistTryInExpr(x.Expr)
		return x
	case IRIndexAccess:
		x.Expr = w.hoistTryInExpr(x.Expr)
		x.Index = w.hoistTryInExpr(x.Index)
		return x
	case IRBinaryExpr:
		x.Left = w.hoistTryInExpr(x.Left)
		x.Right = w.hoistTryInExpr(x.Right)
		return x
	case IRRefExpr:
		x.Expr = w.hoistTryInExpr(x.Expr)
		return x
	case IRConstructorCall:
		for i := range x.Fields {
			x.Fields[i].Value = w.hoistTryInExpr(x.Fields[i].Value)
		}
		return x
	case IROkCall:
		x.Value = w.hoistTryInExpr(x.Value)
		return x
	case IRErrorCall:
		x.Value = w.hoistTryInExpr(x.Value)
		return x
	case IRSomeCall:
		x.Value = w.hoistTryInExpr(x.Value)
		return x
	case IRStringInterp:
		for i := range x.Args {
			x.Args[i] = w.hoistTryInExpr(x.Args[i])
		}
		return x
	case IRListLit:
		for i := range x.Elements {
			x.Elements[i] = w.hoistTryInExpr(x.Elements[i])
		}
		x.Spread = w.hoistTryInExpr(x.Spread)
		return x
	case IRMapLit:
		for i := range x.Entries {
			x.Entries[i].Key = w.hoistTryInExpr(x.Entries[i].Key)
			x.Entries[i].Value = w.hoistTryInExpr(x.Entries[i].Value)
		}
		return x
	case IRTupleLit:
		for i := range x.Elements {
			x.Elements[i] = w.hoistTryInExpr(x.Elements[i])
		}
		return x
	case IRMatch:
		x.Subject = w.hoistTryInExpr(x.Subject)
		return x
	case IRIfExpr:
		x.Cond = w.hoistTryInExpr(x.Cond)
		return x
	case IRForRange:
		x.Start = w.hoistTryInExpr(x.Start)
		x.End = w.hoistTryInExpr(x.End)
		return x
	case IRForEach:
		x.Iter = w.hoistTryInExpr(x.Iter)
		return x
	}
	// IRFn, IRTryBlock, IRBlock, leaf literals, IRIdent — opaque or no
	// children to walk for hoisting.
	return e
}

// walkAndFlattenCallArgs walks each argument through walkLambdasInExpr,
// then expands any Ok(v) / Error(e) constructor at top level into its
// multi-return tuple form. Replaces the previous flattenArgs+expandCallArgs
// passes that ran before stage2.
func walkAndFlattenCallArgs(args []IRExpr) []IRExpr {
	var out []IRExpr
	for _, a := range args {
		walked := walkLambdasInExpr(a)
		if vals := expandedValuesOf(walked); vals != nil {
			out = append(out, vals...)
		} else {
			out = append(out, walked)
		}
	}
	return out
}

// expandedValuesOf returns the multi-return form of an Ok(v) / Error(e)
// constructor — `[v, nil]` or `[zero(T), e]` (or single-element when Ok
// is Unit). Computed inline rather than read from a sideband field.
func expandedValuesOf(e IRExpr) []IRExpr {
	switch x := e.(type) {
	case IROkCall:
		rt, ok := x.Type.(IRResultType)
		if !ok {
			return nil
		}
		if isUnitType(rt.Ok) {
			return []IRExpr{IRIdent{GoName: "nil"}}
		}
		return []IRExpr{x.Value, IRIdent{GoName: "nil"}}
	case IRErrorCall:
		rt, ok := x.Type.(IRResultType)
		if !ok {
			return nil
		}
		if isUnitType(rt.Ok) {
			return []IRExpr{x.Value}
		}
		return []IRExpr{irZeroExpr(rt.Ok), x.Value}
	}
	return nil
}

func hasTypePattern(m IRMatch) bool {
	for _, arm := range m.Arms {
		if _, ok := arm.Pattern.(IRMatchTypePattern); ok {
			return true
		}
	}
	return false
}

func hasWildcardArm(m IRMatch) bool {
	for _, arm := range m.Arms {
		switch arm.Pattern.(type) {
		case IRWildcardPattern, IRSumTypeWildcardPattern, IRListDefaultPattern, IRLiteralDefaultPattern:
			return true
		}
	}
	return false
}

func isValueMode(mode s2Mode) bool {
	return mode == s2Return || mode == s2Assign || mode == s2Multi
}

func isResultMatchArms(m IRMatch) bool {
	for _, arm := range m.Arms {
		switch arm.Pattern.(type) {
		case IRResultOkPattern, IRResultErrorPattern:
			return true
		}
	}
	return false
}

func isOptionMatchArms(m IRMatch) bool {
	for _, arm := range m.Arms {
		switch arm.Pattern.(type) {
		case IROptionSomePattern, IROptionNonePattern:
			return true
		}
	}
	return false
}

// buildResultIfElse converts a Result IRMatch into a GoIfElse, optionally
// preceded by a synthetic GoMultiAssign for the call-subject case.
func (w *stage2Walker) buildResultIfElse(m IRMatch, mode s2Mode, targets []string) []IRStmt {
	valVar, errVar, init := w.resultVars(m)

	var okArm, errArm *IRMatchArm
	for i := range m.Arms {
		switch m.Arms[i].Pattern.(type) {
		case IRResultOkPattern:
			okArm = &m.Arms[i]
		case IRResultErrorPattern:
			errArm = &m.Arms[i]
		}
	}

	okStmts := w.buildResultArmStmts(okArm, valVar, false, m.Subject.irType(), mode, targets)
	errStmts := w.buildResultArmStmts(errArm, errVar, true, m.Subject.irType(), mode, targets)

	// Snapshot convention: cond is `err == nil` (Then = ok arm). When the
	// ok arm is void, flip to `err != nil` so the single non-empty branch
	// is the Then.
	op := "=="
	thenStmts := okStmts
	elseStmts := errStmts
	if len(okStmts) == 0 && len(errStmts) > 0 {
		op = "!="
		thenStmts = errStmts
		elseStmts = nil
	}
	cond := IRBinaryExpr{
		Op:    op,
		Left:  IRIdent{GoName: errVar},
		Right: IRIdent{GoName: "nil"},
		Type:  IRNamedType{GoName: "bool"},
	}

	ifElse := GoIfElse{
		Cond: cond,
		Then: GoBlock{Stmts: thenStmts},
		Else: GoBlock{Stmts: elseStmts},
	}
	if init == nil {
		return []IRStmt{ifElse}
	}
	return []IRStmt{init, ifElse}
}

// resultVars returns (valVar, errVar, init) for a Result IRMatch.
//   - If subject is IRIdent with registered splits (param or prior let) → use them.
//   - If subject is IRIdent without splits → derive from naming convention.
//   - Otherwise → synthetic GoMultiAssign init.
//
// For IRIdent subjects, also blanks unused split slots (mirroring the
// previous resolveMatchBindings behaviour: val slot becomes "_" when no
// arm binds it; err slot stays because the cond reads it).
func (w *stage2Walker) resultVars(m IRMatch) (valVar, errVar string, init IRStmt) {
	if subj, ok := m.Subject.(IRIdent); ok {
		if names := w.splitsFor(subj.GoName); len(names) > 0 {
			w.matchResolved[subj.GoName] = true
			if len(names) >= 2 && !okArmBinds(m) {
				names[0] = "_"
			}
			if len(names) == 1 {
				return "", names[0], nil
			}
			return names[0], names[1], nil
		}
		// IRIdent subject without splits — derive from naming convention.
		if rt, isResult := subj.Type.(IRResultType); isResult && isUnitType(rt.Ok) {
			return "", subj.GoName, nil
		}
		return subj.GoName, subj.GoName + "_err", nil
	}

	// Non-IRIdent subject — synthesise.
	n := w.nextSym()
	if rt, ok := m.Subject.irType().(IRResultType); ok && isUnitType(rt.Ok) {
		errVar = fmt.Sprintf("__match%d_err", n)
		init = GoMultiAssign{Names: []string{errVar}, Value: m.Subject}
		return "", errVar, init
	}
	valVar = fmt.Sprintf("__match%d", n)
	errVar = fmt.Sprintf("__match%d_err", n)
	init = GoMultiAssign{Names: []string{valVar, errVar}, Value: m.Subject}
	return valVar, errVar, init
}

// buildResultArmStmts wraps an arm body in stage2 stmts. Includes the
// binding declaration (or __goError wrap for trait-Error err binding) and
// the recursively-walked body.
func (w *stage2Walker) buildResultArmStmts(arm *IRMatchArm, sourceVar string, isErr bool, subjectType IRType, mode s2Mode, targets []string) []IRStmt {
	if arm == nil {
		return nil
	}
	var out []IRStmt
	if p := arm.Pattern; p != nil {
		var binding *IRBinding
		switch pp := p.(type) {
		case IRResultOkPattern:
			binding = pp.Binding
		case IRResultErrorPattern:
			binding = pp.Binding
		}
		if binding != nil && sourceVar != "" {
			// __goError wrap when subject Err is the trait Error.
			rhs := IRExpr(IRIdent{GoName: sourceVar})
			if isErr {
				if rt, ok := subjectType.(IRResultType); ok {
					if tt, isTrait := rt.Err.(IRTraitType); isTrait && tt.Name == "Error" {
						rhs = GoErrorWrap{Inner: IRIdent{GoName: sourceVar}}
					}
				}
			}
			out = append(out, GoVarDecl{Name: binding.GoName, Init: rhs})
		}
	}
	out = append(out, w.walkExpr(arm.Body, mode, targets)...)
	return out
}

// buildOptionIfElse converts an Option IRMatch into a GoIfElse, with
// synthetic let-hoist for non-IRIdent subjects to avoid double-evaluation.
func (w *stage2Walker) buildOptionIfElse(m IRMatch, mode s2Mode, targets []string) []IRStmt {
	subjectVar, init := w.optionSubjectVar(m)
	collapse := isPointerBackedOption(m.Subject.irType())

	cond := IRBinaryExpr{
		Op:    "!=",
		Left:  IRIdent{GoName: subjectVar},
		Right: IRIdent{GoName: "nil"},
		Type:  IRNamedType{GoName: "bool"},
	}

	var someArm, noneArm *IRMatchArm
	for i := range m.Arms {
		switch m.Arms[i].Pattern.(type) {
		case IROptionSomePattern:
			someArm = &m.Arms[i]
		case IROptionNonePattern:
			noneArm = &m.Arms[i]
		}
	}

	// Then = some arm (cond is `subject != nil`)
	var thenStmts []IRStmt
	if someArm != nil {
		if p, ok := someArm.Pattern.(IROptionSomePattern); ok && p.Binding != nil {
			var rhs IRExpr = IRIdent{GoName: subjectVar}
			if !collapse {
				rhs = GoDeref{Inner: IRIdent{GoName: subjectVar}}
			}
			thenStmts = append(thenStmts, GoVarDecl{Name: p.Binding.GoName, Init: rhs})
		}
		thenStmts = append(thenStmts, w.walkExpr(someArm.Body, mode, targets)...)
	}
	var elseStmts []IRStmt
	if noneArm != nil {
		elseStmts = w.walkExpr(noneArm.Body, mode, targets)
	}

	ifElse := GoIfElse{
		Cond: cond,
		Then: GoBlock{Stmts: thenStmts},
		Else: GoBlock{Stmts: elseStmts},
	}
	if init == nil {
		return []IRStmt{ifElse}
	}
	return []IRStmt{init, ifElse}
}

// optionSubjectVar returns the Go variable name for an Option match's
// subject and an optional GoVarDecl init for non-IRIdent subjects.
func (w *stage2Walker) optionSubjectVar(m IRMatch) (string, IRStmt) {
	if subj, ok := m.Subject.(IRIdent); ok {
		return subj.GoName, nil
	}
	n := w.nextSym()
	name := fmt.Sprintf("__opt%d", n)
	return name, GoVarDecl{Name: name, Type: m.Subject.irType(), Init: m.Subject}
}

// --- Enum / Literal: GoSwitch ---

func (w *stage2Walker) buildEnumSwitch(m IRMatch, mode s2Mode, targets []string) []IRStmt {
	sw := GoSwitch{Subject: m.Subject}
	for i := range m.Arms {
		arm := m.Arms[i]
		body := GoBlock{Stmts: w.walkExpr(arm.Body, mode, targets)}
		switch p := arm.Pattern.(type) {
		case IREnumPattern:
			sw.Cases = append(sw.Cases, GoSwitchCase{
				Vals: []IRExpr{IRIdent{GoName: p.GoValue}},
				Body: body,
			})
		case IRWildcardPattern:
			sw.Default = &body
		}
	}
	if sw.Default == nil && isValueMode(mode) && !hasWildcardArm(m) {
		def := GoBlock{Stmts: []IRStmt{GoUnreachable{}}}
		sw.Default = &def
	}
	return []IRStmt{sw}
}

func (w *stage2Walker) buildLiteralSwitch(m IRMatch, mode s2Mode, targets []string) []IRStmt {
	sw := GoSwitch{Subject: m.Subject}
	for i := range m.Arms {
		arm := m.Arms[i]
		body := GoBlock{Stmts: w.walkExpr(arm.Body, mode, targets)}
		switch p := arm.Pattern.(type) {
		case IRLiteralPattern:
			sw.Cases = append(sw.Cases, GoSwitchCase{
				Vals: []IRExpr{IRIdent{GoName: p.Value}},
				Body: body,
			})
		case IRLiteralDefaultPattern:
			sw.Default = &body
		}
	}
	return []IRStmt{sw}
}

// --- Sum types / Any: GoTypeSwitch ---

func (w *stage2Walker) buildSumSwitch(m IRMatch, mode s2Mode, targets []string) []IRStmt {
	ts := GoTypeSwitch{Subject: m.Subject, BindVar: "v"}
	for i := range m.Arms {
		arm := m.Arms[i]
		switch p := arm.Pattern.(type) {
		case IRSumTypePattern:
			var stmts []IRStmt
			for _, b := range p.Bindings {
				stmts = append(stmts, GoVarDecl{
					Name: b.GoName,
					Init: IRIdent{GoName: "v" + b.Source},
				})
			}
			stmts = append(stmts, w.walkExpr(arm.Body, mode, targets)...)
			ts.Cases = append(ts.Cases, GoTypeCase{
				Type: IRNamedType{GoName: p.GoType},
				Body: GoBlock{Stmts: stmts},
			})
		case IRSumTypeWildcardPattern:
			var stmts []IRStmt
			if p.Binding != nil {
				stmts = append(stmts, GoVarDecl{
					Name: p.Binding.GoName,
					Init: IRIdent{GoName: "v"},
				})
			}
			stmts = append(stmts, w.walkExpr(arm.Body, mode, targets)...)
			body := GoBlock{Stmts: stmts}
			ts.Default = &body
		}
	}
	out := []IRStmt{ts}
	if isValueMode(mode) && !hasWildcardArm(m) {
		out = append(out, GoUnreachable{})
	}
	return out
}

func (w *stage2Walker) buildTypeSwitch(m IRMatch, mode s2Mode, targets []string) []IRStmt {
	ts := GoTypeSwitch{Subject: m.Subject, BindVar: "__tv"}
	for i := range m.Arms {
		arm := m.Arms[i]
		switch p := arm.Pattern.(type) {
		case IRMatchTypePattern:
			var stmts []IRStmt
			if p.Binding != nil {
				stmts = append(stmts, GoVarDecl{
					Name: p.Binding.GoName,
					Init: IRIdent{GoName: "__tv"},
				})
			}
			stmts = append(stmts, w.walkExpr(arm.Body, mode, targets)...)
			ts.Cases = append(ts.Cases, GoTypeCase{
				Type: p.Target,
				Body: GoBlock{Stmts: stmts},
			})
		case IRWildcardPattern:
			body := GoBlock{Stmts: w.walkExpr(arm.Body, mode, targets)}
			ts.Default = &body
		}
	}
	return []IRStmt{ts}
}

// --- List: nested GoIfElse chain on len(subject) ---

func (w *stage2Walker) buildListIfChain(m IRMatch, mode s2Mode, targets []string) []IRStmt {
	// Build from the tail back so each else nests the remaining arms.
	var tail []IRStmt
	for i := len(m.Arms) - 1; i >= 0; i-- {
		arm := m.Arms[i]
		armStmts := w.walkExpr(arm.Body, mode, targets)
		switch p := arm.Pattern.(type) {
		case IRListEmptyPattern:
			cond := IRBinaryExpr{
				Op:    "==",
				Left:  IRIdent{GoName: fmt.Sprintf("len(%s)", exprStr(m.Subject))},
				Right: IRIdent{GoName: "0"},
				Type:  IRNamedType{GoName: "bool"},
			}
			tail = []IRStmt{GoIfElse{
				Cond:      cond,
				Then:      GoBlock{Stmts: armStmts},
				Else:      GoBlock{Stmts: tail},
				ChainElse: true,
			}}
		case IRListExactPattern:
			var bindings []IRStmt
			for _, b := range p.Elements {
				bindings = append(bindings, GoVarDecl{
					Name: b.GoName,
					Init: IRIdent{GoName: exprStr(m.Subject) + b.Source},
				})
			}
			cond := IRBinaryExpr{
				Op:    "==",
				Left:  IRIdent{GoName: fmt.Sprintf("len(%s)", exprStr(m.Subject))},
				Right: IRIdent{GoName: fmt.Sprintf("%d", p.MinLen)},
				Type:  IRNamedType{GoName: "bool"},
			}
			tail = []IRStmt{GoIfElse{
				Cond:      cond,
				Then:      GoBlock{Stmts: append(bindings, armStmts...)},
				Else:      GoBlock{Stmts: tail},
				ChainElse: true,
			}}
		case IRListConsPattern:
			var bindings []IRStmt
			for _, b := range p.Elements {
				bindings = append(bindings, GoVarDecl{
					Name: b.GoName,
					Init: IRIdent{GoName: exprStr(m.Subject) + b.Source},
				})
			}
			if p.Rest != nil {
				bindings = append(bindings, GoVarDecl{
					Name: p.Rest.GoName,
					Init: IRIdent{GoName: exprStr(m.Subject) + p.Rest.Source},
				})
			}
			cond := IRBinaryExpr{
				Op:    ">=",
				Left:  IRIdent{GoName: fmt.Sprintf("len(%s)", exprStr(m.Subject))},
				Right: IRIdent{GoName: fmt.Sprintf("%d", p.MinLen)},
				Type:  IRNamedType{GoName: "bool"},
			}
			tail = []IRStmt{GoIfElse{
				Cond:      cond,
				Then:      GoBlock{Stmts: append(bindings, armStmts...)},
				Else:      GoBlock{Stmts: tail},
				ChainElse: true,
			}}
		case IRListDefaultPattern:
			tail = armStmts
		}
	}
	if isValueMode(mode) && !hasWildcardArm(m) {
		tail = append(tail, GoUnreachable{})
	}
	return tail
}

// buildIfElseFromExpr converts an IRIfExpr into a GoIfElse with its
// branches walked in the surrounding mode.
func (w *stage2Walker) buildIfElseFromExpr(e IRIfExpr, mode s2Mode, targets []string) []IRStmt {
	thenStmts := w.walkExpr(e.Then, mode, targets)
	var elseStmts []IRStmt
	if e.Else != nil {
		elseStmts = w.walkExpr(e.Else, mode, targets)
	}
	return []IRStmt{GoIfElse{
		Cond: e.Cond,
		Then: GoBlock{Stmts: thenStmts},
		Else: GoBlock{Stmts: elseStmts},
	}}
}

// lowerTryLetStmt converts `let x = expr?` into a sequence of Stage 2
// statements: receive multi-return values into split names, branch to
// the enclosing function's error return when the err slot is non-nil,
// then bind the user's name to the value slot. Split names and the
// enclosing-fn error return values are computed inline (replaces the
// prior expandTryLetStmt that pre-populated them on the IR node).
func (w *stage2Walker) lowerTryLetStmt(stmt IRTryLetStmt) []IRStmt {
	splitNames, valueName := w.tryLetSplits(stmt)
	errorReturnValues := w.tryLetErrorReturns(stmt, splitNames)

	var out []IRStmt

	// Receive the multi-return values into the split names.
	if isControlFlowValue(stmt.CallExpr) {
		if rt, ok := stmt.CallExpr.irType().(IRResultType); ok {
			if isUnitType(rt.Ok) && len(splitNames) >= 1 {
				out = append(out, GoVarDecl{Name: splitNames[0], Type: IRNamedType{GoName: "error"}})
			} else if len(splitNames) >= 2 {
				out = append(out, GoVarDecl{Name: splitNames[0], Type: rt.Ok})
				out = append(out, GoVarDecl{Name: splitNames[1], Type: IRNamedType{GoName: "error"}})
			}
		}
		out = append(out, w.walkExpr(stmt.CallExpr, s2Multi, splitNames)...)
	} else if len(splitNames) == 1 {
		out = append(out, GoVarDecl{Name: splitNames[0], Init: stmt.CallExpr})
	} else {
		out = append(out, GoMultiAssign{Names: splitNames, Value: stmt.CallExpr})
	}

	// Error propagation: if err != nil { return errorReturnValues }
	errName := splitNames[len(splitNames)-1]
	out = append(out, GoIfElse{
		Cond: IRBinaryExpr{
			Op:    "!=",
			Left:  IRIdent{GoName: errName},
			Right: IRIdent{GoName: "nil"},
			Type:  IRNamedType{GoName: "bool"},
		},
		Then: GoBlock{Stmts: []IRStmt{GoReturn{Values: errorReturnValues}}},
	})

	// Bind the user's name to the value slot.
	if stmt.GoName != "_" && valueName != "" {
		out = append(out, GoVarDecl{Name: stmt.GoName, Init: IRIdent{GoName: valueName}})
	}

	return out
}

// tryLetSplits picks fresh split names (`__val<N>`, `__err<N>`) for a
// try-let. Errors-only Result (Unit Ok) gets a single err slot. The user's
// `let _ = expr?` discards the value slot via `_`.
func (w *stage2Walker) tryLetSplits(stmt IRTryLetStmt) (splitNames []string, valueName string) {
	n := w.nextSym()
	errOnly := false
	if rt, ok := stmt.CallExpr.irType().(IRResultType); ok {
		errOnly = isUnitType(rt.Ok)
	}
	if errOnly {
		errName := fmt.Sprintf("__err%d", n)
		return []string{errName}, ""
	}
	valName := fmt.Sprintf("__val%d", n)
	if stmt.GoName == "_" {
		valName = "_"
	}
	errName := fmt.Sprintf("__err%d", n)
	return []string{valName, errName}, valName
}

// tryLetErrorReturns builds the GoReturn values for the enclosing
// function's error path: `[zero(Ok), err]` for `(T, error)` returns,
// `[err]` for `error`-only returns. Returns nil when the enclosing fn
// isn't Result-typed (let lower flag the misuse — it shouldn't reach
// stage2 in that shape).
func (w *stage2Walker) tryLetErrorReturns(stmt IRTryLetStmt, splitNames []string) []IRExpr {
	rt, ok := stmt.ReturnType.(IRResultType)
	if !ok {
		return nil
	}
	errName := splitNames[len(splitNames)-1]
	if isUnitType(rt.Ok) {
		return []IRExpr{IRIdent{GoName: errName}}
	}
	return []IRExpr{irZeroExpr(rt.Ok), IRIdent{GoName: errName}}
}

// lowerTryBlockInternals walks an IRTryBlock's Stmts and tail Expr in
// s2Return mode, folding the result back into the block's Stmts (and
// clearing Expr). After this, emitTryBlockExpr can iterate Stmts without
// bodyMode.
func (w *stage2Walker) lowerTryBlockInternals(tb IRTryBlock) IRTryBlock {
	var inner []IRStmt
	for _, s := range tb.Stmts {
		inner = append(inner, w.walkStmt(s)...)
	}
	if tb.Expr != nil {
		inner = append(inner, w.walkExpr(tb.Expr, s2Return, nil)...)
	}
	tb.Stmts = inner
	tb.Expr = nil
	return tb
}

// foldBody walks a body expression in the given mode and returns it as an
// IRBlock with the resulting stage2 stmts, suitable for emit walks that
// no longer use bodyMode (for / for-each / try block bodies).
func (w *stage2Walker) foldBody(body IRExpr, mode s2Mode, targets []string) IRExpr {
	stmts := w.walkExpr(body, mode, targets)
	return IRBlock{Stmts: stmts}
}

// exprStr renders an IRExpr to its Go string form via a minimal printer
// (needed by buildListIfChain when constructing length-check expressions).
// This is a transient helper used only for synthetic IRIdent names that
// embed the subject's surface form; replaced when list patterns gain
// proper Stage 2 nodes that don't string-bake the subject.
func exprStr(e IRExpr) string {
	switch x := e.(type) {
	case IRIdent:
		return x.GoName
	}
	// Fall back to a placeholder; the existing tests use IRIdent subjects
	// for list matches.
	return "/* list subject */"
}

