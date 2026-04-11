package main

import (
	"fmt"
	"math"
	"strings"
)

// --- Compile Errors ---

type ErrorCode int

const (
	ErrUnspecified ErrorCode = iota // zero value — must not collide with a real code
	ErrTypeMismatch
	ErrUnknownType
	ErrUnknownVariable
	ErrNonExhaustiveMatch
	ErrWrongArgCount
	ErrWrongFieldCount
	ErrUnknownField
	ErrPackageNotFound
	ErrFieldAccessOnResult
	ErrFieldAccessOnOption
	ErrReturnTypeMismatch
	ErrCannotInferType
	ErrUnusedPackage
	ErrCannotInferTypeParam
)

type CompileError struct {
	Code  ErrorCode
	Pos   Pos
	Phase string      // "parse", "lower", "validate"
	Data  interface{} // error-specific structured data
}

func (e CompileError) Error() string {
	return fmt.Sprintf("%d:%d: %s", e.Pos.Line, e.Pos.Col, e.Message())
}

func (e CompileError) Message() string {
	switch d := e.Data.(type) {
	case TypeMismatchData:
		return fmt.Sprintf("type mismatch: expected %s, got %s", d.Expected, d.Actual)
	case UnknownVariableData:
		return fmt.Sprintf("undefined variable: %s", d.Name)
	case UnknownTypeData:
		return fmt.Sprintf("unknown type: %s", d.Name)
	case WrongArgCountData:
		if d.AtLeast {
			return fmt.Sprintf("'%s' expects at least %d arguments, got %d", d.Func, d.Expected, d.Actual)
		}
		return fmt.Sprintf("'%s' expects %d arguments, got %d", d.Func, d.Expected, d.Actual)
	case NonExhaustiveMatchData:
		return fmt.Sprintf("non-exhaustive match: missing %s", d.Missing)
	case PackageNotFoundData:
		return fmt.Sprintf("package %s not found. Run: go get %s", d.Path, d.Path)
	case FieldAccessOnWrappedData:
		return fmt.Sprintf("cannot access .%s on %s type. %s", d.Field, d.TypeName, d.Suggestion)
	case CannotInferTypeData:
		return fmt.Sprintf("cannot infer %s type for match subject", d.TypeName)
	case UnusedPackageData:
		return fmt.Sprintf("unused package: %s", d.Name)
	case CannotInferTypeParamData:
		if d.Binding != "" {
			return fmt.Sprintf("cannot infer type of %s — add explicit type args, e.g. %s[T](...)", d.Binding, d.Suggestion)
		}
		return fmt.Sprintf("cannot infer type parameter — add explicit type args, e.g. %s[T](...)", d.Suggestion)
	case MessageData:
		return d.Text
	default:
		return "unknown error"
	}
}

// Error data types
type TypeMismatchData struct {
	Expected string
	Actual   string
}

type UnknownVariableData struct {
	Name string
}

type UnknownTypeData struct {
	Name string
}

type WrongArgCountData struct {
	Func     string
	Expected int
	Actual   int
	AtLeast  bool
}

type NonExhaustiveMatchData struct {
	Missing string
}

type PackageNotFoundData struct {
	Path string
}

type FieldAccessOnWrappedData struct {
	Field      string
	TypeName   string
	Suggestion string
}

type CannotInferTypeData struct {
	TypeName string
}

type UnusedPackageData struct {
	Name string // short name as used in Arca source (e.g. "time", "stdlib")
}

// CannotInferTypeParamData describes a let binding whose inferred type still
// contains an unresolved HM type variable after lowering, typically from a
// generic call where no type argument could be derived from arguments, hint,
// or explicit type args.
type CannotInferTypeParamData struct {
	Binding    string // `let x = ...` → "x"
	Suggestion string // function name to show in the explicit-type-args hint
}

// MessageData is a fallback for errors not yet structured
type MessageData struct {
	Text string
}

// --- Constraint Dimensions ---

type Dimension interface {
	dimKey() string
	isCompatibleWith(other Dimension) bool
}

// Range dimension: Value(min..max), Length(min..max)
type RangeDim struct {
	Key string  // "Value" or "Length"
	Min float64 // -Inf if unbounded
	Max float64 // +Inf if unbounded
}

func (d RangeDim) dimKey() string { return d.Key }
func (d RangeDim) isCompatibleWith(other Dimension) bool {
	o, ok := other.(RangeDim)
	if !ok || o.Key != d.Key {
		return false
	}
	return d.Min >= o.Min && d.Max <= o.Max
}

// Exact dimension: Pattern("..."), Validate(funcName)
type ExactDim struct {
	Key   string // "Pattern" or "Validate"
	Value string
}

func (d ExactDim) dimKey() string { return d.Key }
func (d ExactDim) isCompatibleWith(other Dimension) bool {
	o, ok := other.(ExactDim)
	if !ok || o.Key != d.Key {
		return false
	}
	return d.Value == o.Value
}

// Convert constraints to dimensions
func constraintsToDimensions(constraints []Constraint) []Dimension {
	var dims []Dimension
	vMin := math.Inf(-1)
	vMax := math.Inf(1)
	hasValue := false
	lMin := 0.0
	lMax := math.Inf(1)
	hasLength := false

	for _, c := range constraints {
		switch c.Key {
		case "min":
			hasValue = true
			if v, ok := constToFloat(c.Value); ok {
				vMin = v
			}
		case "max":
			hasValue = true
			if v, ok := constToFloat(c.Value); ok {
				vMax = v
			}
		case "min_length":
			hasLength = true
			if v, ok := constToFloat(c.Value); ok {
				lMin = v
			}
		case "max_length":
			hasLength = true
			if v, ok := constToFloat(c.Value); ok {
				lMax = v
			}
		case "pattern":
			if lit, ok := c.Value.(StringLit); ok {
				dims = append(dims, ExactDim{Key: "Pattern", Value: lit.Value})
			}
		case "validate":
			if id, ok := c.Value.(Ident); ok {
				dims = append(dims, ExactDim{Key: "Validate", Value: id.Name})
			}
		}
	}
	if hasValue {
		dims = append(dims, RangeDim{Key: "Value", Min: vMin, Max: vMax})
	}
	if hasLength {
		dims = append(dims, RangeDim{Key: "Length", Min: lMin, Max: lMax})
	}
	return dims
}

func constToFloat(expr Expr) (float64, bool) {
	switch v := expr.(type) {
	case IntLit:
		return float64(v.Value), true
	case FloatLit:
		return v.Value, true
	}
	return 0, false
}

// Check if source type's constraints are compatible with target type's constraints.
// Compatible means: source is equal or stricter than target on all dimensions.
func dimensionsCompatible(source, target []Dimension) bool {
	for _, td := range target {
		found := false
		for _, sd := range source {
			if sd.dimKey() == td.dimKey() {
				if !sd.isCompatibleWith(td) {
					return false
				}
				found = true
				break
			}
		}
		if !found {
			// Source has no constraint on this dimension → unbounded → not compatible
			return false
		}
	}
	return true
}

// --- Symbol Info ---

// Symbol kinds
const (
	SymVariable  = "variable"
	SymParameter = "parameter"
	SymFunction  = "function"
	SymPackage   = "package"
)

// NewSymbolInfo creates a SymbolInfo with auto-resolved GoName.
// Panics if name is empty.
func NewSymbolInfo(name, kind string) SymbolInfo {
	if name == "" {
		panic("NewSymbolInfo: name must not be empty")
	}
	return SymbolInfo{
		Name:   name,
		GoName: goNameForKind(name, kind),
		Kind:   kind,
	}
}

// goNameForKind determines the Go name based on symbol kind.
func goNameForKind(name, kind string) string {
	switch kind {
	case SymPackage:
		return name // Go packages keep their name as-is
	default:
		return snakeToCamel(name)
	}
}

// SymbolInfo records type info for a symbol at a specific position.
type SymbolInfo struct {
	Name   string
	Type   Type   // AST type (for LSP hover, validation)
	IRType IRType // IR type (for Go FFI resolution)
	GoName string // resolved Go name
	Pos    Pos
	Kind   string
}

// Scope represents a lexical scope with a link to its parent.
type Scope struct {
	parent    *Scope
	symbols   map[string]*SymbolInfo
	declCount map[string]int // same-scope shadowing counter
	Children  []*Scope
	StartPos  Pos
	EndPos    Pos
}

func NewScope(parent *Scope) *Scope {
	s := &Scope{
		parent:    parent,
		symbols:   make(map[string]*SymbolInfo),
		declCount: make(map[string]int),
	}
	if parent != nil {
		parent.Children = append(parent.Children, s)
	}
	return s
}

func (s *Scope) Define(name string, sym *SymbolInfo) {
	s.symbols[name] = sym
}

func (s *Scope) Lookup(name string) *SymbolInfo {
	for scope := s; scope != nil; scope = scope.parent {
		if sym, ok := scope.symbols[name]; ok {
			return sym
		}
	}
	return nil
}

// FindScopeAt returns the innermost scope containing the given position.
func (s *Scope) FindScopeAt(pos Pos) *Scope {
	for _, child := range s.Children {
		if child.Contains(pos) {
			return child.FindScopeAt(pos)
		}
	}
	return s
}

// Contains checks if a position is within this scope's range.
func (s *Scope) Contains(pos Pos) bool {
	if s.StartPos.Line == 0 && s.EndPos.Line == 0 {
		return s.parent == nil // only root scope matches all positions
	}
	if pos.Line < s.StartPos.Line || pos.Line > s.EndPos.Line {
		return false
	}
	if pos.Line == s.StartPos.Line && pos.Col < s.StartPos.Col {
		return false
	}
	if pos.Line == s.EndPos.Line && pos.Col > s.EndPos.Col {
		return false
	}
	return true
}

// --- Type Comparison ---

func typeName(t Type) string {
	if t == nil {
		return "unknown"
	}
	switch tt := t.(type) {
	case NamedType:
		if len(tt.Params) > 0 {
			params := make([]string, len(tt.Params))
			for i, p := range tt.Params {
				params[i] = typeName(p)
			}
			return tt.Name + "[" + strings.Join(params, ", ") + "]"
		}
		return tt.Name
	case PointerType:
		return "*" + typeName(tt.Inner)
	case TupleType:
		elems := make([]string, len(tt.Elements))
		for i, e := range tt.Elements {
			elems[i] = typeName(e)
		}
		return "(" + strings.Join(elems, ", ") + ")"
	default:
		return "unknown"
	}
}

func isResultReturn(declared, actual Type) bool {
	dn, ok := declared.(NamedType)
	if !ok {
		return false
	}
	return dn.Name == "Result"
}
