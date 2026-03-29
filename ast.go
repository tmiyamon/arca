package main

// --- Position ---

type Pos struct {
	Line int
	Col  int
}

// --- Types ---

type Type interface {
	typeNode()
}

type NamedType struct {
	Pos    Pos
	Name   string
	Params []Type
}

type TupleType struct {
	Elements []Type
}

func (NamedType) typeNode()  {}
func (TupleType) typeNode() {}

// --- Expressions ---

type Expr interface {
	exprNode()
}

type IntLit struct{ Value int64 }
type FloatLit struct{ Value float64 }
type StringLit struct{ Value string }
type BoolLit struct{ Value bool }
type Ident struct{ Name string }

type StringInterp struct {
	Parts []Expr // alternating StringLit and expressions
}

type FnCall struct {
	Fn   Expr
	Args []Expr
}

type FieldAccess struct {
	Expr  Expr
	Field string
}

type MatchExpr struct {
	Pos     Pos
	Subject Expr
	Arms    []MatchArm
}

type MatchArm struct {
	Pattern Pattern
	Body    Expr
}

type Block struct {
	Stmts []Stmt
	Expr  Expr // final expression (return value)
}

type ConstructorCall struct {
	Pos    Pos
	Name   string
	Fields []FieldValue
}

type FieldValue struct {
	Name  string
	Value Expr
}

type Lambda struct {
	Params     []LambdaParam
	ReturnType Type // optional
	Body       Expr
}

type LambdaParam struct {
	Name string
	Type Type // optional, nil if not annotated
}

type TupleExpr struct {
	Elements []Expr
}

type ForExpr struct {
	Binding string
	Iter    Expr
	Body    Expr
}

type ListLit struct {
	Elements []Expr
	Spread   Expr // non-nil if [a, b, ..existing]
}

type RangeExpr struct {
	Start Expr
	End   Expr
}

type BinaryExpr struct {
	Op    string
	Left  Expr
	Right Expr
}

func (IntLit) exprNode()          {}
func (FloatLit) exprNode()        {}
func (StringLit) exprNode()       {}
func (StringInterp) exprNode()    {}
func (BoolLit) exprNode()         {}
func (Ident) exprNode()           {}
func (FnCall) exprNode()          {}
func (FieldAccess) exprNode()     {}
func (MatchExpr) exprNode()       {}
func (Block) exprNode()           {}
func (ConstructorCall) exprNode() {}
func (Lambda) exprNode()          {}
func (TupleExpr) exprNode()       {}
func (ForExpr) exprNode()         {}
func (ListLit) exprNode()          {}
func (RangeExpr) exprNode()       {}
func (BinaryExpr) exprNode()      {}

// --- Patterns ---

type Pattern interface {
	patternNode()
}

type ConstructorPattern struct {
	Name   string
	Fields []FieldPattern
}

type FieldPattern struct {
	Name    string
	Binding string
}

type WildcardPattern struct{}
type LitPattern struct{ Expr Expr }
type BindPattern struct{ Name string }

type ListPattern struct {
	Elements []Pattern
	Rest     string // "" if no rest, e.g. [a, b, ..rest] → Rest = "rest"
}

func (ConstructorPattern) patternNode() {}
func (WildcardPattern) patternNode()    {}
func (LitPattern) patternNode()         {}
func (BindPattern) patternNode()        {}
func (ListPattern) patternNode()        {}

// --- Statements ---

type Stmt interface {
	stmtNode()
}

type LetStmt struct {
	Name  string
	Type  Type // optional
	Value Expr
}

type ExprStmt struct {
	Expr Expr
}

func (LetStmt) stmtNode()  {}
func (ExprStmt) stmtNode() {}

// --- Top-level declarations ---

type Decl interface {
	declNode()
}

type ImportDecl struct {
	Path string // e.g. "go/fmt", "user"
}

type TypeDecl struct {
	Name         string
	Params       []string
	Constructors []Constructor
}

type Constructor struct {
	Name   string
	Fields []Field
}

type Field struct {
	Name string
	Type Type
}

type FnDecl struct {
	Pos        Pos
	Name       string
	Public     bool
	Params     []FnParam
	ReturnType Type // nil = no return type (void)
	Body       Expr
}

type FnParam struct {
	Name string
	Type Type
}

func (ImportDecl) declNode() {}
func (TypeDecl) declNode()   {}
func (FnDecl) declNode()     {}

// --- Program ---

type Program struct {
	Decls []Decl
}
