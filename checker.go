package main

import (
	"fmt"
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

type CheckError struct {
	Pos     Pos
	Message string
}

func (e CheckError) Error() string {
	return fmt.Sprintf("%d:%d: %s", e.Pos.Line, e.Pos.Col, e.Message)
}

// --- Scope ---

type Scope struct {
	parent *Scope
	vars   map[string]Type
}

func NewScope(parent *Scope) *Scope {
	return &Scope{parent: parent, vars: make(map[string]Type)}
}

func (s *Scope) Define(name string, t Type) {
	s.vars[name] = t
}

func (s *Scope) Lookup(name string) (Type, bool) {
	if t, ok := s.vars[name]; ok {
		return t, true
	}
	if s.parent != nil {
		return s.parent.Lookup(name)
	}
	return nil, false
}

// --- Checker ---

type Checker struct {
	types           map[string]TypeDecl
	typeAliases     map[string]TypeAliasDecl
	ctorTypes       map[string]string // constructor name -> type name
	functions       map[string]FnDecl
	errors          []CheckError
	scope           *Scope
	currentFn       *FnDecl
	currentTypeName string // set inside type methods for Self resolution
	typeParams      map[string]bool // currently in-scope type parameters
}

func NewChecker() *Checker {
	return &Checker{
		types:       make(map[string]TypeDecl),
		typeAliases: make(map[string]TypeAliasDecl),
		ctorTypes:   make(map[string]string),
		functions:   make(map[string]FnDecl),
		scope:       NewScope(nil),
	}
}

func (c *Checker) Check(prog *Program) []CheckError {
	// Pass 1: collect declarations
	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case TypeDecl:
			c.types[d.Name] = d
			for _, ctor := range d.Constructors {
				c.ctorTypes[ctor.Name] = d.Name
			}
		case TypeAliasDecl:
			c.typeAliases[d.Name] = d
		case FnDecl:
			c.functions[d.Name] = d
		}
	}

	registerTypeParams(c.types)

	// Pass 2: check everything
	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case TypeDecl:
			c.checkTypeDecl(d)
		case FnDecl:
			c.checkFnDecl(d)
		}
	}

	return c.errors
}

func (c *Checker) addErrorAt(pos Pos, format string, args ...interface{}) {
	c.errors = append(c.errors, CheckError{Pos: pos, Message: fmt.Sprintf(format, args...)})
}

func (c *Checker) pushScope() {
	c.scope = NewScope(c.scope)
}

func (c *Checker) popScope() {
	c.scope = c.scope.parent
}

// --- Type Comparison ---

// allTypeParams collects all type parameter names from all type declarations.
// Used to check if a name is a type parameter in typesEqual.
var allTypeParamNames map[string]bool

func registerTypeParams(types map[string]TypeDecl) {
	allTypeParamNames = make(map[string]bool)
	for _, td := range types {
		for _, p := range td.Params {
			allTypeParamNames[p] = true
		}
	}
}

func isTypeParam(name string) bool {
	if allTypeParamNames == nil {
		return false
	}
	return allTypeParamNames[name]
}

func typesEqual(a, b Type) bool {
	if a == nil || b == nil {
		return false
	}
	na, aOk := a.(NamedType)
	nb, bOk := b.(NamedType)
	if aOk && bOk {
		// Type parameter matches anything
		if isTypeParam(na.Name) || isTypeParam(nb.Name) {
			return true
		}
		// Qualified types (Go FFI) match loosely
		if strings.Contains(na.Name, ".") || strings.Contains(nb.Name, ".") {
			return na.Name == nb.Name
		}
		if na.Name != nb.Name {
			return false
		}
		// Empty list (List with no params) matches any List[T]
		if na.Name == "List" && (len(na.Params) == 0 || len(nb.Params) == 0) {
			return true
		}
		if len(na.Params) != len(nb.Params) {
			return false
		}
		for i := range na.Params {
			if !typesEqual(na.Params[i], nb.Params[i]) {
				return false
			}
		}
		return true
	}
	pa, aOk := a.(PointerType)
	pb, bOk := b.(PointerType)
	if aOk && bOk {
		return typesEqual(pa.Inner, pb.Inner)
	}
	if aOk || bOk {
		return false
	}
	ta, aOk := a.(TupleType)
	tb, bOk := b.(TupleType)
	if aOk && bOk {
		if len(ta.Elements) != len(tb.Elements) {
			return false
		}
		for i := range ta.Elements {
			if !typesEqual(ta.Elements[i], tb.Elements[i]) {
				return false
			}
		}
		return true
	}
	return false
}

// typesCompatible checks if source can be passed where target is expected.
// This handles type alias constraint compatibility (e.g. AdultAge → Age).
func (c *Checker) typesCompatible(source, target Type) bool {
	if typesEqual(source, target) {
		return true
	}
	ns, sOk := source.(NamedType)
	nt, tOk := target.(NamedType)
	if !sOk || !tOk {
		return false
	}

	_, sIsAlias := c.typeAliases[ns.Name]
	_, tIsAlias := c.typeAliases[nt.Name]

	// Both are type aliases with different names → only compatible if
	// source constraints are stricter than target constraints (same base type)
	// But both must have constraints — two unconstrained aliases are never compatible
	sBase, sConstraints := c.resolveAlias(ns.Name)
	tBase, tConstraints := c.resolveAlias(nt.Name)

	if sBase != tBase {
		return false
	}

	// Two different type aliases with no constraints → nominal, not compatible
	if sIsAlias && tIsAlias && len(sConstraints) == 0 && len(tConstraints) == 0 {
		return false
	}

	sDims := constraintsToDimensions(sConstraints)
	tDims := constraintsToDimensions(tConstraints)

	return dimensionsCompatible(sDims, tDims)
}

// resolveAlias returns the base type name and constraints for a type alias.
// For non-alias types, returns the name itself with no constraints.
func (c *Checker) resolveAlias(name string) (string, []Constraint) {
	if alias, ok := c.typeAliases[name]; ok {
		if nt, ok := alias.Type.(NamedType); ok {
			return nt.Name, nt.Constraints
		}
	}
	return name, nil
}

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

// --- Type Inference ---

func (c *Checker) inferType(expr Expr) Type {
	if expr == nil {
		return nil
	}
	switch e := expr.(type) {
	case IntLit:
		return NamedType{Name: "Int"}
	case FloatLit:
		return NamedType{Name: "Float"}
	case StringLit:
		return NamedType{Name: "String"}
	case StringInterp:
		return NamedType{Name: "String"}
	case BoolLit:
		return NamedType{Name: "Bool"}
	case Ident:
		if t, ok := c.scope.Lookup(e.Name); ok {
			return t
		}
		// Check if it's an enum constructor
		if typeName, ok := c.ctorTypes[e.Name]; ok {
			return NamedType{Name: typeName}
		}
		return nil
	case ConstructorCall:
		if e.Name == "Ok" {
			if c.currentFn != nil && c.currentFn.ReturnType != nil {
				return c.currentFn.ReturnType
			}
		}
		if e.Name == "Error" {
			if c.currentFn != nil && c.currentFn.ReturnType != nil {
				return c.currentFn.ReturnType
			}
		}
		if e.TypeName != "" {
			tn := e.TypeName
			if tn == "Self" && c.currentTypeName != "" {
				tn = c.currentTypeName
			}
			return NamedType{Name: tn}
		}
		if typeName, ok := c.ctorTypes[e.Name]; ok {
			return NamedType{Name: typeName}
		}
		if _, ok := c.typeAliases[e.Name]; ok {
			return NamedType{Name: e.Name}
		}
		return nil
	case FieldAccess:
		recvType := c.inferType(e.Expr)
		if recvType == nil {
			return nil
		}
		nt, ok := recvType.(NamedType)
		if !ok {
			return nil
		}
		td, ok := c.types[nt.Name]
		if !ok {
			return nil
		}
		if len(td.Constructors) == 1 {
			for _, f := range td.Constructors[0].Fields {
				if f.Name == e.Field {
					return f.Type
				}
			}
		}
		return nil
	case BinaryExpr:
		switch e.Op {
		case "==", "!=", "<", ">", "<=", ">=", "&&", "||":
			return NamedType{Name: "Bool"}
		default:
			return c.inferType(e.Left)
		}
	case FnCall:
		if ident, ok := e.Fn.(Ident); ok {
			// __try(expr) unwraps Result → return inner type
			if ident.Name == "__try" && len(e.Args) > 0 {
				return c.inferType(e.Args[0])
			}
			if fn, ok := c.functions[ident.Name]; ok {
				return fn.ReturnType
			}
		}
		return nil
	case MatchExpr:
		if len(e.Arms) > 0 {
			return c.inferType(e.Arms[0].Body)
		}
		return nil
	case Block:
		if e.Expr != nil {
			return c.inferType(e.Expr)
		}
		return nil
	case ListLit:
		if len(e.Elements) > 0 {
			elemType := c.inferType(e.Elements[0])
			if elemType != nil {
				return NamedType{Name: "List", Params: []Type{elemType}}
			}
		}
		return NamedType{Name: "List"}
	case TupleExpr:
		elems := make([]Type, len(e.Elements))
		for i, el := range e.Elements {
			elems[i] = c.inferType(el)
		}
		return TupleType{Elements: elems}
	default:
		return nil
	}
}

// --- Type Declaration Checks ---

func (c *Checker) checkTypeDecl(td TypeDecl) {
	// Register type parameters during this check
	prev := c.typeParams
	c.typeParams = make(map[string]bool)
	for _, p := range td.Params {
		c.typeParams[p] = true
	}
	for _, ctor := range td.Constructors {
		for _, field := range ctor.Fields {
			c.checkTypeExists(field.Type)
		}
	}
	// Check methods
	for _, method := range td.Methods {
		c.currentTypeName = td.Name
		c.checkMethodDecl(td, method)
		c.currentTypeName = ""
	}
	c.typeParams = prev
}

func (c *Checker) checkMethodDecl(td TypeDecl, fd FnDecl) {
	for _, param := range fd.Params {
		c.checkTypeExists(param.Type)
	}
	if fd.ReturnType != nil {
		c.checkTypeExists(fd.ReturnType)
	}
	c.pushScope()
	c.currentFn = &fd
	// Register self as the type
	c.scope.Define("self", NamedType{Name: td.Name})
	for _, param := range fd.Params {
		c.scope.Define(param.Name, param.Type)
	}
	c.checkExpr(fd.Body)
	c.currentFn = nil
	c.popScope()
}

func (c *Checker) checkTypeExists(t Type) {
	switch tt := t.(type) {
	case NamedType:
		if !c.isKnownType(tt.Name) {
			c.addErrorAt(tt.Pos, "unknown type: %s", tt.Name)
		}
		for _, param := range tt.Params {
			c.checkTypeExists(param)
		}
	case PointerType:
		c.checkTypeExists(tt.Inner)
	case TupleType:
		for _, elem := range tt.Elements {
			c.checkTypeExists(elem)
		}
	}
}

func (c *Checker) isKnownType(name string) bool {
	builtins := map[string]bool{
		"Unit": true,
		"Int": true, "Float": true, "String": true, "Bool": true,
		"List": true, "Option": true, "Result": true,
		"error": true,
	}
	if builtins[name] {
		return true
	}
	if c.typeParams != nil && c.typeParams[name] {
		return true
	}
	// Qualified types (Go FFI like http.Request) are always allowed
	if strings.Contains(name, ".") {
		return true
	}
	if _, ok := c.types[name]; ok {
		return true
	}
	_, ok := c.typeAliases[name]
	return ok
}

// --- Function Declaration Checks ---

func (c *Checker) checkFnDecl(fd FnDecl) {
	// Check parameter types exist
	for _, param := range fd.Params {
		c.checkTypeExists(param.Type)
	}
	// Check return type exists
	if fd.ReturnType != nil {
		c.checkTypeExists(fd.ReturnType)
	}

	// Create scope with parameters
	c.pushScope()
	c.currentFn = &fd
	for _, param := range fd.Params {
		c.scope.Define(param.Name, param.Type)
	}

	// Check body and verify return type
	c.checkExpr(fd.Body)
	if fd.ReturnType != nil {
		bodyType := c.inferType(fd.Body)
		if bodyType != nil && !typesEqual(bodyType, fd.ReturnType) {
			// Don't report for Result types (Ok/Error handle this)
			if !isResultReturn(fd.ReturnType, bodyType) {
				c.addErrorAt(fd.Pos, "function '%s' returns %s but body has type %s",
					fd.Name, typeName(fd.ReturnType), typeName(bodyType))
			}
		}
	}

	c.currentFn = nil
	c.popScope()
}

func isResultReturn(declared, actual Type) bool {
	dn, ok := declared.(NamedType)
	if !ok {
		return false
	}
	if dn.Name == "Result" {
		return true
	}
	return false
}

// --- Expression Checks ---

func (c *Checker) checkExpr(expr Expr) {
	if expr == nil {
		return
	}
	switch e := expr.(type) {
	case ConstructorCall:
		c.checkConstructorCall(e)
	case MatchExpr:
		c.checkExpr(e.Subject)
		c.checkMatchExpr(e)
	case FnCall:
		c.checkFnCall(e)
	case FieldAccess:
		c.checkExpr(e.Expr)
	case Block:
		c.pushScope()
		for _, stmt := range e.Stmts {
			c.checkStmt(stmt)
		}
		c.checkExpr(e.Expr)
		c.popScope()
	case BinaryExpr:
		c.checkExpr(e.Left)
		c.checkExpr(e.Right)
	case Lambda:
		c.pushScope()
		for _, p := range e.Params {
			if p.Type != nil {
				c.scope.Define(p.Name, p.Type)
			}
		}
		c.checkExpr(e.Body)
		c.popScope()
	case ForExpr:
		c.checkExpr(e.Iter)
		c.pushScope()
		// Infer binding type from iterator
		iterType := c.inferType(e.Iter)
		if iterType != nil {
			if nt, ok := iterType.(NamedType); ok && nt.Name == "List" && len(nt.Params) > 0 {
				c.scope.Define(e.Binding, nt.Params[0])
			}
		}
		c.checkExpr(e.Body)
		c.popScope()
	case StringInterp:
		for _, part := range e.Parts {
			c.checkExpr(part)
		}
	case ListLit:
		for _, elem := range e.Elements {
			c.checkExpr(elem)
		}
	case TupleExpr:
		for _, elem := range e.Elements {
			c.checkExpr(elem)
		}
	}
}

func (c *Checker) checkStmt(stmt Stmt) {
	switch s := stmt.(type) {
	case LetStmt:
		c.checkExpr(s.Value)
		if s.Pattern != nil {
			// Destructuring
			valType := c.inferType(s.Value)
			c.bindPatternVars(s.Pattern, valType)
		} else if s.Type != nil {
			// Explicit type annotation: let name: Type = expr
			c.scope.Define(s.Name, s.Type)
		} else {
			// Simple binding — infer from value
			t := c.inferType(s.Value)
			if t != nil {
				c.scope.Define(s.Name, t)
			}
		}
	case DeferStmt:
		c.checkExpr(s.Expr)
	case AssertStmt:
		c.checkExpr(s.Expr)
	case ExprStmt:
		c.checkExpr(s.Expr)
	}
}

// --- Function Call Checks ---

func (c *Checker) checkFnCall(e FnCall) {
	c.checkExpr(e.Fn)
	for _, arg := range e.Args {
		c.checkExpr(arg)
	}

	// Check argument types against declared function parameters
	ident, ok := e.Fn.(Ident)
	if !ok {
		return
	}
	// Skip builtins
	if ident.Name == "__try" || ident.Name == "map" || ident.Name == "filter" || ident.Name == "fold" {
		return
	}
	// Skip Go FFI calls (contains dot)
	if strings.Contains(ident.Name, ".") {
		return
	}
	fn, ok := c.functions[ident.Name]
	if !ok {
		return
	}
	if len(e.Args) != len(fn.Params) {
		c.addErrorAt(e.Pos, "function '%s' expects %d arguments, got %d", ident.Name, len(fn.Params), len(e.Args))
		return
	}
	for i, arg := range e.Args {
		argType := c.inferType(arg)
		if argType == nil {
			continue
		}
		paramType := fn.Params[i].Type
		if !c.typesCompatible(argType, paramType) {
			c.addErrorAt(e.Pos, "argument %d of '%s' expects %s, got %s",
				i+1, ident.Name, typeName(paramType), typeName(argType))
		}
	}
}

// --- Constructor Call Checks ---

func (c *Checker) checkConstructorCall(cc ConstructorCall) {
	// Built-in Result constructors
	if cc.Name == "Ok" || cc.Name == "Error" || cc.Name == "Some" || cc.Name == "None" {
		for _, fv := range cc.Fields {
			c.checkExpr(fv.Value)
		}
		return
	}

	// Type alias constructor: Email("test@example.com")
	if _, ok := c.typeAliases[cc.Name]; ok {
		for _, fv := range cc.Fields {
			c.checkExpr(fv.Value)
		}
		return
	}

	var typeName string
	var ok bool
	qualifiedType := cc.TypeName
	if qualifiedType == "Self" && c.currentTypeName != "" {
		qualifiedType = c.currentTypeName
	}
	if qualifiedType != "" {
		// Qualified: Greeting.Hello(...) or Self.Hello(...)
		td, exists := c.types[qualifiedType]
		if !exists {
			c.addErrorAt(cc.Pos, "unknown type: %s", qualifiedType)
			return
		}
		found := false
		for _, ctor := range td.Constructors {
			if ctor.Name == cc.Name {
				found = true
				break
			}
		}
		if !found {
			c.addErrorAt(cc.Pos, "type %s has no constructor %s", qualifiedType, cc.Name)
			return
		}
		typeName = qualifiedType
		ok = true
	} else {
		typeName, ok = c.ctorTypes[cc.Name]
	}
	if !ok {
		c.addErrorAt(cc.Pos, "unknown constructor: %s", cc.Name)
		return
	}
	td := c.types[typeName]
	var ctor Constructor
	for _, ct := range td.Constructors {
		if ct.Name == cc.Name {
			ctor = ct
			break
		}
	}

	if len(cc.Fields) != len(ctor.Fields) {
		c.addErrorAt(cc.Pos, "constructor %s expects %d fields, got %d", cc.Name, len(ctor.Fields), len(cc.Fields))
		return
	}

	// Check named fields match and types
	for i, fv := range cc.Fields {
		if fv.Name != "" {
			found := false
			for _, cf := range ctor.Fields {
				if cf.Name == fv.Name {
					found = true
					// Check field type
					argType := c.inferType(fv.Value)
					if argType != nil && !c.typesCompatible(argType, cf.Type) {
						c.addErrorAt(cc.Pos, "field '%s' of %s expects %s, got %s",
							fv.Name, cc.Name, typeNameStr(cf.Type), typeNameStr(argType))
					}
					break
				}
			}
			if !found {
				c.addErrorAt(cc.Pos, "constructor %s has no field named '%s'", cc.Name, fv.Name)
			}
		} else if i < len(ctor.Fields) {
			argType := c.inferType(fv.Value)
			if argType != nil && !c.typesCompatible(argType, ctor.Fields[i].Type) {
				c.addErrorAt(cc.Pos, "field %d of %s expects %s, got %s",
					i+1, cc.Name, typeNameStr(ctor.Fields[i].Type), typeNameStr(argType))
			}
		}
		c.checkExpr(fv.Value)
	}
}

func typeNameStr(t Type) string {
	return typeName(t)
}

// --- Match Exhaustiveness ---

func (c *Checker) checkMatchExpr(me MatchExpr) {
	for _, arm := range me.Arms {
		// Bind pattern variables in arm scope
		c.pushScope()
		c.bindPatternVars(arm.Pattern, c.inferType(me.Subject))
		c.checkExpr(arm.Body)
		c.popScope()
	}

	// Find what type we're matching on by looking at patterns
	var matchedType string
	for _, arm := range me.Arms {
		if cp, ok := arm.Pattern.(ConstructorPattern); ok {
			if tn, ok := c.ctorTypes[cp.Name]; ok {
				matchedType = tn
				break
			}
		}
	}

	if matchedType == "" {
		return
	}

	td, ok := c.types[matchedType]
	if !ok {
		return
	}

	// Check if there's a wildcard or bind pattern
	for _, arm := range me.Arms {
		switch arm.Pattern.(type) {
		case WildcardPattern, BindPattern:
			return
		}
	}

	// Check all constructors are covered
	covered := make(map[string]bool)
	for _, arm := range me.Arms {
		if cp, ok := arm.Pattern.(ConstructorPattern); ok {
			covered[cp.Name] = true
		}
	}

	var missing []string
	for _, ctor := range td.Constructors {
		if !covered[ctor.Name] {
			missing = append(missing, ctor.Name)
		}
	}

	if len(missing) > 0 {
		c.addErrorAt(me.Pos, "non-exhaustive match on %s: missing %s", matchedType, strings.Join(missing, ", "))
	}
}

func (c *Checker) bindPatternVars(pat Pattern, subjectType Type) {
	switch p := pat.(type) {
	case ConstructorPattern:
		// Built-in Result/Option constructors
		if p.Name == "Ok" && len(p.Fields) > 0 {
			if subjectType != nil {
				if nt, ok := subjectType.(NamedType); ok && nt.Name == "Result" && len(nt.Params) > 0 {
					c.scope.Define(p.Fields[0].Binding, nt.Params[0])
				}
			}
			return
		}
		if p.Name == "Error" && len(p.Fields) > 0 {
			if subjectType != nil {
				if nt, ok := subjectType.(NamedType); ok && nt.Name == "Result" && len(nt.Params) > 1 {
					c.scope.Define(p.Fields[0].Binding, nt.Params[1])
				}
			}
			return
		}
		if p.Name == "Some" && len(p.Fields) > 0 {
			if subjectType != nil {
				if nt, ok := subjectType.(NamedType); ok && nt.Name == "Option" && len(nt.Params) > 0 {
					c.scope.Define(p.Fields[0].Binding, nt.Params[0])
				}
			}
			return
		}
		if p.Name == "None" {
			return
		}
		typeName, ok := c.ctorTypes[p.Name]
		if !ok {
			return
		}
		td := c.types[typeName]
		var ctor Constructor
		for _, ct := range td.Constructors {
			if ct.Name == p.Name {
				ctor = ct
				break
			}
		}
		for i, fp := range p.Fields {
			if i < len(ctor.Fields) {
				c.scope.Define(fp.Binding, ctor.Fields[i].Type)
			}
		}
	case BindPattern:
		if subjectType != nil {
			c.scope.Define(p.Name, subjectType)
		}
	case TuplePattern:
		if subjectType != nil {
			if tt, ok := subjectType.(TupleType); ok {
				for i, ep := range p.Elements {
					if bp, ok := ep.(BindPattern); ok && i < len(tt.Elements) {
						c.scope.Define(bp.Name, tt.Elements[i])
					}
				}
			}
		}
	case ListPattern:
		// Infer element type from subject
		if subjectType != nil {
			if nt, ok := subjectType.(NamedType); ok && nt.Name == "List" && len(nt.Params) > 0 {
				elemType := nt.Params[0]
				for _, ep := range p.Elements {
					if bp, ok := ep.(BindPattern); ok {
						c.scope.Define(bp.Name, elemType)
					}
				}
				if p.Rest != "" {
					c.scope.Define(p.Rest, subjectType)
				}
			}
		}
	}
}
