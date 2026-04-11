package main

// TypeResolver abstracts the boundary between Arca's type world and Go's type world.
// The lowerer uses this interface to query Go package type information without
// depending on go/types directly.

type FuncInfo struct {
	Params    []ParamInfo
	Results   []ParamInfo
	Variadic  bool
}

type ParamInfo struct {
	Name string
	Type string // Go type string (e.g. "string", "int", "http.ResponseWriter")
}

type TypeInfo struct {
	Kind    TypeInfoKind
	Methods []string // method names
	Fields  []FieldInfo
}

type TypeInfoKind int

const (
	TypeInfoStruct TypeInfoKind = iota
	TypeInfoInterface
	TypeInfoBasic
	TypeInfoOther
)

type FieldInfo struct {
	Name string
	Type string
}

type TypeResolver interface {
	// ResolveFunc returns type info for a package-level function.
	// pkg is the Go import path (e.g. "fmt"), name is the function (e.g. "Println").
	// Returns nil if unknown.
	ResolveFunc(pkg, name string) *FuncInfo

	// ResolveType returns type info for a named type in a package.
	// Returns nil if unknown.
	ResolveType(pkg, name string) *TypeInfo

	// ResolveMethod returns type info for a method on a type.
	// typ is the full qualified type (e.g. "http.ResponseWriter"), method is the method name.
	// Returns nil if unknown.
	ResolveMethod(pkg, typ, method string) *FuncInfo

	// CanLoadPackage checks if a Go package can be loaded.
	// Returns true if the package is resolvable (in go.mod or stdlib).
	CanLoadPackage(pkg string) bool

	// ResolveUnderlying returns the underlying type string for a named type.
	// e.g. "github.com/labstack/echo/v5.HandlerFunc" → "func(c *echo.Context) error"
	// Returns "" if unknown.
	ResolveUnderlying(goType string) string

	// MemberPos returns the source file and position where a package-level
	// member (function, type, var, const) is defined. For go-to-definition.
	MemberPos(pkg, name string) (string, Pos)

	// MethodPos returns the source file and position where a method on a type
	// is defined. For go-to-definition on Go FFI method calls.
	MethodPos(pkg, typeName, method string) (string, Pos)

	// PackageMembers lists all exported package-level members (functions,
	// types, vars, consts) of a package. For LSP completion.
	PackageMembers(pkg string) []MemberInfo

	// TypeMembers lists all exported methods (and fields for structs) of a
	// named type in a package. For LSP completion.
	TypeMembers(pkg, typeName string) []MemberInfo
}

// MemberInfo describes an exported package-level or type member for completion.
type MemberInfo struct {
	Name   string
	Kind   string // "func", "type", "var", "const", "field", "method"
	Detail string // signature or type for display
}

// NullTypeResolver returns nil for all queries — preserves current behavior
// where Go FFI types are not checked by Arca.
type NullTypeResolver struct{}

func (NullTypeResolver) ResolveFunc(pkg, name string) *FuncInfo          { return nil }
func (NullTypeResolver) ResolveType(pkg, name string) *TypeInfo          { return nil }
func (NullTypeResolver) ResolveMethod(pkg, typ, method string) *FuncInfo { return nil }
func (NullTypeResolver) CanLoadPackage(pkg string) bool                  { return true }
func (NullTypeResolver) ResolveUnderlying(goType string) string          { return "" }
func (NullTypeResolver) MemberPos(pkg, name string) (string, Pos)        { return "", Pos{} }
func (NullTypeResolver) MethodPos(pkg, typ, method string) (string, Pos) { return "", Pos{} }
func (NullTypeResolver) PackageMembers(pkg string) []MemberInfo          { return nil }
func (NullTypeResolver) TypeMembers(pkg, typ string) []MemberInfo        { return nil }
