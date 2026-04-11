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

type IRInterfaceType struct{} // fallback: interface{}

type IRTypeVar struct {
	ID int // unique identifier for this type variable
}

func (IRNamedType) irTypeNode()     {}
func (IRPointerType) irTypeNode()   {}
func (IRTupleType) irTypeNode()     {}
func (IRListType) irTypeNode()      {}
func (IRMapType) irTypeNode()       {}
func (IRResultType) irTypeNode()    {}
func (IROptionType) irTypeNode()    {}
func (IRInterfaceType) irTypeNode() {}
func (IRTypeVar) irTypeNode()       {}

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

type IRFuncDecl struct {
	GoName     string
	Receiver   *IRReceiver // nil for free functions and associated functions
	Params     []IRParamDecl
	ReturnType IRType // nil for void
	Body       IRExpr
	Source     SourceInfo
}

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

// Function call
type IRFnCall struct {
	Func          string   // "fmt.Println", "message", "userFrom", etc.
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
