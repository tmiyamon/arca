package main

import (
	"fmt"
	"sort"
	"strings"
)

// Lowerer converts an AST Program into an IR Program.
// It resolves names, constructors, builtins, shadowing, and match kinds.
type Lowerer struct {
	types        map[string]TypeDecl
	typeAliases  map[string]TypeAliasDecl
	traits       map[string]TraitDecl
	impls        map[string][]ImplDecl // keyed by target type name
	bindableTypes map[string]bool       // type names with `derive Bindable`; B2b+ consume
	ctorTypes    map[string]string     // constructor name → type name
	fnNames      map[string]string     // arca name → Go name for pub functions
	functions    map[string]FnDecl
	moduleNames  map[string]bool
	goModule     string
	typeResolver  TypeResolver
	goPackages    map[string]*GoPackage // short name → Go package info (carries Pos/SideEffect/Used)

	// Per-function state
	currentRetType         Type
	currentIRRetType       IRType // overrides currentRetType when set (for try blocks with type vars)
	currentReceiver        string
	currentTypeName        string
	currentFnTypeParams    []string        // names of [T, U, ...] declared on the enclosing fn
	currentFnBindableParams map[string]bool // subset whose constraint is Bindable; B2e dispatch passes these through transitively
	matchHint              IRType // type hint for match arm bodies

	// Collected during lowering
	imports      []IRImport
	builtins     map[string]bool
	tmpCounter   int
	errors       []CompileError
	symbols      []SymbolInfo // all symbols (flat, for LSP global list)
	rootScope    *Scope       // root of scope tree (preserved after lowering)
	currentScope *Scope       // current scope during lowering

	// HM type inference — per-function scope
	infer *InferScope
}

// --- HM type inference ---

// InferScope holds type inference state for a single function body.
type InferScope struct {
	varCounter    int
	substitution  map[int]IRType
	typeParamVars map[string]int
}

func NewInferScope() *InferScope {
	return &InferScope{
		substitution:  make(map[int]IRType),
		typeParamVars: make(map[string]int),
	}
}

func (s *InferScope) freshTypeVar() IRTypeVar {
	s.varCounter++
	return IRTypeVar{ID: s.varCounter}
}

func (s *InferScope) resolve(t IRType) IRType {
	tv, ok := t.(IRTypeVar)
	if !ok {
		return t
	}
	if resolved, exists := s.substitution[tv.ID]; exists {
		r := s.resolve(resolved)
		s.substitution[tv.ID] = r // path compression
		return r
	}
	return t
}

func (s *InferScope) resolveDeep(t IRType) IRType {
	if t == nil {
		return nil
	}
	t = s.resolve(t)
	switch tt := t.(type) {
	case IRResultType:
		return IRResultType{Ok: s.resolveDeep(tt.Ok), Err: s.resolveDeep(tt.Err)}
	case IROptionType:
		return IROptionType{Inner: s.resolveDeep(tt.Inner)}
	case IRListType:
		return IRListType{Elem: s.resolveDeep(tt.Elem)}
	case IRTupleType:
		elems := make([]IRType, len(tt.Elements))
		for i, e := range tt.Elements {
			elems[i] = s.resolveDeep(e)
		}
		return IRTupleType{Elements: elems}
	case IRPointerType:
		return IRPointerType{Inner: s.resolveDeep(tt.Inner)}
	case IRRefType:
		return IRRefType{Inner: s.resolveDeep(tt.Inner)}
	case IRNamedType:
		if len(tt.Params) == 0 {
			return tt
		}
		params := make([]IRType, len(tt.Params))
		for i, p := range tt.Params {
			params[i] = s.resolveDeep(p)
		}
		return IRNamedType{GoName: tt.GoName, Params: params}
	case IRFnType:
		params := make([]IRType, len(tt.Params))
		for i, p := range tt.Params {
			params[i] = s.resolveDeep(p)
		}
		return IRFnType{Params: params, Ret: s.resolveDeep(tt.Ret)}
	default:
		return t
	}
}

func (s *InferScope) unify(a, b IRType) bool {
	a = s.resolve(a)
	b = s.resolve(b)

	if a == nil || b == nil {
		return true
	}

	if av, ok := a.(IRTypeVar); ok {
		if bv, ok := b.(IRTypeVar); ok && av.ID == bv.ID {
			return true // identical type-var: no binding needed (avoids self-cycle)
		}
		s.substitution[av.ID] = b
		return true
	}
	if bv, ok := b.(IRTypeVar); ok {
		s.substitution[bv.ID] = a
		return true
	}

	if _, ok := a.(IRInterfaceType); ok {
		return true
	}
	if _, ok := b.(IRInterfaceType); ok {
		return true
	}
	// IRError unifies with anything to suppress cascade errors — the
	// resolution failure has already been reported at its origin site.
	if _, ok := a.(IRError); ok {
		return true
	}
	if _, ok := b.(IRError); ok {
		return true
	}

	switch at := a.(type) {
	case IRNamedType:
		if bt, ok := b.(IRNamedType); ok {
			if at.GoName != bt.GoName || len(at.Params) != len(bt.Params) {
				return false
			}
			for i := range at.Params {
				if !s.unify(at.Params[i], bt.Params[i]) {
					return false
				}
			}
			return true
		}
		// Go stdlib `error` unifies with Arca's Error trait: same runtime
		// representation in Go return positions.
		if at.GoName == "error" {
			if bt, ok := b.(IRTraitType); ok {
				return bt.Name == "Error"
			}
		}
		return false
	case IRResultType:
		bt, ok := b.(IRResultType)
		if !ok {
			return false
		}
		return s.unify(at.Ok, bt.Ok) && s.unify(at.Err, bt.Err)
	case IROptionType:
		bt, ok := b.(IROptionType)
		if !ok {
			return false
		}
		return s.unify(at.Inner, bt.Inner)
	case IRListType:
		bt, ok := b.(IRListType)
		if !ok {
			return false
		}
		return s.unify(at.Elem, bt.Elem)
	case IRMapType:
		bt, ok := b.(IRMapType)
		if !ok {
			return false
		}
		return s.unify(at.Key, bt.Key) && s.unify(at.Value, bt.Value)
	case IRTupleType:
		bt, ok := b.(IRTupleType)
		if !ok || len(at.Elements) != len(bt.Elements) {
			return false
		}
		for i := range at.Elements {
			if !s.unify(at.Elements[i], bt.Elements[i]) {
				return false
			}
		}
		return true
	case IRPointerType:
		if bt, ok := b.(IRPointerType); ok {
			return s.unify(at.Inner, bt.Inner)
		}
		// Transitional compat: legacy `*T` Arca syntax parses to IRPointerType,
		// while FFI-wrapped positions now produce IRRefType. Both emit as Go *T.
		// Keep interchangeable until `*T` user syntax is retired.
		if rt, ok := b.(IRRefType); ok {
			return s.unify(at.Inner, rt.Inner)
		}
		return false
	case IRRefType:
		if bt, ok := b.(IRRefType); ok {
			return s.unify(at.Inner, bt.Inner)
		}
		if pt, ok := b.(IRPointerType); ok {
			return s.unify(at.Inner, pt.Inner)
		}
		return false
	case IRTraitType:
		if bt, ok := b.(IRTraitType); ok {
			return at.Name == bt.Name
		}
		// Arca trait Error and Go stdlib error (IRNamedType{"error"}) are
		// interchangeable in IR: both emit as `error` in return positions
		// and interop via auto-shim + __goError wrap.
		if at.Name == "Error" {
			if bt, ok := b.(IRNamedType); ok {
				return bt.GoName == "error"
			}
		}
		return false
	case IRFnType:
		bt, ok := b.(IRFnType)
		if !ok || len(at.Params) != len(bt.Params) {
			return false
		}
		for i := range at.Params {
			if !s.unify(at.Params[i], bt.Params[i]) {
				return false
			}
		}
		return s.unify(at.Ret, bt.Ret)
	}

	return false
}

func (s *InferScope) typeParamVar(name string) IRTypeVar {
	if id, ok := s.typeParamVars[name]; ok {
		return IRTypeVar{ID: id}
	}
	tv := s.freshTypeVar()
	s.typeParamVars[name] = tv.ID
	return tv
}

// Lowerer convenience methods that delegate to current InferScope.
func (l *Lowerer) freshTypeVar() IRTypeVar   { return l.infer.freshTypeVar() }
func (l *Lowerer) resolve(t IRType) IRType    { return l.infer.resolve(t) }
func (l *Lowerer) resolveDeep(t IRType) IRType {
	// After Lower() finishes l.infer is nil (withInferScope restores the saved
	// nil). LSP-driven partial re-lowering calls resolveDeep outside a live
	// infer scope; in that case the type is already concrete (nothing to
	// substitute) so returning it unchanged is correct.
	if l.infer == nil {
		return t
	}
	return l.infer.resolveDeep(t)
}
func (l *Lowerer) typeParamVar(name string) IRTypeVar { return l.infer.typeParamVar(name) }

// unify runs HM unification on two IR types and reports ErrTypeMismatch at
// pos on failure. This is the type-checking entry point — every call site
// that needs error reporting goes through here.
//
// Raw substitution-only unification (fresh-var binding, hint propagation
// that must not report) uses `l.infer.unify(a, b)` directly instead. That
// makes the intent visible in the call: if you see `l.unify(...)` you are
// type-checking, if you see `l.infer.unify(...)` you are rewriting
// substitution for codegen.
//
// Besides structural HM unification, this wrapper accepts
// constraint-compatible type alias pairs (e.g. AdultAge → Age) at the top
// level so hint-based type checks can flow stricter-to-wider alias values.
func (l *Lowerer) unify(a, b IRType, pos Pos) bool {
	if l.infer.unify(a, b) {
		return true
	}
	if l.constraintCompatible(a, b) {
		return true
	}
	if l.traitImplCompatible(a, b) {
		return true
	}
	if l.errorTraitCompatible(a, b) {
		return true
	}
	l.addCompileError(ErrTypeMismatch, pos, TypeMismatchData{
		Expected: irTypeDisplayStr(l.resolveDeep(b)),
		Actual:   irTypeDisplayStr(l.resolveDeep(a)),
	})
	return false
}

// errorTraitCompatible bridges Arca's Error trait and Go's stdlib `error`
// interface at unify time. Both emit as `error` in Go return positions and
// interop via the auto-generated shim plus __goError wrap, so internal IR
// sites that still reference IRNamedType{"error"} (e.g., try hint defaults,
// some ad-hoc result ctor paths) flow into user-declared Result[_, Error]
// without rewriting every site.
func (l *Lowerer) errorTraitCompatible(a, b IRType) bool {
	isError := func(t IRType) bool {
		switch tt := l.resolveDeep(t).(type) {
		case IRNamedType:
			return tt.GoName == "error"
		case IRTraitType:
			return tt.Name == "Error"
		}
		return false
	}
	return isError(a) && isError(b)
}

// traitImplCompatible reports whether a concrete type and a trait type pair
// via a registered `impl Type: Trait`. Symmetric — either side may be the
// trait, since unify is order-agnostic at call sites.
func (l *Lowerer) traitImplCompatible(a, b IRType) bool {
	an := l.resolveDeep(a)
	bn := l.resolveDeep(b)
	if tt, ok := an.(IRTraitType); ok {
		if nn, ok := bn.(IRNamedType); ok {
			return l.typeImplementsTrait(nn.GoName, tt.Name)
		}
	}
	if tt, ok := bn.(IRTraitType); ok {
		if nn, ok := an.(IRNamedType); ok {
			return l.typeImplementsTrait(nn.GoName, tt.Name)
		}
	}
	return false
}

func (l *Lowerer) typeImplementsTrait(typeName, traitName string) bool {
	for _, impl := range l.impls[typeName] {
		if impl.TraitName == traitName {
			return true
		}
	}
	return false
}

// constraintCompatible reports whether two resolved named types are related
// by constrained alias widening. Used as a last-ditch success path in unify
// so `AdultAge → Age` hint checks still pass.
func (l *Lowerer) constraintCompatible(a, b IRType) bool {
	an, ok := l.resolveDeep(a).(IRNamedType)
	if !ok {
		return false
	}
	bn, ok := l.resolveDeep(b).(IRNamedType)
	if !ok {
		return false
	}
	return l.isConstraintCompatible(an.GoName, bn.GoName)
}

// withInferScope runs fn with a fresh InferScope, restoring the previous scope after.
func (l *Lowerer) withInferScope(fn func()) {
	saved := l.infer
	l.infer = NewInferScope()
	fn()
	l.infer = saved
}

// resolveExprTypes walks an IR expression tree and resolves type variables
// to their concrete types after unification.
// resolveResultExprTypeArgs resolves a Result-typed builtin call's TypeArgs string.
func (l *Lowerer) resolveResultExprTypeArgs(t IRType) (IRType, string) {
	resolved := l.resolveDeep(t)
	if rt, ok := resolved.(IRResultType); ok {
		return resolved, "[" + irTypeEmitStr(rt.Ok) + ", " + irTypeEmitStr(rt.Err) + "]"
	}
	return resolved, ""
}

// resolveExprs applies resolveExprTypes to a slice in place.
func (l *Lowerer) resolveExprs(es []IRExpr) {
	for i := range es {
		es[i] = l.resolveExprTypes(es[i])
	}
}

func (l *Lowerer) resolveExprTypes(e IRExpr) IRExpr {
	if e == nil {
		return nil
	}
	switch expr := e.(type) {
	case IRNoneExpr:
		resolved := l.resolveDeep(expr.Type)
		if ot, ok := resolved.(IROptionType); ok {
			expr.TypeArg = "[" + irTypeEmitStr(ot.Inner) + "]"
		}
		expr.Type = resolved
		return expr
	case IROkCall:
		expr.Type, expr.TypeArgs = l.resolveResultExprTypeArgs(expr.Type)
		expr.Value = l.resolveExprTypes(expr.Value)
		return expr
	case IRErrorCall:
		expr.Type, expr.TypeArgs = l.resolveResultExprTypeArgs(expr.Type)
		expr.Value = l.resolveExprTypes(expr.Value)
		return expr
	case IRSomeCall:
		expr.Type = l.resolveDeep(expr.Type)
		expr.Value = l.resolveExprTypes(expr.Value)
		return expr
	case IRBlock:
		for i, stmt := range expr.Stmts {
			expr.Stmts[i] = l.resolveStmtTypes(stmt)
		}
		expr.Expr = l.resolveExprTypes(expr.Expr)
		return expr
	case IRMatch:
		expr.Subject = l.resolveExprTypes(expr.Subject)
		for i := range expr.Arms {
			expr.Arms[i].Body = l.resolveExprTypes(expr.Arms[i].Body)
		}
		expr.Type = l.resolveDeep(expr.Type)
		return expr
	case IRIfExpr:
		expr.Cond = l.resolveExprTypes(expr.Cond)
		expr.Then = l.resolveExprTypes(expr.Then)
		expr.Else = l.resolveExprTypes(expr.Else)
		expr.Type = l.resolveDeep(expr.Type)
		return expr
	case IRListLit:
		resolved := l.resolveDeep(expr.Type)
		if lt, ok := resolved.(IRListType); ok {
			expr.ElemType = irTypeEmitStr(lt.Elem)
		}
		expr.Type = resolved
		l.resolveExprs(expr.Elements)
		return expr
	case IRFnCall:
		l.resolveExprs(expr.Args)
		return expr
	case IRMethodCall:
		expr.Receiver = l.resolveExprTypes(expr.Receiver)
		l.resolveExprs(expr.Args)
		return expr
	case IRBinaryExpr:
		expr.Left = l.resolveExprTypes(expr.Left)
		expr.Right = l.resolveExprTypes(expr.Right)
		expr.Type = l.resolveDeep(expr.Type)
		return expr
	case IRFn:
		expr.Body = l.resolveExprTypes(expr.Body)
		return expr
	case IRTryBlock:
		for i, stmt := range expr.Stmts {
			expr.Stmts[i] = l.resolveStmtTypes(stmt)
		}
		expr.Expr = l.resolveExprTypes(expr.Expr)
		expr.OkType = l.resolveDeep(expr.OkType)
		expr.ErrType = l.resolveDeep(expr.ErrType)
		return expr
	default:
		return e
	}
}

func (l *Lowerer) resolveStmtTypes(s IRStmt) IRStmt {
	switch stmt := s.(type) {
	case IRLetStmt:
		stmt.Value = l.resolveExprTypes(stmt.Value)
		// For empty lists with inferred type, set Type so emit generates `var x []T`
		if stmt.Type == nil {
			if ll, ok := stmt.Value.(IRListLit); ok && len(ll.Elements) == 0 && ll.Spread == nil {
				if lt, ok := ll.Type.(IRListType); ok {
					if _, isTV := lt.Elem.(IRTypeVar); !isTV {
						stmt.Type = ll.Type
					}
				}
			}
		}
		// An unresolved HM type variable in a generic-call RHS means the
		// type parameter could not be inferred. Report it here instead of
		// letting `interface{}` flow to Go, which would surface downstream
		// as confusing method or field errors on `interface{}`. Skipped
		// when the user provided an explicit annotation or when the RHS is
		// a bare collection literal (empty `[]` / `{}` are allowed to
		// default to interface element types).
		if stmt.GoName != "_" && stmt.Type == nil && isGenericCall(stmt.Value) {
			resolved := l.resolveDeep(stmt.Value.irType())
			if containsUnresolvedTypeVar(resolved) {
				l.addCompileError(ErrCannotInferTypeParam, stmt.Pos, CannotInferTypeParamData{
					Binding:    stmt.GoName,
					Suggestion: callFuncName(stmt.Value),
				})
			}
		}
		return stmt
	case IRExprStmt:
		stmt.Expr = l.resolveExprTypes(stmt.Expr)
		return stmt
	case IRTryLetStmt:
		stmt.CallExpr = l.resolveExprTypes(stmt.CallExpr)
		if stmt.ReturnType != nil {
			stmt.ReturnType = l.resolveDeep(stmt.ReturnType)
		}
		// Same unresolved-type-var check as IRLetStmt: `let x = f()?` where
		// f's generic T can't be inferred would otherwise hand `x` to the
		// rest of the function with an unresolved type. Try always wraps a
		// call, so no literal-value exception is needed here.
		if stmt.GoName != "_" && isGenericCall(stmt.CallExpr) {
			unwrapped := l.resolveDeep(stmt.CallExpr.irType())
			if rt, ok := unwrapped.(IRResultType); ok {
				unwrapped = rt.Ok
			}
			if containsUnresolvedTypeVar(unwrapped) {
				pos := callExprPos(stmt.CallExpr)
				l.addCompileError(ErrCannotInferTypeParam, pos, CannotInferTypeParamData{
					Binding:    stmt.GoName,
					Suggestion: callFuncName(stmt.CallExpr),
				})
			}
		}
		return stmt
	default:
		return s
	}
}

// containsUnresolvedTypeVar reports whether an IR type (after resolveDeep)
// still contains an HM type variable anywhere. Used to detect let bindings
// whose generic type parameter was never pinned down.
func containsUnresolvedTypeVar(t IRType) bool {
	switch tt := t.(type) {
	case IRTypeVar:
		return true
	case IRPointerType:
		return containsUnresolvedTypeVar(tt.Inner)
	case IRRefType:
		return containsUnresolvedTypeVar(tt.Inner)
	case IRListType:
		return containsUnresolvedTypeVar(tt.Elem)
	case IRMapType:
		return containsUnresolvedTypeVar(tt.Key) || containsUnresolvedTypeVar(tt.Value)
	case IROptionType:
		return containsUnresolvedTypeVar(tt.Inner)
	case IRResultType:
		return containsUnresolvedTypeVar(tt.Ok) || containsUnresolvedTypeVar(tt.Err)
	case IRTupleType:
		for _, e := range tt.Elements {
			if containsUnresolvedTypeVar(e) {
				return true
			}
		}
		return false
	case IRNamedType:
		for _, p := range tt.Params {
			if containsUnresolvedTypeVar(p) {
				return true
			}
		}
		return false
	}
	return false
}

// callFuncName extracts the function name from an IRFnCall (possibly wrapped
// in a block/if/etc.), for use in diagnostic suggestions.
func callFuncName(e IRExpr) string {
	switch expr := e.(type) {
	case IRFnCall:
		if ident, ok := expr.Fn.(IRIdent); ok {
			return ident.GoName
		}
		return "<call>"
	case IRMethodCall:
		return expr.Method
	}
	return "<call>"
}

// isGenericCall reports whether an expression is a direct function or method
// call — the only RHS kinds where an unresolved type variable legitimately
// points to an uninferrable generic type parameter. Literals like empty `[]`
// default to interface element types and must not trigger the check.
func isGenericCall(e IRExpr) bool {
	switch e.(type) {
	case IRFnCall, IRMethodCall:
		return true
	}
	return false
}

// callExprPos returns the source position of a call expression's origin.
// Falls back to zero Pos when the expression is not a recognized call.
func callExprPos(e IRExpr) Pos {
	if call, ok := e.(IRFnCall); ok {
		return call.Source.Pos
	}
	return Pos{}
}

func (l *Lowerer) addError(pos Pos, format string, args ...interface{}) {
	l.errors = append(l.errors, CompileError{
		Pos:   pos,
		Phase: "lower",
		Data:  MessageData{Text: fmt.Sprintf(format, args...)},
	})
}

func (l *Lowerer) addCompileError(code ErrorCode, pos Pos, data ErrorData) {
	l.errors = append(l.errors, CompileError{Code: code, Pos: pos, Phase: "lower", Data: data})
}

func (l *Lowerer) recordSymbol(name string, t Type, kind string) {
	l.symbols = append(l.symbols, SymbolInfo{Name: name, Type: t, Kind: kind})
}

// registerSymbol registers a data symbol (variable, parameter, binding) with both
// AST and IR types. All variable bindings must go through this function.
// Returns the resolved Go name (with shadowing suffix if needed).
func (l *Lowerer) registerSymbol(info SymbolRegInfo) string {
	sym := NewSymbolInfo(info.Name, info.Kind)

	// Same-scope shadowing: suffix with count
	if l.currentScope != nil {
		count := l.currentScope.declCount[sym.GoName]
		l.currentScope.declCount[sym.GoName] = count + 1
		if count > 0 {
			sym.GoName = fmt.Sprintf("%s_%d", sym.GoName, count+1)
		}
	}

	if info.ArcaType != nil {
		sym.Type = info.ArcaType
	}
	if info.IRType != nil {
		if _, isInterface := info.IRType.(IRInterfaceType); !isInterface {
			sym.IRType = info.IRType
		}
	}
	sym.Pos = info.Pos

	// Record in lexical scope
	if l.currentScope != nil {
		l.currentScope.Define(info.Name, &sym)
	}

	// Record in flat list (for LSP global queries)
	l.symbols = append(l.symbols, sym)

	return sym.GoName
}

type SymbolRegInfo struct {
	Name     string
	ArcaType Type   // nullable
	IRType   IRType // nullable
	Kind     string // SymVariable, SymParameter, etc.
	Pos      Pos    // definition position (for LSP go to definition)
}

func (l *Lowerer) withScope(startPos, endPos Pos, symbols []SymbolRegInfo, fn func()) {
	l.currentScope = NewScope(l.currentScope)
	l.currentScope.StartPos = startPos
	l.currentScope.EndPos = endPos
	defer func() { l.currentScope = l.currentScope.parent }()
	for _, s := range symbols {
		l.registerSymbol(s)
	}
	fn()
}

// LookupSymbol finds a symbol by name in the current lexical scope chain.
// Falls back to flat symbol list if no scope is active.
func (l *Lowerer) LookupSymbol(name string) *SymbolInfo {
	if l.currentScope != nil {
		return l.currentScope.Lookup(name)
	}
	// Fallback: flat search
	for i := len(l.symbols) - 1; i >= 0; i-- {
		if l.symbols[i].Name == name {
			return &l.symbols[i]
		}
	}
	return nil
}

// FindSymbolAt finds a symbol by name at a specific source position,
// using the scope tree to resolve lexical scoping.
func (l *Lowerer) FindSymbolAt(name string, pos Pos) *SymbolInfo {
	if l.rootScope != nil {
		scope := l.rootScope.FindScopeAt(pos)
		return scope.Lookup(name)
	}
	return l.LookupSymbol(name)
}

// Types returns the collected type declarations.
func (l *Lowerer) Types() map[string]TypeDecl { return l.types }

// TypeAliases returns the collected type alias declarations.
func (l *Lowerer) TypeAliases() map[string]TypeAliasDecl { return l.typeAliases }

// Functions returns the collected function declarations.
func (l *Lowerer) Functions() map[string]FnDecl { return l.functions }

// GoPackages returns the collected Go package imports.
func (l *Lowerer) GoPackages() map[string]*GoPackage { return l.goPackages }

// lookupGoPackage returns the Go package for a short name and marks it used.
// Use this instead of direct l.goPackages[name] access at resolution sites so
// unused-import diagnostics work correctly.
func (l *Lowerer) lookupGoPackage(name string) (*GoPackage, bool) {
	pkg, ok := l.goPackages[name]
	if ok {
		pkg.Used = true
	}
	return pkg, ok
}

// TypeResolver returns the type resolver for Go FFI lookups.
func (l *Lowerer) TypeResolver() TypeResolver { return l.typeResolver }

func (l *Lowerer) Errors() []CompileError {
	return l.errors
}

func NewLowerer(prog *Program, goModule string, resolver TypeResolver) *Lowerer {
	if resolver == nil {
		resolver = NullTypeResolver{}
	}
	root := NewScope(nil)
	l := &Lowerer{
		types:        make(map[string]TypeDecl),
		typeAliases:  make(map[string]TypeAliasDecl),
		traits:       make(map[string]TraitDecl),
		impls:        make(map[string][]ImplDecl),
		bindableTypes: make(map[string]bool),
		ctorTypes:    make(map[string]string),
		fnNames:      make(map[string]string),
		functions:    make(map[string]FnDecl),
		moduleNames:  make(map[string]bool),
		builtins:     make(map[string]bool),
		goModule:     goModule,
		typeResolver: resolver,
		rootScope:    root,
		currentScope: root,
	}
	registerPreludeTraits(l)
	registerPreludeBindable(l)
	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case TypeDecl:
			l.types[d.Name] = d
			for _, ctor := range d.Constructors {
				l.ctorTypes[ctor.Name] = d.Name
			}
		case TypeAliasDecl:
			l.typeAliases[d.Name] = d
		case ImportDecl:
			if strings.HasPrefix(d.Path, "go/") {
				pkg := NewGoPackage(d.Path[3:])
				pkg.Pos = d.Pos
				pkg.SideEffect = d.SideEffect
				if l.goPackages == nil {
					l.goPackages = make(map[string]*GoPackage)
				}
				l.goPackages[pkg.ShortName] = pkg
				l.registerSymbol(SymbolRegInfo{Name: pkg.ShortName, Kind: SymPackage, Pos: d.Pos})
				l.imports = append(l.imports, IRImport{
					Path:       pkg.FullPath,
					SideEffect: d.SideEffect,
				})
				if !isStdLib(pkg.FullPath) && !l.typeResolver.CanLoadPackage(pkg.FullPath) {
					l.addCompileError(ErrPackageNotFound, d.Pos, PackageNotFoundData{Path: pkg.FullPath})
				}
				break
			}
			// Arca built-in package: import stdlib, import stdlib.db, etc.
			rootName := strings.Split(d.Path, ".")[0]
			if pkg := lookupArcaPackage(rootName); pkg != nil {
				if l.goPackages == nil {
					l.goPackages = make(map[string]*GoPackage)
				}
				goPkg := NewGoPackage(pkg.GoModPath)
				goPkg.Pos = d.Pos
				l.goPackages[pkg.Name] = goPkg
				l.registerSymbol(SymbolRegInfo{Name: pkg.Name, Kind: SymPackage, Pos: d.Pos})
				l.imports = append(l.imports, IRImport{Path: pkg.GoModPath})
				break
			}
			// Arca module: import user, import user.{find}
			parts := strings.Split(d.Path, ".")
			l.moduleNames[parts[len(parts)-1]] = true
		case FnDecl:
			l.functions[d.Name] = d
			if d.Public {
				l.fnNames[d.Name] = snakeToPascal(d.Name)
			}
			l.registerSymbol(SymbolRegInfo{Name: d.Name, Kind: SymFunction, Pos: d.NamePos})
		case TraitDecl:
			l.traits[d.Name] = d
		case ImplDecl:
			l.impls[d.TypeName] = append(l.impls[d.TypeName], d)
		}
	}
	return l
}

// Lower converts the entire program.
// pkgName is the Go package name ("main" for main files).
// pubOnly limits function output to pub functions only (for same-dir module files).
func (l *Lowerer) Lower(prog *Program, pkgName string, pubOnly bool) IRProgram {
	var types []IRTypeDecl
	var funcs []IRFuncDecl

	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case TypeDecl:
			types = append(types, l.lowerTypeDecl(d))
			for _, method := range d.Methods {
				funcs = append(funcs, l.lowerMethod(d, method)...)
			}
		case TypeAliasDecl:
			types = append(types, l.lowerTypeAliasDecl(d))
		case FnDecl:
			if d.ReceiverType == "" {
				if pubOnly && !d.Public {
					continue
				}
				funcs = append(funcs, l.lowerFnDecl(d))
			}
		case TraitDecl:
			types = append(types, l.lowerTraitDecl(d))
		case ImplDecl:
			funcs = append(funcs, l.lowerImplDecl(d)...)
		}
	}

	// Error is the one trait that maps to Go's stdlib `error` interface
	// (see irTypeStr). No ArcaError interface is emitted — user code calls
	// trait methods via __goError wrap at match bindings / method-call sites.
	// The prelude-registered Error trait exists at the lowerer level only,
	// for type checking; it never surfaces as a Go type declaration.

	l.checkMethodCollisions()

	// Bindable Draft synthesis (B2b): for each `derive Bindable` host,
	// emit a `<TypeName>Draft` struct with BindableSlot-wrapped fields.
	types = append(types, l.synthesizeBindableTypes()...)

	// Expand sum type methods to per-variant implementations
	funcs = l.expandSumTypeMethods(funcs)

	// Stage 2 lowering: rewrite Result/Option dispatch + multi-receive let
	// shapes into Stage 2 IR (defined in go_ir.go) so emit becomes a
	// pretty-printer for those nodes. stage2 self-sufficiently expands
	// params, computes split names, generates ExpandedValues inline, and
	// blanks unused split slots — replacing the prior expandResultOption
	// pass entirely. The types pass drops dictionary-kind traits, which
	// have no Go-interface representation.
	types = stage2LowerTypes(types)
	funcs = stage2Lower(funcs)

	// Bindable dispatch synthesis (B2c): emits per-host __<Type>Freeze
	// function and __<Type>Bindable dictionary instance. Stage 2 IR is
	// produced directly so the freeze body can use GoIfElse / GoReturn
	// without going through stage2Lower again.
	dispatchFuncs, dispatchGlobals := l.synthesizeBindableDispatch()
	funcs = append(funcs, dispatchFuncs...)

	// Report unused imports (skip side-effect imports, which are intentional,
	// and packages consumed indirectly via auto-detected builtins like string
	// interpolation needing fmt). Sort for deterministic diagnostic order.
	var unusedNames []string
	for name, pkg := range l.goPackages {
		if pkg.SideEffect || pkg.Used || l.builtins[name] {
			continue
		}
		unusedNames = append(unusedNames, name)
	}
	sort.Strings(unusedNames)
	for _, name := range unusedNames {
		pkg := l.goPackages[name]
		l.addCompileError(ErrUnusedPackage, pkg.Pos, UnusedPackageData{Name: name})
	}

	// Collect builtin names
	var builtinNames []string
	for name := range l.builtins {
		builtinNames = append(builtinNames, name)
	}

	// Build final imports including auto-detected ones
	imports := make([]IRImport, len(l.imports))
	copy(imports, l.imports)
	if l.builtins["fmt"] && !l.hasImport("fmt") {
		imports = append(imports, IRImport{Path: "fmt"})
	}
	if l.builtins["regexp"] {
		imports = append(imports, IRImport{Path: "regexp"})
	}
	if l.builtins["os"] && !l.hasImport("os") {
		imports = append(imports, IRImport{Path: "os"})
	}
	if l.builtins["errors"] && !l.hasImport("errors") {
		imports = append(imports, IRImport{Path: "errors"})
	}

	return IRProgram{
		Package:  pkgName,
		Imports:  imports,
		Types:    types,
		Globals:  dispatchGlobals,
		Funcs:    funcs,
		Builtins: builtinNames,
	}
}

func (l *Lowerer) hasImport(pkg string) bool {
	for _, imp := range l.imports {
		if imp.Path == pkg {
			return true
		}
	}
	return false
}

// --- Type Declarations ---

// bindableStdlibFns lists Go FFI helpers whose first parameter is the
// hidden `stdlib.BindableDict[T, B]`. The compiler injects the user's
// derive-Bindable dictionary at the call site, so users only write
// `stdlib.Decode[Todo](data)` — they never pass the dict explicitly.
var bindableStdlibFns = map[string]bool{
	"stdlib.Decode":     true,
	"stdlib.BindJSON":   true,
	"stdlib.QueryAs":    true,
	"stdlib.QueryOneAs": true,
}

// bindableInjectKind tells the call-site lowerer how to handle a
// Bindable-stdlib helper call: nothing to do, inject the dict, or skip
// further validation because the type-argument check already failed.
type bindableInjectKind int

const (
	bindableInjectNone   bindableInjectKind = iota // not a Bindable stdlib call
	bindableInjectOK                               // dict + draft type ready to inject
	bindableInjectFailed                           // diagnostic already raised; suppress cascade
)

// maybeInjectBindableStdlibArg classifies a Go FFI call against the
// `bindableStdlibFns` helper list. When the user supplies a type argument
// declared with `derive Bindable`, the dispatch dictionary IRIdent and
// the corresponding Draft type are returned for injection. When the user
// supplies a type argument that is NOT bindable, ErrNonBindableTypeArg is
// raised here and the caller is expected to short-circuit so the missing
// dict doesn't surface as cascading arg-count / type-mismatch errors.
func (l *Lowerer) maybeInjectBindableStdlibArg(goCallName string, e FnCall) (IRExpr, Type, bindableInjectKind) {
	if !bindableStdlibFns[goCallName] || len(e.TypeArgs) < 1 {
		return nil, nil, bindableInjectNone
	}
	concrete, ok := e.TypeArgs[0].(NamedType)
	if !ok {
		l.addCompileError(ErrNonBindableTypeArg, e.Pos, NonBindableTypeArgData{
			FnName: goCallName, TypeName: irTypeDisplayStrFromAST(e.TypeArgs[0]),
		})
		return nil, nil, bindableInjectFailed
	}
	if !l.bindableTypes[concrete.Name] {
		l.addCompileError(ErrNonBindableTypeArg, e.Pos, NonBindableTypeArgData{
			FnName: goCallName, TypeName: concrete.Name,
		})
		return nil, nil, bindableInjectFailed
	}
	dictArg := IRIdent{
		GoName: "__" + concrete.Name + "Bindable",
		Type:   IRNamedType{GoName: "stdlib.BindableDict", Params: []IRType{IRNamedType{GoName: concrete.Name}, IRNamedType{GoName: concrete.Name + "Draft"}}},
	}
	return dictArg, NamedType{Name: concrete.Name + "Draft"}, bindableInjectOK
}

// irTypeDisplayStrFromAST renders an AST Type for diagnostic messages.
// Used by NonBindableTypeArgData when the type arg isn't a simple
// NamedType (e.g., a complex generic instantiation).
func irTypeDisplayStrFromAST(t Type) string {
	if nt, ok := t.(NamedType); ok {
		return nt.Name
	}
	return fmt.Sprintf("%v", t)
}

// substType walks an AST Type and substitutes any NamedType whose name is
// a key in subst with the substitution target. Used at generic-fn call
// sites (B2e) to specialise the callee's signature against explicit type
// args before lowering. Pure structural rewrite — no scope side effects.
func substType(t Type, subst map[string]Type) Type {
	if t == nil {
		return nil
	}
	switch tt := t.(type) {
	case NamedType:
		if rep, ok := subst[tt.Name]; ok && len(tt.Params) == 0 {
			return rep
		}
		params := make([]Type, len(tt.Params))
		for i, p := range tt.Params {
			params[i] = substType(p, subst)
		}
		return NamedType{Pos: tt.Pos, Name: tt.Name, Params: params}
	case FunctionType:
		params := make([]Type, len(tt.Params))
		for i, p := range tt.Params {
			params[i] = substType(p, subst)
		}
		return FunctionType{Params: params, Ret: substType(tt.Ret, subst)}
	default:
		return t
	}
}

// intrinsicTraitNames lists trait names reserved for compiler synthesis;
// users cannot `impl` them and `derive` is the only way to associate one
// with a type. Bindable is the first member (decisions/ffi.md 2026-05-04
// refined). Cloneable / Hashable etc. may join later.
var intrinsicTraitNames = map[string]bool{
	"Bindable": true,
}

// validateDeriveList enforces the rules from decisions/ffi.md 2026-05-04
// refined Synthetic Builder for `derive Trait` clauses on a type:
// the trait must be a known compiler intrinsic, and `derive Bindable`
// records the type name in l.bindableTypes for later sub-slices (B2b+)
// to drive Draft / Dictionary synthesis. Unknown derive targets (anything
// other than Bindable in MVP) and shapes outside Bindable's MVP scope
// (sum types — Q10 deferred to B5) are rejected.
func (l *Lowerer) validateDeriveList(td TypeDecl) {
	for _, d := range td.Derives {
		if !intrinsicTraitNames[d.Name] {
			l.addCompileError(ErrUnsupportedFeature, d.Pos, UnsupportedFeatureData{
				Feature: fmt.Sprintf("derive %s", d.Name),
				Context: "MVP supports `derive Bindable` only",
			})
			continue
		}
		if d.Name == "Bindable" {
			if len(td.Constructors) != 1 {
				l.addCompileError(ErrUnsupportedFeature, d.Pos, UnsupportedFeatureData{
					Feature: "derive Bindable on sum type",
					Context: "MVP supports product (single-constructor) types only; sum types deferred to B5",
				})
				continue
			}
			l.bindableTypes[td.Name] = true
		}
	}
}

// synthesizeBindableTypes generates the prelude `BindableSlot[T any]` struct
// plus a `<TypeName>Draft` IRStructDecl for each host type recorded in
// l.bindableTypes (set by validateDeriveList). Each Draft mirrors the host's
// fields with field types wrapped in BindableSlot[Inner]. Output is normal
// IRStructDecl so emit stays a pretty-printer — no bindable-specific branch
// in emit.go.
//
// Per decisions/ffi.md 2026-05-04 refined Synthetic Builder, this is B2b:
// type synthesis only — factory (`Todo.draft()`), freeze, and dictionary
// dispatch land in B2c through B2f.
func (l *Lowerer) synthesizeBindableTypes() []IRTypeDecl {
	if len(l.bindableTypes) == 0 {
		return nil
	}
	names := make([]string, 0, len(l.bindableTypes))
	for name := range l.bindableTypes {
		names = append(names, name)
	}
	sort.Strings(names)

	// BindableSlot and BindableDict live in the stdlib package
	// (`stdlib/bindable.go`), so synthesis emits only the per-host Draft
	// struct and references the shared types via `stdlib.<Name>`. Auto-
	// register stdlib so emit's import block + receiver-type lookup pick
	// it up (B3a).
	l.ensureStdlibImported()

	var out []IRTypeDecl
	for _, name := range names {
		td, ok := l.types[name]
		if !ok {
			continue
		}
		ctor := td.Constructors[0]
		fields := make([]IRFieldDecl, len(ctor.Fields))
		for i, f := range ctor.Fields {
			fields[i] = IRFieldDecl{
				GoName: capitalize(f.Name),
				Type:   IRNamedType{GoName: "stdlib.BindableSlot", Params: []IRType{l.lowerType(f.Type)}},
				Tag:    l.genStructTagFromRules(f.Name, td.Tags),
			}
		}
		out = append(out, IRStructDecl{
			GoName:     td.Name + "Draft",
			TypeParams: td.Params,
			Fields:     fields,
			Tags:       td.Tags,
		})
	}
	return out
}

// ensureStdlibImported registers the Arca built-in stdlib package + its Go
// import path so emit picks them up. Idempotent.
func (l *Lowerer) ensureStdlibImported() {
	if _, ok := l.goPackages["stdlib"]; ok {
		return
	}
	pkg := lookupArcaPackage("stdlib")
	if pkg == nil {
		return
	}
	if l.goPackages == nil {
		l.goPackages = make(map[string]*GoPackage)
	}
	goPkg := NewGoPackage(pkg.GoModPath)
	goPkg.Used = true
	l.goPackages[pkg.Name] = goPkg
	l.imports = append(l.imports, IRImport{Path: pkg.GoModPath})
}

// synthesizeBindableDispatch generates the per-host BindableDict instance
// (`__<TypeName>Bindable`) and its standalone Freeze function
// (`__<TypeName>Freeze`). Runs after stage2Lower so the freeze body can use
// Stage 2 IR (GoIfElse / GoReturn) directly. Per decisions/ffi.md
// 2026-05-04 refined Synthetic Builder, this is B2c — the Draft factory and
// d.freeze() user surface land in B2f.
func (l *Lowerer) synthesizeBindableDispatch() ([]IRFn, []IRGlobalVar) {
	if len(l.bindableTypes) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(l.bindableTypes))
	for name := range l.bindableTypes {
		names = append(names, name)
	}
	sort.Strings(names)

	var funcs []IRFn
	var globals []IRGlobalVar
	boolType := IRNamedType{GoName: "bool"}
	stringType := IRNamedType{GoName: "string"}
	errorType := IRNamedType{GoName: "error"}

	for _, name := range names {
		td, ok := l.types[name]
		if !ok {
			continue
		}
		ctor := td.Constructors[0]
		hostType := IRNamedType{GoName: name}
		draftType := IRNamedType{GoName: name + "Draft"}
		draftIdent := IRIdent{GoName: "d", Type: draftType}

		var stmts []IRStmt
		for _, f := range ctor.Fields {
			goField := capitalize(f.Name)
			slot := IRFieldAccess{Expr: draftIdent, Field: goField}
			cond := IRBinaryExpr{
				Op:    "==",
				Left:  IRFieldAccess{Expr: slot, Field: "Set", Type: boolType},
				Right: IRBoolLit{Value: false, Type: boolType},
				Type:  boolType,
			}
			errCall := IRFnCall{
				Fn:   IRIdent{GoName: "errors.New"},
				Args: []IRExpr{IRStringLit{Value: fmt.Sprintf("%s.%s is unset", name, f.Name), Type: stringType}},
				Type: errorType,
			}
			stmts = append(stmts, GoIfElse{
				Cond: cond,
				Then: GoBlock{Stmts: []IRStmt{
					GoReturn{Values: []IRExpr{
						IRConstructorCall{GoName: name, Type: hostType},
						errCall,
					}},
				}},
			})
		}

		var successValues []IRExpr
		if l.hasConstraints(td) {
			args := make([]IRFieldValue, len(ctor.Fields))
			for i, f := range ctor.Fields {
				slot := IRFieldAccess{Expr: draftIdent, Field: capitalize(f.Name)}
				args[i] = IRFieldValue{Value: IRFieldAccess{Expr: slot, Field: "Value"}}
			}
			// Constrained type → call the validating `New<Type>` constructor
			// (matches the convention in lowerUserConstructorCall for direct
			// user-written calls).
			successValues = []IRExpr{IRConstructorCall{
				GoName:        "New" + name,
				Fields:        args,
				GoMultiReturn: true,
				Type:          hostType,
			}}
		} else {
			fields := make([]IRFieldValue, len(ctor.Fields))
			for i, f := range ctor.Fields {
				slot := IRFieldAccess{Expr: draftIdent, Field: capitalize(f.Name)}
				fields[i] = IRFieldValue{
					GoName: capitalize(f.Name),
					Value:  IRFieldAccess{Expr: slot, Field: "Value"},
				}
			}
			successValues = []IRExpr{
				IRConstructorCall{GoName: name, Fields: fields, Type: hostType},
				IRIdent{GoName: "nil"},
			}
		}
		stmts = append(stmts, GoReturn{Values: successValues})

		// `(d <Type>Draft) Freeze` — user-facing inherent method (B2f)
		// that also serves as the dictionary's Freeze function pointer via
		// a Go method expression `<Type>Draft.Freeze`.
		funcs = append(funcs, IRFn{
			GoName:   "Freeze",
			Receiver: &IRReceiver{GoName: "d", Type: name + "Draft"},
			Ret:      IRResultType{Ok: hostType, Err: errorType},
			Body:     IRBlock{Stmts: stmts},
		})

		// `<typeName>Draft()` — user-facing factory (B2f) returning an
		// empty Draft. Also referenced by the dictionary's Draft field.
		factoryName := strings.ToLower(name[:1]) + name[1:] + "Draft"
		funcs = append(funcs, IRFn{
			GoName: factoryName,
			Ret:    draftType,
			Body: IRBlock{Stmts: []IRStmt{
				GoReturn{Values: []IRExpr{IRConstructorCall{GoName: name + "Draft", Type: draftType}}},
			}},
		})

		dictType := IRNamedType{GoName: "stdlib.BindableDict", Params: []IRType{hostType, draftType}}
		globals = append(globals, IRGlobalVar{
			GoName: "__" + name + "Bindable",
			Init: IRConstructorCall{
				GoName:   "stdlib.BindableDict",
				TypeArgs: fmt.Sprintf("[%s, %s]", name, name+"Draft"),
				Fields: []IRFieldValue{
					{GoName: "Draft", Value: IRIdent{GoName: factoryName}},
					{GoName: "Freeze", Value: IRIdent{GoName: name + "Draft.Freeze"}},
				},
				Type: dictType,
			},
		})
	}

	if len(funcs) > 0 {
		l.builtins["errors"] = true
	}
	return funcs, globals
}

func (l *Lowerer) lowerTypeDecl(td TypeDecl) IRTypeDecl {
	// Set currentTypeName so isTypeParam resolves generic params (e.g. A, B).
	prev := l.currentTypeName
	l.currentTypeName = td.Name
	defer func() { l.currentTypeName = prev }()

	l.validateDeriveList(td)

	// Check field types exist in all constructors
	for _, ctor := range td.Constructors {
		for _, f := range ctor.Fields {
			l.checkTypeExists(f.Type)
		}
	}
	// Check method signature types
	for _, m := range td.Methods {
		for _, p := range m.Params {
			if p.Type != nil {
				l.checkTypeExists(p.Type)
			}
		}
		if m.ReturnType != nil {
			l.checkTypeExists(m.ReturnType)
		}
	}

	if isEnum(td) {
		return l.lowerEnumDecl(td)
	}
	if len(td.Constructors) == 1 {
		return l.lowerStructDecl(td)
	}
	return l.lowerSumTypeDecl(td)
}

func (l *Lowerer) lowerEnumDecl(td TypeDecl) IREnumDecl {
	variants := make([]string, len(td.Constructors))
	for i, c := range td.Constructors {
		variants[i] = c.Name
	}
	return IREnumDecl{
		GoName:   td.Name,
		Variants: variants,
	}
}

func (l *Lowerer) lowerStructDecl(td TypeDecl) IRStructDecl {
	ctor := td.Constructors[0]
	fields := make([]IRFieldDecl, len(ctor.Fields))
	for i, f := range ctor.Fields {
		tag := l.genStructTagFromRules(f.Name, td.Tags)
		fields[i] = IRFieldDecl{
			GoName: capitalize(f.Name),
			Type:   l.lowerType(f.Type),
			Tag:    tag,
		}
	}

	var validator *IRValidator
	if l.hasConstraints(td) {
		validator = l.buildStructValidator(td)
	}

	return IRStructDecl{
		GoName:     td.Name,
		TypeParams: td.Params,
		Fields:     fields,
		Tags:       td.Tags,
		Validator:  validator,
	}
}

func (l *Lowerer) lowerSumTypeDecl(td TypeDecl) IRSumTypeDecl {
	variants := make([]IRVariantDecl, len(td.Constructors))
	for i, c := range td.Constructors {
		fields := make([]IRFieldDecl, len(c.Fields))
		for j, f := range c.Fields {
			fields[j] = IRFieldDecl{
				GoName: capitalize(f.Name),
				Type:   l.lowerType(f.Type),
			}
		}
		variants[i] = IRVariantDecl{
			GoName: td.Name + c.Name,
			Fields: fields,
		}
	}
	// Collect method signatures for interface definition
	var ifaceMethods []IRInterfaceMethod
	for _, m := range td.Methods {
		if !m.Static {
			name := snakeToCamel(m.Name)
			if m.Public {
				name = snakeToPascal(m.Name)
			}
			var retType IRType
			if m.ReturnType != nil {
				retType = l.lowerType(m.ReturnType)
			}
			ifaceMethods = append(ifaceMethods, IRInterfaceMethod{
				Name:       name,
				Params:     l.lowerParams(m.Params),
				ReturnType: retType,
			})
		}
	}

	return IRSumTypeDecl{
		GoName:           td.Name,
		TypeParams:       td.Params,
		Variants:         variants,
		InterfaceMethods: ifaceMethods,
	}
}

func (l *Lowerer) lowerTypeAliasDecl(d TypeAliasDecl) IRTypeAliasDecl {
	nt, ok := d.Type.(NamedType)
	if !ok {
		return IRTypeAliasDecl{GoName: d.Name, GoBase: "interface{}"}
	}
	goBase := irTypeEmitStr(l.lowerType(NamedType{Name: nt.Name, Params: nt.Params}))

	var validator *IRValidator
	if len(nt.Constraints) > 0 {
		validator = l.buildAliasValidator(d.Name, goBase, nt.Constraints)
	}

	return IRTypeAliasDecl{
		GoName:    d.Name,
		GoBase:    goBase,
		Validator: validator,
	}
}

// --- Validators ---

func (l *Lowerer) hasConstraints(td TypeDecl) bool {
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

func (l *Lowerer) buildStructValidator(td TypeDecl) *IRValidator {
	ctor := td.Constructors[0]
	var checks []IRValidationCheck
	for _, f := range ctor.Fields {
		nt, ok := f.Type.(NamedType)
		if !ok || len(nt.Constraints) == 0 {
			continue
		}
		fieldVar := snakeToCamel(f.Name)
		for _, c := range nt.Constraints {
			checks = append(checks, IRValidationCheck{
				Kind:     c.Key,
				Field:    fieldVar,
				Value:    l.constExprStr(c.Value),
				ZeroVal:  td.Name + "{}",
				TypeName: f.Name,
			})
		}
	}
	if len(checks) == 0 {
		return nil
	}
	l.builtins["fmt"] = true
	return &IRValidator{Checks: checks}
}

func (l *Lowerer) buildAliasValidator(typeName, goBase string, constraints []Constraint) *IRValidator {
	zeroVal := typeZeroValue(typeName, goBase)
	var checks []IRValidationCheck
	for _, c := range constraints {
		if c.Key == "pattern" {
			l.builtins["regexp"] = true
		}
		checks = append(checks, IRValidationCheck{
			Kind:     c.Key,
			Field:    "v",
			Value:    l.constExprStr(c.Value),
			ZeroVal:  zeroVal,
			TypeName: typeName,
		})
	}
	if len(checks) == 0 {
		return nil
	}
	l.builtins["fmt"] = true
	return &IRValidator{Checks: checks}
}

// constExprStr renders a constraint value expression as a Go string.
func (l *Lowerer) constExprStr(expr Expr) string {
	switch e := expr.(type) {
	case IntLit:
		return fmt.Sprintf("%d", e.Value)
	case FloatLit:
		return fmt.Sprintf("%g", e.Value)
	case StringLit:
		return fmt.Sprintf("%q", e.Value)
	case BoolLit:
		if e.Value {
			return "true"
		}
		return "false"
	case Ident:
		return e.Name
	default:
		return "/* unknown constraint value */"
	}
}

// --- Struct Tags ---

func (l *Lowerer) genStructTagFromRules(fieldName string, rules []TagRule) string {
	if len(rules) == 0 {
		return ""
	}
	var tags []string
	for _, rule := range rules {
		if val, ok := rule.Overrides[fieldName]; ok {
			tags = append(tags, fmt.Sprintf("%s:%q", rule.Name, val))
			continue
		}
		if rule.Case == "" && len(rule.Overrides) > 0 {
			continue
		}
		tagValue := fieldName
		switch rule.Case {
		case "snake":
			tagValue = camelToSnake(fieldName)
		case "kebab":
			tagValue = camelToKebab(fieldName)
		}
		tags = append(tags, fmt.Sprintf("%s:%q", rule.Name, tagValue))
	}
	if len(tags) == 0 {
		return ""
	}
	return "`" + strings.Join(tags, " ") + "`"
}

// --- Functions ---

// loweredFn holds the common lowered parts of a function declaration.
type loweredFn struct {
	params     []IRParamDecl
	retType    IRType
	body       IRExpr
	typeParams []string // expanded Go type-param list (includes hidden Draft params for Bindable)
}

// lowerFnCommon lowers the signature and body of a function-like declaration,
// managing per-function state (currentRetType, currentReceiver, currentTypeName,
// currentFnTypeParams, lexical scope, type inference scope). B2d validates
// any `[T: Bindable]` constraint annotations: only the Bindable intrinsic is
// accepted in MVP, anything else surfaces as ErrUnsupportedFeature.
func (l *Lowerer) lowerFnCommon(fd FnDecl, typeName, receiver string) loweredFn {
	for _, tp := range fd.TypeParams {
		if tp.Constraint == "" {
			continue
		}
		if !intrinsicTraitNames[tp.Constraint] {
			l.addCompileError(ErrUnsupportedFeature, tp.Pos, UnsupportedFeatureData{
				Feature: fmt.Sprintf("type-param constraint %s", tp.Constraint),
				Context: "MVP supports `: Bindable` only",
			})
		}
	}

	prevRet := l.currentRetType
	prevRecv := l.currentReceiver
	prevType := l.currentTypeName
	prevTypeParams := l.currentFnTypeParams
	prevBindable := l.currentFnBindableParams

	l.currentRetType = fd.ReturnType
	l.currentReceiver = receiver
	if typeName != "" {
		l.currentTypeName = typeName
	}

	// B2e: expand the type-param list with hidden Draft type params and
	// build hidden BindableDict value params for each Bindable-constrained
	// type param. `[T: Bindable]` becomes Go `[T any, __draftT any]` plus
	// a leading `__bindableT BindableDict[T, __draftT]` value param.
	tpNames := make([]string, 0, len(fd.TypeParams)*2)
	bindable := make(map[string]bool)
	var hiddenParams []FnParam
	for _, tp := range fd.TypeParams {
		tpNames = append(tpNames, tp.Name)
		if tp.Constraint == "Bindable" {
			bindable[tp.Name] = true
			draftName := "__draft" + tp.Name
			tpNames = append(tpNames, draftName)
			hiddenParams = append(hiddenParams, FnParam{
				Pos:  tp.Pos,
				Name: "__bindable" + tp.Name,
				Type: NamedType{Name: "stdlib.BindableDict", Params: []Type{
					NamedType{Name: tp.Name},
					NamedType{Name: draftName},
				}},
			})
		}
	}
	l.currentFnTypeParams = tpNames
	l.currentFnBindableParams = bindable
	expandedFnParams := append(hiddenParams, fd.Params...)

	// Check parameter and return types exist (after type-params are in scope
	// so signatures referencing T resolve as type-vars rather than unknowns).
	for _, p := range expandedFnParams {
		if p.Type != nil {
			l.checkTypeExists(p.Type)
		}
	}
	if fd.ReturnType != nil {
		l.checkTypeExists(fd.ReturnType)
	}

	params := l.lowerParams(expandedFnParams)
	var retType IRType
	if fd.ReturnType != nil {
		retType = l.lowerType(fd.ReturnType)
	}

	// Scope spans from the function declaration start (so parameters are
	// hover-able in the signature) through the body end.
	_, ep := bodyPos(fd.Body)
	sp := fd.Pos
	symbols := l.paramsToSymbols(expandedFnParams)
	// Method body: register `self` as a symbol with the receiver type
	if receiver != "" && typeName != "" {
		symbols = append(symbols, SymbolRegInfo{
			Name:     "self",
			ArcaType: NamedType{Name: typeName},
			IRType:   IRNamedType{GoName: typeName},
			Kind:     SymVariable,
		})
	}
	var body IRExpr
	l.withInferScope(func() {
		l.withScope(sp, ep, symbols, func() {
			body = l.lowerFnBody(fd.Body, fd.ReturnType != nil)
		})
		body = l.resolveExprTypes(body)
	})

	l.currentRetType = prevRet
	l.currentReceiver = prevRecv
	l.currentTypeName = prevType
	l.currentFnTypeParams = prevTypeParams
	l.currentFnBindableParams = prevBindable

	return loweredFn{params: params, retType: retType, body: body, typeParams: tpNames}
}

func (l *Lowerer) lowerFnDecl(fd FnDecl) IRFuncDecl {
	name := fd.Name
	if fd.Public {
		name = snakeToPascal(name)
	}

	lf := l.lowerFnCommon(fd, "", "")

	// `fun main() -> Result[_, _]` is lowered normally; emit wraps it in
	// an IIFE that prints Err to stderr and exits. Register the imports
	// the wrapper needs so they land in the output.
	if fd.Name == "main" {
		if _, ok := lf.retType.(IRResultType); ok {
			l.builtins["fmt"] = true
			l.builtins["os"] = true
		}
	}

	return IRFuncDecl{
		GoName:     name,
		TypeParams: lf.typeParams,
		Params:     lf.params,
		Ret:        lf.retType,
		Body:       lf.body,
		Source:     SourceInfo{Pos: fd.Pos, Name: fd.Name, ReturnType: fd.ReturnType},
	}
}

// traitGoName maps an Arca trait name to its emitted Go interface name.
// Arca-prefixed to avoid colliding with Go stdlib types (Error, Reader, ...).
func traitGoName(name string) string { return "Arca" + name }

// registerPreludeTraits seeds built-in traits visible in every file without
// import or declaration. Currently just `trait Error { fun message() -> String }`.
// The IRTraitDecl for Error is emitted only when the file actually references
// the trait (gated via l.builtins["error_trait"]).
// registerPreludeBindable seeds the Arca-side TypeDecl for BindableDict so
// field access on `__bindableT` (the hidden param injected by B2e for
// `[T: Bindable]`-constrained functions) resolves through the normal
// lower path. The IR-level BindableDict is emitted separately by
// synthesizeBindableTypes — these two views must stay structurally
// aligned: same type-param order (T, B), same field names + signatures.
func registerPreludeBindable(l *Lowerer) {
	resultTB := NamedType{Name: "Result", Params: []Type{NamedType{Name: "T"}, NamedType{Name: "Error"}}}
	l.types["BindableDict"] = TypeDecl{
		Name:   "BindableDict",
		Params: []string{"T", "B"},
		Constructors: []Constructor{{
			Name: "BindableDict",
			Fields: []Field{
				{Name: "draft", Type: FunctionType{Ret: NamedType{Name: "B"}}},
				{Name: "freeze", Type: FunctionType{Params: []Type{NamedType{Name: "B"}}, Ret: resultTB}},
			},
		}},
	}
}

func registerPreludeTraits(l *Lowerer) {
	l.traits["Error"] = TraitDecl{
		Name: "Error",
		Methods: []FnDecl{
			{
				Name:         "message",
				ReceiverType: "Error",
				ReturnType:   NamedType{Name: "String"},
			},
		},
	}
}

// analyzeTraitObjectSafety classifies a trait by Rust-style object-safety
// rules. A trait is object-safe (TraitKindVtable) when every method:
//   - is non-static (uses an implicit `self` receiver), and
//   - mentions `Self` only via the receiver — never in a parameter type,
//     return type, or nested type argument.
//
// Otherwise the trait is TraitKindDictionary. stage2LowerTypes drops
// dictionary IRTraitDecl nodes before emit (B1b), and lowerNamedType /
// lowerImplDecl reject usage of such traits with ErrUnsupportedFeature
// until B2 lands the dictionary-struct emission.
func analyzeTraitObjectSafety(d TraitDecl) TraitKind {
	if len(d.AssocTypes) > 0 {
		return TraitKindDictionary
	}
	for _, m := range d.Methods {
		if m.Static {
			return TraitKindDictionary
		}
		if typeContainsSelf(m.ReturnType) {
			return TraitKindDictionary
		}
		for _, p := range m.Params {
			if typeContainsSelf(p.Type) {
				return TraitKindDictionary
			}
		}
	}
	return TraitKindVtable
}

// typeContainsSelf reports whether the AST type tree mentions `Self`
// anywhere — top-level or as a generic parameter / pointer inner /
// tuple element / function param/ret / associated-type receiver.
func typeContainsSelf(t Type) bool {
	if t == nil {
		return false
	}
	switch x := t.(type) {
	case NamedType:
		if x.Name == "Self" {
			return true
		}
		for _, p := range x.Params {
			if typeContainsSelf(p) {
				return true
			}
		}
	case PointerType:
		return typeContainsSelf(x.Inner)
	case TupleType:
		for _, e := range x.Elements {
			if typeContainsSelf(e) {
				return true
			}
		}
	case FunctionType:
		for _, p := range x.Params {
			if typeContainsSelf(p) {
				return true
			}
		}
		return typeContainsSelf(x.Ret)
	case AssocTypeName:
		return x.Recv == "Self"
	}
	return false
}

// lowerTraitDecl emits the Go interface declaration for a trait.
func (l *Lowerer) lowerTraitDecl(d TraitDecl) IRTraitDecl {
	methods := make([]IRInterfaceMethod, 0, len(d.Methods))
	for _, m := range d.Methods {
		var retType IRType
		if m.ReturnType != nil {
			retType = l.lowerType(m.ReturnType)
		}
		methods = append(methods, IRInterfaceMethod{
			Name:       snakeToPascal(m.Name),
			Params:     l.lowerParams(m.Params),
			ReturnType: retType,
		})
	}
	return IRTraitDecl{
		GoName:  traitGoName(d.Name),
		Methods: methods,
		Kind:    analyzeTraitObjectSafety(d),
	}
}

// traitIsDictionary reports whether the named trait routes through dictionary
// dispatch (Self / static fun / associated type). Dictionary traits have no
// Go-interface representation; B1b rejects every usage site, B2 will replace
// the rejection with dictionary-struct emission.
func (l *Lowerer) traitIsDictionary(name string) bool {
	d, ok := l.traits[name]
	if !ok {
		return false
	}
	return analyzeTraitObjectSafety(d) == TraitKindDictionary
}

// lowerImplDecl emits impl methods as Go methods on the target concrete type.
// Go's structural interface satisfaction means no extra registration is
// needed on the Go side — the ArcaTrait interface and these methods share
// PascalCase names, so impl methods transparently satisfy the interface.
func (l *Lowerer) lowerImplDecl(d ImplDecl) []IRFuncDecl {
	td, ok := l.types[d.TypeName]
	if !ok {
		l.addCompileError(ErrUnknownType, d.Pos, UnknownTypeData{Name: d.TypeName})
		return nil
	}
	if intrinsicTraitNames[d.TraitName] {
		l.addCompileError(ErrUnsupportedFeature, d.Pos, UnsupportedFeatureData{
			Feature: fmt.Sprintf("impl of compiler-intrinsic trait %s", d.TraitName),
			Context: fmt.Sprintf("use `derive %s` on the type declaration", d.TraitName),
		})
		return nil
	}
	if _, ok := l.traits[d.TraitName]; !ok {
		l.addCompileError(ErrUnknownType, d.Pos, UnknownTypeData{Name: d.TraitName})
		return nil
	}
	if l.traitIsDictionary(d.TraitName) {
		l.addCompileError(ErrUnsupportedFeature, d.Pos, UnsupportedFeatureData{
			Feature: fmt.Sprintf("impl of constraint-only trait %s", d.TraitName),
			Context: "Phase 1 (dictionary dispatch lands in B2)",
		})
		return nil
	}
	if d.TraitName == "Error" {
		l.builtins["error_trait"] = true
	}
	var funcs []IRFuncDecl
	for _, method := range d.Methods {
		// Impl methods must be exported Go methods to satisfy the ArcaTrait
		// interface's exported method set. Mark as Public regardless of the
		// source annotation so lowerMethod picks snakeToPascal.
		method.Public = true
		funcs = append(funcs, l.lowerMethod(td, method)...)
	}
	// Auto-generate the Go `error` shim for impls of the built-in Error trait.
	// `fun error() -> String { self.message() }` lowers to an IRFuncDecl that
	// emits as `func (x X) Error() string { return x.Message() }`. Go's
	// structural interface means X now satisfies both ArcaError and the
	// stdlib `error` interface.
	if d.TraitName == "Error" {
		shim := FnDecl{
			Pos:     d.Pos,
			NamePos: d.Pos,
			Name:    "error",
			Public:  true,
			Params:  nil,
			ReturnType: NamedType{Name: "String"},
			Body: Block{
				Expr: FnCall{
					NodePos: AtPos(d.Pos),
					Fn: FieldAccess{
						NodePos: AtPos(d.Pos),
						Expr:    Ident{NodePos: AtPos(d.Pos), Name: "self"},
						Field:   "message",
					},
				},
			},
		}
		funcs = append(funcs, l.lowerMethod(td, shim)...)
	}
	return funcs
}

// checkMethodCollisions rejects any collision between inherent type methods
// and impl methods, and between two impls of different traits on the same
// type. Phase 1: no disambiguation syntax — any overlap is a compile error.
func (l *Lowerer) checkMethodCollisions() {
	for typeName, impls := range l.impls {
		// seen: method name → (kind, trait or type)
		type origin struct{ kind, source string }
		seen := map[string]origin{}
		if td, ok := l.types[typeName]; ok {
			for _, m := range td.Methods {
				seen[m.Name] = origin{kind: "type", source: typeName}
			}
		}
		for _, impl := range impls {
			for _, m := range impl.Methods {
				if prior, dup := seen[m.Name]; dup {
					l.addCompileError(ErrTraitMethodCollision, m.NamePos, TraitMethodCollisionData{
						TypeName:  typeName,
						Method:    m.Name,
						Prior:     prior.source,
						PriorKind: prior.kind,
						This:      impl.TraitName,
					})
					continue
				}
				seen[m.Name] = origin{kind: "impl", source: impl.TraitName}
			}
		}
	}
}

func (l *Lowerer) lowerMethod(td TypeDecl, fd FnDecl) []IRFuncDecl {
	if fd.Static {
		return []IRFuncDecl{l.lowerAssociatedFunc(td, fd)}
	}

	// Sum type methods: lower as normal method, expand to per-variant later

	methodName := snakeToCamel(fd.Name)
	if fd.Public {
		methodName = snakeToPascal(fd.Name)
	}

	receiver := strings.ToLower(td.Name[:1])
	lf := l.lowerFnCommon(fd, td.Name, receiver)

	return []IRFuncDecl{{
		GoName: methodName,
		Receiver: &IRReceiver{
			GoName: receiver,
			Type:   td.Name,
		},
		Params: lf.params,
		Ret:    lf.retType,
		Body:   lf.body,
		Source: SourceInfo{Pos: fd.Pos, Name: fd.Name, TypeName: td.Name, ReturnType: fd.ReturnType},
	}}
}

// expandSumTypeMethods transforms methods on sum types (interfaces) into
// per-variant method implementations. Operates on IR after lowering.
func (l *Lowerer) expandSumTypeMethods(funcs []IRFuncDecl) []IRFuncDecl {
	var result []IRFuncDecl
	for _, fn := range funcs {
		if fn.Receiver == nil {
			result = append(result, fn)
			continue
		}
		// Check if receiver type is a sum type
		td, isSumType := l.types[fn.Receiver.Type]
		if !isSumType || len(td.Constructors) <= 1 || isEnum(td) {
			result = append(result, fn)
			continue
		}
		// Expand to per-variant methods
		result = append(result, l.expandToVariants(fn, td)...)
	}
	return result
}

func (l *Lowerer) expandToVariants(fn IRFuncDecl, td TypeDecl) []IRFuncDecl {
	// Find match on self in body
	matchExpr := findIRMatchSelf(fn.Body, fn.Receiver.GoName)
	if matchExpr != nil {
		return l.expandMatchSelf(fn, td, matchExpr)
	}
	// No match self: duplicate body for each variant
	return l.duplicateForVariants(fn, td)
}

// findIRMatchSelf finds an IRMatch on the receiver variable in the IR body.
func findIRMatchSelf(body IRExpr, receiverName string) *IRMatch {
	switch e := body.(type) {
	case IRMatch:
		if ident, ok := e.Subject.(IRIdent); ok && ident.GoName == receiverName {
			return &e
		}
	case IRBlock:
		if e.Expr != nil {
			return findIRMatchSelf(e.Expr, receiverName)
		}
	}
	return nil
}

// expandMatchSelf splits a match-on-self method into per-variant methods,
// each with the corresponding arm's body.
func (l *Lowerer) expandMatchSelf(fn IRFuncDecl, td TypeDecl, m *IRMatch) []IRFuncDecl {
	var funcs []IRFuncDecl
	for _, arm := range m.Arms {
		p, ok := arm.Pattern.(IRSumTypePattern)
		if !ok {
			continue
		}
		// Build body: bind pattern fields from receiver, then arm body
		var stmts []IRStmt
		for _, b := range p.Bindings {
			stmts = append(stmts, IRLetStmt{
				GoName: b.GoName,
				Value:  IRFieldAccess{Expr: IRIdent{GoName: fn.Receiver.GoName}, Field: b.Source[1:]}, // strip leading "."
			})
		}
		var body IRExpr
		if len(stmts) > 0 {
			body = IRBlock{Stmts: stmts, Expr: arm.Body}
		} else {
			body = arm.Body
		}
		funcs = append(funcs, IRFuncDecl{
			GoName:   fn.GoName,
			Receiver: &IRReceiver{GoName: fn.Receiver.GoName, Type: p.GoType},
			Params:   fn.Params,
			Ret:      fn.Ret,
			Body:     body,
			Source:   fn.Source,
		})
	}
	return funcs
}

// duplicateForVariants creates identical method implementations for each variant.
func (l *Lowerer) duplicateForVariants(fn IRFuncDecl, td TypeDecl) []IRFuncDecl {
	var funcs []IRFuncDecl
	for _, ctor := range td.Constructors {
		variantName := td.Name + ctor.Name
		funcs = append(funcs, IRFuncDecl{
			GoName:   fn.GoName,
			Receiver: &IRReceiver{GoName: fn.Receiver.GoName, Type: variantName},
			Params:   fn.Params,
			Ret:      fn.Ret,
			Body:     fn.Body,
			Source:   fn.Source,
		})
	}
	return funcs
}

func (l *Lowerer) lowerAssociatedFunc(td TypeDecl, fd FnDecl) IRFuncDecl {
	funcName := td.Name + capitalize(fd.Name)
	if !fd.Public {
		funcName = strings.ToLower(td.Name[:1]) + td.Name[1:] + capitalize(fd.Name)
	}

	lf := l.lowerFnCommon(fd, td.Name, "")

	return IRFuncDecl{
		GoName: funcName,
		Params: lf.params,
		Ret:    lf.retType,
		Body:   lf.body,
		Source: SourceInfo{Pos: fd.Pos, Name: fd.Name, TypeName: td.Name, ReturnType: fd.ReturnType},
	}
}

func (l *Lowerer) lowerParams(params []FnParam) []IRParamDecl {
	result := make([]IRParamDecl, len(params))
	for i, p := range params {
		result[i] = IRParamDecl{
			GoName: snakeToCamel(p.Name),
			Type:   l.lowerType(p.Type),
			Source: SourceInfo{Type: p.Type},
		}
	}
	return result
}

func (l *Lowerer) lowerFnBody(body Expr, hasReturn bool) IRExpr {
	var hint IRType
	if hasReturn && l.currentRetType != nil {
		hint = l.lowerType(l.currentRetType)
	}
	expr := l.lowerExprHint(body, hint)
	if !hasReturn {
		expr = l.markVoidContext(expr)
	}
	return expr
}

// markVoidContext replaces trailing Unit in match arms when the result is not used.
// Applied to void function bodies and expression statements.
func (l *Lowerer) markVoidContext(expr IRExpr) IRExpr {
	if m, ok := expr.(IRMatch); ok {
		for i := range m.Arms {
			m.Arms[i].Body = replaceTrailingUnit(m.Arms[i].Body)
		}
		return m
	}
	if block, ok := expr.(IRBlock); ok && block.Expr != nil {
		block.Expr = l.markVoidContext(block.Expr)
		return block
	}
	return expr
}

// checkTypeHint checks if an expression's type matches the expected hint type.
func (l *Lowerer) checkTypeHint(result IRExpr, hint IRType, sourceExpr Expr) {
	l.checkTypeHintPos(result, hint, sourceExpr.exprPos())
}

// checkTypeHintPos is a thin wrapper routing hint-based type checks through
// HM unify. When a mismatch occurs, unify emits the diagnostic at pos
// directly; on success it records any substitutions, which feeds back into
// further inference. Single source of truth for type compatibility.
func (l *Lowerer) checkTypeHintPos(result IRExpr, hint IRType, pos Pos) {
	actualType := result.irType()
	if actualType == nil {
		return
	}
	// Unknown types on either side are intentionally permissive: the
	// lowerer uses IRInterfaceType as "unresolved" and there are paths
	// (prelude fn return types, default expressions) that produce it.
	if _, ok := actualType.(IRInterfaceType); ok {
		return
	}
	if _, ok := hint.(IRInterfaceType); ok {
		return
	}
	l.unify(actualType, hint, pos)
}

// isConstraintCompatible checks if source type alias is compatible with target type alias.
// e.g. AdultAge (min:18, max:150) → Age (min:0, max:150) is compatible.
func (l *Lowerer) isConstraintCompatible(sourceName, targetName string) bool {
	sourceAlias, sourceOk := l.typeAliases[sourceName]
	targetAlias, targetOk := l.typeAliases[targetName]
	if !sourceOk || !targetOk {
		return false
	}
	sourceNT, sourceIsNT := sourceAlias.Type.(NamedType)
	targetNT, targetIsNT := targetAlias.Type.(NamedType)
	if !sourceIsNT || !targetIsNT {
		return false
	}
	// Must be same base type
	if sourceNT.Name != targetNT.Name {
		return false
	}
	// Two different aliases with no constraints → nominal, not compatible
	if len(sourceNT.Constraints) == 0 && len(targetNT.Constraints) == 0 {
		return false
	}
	// Check constraint dimensions
	sDims := constraintsToDimensions(sourceNT.Constraints)
	tDims := constraintsToDimensions(targetNT.Constraints)
	return dimensionsCompatible(sDims, tDims)
}

// irTypeDisplayStr returns a human-readable type name for error messages.
// irTypeDisplayStr renders an IR type in Arca source-level syntax for
// user-facing error messages. Unlike irTypeEmitStr (which produces the
// underlying Go form like `Result_` / `struct{}`), this keeps the names the
// user wrote — `Result`, `Option`, `Unit`, `Int`, etc.
func irTypeDisplayStr(t IRType) string {
	switch tt := t.(type) {
	case IRNamedType:
		base := arcaDisplayName(tt.GoName)
		if len(tt.Params) == 0 {
			return base
		}
		params := make([]string, len(tt.Params))
		for i, p := range tt.Params {
			params[i] = irTypeDisplayStr(p)
		}
		return base + "[" + strings.Join(params, ", ") + "]"
	case IRPointerType:
		return "*" + irTypeDisplayStr(tt.Inner)
	case IRRefType:
		return "Ref[" + irTypeDisplayStr(tt.Inner) + "]"
	case IRResultType:
		return "Result[" + irTypeDisplayStr(tt.Ok) + ", " + irTypeDisplayStr(tt.Err) + "]"
	case IROptionType:
		return "Option[" + irTypeDisplayStr(tt.Inner) + "]"
	case IRListType:
		return "List[" + irTypeDisplayStr(tt.Elem) + "]"
	case IRMapType:
		return "Map[" + irTypeDisplayStr(tt.Key) + ", " + irTypeDisplayStr(tt.Value) + "]"
	case IRTupleType:
		elems := make([]string, len(tt.Elements))
		for i, e := range tt.Elements {
			elems[i] = irTypeDisplayStr(e)
		}
		return "(" + strings.Join(elems, ", ") + ")"
	case IRTypeVar:
		return "_"
	case IRInterfaceType:
		return "Any"
	case IRTraitType:
		return tt.Name
	case IRFnType:
		params := make([]string, len(tt.Params))
		for i, p := range tt.Params {
			params[i] = irTypeDisplayStr(p)
		}
		ret := irTypeDisplayStr(tt.Ret)
		if len(tt.Params) == 1 {
			// Bare form for single param, parens if the param is itself
			// a fn type so `(A -> B) -> C` is unambiguous.
			if _, isFn := tt.Params[0].(IRFnType); isFn {
				return "(" + params[0] + ") -> " + ret
			}
			return params[0] + " -> " + ret
		}
		return "(" + strings.Join(params, ", ") + ") -> " + ret
	}
	return "unknown"
}

// arcaDisplayName maps internal Go type names back to the Arca source name
// so error messages use the identifier the user actually wrote.
func arcaDisplayName(goName string) string {
	switch goName {
	case "struct{}":
		return "Unit"
	case "int":
		return "Int"
	case "uint":
		return "UInt"
	case "float64":
		return "Float"
	case "string":
		return "String"
	case "bool":
		return "Bool"
	}
	return goName
}

// checkMethodArgCount validates method call argument count against Go signature.
func (l *Lowerer) checkMethodArgCount(receiver IRExpr, method string, argCount int, pos Pos) {
	pkg, typ, ok := l.resolveReceiverGoType(receiver)
	if !ok {
		return
	}
	info := l.typeResolver.ResolveMethod(pkg, typ, method)
	if info == nil {
		return
	}
	minArgs := len(info.Params)
	if info.Variadic {
		minArgs--
	}
	if !info.Variadic && argCount != len(info.Params) {
		l.addCompileError(ErrWrongArgCount, pos, WrongArgCountData{Func: typ + "." + method, Expected: len(info.Params), Actual: argCount})
	} else if info.Variadic && argCount < minArgs {
		l.addCompileError(ErrWrongArgCount, pos, WrongArgCountData{Func: typ + "." + method, Expected: minArgs, Actual: argCount, AtLeast: true})
	}
}

// checkTypeExists reports ErrUnknownType if the AST type references an unknown name.
// Used in declaration contexts (type fields, function signatures) where unify
// cannot catch non-existent types.
func (l *Lowerer) checkTypeExists(t Type) {
	switch tt := t.(type) {
	case NamedType:
		if !l.isKnownTypeName(tt.Name) {
			l.addCompileError(ErrUnknownType, tt.Pos, UnknownTypeData{Name: tt.Name})
		}
		for _, p := range tt.Params {
			l.checkTypeExists(p)
		}
	case PointerType:
		l.checkTypeExists(tt.Inner)
	case TupleType:
		for _, e := range tt.Elements {
			l.checkTypeExists(e)
		}
	case FunctionType:
		for _, p := range tt.Params {
			l.checkTypeExists(p)
		}
		if tt.Ret != nil {
			l.checkTypeExists(tt.Ret)
		}
	}
}

// isKnownTypeName checks if a name is a known type in the lowerer context.
func (l *Lowerer) isKnownTypeName(name string) bool {
	switch name {
	case "Unit", "Int", "UInt", "Float", "String", "Bool", "List", "Map", "Option", "Result", "Ref", "Any", "Self":
		return true
	}
	if strings.Contains(name, ".") {
		return true
	}
	if _, ok := l.types[name]; ok {
		return true
	}
	if _, ok := l.typeAliases[name]; ok {
		return true
	}
	if _, ok := l.traits[name]; ok {
		return true
	}
	if l.isTypeParam(name) {
		return true
	}
	if l.moduleNames[name] {
		return true
	}
	return false
}

// isTypeParam checks if a name is a type parameter of the current type
// or the enclosing function. Type-level params come from the type's decl;
// function-level params come from `currentFnTypeParams` set by
// lowerFnCommon (B2d). External constructor / call sites never hit this
// path: lowerUserConstructorCall instantiates fresh type vars per call via
// instantiateGenericType.
func (l *Lowerer) isTypeParam(name string) bool {
	for _, p := range l.currentFnTypeParams {
		if p == name {
			return true
		}
	}
	if l.currentTypeName == "" {
		return false
	}
	td, ok := l.types[l.currentTypeName]
	if !ok {
		return false
	}
	for _, p := range td.Params {
		if p == name {
			return true
		}
	}
	return false
}

// instantiateGenericType creates a fresh IRTypeVar for each type parameter of
// a generic Arca type declaration, returning the name→var map. Mirrors
// instantiateGeneric for Go FFI: each constructor call site gets its own
// independent vars so two `Pair(...)` calls in the same function can bind to
// different argument types without sharing substitution entries.
func (l *Lowerer) instantiateGenericType(td TypeDecl) map[string]IRType {
	if len(td.Params) == 0 || l.infer == nil {
		return nil
	}
	vars := make(map[string]IRType, len(td.Params))
	for _, p := range td.Params {
		vars[p] = l.freshTypeVar()
	}
	return vars
}

// lowerTypeWithVars lowers an AST type while substituting type parameter
// references from the supplied vars map. Used by generic constructor calls
// so fresh per-call type vars flow into the constructor's field hints.
// Falls back to lowerType when vars is nil.
func (l *Lowerer) lowerTypeWithVars(t Type, vars map[string]IRType) IRType {
	if vars == nil || t == nil {
		return l.lowerType(t)
	}
	switch tt := t.(type) {
	case NamedType:
		if len(tt.Params) == 0 {
			if tv, ok := vars[tt.Name]; ok {
				return tv
			}
		}
		// Recurse into params for composite types like Option[A], List[A]
		if len(tt.Params) > 0 {
			params := make([]Type, len(tt.Params))
			for i, p := range tt.Params {
				// Substitute bare param refs; leave other types to lowerType.
				if n, ok := p.(NamedType); ok && len(n.Params) == 0 {
					if _, hit := vars[n.Name]; hit {
						// Keep as NamedType for now; lowerType handles the leaf
						// via a recursive call through the switch above.
					}
				}
				params[i] = p
			}
			// Lower the outer type normally, then patch substituted leaves.
			lowered := l.lowerType(NamedType{Name: tt.Name, Params: params})
			return substituteTypeVars(lowered, vars)
		}
		return l.lowerType(t)
	case PointerType:
		return IRPointerType{Inner: l.lowerTypeWithVars(tt.Inner, vars)}
	case TupleType:
		elems := make([]IRType, len(tt.Elements))
		for i, e := range tt.Elements {
			elems[i] = l.lowerTypeWithVars(e, vars)
		}
		return IRTupleType{Elements: elems}
	case FunctionType:
		params := make([]IRType, len(tt.Params))
		for i, p := range tt.Params {
			params[i] = l.lowerTypeWithVars(p, vars)
		}
		var ret IRType
		if tt.Ret != nil {
			ret = l.lowerTypeWithVars(tt.Ret, vars)
		}
		return IRFnType{Params: params, Ret: ret}
	default:
		return l.lowerType(t)
	}
}

// substituteTypeVars walks an IR type and replaces IRNamedType leaves whose
// name matches a key in vars with the mapped type var. Used after lowering a
// composite like Option[A] to patch A → TypeVar.
func substituteTypeVars(t IRType, vars map[string]IRType) IRType {
	switch tt := t.(type) {
	case IRNamedType:
		if len(tt.Params) == 0 {
			if tv, ok := vars[tt.GoName]; ok {
				return tv
			}
			return tt
		}
		params := make([]IRType, len(tt.Params))
		for i, p := range tt.Params {
			params[i] = substituteTypeVars(p, vars)
		}
		return IRNamedType{GoName: tt.GoName, Params: params}
	case IRPointerType:
		return IRPointerType{Inner: substituteTypeVars(tt.Inner, vars)}
	case IRRefType:
		return IRRefType{Inner: substituteTypeVars(tt.Inner, vars)}
	case IRListType:
		return IRListType{Elem: substituteTypeVars(tt.Elem, vars)}
	case IROptionType:
		return IROptionType{Inner: substituteTypeVars(tt.Inner, vars)}
	case IRResultType:
		return IRResultType{Ok: substituteTypeVars(tt.Ok, vars), Err: substituteTypeVars(tt.Err, vars)}
	case IRMapType:
		return IRMapType{Key: substituteTypeVars(tt.Key, vars), Value: substituteTypeVars(tt.Value, vars)}
	case IRTupleType:
		elems := make([]IRType, len(tt.Elements))
		for i, e := range tt.Elements {
			elems[i] = substituteTypeVars(e, vars)
		}
		return IRTupleType{Elements: elems}
	}
	return t
}

// irTypeEmitStr returns a Go type string for an IRType (used for type args).
func irTypeEmitStr(t IRType) string {
	switch tt := t.(type) {
	case IRNamedType:
		return tt.GoName
	case IRPointerType:
		return "*" + irTypeEmitStr(tt.Inner)
	case IRRefType:
		return "*" + irTypeEmitStr(tt.Inner)
	case IRResultType:
		return "(" + irTypeEmitStr(tt.Ok) + ", error)"
	case IROptionType:
		return "(" + irTypeEmitStr(tt.Inner) + ", bool)"
	case IRListType:
		return "[]" + irTypeEmitStr(tt.Elem)
	case IRMapType:
		return "map[" + irTypeEmitStr(tt.Key) + "]" + irTypeEmitStr(tt.Value)
	case IRTupleType:
		if len(tt.Elements) == 2 {
			return fmt.Sprintf("struct{ First %s; Second %s }", irTypeEmitStr(tt.Elements[0]), irTypeEmitStr(tt.Elements[1]))
		}
		return "interface{}"
	case IRTraitType:
		if tt.Name == "Error" {
			return "error"
		}
		return traitGoName(tt.Name)
	case IRFnType:
		params := make([]string, len(tt.Params))
		for i, p := range tt.Params {
			params[i] = irTypeEmitStr(p)
		}
		if tt.Ret == nil {
			return "func(" + strings.Join(params, ", ") + ")"
		}
		return "func(" + strings.Join(params, ", ") + ") " + irTypeEmitStr(tt.Ret)
	case IRTypeVar:
		return "interface{}" // unresolved type variable falls back to interface{}
	default:
		return "interface{}"
	}
}

// bodyPos extracts start/end position from a function body expression.
//
// Blocks carry explicit start/end positions. For non-Block bodies (e.g. a
// shorthand lambda body `u => u.field`), AST nodes don't track end positions
// uniformly — a few constructors (FieldAccess, FnCall-with-postfix) are
// built without NodePos, so the body's own exprPos may be zero even though
// an inner node like the receiver Ident has a valid position. exprFirstPos
// walks the body to find the first usable position, and we pad a generous
// one-line span for the end so cursor lookups inside single-line expression
// bodies succeed. Multi-line non-Block bodies stay as a known gap.
func bodyPos(body Expr) (Pos, Pos) {
	if body == nil {
		return Pos{}, Pos{}
	}
	if b, ok := body.(Block); ok {
		return b.Pos, b.EndPos
	}
	sp := exprFirstPos(body)
	if sp == (Pos{}) {
		return Pos{}, Pos{}
	}
	return sp, Pos{Line: sp.Line, Col: sp.Col + 10000}
}

// exprFirstPos walks an AST expression to find the first non-zero source
// position. Parser-built nodes like FieldAccess and some FnCall chains
// don't set NodePos, so a direct exprPos can return zero even when inner
// operands (idents, literals) have valid positions. Used by bodyPos to
// anchor non-Block lambda scopes.
func exprFirstPos(e Expr) Pos {
	if e == nil {
		return Pos{}
	}
	if p := e.exprPos(); p != (Pos{}) {
		return p
	}
	switch ex := e.(type) {
	case FieldAccess:
		return exprFirstPos(ex.Expr)
	case FnCall:
		return exprFirstPos(ex.Fn)
	case BinaryExpr:
		if p := exprFirstPos(ex.Left); p != (Pos{}) {
			return p
		}
		return exprFirstPos(ex.Right)
	case RefExpr:
		return exprFirstPos(ex.Expr)
	}
	return Pos{}
}

// --- Variable Scoping ---

// paramsToSymbols converts function parameters to symbol registration info.
func (l *Lowerer) paramsToSymbols(params []FnParam) []SymbolRegInfo {
	syms := make([]SymbolRegInfo, len(params))
	for i, p := range params {
		var irType IRType
		if p.Type != nil {
			irType = l.lowerType(p.Type)
		}
		syms[i] = SymbolRegInfo{
			Name:     p.Name,
			ArcaType: p.Type,
			IRType:   irType,
			Kind:     SymParameter,
			Pos:      p.Pos,
		}
	}
	return syms
}

func (l *Lowerer) resolveVar(name string) string {
	if l.currentScope != nil {
		if sym := l.currentScope.Lookup(name); sym != nil {
			return sym.GoName
		}
	}
	return snakeToCamel(name)
}

// --- Types ---

func (l *Lowerer) lowerType(t Type) IRType {
	if t == nil {
		return nil
	}
	switch tt := t.(type) {
	case NamedType:
		return l.lowerNamedType(tt)
	case PointerType:
		return IRPointerType{Inner: l.lowerType(tt.Inner)}
	case TupleType:
		elems := make([]IRType, len(tt.Elements))
		for i, e := range tt.Elements {
			elems[i] = l.lowerType(e)
		}
		return IRTupleType{Elements: elems}
	case FunctionType:
		params := make([]IRType, len(tt.Params))
		for i, p := range tt.Params {
			params[i] = l.lowerType(p)
		}
		var ret IRType
		if tt.Ret != nil {
			ret = l.lowerType(tt.Ret)
		}
		return IRFnType{Params: params, Ret: ret}
	case AssocTypeName:
		// Self.Builder — opaque in B1d. The enclosing trait is classified
		// Dictionary by analyzeTraitObjectSafety and dropped by
		// stage2LowerTypes, so this IR placeholder never reaches emit.
		// B2 introduces a dedicated IR node and substitutes against the
		// impl-side concrete type.
		return IRInterfaceType{}
	default:
		return IRInterfaceType{}
	}
}

func (l *Lowerer) lowerNamedType(nt NamedType) IRType {
	switch nt.Name {
	case "Unit":
		return IRNamedType{GoName: "struct{}"}
	case "Int":
		return IRNamedType{GoName: "int"}
	case "UInt":
		return IRNamedType{GoName: "uint"}
	case "Float":
		return IRNamedType{GoName: "float64"}
	case "String":
		return IRNamedType{GoName: "string"}
	case "Bool":
		return IRNamedType{GoName: "bool"}
	case "Any":
		// Arca's unknown-shaped escape hatch. Go emit is `interface{}`, and the
		// unify rules already treat IRInterfaceType as permissive so any value
		// can flow in. Narrowing is intended via `match v { id: T => ... }`
		// (match type pattern — Slice 3c).
		return IRInterfaceType{}
	case "List":
		if len(nt.Params) > 0 {
			return IRListType{Elem: l.lowerType(nt.Params[0])}
		}
		return IRListType{Elem: IRInterfaceType{}}
	case "Map":
		if len(nt.Params) >= 2 {
			return IRMapType{
				Key:   l.lowerType(nt.Params[0]),
				Value: l.lowerType(nt.Params[1]),
			}
		}
		return IRMapType{Key: IRInterfaceType{}, Value: IRInterfaceType{}}
	case "Ref":
		if len(nt.Params) > 0 {
			return IRRefType{Inner: l.lowerType(nt.Params[0])}
		}
		return IRRefType{Inner: IRInterfaceType{}}
	case "Option":
		l.builtins["option"] = true
		if len(nt.Params) > 0 {
			return IROptionType{Inner: l.lowerType(nt.Params[0])}
		}
		return IRInterfaceType{}
	case "Result":
		l.builtins["result"] = true
		if len(nt.Params) >= 2 {
			return IRResultType{
				Ok:  l.lowerType(nt.Params[0]),
				Err: l.lowerType(nt.Params[1]),
			}
		}
		if len(nt.Params) == 1 {
			return IRResultType{
				Ok:  l.lowerType(nt.Params[0]),
				Err: IRNamedType{GoName: "error"},
			}
		}
		return IRResultType{
			Ok:  IRInterfaceType{},
			Err: IRNamedType{GoName: "error"},
		}
	case "Self":
		if l.currentTypeName != "" {
			return IRNamedType{GoName: l.currentTypeName}
		}
		return IRNamedType{GoName: "Self"}
	default:
		// Type parameter → IRTypeVar (only outside type definitions)
		if l.infer != nil && l.isTypeParam(nt.Name) {
			return l.typeParamVar(nt.Name)
		}
		// Trait used as a type (trait object): `fun f(e: Error)`.
		if _, ok := l.traits[nt.Name]; ok {
			if l.traitIsDictionary(nt.Name) {
				l.addCompileError(ErrUnsupportedFeature, nt.Pos, UnsupportedFeatureData{
					Feature: fmt.Sprintf("constraint-only trait %s as a type", nt.Name),
					Context: "Phase 1 (use a vtable trait, or wait for B2 dictionary dispatch)",
				})
				return IRInterfaceType{}
			}
			if nt.Name == "Error" {
				l.builtins["error_trait"] = true
			}
			return IRTraitType{Name: nt.Name}
		}
		// Qualified Go type (e.g. "sql.DB", "time.Time") marks the package as used.
		if dot := strings.IndexByte(nt.Name, '.'); dot > 0 {
			l.lookupGoPackage(nt.Name[:dot])
		}
		params := make([]IRType, len(nt.Params))
		for i, p := range nt.Params {
			params[i] = l.lowerType(p)
		}
		return IRNamedType{GoName: nt.Name, Params: params}
	}
}

// --- Expressions ---

// lowerExpr lowers an expression with no type hint.
func (l *Lowerer) lowerExpr(expr Expr) IRExpr {
	return l.lowerExprHint(expr, nil)
}

// lowerExprHint lowers an expression with an optional type hint for bidirectional inference.
// Dispatches to type-specific lowering functions and validates against the hint when given.
func (l *Lowerer) lowerExprHint(expr Expr, hint IRType) IRExpr {
	if expr == nil {
		return nil
	}
	result := l.dispatchLowerExpr(expr, hint)
	if hint != nil && result != nil {
		result = l.autoSomeLift(result, hint)
		l.checkTypeHint(result, hint, expr)
	}
	return result
}

// autoSomeLift wraps result in Some(...) when the hint is Option<T> and the
// value's static type is T (not already Option). Single-layer only:
// Option<Option<T>> does not lift through two levels — the user must write
// Some(Some(v)) or Some(None) to disambiguate. None and & (Ref construction)
// are never auto-inserted — they carry information that explicit-first
// preserves.
func (l *Lowerer) autoSomeLift(result IRExpr, hint IRType) IRExpr {
	opt, ok := l.infer.resolve(hint).(IROptionType)
	if !ok {
		return result
	}
	actualType := result.irType()
	if actualType == nil {
		return result
	}
	resolved := l.infer.resolve(actualType)
	// Already an Option value: no lift. Covers pass-through and blocks the
	// multi-layer lift case (value of type Option<T> into hint Option<Option<T>>
	// stays as-is so checkTypeHint reports the real mismatch).
	if _, isOpt := resolved.(IROptionType); isOpt {
		return result
	}
	// Unresolved placeholder or a free type var: let checkTypeHint drive
	// unification. Lifting here would commit a Some wrap prematurely.
	if _, isIface := resolved.(IRInterfaceType); isIface {
		return result
	}
	if _, isVar := resolved.(IRTypeVar); isVar {
		return result
	}
	_ = opt
	l.builtins["option"] = true
	return IRSomeCall{Value: result, Type: IROptionType{Inner: actualType}}
}

func (l *Lowerer) dispatchLowerExpr(expr Expr, hint IRType) IRExpr {
	switch e := expr.(type) {
	case IntLit:
		return IRIntLit{Value: e.Value, Type: IRNamedType{GoName: "int"}}
	case FloatLit:
		return IRFloatLit{Value: e.Value, Type: IRNamedType{GoName: "float64"}}
	case StringLit:
		return IRStringLit{Value: e.Value, Type: IRNamedType{GoName: "string"}, Multiline: e.Multiline}
	case BoolLit:
		return IRBoolLit{Value: e.Value, Type: IRNamedType{GoName: "bool"}}
	case Ident:
		return l.lowerIdentHint(e, hint)
	case StringInterp:
		return l.lowerStringInterp(e)
	case FnCall:
		return l.lowerFnCallHint(e, hint)
	case FieldAccess:
		return l.lowerFieldAccess(e)
	case IndexAccess:
		return l.lowerIndexAccess(e)
	case IfExpr:
		return l.lowerIfExpr(e)
	case ConstructorCall:
		return l.lowerConstructorCallHint(e, hint)
	case Block:
		return l.lowerBlockHint(e, hint)
	case TryBlockExpr:
		return l.lowerTryBlock(e)
	case MatchExpr:
		return l.lowerMatchExprHint(e, hint)
	case Lambda:
		return l.lowerLambdaHint(e, hint)
	case TupleExpr:
		return l.lowerTuple(e)
	case ForExpr:
		return l.lowerForExpr(e)
	case ListLit:
		return l.lowerListLit(e)
	case MapLit:
		return l.lowerMapLitHint(e, hint)
	case BinaryExpr:
		return l.lowerBinaryExpr(e)
	case RefExpr:
		inner := l.lowerExpr(e.Expr)
		return IRRefExpr{Expr: inner, Type: IRRefType{Inner: inner.irType()}}
	case RangeExpr:
		// RangeExpr as standalone expression (not inside for) — shouldn't happen often
		return IRFnCall{
			Fn:   IRIdent{GoName: "__range"},
			Args: []IRExpr{l.lowerExpr(e.Start), l.lowerExpr(e.End)},
			Type: IRListType{Elem: IRNamedType{GoName: "int"}},
		}
	default:
		return IRStringLit{Value: "/* unsupported expr */", Type: IRInterfaceType{}}
	}
}

func (l *Lowerer) lowerIdentHint(e Ident, hint IRType) IRExpr {
	if e.Name == "None" {
		return l.lowerNoneCall(hint)
	}
	return l.lowerIdent(e)
}

func (l *Lowerer) lowerIdent(e Ident) IRExpr {
	// self → receiver variable
	if e.Name == "self" && l.currentReceiver != "" {
		return IRIdent{GoName: l.currentReceiver, Type: IRNamedType{GoName: l.currentTypeName}}
	}
	// Unit literal
	if e.Name == "Unit" {
		return IRIdent{GoName: "struct{}{}", Type: IRNamedType{GoName: "struct{}"}}
	}
	// None bare identifier
	if e.Name == "None" {
		return l.lowerNoneCall(nil)
	}
	// Enum variant bare reference: e.g. `Red` resolves to `ColorRed`
	if typeName := l.findTypeName(e.Name); typeName != "" {
		if td, ok := l.types[typeName]; ok && isEnum(td) {
			return IRIdent{
				GoName: typeName + e.Name,
				Type:   IRNamedType{GoName: typeName},
			}
		}
	}
	// Public function name resolution
	if goName, ok := l.fnNames[e.Name]; ok {
		return IRIdent{GoName: goName, Type: IRInterfaceType{}}
	}
	// Dotted names: Type.method or Go FFI like fmt.Println
	if strings.Contains(e.Name, ".") {
		parts := strings.SplitN(e.Name, ".", 2)
		// `Self.staticFun` resolves against the enclosing type so `static fun`
		// calls inside methods don't have to repeat the type name.
		if parts[0] == "Self" && l.currentTypeName != "" {
			parts[0] = l.currentTypeName
		}
		if td, ok := l.types[parts[0]]; ok {
			for _, m := range td.Methods {
				if m.Name == parts[1] && m.Static {
					funcName := parts[0] + capitalize(parts[1])
					if !m.Public {
						funcName = strings.ToLower(parts[0][:1]) + parts[0][1:] + capitalize(parts[1])
					}
					return IRIdent{GoName: funcName, Type: IRInterfaceType{}}
				}
			}
		}
		// B2f: synthesised `<Type>.draft()` factory for derive Bindable hosts.
		if l.bindableTypes[parts[0]] && parts[1] == "draft" {
			factoryName := strings.ToLower(parts[0][:1]) + parts[0][1:] + "Draft"
			return IRIdent{GoName: factoryName, Type: IRFnType{Ret: IRNamedType{GoName: parts[0] + "Draft"}}}
		}
		return IRIdent{GoName: e.Name, Type: IRInterfaceType{}}
	}
	// Bare reference to a Go/Arca package (e.g. `http` in `http.StatusOK`)
	if _, ok := l.lookupGoPackage(e.Name); ok {
		return IRIdent{GoName: e.Name, Type: IRInterfaceType{}}
	}
	// Variable resolution via lexical scope
	goName := l.resolveVar(e.Name)
	var arcaType Type
	irType := IRType(IRInterfaceType{})
	found := false
	if l.currentScope != nil {
		if sym := l.currentScope.Lookup(e.Name); sym != nil {
			arcaType = sym.Type
			if sym.IRType != nil {
				irType = sym.IRType
			}
			found = true
		}
	}
	if !found && l.currentScope != nil {
		l.addCompileError(ErrUnknownVariable, e.Pos, UnknownVariableData{Name: e.Name})
	}
	return IRIdent{GoName: goName, Type: irType, Source: SourceInfo{Type: arcaType}}
}

func (l *Lowerer) lowerStringInterp(si StringInterp) IRExpr {
	l.builtins["fmt"] = true
	var fmtParts []string
	var args []IRExpr
	for _, part := range si.Parts {
		if lit, ok := part.(StringLit); ok {
			fmtParts = append(fmtParts, lit.Value)
		} else {
			fmtParts = append(fmtParts, "%v")
			args = append(args, l.lowerExpr(part))
		}
	}
	fmtStr := strings.Join(fmtParts, "")
	if len(args) == 0 {
		return IRStringLit{Value: fmtStr, Type: IRNamedType{GoName: "string"}, Multiline: si.Multiline}
	}
	return IRStringInterp{
		Format:    fmtStr,
		Args:      args,
		Type:      IRNamedType{GoName: "string"},
		Multiline: si.Multiline,
	}
}

func (l *Lowerer) lowerFnCallHint(e FnCall, hint IRType) IRExpr {
	return l.lowerFnCallWithHint(e, hint)
}

// explicitTypeArgsStr renders explicit type arguments as a Go type args string.
// Returns empty string if no type args were provided.
func (l *Lowerer) explicitTypeArgsStr(typeArgs []Type) string {
	if len(typeArgs) == 0 {
		return ""
	}
	parts := make([]string, len(typeArgs))
	for i, ta := range typeArgs {
		parts[i] = irTypeEmitStr(l.lowerType(ta))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// orderedTypeParamNames returns the type-param names of a generic call in
// declaration order when available (B3a, sourced from goReturnInfo), falling
// back to sorted-by-name for callers that haven't been updated yet.
func orderedTypeParamNames(vars map[string]IRType, order []string) []string {
	if len(order) > 0 {
		return order
	}
	names := make([]string, 0, len(vars))
	for name := range vars {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// buildGoTypeArgsStr builds the Go generic type args string "[T1, T2, ...]"
// from a generic call's type vars map (after unification). Explicit type args
// take precedence if provided.
func (l *Lowerer) buildGoTypeArgsStr(vars map[string]IRType, order []string, explicit []Type) string {
	if len(explicit) > 0 {
		return l.explicitTypeArgsStr(explicit)
	}
	if len(vars) == 0 {
		return ""
	}
	names := orderedTypeParamNames(vars, order)
	parts := make([]string, len(names))
	for i, name := range names {
		resolved := l.resolveDeep(vars[name])
		parts[i] = irTypeEmitStr(resolved)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// unifyExplicitTypeArgs unifies explicit type arguments with a generic call's
// type parameter variables in declaration order.
func (l *Lowerer) unifyExplicitTypeArgs(vars map[string]IRType, order []string, typeArgs []Type) {
	if len(typeArgs) == 0 || len(vars) == 0 {
		return
	}
	names := orderedTypeParamNames(vars, order)
	for i, name := range names {
		if i >= len(typeArgs) {
			break
		}
		// Raw substitution so buildGoTypeArgsStr can resolveDeep later.
		// Cannot fail: vars[name] is a freshly instantiated type var.
		l.infer.unify(vars[name], l.lowerType(typeArgs[i]))
	}
}

func (l *Lowerer) lowerFnCall(e FnCall) IRExpr {
	return l.lowerFnCallWithHint(e, nil)
}

func (l *Lowerer) lowerFnCallWithHint(e FnCall, hint IRType) IRExpr {
	// Builtin functions
	if ident, ok := e.Fn.(Ident); ok {
		switch ident.Name {
		case "__try":
			// Try operator in expression context: produces IRTryExpr which
			// stage2Lower hoists into a multi-assign + nil-check + early
			// return ahead of the enclosing statement. Statement-level
			// `let x = expr?` and bare `expr?` are intercepted earlier in
			// lowerStmt and use IRTryLetStmt instead.
			if len(e.Args) == 1 {
				return l.lowerTryExprWithHint(e, e.Args[0], hint)
			}
		default:
			// Check prelude builtins
			if def, ok := prelude[ident.Name]; ok {
				if def.Import != "" {
					l.builtins[def.Import] = true
				}
				if def.Builtin != "" {
					l.builtins[def.Builtin] = true
				}
				if def.Signature != nil {
					sig := def.Signature(l)
					args := make([]IRExpr, len(e.Args))
					for i, a := range e.Args {
						var hint IRType
						if i < len(sig.Params) {
							hint = sig.Params[i]
						}
						args[i] = l.lowerExprHint(a, hint)
					}
					return IRFnCall{
						Fn:     IRIdent{GoName: def.GoFunc},
						Args:   args,
						Type:   l.resolveDeep(sig.Ret),
						Source: SourceInfo{Pos: e.Pos},
					}
				}
				args := l.lowerExprs(e.Args)
				if def.Lower != nil {
					if result := def.Lower(args); result != nil {
						return result
					}
				}
				// Plain GoFunc builtins (println, print): emit a direct
				// untyped call — variadic `fmt.Println` etc. have no
				// useful return type to check.
				return IRFnCall{
					Fn:     IRIdent{GoName: def.GoFunc},
					Args:   args,
					Type:   IRInterfaceType{},
					Source: SourceInfo{Pos: e.Pos},
				}
			}
		}
	}

	// Go FFI or module-qualified call
	if fa, ok := e.Fn.(FieldAccess); ok {
		// Go FFI call: strconv.Itoa(...), http.HandleFunc(...)
		if ident, ok := fa.Expr.(Ident); ok {
			if _, isGoPkg := l.lookupGoPackage(ident.Name); isGoPkg {
				goCallName := ident.Name + "." + fa.Field
				args := l.lowerCallArgs(e)
				typeArgs := e.TypeArgs
				// B3a: stdlib helpers taking `BindableDict[T, B]` as their
				// hidden first arg get the dictionary instance + Draft type
				// injected from the user's explicit type arg.
				switch dictArg, draftType, kind := l.maybeInjectBindableStdlibArg(goCallName, e); kind {
				case bindableInjectOK:
					args = append([]IRExpr{dictArg}, args...)
					typeArgs = append(typeArgs, draftType)
				case bindableInjectFailed:
					// Type-arg precondition reported; short-circuit to
					// avoid cascading arg-count / type-mismatch errors
					// from the now-incomplete call site.
					return IRFnCall{
						Fn:     IRIdent{GoName: goCallName},
						Args:   args,
						Type:   IRError{Reason: fmt.Sprintf("%s: type argument is not Bindable", goCallName)},
						Source: SourceInfo{Pos: e.Pos},
					}
				}
				ret := l.resolveGoCall(goCallName, args, e.Pos)
				l.unifyExplicitTypeArgs(ret.TypeVars, ret.TypeParamOrder, typeArgs)
				if hint != nil {
					l.infer.unify(ret.Type, hint)
				}
				typeArgsStr := l.buildGoTypeArgsStr(ret.TypeVars, ret.TypeParamOrder, typeArgs)
				return IRFnCall{Fn: IRIdent{GoName: goCallName}, Args: args, Type: l.resolveDeep(ret.Type), TypeArgs: typeArgsStr, GoMultiReturn: ret.GoMultiReturn, Source: SourceInfo{Pos: e.Pos}}
			}
		}
		// Arca module-qualified call
		if ident, ok := fa.Expr.(Ident); ok && l.moduleNames[ident.Name] {
			fnName := fa.Field
			if goName, ok := l.fnNames[fnName]; ok {
				fnName = goName
			}
			args := l.lowerCallArgs(e)
			if l.goModule != "" {
				return IRFnCall{
					Fn:     IRIdent{GoName: ident.Name + "." + fnName},
					Args:   args,
					Type:   IRInterfaceType{},
					Source: SourceInfo{Pos: e.Pos, Name: fa.Field},
				}
			}
			return IRFnCall{Fn: IRIdent{GoName: fnName}, Args: args, Type: IRInterfaceType{}, Source: SourceInfo{Pos: e.Pos, Name: fa.Field}}
		}
		// Monadic methods on Result/Option desugar to try-block expressions.
		// Handled before regular method dispatch so emit stays unchanged.
		if desugared := l.tryDesugarMonadicMethod(fa, e); desugared != nil {
			return l.lowerExprHint(desugared, hint)
		}

		// Regular method call: obj.method(args)
		methodName := l.resolveMethodName(fa.Field)
		args := l.lowerCallArgs(e)
		receiver := l.lowerExpr(fa.Expr)
		ret := l.resolveMethodReturnType(receiver, fa.Field, e.Pos)
		// Check method argument count
		l.checkMethodArgCount(receiver, fa.Field, len(e.Args), e.Pos)
		return IRMethodCall{
			Receiver:      receiver,
			Method:        methodName,
			Args:          args,
			Type:          ret.Type,
			GoMultiReturn: ret.GoMultiReturn,
		}
	}

	args := l.lowerCallArgs(e)
	fnExpr := l.lowerExpr(e.Fn)
	if ident, ok := fnExpr.(IRIdent); ok {
		arcaName := ""
		if id, ok := e.Fn.(Ident); ok {
			arcaName = id.Name
		}
		// Try Arca function return type first
		var fnType IRType = IRInterfaceType{}
		var goMultiReturn bool
		var arcaTypeArgsStr string
		if id, ok := e.Fn.(Ident); ok {
			if fn, ok := l.functions[id.Name]; ok {
				if len(args) != len(fn.Params) {
					l.addCompileError(ErrWrongArgCount, e.Pos, WrongArgCountData{
						Func: id.Name, Expected: len(fn.Params), Actual: len(args),
					})
				}
				// B2e: generic Arca fn called with explicit type args.
				// Substitute the callee's TypeParams into its signature, and
				// for each Bindable-constrained param inject the matching
				// `__<Type>Bindable` global as a hidden value arg + the
				// `<Type>Draft` Go type arg. Implicit-arg inference deferred.
				if len(fn.TypeParams) > 0 && len(e.TypeArgs) == len(fn.TypeParams) {
					subst := make(map[string]Type, len(fn.TypeParams))
					for i, tp := range fn.TypeParams {
						subst[tp.Name] = e.TypeArgs[i]
					}
					var hiddenArgs []IRExpr
					var allTypeArgs []string
					for _, tp := range fn.TypeParams {
						// Direct AST→Go-name path for the common simple-name
						// case so type-param-as-arg (transitive Bindable
						// dispatch) emits as `T` rather than the IRTypeVar
						// fallback `interface{}`.
						if nt, ok := subst[tp.Name].(NamedType); ok && len(nt.Params) == 0 {
							allTypeArgs = append(allTypeArgs, nt.Name)
						} else {
							allTypeArgs = append(allTypeArgs, irTypeEmitStr(l.lowerType(subst[tp.Name])))
						}
						if tp.Constraint != "Bindable" {
							continue
						}
						concrete, ok := subst[tp.Name].(NamedType)
						if !ok {
							l.addCompileError(ErrUnsupportedFeature, e.Pos, UnsupportedFeatureData{
								Feature: "non-named type as Bindable type argument",
								Context: "Bindable dispatch requires a concrete derive-Bindable type or a forwarded `[T: Bindable]` parameter",
							})
							continue
						}
						// Transitive case: explicit type arg is the caller's
						// own Bindable-constrained type-param. Pass through
						// the caller's hidden `__bindable<T>` and `__draft<T>`
						// rather than looking up a concrete dictionary.
						if l.currentFnBindableParams[concrete.Name] {
							allTypeArgs = append(allTypeArgs, "__draft"+concrete.Name)
							hiddenArgs = append(hiddenArgs, IRIdent{
								GoName: "__bindable" + concrete.Name,
								Type:   IRNamedType{GoName: "stdlib.BindableDict", Params: []IRType{IRNamedType{GoName: concrete.Name}, IRNamedType{GoName: "__draft" + concrete.Name}}},
							})
							continue
						}
						if !l.bindableTypes[concrete.Name] {
							l.addCompileError(ErrUnsupportedFeature, e.Pos, UnsupportedFeatureData{
								Feature: fmt.Sprintf("Bindable type argument %s", concrete.Name),
								Context: "type must be declared with `derive Bindable`",
							})
							continue
						}
						allTypeArgs = append(allTypeArgs, concrete.Name+"Draft")
						hiddenArgs = append(hiddenArgs, IRIdent{
							GoName: "__" + concrete.Name + "Bindable",
							Type:   IRNamedType{GoName: "stdlib.BindableDict", Params: []IRType{IRNamedType{GoName: concrete.Name}, IRNamedType{GoName: concrete.Name + "Draft"}}},
						})
					}
					arcaTypeArgsStr = "[" + strings.Join(allTypeArgs, ", ") + "]"
					args = append(hiddenArgs, args...)
					if fn.ReturnType != nil {
						fnType = l.lowerType(substType(fn.ReturnType, subst))
					}
				} else if fn.ReturnType != nil {
					fnType = l.lowerType(fn.ReturnType)
				}
			}
		}
		// Fall back to Go FFI resolution
		var goTypeVars map[string]IRType
		var goTypeParamOrder []string
		if _, isInterface := fnType.(IRInterfaceType); isInterface {
			ret := l.resolveGoCall(ident.GoName, args, e.Pos)
			fnType = ret.Type
			goMultiReturn = ret.GoMultiReturn
			goTypeVars = ret.TypeVars
			goTypeParamOrder = ret.TypeParamOrder
			l.unifyExplicitTypeArgs(goTypeVars, goTypeParamOrder, e.TypeArgs)
			// Same as the FieldAccess path above: raw substitution only,
			// outer checkTypeHint owns the reporting.
			if hint != nil {
				l.infer.unify(fnType, hint)
			}
		}
		typeArgsStr := arcaTypeArgsStr
		if typeArgsStr == "" {
			typeArgsStr = l.buildGoTypeArgsStr(goTypeVars, goTypeParamOrder, e.TypeArgs)
		}
		return IRFnCall{Fn: ident, Args: args, Type: l.resolveDeep(fnType), TypeArgs: typeArgsStr, GoMultiReturn: goMultiReturn, Source: SourceInfo{Pos: e.Pos, Name: arcaName}}
	}
	// Function-valued expression as callee: inline lambda (`(x => ...)(v)`,
	// or a desugar-built `FnCall{Fn: Lambda}` from monadic methods), or any
	// other IR expression that evaluates to a Go function. Fn carries the
	// callee verbatim; emit renders via emitExpr so `func(...) { ... }(args)`
	// falls out naturally. For inline IRFn, Type is the lambda's return type.
	var callType IRType = IRInterfaceType{}
	if fn, ok := fnExpr.(IRFn); ok && fn.Ret != nil {
		callType = fn.Ret
	}
	return IRFnCall{Fn: fnExpr, Args: args, Type: callType, Source: SourceInfo{Pos: e.Pos}}
}

// resolveGoCall validates a Go FFI function call and returns the resolved return info.
// Checks argument count and types against the Go function signature.
func (l *Lowerer) resolveGoCall(goName string, args []IRExpr, pos Pos) goReturnInfo {
	if !strings.Contains(goName, ".") {
		return goReturnInfo{Type: IRInterfaceType{}}
	}
	parts := strings.SplitN(goName, ".", 2)
	pkgShort := parts[0]
	funcName := parts[1]

	goPkg, ok := l.lookupGoPackage(pkgShort)
	if !ok {
		return goReturnInfo{Type: IRInterfaceType{}}
	}

	info := l.typeResolver.ResolveFunc(goPkg.FullPath, funcName)
	if info == nil {
		// Detect camelCase Arca-style write of a PascalCase Go function so
		// the diagnostic suggests the right form (`fmt.println` → `fmt.Println`).
		// Returning the resolved type keeps cascade type-checking clean.
		if pascal := capitalize(funcName); pascal != funcName {
			if alt := l.typeResolver.ResolveFunc(goPkg.FullPath, pascal); alt != nil {
				l.addCompileError(ErrGoFFINameConvention, pos, GoFFINameConventionData{
					Given: funcName, Expected: pascal, Kind: "function", Receiver: pkgShort,
				})
				info = alt
			}
		}
		if info == nil {
			return goReturnInfo{Type: IRInterfaceType{}}
		}
	}

	// Validate argument count
	minArgs := len(info.Params)
	if info.Variadic {
		minArgs-- // last param is variadic, not required
	}
	if !info.Variadic && len(args) != len(info.Params) {
		l.addCompileError(ErrWrongArgCount, pos, WrongArgCountData{Func: goName, Expected: len(info.Params), Actual: len(args)})
	} else if info.Variadic && len(args) < minArgs {
		l.addCompileError(ErrWrongArgCount, pos, WrongArgCountData{Func: goName, Expected: minArgs, Actual: len(args), AtLeast: true})
	}

	// Generic function: instantiate with fresh type variables and unify with args.
	// This lets HM inference substitute type params from arg types and explicit type args.
	if len(info.TypeParams) > 0 {
		vars, paramTypes, ret := l.instantiateGeneric(info)
		for i, arg := range args {
			if i >= len(paramTypes) {
				break
			}
			l.unify(arg.irType(), paramTypes[i], pos)
		}
		ret.TypeVars = vars
		ret.TypeParamOrder = info.TypeParams
		return ret
	}

	// Validate argument types (Phase 2)
	for i, arg := range args {
		argType := irTypeToGoString(arg.irType())
		if argType == "" {
			continue // unknown type — skip
		}

		var paramType string
		if i < len(info.Params) {
			paramType = info.Params[i].Type
		}

		// Variadic: args beyond param count check against the variadic element type
		if info.Variadic && i >= len(info.Params)-1 {
			variadicSliceType := info.Params[len(info.Params)-1].Type
			// "[]string" → "string", "[]any" → "any"
			if strings.HasPrefix(variadicSliceType, "[]") {
				paramType = variadicSliceType[2:]
			}
		}

		if paramType != "" && !goTypesCompatible(argType, paramType) {
			l.addCompileError(ErrTypeMismatch, pos, ArgTypeMismatchData{
				Func:     goName,
				ArgIndex: i + 1,
				Expected: paramType,
				Actual:   argType,
			})
		}
	}

	return l.goFuncReturnType(info)
}

// irTypeToGoString converts an IRType to a Go type string for comparison.
// Returns "" if the type is unknown/interface{}.
func irTypeToGoString(t IRType) string {
	if t == nil {
		return ""
	}
	switch tt := t.(type) {
	case IRNamedType:
		switch tt.GoName {
		case "int", "uint", "float64", "string", "bool", "byte", "struct{}":
			return tt.GoName
		default:
			return "" // user-defined or complex — skip check
		}
	case IRListType:
		elem := irTypeToGoString(tt.Elem)
		if elem != "" {
			return "[]" + elem
		}
		return ""
	case IRInterfaceType:
		return "" // unknown — skip check
	default:
		return ""
	}
}

// goTypesCompatible checks if an Arca-resolved Go type is compatible with a Go parameter type.
func goTypesCompatible(arcaType, goParam string) bool {
	if arcaType == goParam {
		return true
	}
	// interface{}/any accepts everything
	if goParam == "any" || goParam == "interface{}" {
		return true
	}
	// Variadic slice: []any, []interface{}, []string etc.
	if strings.HasPrefix(goParam, "[]") && strings.HasPrefix(arcaType, "[]") {
		return goTypesCompatible(arcaType[2:], goParam[2:])
	}
	return false
}

// goReturnInfo holds the resolved Arca type and whether the Go function returns multiple values.
type goReturnInfo struct {
	Type           IRType
	GoMultiReturn  bool              // true if Go func returns multiple values (needs multi-value receive)
	TypeVars       map[string]IRType // type param name → fresh IRTypeVar (for generic calls)
	TypeParamOrder []string          // declaration order of type params; used for explicit-type-arg alignment
}

// goFuncReturnType converts a FuncInfo's return types to an Arca IR type.
// Go multi-return is mechanically mapped:
//
//	()           → Unit
//	(T)          → T
//	(error)      → Result[Unit, error]
//	(T, error)   → Result[T, error]
//	(T, bool)    → Option[T]
//	(T1, T2)     → (T1, T2)
//	(T1, T2, T3) → (T1, T2, T3)
func (l *Lowerer) goFuncReturnType(info *FuncInfo) goReturnInfo {
	return l.goFuncReturnTypeWithVars(info, nil)
}

// goFuncReturnTypeWithVars builds the IR return type, substituting generic
// type parameter names with IRTypes from the vars map.
func (l *Lowerer) goFuncReturnTypeWithVars(info *FuncInfo, vars map[string]IRType) goReturnInfo {
	toIR := func(s string) IRType { return goTypeToIRWithVars(s, vars) }
	if len(info.Results) == 0 {
		return goReturnInfo{Type: IRNamedType{GoName: "struct{}"}}
	}
	if len(info.Results) == 1 {
		if info.Results[0].Type == "error" {
			// error-only: single Go return value, NOT multi-return.
			// GoMultiReturn stays false — the Go call returns one `error`.
			return goReturnInfo{
				Type: IRResultType{Ok: IRNamedType{GoName: "struct{}"}, Err: IRTraitType{Name: "Error"}},
			}
		}
		return goReturnInfo{Type: wrapPointerInOption(toIR(info.Results[0].Type))}
	}
	if len(info.Results) == 2 {
		if info.Results[1].Type == "error" {
			return goReturnInfo{
				Type:          IRResultType{Ok: wrapPointerInOption(toIR(info.Results[0].Type)), Err: IRTraitType{Name: "Error"}},
				GoMultiReturn: true,
			}
		}
		if info.Results[1].Type == "bool" {
			return goReturnInfo{
				Type:          IROptionType{Inner: toIR(info.Results[0].Type)},
				GoMultiReturn: true,
			}
		}
	}
	// 2+ non-special or 3+ returns → Tuple
	elems := make([]IRType, len(info.Results))
	for i, r := range info.Results {
		elems[i] = wrapPointerInOption(toIR(r.Type))
	}
	return goReturnInfo{
		Type:          IRTupleType{Elements: elems},
		GoMultiReturn: true,
	}
}

// instantiateGeneric creates a fresh type variable for each type parameter
// of a generic Go function and returns the substituted parameter and return types.
func (l *Lowerer) instantiateGeneric(info *FuncInfo) (vars map[string]IRType, paramTypes []IRType, ret goReturnInfo) {
	if len(info.TypeParams) == 0 {
		return nil, nil, l.goFuncReturnType(info)
	}
	vars = make(map[string]IRType, len(info.TypeParams))
	for _, name := range info.TypeParams {
		vars[name] = l.freshTypeVar()
	}
	paramTypes = make([]IRType, len(info.Params))
	for i, p := range info.Params {
		paramTypes[i] = wrapPointerInOption(goTypeToIRWithVars(p.Type, vars))
	}
	ret = l.goFuncReturnTypeWithVars(info, vars)
	return
}

// wrapPointerInOption recursively walks a Go-sourced IR type and converts
// every IRPointerType to IROptionType{IRRefType{...}} — the safe Arca form
// for Go pointers that may be nil. Walks inside List/Map/Tuple/Option/Result
// so nested pointer positions are wrapped too (e.g. `[]*T` → `List[Option[Ref[T]]]`).
// Applied at FFI positions where Go pointers cross into Arca: return types,
// parameter types, struct fields, generic inner arguments.
func wrapPointerInOption(t IRType) IRType {
	switch tt := t.(type) {
	case IRPointerType:
		return IROptionType{Inner: IRRefType{Inner: wrapPointerInOption(tt.Inner)}}
	case IRListType:
		return IRListType{Elem: wrapPointerInOption(tt.Elem)}
	case IRMapType:
		return IRMapType{Key: wrapPointerInOption(tt.Key), Value: wrapPointerInOption(tt.Value)}
	case IRTupleType:
		elems := make([]IRType, len(tt.Elements))
		for i, e := range tt.Elements {
			elems[i] = wrapPointerInOption(e)
		}
		return IRTupleType{Elements: elems}
	case IROptionType:
		return IROptionType{Inner: wrapPointerInOption(tt.Inner)}
	case IRResultType:
		return IRResultType{Ok: wrapPointerInOption(tt.Ok), Err: wrapPointerInOption(tt.Err)}
	}
	return t
}

// goTypeToIRName converts a go/types type string to a short Go type name.
// goTypeToIR converts a go/types type string to an IRType, handling pointer types.
func goTypeToIR(goType string) IRType {
	return goTypeToIRWithVars(goType, nil)
}

// goTypeToIRWithVars converts a Go type string to an IRType, substituting
// type parameter names with the IRType in the vars map.
func goTypeToIRWithVars(goType string, vars map[string]IRType) IRType {
	if strings.HasPrefix(goType, "*") {
		return IRPointerType{Inner: goTypeToIRWithVars(goType[1:], vars)}
	}
	if strings.HasPrefix(goType, "[]") {
		return IRListType{Elem: goTypeToIRWithVars(goType[2:], vars)}
	}
	if strings.HasPrefix(goType, "func(") {
		if fn, ok := goFuncTypeToIR(goType, vars); ok {
			return fn
		}
	}
	if vars != nil {
		if v, ok := vars[goType]; ok {
			return v
		}
	}
	// Go's `any` / `interface{}` surface as Arca's Any (IRInterfaceType).
	// Without this, FFI returns of `any` became an opaque IRNamedType{"any"}
	// that didn't unify with anything, so the return was unusable on the
	// Arca side.
	if goType == "any" || goType == "interface{}" {
		return IRInterfaceType{}
	}
	// Generic instantiation: "<pkg>.<Type>[<arg>, <arg>, ...]" (B3a).
	// go/types renders parametrised types this way, and unify needs the
	// inner type args carried as IRType params (not embedded in GoName).
	if idx := strings.Index(goType, "["); idx > 0 && strings.HasSuffix(goType, "]") {
		base := goType[:idx]
		paramsStr := goType[idx+1 : len(goType)-1]
		var params []IRType
		depth := 0
		start := 0
		for i := 0; i < len(paramsStr); i++ {
			switch paramsStr[i] {
			case '[':
				depth++
			case ']':
				depth--
			case ',':
				if depth == 0 {
					params = append(params, goTypeToIRWithVars(strings.TrimSpace(paramsStr[start:i]), vars))
					start = i + 1
				}
			}
		}
		if start <= len(paramsStr) {
			params = append(params, goTypeToIRWithVars(strings.TrimSpace(paramsStr[start:]), vars))
		}
		return IRNamedType{GoName: goTypeToIRName(base), Params: params}
	}
	return IRNamedType{GoName: goTypeToIRName(goType)}
}

// goFuncTypeToIR parses a Go `func(...) ...` type string into an IRFnType.
// Single-return-value shapes only (matches the legacy parseFuncType); nested
// function types inside params are not split and fall through as named types
// at the inner boundary. Returns (nil, false) when the string is malformed so
// the caller can fall through to the catch-all IRNamedType emission.
func goFuncTypeToIR(goType string, vars map[string]IRType) (IRType, bool) {
	inner := goType[len("func("):]
	parenEnd := strings.Index(inner, ")")
	if parenEnd < 0 {
		return nil, false
	}
	paramStr := inner[:parenEnd]
	resultStr := strings.TrimSpace(inner[parenEnd+1:])

	var params []IRType
	if paramStr != "" {
		for _, p := range strings.Split(paramStr, ", ") {
			p = strings.TrimSpace(p)
			// Named params surface as "name type" from go/types; the type is
			// always the last whitespace-delimited token.
			if spaceIdx := strings.LastIndex(p, " "); spaceIdx >= 0 {
				p = p[spaceIdx+1:]
			}
			params = append(params, goTypeToIRWithVars(p, vars))
		}
	}
	var ret IRType
	if resultStr != "" {
		if resultStr == "error" {
			// Error-only return at the Go surface corresponds to Arca's
			// Result[Unit, Error]. Matches goFuncReturnType's mapping so a
			// lambda body returning `Result[Unit, Error]` unifies with a
			// Go callback typed `func(...) error`.
			ret = IRResultType{Ok: IRNamedType{GoName: "struct{}"}, Err: IRTraitType{Name: "Error"}}
		} else {
			ret = goTypeToIRWithVars(resultStr, vars)
		}
	}
	return IRFnType{Params: params, Ret: ret}, true
}

// goTypeToIRName converts a go/types type string to a short Go type name.
// "net/http.ResponseWriter" → "http.ResponseWriter"
// "github.com/labstack/echo/v5.Echo" → "echo.Echo"
// Pointer types should be handled by goTypeToIR before calling this.
func goTypeToIRName(goType string) string {
	dotIdx := strings.LastIndex(goType, ".")
	if dotIdx < 0 {
		return goType // no dot = primitive type ("int", "string", etc.)
	}
	pkgPath := goType[:dotIdx]
	typeName := goType[dotIdx+1:]
	shortPkg := NewGoPackage(pkgPath).ShortName
	return shortPkg + "." + typeName
}

// resolveReceiverGoType extracts the Go package and type name from an IR expression's type.
func (l *Lowerer) resolveReceiverGoType(expr IRExpr) (pkg, typ string, ok bool) {
	if expr == nil {
		return "", "", false
	}
	named, isNamed := peelToGoNamedType(expr.irType())
	if !isNamed || !strings.Contains(named.GoName, ".") {
		return "", "", false
	}
	parts := strings.SplitN(named.GoName, ".", 2)
	if goPkg, exists := l.lookupGoPackage(parts[0]); exists {
		return goPkg.FullPath, parts[1], true
	}
	return "", "", false
}

// peelToGoNamedType strips the common wrapper layers (Option, Ref, Pointer)
// off a receiver type to find the underlying IRNamedType. Go emit auto-derefs
// pointer method receivers, and Arca's Option/Ref add no runtime presence over
// `*T`, so method lookup and hint propagation through these wrappers matches
// what the generated Go actually does.
func peelToGoNamedType(t IRType) (IRNamedType, bool) {
	for {
		switch tt := t.(type) {
		case IRNamedType:
			return tt, true
		case IRPointerType:
			t = tt.Inner
		case IRRefType:
			t = tt.Inner
		case IROptionType:
			t = tt.Inner
		default:
			return IRNamedType{}, false
		}
	}
}

// monadicMethodInfo is a prelude-provided method on Result or Option. The
// slice below is the single source of truth for dispatch (tryDesugarMonadicMethod)
// and LSP completion (monadic method completion branch in lsp.go). Drift
// between the two paths is impossible because both read this table.
//
// LamArg returns the function-type hint for the method's lambda argument as
// a function of the receiver's resolved IR type (e.g. `.map` on Result[T, E]
// returns IRFnType{Params: [T]}). nil when the arg is not a lambda
// (e.g. okOr's `err: E`). Ret is left nil — the lambda body infers its own
// return type; only the param side needs hint propagation for inside-body
// completion / hover to work.
type monadicMethodInfo struct {
	Name      string
	Signature string // user-facing signature for LSP detail
	Build     func(recv Expr, args []Expr, pos Pos) Expr
	LamArg    func(recv IRType) IRType
}

var resultMonadicMethods = []monadicMethodInfo{
	{"map", "(f: T -> U) -> Result[U, E]", buildResultMapDesugar, resultLamArgOk},
	{"flatMap", "(f: T -> Result[U, E]) -> Result[U, E]", buildResultFlatMapDesugar, resultLamArgOk},
	{"mapError", "(f: E -> F) -> Result[T, F]", buildResultMapErrorDesugar, resultLamArgErr},
}

var optionMonadicMethods = []monadicMethodInfo{
	{"map", "(f: T -> U) -> Option[U]", buildOptionMapDesugar, optionLamArgInner},
	{"flatMap", "(f: T -> Option[U]) -> Option[U]", buildOptionFlatMapDesugar, optionLamArgInner},
	{"okOr", "(err: E) -> Result[T, E]", buildOptionOkOrDesugar, nil},
	{"okOrElse", "(fn: () -> E) -> Result[T, E]", buildOptionOkOrElseDesugar, optionLamArgNullary},
}

// resultLamArgOk yields the lambda-arg signature whose single param is the
// Result's Ok type (used by .map / .flatMap).
func resultLamArgOk(recv IRType) IRType {
	rt, ok := recv.(IRResultType)
	if !ok {
		return nil
	}
	return IRFnType{Params: []IRType{rt.Ok}}
}

// resultLamArgErr yields the lambda-arg signature whose single param is the
// Result's Err type (used by .mapError).
func resultLamArgErr(recv IRType) IRType {
	rt, ok := recv.(IRResultType)
	if !ok {
		return nil
	}
	return IRFnType{Params: []IRType{rt.Err}}
}

// optionLamArgInner yields the lambda-arg signature whose single param is the
// Option's inner type (used by .map / .flatMap).
func optionLamArgInner(recv IRType) IRType {
	ot, ok := recv.(IROptionType)
	if !ok {
		return nil
	}
	return IRFnType{Params: []IRType{ot.Inner}}
}

// optionLamArgNullary yields the no-arg lambda signature used by .okOrElse.
func optionLamArgNullary(recv IRType) IRType {
	return IRFnType{}
}

// monadicMethodsFor returns the method table for a receiver IR type, or nil
// if the type is not a monadic carrier.
func monadicMethodsFor(t IRType) []monadicMethodInfo {
	switch t.(type) {
	case IRResultType:
		return resultMonadicMethods
	case IROptionType:
		return optionMonadicMethods
	}
	return nil
}

// tryDesugarMonadicMethod detects monadic method calls on Result and Option
// and returns an equivalent match-based AST expression. Returns nil for
// non-monadic calls or unsupported methods.
//
// All methods desugar to match expressions so emit reuses existing
// Result/Option match lowering — no new IR nodes or emit code.
func (l *Lowerer) tryDesugarMonadicMethod(fa FieldAccess, call FnCall) Expr {
	recvIR := l.lowerExpr(fa.Expr)
	recvType := l.resolveDeep(recvIR.irType())
	methods := monadicMethodsFor(recvType)
	for _, m := range methods {
		if m.Name != fa.Field {
			continue
		}
		// Pre-type the lambda arg from the method's LamArg signature so its
		// body lowers against the concrete param type. Without this the
		// lambda flows into Build as plain AST and is later re-lowered with
		// a nil hint — untyped params → no scope IRType → LSP completion
		// inside `.map(x => x.)` misses the field list.
		args := call.Args
		if len(args) >= 1 && m.LamArg != nil {
			if lam, ok := args[0].(Lambda); ok {
				if hint := m.LamArg(recvType); hint != nil {
					args = append([]Expr(nil), args...)
					args[0] = l.applyLambdaHint(lam, hint)
				}
			}
		}
		desugared := m.Build(fa.Expr, args, call.Pos)
		// Synthetic monadic arms have no user-visible EndPos; give each a
		// generous line-local span so the arm scope registered by lowerMatch
		// actually contains any cursor inside the lambda body. Without this,
		// FindScopeAt cannot descend from the function scope into the Ok
		// arm's lambda scope and completion inside `.map(u => u.)` misses
		// `u`'s fields.
		if me, ok := desugared.(MatchExpr); ok {
			endPos := Pos{Line: call.Pos.Line + 100, Col: 10000}
			for i := range me.Arms {
				if me.Arms[i].EndPos == (Pos{}) {
					me.Arms[i].EndPos = endPos
				}
			}
			desugared = me
		}
		return desugared
	}
	return nil
}

// buildResultMapDesugar: r.map(f)  →
//   match r { Ok(v) => Ok(f(v)); Error(e) => Error(e) }
func buildResultMapDesugar(receiver Expr, args []Expr, pos Pos) Expr {
	if len(args) != 1 {
		return nil
	}
	f := args[0]
	vName := "__monadic_v"
	eName := "__monadic_e"
	vRef := Ident{Name: vName, NodePos: AtPos(pos)}
	eRef := Ident{Name: eName, NodePos: AtPos(pos)}
	apply := FnCall{NodePos: AtPos(pos), Fn: f, Args: []Expr{vRef}}
	okBody := ConstructorCall{NodePos: AtPos(pos), Name: "Ok", Fields: []FieldValue{{Value: apply}}}
	errBody := ConstructorCall{NodePos: AtPos(pos), Name: "Error", Fields: []FieldValue{{Value: eRef}}}
	return MatchExpr{
		NodePos: AtPos(pos),
		Subject: receiver,
		Arms: []MatchArm{
			{Pos: pos, Pattern: ConstructorPattern{Name: "Ok", Fields: []FieldPattern{{Binding: vName}}}, Body: okBody},
			{Pos: pos, Pattern: ConstructorPattern{Name: "Error", Fields: []FieldPattern{{Binding: eName}}}, Body: errBody},
		},
	}
}

// buildResultFlatMapDesugar: r.flatMap(f)  →
//   match r { Ok(v) => f(v); Error(e) => Error(e) }
func buildResultFlatMapDesugar(receiver Expr, args []Expr, pos Pos) Expr {
	if len(args) != 1 {
		return nil
	}
	f := args[0]
	vName := "__monadic_v"
	eName := "__monadic_e"
	vRef := Ident{Name: vName, NodePos: AtPos(pos)}
	eRef := Ident{Name: eName, NodePos: AtPos(pos)}
	apply := FnCall{NodePos: AtPos(pos), Fn: f, Args: []Expr{vRef}}
	errBody := ConstructorCall{NodePos: AtPos(pos), Name: "Error", Fields: []FieldValue{{Value: eRef}}}
	return MatchExpr{
		NodePos: AtPos(pos),
		Subject: receiver,
		Arms: []MatchArm{
			{Pos: pos, Pattern: ConstructorPattern{Name: "Ok", Fields: []FieldPattern{{Binding: vName}}}, Body: apply},
			{Pos: pos, Pattern: ConstructorPattern{Name: "Error", Fields: []FieldPattern{{Binding: eName}}}, Body: errBody},
		},
	}
}

// buildResultMapErrorDesugar: r.mapError(f)  →
//   match r { Ok(v) => Ok(v); Error(e) => Error(f(e)) }
func buildResultMapErrorDesugar(receiver Expr, args []Expr, pos Pos) Expr {
	if len(args) != 1 {
		return nil
	}
	f := args[0]
	vName := "__monadic_v"
	eName := "__monadic_e"
	vRef := Ident{Name: vName, NodePos: AtPos(pos)}
	eRef := Ident{Name: eName, NodePos: AtPos(pos)}
	apply := FnCall{NodePos: AtPos(pos), Fn: f, Args: []Expr{eRef}}
	okBody := ConstructorCall{NodePos: AtPos(pos), Name: "Ok", Fields: []FieldValue{{Value: vRef}}}
	errBody := ConstructorCall{NodePos: AtPos(pos), Name: "Error", Fields: []FieldValue{{Value: apply}}}
	return MatchExpr{
		NodePos: AtPos(pos),
		Subject: receiver,
		Arms: []MatchArm{
			{Pos: pos, Pattern: ConstructorPattern{Name: "Ok", Fields: []FieldPattern{{Binding: vName}}}, Body: okBody},
			{Pos: pos, Pattern: ConstructorPattern{Name: "Error", Fields: []FieldPattern{{Binding: eName}}}, Body: errBody},
		},
	}
}

// buildOptionMapDesugar: opt.map(f)  →
//   match opt { Some(v) => Some(f(v)); None => None }
func buildOptionMapDesugar(receiver Expr, args []Expr, pos Pos) Expr {
	if len(args) != 1 {
		return nil
	}
	f := args[0]
	vName := "__monadic_v"
	vRef := Ident{Name: vName, NodePos: AtPos(pos)}
	apply := FnCall{NodePos: AtPos(pos), Fn: f, Args: []Expr{vRef}}
	someBody := ConstructorCall{NodePos: AtPos(pos), Name: "Some", Fields: []FieldValue{{Value: apply}}}
	noneBody := ConstructorCall{NodePos: AtPos(pos), Name: "None"}
	return MatchExpr{
		NodePos: AtPos(pos),
		Subject: receiver,
		Arms: []MatchArm{
			{Pos: pos, Pattern: ConstructorPattern{Name: "Some", Fields: []FieldPattern{{Binding: vName}}}, Body: someBody},
			{Pos: pos, Pattern: ConstructorPattern{Name: "None"}, Body: noneBody},
		},
	}
}

// buildOptionFlatMapDesugar: opt.flatMap(f)  →
//   match opt { Some(v) => f(v); None => None }
func buildOptionFlatMapDesugar(receiver Expr, args []Expr, pos Pos) Expr {
	if len(args) != 1 {
		return nil
	}
	f := args[0]
	vName := "__monadic_v"
	vRef := Ident{Name: vName, NodePos: AtPos(pos)}
	apply := FnCall{NodePos: AtPos(pos), Fn: f, Args: []Expr{vRef}}
	noneBody := ConstructorCall{NodePos: AtPos(pos), Name: "None"}
	return MatchExpr{
		NodePos: AtPos(pos),
		Subject: receiver,
		Arms: []MatchArm{
			{Pos: pos, Pattern: ConstructorPattern{Name: "Some", Fields: []FieldPattern{{Binding: vName}}}, Body: apply},
			{Pos: pos, Pattern: ConstructorPattern{Name: "None"}, Body: noneBody},
		},
	}
}

// buildOptionOkOrDesugar: opt.okOr(err)  →
//   match opt { Some(v) => Ok(v); None => Error(err) }
func buildOptionOkOrDesugar(receiver Expr, args []Expr, pos Pos) Expr {
	if len(args) != 1 {
		return nil
	}
	err := args[0]
	vName := "__monadic_v"
	vRef := Ident{Name: vName, NodePos: AtPos(pos)}
	okBody := ConstructorCall{NodePos: AtPos(pos), Name: "Ok", Fields: []FieldValue{{Value: vRef}}}
	errBody := ConstructorCall{NodePos: AtPos(pos), Name: "Error", Fields: []FieldValue{{Value: err}}}
	return MatchExpr{
		NodePos: AtPos(pos),
		Subject: receiver,
		Arms: []MatchArm{
			{Pos: pos, Pattern: ConstructorPattern{Name: "Some", Fields: []FieldPattern{{Binding: vName}}}, Body: okBody},
			{Pos: pos, Pattern: ConstructorPattern{Name: "None"}, Body: errBody},
		},
	}
}

// buildOptionOkOrElseDesugar: opt.okOrElse(fn)  →
//   match opt { Some(v) => Ok(v); None => Error(fn()) }
func buildOptionOkOrElseDesugar(receiver Expr, args []Expr, pos Pos) Expr {
	if len(args) != 1 {
		return nil
	}
	fn := args[0]
	vName := "__monadic_v"
	vRef := Ident{Name: vName, NodePos: AtPos(pos)}
	okBody := ConstructorCall{NodePos: AtPos(pos), Name: "Ok", Fields: []FieldValue{{Value: vRef}}}
	call := FnCall{NodePos: AtPos(pos), Fn: fn, Args: nil}
	errBody := ConstructorCall{NodePos: AtPos(pos), Name: "Error", Fields: []FieldValue{{Value: call}}}
	return MatchExpr{
		NodePos: AtPos(pos),
		Subject: receiver,
		Arms: []MatchArm{
			{Pos: pos, Pattern: ConstructorPattern{Name: "Some", Fields: []FieldPattern{{Binding: vName}}}, Body: okBody},
			{Pos: pos, Pattern: ConstructorPattern{Name: "None"}, Body: errBody},
		},
	}
}

// resolveMethodReturnType resolves the return type of a method call on a Go or Arca type.
func (l *Lowerer) resolveMethodReturnType(receiver IRExpr, method string, pos Pos) goReturnInfo {
	// Trait object: receiver's static type is IRTraitType → look up in trait's method set.
	if tt, ok := receiver.irType().(IRTraitType); ok {
		if trait, ok := l.traits[tt.Name]; ok {
			if info := l.lookupArcaMethodReturn(trait.Methods, method); info != nil {
				return *info
			}
		}
	}

	// Try Go FFI type first. Go method names are PascalCase and Arca's de
	// facto FFI convention writes them verbatim (`fmt.Println`, `sql.Open`,
	// etc.); a camelCase form like `res.lastInsertId()` doesn't match any
	// Go method, so the lookup misses, the return type falls to Any, and
	// downstream type-mismatch diagnostics evaporate. Detect that case and
	// surface ErrGoFFINameConvention with the PascalCase suggestion — and
	// return the resolved type so cascade type-checking stays informed.
	pkg, typ, ok := l.resolveReceiverGoType(receiver)
	if ok {
		if info := l.typeResolver.ResolveMethod(pkg, typ, method); info != nil {
			return l.goFuncReturnType(info)
		}
		if pascal := capitalize(method); pascal != method {
			if info := l.typeResolver.ResolveMethod(pkg, typ, pascal); info != nil {
				l.addCompileError(ErrGoFFINameConvention, pos, GoFFINameConventionData{
					Given: method, Expected: pascal, Kind: "method", Receiver: typ,
				})
				return l.goFuncReturnType(info)
			}
		}
	}

	// Try Arca type
	arcaTypeName := l.resolveReceiverArcaType(receiver)
	if arcaTypeName != "" {
		if td, ok := l.types[arcaTypeName]; ok {
			if info := l.lookupArcaMethodReturn(td.Methods, method); info != nil {
				return *info
			}
		}
		// Trait-impl methods on the same concrete type.
		for _, impl := range l.impls[arcaTypeName] {
			if info := l.lookupArcaMethodReturn(impl.Methods, method); info != nil {
				return *info
			}
		}
		// B2f: synthesised `(d <Host>Draft) freeze()` for derive Bindable hosts.
		if method == "freeze" && strings.HasSuffix(arcaTypeName, "Draft") {
			host := strings.TrimSuffix(arcaTypeName, "Draft")
			if l.bindableTypes[host] {
				return goReturnInfo{
					Type:          IRResultType{Ok: IRNamedType{GoName: host}, Err: IRNamedType{GoName: "error"}},
					GoMultiReturn: true,
				}
			}
		}
	}

	return goReturnInfo{Type: IRInterfaceType{}}
}

// lookupArcaMethodReturn scans Arca method declarations for a matching name
// (identity + snakeToCamel/Pascal variants) and returns the lowered return-type info.
func (l *Lowerer) lookupArcaMethodReturn(methods []FnDecl, method string) *goReturnInfo {
	for _, m := range methods {
		if m.Name != method && snakeToCamel(m.Name) != method && snakeToPascal(m.Name) != method {
			continue
		}
		if m.ReturnType == nil {
			info := goReturnInfo{Type: IRNamedType{GoName: "struct{}"}}
			return &info
		}
		info := goReturnInfo{Type: l.lowerType(m.ReturnType)}
		return &info
	}
	return nil
}

// resolveFieldType resolves the type of a field access on a Go or Arca type.
func (l *Lowerer) resolveFieldType(receiver IRExpr, field string, pos Pos) IRType {
	// Try Go FFI type first. Go struct fields of type *T wrap to
	// Option[Ref[T]] at the Arca boundary (same rule as return/param).
	pkg, typ, ok := l.resolveReceiverGoType(receiver)
	if ok {
		typeInfo := l.typeResolver.ResolveType(pkg, typ)
		if typeInfo != nil {
			for _, f := range typeInfo.Fields {
				if f.Name == field {
					return wrapPointerInOption(goTypeToIR(f.Type))
				}
			}
			// Go FFI type resolved but no field — receiver-side method
			// access via field-access form is left to Go's compiler (Go
			// allows method-value expressions). Fall through to Any.
		}
	}

	// Try Arca type
	arcaTypeName := l.resolveReceiverArcaType(receiver)
	if arcaTypeName != "" {
		if td, ok := l.types[arcaTypeName]; ok {
			// field is capitalized Go name, findField uses Arca name
			for _, ctor := range td.Constructors {
				for i, f := range ctor.Fields {
					if capitalize(f.Name) == field {
						return l.lowerType(ctor.Fields[i].Type)
					}
				}
			}
			// No field — check methods (silent-bug guard for `e.message`
			// when message is a method).
			if hint, sig, ok := l.lookupMethodAsField(td.Methods, field); ok {
				l.addCompileError(ErrMethodAccessAsField, pos, MethodAccessAsFieldData{
					Method: lowerFirst(field), TypeName: arcaTypeName, Signature: sig,
				})
				return IRError{Reason: fmt.Sprintf("method '%s' on %s called as field", field, arcaTypeName), Hint: hint}
			}
			l.addCompileError(ErrUnknownMember, pos, UnknownMemberData{Member: lowerFirst(field), TypeName: arcaTypeName})
			return IRError{Reason: fmt.Sprintf("no field or method '%s' on %s", field, arcaTypeName)}
		}
	}

	// Trait receivers: methods are the only members; same method-as-field
	// guard but routed through the trait's method set.
	if tt, ok := receiver.irType().(IRTraitType); ok {
		if trait, ok := l.traits[tt.Name]; ok {
			if hint, sig, ok := l.lookupMethodAsField(trait.Methods, field); ok {
				l.addCompileError(ErrMethodAccessAsField, pos, MethodAccessAsFieldData{
					Method: lowerFirst(field), TypeName: tt.Name, Signature: sig,
				})
				return IRError{Reason: fmt.Sprintf("method '%s' on trait %s called as field", field, tt.Name), Hint: hint}
			}
			l.addCompileError(ErrUnknownMember, pos, UnknownMemberData{Member: lowerFirst(field), TypeName: tt.Name})
			return IRError{Reason: fmt.Sprintf("no member '%s' on trait %s", field, tt.Name)}
		}
	}

	// Receiver type itself is unresolved or opaque (e.g. real interface{}):
	// stay silent so partial / under-typed code keeps lowering for LSP.
	return IRInterfaceType{}
}

// lookupMethodAsField scans an Arca method list for a name matching the
// requested field (capitalized via lowerFieldAccess) and, on a hit, returns
// (1) an IRFnType representing the method's signature for IRError.Hint and
// (2) an Arca-style signature display string for the diagnostic.
func (l *Lowerer) lookupMethodAsField(methods []FnDecl, field string) (IRType, string, bool) {
	for _, m := range methods {
		if capitalize(m.Name) != field && m.Name != field {
			continue
		}
		paramTypes := make([]IRType, len(m.Params))
		paramNames := make([]string, len(m.Params))
		for i, p := range m.Params {
			if p.Type != nil {
				paramTypes[i] = l.lowerType(p.Type)
			} else {
				paramTypes[i] = IRInterfaceType{}
			}
			paramNames[i] = irTypeEmitStr(paramTypes[i])
		}
		var ret IRType
		if m.ReturnType != nil {
			ret = l.lowerType(m.ReturnType)
		}
		retStr := ""
		if ret != nil {
			retStr = " -> " + irTypeEmitStr(ret)
		}
		sig := "(" + strings.Join(paramNames, ", ") + ")" + retStr
		return IRFnType{Params: paramTypes, Ret: ret}, sig, true
	}
	return nil, "", false
}

// resolveReceiverArcaType extracts the Arca type name from an IR expression.
// Peels Ref[T] / *T so field and method access auto-deref the way SPEC.md
// describes.
func (l *Lowerer) resolveReceiverArcaType(expr IRExpr) string {
	irType := expr.irType()
	for {
		switch tt := irType.(type) {
		case IRRefType:
			irType = tt.Inner
			continue
		case IRPointerType:
			irType = tt.Inner
			continue
		}
		break
	}
	if irType == nil {
		return ""
	}
	if named, ok := irType.(IRNamedType); ok {
		if !strings.Contains(named.GoName, ".") {
			return named.GoName
		}
		// Stdlib-shared compiler-known types (BindableDict, BindableSlot)
		// are registered in l.types under their simple name and emitted as
		// `stdlib.<Name>` in Go. Field/method access goes through the Arca
		// path so the type-param substitution machinery can fill in T / B.
		if strings.HasPrefix(named.GoName, "stdlib.") {
			suffix := strings.TrimPrefix(named.GoName, "stdlib.")
			if _, ok := l.types[suffix]; ok {
				return suffix
			}
		}
	}
	return ""
}

// lowerCallArgs lowers function call arguments with context-aware type coercion.
func (l *Lowerer) lowerCallArgs(e FnCall) []IRExpr {
	args := make([]IRExpr, len(e.Args))
	for i, a := range e.Args {
		args[i] = l.lowerArgWithContext(a, e, i)
	}
	return args
}

// lowerArgWithContext handles type alias coercion and empty list/None resolution.
func (l *Lowerer) lowerArgWithContext(expr Expr, call FnCall, argIndex int) IRExpr {
	fnIdent, isFnIdent := call.Fn.(Ident)

	// Empty list with resolvable element type
	if ll, ok := expr.(ListLit); ok && len(ll.Elements) == 0 && ll.Spread == nil {
		if isFnIdent {
			if fn, ok := l.functions[fnIdent.Name]; ok && argIndex < len(fn.Params) {
				if nt, ok := fn.Params[argIndex].Type.(NamedType); ok && nt.Name == "List" && len(nt.Params) > 0 {
					elemType := l.lowerType(nt.Params[0])
					return IRListLit{
						ElemType: irTypeEmitStr(elemType),
						Type:     IRListType{Elem: elemType},
					}
				}
			}
		}
	}

	// None with resolvable inner type
	if ident, ok := expr.(Ident); ok && ident.Name == "None" {
		if isFnIdent {
			if fn, ok := l.functions[fnIdent.Name]; ok && argIndex < len(fn.Params) {
				if nt, ok := fn.Params[argIndex].Type.(NamedType); ok && nt.Name == "Option" && len(nt.Params) > 0 {
					innerType := l.lowerType(nt.Params[0])
					return IRNoneExpr{
						TypeArg: "[" + irTypeEmitStr(innerType) + "]",
						Type:    IROptionType{Inner: innerType},
					}
				}
			}
		}
	}

	// Type alias parameter coercion with constraint compatibility check
	if isFnIdent {
		if fn, ok := l.functions[fnIdent.Name]; ok && argIndex < len(fn.Params) {
			if pnt, ok := fn.Params[argIndex].Type.(NamedType); ok {
				if _, isAlias := l.typeAliases[pnt.Name]; isAlias {
					inner := l.lowerExpr(expr)
					// Check constraint compatibility before coercion
					innerType := inner.irType()
					if named, ok := innerType.(IRNamedType); ok {
						if named.GoName != pnt.Name && !l.isConstraintCompatible(named.GoName, pnt.Name) {
							l.addCompileError(ErrTypeMismatch, call.Pos, TypeMismatchData{Expected: pnt.Name, Actual: named.GoName})
						}
					}
					return IRFnCall{
						Fn:   IRIdent{GoName: pnt.Name},
						Args: []IRExpr{inner},
						Type: IRNamedType{GoName: pnt.Name},
					}
				}
			}
		}
	}

	// Resolve the expected IR type of this arg. Covers Arca functions
	// (l.functions), Go FFI package-level calls (typeResolver.ResolveFunc),
	// and Go methods on a receiver (typeResolver.ResolveMethod). Lambdas in
	// any of those positions get their param types from the resulting
	// IRFnType via lowerLambdaHint — no per-call site special-casing.
	hint := l.resolveCallParamIRType(call, argIndex)
	return l.lowerExprHint(expr, hint)
}

// resolveCallParamIRType returns the IR type expected at argIndex of call,
// driving hint-based arg lowering. Supports three call shapes:
//   - Arca user function (call.Fn is a name in l.functions)
//   - Go FFI package-level function (call.Fn is `pkg.Func`)
//   - Go method on a receiver (call.Fn is `expr.method` on a non-package receiver)
//
// Go param types surface as IR types: pointer params wrap to Option[Ref[T]]
// and `func(...)` params — including type aliases that resolve to a func
// signature, e.g. echo.HandlerFunc — surface as IRFnType so lambda args
// get hint-driven param inference uniformly.
func (l *Lowerer) resolveCallParamIRType(call FnCall, argIndex int) IRType {
	if fnIdent, ok := call.Fn.(Ident); ok {
		if fn, ok := l.functions[fnIdent.Name]; ok && argIndex < len(fn.Params) {
			return l.lowerType(fn.Params[argIndex].Type)
		}
	}
	fa, ok := call.Fn.(FieldAccess)
	if !ok {
		return nil
	}
	if ident, ok := fa.Expr.(Ident); ok {
		// Arca submodule-qualified call (`math.add(...)`) resolves through its
		// own dispatch; no FFI hint is available and lowering the module name
		// as an expression would raise a spurious "undefined variable".
		if l.moduleNames[ident.Name] {
			return nil
		}
		if goPkg, isGoPkg := l.lookupGoPackage(ident.Name); isGoPkg {
			if info := l.typeResolver.ResolveFunc(goPkg.FullPath, fa.Field); info != nil {
				goCallName := ident.Name + "." + fa.Field
				// B3a: stdlib Bindable helpers prepend a hidden dict at
				// param[0], so the user's argIndex maps to params[argIndex+1].
				if bindableStdlibFns[goCallName] {
					argIndex++
				}
				return l.goParamTypeHint(resolveGoParamGoType(info, argIndex))
			}
			return nil
		}
	}
	receiver := l.lowerExpr(fa.Expr)
	pkg, typ, ok := l.resolveReceiverGoType(receiver)
	if !ok {
		return nil
	}
	if info := l.typeResolver.ResolveMethod(pkg, typ, fa.Field); info != nil {
		return l.goParamTypeHint(resolveGoParamGoType(info, argIndex))
	}
	return nil
}

// goParamTypeHint converts a Go param type string to an IR hint, resolving
// type aliases whose underlying type is `func(...)` (e.g. echo.HandlerFunc)
// so the alias stops at IRFnType and lambda param inference sees the real
// signature. Non-func types stay as they are.
func (l *Lowerer) goParamTypeHint(goType string) IRType {
	if goType == "" {
		return nil
	}
	if !strings.HasPrefix(goType, "func(") {
		if resolved := l.typeResolver.ResolveUnderlying(goType); strings.HasPrefix(resolved, "func(") {
			goType = resolved
		}
	}
	return wrapPointerInOption(goTypeToIR(goType))
}

// resolveGoParamGoType returns the effective Go type string for the arg at
// argIndex of a Go function, peeling the variadic slice for trailing args.
// Empty string when argIndex is out of range without a variadic fallback,
// or when the param is an `any` / `interface{}` slot (no useful hint).
func resolveGoParamGoType(info *FuncInfo, argIndex int) string {
	if len(info.Params) == 0 {
		return ""
	}
	var raw string
	if info.Variadic && argIndex >= len(info.Params)-1 {
		last := info.Params[len(info.Params)-1].Type
		if strings.HasPrefix(last, "[]") {
			raw = last[2:]
		} else {
			raw = last
		}
	} else if argIndex < len(info.Params) {
		raw = info.Params[argIndex].Type
	}
	if raw == "any" || raw == "interface{}" {
		return ""
	}
	return raw
}

func (l *Lowerer) lowerFieldAccess(e FieldAccess) IRExpr {
	receiver := l.lowerExpr(e.Expr)
	// Check for field access on Result/Option (likely missing ?)
	if rt := receiver.irType(); rt != nil {
		if _, ok := rt.(IRResultType); ok {
			if ident, ok := e.Expr.(Ident); ok {
				l.addCompileError(ErrFieldAccessOnResult, ident.Pos, FieldAccessOnWrappedData{Field: e.Field, TypeName: "Result", Suggestion: "Use ? to unwrap first"})
			}
		}
		if _, ok := rt.(IROptionType); ok {
			if ident, ok := e.Expr.(Ident); ok {
				l.addCompileError(ErrFieldAccessOnOption, ident.Pos, FieldAccessOnWrappedData{Field: e.Field, TypeName: "Option", Suggestion: "Use match to unwrap first"})
			}
		}
	}
	fieldType := l.resolveFieldType(receiver, capitalize(e.Field), e.exprPos())
	return IRFieldAccess{
		Expr:  receiver,
		Field: capitalize(e.Field),
		Type:  fieldType,
	}
}

func (l *Lowerer) lowerIfExpr(e IfExpr) IRExpr {
	cond := l.lowerExpr(e.Cond)
	then := l.lowerExpr(e.Then)
	var elseBody IRExpr
	if e.Else != nil {
		elseBody = l.lowerExpr(e.Else)
	}
	// Unify then/else types. When the if is used in value position without
	// an outer hint (e.g. `let x = if ...`) this is the only type-mismatch
	// check, so it must report. The Else's own position anchors the error
	// at the branch that disagreed with the Then branch.
	var typ IRType = then.irType()
	if elseBody != nil {
		elsePos := e.Else.exprPos()
		l.unify(typ, elseBody.irType(), elsePos)
		typ = l.resolveDeep(typ)
	}
	return IRIfExpr{Cond: cond, Then: then, Else: elseBody, Type: typ}
}

func (l *Lowerer) lowerIndexAccess(e IndexAccess) IRExpr {
	expr := l.lowerExpr(e.Expr)
	index := l.lowerExpr(e.Index)
	// Infer element type from list type
	var elemType IRType = IRInterfaceType{}
	if lt, ok := expr.irType().(IRListType); ok {
		elemType = lt.Elem
	}
	return IRIndexAccess{Expr: expr, Index: index, Type: elemType}
}

func (l *Lowerer) lowerConstructorCallHint(e ConstructorCall, hint IRType) IRExpr {
	// Built-in Result constructors
	if e.Name == "Ok" && len(e.Fields) == 1 {
		return l.lowerOkCall(e.Fields[0].Value, hint)
	}
	if e.Name == "Error" && len(e.Fields) == 1 {
		return l.lowerErrorCall(e.Fields[0].Value, hint)
	}
	// Built-in Option constructors
	if e.Name == "Some" && len(e.Fields) == 1 {
		return l.lowerSomeCall(e.Fields[0].Value)
	}
	if e.Name == "None" {
		return l.lowerNoneCall(hint)
	}

	return l.lowerUserConstructorCall(e)
}

// resolveResultTypeArgs computes the type args string for a Result constructor,
// resolving type variables via the current infer scope.
func (l *Lowerer) resolveResultTypeArgs(rt IRResultType) string {
	if ta := l.resultTypeArgs(); ta != "" {
		return ta
	}
	resolved := l.resolveDeep(rt).(IRResultType)
	return "[" + irTypeEmitStr(resolved.Ok) + ", " + irTypeEmitStr(resolved.Err) + "]"
}

// hintResultType extracts Ok/Err types from a Result hint, or defaults.
// defaultOk is used when hint is not a Result. errorType is always `error`.
func (l *Lowerer) hintResultType(hint IRType, defaultOk IRType) (okType, errType IRType) {
	errType = IRNamedType{GoName: "error"}
	okType = defaultOk
	if rt, ok := hint.(IRResultType); ok {
		okType = rt.Ok
		errType = rt.Err
	}
	return
}

func (l *Lowerer) lowerOkCall(valExpr Expr, hint IRType) IRExpr {
	l.builtins["result"] = true
	var valHint IRType
	if rt, ok := hint.(IRResultType); ok {
		valHint = rt.Ok
	}
	val := l.lowerExprHint(valExpr, valHint)
	okType, errType := l.hintResultType(hint, val.irType())
	if _, isIface := okType.(IRInterfaceType); isIface {
		okType = l.freshTypeVar()
	}
	// Raw substitution only. When okType came from a concrete hint and
	// val.irType() doesn't match, lowerExprHint(valExpr, valHint) above
	// already reported the mismatch at valExpr's position — reporting here
	// too would emit a duplicate at Pos{0,0}.
	l.infer.unify(okType, val.irType())
	rt := IRResultType{Ok: okType, Err: errType}
	return IROkCall{Value: val, TypeArgs: l.resolveResultTypeArgs(rt), Type: rt}
}

func (l *Lowerer) lowerErrorCall(valExpr Expr, hint IRType) IRExpr {
	l.builtins["result"] = true
	var valHint IRType
	if rt, ok := hint.(IRResultType); ok {
		valHint = rt.Err
	}
	val := l.lowerExprHint(valExpr, valHint)
	okType, errType := l.hintResultType(hint, l.freshTypeVar())
	rt := IRResultType{Ok: okType, Err: errType}
	return IRErrorCall{Value: val, TypeArgs: l.resolveResultTypeArgs(rt), Type: rt}
}

func (l *Lowerer) lowerSomeCall(valExpr Expr) IRExpr {
	l.builtins["option"] = true
	val := l.lowerExpr(valExpr)
	return IRSomeCall{Value: val, Type: IROptionType{Inner: val.irType()}}
}

func (l *Lowerer) lowerNoneCall(hint IRType) IRExpr {
	l.builtins["option"] = true
	innerType := IRType(l.freshTypeVar())
	if ot, ok := hint.(IROptionType); ok {
		// Cannot fail: innerType is a fresh type var we just allocated.
		l.infer.unify(innerType, ot.Inner)
		innerType = ot.Inner
	}
	resolved := l.resolveDeep(innerType)
	return IRNoneExpr{
		TypeArg: "[" + irTypeEmitStr(resolved) + "]",
		Type:    IROptionType{Inner: innerType},
	}
}

func (l *Lowerer) lowerUserConstructorCall(cc ConstructorCall) IRExpr {
	typeName := cc.TypeName
	if typeName == "Self" && l.currentTypeName != "" {
		typeName = l.currentTypeName
	}

	var td TypeDecl
	var found bool

	if typeName != "" {
		td, found = l.types[typeName]
	} else {
		for tn, t := range l.types {
			for _, ctor := range t.Constructors {
				if ctor.Name == cc.Name {
					typeName = tn
					td = t
					found = true
					break
				}
			}
			if found {
				break
			}
		}
	}

	if found {
		// Enum variant
		if isEnum(td) {
			return IRIdent{
				GoName: typeName + cc.Name,
				Type:   IRNamedType{GoName: typeName},
			}
		}

		// Find matching constructor's field definitions
		var ctorFields []Field
		for _, c := range td.Constructors {
			if c.Name == cc.Name || (len(td.Constructors) == 1) {
				ctorFields = c.Fields
				break
			}
		}

		// Validate field count
		if len(cc.Fields) != len(ctorFields) {
			l.addCompileError(ErrWrongFieldCount, cc.Pos, WrongArgCountData{
				Func: cc.Name, Expected: len(ctorFields), Actual: len(cc.Fields),
			})
		}
		// Validate field names
		for _, fv := range cc.Fields {
			if fv.Name != "" {
				found := false
				for _, cf := range ctorFields {
					if cf.Name == fv.Name {
						found = true
						break
					}
				}
				if !found {
					l.addCompileError(ErrUnknownField, cc.Pos, MessageData{
						Text: fmt.Sprintf("constructor %s has no field named '%s'", cc.Name, fv.Name),
					})
				}
			}
		}

		goName := typeName
		if len(td.Constructors) > 1 {
			goName = typeName + cc.Name
		}

		// Constrained type constructor: NewType returns (T, error)
		if l.hasConstraints(td) {
			l.builtins["fmt"] = true
			fields := l.lowerFieldValuesWithTypes(cc.Fields, ctorFields, cc.Pos)
			return IRConstructorCall{
				GoName:        "New" + goName,
				Fields:        fields,
				GoMultiReturn: true,
				Type:          IRResultType{Ok: IRNamedType{GoName: goName}, Err: IRNamedType{GoName: "error"}},
				Source:        SourceInfo{Pos: cc.Pos, Name: cc.Name, TypeName: typeName},
			}
		}

		// Instantiate generic type params with fresh type vars per call so
		// two independent constructor calls don't share substitution
		// entries. For non-generic types vars is nil and lowerTypeWithVars
		// falls through to plain lowerType.
		vars := l.instantiateGenericType(td)
		hints := make([]IRType, len(ctorFields))
		for i, cf := range ctorFields {
			hints[i] = l.lowerTypeWithVars(cf.Type, vars)
		}
		// Arg unification happens inside lowerFieldValuesWithHints via
		// checkTypeHint → unify, so the fresh type vars bind during the
		// lowerExprHint pass. No explicit post-loop needed.
		fields := l.lowerFieldValuesWithHints(cc.Fields, hints)

		typeArgs := ""
		if len(td.Params) > 0 {
			args := make([]string, len(td.Params))
			for i, p := range td.Params {
				args[i] = irTypeEmitStr(l.resolveDeep(vars[p]))
			}
			typeArgs = "[" + strings.Join(args, ", ") + "]"
		}
		return IRConstructorCall{
			GoName:   goName,
			Fields:   fields,
			TypeArgs: typeArgs,
			Type:     IRNamedType{GoName: typeName},
			Source:   SourceInfo{Pos: cc.Pos, Name: cc.Name, TypeName: typeName},
		}
	}

	// Type alias constructor
	if alias, ok := l.typeAliases[cc.Name]; ok {
		fields := l.lowerFieldValues(cc.Fields)
		if nt, ok := alias.Type.(NamedType); ok && len(nt.Constraints) > 0 {
			l.builtins["fmt"] = true
			return IRConstructorCall{
				GoName:        "New" + cc.Name,
				Fields:        fields,
				GoMultiReturn: true,
				Type:          IRResultType{Ok: IRNamedType{GoName: cc.Name}, Err: IRNamedType{GoName: "error"}},
				Source:        SourceInfo{Pos: cc.Pos, Name: cc.Name, TypeName: cc.Name},
			}
		}
		// Unconstrained alias: simple type conversion
		return IRConstructorCall{
			GoName: cc.Name,
			Fields: fields,
			Type:   IRNamedType{GoName: cc.Name},
			Source: SourceInfo{Pos: cc.Pos, Name: cc.Name, TypeName: cc.Name},
		}
	}

	// Unknown constructor
	l.addCompileError(ErrUnknownType, cc.Pos, UnknownTypeData{Name: cc.Name})
	return IRConstructorCall{
		GoName: cc.Name,
		Fields: l.lowerFieldValues(cc.Fields),
		Type:   IRInterfaceType{},
		Source: SourceInfo{Pos: cc.Pos, Name: cc.Name},
	}
}

func (l *Lowerer) lowerFieldValues(fields []FieldValue) []IRFieldValue {
	return l.lowerFieldValuesWithTypes(fields, nil, Pos{})
}

// lowerFieldValuesWithTypes lowers field values with hint-based type checking
// against the expected constructor field types.
func (l *Lowerer) lowerFieldValuesWithTypes(fields []FieldValue, ctorFields []Field, ctorPos Pos) []IRFieldValue {
	hints := make([]IRType, len(ctorFields))
	for i, cf := range ctorFields {
		hints[i] = l.lowerType(cf.Type)
	}
	return l.lowerFieldValuesWithHints(fields, hints)
}

// lowerFieldValuesWithHints lowers field values against pre-lowered hint
// types. Used by generic constructor calls so the hints can carry type-var
// substitutions from instantiateGenericType.
func (l *Lowerer) lowerFieldValuesWithHints(fields []FieldValue, hints []IRType) []IRFieldValue {
	result := make([]IRFieldValue, len(fields))
	for i, f := range fields {
		goName := ""
		if f.Name != "" {
			goName = capitalize(f.Name)
		}
		var hint IRType
		if i < len(hints) {
			hint = hints[i]
		}
		value := l.lowerExprHint(f.Value, hint)
		result[i] = IRFieldValue{
			GoName: goName,
			Value:  value,
			Source: SourceInfo{Name: f.Name},
		}
	}
	return result
}

func (l *Lowerer) lowerBlock(b Block) IRExpr {
	return l.lowerBlockHint(b, nil)
}

func (l *Lowerer) lowerBlockHint(b Block, hint IRType) IRExpr {
	// Empty block {} with a Map hint → empty map literal
	if len(b.Stmts) == 0 && b.Expr == nil {
		if _, ok := hint.(IRMapType); ok {
			return l.lowerMapLitHint(MapLit{NodePos: b.NodePos}, hint)
		}
	}
	stmts := make([]IRStmt, 0, len(b.Stmts))
	for _, s := range b.Stmts {
		stmts = append(stmts, l.lowerStmt(s)...)
	}
	var expr IRExpr
	var blockType IRType = IRInterfaceType{}
	if b.Expr != nil {
		// Hint applies to the block's final expression (return value)
		expr = l.lowerExprHint(b.Expr, hint)
		if t := expr.irType(); t != nil {
			blockType = t
		}
	}
	return IRBlock{
		Stmts: stmts,
		Expr:  expr,
		Type:  blockType,
	}
}

func (l *Lowerer) lowerTryBlock(tb TryBlockExpr) IRExpr {
	prevRet := l.currentRetType
	prevIRRet := l.currentIRRetType

	// Fresh type var for Ok — will be unified with final expr type
	okVar := l.freshTypeVar()
	errType := IRNamedType{GoName: "error"}
	l.currentIRRetType = IRResultType{Ok: okVar, Err: errType}
	// currentRetType stays as-is (not Result); lowerTryLetStmt uses currentIRRetType

	stmts := make([]IRStmt, 0, len(tb.Body.Stmts))
	for _, s := range tb.Body.Stmts {
		stmts = append(stmts, l.lowerStmt(s)...)
	}

	var expr IRExpr
	okType := IRType(IRNamedType{GoName: "struct{}"})
	if tb.Body.Expr != nil {
		inner := l.lowerExpr(tb.Body.Expr)
		if t := inner.irType(); t != nil {
			l.unify(okVar, t, tb.Pos)
			okType = l.resolveDeep(okVar)
		}
		// Wrap in IROkCall so stage2's wrapTail produces GoReturn{val, nil}.
		resultType := IRResultType{Ok: okType, Err: errType}
		expr = IROkCall{Value: inner, Type: resultType}
	}

	l.currentRetType = prevRet
	l.currentIRRetType = prevIRRet
	l.builtins["result"] = true

	return IRTryBlock{
		Stmts:   stmts,
		Expr:    expr,
		OkType:  okType,
		ErrType: errType,
	}
}

// lowerLambdaHint propagates an IRFnType hint into an untyped lambda so its
// body lowers against concrete param types (enabling type-dependent
// resolution — method / field lookup, hover, completion). Typed params on
// the AST lambda are respected; only nil slots are filled from the hint.
// Non-fn-type hints fall through to plain lowerLambda.
func (l *Lowerer) lowerLambdaHint(lam Lambda, hint IRType) IRExpr {
	return l.lowerLambda(l.applyLambdaHint(lam, hint))
}

// applyLambdaHint returns lam with missing param types filled from hint when
// hint resolves to an IRFnType. Already-typed params are preserved; non-fn
// hints leave lam unchanged. Shared by lowerLambdaHint and the monadic
// method desugar (see tryDesugarMonadicMethod) so both sites route lambda
// param inference through the single irTypeToASTType conversion.
func (l *Lowerer) applyLambdaHint(lam Lambda, hint IRType) Lambda {
	fnType, ok := l.resolveDeep(hint).(IRFnType)
	if !ok {
		return lam
	}
	updated := make([]LambdaParam, len(lam.Params))
	copy(updated, lam.Params)
	for i := range updated {
		if updated[i].Type != nil || i >= len(fnType.Params) {
			continue
		}
		if astType := l.irTypeToASTType(fnType.Params[i]); astType != nil {
			updated[i].Type = astType
		}
	}
	lam.Params = updated
	return lam
}

func (l *Lowerer) lowerLambda(lam Lambda) IRExpr {
	params := make([]IRParamDecl, len(lam.Params))
	var lamSymbols []SymbolRegInfo
	for i, p := range lam.Params {
		var typ IRType
		if p.Type != nil {
			typ = l.lowerType(p.Type)
		}
		params[i] = IRParamDecl{GoName: p.Name, Type: typ}
		lamSymbols = append(lamSymbols, SymbolRegInfo{
			Name:     p.Name,
			ArcaType: p.Type,
			IRType:   typ,
			Kind:     SymParameter,
		})
	}
	var retType IRType
	if lam.ReturnType != nil {
		retType = l.lowerType(lam.ReturnType)
	}
	sp, ep := bodyPos(lam.Body)
	var body IRExpr
	l.withScope(sp, ep, lamSymbols, func() {
		body = l.lowerExpr(lam.Body)
	})
	// Infer return type from body if not explicitly annotated
	if retType == nil {
		retType = body.irType()
	}
	return IRFn{
		Params: params,
		Ret:    retType,
		Body:   body,
	}
}

func (l *Lowerer) lowerTuple(t TupleExpr) IRExpr {
	elems := make([]IRExpr, len(t.Elements))
	for i, e := range t.Elements {
		elems[i] = l.lowerExpr(e)
	}
	return IRTupleLit{Elements: elems, Type: IRInterfaceType{}}
}

func (l *Lowerer) lowerForExpr(fe ForExpr) IRExpr {
	binding := snakeToCamel(fe.Binding)
	forSymbols := []SymbolRegInfo{{Name: fe.Binding, Kind: SymVariable}}
	sp, ep := bodyPos(fe.Body)

	if rangeExpr, ok := fe.Iter.(RangeExpr); ok {
		var body IRExpr
		l.withScope(sp, ep, forSymbols, func() {
			body = l.lowerExpr(fe.Body)
		})
		return IRForRange{
			Binding: binding,
			Start:   l.lowerExpr(rangeExpr.Start),
			End:     l.lowerExpr(rangeExpr.End),
			Body:    body,
			Type:    IRNamedType{GoName: "struct{}"},
		}
	}

	var body IRExpr
	l.withScope(sp, ep, forSymbols, func() {
		body = l.lowerExpr(fe.Body)
	})
	return IRForEach{
		Binding: binding,
		Iter:    l.lowerExpr(fe.Iter),
		Body:    body,
		Type:    IRNamedType{GoName: "struct{}"},
	}
}

func (l *Lowerer) lowerListLit(ll ListLit) IRExpr {
	if len(ll.Elements) == 0 && ll.Spread == nil {
		elemTV := l.freshTypeVar()
		return IRListLit{
			ElemType: irTypeEmitStr(elemTV),
			Type:     IRListType{Elem: elemTV},
		}
	}

	elems := make([]IRExpr, len(ll.Elements))
	for i, e := range ll.Elements {
		elems[i] = l.lowerExpr(e)
	}

	elemType := "interface{}"
	if len(ll.Elements) > 0 {
		elemType = l.inferGoElemType(ll.Elements[0])
	}

	var spread IRExpr
	if ll.Spread != nil {
		spread = l.lowerExpr(ll.Spread)
	}

	return IRListLit{
		ElemType: elemType,
		Elements: elems,
		Spread:   spread,
		Type:     IRListType{Elem: IRNamedType{GoName: elemType}},
	}
}

func (l *Lowerer) lowerMapLit(ml MapLit) IRExpr {
	return l.lowerMapLitHint(ml, nil)
}

func (l *Lowerer) lowerMapLitHint(ml MapLit, hint IRType) IRExpr {
	// If hint is a Map type, use its key/value as the expected types
	var hintKey, hintValue IRType
	if mt, ok := hint.(IRMapType); ok {
		hintKey = mt.Key
		hintValue = mt.Value
	}

	entries := make([]IRMapEntry, len(ml.Entries))
	var keyType, valueType IRType
	for i, e := range ml.Entries {
		k := l.lowerExprHint(e.Key, hintKey)
		v := l.lowerExprHint(e.Value, hintValue)
		entries[i] = IRMapEntry{Key: k, Value: v}
		if keyType == nil {
			keyType = k.irType()
		}
		if valueType == nil {
			valueType = v.irType()
		}
	}
	if keyType == nil {
		if hintKey != nil {
			keyType = hintKey
		} else {
			keyType = l.freshTypeVar()
		}
	}
	if valueType == nil {
		if hintValue != nil {
			valueType = hintValue
		} else {
			valueType = l.freshTypeVar()
		}
	}
	return IRMapLit{
		KeyType:   irTypeEmitStr(keyType),
		ValueType: irTypeEmitStr(valueType),
		Entries:   entries,
		Type:      IRMapType{Key: keyType, Value: valueType},
	}
}

func (l *Lowerer) lowerBinaryExpr(be BinaryExpr) IRExpr {
	left := l.lowerExpr(be.Left)
	right := l.lowerExpr(be.Right)
	var typ IRType
	switch be.Op {
	case "==", "!=", "<", ">", "<=", ">=", "&&", "||":
		typ = IRNamedType{GoName: "bool"}
	case "+", "-", "*", "/", "%":
		typ = left.irType()
		if _, ok := typ.(IRInterfaceType); ok {
			typ = right.irType()
		}
	default:
		typ = IRInterfaceType{}
	}
	return IRBinaryExpr{
		Op:    be.Op,
		Left:  left,
		Right: right,
		Type:  typ,
	}
}

// --- Statements ---

func (l *Lowerer) lowerStmt(stmt Stmt) []IRStmt {
	switch s := stmt.(type) {
	case LetStmt:
		return l.lowerLetStmt(s)
	case DeferStmt:
		return []IRStmt{IRDeferStmt{Expr: l.lowerExpr(s.Expr)}}
	case AssertStmt:
		irExpr := l.lowerExpr(s.Expr)
		// Reconstruct expression string for panic message
		exprStr := l.exprToString(s.Expr)
		return []IRStmt{IRAssertStmt{Expr: irExpr, ExprStr: exprStr}}
	case ExprStmt:
		// Try operator in expression statement: expr? → let _ = expr?
		if call, ok := s.Expr.(FnCall); ok {
			if ident, ok := call.Fn.(Ident); ok && ident.Name == "__try" && len(call.Args) == 1 {
				loweredExpr := l.lowerExpr(call.Args[0])
				var retType IRType
				if l.currentRetType != nil {
					retType = l.lowerType(l.currentRetType)
				}
				if isIRResultType(retType) {
					l.builtins["result"] = true
				}
				return []IRStmt{IRTryLetStmt{
					GoName:     "_",
					CallExpr:   loweredExpr,
					ReturnType: retType,
				}}
			}
		}
		expr := l.lowerExpr(s.Expr)
		expr = l.markVoidContext(expr)
		return []IRStmt{IRExprStmt{Expr: expr}}
	default:
		return nil
	}
}

func (l *Lowerer) lowerLetStmt(s LetStmt) []IRStmt {
	// Destructuring: let [first, ..rest] = expr or let (a, b) = expr
	if s.Pattern != nil {
		return l.lowerLetDestructure(s.Pattern, s.Value)
	}

	// Try operator: let x = expr?
	if call, ok := s.Value.(FnCall); ok {
		if ident, ok := call.Fn.(Ident); ok && ident.Name == "__try" && len(call.Args) == 1 {
			return l.lowerTryLetStmt(s, call.Args[0])
		}
	}

	// Discard: let _ = expr
	if s.Name == "_" {
		return []IRStmt{IRLetStmt{
			GoName: "_",
			Value:  l.lowerExpr(s.Value),
			Pos:    s.Pos,
		}}
	}

	return l.lowerNormalLetStmt(s)
}

// lowerTryExprWithHint lowers a `?` in expression position into IRTryExpr.
// Stage 2 hoists the call to a synthetic GoMultiAssign + GoIfElse{GoReturn}
// ahead of the enclosing statement and substitutes the expression with the
// Ok-typed split ident.
func (l *Lowerer) lowerTryExprWithHint(call FnCall, inner Expr, hint IRType) IRExpr {
	var innerHint IRType
	if hint != nil {
		innerHint = IRResultType{Ok: hint, Err: IRNamedType{GoName: "error"}}
	}
	loweredInner := l.lowerExprHint(inner, innerHint)

	rt, isResult := loweredInner.irType().(IRResultType)
	if !isResult {
		// Inner is not a Result — emit it as-is. Type mismatch will be
		// reported by the outer hint check; no need to double-report.
		return loweredInner
	}

	var enclosingRet IRType
	if l.currentIRRetType != nil {
		enclosingRet = l.currentIRRetType
	} else if l.currentRetType != nil {
		enclosingRet = l.lowerType(l.currentRetType)
	}
	if !isIRResultType(enclosingRet) {
		l.addCompileError(ErrTryOutsideResultContext, call.Pos, TryOutsideResultContextData{})
		return loweredInner
	}
	l.builtins["result"] = true

	return IRTryExpr{
		Inner:      loweredInner,
		OkType:     rt.Ok,
		ErrType:    rt.Err,
		ReturnType: enclosingRet,
		Pos:        call.Pos,
	}
}

// lowerTryLetStmt lowers `let x = expr?` into IRTryLetStmt.
func (l *Lowerer) lowerTryLetStmt(s LetStmt, inner Expr) []IRStmt {
	// Pass hint from let type annotation (try unwraps Result, so wrap hint in ResultType)
	var tryHint IRType
	if s.Type != nil {
		tryHint = IRResultType{Ok: l.lowerType(s.Type), Err: IRNamedType{GoName: "error"}}
	}
	loweredExpr := l.lowerExprHint(inner, tryHint)

	goVarName := "_"
	if s.Name != "_" {
		// Try unwraps Result once: variable gets Ok type.
		// (No longer unwraps an inner Option — use .okOr(err)? explicitly.)
		var irType IRType
		if rt, ok := loweredExpr.irType().(IRResultType); ok {
			irType = rt.Ok
		}
		goVarName = l.registerSymbol(SymbolRegInfo{
			Name:     s.Name,
			ArcaType: l.inferASTType(inner),
			IRType:   irType,
			Kind:     SymVariable,
			Pos:      s.Pos,
		})
	}

	var retType IRType
	if l.currentIRRetType != nil {
		retType = l.currentIRRetType
	} else if l.currentRetType != nil {
		retType = l.lowerType(l.currentRetType)
	}
	if !isIRResultType(retType) {
		l.addCompileError(ErrTryOutsideResultContext, s.Pos, TryOutsideResultContextData{})
		return []IRStmt{IRExprStmt{Expr: loweredExpr}}
	}
	l.builtins["result"] = true

	return []IRStmt{IRTryLetStmt{
		GoName:     goVarName,
		CallExpr:   loweredExpr,
		ReturnType: retType,
	}}
}

// lowerNormalLetStmt lowers `let x: Type = expr` (the common case).
func (l *Lowerer) lowerNormalLetStmt(s LetStmt) []IRStmt {
	// Lower the type annotation once (used as hint and as IR type)
	var loweredType IRType
	if s.Type != nil {
		loweredType = l.lowerType(s.Type)
	}

	// Lower value BEFORE declaring variable (shadowing must not affect the RHS)
	loweredValue := l.lowerExprHint(s.Value, loweredType)

	// GoMultiReturn calls that return Result/Option need builtins
	if isGoMultiReturn(loweredValue) {
		switch loweredValue.irType().(type) {
		case IRResultType:
			l.builtins["result"] = true
		case IROptionType:
			l.builtins["option"] = true
		}
	}

	arcaType := s.Type
	if arcaType == nil {
		arcaType = l.inferASTType(s.Value)
	}
	goVarName := l.registerSymbol(SymbolRegInfo{
		Name:     s.Name,
		ArcaType: arcaType,
		IRType:   loweredValue.irType(),
		Kind:     SymVariable,
		Pos:      s.Pos,
	})

	return []IRStmt{IRLetStmt{
		GoName: goVarName,
		Value:  loweredValue,
		Type:   loweredType,
		Pos:    s.Pos,
	}}
}

// inferASTType infers the Arca AST type of an expression (for symbol recording).
func (l *Lowerer) inferASTType(expr Expr) Type {
	switch e := expr.(type) {
	case IntLit:
		return NamedType{Name: "Int"}
	case FloatLit:
		return NamedType{Name: "Float"}
	case StringLit:
		return NamedType{Name: "String"}
	case StringInterp:
		return NamedType{Name: "String"}
	case BoolLit:
		return NamedType{Name: "Bool"}
	case ConstructorCall:
		if e.TypeName != "" {
			return NamedType{Name: e.TypeName}
		}
		if typeName, ok := l.ctorTypes[e.Name]; ok {
			return NamedType{Name: typeName}
		}
		if _, ok := l.typeAliases[e.Name]; ok {
			return NamedType{Name: e.Name}
		}
	case FnCall:
		if ident, ok := e.Fn.(Ident); ok {
			if fn, ok := l.functions[ident.Name]; ok {
				return fn.ReturnType
			}
		}
	case ListLit:
		if len(e.Elements) > 0 {
			elemType := l.inferASTType(e.Elements[0])
			if elemType != nil {
				return NamedType{Name: "List", Params: []Type{elemType}}
			}
		}
		return NamedType{Name: "List"}
	}
	return nil
}

func (l *Lowerer) lowerLetDestructure(pat Pattern, value Expr) []IRStmt {
	switch p := pat.(type) {
	case TuplePattern:
		var bindings []IRDestructureBinding
		for i, elemPat := range p.Elements {
			if bp, ok := elemPat.(BindPattern); ok {
				l.registerSymbol(SymbolRegInfo{Name: bp.Name, Kind: SymVariable})
				bindings = append(bindings, IRDestructureBinding{
					GoName: snakeToCamel(bp.Name),
					Index:  i,
				})
			}
		}
		return []IRStmt{IRDestructureStmt{
			Kind:     IRDestructureTuple,
			Bindings: bindings,
			Value:    l.lowerExpr(value),
		}}
	case ListPattern:
		var bindings []IRDestructureBinding
		for i, elemPat := range p.Elements {
			if bp, ok := elemPat.(BindPattern); ok {
				l.registerSymbol(SymbolRegInfo{Name: bp.Name, Kind: SymVariable})
				bindings = append(bindings, IRDestructureBinding{
					GoName: snakeToCamel(bp.Name),
					Index:  i,
				})
			}
		}
		if p.Rest != "" {
			l.registerSymbol(SymbolRegInfo{Name: p.Rest, Kind: SymVariable})
			bindings = append(bindings, IRDestructureBinding{
				GoName: snakeToCamel(p.Rest),
				Index:  len(p.Elements),
				Slice:  true,
			})
		}
		return []IRStmt{IRDestructureStmt{
			Kind:     IRDestructureList,
			Bindings: bindings,
			Value:    l.lowerExpr(value),
		}}
	default:
		return []IRStmt{IRLetStmt{
			GoName: "_",
			Value:  l.lowerExpr(value),
		}}
	}
}

// --- Match Expressions ---

func (l *Lowerer) lowerMatchExprHint(me MatchExpr, hint IRType) IRExpr {
	l.matchHint = hint
	defer func() { l.matchHint = nil }()
	return l.lowerMatchExpr(me)
}

func (l *Lowerer) lowerMatchExpr(me MatchExpr) IRExpr {
	if l.isResultMatch(me) {
		return l.lowerResultMatch(me)
	}
	if l.isOptionMatch(me) {
		return l.lowerOptionMatch(me)
	}
	if l.isListMatch(me) {
		return l.lowerListMatch(me)
	}
	if l.isTypeMatch(me) {
		return l.lowerTypeMatch(me)
	}
	if l.isLiteralMatch(me) {
		return l.lowerLiteralMatch(me)
	}
	if l.isEnumMatch(me) {
		return l.lowerEnumMatch(me)
	}
	if l.isStructMatch(me) {
		return l.lowerStructMatch(me)
	}
	return l.lowerSumTypeMatch(me)
}

// isStructMatch detects a match whose subject is a single-variant sum type —
// `type AppError { DBInitError(e) }` lowers to a Go struct, not an interface,
// so the match has one always-taken arm and no type switch. Dispatches to
// `lowerStructMatch` before the generic sum type path.
func (l *Lowerer) isStructMatch(me MatchExpr) bool {
	if len(me.Arms) == 0 {
		return false
	}
	pat, ok := me.Arms[0].Pattern.(ConstructorPattern)
	if !ok {
		return false
	}
	typeName := l.findTypeName(pat.Name)
	td, ok := l.types[typeName]
	if !ok {
		return false
	}
	return !isEnum(td) && len(td.Constructors) == 1
}

// isTypeMatch recognizes `match v { id: T => ..., _ => ... }` — narrowing
// an Any subject against concrete types. Any arm being a TypePattern is
// sufficient; the dispatcher is placed before literal/enum/sum matches
// since those use other pattern shapes.
func (l *Lowerer) isTypeMatch(me MatchExpr) bool {
	for _, arm := range me.Arms {
		if _, ok := arm.Pattern.(TypePattern); ok {
			return true
		}
	}
	return false
}

func (l *Lowerer) lowerTypeMatch(me MatchExpr) IRExpr {
	subject := l.lowerExpr(me.Subject)
	var arms []IRMatchArm
	for _, arm := range me.Arms {
		switch p := arm.Pattern.(type) {
		case TypePattern:
			target := l.lowerType(p.Target)
			symbol := SymbolRegInfo{
				Name:   p.Binding,
				IRType: target,
				Kind:   SymVariable,
				Pos:    arm.Pos,
			}
			body := l.lowerArmBody(arm, []SymbolRegInfo{symbol})
			arms = append(arms, IRMatchArm{
				Pattern: IRMatchTypePattern{
					Binding: &IRBinding{GoName: p.Binding},
					Target:  target,
				},
				Body: body,
			})
		case WildcardPattern, BindPattern:
			body := l.lowerArmBody(arm, nil)
			arms = append(arms, IRMatchArm{Pattern: IRWildcardPattern{}, Body: body})
		default:
			_ = p
		}
	}
	return l.buildMatch(subject, arms, me.Pos)
}

// lowerArmBody lowers a match arm body within a fresh lexical scope, with the
// given symbols registered. Uses l.matchHint for type propagation.
func (l *Lowerer) lowerArmBody(arm MatchArm, symbols []SymbolRegInfo) IRExpr {
	var body IRExpr
	l.withScope(arm.Pos, arm.EndPos, symbols, func() {
		body = l.lowerExprHint(arm.Body, l.matchHint)
	})
	return body
}

// buildMatch constructs an IRMatch from a subject, arms, and position.
func (l *Lowerer) buildMatch(subject IRExpr, arms []IRMatchArm, pos Pos) IRMatch {
	m := IRMatch{Subject: subject, Arms: arms, Type: l.inferMatchType(arms), Pos: pos}
	l.checkMatchExhaustiveness(m)
	return m
}

// checkMatchExhaustiveness reports errors for non-exhaustive match expressions.
func (l *Lowerer) checkMatchExhaustiveness(m IRMatch) {
	if len(m.Arms) == 0 {
		return
	}
	switch m.Arms[0].Pattern.(type) {
	case IRResultOkPattern, IRResultErrorPattern:
		l.checkResultExhaustiveness(m)
	case IROptionSomePattern, IROptionNonePattern:
		l.checkOptionExhaustiveness(m)
	case IREnumPattern:
		l.checkEnumExhaustiveness(m)
	case IRSumTypePattern, IRSumTypeWildcardPattern:
		l.checkSumTypeExhaustiveness(m)
	}
}

func (l *Lowerer) checkResultExhaustiveness(m IRMatch) {
	hasOk, hasError := false, false
	for _, arm := range m.Arms {
		switch arm.Pattern.(type) {
		case IRResultOkPattern:
			hasOk = true
		case IRResultErrorPattern:
			hasError = true
		}
	}
	if !hasOk {
		l.addCompileError(ErrNonExhaustiveMatch, m.Pos, NonExhaustiveMatchData{Missing: "Ok arm"})
	}
	if !hasError {
		l.addCompileError(ErrNonExhaustiveMatch, m.Pos, NonExhaustiveMatchData{Missing: "Error arm"})
	}
}

func (l *Lowerer) checkOptionExhaustiveness(m IRMatch) {
	hasSome, hasNone := false, false
	for _, arm := range m.Arms {
		switch arm.Pattern.(type) {
		case IROptionSomePattern:
			hasSome = true
		case IROptionNonePattern:
			hasNone = true
		}
	}
	if !hasSome {
		l.addCompileError(ErrNonExhaustiveMatch, m.Pos, NonExhaustiveMatchData{Missing: "Some arm"})
	}
	if !hasNone {
		l.addCompileError(ErrNonExhaustiveMatch, m.Pos, NonExhaustiveMatchData{Missing: "None arm"})
	}
}

func (l *Lowerer) checkEnumExhaustiveness(m IRMatch) {
	hasWildcard := false
	matched := make(map[string]bool)
	for _, arm := range m.Arms {
		switch p := arm.Pattern.(type) {
		case IREnumPattern:
			matched[p.GoValue] = true
		case IRWildcardPattern:
			hasWildcard = true
		}
	}
	if hasWildcard {
		return
	}
	for _, td := range l.types {
		if !isEnum(td) {
			continue
		}
		for _, c := range td.Constructors {
			goValue := td.Name + c.Name
			if matched[goValue] {
				for _, ctor := range td.Constructors {
					if !matched[td.Name+ctor.Name] {
						l.addCompileError(ErrNonExhaustiveMatch, m.Pos, NonExhaustiveMatchData{Missing: ctor.Name + " variant"})
					}
				}
				return
			}
		}
	}
}

func (l *Lowerer) checkSumTypeExhaustiveness(m IRMatch) {
	hasWildcard := false
	matched := make(map[string]bool)
	for _, arm := range m.Arms {
		switch p := arm.Pattern.(type) {
		case IRSumTypePattern:
			matched[p.GoType] = true
		case IRSumTypeWildcardPattern:
			hasWildcard = true
		}
	}
	if hasWildcard {
		return
	}
	for _, td := range l.types {
		if isEnum(td) || len(td.Constructors) <= 1 {
			continue
		}
		for _, c := range td.Constructors {
			goType := td.Name + c.Name
			if matched[goType] {
				for _, ctor := range td.Constructors {
					if !matched[td.Name+ctor.Name] {
						l.addCompileError(ErrNonExhaustiveMatch, m.Pos, NonExhaustiveMatchData{Missing: ctor.Name + " variant"})
					}
				}
				return
			}
		}
	}
}

func (l *Lowerer) isResultMatch(me MatchExpr) bool {
	for _, arm := range me.Arms {
		if cp, ok := arm.Pattern.(ConstructorPattern); ok {
			if cp.Name == "Ok" || cp.Name == "Error" {
				if _, isUserCtor := l.ctorTypes[cp.Name]; !isUserCtor {
					return true
				}
			}
		}
	}
	return false
}

// lowerWrappedArm lowers an arm of Result/Option-like match (single binding).
// fieldType is the IR type extracted from subject (e.g. rt.Ok, rt.Err, ot.Inner).
// source is the Go field source (".Value" or ".Err").
// Returns the binding (nil if unused) and the lowered body.
func (l *Lowerer) lowerWrappedArm(
	arm MatchArm, cp ConstructorPattern,
	fieldType IRType, source string,
	subjectErr Pos, missingTypeName string,
) (*IRBinding, IRExpr) {
	var binding *IRBinding
	var armSymbols []SymbolRegInfo
	if len(cp.Fields) > 0 {
		if fieldType == nil {
			l.addCompileError(ErrCannotInferType, subjectErr, CannotInferTypeData{TypeName: missingTypeName})
		}
		usedVars := collectUsedIdents(arm.Body)
		if _, used := usedVars[cp.Fields[0].Binding]; used {
			binding = &IRBinding{
				GoName: snakeToCamel(cp.Fields[0].Binding),
				Source: source,
			}
		}
		if fieldType != nil {
			armSymbols = append(armSymbols, SymbolRegInfo{
				Name: cp.Fields[0].Binding, IRType: fieldType, Kind: SymVariable,
			})
		}
	}
	return binding, l.lowerArmBody(arm, armSymbols)
}

func (l *Lowerer) lowerResultMatch(me MatchExpr) IRExpr {
	subject := l.lowerExpr(me.Subject)
	rt, _ := subject.irType().(IRResultType)
	var rtOk, rtErr IRType
	if (rt != IRResultType{}) {
		rtOk, rtErr = rt.Ok, rt.Err
	}

	var arms []IRMatchArm
	for _, arm := range me.Arms {
		cp, ok := arm.Pattern.(ConstructorPattern)
		if !ok {
			continue
		}
		switch cp.Name {
		case "Ok":
			binding, body := l.lowerWrappedArm(arm, cp, rtOk, ".Value", me.Pos, "Result")
			arms = append(arms, IRMatchArm{Pattern: IRResultOkPattern{Binding: binding}, Body: body})
		case "Error":
			binding, body := l.lowerWrappedArm(arm, cp, rtErr, ".Err", me.Pos, "Result")
			arms = append(arms, IRMatchArm{Pattern: IRResultErrorPattern{Binding: binding}, Body: body})
		}
	}

	return l.buildMatch(subject, arms, me.Pos)
}

func (l *Lowerer) isOptionMatch(me MatchExpr) bool {
	for _, arm := range me.Arms {
		if cp, ok := arm.Pattern.(ConstructorPattern); ok {
			if cp.Name == "Some" || cp.Name == "None" {
				return true
			}
		}
	}
	return false
}

func (l *Lowerer) lowerOptionMatch(me MatchExpr) IRExpr {
	subject := l.lowerExpr(me.Subject)
	ot, _ := subject.irType().(IROptionType)
	var inner IRType
	if (ot != IROptionType{}) {
		inner = ot.Inner
	}

	var arms []IRMatchArm
	for _, arm := range me.Arms {
		cp, ok := arm.Pattern.(ConstructorPattern)
		if !ok {
			continue
		}
		switch cp.Name {
		case "Some":
			binding, body := l.lowerWrappedArm(arm, cp, inner, ".Value", me.Pos, "Option")
			arms = append(arms, IRMatchArm{Pattern: IROptionSomePattern{Binding: binding}, Body: body})
		case "None":
			body := l.lowerArmBody(arm, nil)
			arms = append(arms, IRMatchArm{Pattern: IROptionNonePattern{}, Body: body})
		}
	}

	return l.buildMatch(subject, arms, me.Pos)
}

func (l *Lowerer) isListMatch(me MatchExpr) bool {
	for _, arm := range me.Arms {
		if _, ok := arm.Pattern.(ListPattern); ok {
			return true
		}
	}
	return false
}

func (l *Lowerer) lowerListMatch(me MatchExpr) IRExpr {
	subject := l.lowerExpr(me.Subject)
	var arms []IRMatchArm

	for _, arm := range me.Arms {
		lp, ok := arm.Pattern.(ListPattern)
		if !ok {
			arms = append(arms, IRMatchArm{
				Pattern: IRListDefaultPattern{},
				Body:    l.lowerArmBody(arm, nil),
			})
			continue
		}

		if len(lp.Elements) == 0 && lp.Rest == "" {
			arms = append(arms, IRMatchArm{
				Pattern: IRListEmptyPattern{},
				Body:    l.lowerArmBody(arm, nil),
			})
			continue
		}

		// Collect arm symbols from list pattern bindings
		var armSymbols []SymbolRegInfo
		for _, elemPat := range lp.Elements {
			if bp, ok := elemPat.(BindPattern); ok {
				armSymbols = append(armSymbols, SymbolRegInfo{
					Name: bp.Name, Kind: SymVariable,
				})
			}
		}
		if lp.Rest != "" {
			armSymbols = append(armSymbols, SymbolRegInfo{
				Name: lp.Rest, Kind: SymVariable,
			})
		}

		body := l.lowerArmBody(arm, armSymbols)

		usedVars := collectUsedIdents(arm.Body)
		var bindings []IRBinding
		for i, elemPat := range lp.Elements {
			if bp, ok := elemPat.(BindPattern); ok {
				if _, used := usedVars[bp.Name]; used {
					bindings = append(bindings, IRBinding{
						GoName: snakeToCamel(bp.Name),
						Source: fmt.Sprintf("[%d]", i),
					})
				}
			}
		}

		var rest *IRBinding
		if lp.Rest != "" {
			if _, used := usedVars[lp.Rest]; used {
				rest = &IRBinding{
					GoName: snakeToCamel(lp.Rest),
					Source: fmt.Sprintf("[%d:]", len(lp.Elements)),
				}
			}
		}

		if lp.Rest != "" {
			arms = append(arms, IRMatchArm{
				Pattern: IRListConsPattern{Elements: bindings, Rest: rest, MinLen: len(lp.Elements)},
				Body:    body,
			})
		} else {
			arms = append(arms, IRMatchArm{
				Pattern: IRListExactPattern{Elements: bindings, MinLen: len(lp.Elements)},
				Body:    body,
			})
		}
	}

	return l.buildMatch(subject, arms, me.Pos)
}

func (l *Lowerer) isLiteralMatch(me MatchExpr) bool {
	for _, arm := range me.Arms {
		if _, ok := arm.Pattern.(LitPattern); ok {
			return true
		}
	}
	return false
}

func (l *Lowerer) lowerLiteralMatch(me MatchExpr) IRExpr {
	subject := l.lowerExpr(me.Subject)
	var arms []IRMatchArm

	for _, arm := range me.Arms {
		body := l.lowerArmBody(arm, nil)
		switch p := arm.Pattern.(type) {
		case LitPattern:
			arms = append(arms, IRMatchArm{Pattern: IRLiteralPattern{Value: l.litPatternGoStr(p)}, Body: body})
		case WildcardPattern, BindPattern:
			_ = p
			arms = append(arms, IRMatchArm{Pattern: IRLiteralDefaultPattern{}, Body: body})
		}
	}

	return l.buildMatch(subject, arms, me.Pos)
}

func (l *Lowerer) litPatternGoStr(lp LitPattern) string {
	switch e := lp.Expr.(type) {
	case IntLit:
		return fmt.Sprintf("%d", e.Value)
	case FloatLit:
		return fmt.Sprintf("%g", e.Value)
	case StringLit:
		return fmt.Sprintf("%q", e.Value)
	case BoolLit:
		if e.Value {
			return "true"
		}
		return "false"
	default:
		return "/* unknown lit */"
	}
}

func (l *Lowerer) isEnumMatch(me MatchExpr) bool {
	for _, arm := range me.Arms {
		if cp, ok := arm.Pattern.(ConstructorPattern); ok {
			typeName := l.findTypeName(cp.Name)
			if td, ok := l.types[typeName]; ok {
				return isEnum(td)
			}
		}
	}
	return false
}

func (l *Lowerer) lowerEnumMatch(me MatchExpr) IRExpr {
	subject := l.lowerExpr(me.Subject)
	var arms []IRMatchArm

	for _, arm := range me.Arms {
		body := l.lowerArmBody(arm, nil)
		if cp, ok := arm.Pattern.(ConstructorPattern); ok {
			typeName := l.findTypeName(cp.Name)
			arms = append(arms, IRMatchArm{Pattern: IREnumPattern{GoValue: typeName + cp.Name}, Body: body})
		} else {
			arms = append(arms, IRMatchArm{Pattern: IRWildcardPattern{}, Body: body})
		}
	}

	return l.buildMatch(subject, arms, me.Pos)
}

// lowerStructMatch lowers a match on a single-variant sum type (emitted as
// struct). The one arm is always taken, so the generated Go is the body
// prefixed with let-bindings for the pattern's fields — no switch.
// Wildcard / bind arms that accept the whole subject skip the field pull and
// emit the body directly (with the subject bound when a BindPattern names it).
func (l *Lowerer) lowerStructMatch(me MatchExpr) IRExpr {
	subject := l.lowerExpr(me.Subject)
	arm := me.Arms[0]
	switch pat := arm.Pattern.(type) {
	case ConstructorPattern:
		typeName := l.findTypeName(pat.Name)
		var ctorFields []Field
		if td, ok := l.types[typeName]; ok {
			ctorFields = td.Constructors[0].Fields
		}
		var armSymbols []SymbolRegInfo
		var stmts []IRStmt
		for i, fp := range pat.Fields {
			if i >= len(ctorFields) {
				continue
			}
			fieldType := ctorFields[i].Type
			irFieldType := l.lowerType(fieldType)
			armSymbols = append(armSymbols, SymbolRegInfo{
				Name:     fp.Binding,
				ArcaType: fieldType,
				IRType:   irFieldType,
				Kind:     SymVariable,
			})
			goFieldName := capitalize(ctorFields[i].Name)
			stmts = append(stmts, IRLetStmt{
				GoName: snakeToCamel(fp.Binding),
				Value:  IRFieldAccess{Expr: subject, Field: goFieldName, Type: irFieldType},
				Type:   irFieldType,
			})
		}
		body := l.lowerArmBody(arm, armSymbols)
		if len(stmts) == 0 {
			return body
		}
		return IRBlock{Stmts: stmts, Expr: body, Type: body.irType()}
	case BindPattern:
		armSymbols := []SymbolRegInfo{{
			Name:   pat.Name,
			IRType: subject.irType(),
			Kind:   SymVariable,
		}}
		body := l.lowerArmBody(arm, armSymbols)
		return IRBlock{
			Stmts: []IRStmt{IRLetStmt{GoName: snakeToCamel(pat.Name), Value: subject, Type: subject.irType()}},
			Expr:  body,
			Type:  body.irType(),
		}
	case WildcardPattern:
		return l.lowerArmBody(arm, nil)
	}
	// Unexpected pattern — fall back to sum type path for diagnostic parity.
	return l.lowerSumTypeMatch(me)
}

func (l *Lowerer) lowerSumTypeMatch(me MatchExpr) IRExpr {
	subject := l.lowerExpr(me.Subject)
	var arms []IRMatchArm

	for _, arm := range me.Arms {
		switch pat := arm.Pattern.(type) {
		case ConstructorPattern:
			typeName := l.findTypeName(pat.Name)
			variantName := typeName + pat.Name

			var ctorFields []Field
			if td, ok := l.types[typeName]; ok {
				for _, c := range td.Constructors {
					if c.Name == pat.Name {
						ctorFields = c.Fields
						break
					}
				}
			}

			var armSymbols []SymbolRegInfo
			for i, fp := range pat.Fields {
				if i < len(ctorFields) {
					armSymbols = append(armSymbols, SymbolRegInfo{
						Name:     fp.Binding,
						ArcaType: ctorFields[i].Type,
						IRType:   l.lowerType(ctorFields[i].Type),
						Kind:     SymVariable,
					})
				}
			}

			body := l.lowerArmBody(arm, armSymbols)
			usedVars := collectUsedIdents(arm.Body)

			var bindings []IRBinding
			for i, fp := range pat.Fields {
				if _, used := usedVars[fp.Binding]; used {
					goFieldName := capitalize(fp.Name)
					if i < len(ctorFields) {
						goFieldName = capitalize(ctorFields[i].Name)
					}
					bindings = append(bindings, IRBinding{
						GoName: snakeToCamel(fp.Binding),
						Source: "." + goFieldName,
					})
				}
			}

			arms = append(arms, IRMatchArm{
				Pattern: IRSumTypePattern{GoType: variantName, Bindings: bindings},
				Body:    body,
			})
		case WildcardPattern:
			body := l.lowerArmBody(arm, nil)
			arms = append(arms, IRMatchArm{Pattern: IRSumTypeWildcardPattern{}, Body: body})
		case BindPattern:
			body := l.lowerArmBody(arm, nil)
			arms = append(arms, IRMatchArm{
				Pattern: IRSumTypeWildcardPattern{Binding: &IRBinding{GoName: snakeToCamel(pat.Name)}},
				Body:    body,
			})
		}
	}

	return l.buildMatch(subject, arms, me.Pos)
}

// inferMatchType unifies all arm body types to determine the match expression type.
func (l *Lowerer) inferMatchType(arms []IRMatchArm) IRType {
	var result IRType
	for _, arm := range arms {
		if arm.Body == nil {
			continue
		}
		t := arm.Body.irType()
		if _, ok := t.(IRInterfaceType); ok {
			continue
		}
		if _, ok := t.(IRTypeVar); ok {
			continue
		}
		if result == nil {
			result = t
			continue
		}
		// Outer match hint already reports arm-vs-expected mismatches via
		// each arm's checkTypeHint; this is pure substitution so inference
		// can resolveDeep the match result type.
		l.infer.unify(result, t)
	}
	if result == nil {
		return IRInterfaceType{}
	}
	return l.resolveDeep(result)
}

// --- Helpers ---

func (l *Lowerer) lowerExprs(exprs []Expr) []IRExpr {
	result := make([]IRExpr, len(exprs))
	for i, e := range exprs {
		result[i] = l.lowerExpr(e)
	}
	return result
}

// irTypeToASTType is a best-effort IR → AST projection used when a resolved
// IR type needs to flow back into a site that later re-lowers from AST (e.g.
// hint-driven lambda param typing). Returns nil when any leaf cannot be
// projected so the caller skips that slot rather than producing an invalid
// AST. Round-trips through lowerType are intentionally idempotent for the
// shapes handled here.
func (l *Lowerer) irTypeToASTType(t IRType) Type {
	switch tt := t.(type) {
	case IRNamedType:
		var params []Type
		if len(tt.Params) > 0 {
			params = make([]Type, 0, len(tt.Params))
			for _, p := range tt.Params {
				pa := l.irTypeToASTType(p)
				if pa == nil {
					return nil
				}
				params = append(params, pa)
			}
		}
		return NamedType{Name: tt.GoName, Params: params}
	case IRPointerType:
		inner := l.irTypeToASTType(tt.Inner)
		if inner != nil {
			return PointerType{Inner: inner}
		}
	case IRListType:
		inner := l.irTypeToASTType(tt.Elem)
		if inner != nil {
			return NamedType{Name: "List", Params: []Type{inner}}
		}
	case IRMapType:
		k := l.irTypeToASTType(tt.Key)
		v := l.irTypeToASTType(tt.Value)
		if k != nil && v != nil {
			return NamedType{Name: "Map", Params: []Type{k, v}}
		}
	case IRRefType:
		inner := l.irTypeToASTType(tt.Inner)
		if inner != nil {
			return NamedType{Name: "Ref", Params: []Type{inner}}
		}
	case IROptionType:
		inner := l.irTypeToASTType(tt.Inner)
		if inner != nil {
			return NamedType{Name: "Option", Params: []Type{inner}}
		}
	case IRResultType:
		ok := l.irTypeToASTType(tt.Ok)
		er := l.irTypeToASTType(tt.Err)
		if ok != nil && er != nil {
			return NamedType{Name: "Result", Params: []Type{ok, er}}
		}
	case IRTraitType:
		return NamedType{Name: tt.Name}
	case IRInterfaceType:
		return NamedType{Name: "Any"}
	case IRFnType:
		params := make([]Type, len(tt.Params))
		for i, p := range tt.Params {
			ap := l.irTypeToASTType(p)
			if ap == nil {
				return nil
			}
			params[i] = ap
		}
		var ret Type
		if tt.Ret != nil {
			ret = l.irTypeToASTType(tt.Ret)
			if ret == nil {
				return nil
			}
		}
		return FunctionType{Params: params, Ret: ret}
	}
	return nil
}

func (l *Lowerer) findTypeName(ctorName string) string {
	for typeName, td := range l.types {
		for _, c := range td.Constructors {
			if c.Name == ctorName {
				return typeName
			}
		}
	}
	return ""
}

func (l *Lowerer) resultTypeArgs() string {
	if l.currentRetType == nil {
		return ""
	}
	if nt, ok := l.currentRetType.(NamedType); ok && nt.Name == "Result" {
		if len(nt.Params) >= 2 {
			return "[" + irTypeEmitStr(l.lowerType(nt.Params[0])) + ", " + irTypeEmitStr(l.lowerType(nt.Params[1])) + "]"
		}
		if len(nt.Params) == 1 {
			return "[" + irTypeEmitStr(l.lowerType(nt.Params[0])) + ", error]"
		}
	}
	return ""
}

func (l *Lowerer) resolveMethodName(name string) string {
	for _, td := range l.types {
		for _, m := range td.Methods {
			if m.Name == name {
				if m.Public {
					return snakeToPascal(name)
				}
				return snakeToCamel(name)
			}
		}
	}
	// Trait impls are emitted as exported Go methods so the Go interface
	// method set is satisfied. See lowerImplDecl's Public override.
	for _, impls := range l.impls {
		for _, impl := range impls {
			for _, m := range impl.Methods {
				if m.Name == name {
					return snakeToPascal(name)
				}
			}
		}
	}
	for _, trait := range l.traits {
		for _, m := range trait.Methods {
			if m.Name == name {
				return snakeToPascal(name)
			}
		}
	}
	return capitalize(name)
}

func (l *Lowerer) inferGoElemType(expr Expr) string {
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

// exprToString produces a human-readable string representation of an AST expression,
// used for assert panic messages.
func (l *Lowerer) exprToString(expr Expr) string {
	switch e := expr.(type) {
	case IntLit:
		return fmt.Sprintf("%d", e.Value)
	case FloatLit:
		return fmt.Sprintf("%g", e.Value)
	case StringLit:
		return fmt.Sprintf("%q", e.Value)
	case BoolLit:
		if e.Value {
			return "true"
		}
		return "false"
	case Ident:
		return e.Name
	case BinaryExpr:
		return l.exprToString(e.Left) + " " + e.Op + " " + l.exprToString(e.Right)
	case FnCall:
		if ident, ok := e.Fn.(Ident); ok {
			args := make([]string, len(e.Args))
			for i, a := range e.Args {
				args[i] = l.exprToString(a)
			}
			return ident.Name + "(" + strings.Join(args, ", ") + ")"
		}
		return "/* expr */"
	case FieldAccess:
		return l.exprToString(e.Expr) + "." + e.Field
	default:
		return "/* expr */"
	}
}


// irZeroExpr returns an IR expression representing the Go zero value for a type.
func irZeroExpr(t IRType) IRExpr {
	switch tt := t.(type) {
	case IRNamedType:
		switch tt.GoName {
		case "int", "uint", "float64", "byte":
			return IRIntLit{Value: 0}
		case "string":
			return IRStringLit{Value: ""}
		case "bool":
			return IRBoolLit{Value: false}
		case "struct{}":
			return IRIdent{GoName: "struct{}{}"}
		}
		return IRIdent{GoName: tt.GoName + "{}"}
	case IRPointerType, IRListType, IRMapType, IRInterfaceType:
		return IRIdent{GoName: "nil"}
	}
	return IRIdent{GoName: "nil"}
}
