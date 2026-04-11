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
	ctorTypes    map[string]string // constructor name → type name
	fnNames      map[string]string // arca name → Go name for pub functions
	functions    map[string]FnDecl
	moduleNames  map[string]bool
	goModule     string
	typeResolver  TypeResolver
	goPackages    map[string]*GoPackage // short name → Go package info (carries Pos/SideEffect/Used)

	// Per-function state
	currentRetType  Type
	currentReceiver string
	currentTypeName string
	matchHint       IRType // type hint for match arm bodies

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
	case IRNamedType:
		if len(tt.Params) == 0 {
			return tt
		}
		params := make([]IRType, len(tt.Params))
		for i, p := range tt.Params {
			params[i] = s.resolveDeep(p)
		}
		return IRNamedType{GoName: tt.GoName, Params: params}
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

	switch at := a.(type) {
	case IRNamedType:
		bt, ok := b.(IRNamedType)
		if !ok || at.GoName != bt.GoName || len(at.Params) != len(bt.Params) {
			return false
		}
		for i := range at.Params {
			if !s.unify(at.Params[i], bt.Params[i]) {
				return false
			}
		}
		return true
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
		bt, ok := b.(IRPointerType)
		if !ok {
			return false
		}
		return s.unify(at.Inner, bt.Inner)
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
func (l *Lowerer) resolveDeep(t IRType) IRType { return l.infer.resolveDeep(t) }
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
	l.addCompileError(ErrTypeMismatch, pos, TypeMismatchData{
		Expected: irTypeDisplayStr(l.resolveDeep(b)),
		Actual:   irTypeDisplayStr(l.resolveDeep(a)),
	})
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
	case IRLambda:
		expr.Body = l.resolveExprTypes(expr.Body)
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
		return expr.Func
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

func (l *Lowerer) addCompileError(code ErrorCode, pos Pos, data interface{}) {
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
		}
	}

	// Expand sum type methods to per-variant implementations
	funcs = l.expandSumTypeMethods(funcs)

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

	return IRProgram{
		Package:  pkgName,
		Imports:  imports,
		Types:    types,
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

func (l *Lowerer) lowerTypeDecl(td TypeDecl) IRTypeDecl {
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
	params  []IRParamDecl
	retType IRType
	body    IRExpr
}

// lowerFnCommon lowers the signature and body of a function-like declaration,
// managing per-function state (currentRetType, currentReceiver, currentTypeName,
// lexical scope, type inference scope).
func (l *Lowerer) lowerFnCommon(fd FnDecl, typeName, receiver string) loweredFn {
	prevRet := l.currentRetType
	prevRecv := l.currentReceiver
	prevType := l.currentTypeName

	l.currentRetType = fd.ReturnType
	l.currentReceiver = receiver
	if typeName != "" {
		l.currentTypeName = typeName
	}

	params := l.lowerParams(fd.Params)
	var retType IRType
	if fd.ReturnType != nil {
		retType = l.lowerType(fd.ReturnType)
	}

	// Scope spans from the function declaration start (so parameters are
	// hover-able in the signature) through the body end.
	_, ep := bodyPos(fd.Body)
	sp := fd.Pos
	symbols := l.paramsToSymbols(fd.Params)
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

	return loweredFn{params: params, retType: retType, body: body}
}

func (l *Lowerer) lowerFnDecl(fd FnDecl) IRFuncDecl {
	name := fd.Name
	if fd.Public {
		name = snakeToPascal(name)
	}

	lf := l.lowerFnCommon(fd, "", "")

	return IRFuncDecl{
		GoName:     name,
		Params:     lf.params,
		ReturnType: lf.retType,
		Body:       lf.body,
		Source:     SourceInfo{Pos: fd.Pos, Name: fd.Name, ReturnType: fd.ReturnType},
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
		Params:     lf.params,
		ReturnType: lf.retType,
		Body:       lf.body,
		Source:     SourceInfo{Pos: fd.Pos, Name: fd.Name, TypeName: td.Name, ReturnType: fd.ReturnType},
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
			GoName:     fn.GoName,
			Receiver:   &IRReceiver{GoName: fn.Receiver.GoName, Type: p.GoType},
			Params:     fn.Params,
			ReturnType: fn.ReturnType,
			Body:       body,
			Source:     fn.Source,
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
			GoName:     fn.GoName,
			Receiver:   &IRReceiver{GoName: fn.Receiver.GoName, Type: variantName},
			Params:     fn.Params,
			ReturnType: fn.ReturnType,
			Body:       fn.Body,
			Source:     fn.Source,
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
		GoName:     funcName,
		Params:     lf.params,
		ReturnType: lf.retType,
		Body:       lf.body,
		Source:     SourceInfo{Pos: fd.Pos, Name: fd.Name, TypeName: td.Name, ReturnType: fd.ReturnType},
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

// isTypeParam checks if a name is a type parameter of the current type.
// Only looks at the enclosing type's declaration so method bodies resolve
// their own params consistently. External constructor calls never hit this
// path: lowerUserConstructorCall instantiates fresh type vars per call via
// instantiateGenericType.
func (l *Lowerer) isTypeParam(name string) bool {
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
	case IRResultType:
		return "Result_[" + irTypeEmitStr(tt.Ok) + ", " + irTypeEmitStr(tt.Err) + "]"
	case IROptionType:
		return "Option_[" + irTypeEmitStr(tt.Inner) + "]"
	case IRListType:
		return "[]" + irTypeEmitStr(tt.Elem)
	case IRMapType:
		return "map[" + irTypeEmitStr(tt.Key) + "]" + irTypeEmitStr(tt.Value)
	case IRTupleType:
		if len(tt.Elements) == 2 {
			return fmt.Sprintf("struct{ First %s; Second %s }", irTypeEmitStr(tt.Elements[0]), irTypeEmitStr(tt.Elements[1]))
		}
		return "interface{}"
	case IRTypeVar:
		return "interface{}" // unresolved type variable falls back to interface{}
	default:
		return "interface{}"
	}
}

// bodyPos extracts start/end position from a function body expression.
func bodyPos(body Expr) (Pos, Pos) {
	if b, ok := body.(Block); ok {
		return b.Pos, b.EndPos
	}
	return Pos{}, Pos{}
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
	case "Float":
		return IRNamedType{GoName: "float64"}
	case "String":
		return IRNamedType{GoName: "string"}
	case "Bool":
		return IRNamedType{GoName: "bool"}
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
		l.checkTypeHint(result, hint, expr)
	}
	return result
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
		return IRRefExpr{Expr: inner, Type: IRPointerType{Inner: inner.irType()}}
	case RangeExpr:
		// RangeExpr as standalone expression (not inside for) — shouldn't happen often
		return IRFnCall{
			Func: "__range",
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

// buildGoTypeArgsStr builds the Go generic type args string "[T1, T2, ...]"
// from a generic call's type vars map (after unification). Explicit type args
// take precedence if provided.
func (l *Lowerer) buildGoTypeArgsStr(vars map[string]IRType, explicit []Type) string {
	if len(explicit) > 0 {
		return l.explicitTypeArgsStr(explicit)
	}
	if len(vars) == 0 {
		return ""
	}
	names := make([]string, 0, len(vars))
	for name := range vars {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, name := range names {
		resolved := l.resolveDeep(vars[name])
		parts[i] = irTypeEmitStr(resolved)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// unifyExplicitTypeArgs unifies explicit type arguments with a generic call's
// type parameter variables. Type params are ordered by declaration.
func (l *Lowerer) unifyExplicitTypeArgs(vars map[string]IRType, typeArgs []Type) {
	if len(typeArgs) == 0 || len(vars) == 0 {
		return
	}
	// Order by name for deterministic mapping
	names := make([]string, 0, len(vars))
	for name := range vars {
		names = append(names, name)
	}
	sort.Strings(names)
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
			// Try operator: this shouldn't appear as a standalone expression,
			// it is handled at the statement level in lowerStmt.
			// If it leaks here, lower the inner expression.
			if len(e.Args) == 1 {
				return l.lowerExpr(e.Args[0])
			}
		default:
			// Check prelude builtins
			if def, ok := prelude[ident.Name]; ok {
				args := l.lowerPreludeArgs(ident.Name, e.Args)
				if def.Import != "" {
					l.builtins[def.Import] = true
				}
				if def.Builtin != "" {
					l.builtins[def.Builtin] = true
				}
				if def.Lower != nil {
					if result := def.Lower(args); result != nil {
						return result
					}
				} else {
					retType := l.inferPreludeReturnType(ident.Name, args)
					return IRFnCall{Func: def.GoFunc, Args: args, Type: retType, Source: SourceInfo{Pos: e.Pos}}
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
				ret := l.resolveGoCall(goCallName, args, e.Pos)
				// Unify explicit type args with type vars from generic instantiation
				l.unifyExplicitTypeArgs(ret.TypeVars, e.TypeArgs)
				// Propagate hint into the generic return type so fresh type
				// vars bind for buildGoTypeArgsStr. Real mismatches are
				// reported later by the outer checkTypeHint pass, so this
				// must stay as raw HM substitution to avoid double-reporting.
				if hint != nil {
					l.infer.unify(ret.Type, hint)
				}
				typeArgsStr := l.buildGoTypeArgsStr(ret.TypeVars, e.TypeArgs)
				return IRFnCall{Func: goCallName, Args: args, Type: l.resolveDeep(ret.Type), TypeArgs: typeArgsStr, GoMultiReturn: ret.GoMultiReturn, Source: SourceInfo{Pos: e.Pos}}
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
					Func:   ident.Name + "." + fnName,
					Args:   args,
					Type:   IRInterfaceType{},
					Source: SourceInfo{Pos: e.Pos, Name: fa.Field},
				}
			}
			return IRFnCall{Func: fnName, Args: args, Type: IRInterfaceType{}, Source: SourceInfo{Pos: e.Pos, Name: fa.Field}}
		}
		// Regular method call: obj.method(args)
		methodName := l.resolveMethodName(fa.Field)
		args := l.lowerCallArgs(e)
		receiver := l.lowerExpr(fa.Expr)
		ret := l.resolveMethodReturnType(receiver, fa.Field)
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
		if id, ok := e.Fn.(Ident); ok {
			if fn, ok := l.functions[id.Name]; ok {
				// Arg/param type checks happen in lowerArgWithContext via
				// lowerExprHint → checkTypeHint → unify during lowerCallArgs.
				// No additional unify needed here.
				if fn.ReturnType != nil {
					fnType = l.lowerType(fn.ReturnType)
				}
			}
		}
		// Fall back to Go FFI resolution
		var goTypeVars map[string]IRType
		if _, isInterface := fnType.(IRInterfaceType); isInterface {
			ret := l.resolveGoCall(ident.GoName, args, e.Pos)
			fnType = ret.Type
			goMultiReturn = ret.GoMultiReturn
			goTypeVars = ret.TypeVars
			l.unifyExplicitTypeArgs(goTypeVars, e.TypeArgs)
			// Same as the FieldAccess path above: raw substitution only,
			// outer checkTypeHint owns the reporting.
			if hint != nil {
				l.infer.unify(fnType, hint)
			}
		}
		typeArgsStr := l.buildGoTypeArgsStr(goTypeVars, e.TypeArgs)
		return IRFnCall{Func: ident.GoName, Args: args, Type: l.resolveDeep(fnType), TypeArgs: typeArgsStr, GoMultiReturn: goMultiReturn, Source: SourceInfo{Pos: e.Pos, Name: arcaName}}
	}
	// Lambda call or other complex expression
	return IRFnCall{Func: "/* complex call */", Args: args, Type: IRInterfaceType{}, Source: SourceInfo{Pos: e.Pos}}
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
		return goReturnInfo{Type: IRInterfaceType{}}
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
			l.addError(pos, "argument %d of '%s' expects %s, got %s", i+1, goName, paramType, argType)
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
		case "int", "float64", "string", "bool", "byte", "struct{}":
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
	Type          IRType
	GoMultiReturn bool               // true if Go func returns multiple values (needs multi-value receive)
	TypeVars      map[string]IRType  // type param name → fresh IRTypeVar (for generic calls)
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
			return goReturnInfo{
				Type:          IRResultType{Ok: IRNamedType{GoName: "struct{}"}, Err: IRNamedType{GoName: "error"}},
				GoMultiReturn: true,
			}
		}
		return goReturnInfo{Type: toIR(info.Results[0].Type)}
	}
	if len(info.Results) == 2 {
		if info.Results[1].Type == "error" {
			return goReturnInfo{
				Type:          IRResultType{Ok: toIR(info.Results[0].Type), Err: IRNamedType{GoName: "error"}},
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
		elems[i] = toIR(r.Type)
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
		paramTypes[i] = goTypeToIRWithVars(p.Type, vars)
	}
	ret = l.goFuncReturnTypeWithVars(info, vars)
	return
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
	if vars != nil {
		if v, ok := vars[goType]; ok {
			return v
		}
	}
	return IRNamedType{GoName: goTypeToIRName(goType)}
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
	irType := expr.irType()
	if irType == nil {
		return "", "", false
	}
	named, isNamed := irType.(IRNamedType)
	if !isNamed {
		if ptr, isPtr := irType.(IRPointerType); isPtr {
			if inner, isInner := ptr.Inner.(IRNamedType); isInner {
				named = inner
				isNamed = true
			}
		}
	}
	if !isNamed || !strings.Contains(named.GoName, ".") {
		return "", "", false
	}
	parts := strings.SplitN(named.GoName, ".", 2)
	if goPkg, exists := l.lookupGoPackage(parts[0]); exists {
		return goPkg.FullPath, parts[1], true
	}
	return "", "", false
}

// resolveMethodReturnType resolves the return type of a method call on a Go or Arca type.
func (l *Lowerer) resolveMethodReturnType(receiver IRExpr, method string) goReturnInfo {
	// Try Go FFI type first
	pkg, typ, ok := l.resolveReceiverGoType(receiver)
	if ok {
		info := l.typeResolver.ResolveMethod(pkg, typ, method)
		if info != nil {
			return l.goFuncReturnType(info)
		}
	}

	// Try Arca type
	arcaTypeName := l.resolveReceiverArcaType(receiver)
	if arcaTypeName != "" {
		if td, ok := l.types[arcaTypeName]; ok {
			for _, m := range td.Methods {
				if m.Name == method || snakeToCamel(m.Name) == method || snakeToPascal(m.Name) == method {
					if m.ReturnType != nil {
						return goReturnInfo{Type: l.lowerType(m.ReturnType)}
					}
					return goReturnInfo{Type: IRNamedType{GoName: "struct{}"}}
				}
			}
		}
	}

	return goReturnInfo{Type: IRInterfaceType{}}
}

// resolveFieldType resolves the type of a field access on a Go or Arca type.
func (l *Lowerer) resolveFieldType(receiver IRExpr, field string) IRType {
	// Try Go FFI type first
	pkg, typ, ok := l.resolveReceiverGoType(receiver)
	if ok {
		typeInfo := l.typeResolver.ResolveType(pkg, typ)
		if typeInfo != nil {
			for _, f := range typeInfo.Fields {
				if f.Name == field {
					return IRNamedType{GoName: goTypeToIRName(f.Type)}
				}
			}
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
		}
	}

	return IRInterfaceType{}
}

// resolveReceiverArcaType extracts the Arca type name from an IR expression.
func (l *Lowerer) resolveReceiverArcaType(expr IRExpr) string {
	irType := expr.irType()
	if irType == nil {
		return ""
	}
	if named, ok := irType.(IRNamedType); ok {
		if !strings.Contains(named.GoName, ".") {
			return named.GoName
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
						Func: pnt.Name,
						Args: []IRExpr{inner},
						Type: IRNamedType{GoName: pnt.Name},
					}
				}
			}
		}
	}

	// Lambda with missing parameter types: infer from call context
	if lam, ok := expr.(Lambda); ok {
		if paramTypes := l.resolveCallParamFuncType(call, argIndex); paramTypes != nil {
			lam = l.inferLambdaParamTypes(lam, paramTypes)
		}
		return l.lowerLambda(lam)
	}

	// Resolve expected type for hint-based type checking and constructor type inference
	var hint IRType
	if fnIdent, ok := call.Fn.(Ident); ok {
		if fn, ok := l.functions[fnIdent.Name]; ok && argIndex < len(fn.Params) {
			hint = l.lowerType(fn.Params[argIndex].Type)
		}
	}
	result := l.lowerExprHint(expr, hint)
	return result
}

// resolveCallParamFuncType resolves the Go function type for a parameter at argIndex.
// Returns the FuncInfo if the parameter is a function type, nil otherwise.
func (l *Lowerer) resolveCallParamFuncType(call FnCall, argIndex int) *FuncInfo {
	if fa, ok := call.Fn.(FieldAccess); ok {
		// Method call: resolve receiver type → method signature → param type
		if ident, ok := fa.Expr.(Ident); ok {
			if goPkg, isGoPkg := l.lookupGoPackage(ident.Name); isGoPkg {
				if info := l.typeResolver.ResolveFunc(goPkg.FullPath, fa.Field); info != nil {
					if argIndex < len(info.Params) {
						return l.parseFuncType(info.Params[argIndex].Type)
					}
				}
				return nil
			}
		}
		// Method on receiver
		receiver := l.lowerExpr(fa.Expr)
		pkg, typ, ok := l.resolveReceiverGoType(receiver)
		if ok {
			if info := l.typeResolver.ResolveMethod(pkg, typ, fa.Field); info != nil {
				if argIndex < len(info.Params) {
					return l.parseFuncType(info.Params[argIndex].Type)
				}
			}
		}
	}
	return nil
}

// parseFuncType parses a Go type string like "func(echo.Context) error" into FuncInfo.
// Also resolves type aliases (e.g. echo.HandlerFunc → func(Context) error).
func (l *Lowerer) parseFuncType(goType string) *FuncInfo {
	if !strings.HasPrefix(goType, "func(") {
		// Try resolving as a type alias with underlying func type
		resolved := l.typeResolver.ResolveUnderlying(goType)
		if resolved != "" && strings.HasPrefix(resolved, "func(") {
			goType = resolved
		} else {
			return nil
		}
	}
	// Use TypeResolver to get detailed function signature
	// For now, extract param types from the string
	// "func(echo.Context) error" → params: ["echo.Context"], results: ["error"]
	inner := goType[5:] // strip "func("
	parenEnd := strings.Index(inner, ")")
	if parenEnd < 0 {
		return nil
	}
	paramStr := inner[:parenEnd]
	resultStr := strings.TrimSpace(inner[parenEnd+1:])

	info := &FuncInfo{}
	if paramStr != "" {
		for _, p := range strings.Split(paramStr, ", ") {
			p = strings.TrimSpace(p)
			// Named params: "c *echo.Context" → type is "*echo.Context"
			if spaceIdx := strings.LastIndex(p, " "); spaceIdx >= 0 {
				p = p[spaceIdx+1:]
			}
			info.Params = append(info.Params, ParamInfo{Type: p})
		}
	}
	if resultStr != "" {
		info.Results = append(info.Results, ParamInfo{Type: resultStr})
	}
	return info
}

// inferLambdaParamTypes fills in missing parameter types from a Go function signature.
func (l *Lowerer) inferLambdaParamTypes(lam Lambda, funcType *FuncInfo) Lambda {
	for i := range lam.Params {
		if lam.Params[i].Type == nil && i < len(funcType.Params) {
			goType := funcType.Params[i].Type
			lam.Params[i].Type = l.goTypeToArcaType(goType)
		}
	}
	return lam
}

// goTypeToArcaType converts a Go type string to an Arca AST type.
func (l *Lowerer) goTypeToArcaType(goType string) Type {
	if strings.HasPrefix(goType, "*") {
		inner := l.goTypeToArcaType(goType[1:])
		if inner != nil {
			return PointerType{Inner: inner}
		}
		return nil
	}
	return NamedType{Name: goTypeToIRName(goType)}
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
	fieldType := l.resolveFieldType(receiver, capitalize(e.Field))
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

func (l *Lowerer) lowerLambdaHint(lam Lambda, hint IRType) IRExpr {
	return l.lowerLambda(lam)
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
			Name:   p.Name,
			IRType: typ,
			Kind:   SymParameter,
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
	return IRLambda{
		Params:     params,
		ReturnType: retType,
		Body:       body,
		Type:       IRInterfaceType{}, // lambda type is opaque to Go FFI arg checking
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
		// Try unwraps Result: the variable gets the Ok type
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
	if l.currentRetType != nil {
		retType = l.lowerType(l.currentRetType)
	}
	if isIRResultType(retType) {
		l.builtins["result"] = true
	}

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
	if l.isLiteralMatch(me) {
		return l.lowerLiteralMatch(me)
	}
	if l.isEnumMatch(me) {
		return l.lowerEnumMatch(me)
	}
	return l.lowerSumTypeMatch(me)
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
	return IRMatch{Subject: subject, Arms: arms, Type: l.inferMatchType(arms), Pos: pos}
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

// lowerPreludeArgs lowers arguments for prelude functions (map, filter, fold),
// inferring lambda parameter types from the list element type.
func (l *Lowerer) lowerPreludeArgs(fnName string, args []Expr) []IRExpr {
	// For map/filter/takeWhile: first arg is list, second is lambda
	if (fnName == "map" || fnName == "filter" || fnName == "takeWhile") && len(args) == 2 {
		listArg := l.lowerExpr(args[0])
		if lam, ok := args[1].(Lambda); ok {
			if lt, ok := listArg.irType().(IRListType); ok {
				// Set lambda param type from list element type
				for i := range lam.Params {
					if lam.Params[i].Type == nil {
						lam.Params[i].Type = l.irTypeToASTType(lt.Elem)
					}
				}
			}
			return []IRExpr{listArg, l.lowerLambda(lam)}
		}
		return []IRExpr{listArg, l.lowerExpr(args[1])}
	}
	// For fold: first arg is list, second is init, third is lambda
	if fnName == "fold" && len(args) == 3 {
		listArg := l.lowerExpr(args[0])
		initArg := l.lowerExpr(args[1])
		if lam, ok := args[2].(Lambda); ok {
			if lt, ok := listArg.irType().(IRListType); ok {
				// fold lambda: (acc, elem) -> acc
				if len(lam.Params) >= 1 && lam.Params[0].Type == nil {
					lam.Params[0].Type = l.irTypeToASTType(initArg.irType())
				}
				if len(lam.Params) >= 2 && lam.Params[1].Type == nil {
					lam.Params[1].Type = l.irTypeToASTType(lt.Elem)
				}
			}
			return []IRExpr{listArg, initArg, l.lowerLambda(lam)}
		}
		return []IRExpr{listArg, initArg, l.lowerExpr(args[2])}
	}
	return l.lowerExprs(args)
}

// irTypeToASTType converts an IRType back to an AST Type for lambda param inference.
// inferPreludeReturnType infers the return type of prelude functions from their arguments.
func (l *Lowerer) inferPreludeReturnType(name string, args []IRExpr) IRType {
	switch name {
	case "map":
		// map(list, f) → []U where U is f's return type
		if len(args) == 2 {
			if lam, ok := args[1].(IRLambda); ok && lam.ReturnType != nil {
				return IRListType{Elem: lam.ReturnType}
			}
			// Fallback: same element type as input list
			if lt, ok := args[0].irType().(IRListType); ok {
				return lt
			}
		}
	case "filter", "take", "takeWhile":
		// filter/take/takeWhile(list, ...) → same list type
		if len(args) >= 1 {
			return args[0].irType()
		}
	case "fold":
		// fold(list, init, f) → type of init
		if len(args) == 3 {
			return args[1].irType()
		}
	}
	return IRInterfaceType{}
}

func (l *Lowerer) irTypeToASTType(t IRType) Type {
	switch tt := t.(type) {
	case IRNamedType:
		return NamedType{Name: tt.GoName}
	case IRListType:
		inner := l.irTypeToASTType(tt.Elem)
		if inner != nil {
			return NamedType{Name: "List", Params: []Type{inner}}
		}
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
