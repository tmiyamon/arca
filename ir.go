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

type IRTupleType struct {
	Elements []IRType
}

type IRListType struct {
	Elem IRType
}

type IRResultType struct {
	Ok  IRType
	Err IRType
}

type IROptionType struct {
	Inner IRType
}

type IRInterfaceType struct{} // fallback: interface{}

func (IRNamedType) irTypeNode()     {}
func (IRPointerType) irTypeNode()   {}
func (IRTupleType) irTypeNode()     {}
func (IRListType) irTypeNode()      {}
func (IRResultType) irTypeNode()    {}
func (IROptionType) irTypeNode()    {}
func (IRInterfaceType) irTypeNode() {}

// --- Program ---

type IRProgram struct {
	Package  string
	Imports  []IRImport
	Types    []IRTypeDecl
	Funcs    []IRFuncDecl
	Builtins []string // "result", "option", "map", "filter", "fold"
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
	GoName     string
	TypeParams []string
	Variants   []IRVariantDecl
}

type IRTypeAliasDecl struct {
	GoName    string // "Email"
	GoBase    string // "string"
	Validator *IRValidator
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

// --- Function Declarations ---

type IRFuncDecl struct {
	GoName     string
	Receiver   *IRReceiver // nil for free functions and associated functions
	Params     []IRParamDecl
	ReturnType IRType // nil for void
	Body       IRExpr
}

type IRReceiver struct {
	GoName string // "u"
	Type   string // "User"
}

type IRParamDecl struct {
	GoName string
	Type   IRType
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
	Value string
	Type  IRType
}

type IRBoolLit struct {
	Value bool
	Type  IRType
}

// Identifiers — fully resolved
type IRIdent struct {
	GoName string // "email_2", "fmt.Println", etc.
	Type   IRType
}

// String interpolation — resolved to fmt.Sprintf
type IRStringInterp struct {
	Format string   // "Hello, %v!"
	Args   []IRExpr
	Type   IRType
}

// Function call
type IRFnCall struct {
	Func string   // "fmt.Println", "message", "userFrom", etc.
	Args []IRExpr
	Type IRType
}

// Method call: expr.Method(args)
type IRMethodCall struct {
	Receiver IRExpr
	Method   string
	Args     []IRExpr
	Type     IRType
}

// Field access: expr.Field
type IRFieldAccess struct {
	Expr  IRExpr
	Field string // Go field name (capitalized)
	Type  IRType
}

// Block: statements + optional final expression
type IRBlock struct {
	Stmts []IRStmt
	Expr  IRExpr // nil for void blocks
	Type  IRType
}

// Constructor: resolved to Go struct literal or NewType() call
type IRConstructorCall struct {
	GoName        string // "GreetingHello", "User"
	Fields        []IRFieldValue
	TypeArgs      string // "[int, string]" for generics, empty otherwise
	ReturnsResult bool   // true if constrained (NewType returns (T, error))
	Type          IRType
}

type IRFieldValue struct {
	GoName string // "Name" (capitalized), empty for positional
	Value  IRExpr
}

// Builtin constructors
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

// Lambda
type IRLambda struct {
	Params     []IRParamDecl
	ReturnType IRType // nil if not annotated
	Body       IRExpr
	Type       IRType
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

// --- Match Expressions (structurally exhaustive) ---

type IRResultMatch struct {
	Subject  IRExpr
	OkArm    IRResultArm
	ErrorArm IRResultArm
	Type     IRType
}

type IRResultArm struct {
	Binding *IRBinding // nil if not bound
	Body    IRExpr
}

type IROptionMatch struct {
	Subject IRExpr
	SomeArm IROptionSomeArm
	NoneArm IRExpr
	Type    IRType
}

type IROptionSomeArm struct {
	Binding *IRBinding
	Body    IRExpr
}

type IREnumMatch struct {
	Subject  IRExpr
	Arms     []IREnumArm // one per variant, ordered
	Wildcard *IRExpr     // nil if all variants covered
	Type     IRType
}

type IREnumArm struct {
	GoValue string // "ColorRed"
	Body    IRExpr
}

type IRSumTypeMatch struct {
	Subject  IRExpr
	Arms     []IRSumTypeArm
	Wildcard *IRSumTypeWildcard // nil if all variants covered
	Type     IRType
}

type IRSumTypeArm struct {
	GoType   string       // "GreetingHello"
	Bindings []IRBinding
	Body     IRExpr
}

type IRSumTypeWildcard struct {
	Binding *IRBinding // nil for _, non-nil for named wildcard
	Body    IRExpr
}

type IRListMatch struct {
	Subject IRExpr
	Arms    []IRListArm
	Type    IRType
}

type IRListArm struct {
	Kind     IRListArmKind
	Elements []IRBinding
	Rest     *IRBinding // nil if no rest
	MinLen   int
	Body     IRExpr
}

type IRListArmKind int

const (
	IRListEmpty   IRListArmKind = iota
	IRListExact                         // [a, b] — exact length
	IRListCons                          // [a, ..rest] — at least N elements
	IRListDefault                       // _ or bind
)

type IRLiteralMatch struct {
	Subject IRExpr
	Arms    []IRLiteralArm
	Default *IRExpr // nil if no default
	Type    IRType
}

type IRLiteralArm struct {
	Value string // Go literal expression
	Body  IRExpr
}

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
}

// Let with constrained constructor: generates Result wrapping
type IRConstrainedLetStmt struct {
	GoName   string
	CallExpr IRExpr // the constructor call expression
	GoType   string // "Email"
}

// Try let: let x = expr? — error propagation
type IRTryLetStmt struct {
	GoName     string // "_" for discard
	CallExpr   IRExpr
	ReturnType IRType // enclosing function's return type, for Err_ type args
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

func (IRLetStmt) irStmtNode()            {}
func (IRConstrainedLetStmt) irStmtNode() {}
func (IRTryLetStmt) irStmtNode()         {}
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
func (e IRBlock) irExprNode()           {}
func (e IRBlock) irType() IRType        { return e.Type }
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
func (e IRLambda) irExprNode()          {}
func (e IRLambda) irType() IRType       { return e.Type }
func (e IRBinaryExpr) irExprNode()      {}
func (e IRBinaryExpr) irType() IRType   { return e.Type }
func (e IRListLit) irExprNode()         {}
func (e IRListLit) irType() IRType      { return e.Type }
func (e IRTupleLit) irExprNode()        {}
func (e IRTupleLit) irType() IRType     { return e.Type }
func (e IRRefExpr) irExprNode()         {}
func (e IRRefExpr) irType() IRType      { return e.Type }
func (e IRForRange) irExprNode()        {}
func (e IRForRange) irType() IRType     { return e.Type }
func (e IRForEach) irExprNode()         {}
func (e IRForEach) irType() IRType      { return e.Type }
func (e IRResultMatch) irExprNode()     {}
func (e IRResultMatch) irType() IRType  { return e.Type }
func (e IROptionMatch) irExprNode()     {}
func (e IROptionMatch) irType() IRType  { return e.Type }
func (e IREnumMatch) irExprNode()       {}
func (e IREnumMatch) irType() IRType    { return e.Type }
func (e IRSumTypeMatch) irExprNode()    {}
func (e IRSumTypeMatch) irType() IRType { return e.Type }
func (e IRListMatch) irExprNode()       {}
func (e IRListMatch) irType() IRType    { return e.Type }
func (e IRLiteralMatch) irExprNode()    {}
func (e IRLiteralMatch) irType() IRType { return e.Type }
