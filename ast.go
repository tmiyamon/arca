package main

// --- Position ---

type Pos struct {
	Line int
	Col  int
}

// NodePos is embedded in all AST expression nodes to provide source position.
type NodePos struct {
	Pos Pos
}

func (n NodePos) exprPos() Pos { return n.Pos }

func At(line, col int) NodePos { return NodePos{Pos: Pos{Line: line, Col: col}} }
func AtPos(pos Pos) NodePos    { return NodePos{Pos: pos} }
func AtTok(tok Token) NodePos  { return NodePos{Pos: Pos{Line: tok.Line, Col: tok.Col}} }

// --- Types ---

type Type interface {
	typeNode()
}

type Constraint struct {
	Key   string // "min", "max", "min_length", "max_length", "pattern", "validate"
	Value Expr   // IntLit, FloatLit, StringLit, or Ident (for validate)
}

type NamedType struct {
	Pos         Pos
	Name        string
	Params      []Type
	Constraints []Constraint
}

type PointerType struct {
	Inner Type
}

type TupleType struct {
	Elements []Type
}

func (NamedType) typeNode()    {}
func (PointerType) typeNode()  {}
func (TupleType) typeNode()    {}

// --- Expressions ---

type Expr interface {
	exprNode()
	exprPos() Pos
}

type IntLit struct {
	NodePos
	Value int64
}
type FloatLit struct {
	NodePos
	Value float64
}
type StringLit struct {
	NodePos
	Value     string
	Multiline bool
}
type BoolLit struct {
	NodePos
	Value bool
}
type Ident struct {
	NodePos
	Name string
}

type StringInterp struct {
	NodePos
	Parts     []Expr // alternating StringLit and expressions
	Multiline bool
}

type FnCall struct {
	NodePos
	Fn       Expr
	Args     []Expr
	TypeArgs []Type // explicit type arguments: f[Int, String](args)
}

type FieldAccess struct {
	NodePos
	Expr  Expr
	Field string
}

type IndexAccess struct {
	NodePos
	Expr  Expr
	Index Expr
}

type IfExpr struct {
	NodePos
	Cond Expr
	Then Expr // block
	Else Expr // block or nil
}

type MatchExpr struct {
	NodePos
	Subject Expr
	Arms    []MatchArm
}

type MatchArm struct {
	Pos     Pos // pattern start position
	EndPos  Pos // body end position
	Pattern Pattern
	Body    Expr
}

type Block struct {
	NodePos
	EndPos Pos  // } の位置
	Stmts  []Stmt
	Expr   Expr // final expression (return value)
}

type ConstructorCall struct {
	NodePos
	TypeName string // "Greeting" in Greeting.Hello(...), empty for builtins (Ok/Error/Some/None)
	Name     string // "Hello" in Greeting.Hello(...), or "Ok" for builtins
	Fields   []FieldValue
}

type FieldValue struct {
	Name  string
	Value Expr
}

type Lambda struct {
	NodePos
	Params     []LambdaParam
	ReturnType Type // optional
	Body       Expr
}

type LambdaParam struct {
	Name string
	Type Type // optional, nil if not annotated
}

type TupleExpr struct {
	NodePos
	Elements []Expr
}

type ForExpr struct {
	NodePos
	Binding string
	Iter    Expr
	Body    Expr
}

type ListLit struct {
	NodePos
	Elements []Expr
	Spread   Expr // non-nil if [a, b, ..existing]
}

type MapLit struct {
	NodePos
	Entries []MapEntry
}

type MapEntry struct {
	Key   Expr
	Value Expr
}

type RangeExpr struct {
	NodePos
	Start Expr
	End   Expr
}

type BinaryExpr struct {
	NodePos
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
func (IndexAccess) exprNode()     {}
func (IfExpr) exprNode()          {}
func (MatchExpr) exprNode()       {}
func (Block) exprNode()           {}
func (ConstructorCall) exprNode() {}
func (Lambda) exprNode()          {}
func (TupleExpr) exprNode()       {}
func (ForExpr) exprNode()         {}
func (ListLit) exprNode()          {}
func (MapLit) exprNode()           {}
type RefExpr struct {
	NodePos
	Expr Expr
}

// TryBlockExpr represents `try { stmts... expr }`.
// Creates a Result context where ? can be used.
type TryBlockExpr struct {
	NodePos
	Body Block
}

func (RefExpr) exprNode()          {}
func (TryBlockExpr) exprNode()     {}
func (RangeExpr) exprNode()        {}
func (BinaryExpr) exprNode()       {}

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

type TuplePattern struct {
	Elements []Pattern
}

// TypePattern narrows an Any subject to a concrete type. `id: Int => body`
// binds id (with type Int) when the runtime dynamic type matches. Only
// valid against an Any/IRInterfaceType subject. Emits as Go type switch.
type TypePattern struct {
	Binding string // identifier bound to the narrowed value
	Target  Type   // the runtime type to match against
}

func (ConstructorPattern) patternNode() {}
func (WildcardPattern) patternNode()    {}
func (LitPattern) patternNode()         {}
func (BindPattern) patternNode()        {}
func (ListPattern) patternNode()        {}
func (TuplePattern) patternNode()       {}
func (TypePattern) patternNode()        {}

// --- Statements ---

type Stmt interface {
	stmtNode()
}

type LetStmt struct {
	Pos     Pos
	Name    string  // simple binding
	Pattern Pattern // nil for simple, non-nil for destructuring
	Type    Type    // optional
	Value   Expr
}

type ExprStmt struct {
	Expr Expr
}

type AssertStmt struct {
	Expr Expr
}

type DeferStmt struct {
	Expr Expr
}

func (LetStmt) stmtNode()   {}
func (ExprStmt) stmtNode()  {}
func (AssertStmt) stmtNode() {}
func (DeferStmt) stmtNode()  {}

// --- Top-level declarations ---

type Decl interface {
	declNode()
}

type ImportDecl struct {
	Pos        Pos
	Path       string   // e.g. "go/fmt", "user"
	SideEffect bool     // import go _ "pkg"
	Names      []string // selective: import user.{find, create} → ["find", "create"]
	Alias      string   // alias: import user as u → "u"
}

type TagRule struct {
	Name     string            // "json", "db", "elastic", etc.
	Case     string            // "snake", "camel", "kebab", "" (default: field name as-is)
	Overrides map[string]string // fieldName → tagValue (individual overrides)
}

type TypeDecl struct {
	Pos          Pos
	Name         string
	Params       []string
	Constructors []Constructor
	Methods      []FnDecl
	Tags         []TagRule
}

type TypeAliasDecl struct {
	Pos  Pos
	Name string
	Type Type
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
	Pos          Pos // position of 'fun' keyword (used for scope start)
	NamePos      Pos // position of function name (used for go-to-definition)
	Name         string
	Public       bool
	Static       bool   // static fun = associated function (no self)
	ReceiverType string // "" for free functions, "User" for fn User.method(self)
	Params       []FnParam
	ReturnType   Type // nil = no return type (void)
	Body         Expr // nil for trait method signatures
}

type TraitDecl struct {
	Pos     Pos
	NamePos Pos
	Name    string
	Methods []FnDecl // body-less (FnDecl.Body == nil)
}

type ImplDecl struct {
	Pos       Pos
	TypeName  string
	TraitName string
	Methods   []FnDecl
}

type FnParam struct {
	Pos  Pos
	Name string
	Type Type
}

func (ImportDecl) declNode()     {}
func (TypeDecl) declNode()       {}
func (TypeAliasDecl) declNode()  {}
func (FnDecl) declNode()         {}
func (TraitDecl) declNode()      {}
func (ImplDecl) declNode()       {}

// --- Program ---

type Program struct {
	Decls []Decl
}
