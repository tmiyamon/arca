package main

// --- Types ---

type Type interface {
	typeNode()
}

type NamedType struct {
	Name   string
	Params []Type
}

func (NamedType) typeNode() {}

// --- Expressions ---

type Expr interface {
	exprNode()
}

type IntLit struct{ Value int64 }
type FloatLit struct{ Value float64 }
type StringLit struct{ Value string }
type BoolLit struct{ Value bool }
type Ident struct{ Name string }

type FnCall struct {
	Fn   Expr
	Args []Expr
}

type FieldAccess struct {
	Expr  Expr
	Field string
}

type MatchExpr struct {
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
	Name   string
	Fields []FieldValue
}

type FieldValue struct {
	Name  string
	Value Expr
}

func (IntLit) exprNode()          {}
func (FloatLit) exprNode()        {}
func (StringLit) exprNode()       {}
func (BoolLit) exprNode()         {}
func (Ident) exprNode()           {}
func (FnCall) exprNode()          {}
func (FieldAccess) exprNode()     {}
func (MatchExpr) exprNode()       {}
func (Block) exprNode()           {}
func (ConstructorCall) exprNode() {}

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

func (ConstructorPattern) patternNode() {}
func (WildcardPattern) patternNode()    {}
func (LitPattern) patternNode()         {}
func (BindPattern) patternNode()        {}

// --- Statements ---

type Stmt interface {
	stmtNode()
}

type LetStmt struct {
	Name  string
	Type  Type // optional
	Value Expr
}

func (LetStmt) stmtNode() {}

// --- Top-level declarations ---

type Decl interface {
	declNode()
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
	Name       string
	Params     []FnParam
	ReturnType Type
	Body       Expr
}

type FnParam struct {
	Name string
	Type Type
}

func (TypeDecl) declNode() {}
func (FnDecl) declNode()   {}

// --- Program ---

type Program struct {
	Decls []Decl
}
