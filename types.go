package main

import (
	"math"
	"strings"
)

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
)

// SymbolInfo records type info for a symbol at a specific position.
type SymbolInfo struct {
	Name   string
	Type   Type   // AST type (for LSP hover, validation)
	IRType IRType // IR type (for Go FFI resolution)
	GoName string // resolved Go name
	Pos    Pos
	Kind   string
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
