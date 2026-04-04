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
}

// NullTypeResolver returns nil for all queries — preserves current behavior
// where Go FFI types are not checked by Arca.
type NullTypeResolver struct{}

func (NullTypeResolver) ResolveFunc(pkg, name string) *FuncInfo              { return nil }
func (NullTypeResolver) ResolveType(pkg, name string) *TypeInfo              { return nil }
func (NullTypeResolver) ResolveMethod(pkg, typ, method string) *FuncInfo     { return nil }
