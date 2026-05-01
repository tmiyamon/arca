package main

// go_ir.go — Stage 2 IR (Go-structure-near).
//
// Stage 1 IR (defined in ir.go) is Arca-semantic: IRMatch / IRSomeCall /
// IRResultType etc. Stage 2 IR mirrors Go's syntactic structure so emit
// becomes a pretty-printer with no semantic decisions.
//
// stage2Lower (introduced in slice S2 of the 2026-05-02
// "Two-stage IR completion" plan in decisions/ideas.md) rewrites Stage 1
// control-flow / let / Result-Option-constructor nodes into the Stage 2
// shapes below. Shared nodes (IRBinaryExpr, IRIdent, IRFnCall, …) cross
// the boundary unchanged.
//
// Convention: Stage 2 nodes use the `Go` prefix to make the boundary
// visible at every site. They satisfy the same IRStmt / IRExpr interfaces
// as Stage 1 — separation is enforced as an invariant of stage2Lower
// rather than via the type system.

// --- Container ---

// GoBlock is a sequence of Go statements. Distinct from Stage 1 IRBlock,
// which carries a tail expression yielding the block's value: a GoBlock
// has no tail. The last statement is GoReturn / GoReassign / GoExprStmt
// as appropriate, encoded structurally by stage2Lower.
type GoBlock struct {
	Stmts []IRStmt
}

// --- Statements ---

// GoIfElse: if [Init;] Cond { Then } else { Else }
//
// Init is optional (nil if absent) and used for Go's `if v, err := f(); err != nil`
// idiom. When Else.Stmts is empty, emit omits the else branch.
type GoIfElse struct {
	Init IRStmt
	Cond IRExpr
	Then GoBlock
	Else GoBlock
	Pos  Pos
}

// GoSwitch: switch Subject { Cases default: Default }
//
// Default is nil when no default arm is needed. stage2Lower fills Default
// with a GoBlock containing GoUnreachable when the surrounding context
// demands every branch yield a value and the match has no wildcard arm.
type GoSwitch struct {
	Subject IRExpr
	Cases   []GoSwitchCase
	Default *GoBlock
	Pos     Pos
}

type GoSwitchCase struct {
	Vals []IRExpr
	Body GoBlock
}

// GoTypeSwitch: switch BindVar := Subject.(type) { ... }
type GoTypeSwitch struct {
	Subject IRExpr
	BindVar string
	Cases   []GoTypeCase
	Default *GoBlock
	Pos     Pos
}

type GoTypeCase struct {
	Type IRType
	Body GoBlock
}

// GoVarDecl: var Name Type [= Init]
//
// When Init is nil, emit produces `var Name Type` (zero-valued declaration).
// When Init is non-nil and Type is nil, emit produces `Name := Init`.
// When both are non-nil, emit produces `var Name Type = Init`.
type GoVarDecl struct {
	Name string
	Type IRType
	Init IRExpr
	Pos  Pos
}

// GoMultiAssign: Names := Value
//
// Used for multi-receive of a multi-return Go call. Types is parallel to
// Names and used when stage2Lower needs to predeclare with `var` and then
// reassign (e.g., when names are used after the call but the call sits
// inside an if-init).
type GoMultiAssign struct {
	Names []string
	Types []IRType
	Value IRExpr
	Pos   Pos
}

// GoReassign: Targets = Values
//
// Reassignment of already-declared variables. Targets and Values are
// parallel; len(Targets) == len(Values). Used for the leaf wrap when a
// control-flow expression assigns to a predeclared var.
type GoReassign struct {
	Targets []string
	Values  []IRExpr
	Pos     Pos
}

// GoReturn: return Values...
//
// Values has length 0 (bare return), 1 (single value), or 2+ (multi-return,
// e.g. (val, err) for Result-typed functions).
type GoReturn struct {
	Values []IRExpr
	Pos    Pos
}

// GoExprStmt: <expr>;
type GoExprStmt struct {
	Expr IRExpr
	Pos  Pos
}

// GoForRange: for [KeyVar, ]ValVar := range Iter { Body }
//
// KeyVar is "" when only the value is bound (`for v := range slice`).
type GoForRange struct {
	KeyVar string
	ValVar string
	Iter   IRExpr
	Body   GoBlock
	Pos    Pos
}

// GoForCStyle: for [Init]; [Cond]; [Post] { Body }
//
// Each of Init / Cond / Post may be nil for the `for` / `for ; cond ;` / etc.
// degenerate forms.
type GoForCStyle struct {
	Init IRStmt
	Cond IRExpr
	Post IRStmt
	Body GoBlock
	Pos  Pos
}

// GoUnreachable: panic("unreachable")
//
// Used as the body of a synthetic default arm when an exhaustive match
// is in value position (Go's return-analysis demands every branch yield
// a value, and the default is provably unreachable per Arca's
// exhaustiveness check).
type GoUnreachable struct {
	Pos Pos
}

// --- Expressions ---

// GoIIFE: func() RetType { Body }()
//
// Used for try-block lowering and any value-position construct that needs
// to be a Go expression. Body must end in GoReturn for non-void RetType.
type GoIIFE struct {
	RetType IRType
	Body    GoBlock
	Type    IRType
	Pos     Pos
}

// GoPtrOf: __ptrOf(Inner)
//
// Some-wrap helper call. Handles non-addressable values transparently
// (the helper takes its arg by value and returns its address).
type GoPtrOf struct {
	Inner IRExpr
	Type  IRType
	Pos   Pos
}

// GoOptFromCall: __optFrom(Call)
//
// Wraps a Go call returning (T, bool) into Arca's pointer-backed Option
// representation (*T). Call is the underlying multi-return Go expression.
type GoOptFromCall struct {
	Call IRExpr
	Type IRType
	Pos  Pos
}

// GoTypedNil: (*GoType)(nil)
//
// Typed-nil literal. Used when None must declare its element type at the
// emit site (e.g., `x := None` becomes `x := (*int)(nil)` so Go's type
// inference resolves).
type GoTypedNil struct {
	GoType string
	Type   IRType
	Pos    Pos
}

// GoErrorWrap: __goError{inner: Inner}
//
// Wraps a Go-level `error` value into the bridge struct so Arca trait
// methods (.message()) resolve. Used when a Result match's Err binding
// is the Arca Error trait (the underlying Go value is `error`-typed).
type GoErrorWrap struct {
	Inner IRExpr
	Type  IRType
	Pos   Pos
}

// GoDeref: *Inner   — Go pointer dereference. Used for Option binding
// when the inner type is not already pointer-backed (e.g. `n := *opt`
// inside the Some arm).
type GoDeref struct {
	Inner IRExpr
	Type  IRType
	Pos   Pos
}

// --- Interface implementations ---

func (GoIfElse) irStmtNode()      {}
func (GoSwitch) irStmtNode()      {}
func (GoTypeSwitch) irStmtNode()  {}
func (GoVarDecl) irStmtNode()     {}
func (GoMultiAssign) irStmtNode() {}
func (GoReassign) irStmtNode()    {}
func (GoReturn) irStmtNode()      {}
func (GoExprStmt) irStmtNode()    {}
func (GoForRange) irStmtNode()    {}
func (GoForCStyle) irStmtNode()   {}
func (GoUnreachable) irStmtNode() {}

func (e GoIIFE) irExprNode()           {}
func (e GoIIFE) irType() IRType        { return e.Type }
func (e GoPtrOf) irExprNode()          {}
func (e GoPtrOf) irType() IRType       { return e.Type }
func (e GoOptFromCall) irExprNode()    {}
func (e GoOptFromCall) irType() IRType { return e.Type }
func (e GoTypedNil) irExprNode()       {}
func (e GoTypedNil) irType() IRType    { return e.Type }
func (e GoErrorWrap) irExprNode()      {}
func (e GoErrorWrap) irType() IRType   { return e.Type }
func (e GoDeref) irExprNode()          {}
func (e GoDeref) irType() IRType       { return e.Type }
