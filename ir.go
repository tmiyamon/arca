package main

// IR — Intermediate Representation
//
// All Arca concepts (Self, shadowing, builtins, constructors) are resolved.
// Every expression carries a resolved Go type.
// Match expressions are structurally exhaustive.

// --- Types ---

type IRType interface {
	irTypeNode()
}

type IRNamedType struct {
	GoName string   // "int", "string", "User", "Email", "Greeting"
	Params []IRType // generic params
}

type IRPointerType struct {
	Inner IRType
}

// IRRefType is Arca's safe non-null reference (Ref[T]).
// Distinct from IRPointerType (FFI-internal raw Go pointer).
// Both emit as *T in Go; the distinction is in semantic guarantees
// enforced via construction rules in the lowerer.
type IRRefType struct {
	Inner IRType
}

type IRTupleType struct {
	Elements []IRType
}

type IRListType struct {
	Elem IRType
}

type IRMapType struct {
	Key   IRType
	Value IRType
}

type IRResultType struct {
	Ok  IRType
	Err IRType
}

type IROptionType struct {
	Inner IRType
}

type IRInterfaceType struct{} // user-explicit Any (interface{})

// IRError is the placeholder type for an expression whose type the lowerer
// could not resolve. Distinct from IRInterfaceType (which represents user-
// requested Any): IRError signals a resolution failure that has already
// raised a compile error, but the lowerer kept building IR so partial-code
// flows (LSP, cascading-error suppression) keep working. Hint optionally
// carries the inferred type when a near-miss was found (e.g. a method seen
// where a field was written) — diagnostic reporters and LSP hover may
// consume Hint to surface richer info; downstream IR / unify treat IRError
// as "broken, propagate without further error".
type IRError struct {
	Reason string
	Hint   IRType
}

// IRTraitType is the Arca trait used as a type (trait object).
// Emitted as a Go interface named Arca<Name>. Distinct from
// IRInterfaceType (Any / interface{}): trait objects have a declared
// method set, while IRInterfaceType is open.
type IRTraitType struct {
	Name string // Arca trait name, e.g. "Error"
}

// IRFnType is the IR representation of a function type (`A -> B`,
// `(A, B) -> C`). n-ary, structural and invariant under unify; emits as Go
// `func(A, B) C`. Introduced in the 2026-04-22 function-types design.
type IRFnType struct {
	Params []IRType
	Ret    IRType
}

type IRTypeVar struct {
	ID int // unique identifier for this type variable
}

func (IRNamedType) irTypeNode()     {}
func (IRPointerType) irTypeNode()   {}
func (IRRefType) irTypeNode()       {}
func (IRTupleType) irTypeNode()     {}
func (IRListType) irTypeNode()      {}
func (IRMapType) irTypeNode()       {}
func (IRResultType) irTypeNode()    {}
func (IROptionType) irTypeNode()    {}
func (IRInterfaceType) irTypeNode() {}
func (IRError) irTypeNode()         {}
func (IRTraitType) irTypeNode()     {}
func (IRFnType) irTypeNode()        {}
func (IRTypeVar) irTypeNode()       {}

// --- Program ---

type IRProgram struct {
	Package  string
	Imports  []IRImport
	Types    []IRTypeDecl
	Globals  []IRGlobalVar
	Funcs    []IRFuncDecl
	Builtins []string // "result", "option", "map", "filter", "fold"
}

// IRGlobalVar is a top-level Go `var Name Type = Init` declaration. Arca's
// user surface has no top-level vars; this is reserved for compiler-emitted
// state such as the `__<TypeName><TraitName>` Bindable dictionary instances
// (decisions/ffi.md 2026-05-04 refined Synthetic Builder, B2c).
type IRGlobalVar struct {
	GoName string
	Type   IRType
	Init   IRExpr
}

type IRImport struct {
	Path       string
	SideEffect bool
}

// --- Type Declarations ---

type IRTypeDecl interface {
	irTypeDeclNode()
}

type IREnumDecl struct {
	GoName   string   // "Color"
	Variants []string // ["Red", "Green", "Blue"]
}

type IRStructDecl struct {
	GoName     string
	TypeParams []string // ["T", "U"] for generics
	Fields     []IRFieldDecl
	Tags       []TagRule
	Validator  *IRValidator // nil if no constraints
}

type IRSumTypeDecl struct {
	GoName           string
	TypeParams       []string
	Variants         []IRVariantDecl
	InterfaceMethods []IRInterfaceMethod // methods that variants implement
}

type IRInterfaceMethod struct {
	Name       string // Go method name
	Params     []IRParamDecl
	ReturnType IRType
}

type IRTypeAliasDecl struct {
	GoName    string // "Email"
	GoBase    string // "string"
	Validator *IRValidator
}

// TraitKind classifies a trait by its dispatch strategy. Determined by
// object-safety analysis (see analyzeTraitObjectSafety in lower.go):
// object-safe traits (only &self methods, no Self outside receiver, no
// static fun, no associated types) emit as Go interfaces dispatched via
// vtable; object-unsafe traits emit as compiler-generated dictionary
// structs threaded through generic functions as hidden parameters.
//
// Per the 2026-05-02 (refined) Synthetic Builder design in
// decisions/ffi.md: every Phase 1 trait is currently object-safe and
// thus TraitKindVtable. The TraitKindDictionary path is infrastructure
// for B2 onwards.
type TraitKind int

const (
	TraitKindVtable TraitKind = iota
	TraitKindDictionary
)

func (k TraitKind) String() string {
	switch k {
	case TraitKindVtable:
		return "vtable"
	case TraitKindDictionary:
		return "dictionary"
	}
	return "unknown"
}

// IRTraitDecl emits as a Go interface declaration when Kind is Vtable.
// Impl methods satisfy it via Go's structural interface rule; no explicit
// registration needed. Dictionary-kind traits emit differently — see B2.
type IRTraitDecl struct {
	GoName  string // Arca<Name>, e.g. "ArcaError"
	Methods []IRInterfaceMethod
	Kind    TraitKind
}

type IRVariantDecl struct {
	GoName string // "GreetingHello"
	Fields []IRFieldDecl
}

type IRFieldDecl struct {
	GoName string // "Name" (capitalized)
	Type   IRType
	Tag    string // Go struct tag, empty if none
}

// Validator for constrained types
type IRValidator struct {
	Checks []IRValidationCheck
}

type IRValidationCheck struct {
	Kind     string // "min", "max", "min_length", "max_length", "pattern", "validate"
	Field    string // field variable name
	Value    string // Go expression for the constraint value
	ZeroVal  string // Go zero value for error return
	TypeName string // for error messages
}

func (IREnumDecl) irTypeDeclNode()      {}
func (IRStructDecl) irTypeDeclNode()    {}
func (IRSumTypeDecl) irTypeDeclNode()   {}
func (IRTypeAliasDecl) irTypeDeclNode() {}
func (IRTraitDecl) irTypeDeclNode()     {}

// --- Source Info ---

// SourceInfo carries original Arca source information for error messages and validation.
// Not part of the resolved IR — purely for display and checking.
type SourceInfo struct {
	Pos        Pos
	Name       string // Arca name (function name, constructor name, field name, variable name)
	TypeName   string // owning type name (for constructors: "Greeting" in Greeting.Hello)
	Type       Type   // Arca AST type (for params, idents)
	ReturnType Type   // Arca return type (for functions)
}

// --- Function Declarations ---

// IRFn is the unified representation for a function in any position:
// top-level declaration (GoName set, emitted as `func Name(...) { ... }`),
// method (Receiver set), or inline lambda literal (GoName empty, emitted
// as `func(...) { ... }`). IRFuncDecl is retained as an alias for readability
// in declaration contexts; both names denote the same type.
type IRFn struct {
	GoName     string       // "" for anonymous (lambda literal)
	Receiver   *IRReceiver  // nil for free functions and lambdas
	TypeParams []string     // Go type-param names for `func F[T any](...)`; constraint info lives at the lowerer level
	Params     []IRParamDecl
	Ret        IRType       // nil for void
	Body       IRExpr
	Source     SourceInfo
}

type IRFuncDecl = IRFn

type IRReceiver struct {
	GoName string // "u"
	Type   string // "User"
}

type IRParamDecl struct {
	GoName string
	Type   IRType
	Source SourceInfo
}

// --- Expressions ---
// Every expression carries a resolved type.

type IRExpr interface {
	irExprNode()
	irType() IRType
}

// Literals
type IRIntLit struct {
	Value int64
	Type  IRType
}

type IRFloatLit struct {
	Value float64
	Type  IRType
}

type IRStringLit struct {
	Value     string
	Type      IRType
	Multiline bool
}

type IRBoolLit struct {
	Value bool
	Type  IRType
}

// Identifiers — fully resolved
type IRIdent struct {
	GoName string // "email_2", "fmt.Println", etc.
	Type   IRType
	Source SourceInfo
}

// String interpolation — resolved to fmt.Sprintf
type IRStringInterp struct {
	Format    string   // "Hello, %v!"
	Multiline bool
	Args   []IRExpr
	Type   IRType
}

// Function call.
//
// Fn carries the callee as an expression: IRIdent for a named ref
// ("fmt.Println", "userFrom"), IRFn for an inline lambda, or any other
// function-valued IR expression. Emit always renders via
// `emitExpr(Fn)(args)`, so no special-casing is needed on the consumer
// side.
type IRFnCall struct {
	Fn            IRExpr
	Args          []IRExpr
	Type          IRType
	TypeArgs      string   // "[User]" for explicit Go generic type args, empty if inferred
	Source        SourceInfo
	GoMultiReturn bool // true if Go func returns multiple values (needs multi-value receive)
}

// Method call: expr.Method(args)
type IRMethodCall struct {
	Receiver      IRExpr
	Method        string
	Args          []IRExpr
	Type          IRType
	GoMultiReturn bool // true if Go method returns multiple values (needs multi-value receive)
}

// Field access: expr.Field
type IRFieldAccess struct {
	Expr  IRExpr
	Field string // Go field name (capitalized)
	Type  IRType
}

type IRIndexAccess struct {
	Expr  IRExpr
	Index IRExpr
	Type  IRType
}

type IRIfExpr struct {
	Cond IRExpr
	Then IRExpr
	Else IRExpr // nil if no else
	Type IRType
}

// Block: statements + optional final expression
type IRBlock struct {
	Stmts []IRStmt
	Expr  IRExpr // nil for void blocks
	Type  IRType
}

// IRTryBlock represents `try { ... }`. Emitted as an IIFE returning (T, error).
type IRTryBlock struct {
	Stmts   []IRStmt
	Expr    IRExpr // final expression (Ok value)
	OkType  IRType // inner Ok type
	ErrType IRType // error type (always IRNamedType{GoName: "error"})
}

// IRTryExpr represents `expr?` in expression position. Stage 2 hoists the
// computation to a synthetic GoMultiAssign + GoIfElse{GoReturn} ahead of
// the enclosing statement and substitutes the expression with an ident
// referring to the Ok-typed split value. ReturnType is the enclosing
// function's (or try-block IIFE's) Result return type, used to build the
// error-propagation return values.
type IRTryExpr struct {
	Inner      IRExpr // must produce a Result
	OkType     IRType
	ErrType    IRType
	ReturnType IRType // enclosing fn / try block return type
	Pos        Pos
}

// Constructor: resolved to Go struct literal or NewType() call
type IRConstructorCall struct {
	GoName        string // "GreetingHello", "User"
	Fields        []IRFieldValue
	TypeArgs      string // "[int, string]" for generics, empty otherwise
	GoMultiReturn bool   // true if constrained (NewType returns (T, error))
	Type          IRType
	Source        SourceInfo // Name = ctor name, TypeName = type name
}

type IRFieldValue struct {
	GoName string // "Name" (capitalized), empty for positional
	Value  IRExpr
	Source SourceInfo // Name = original Arca field name
}

// Builtin constructors. Stage 2 lowering computes the multi-return form
// for Ok/Error inline (see expandedValuesOf) and rewrites Some/None into
// GoPtrOf / GoTypedNil — no sideband fields needed.
type IROkCall struct {
	Value    IRExpr
	TypeArgs string // "[User, error]"
	Type     IRType
}

type IRErrorCall struct {
	Value    IRExpr
	TypeArgs string
	Type     IRType
}

type IRSomeCall struct {
	Value IRExpr
	Type  IRType
}

type IRNoneExpr struct {
	TypeArg string // "[string]" etc.
	Type    IRType
}

// Binary expression
type IRBinaryExpr struct {
	Op   string
	Left IRExpr
	Right IRExpr
	Type  IRType
}

// List literal
type IRListLit struct {
	ElemType string // Go element type
	Elements []IRExpr
	Spread   IRExpr // nil if no spread
	Type     IRType
}

type IRMapLit struct {
	KeyType   string
	ValueType string
	Entries   []IRMapEntry
	Type      IRType
}

type IRMapEntry struct {
	Key   IRExpr
	Value IRExpr
}

// Tuple literal
type IRTupleLit struct {
	Elements []IRExpr
	Type     IRType
}

// Ref (address-of for Go FFI)
type IRRefExpr struct {
	Expr IRExpr
	Type IRType
}

// For loop
type IRForRange struct {
	Binding string // Go variable name
	Start   IRExpr // for range: start value
	End     IRExpr // for range: end value
	Body    IRExpr
	Type    IRType
}

type IRForEach struct {
	Binding string
	Iter    IRExpr
	Body    IRExpr
	Type    IRType
}

// --- Unified Match Expression ---

type IRMatch struct {
	Subject IRExpr
	Arms    []IRMatchArm
	Type    IRType
	Pos     Pos
}

type IRMatchArm struct {
	Pattern IRMatchPattern
	Body    IRExpr
}

// IRMatchPattern interface — each pattern type expresses a match kind + bindings
type IRMatchPattern interface {
	irMatchPatternNode()
}

type IRResultOkPattern struct{ Binding *IRBinding }
type IRResultErrorPattern struct{ Binding *IRBinding }
type IROptionSomePattern struct{ Binding *IRBinding }

func (p IRResultOkPattern) GetBinding() *IRBinding    { return p.Binding }
func (p IRResultErrorPattern) GetBinding() *IRBinding  { return p.Binding }
func (p IROptionSomePattern) GetBinding() *IRBinding   { return p.Binding }
type IROptionNonePattern struct{}
type IREnumPattern struct{ GoValue string }
type IRSumTypePattern struct {
	GoType   string
	Bindings []IRBinding
}
type IRSumTypeWildcardPattern struct{ Binding *IRBinding }
type IRListEmptyPattern struct{}
type IRListExactPattern struct {
	Elements []IRBinding
	MinLen   int
}
type IRListConsPattern struct {
	Elements []IRBinding
	Rest     *IRBinding
	MinLen   int
}
type IRListDefaultPattern struct{ Binding *IRBinding }
type IRLiteralPattern struct{ Value string }
type IRLiteralDefaultPattern struct{}
type IRWildcardPattern struct{}

// IRMatchTypePattern narrows an Any subject to a concrete Go type in a
// match arm. Emits as a `case T:` clause in a Go type switch; the binding
// receives the narrowed value.
type IRMatchTypePattern struct {
	Binding *IRBinding
	Target  IRType
}

func (p IRMatchTypePattern) GetBinding() *IRBinding { return p.Binding }

func (IRResultOkPattern) irMatchPatternNode()       {}
func (IRResultErrorPattern) irMatchPatternNode()     {}
func (IROptionSomePattern) irMatchPatternNode()      {}
func (IROptionNonePattern) irMatchPatternNode()      {}
func (IREnumPattern) irMatchPatternNode()            {}
func (IRSumTypePattern) irMatchPatternNode()         {}
func (IRSumTypeWildcardPattern) irMatchPatternNode() {}
func (IRListEmptyPattern) irMatchPatternNode()       {}
func (IRListExactPattern) irMatchPatternNode()       {}
func (IRListConsPattern) irMatchPatternNode()        {}
func (IRListDefaultPattern) irMatchPatternNode()     {}
func (IRLiteralPattern) irMatchPatternNode()         {}
func (IRMatchTypePattern) irMatchPatternNode()       {}
func (IRLiteralDefaultPattern) irMatchPatternNode()  {}
func (IRWildcardPattern) irMatchPatternNode()        {}

// IRVoidExpr represents "no value" — used in void context (e.g. match arm that does nothing)
type IRVoidExpr struct{}

func (m IRMatch) irExprNode()     {}
func (m IRMatch) irType() IRType  { return m.Type }
func (IRVoidExpr) irExprNode()   {}
func (IRVoidExpr) irType() IRType { return IRNamedType{GoName: "struct{}"} }


type IRBinding struct {
	GoName string // resolved Go variable name
	Source string // Go expression to extract value (e.g. "v.Name", "subject.Value")
}

// --- Statements ---

type IRStmt interface {
	irStmtNode()
}

type IRLetStmt struct {
	GoName string
	Value  IRExpr
	Type   IRType
	Pos    Pos // source position of the `let` keyword, used for diagnostics
}

// Try let: let x = expr? — error propagation.
// Split names and error return values are computed by stage2's
// lowerTryLetStmt; only the user-visible fields and the enclosing-fn
// return type live here.
type IRTryLetStmt struct {
	GoName     string // "_" for discard
	CallExpr   IRExpr
	ReturnType IRType // enclosing function's return type
}

type IRExprStmt struct {
	Expr IRExpr
}

type IRDeferStmt struct {
	Expr IRExpr
}

type IRAssertStmt struct {
	Expr    IRExpr
	ExprStr string // original expression string for panic message
}

// Let destructuring
type IRDestructureKind int

const (
	IRDestructureTuple IRDestructureKind = iota
	IRDestructureList
)

type IRDestructureStmt struct {
	Kind     IRDestructureKind
	Bindings []IRDestructureBinding
	Value    IRExpr
}

type IRDestructureBinding struct {
	GoName string
	Index  int    // for list/tuple element access
	Slice  bool   // true for rest binding (list[N:])
}

func (IRLetStmt) irStmtNode()    {}
func (IRTryLetStmt) irStmtNode() {}
func (IRExprStmt) irStmtNode()           {}
func (IRDeferStmt) irStmtNode()          {}
func (IRAssertStmt) irStmtNode()         {}
func (IRDestructureStmt) irStmtNode()    {}

// --- Expr interface implementations ---

func (e IRIntLit) irExprNode()          {}
func (e IRIntLit) irType() IRType       { return e.Type }
func (e IRFloatLit) irExprNode()        {}
func (e IRFloatLit) irType() IRType     { return e.Type }
func (e IRStringLit) irExprNode()       {}
func (e IRStringLit) irType() IRType    { return e.Type }
func (e IRBoolLit) irExprNode()         {}
func (e IRBoolLit) irType() IRType      { return e.Type }
func (e IRIdent) irExprNode()           {}
func (e IRIdent) irType() IRType        { return e.Type }
func (e IRStringInterp) irExprNode()    {}
func (e IRStringInterp) irType() IRType { return e.Type }
func (e IRFnCall) irExprNode()          {}
func (e IRFnCall) irType() IRType       { return e.Type }
func (e IRMethodCall) irExprNode()      {}
func (e IRMethodCall) irType() IRType   { return e.Type }
func (e IRFieldAccess) irExprNode()     {}
func (e IRFieldAccess) irType() IRType  { return e.Type }
func (e IRIndexAccess) irExprNode()     {}
func (e IRIndexAccess) irType() IRType  { return e.Type }
func (e IRIfExpr) irExprNode()          {}
func (e IRIfExpr) irType() IRType       { return e.Type }
func (e IRBlock) irExprNode()           {}
func (e IRBlock) irType() IRType        { return e.Type }
func (e IRTryBlock) irExprNode()     {}
func (e IRTryBlock) irType() IRType  { return IRResultType{Ok: e.OkType, Err: e.ErrType} }
func (e IRTryExpr) irExprNode()      {}
func (e IRTryExpr) irType() IRType   { return e.OkType }
func (e IRConstructorCall) irExprNode()    {}
func (e IRConstructorCall) irType() IRType { return e.Type }
func (e IROkCall) irExprNode()          {}
func (e IROkCall) irType() IRType       { return e.Type }
func (e IRErrorCall) irExprNode()       {}
func (e IRErrorCall) irType() IRType    { return e.Type }
func (e IRSomeCall) irExprNode()        {}
func (e IRSomeCall) irType() IRType     { return e.Type }
func (e IRNoneExpr) irExprNode()        {}
func (e IRNoneExpr) irType() IRType     { return e.Type }
func (e IRFn) irExprNode()              {}
func (e IRFn) irType() IRType {
	params := make([]IRType, len(e.Params))
	for i, p := range e.Params {
		params[i] = p.Type
	}
	return IRFnType{Params: params, Ret: e.Ret}
}
func (e IRBinaryExpr) irExprNode()      {}
func (e IRBinaryExpr) irType() IRType   { return e.Type }
func (e IRListLit) irExprNode()         {}
func (e IRListLit) irType() IRType      { return e.Type }
func (e IRMapLit) irExprNode()          {}
func (e IRMapLit) irType() IRType       { return e.Type }
func (e IRTupleLit) irExprNode()        {}
func (e IRTupleLit) irType() IRType     { return e.Type }
func (e IRRefExpr) irExprNode()         {}
func (e IRRefExpr) irType() IRType      { return e.Type }
func (e IRForRange) irExprNode()        {}
func (e IRForRange) irType() IRType     { return e.Type }
func (e IRForEach) irExprNode()         {}
func (e IRForEach) irType() IRType      { return e.Type }

// isGoMultiReturn checks if an IR expression is a call with GoMultiReturn set.
func isGoMultiReturn(e IRExpr) bool {
	switch expr := e.(type) {
	case IRFnCall:
		return expr.GoMultiReturn
	case IRMethodCall:
		return expr.GoMultiReturn
	case IRConstructorCall:
		return expr.GoMultiReturn
	}
	return false
}
