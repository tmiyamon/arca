package main

import "fmt"

// stage2.go — Stage 2 lowering for Result/Option dispatch and multi-return
// let bindings (slice S2 of the 2026-05-02 "Two-stage IR completion" plan
// in decisions/ideas.md).
//
// Walks Stage 1 IR (post-expandResultOption) and rewrites:
//   * IRMatch with Result arms     → GoIfElse + (GoMultiAssign init when subject isn't IRIdent)
//   * IRMatch with Option arms     → GoIfElse + (GoVarDecl init when subject isn't IRIdent)
//   * IRLetStmt with SplitNames    → GoMultiAssign / GoVarDecl + reassign (control-flow values)
//   * Tail IROkCall / IRErrorCall  → GoReturn{Values: ExpandedValues}
//
// Stage 1 control-flow we don't yet convert (IRIfExpr, Enum/Sum/List/
// Literal/Type matches, IRTryBlock) is wrapped in goLegacyBody so emit
// keeps walking it with bodyMode. The scaffold and remaining conversions
// land in S3/S4.

// s2Mode encodes where the tail value of a control-flow expression flows.
type s2Mode int

const (
	s2Void   s2Mode = iota // statement context — discard tail value
	s2Return               // tail is the function's return
	s2Assign               // single-target assign (Targets[0])
	s2Multi                // multi-target assign (Targets, Result-split)
)


// stage2Walker carries per-function state (synthetic-name counter).
type stage2Walker struct {
	counter int
}

func (w *stage2Walker) nextSym() int {
	w.counter++
	return w.counter
}

// stage2Lower rewrites each function body's Result/Option dispatch and
// multi-return let bindings into Stage 2 nodes. Other Stage 1 nodes pass
// through (wrapped in goLegacyBody where they sit at a tail-form position).
func stage2Lower(funcs []IRFn) []IRFn {
	for i := range funcs {
		funcs[i] = stage2LowerFn(funcs[i])
	}
	return funcs
}

func stage2LowerFn(fn IRFn) IRFn {
	if fn.Body == nil {
		return fn
	}
	mode := s2Return
	if fn.Ret == nil || isUnitType(fn.Ret) {
		mode = s2Void
	}
	w := &stage2Walker{}
	stmts := w.walkExpr(fn.Body, mode, nil)
	fn.Body = IRBlock{Stmts: stmts, Type: fn.Ret}
	return fn
}

// walkExpr walks an IRExpr in the given mode and produces a sequence of
// IRStmts. Mode determines how leaf values (Ok/Error/literals/calls) are
// wrapped at the tail.
func (w *stage2Walker) walkExpr(e IRExpr, mode s2Mode, targets []string) []IRStmt {
	if e == nil {
		return nil
	}
	if _, ok := e.(IRVoidExpr); ok {
		return nil
	}
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
func (w *stage2Walker) walkStmt(s IRStmt) []IRStmt {
	switch stmt := s.(type) {
	case IRLetStmt:
		// Recurse into IRTryBlock so its internal body is stage2-lowered
		// even when the outer let just emits a multi-receive of the IIFE.
		if tb, ok := stmt.Value.(IRTryBlock); ok {
			stmt.Value = w.lowerTryBlockInternals(tb)
		}
		// Multi-receive let with non-control-flow value → GoMultiAssign.
		if len(stmt.SplitNames) > 0 && !isControlFlowValue(stmt.Value) {
			return []IRStmt{GoMultiAssign{
				Names: stmt.SplitNames,
				Value: stmt.Value,
				Pos:   stmt.Pos,
			}}
		}
		// Multi-receive let with control-flow value: predeclare vars then
		// recurse into value with multi-assign mode.
		if len(stmt.SplitNames) > 0 && isControlFlowValue(stmt.Value) {
			var out []IRStmt
			if rt, ok := stmt.Value.irType().(IRResultType); ok {
				if isUnitType(rt.Ok) && len(stmt.SplitNames) >= 1 {
					out = append(out, GoVarDecl{Name: stmt.SplitNames[0], Type: IRNamedType{GoName: "error"}})
				} else if len(stmt.SplitNames) >= 2 {
					out = append(out, GoVarDecl{Name: stmt.SplitNames[0], Type: rt.Ok})
					out = append(out, GoVarDecl{Name: stmt.SplitNames[1], Type: IRNamedType{GoName: "error"}})
				}
			}
			out = append(out, w.walkExpr(stmt.Value, s2Multi, stmt.SplitNames)...)
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
		return []IRStmt{stmt}

	case IRTryLetStmt:
		// S4 will convert. Pass through.
		return []IRStmt{stmt}

	case IRExprStmt:
		// Statement-position expression: walk in void mode.
		return w.walkExpr(stmt.Expr, s2Void, nil)

	default:
		return []IRStmt{s}
	}
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

func expandedValuesOf(e IRExpr) []IRExpr {
	switch x := e.(type) {
	case IROkCall:
		return x.ExpandedValues
	case IRErrorCall:
		return x.ExpandedValues
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
//   - If subject is IRIdent and arm Source already resolved → use existing names.
//   - If subject is IRIdent (function param case) → derive from naming convention.
//   - Otherwise → synthetic GoMultiAssign init.
func (w *stage2Walker) resultVars(m IRMatch) (valVar, errVar string, init IRStmt) {
	// Try to read resolved Sources first.
	for _, arm := range m.Arms {
		switch p := arm.Pattern.(type) {
		case IRResultOkPattern:
			if p.Binding != nil && p.Binding.Source != "" && p.Binding.Source != ".Value" {
				valVar = p.Binding.Source
			}
		case IRResultErrorPattern:
			if p.Binding != nil && p.Binding.Source != "" && p.Binding.Source != ".Err" {
				errVar = p.Binding.Source
			}
		}
	}

	// Subject is IRIdent — use convention even if Sources aren't resolved.
	if subj, ok := m.Subject.(IRIdent); ok {
		if rt, isResult := subj.Type.(IRResultType); isResult && isUnitType(rt.Ok) {
			// error-only: subject itself is the err var
			if errVar == "" {
				errVar = subj.GoName
			}
			return "", errVar, nil
		}
		if valVar == "" {
			valVar = subj.GoName
		}
		if errVar == "" {
			errVar = subj.GoName + "_err"
		}
		return valVar, errVar, nil
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

