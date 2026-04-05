package main

import (
	"fmt"
	"strings"
)

// ValidateError represents a validation error with source position.
type ValidateError struct {
	Pos     Pos
	Message string
}

func (e ValidateError) Error() string {
	return fmt.Sprintf("%d:%d: %s", e.Pos.Line, e.Pos.Col, e.Message)
}

// IRValidation walks an IR program and validates types, reporting errors.
type IRValidation struct {
	types          map[string]TypeDecl
	typeAliases    map[string]TypeAliasDecl
	ctorTypes      map[string]string // constructor name → type name
	functions      map[string]FnDecl
	errors         []ValidateError
	typeParams     map[string]bool // currently in-scope type parameters
	allTypeParams  map[string]bool // all type parameter names across all types
}

// NewIRValidation creates a validator from lowerer's collected declarations.
func NewIRValidation(l *Lowerer) *IRValidation {
	types := l.Types()
	// Collect all type parameter names for type equality checks
	allTP := make(map[string]bool)
	for _, td := range types {
		for _, p := range td.Params {
			allTP[p] = true
		}
	}
	return &IRValidation{
		types:         types,
		typeAliases:   l.TypeAliases(),
		ctorTypes:     l.ctorTypes,
		functions:     l.Functions(),
		allTypeParams: allTP,
	}
}

// Validate walks the IR program and returns any validation errors.
func (v *IRValidation) Validate(prog IRProgram) []ValidateError {
	for _, td := range prog.Types {
		v.validateTypeDecl(td)
	}
	for _, fn := range prog.Funcs {
		v.validateFunc(fn)
	}
	return v.errors
}

func (v *IRValidation) addError(pos Pos, format string, args ...interface{}) {
	v.errors = append(v.errors, ValidateError{Pos: pos, Message: fmt.Sprintf(format, args...)})
}

// --- Type Declaration Validation ---

func (v *IRValidation) validateTypeDecl(td IRTypeDecl) {
	switch d := td.(type) {
	case IRStructDecl:
		// Check that field types are known
		name := d.GoName
		if astTD, ok := v.types[name]; ok {
			prev := v.typeParams
			v.typeParams = make(map[string]bool)
			for _, p := range astTD.Params {
				v.typeParams[p] = true
			}
			for _, ctor := range astTD.Constructors {
				for _, field := range ctor.Fields {
					v.checkTypeExists(field.Type)
				}
			}
			for _, method := range astTD.Methods {
				v.validateMethodTypes(method)
			}
			v.typeParams = prev
		}
	case IRSumTypeDecl:
		name := d.GoName
		if astTD, ok := v.types[name]; ok {
			prev := v.typeParams
			v.typeParams = make(map[string]bool)
			for _, p := range astTD.Params {
				v.typeParams[p] = true
			}
			for _, ctor := range astTD.Constructors {
				for _, field := range ctor.Fields {
					v.checkTypeExists(field.Type)
				}
			}
			for _, method := range astTD.Methods {
				v.validateMethodTypes(method)
			}
			v.typeParams = prev
		}
	case IREnumDecl:
		// Enum variants have no fields to check
	}
}

func (v *IRValidation) validateMethodTypes(fd FnDecl) {
	for _, param := range fd.Params {
		v.checkTypeExists(param.Type)
	}
	if fd.ReturnType != nil {
		v.checkTypeExists(fd.ReturnType)
	}
}

func (v *IRValidation) checkTypeExists(t Type) {
	switch tt := t.(type) {
	case NamedType:
		if !v.isKnownType(tt.Name) {
			v.addError(tt.Pos, "unknown type: %s", tt.Name)
		}
		for _, param := range tt.Params {
			v.checkTypeExists(param)
		}
	case PointerType:
		v.checkTypeExists(tt.Inner)
	case TupleType:
		for _, elem := range tt.Elements {
			v.checkTypeExists(elem)
		}
	}
}

var builtinTypes = map[string]bool{
	"Unit": true,
	"Int": true, "Float": true, "String": true, "Bool": true,
	"List": true, "Option": true, "Result": true,
	"error": true,
}

func (v *IRValidation) isKnownType(name string) bool {
	if builtinTypes[name] {
		return true
	}
	if v.typeParams != nil && v.typeParams[name] {
		return true
	}
	// Qualified types (Go FFI like http.Request) are always allowed
	if strings.Contains(name, ".") {
		return true
	}
	if _, ok := v.types[name]; ok {
		return true
	}
	_, ok := v.typeAliases[name]
	return ok
}

// --- Function Validation ---

func (v *IRValidation) validateFunc(fn IRFuncDecl) {
	// Check parameter types exist
	for _, param := range fn.Params {
		if param.Source.Type != nil {
			v.checkTypeExists(param.Source.Type)
		}
	}
	// Check return type exists
	if fn.Source.ReturnType != nil {
		v.checkTypeExists(fn.Source.ReturnType)
	}

	// Check return type mismatch
	if fn.Source.ReturnType != nil && fn.Body != nil {
		bodyType := v.inferExprType(fn.Body)
		if bodyType != nil && !v.typesEqualLocal(bodyType, fn.Source.ReturnType) {
			if !isResultReturn(fn.Source.ReturnType, bodyType) {
				v.addError(fn.Source.Pos, "function '%s' returns %s but body has type %s",
					fn.Source.Name, typeName(fn.Source.ReturnType), typeName(bodyType))
			}
		}
	}

	// Validate expressions in body
	if fn.Body != nil {
		v.validateExpr(fn.Body)
	}
}

// --- Expression Validation ---

func (v *IRValidation) validateExpr(expr IRExpr) {
	if expr == nil {
		return
	}
	switch e := expr.(type) {
	case IRFnCall:
		v.validateFnCall(e)
	case IRConstructorCall:
		v.validateConstructorCall(e)
	case IRBlock:
		for _, stmt := range e.Stmts {
			v.validateStmt(stmt)
		}
		v.validateExpr(e.Expr)
	case IRBinaryExpr:
		v.validateExpr(e.Left)
		v.validateExpr(e.Right)
	case IRMethodCall:
		v.validateExpr(e.Receiver)
		for _, arg := range e.Args {
			v.validateExpr(arg)
		}
	case IRLambda:
		v.validateExpr(e.Body)
	case IRForRange:
		v.validateExpr(e.Body)
	case IRForEach:
		v.validateExpr(e.Iter)
		v.validateExpr(e.Body)
	case IRMatch:
		v.validateExpr(e.Subject)
		for _, arm := range e.Arms {
			v.validateExpr(arm.Body)
		}
		v.validateMatchExhaustiveness(e)
	case IRStringInterp:
		for _, arg := range e.Args {
			v.validateExpr(arg)
		}
	case IRListLit:
		for _, elem := range e.Elements {
			v.validateExpr(elem)
		}
		if e.Spread != nil {
			v.validateExpr(e.Spread)
		}
	case IRTupleLit:
		for _, elem := range e.Elements {
			v.validateExpr(elem)
		}
	case IROkCall:
		v.validateExpr(e.Value)
	case IRErrorCall:
		v.validateExpr(e.Value)
	case IRSomeCall:
		v.validateExpr(e.Value)
	case IRRefExpr:
		v.validateExpr(e.Expr)
	case IRFieldAccess:
		v.validateExpr(e.Expr)
	}
}

func (v *IRValidation) validateStmt(stmt IRStmt) {
	switch s := stmt.(type) {
	case IRLetStmt:
		v.validateExpr(s.Value)
	case IRTryLetStmt:
		v.validateExpr(s.CallExpr)
	case IRExprStmt:
		v.validateExpr(s.Expr)
	case IRDeferStmt:
		v.validateExpr(s.Expr)
	case IRAssertStmt:
		v.validateExpr(s.Expr)
	case IRDestructureStmt:
		v.validateExpr(s.Value)
	}
}

// --- Function Call Validation ---

func (v *IRValidation) validateFnCall(e IRFnCall) {
	// Validate args recursively first
	for _, arg := range e.Args {
		v.validateExpr(arg)
	}

	// Only validate Arca function calls (not builtins, Go FFI, etc.)
	if e.Source.Name == "" {
		return
	}
	// Skip builtins
	if e.Source.Name == "__try" || e.Source.Name == "map" || e.Source.Name == "filter" || e.Source.Name == "fold" {
		return
	}
	// Skip Go FFI calls (contains dot)
	if strings.Contains(e.Source.Name, ".") {
		return
	}
	fn, ok := v.functions[e.Source.Name]
	if !ok {
		return
	}
	if len(e.Args) != len(fn.Params) {
		v.addError(e.Source.Pos, "function '%s' expects %d arguments, got %d",
			e.Source.Name, len(fn.Params), len(e.Args))
		return
	}
	for i, arg := range e.Args {
		argType := v.inferExprType(arg)
		if argType == nil {
			continue
		}
		paramType := fn.Params[i].Type
		if !v.typesCompatible(argType, paramType) {
			v.addError(e.Source.Pos, "argument %d of '%s' expects %s, got %s",
				i+1, e.Source.Name, typeName(paramType), typeName(argType))
		}
	}
}

// --- Constructor Call Validation ---

func (v *IRValidation) validateConstructorCall(cc IRConstructorCall) {
	// Validate field values recursively first
	for _, fv := range cc.Fields {
		v.validateExpr(fv.Value)
	}

	ctorName := cc.Source.Name
	if ctorName == "" {
		return
	}

	// Built-in Result/Option constructors
	if ctorName == "Ok" || ctorName == "Error" || ctorName == "Some" || ctorName == "None" {
		return
	}

	// Type alias constructor
	if _, ok := v.typeAliases[ctorName]; ok {
		return
	}

	tn := cc.Source.TypeName
	if tn == "" {
		tn2, ok := v.ctorTypes[ctorName]
		if !ok {
			v.addError(cc.Source.Pos, "unknown constructor: %s", ctorName)
			return
		}
		tn = tn2
	}

	td, ok := v.types[tn]
	if !ok {
		v.addError(cc.Source.Pos, "unknown type: %s", tn)
		return
	}

	var ctor Constructor
	found := false
	for _, ct := range td.Constructors {
		if ct.Name == ctorName {
			ctor = ct
			found = true
			break
		}
	}
	if !found {
		v.addError(cc.Source.Pos, "type %s has no constructor %s", tn, ctorName)
		return
	}

	if len(cc.Fields) != len(ctor.Fields) {
		v.addError(cc.Source.Pos, "constructor %s expects %d fields, got %d",
			ctorName, len(ctor.Fields), len(cc.Fields))
		return
	}

	for i, fv := range cc.Fields {
		if fv.Source.Name != "" {
			found := false
			for _, cf := range ctor.Fields {
				if cf.Name == fv.Source.Name {
					found = true
					argType := v.inferExprType(fv.Value)
					if argType != nil && !v.typesCompatible(argType, cf.Type) {
						v.addError(cc.Source.Pos, "field '%s' of %s expects %s, got %s",
							fv.Source.Name, ctorName, typeName(cf.Type), typeName(argType))
					}
					break
				}
			}
			if !found {
				v.addError(cc.Source.Pos, "constructor %s has no field named '%s'", ctorName, fv.Source.Name)
			}
		} else if i < len(ctor.Fields) {
			argType := v.inferExprType(fv.Value)
			if argType != nil && !v.typesCompatible(argType, ctor.Fields[i].Type) {
				v.addError(cc.Source.Pos, "field %d of %s expects %s, got %s",
					i+1, ctorName, typeName(ctor.Fields[i].Type), typeName(argType))
			}
		}
	}
}

// --- Type Inference from IR ---

// inferExprType infers the Arca-level type from an IR expression.
func (v *IRValidation) inferExprType(expr IRExpr) Type {
	if expr == nil {
		return nil
	}
	switch e := expr.(type) {
	case IRIntLit:
		return NamedType{Name: "Int"}
	case IRFloatLit:
		return NamedType{Name: "Float"}
	case IRStringLit:
		return NamedType{Name: "String"}
	case IRStringInterp:
		return NamedType{Name: "String"}
	case IRBoolLit:
		return NamedType{Name: "Bool"}
	case IRIdent:
		if e.Source.Type != nil {
			return e.Source.Type
		}
		return v.goTypeToArcaType(e.Type)
	case IRFnCall:
		// Type alias coercion call (generated by lowerer for arg coercion):
		// has no Source.Name, Func is a type alias name, single arg
		if e.Source.Name == "" && len(e.Args) == 1 {
			if _, isAlias := v.typeAliases[e.Func]; isAlias {
				return v.inferExprType(e.Args[0])
			}
		}
		// Look up Arca function return type
		if e.Source.Name != "" {
			if fn, ok := v.functions[e.Source.Name]; ok {
				return fn.ReturnType
			}
		}
		return v.goTypeToArcaType(e.Type)
	case IRConstructorCall:
		if e.Source.TypeName != "" {
			return NamedType{Name: e.Source.TypeName}
		}
		if e.Source.Name != "" {
			if tn, ok := v.ctorTypes[e.Source.Name]; ok {
				return NamedType{Name: tn}
			}
			if _, ok := v.typeAliases[e.Source.Name]; ok {
				return NamedType{Name: e.Source.Name}
			}
		}
		return nil
	case IRBlock:
		if e.Expr != nil {
			return v.inferExprType(e.Expr)
		}
		return nil
	case IRBinaryExpr:
		switch e.Op {
		case "==", "!=", "<", ">", "<=", ">=", "&&", "||":
			return NamedType{Name: "Bool"}
		default:
			return v.inferExprType(e.Left)
		}
	case IRListLit:
		if len(e.Elements) > 0 {
			elemType := v.inferExprType(e.Elements[0])
			if elemType != nil {
				return NamedType{Name: "List", Params: []Type{elemType}}
			}
		}
		return NamedType{Name: "List"}
	case IRTupleLit:
		elems := make([]Type, len(e.Elements))
		for i, el := range e.Elements {
			elems[i] = v.inferExprType(el)
		}
		return TupleType{Elements: elems}
	case IROkCall:
		return nil // Result type - handled by isResultReturn
	case IRErrorCall:
		return nil // Result type - handled by isResultReturn
	case IRSomeCall:
		return nil
	case IRNoneExpr:
		return nil
	case IRMatch:
		if len(e.Arms) > 0 {
			return v.inferExprType(e.Arms[0].Body)
		}
		return nil
	case IRFieldAccess:
		return nil // would need full type resolution
	case IRMethodCall:
		return nil
	case IRLambda:
		return nil
	default:
		return nil
	}
}

// goTypeToArcaType converts an IR type back to an Arca-level type.
func (v *IRValidation) goTypeToArcaType(t IRType) Type {
	if t == nil {
		return nil
	}
	switch tt := t.(type) {
	case IRNamedType:
		switch tt.GoName {
		case "int":
			return NamedType{Name: "Int"}
		case "float64":
			return NamedType{Name: "Float"}
		case "string":
			return NamedType{Name: "String"}
		case "bool":
			return NamedType{Name: "Bool"}
		default:
			// User-defined types keep their Go name (which is same as Arca name)
			return NamedType{Name: tt.GoName}
		}
	case IRListType:
		elem := v.goTypeToArcaType(tt.Elem)
		if elem != nil {
			return NamedType{Name: "List", Params: []Type{elem}}
		}
		return NamedType{Name: "List"}
	case IROptionType:
		inner := v.goTypeToArcaType(tt.Inner)
		if inner != nil {
			return NamedType{Name: "Option", Params: []Type{inner}}
		}
		return NamedType{Name: "Option"}
	case IRResultType:
		ok := v.goTypeToArcaType(tt.Ok)
		err := v.goTypeToArcaType(tt.Err)
		var params []Type
		if ok != nil {
			params = append(params, ok)
		}
		if err != nil {
			params = append(params, err)
		}
		return NamedType{Name: "Result", Params: params}
	case IRPointerType:
		inner := v.goTypeToArcaType(tt.Inner)
		if inner != nil {
			return PointerType{Inner: inner}
		}
		return nil
	case IRTupleType:
		elems := make([]Type, len(tt.Elements))
		for i, e := range tt.Elements {
			elems[i] = v.goTypeToArcaType(e)
		}
		return TupleType{Elements: elems}
	case IRInterfaceType:
		return nil // unknown type
	default:
		return nil
	}
}

// --- Type Compatibility ---

// isTypeParamLocal checks if a name is a type parameter using this validator's
// own type parameter set for thread safety in parallel tests.
func (v *IRValidation) isTypeParamLocal(name string) bool {
	return v.allTypeParams[name]
}

// typesEqualLocal is like typesEqual but uses the validator's local type params.
func (v *IRValidation) typesEqualLocal(a, b Type) bool {
	if a == nil || b == nil {
		return false
	}
	na, aOk := a.(NamedType)
	nb, bOk := b.(NamedType)
	if aOk && bOk {
		if v.isTypeParamLocal(na.Name) || v.isTypeParamLocal(nb.Name) {
			return true
		}
		if strings.Contains(na.Name, ".") || strings.Contains(nb.Name, ".") {
			return na.Name == nb.Name
		}
		if na.Name != nb.Name {
			return false
		}
		if na.Name == "List" && (len(na.Params) == 0 || len(nb.Params) == 0) {
			return true
		}
		if len(na.Params) != len(nb.Params) {
			return false
		}
		for i := range na.Params {
			if !v.typesEqualLocal(na.Params[i], nb.Params[i]) {
				return false
			}
		}
		return true
	}
	pa, aOk := a.(PointerType)
	pb, bOk := b.(PointerType)
	if aOk && bOk {
		return v.typesEqualLocal(pa.Inner, pb.Inner)
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
			if !v.typesEqualLocal(ta.Elements[i], tb.Elements[i]) {
				return false
			}
		}
		return true
	}
	return false
}

func (v *IRValidation) typesCompatible(source, target Type) bool {
	if v.typesEqualLocal(source, target) {
		return true
	}
	ns, sOk := source.(NamedType)
	nt, tOk := target.(NamedType)
	if !sOk || !tOk {
		return false
	}

	_, sIsAlias := v.typeAliases[ns.Name]
	_, tIsAlias := v.typeAliases[nt.Name]

	sBase, sConstraints := v.resolveAlias(ns.Name)
	tBase, tConstraints := v.resolveAlias(nt.Name)

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

func (v *IRValidation) resolveAlias(name string) (string, []Constraint) {
	if alias, ok := v.typeAliases[name]; ok {
		if nt, ok := alias.Type.(NamedType); ok {
			return nt.Name, nt.Constraints
		}
	}
	return name, nil
}

// --- Match Exhaustiveness ---

func (v *IRValidation) validateMatchExhaustiveness(m IRMatch) {
	if len(m.Arms) == 0 {
		return
	}
	switch m.Arms[0].Pattern.(type) {
	case IRResultOkPattern, IRResultErrorPattern:
		v.checkResultExhaustiveness(m)
	case IROptionSomePattern, IROptionNonePattern:
		v.checkOptionExhaustiveness(m)
	case IREnumPattern:
		v.checkEnumExhaustiveness(m)
	case IRSumTypePattern, IRSumTypeWildcardPattern:
		v.checkSumTypeExhaustiveness(m)
	}
	// List and Literal matches don't require exhaustiveness (have default/wildcard patterns)
}

func (v *IRValidation) checkResultExhaustiveness(m IRMatch) {
	hasOk, hasError := false, false
	for _, arm := range m.Arms {
		switch arm.Pattern.(type) {
		case IRResultOkPattern:
			hasOk = true
		case IRResultErrorPattern:
			hasError = true
		}
	}
	if !hasOk {
		v.addError(m.Pos, "non-exhaustive match: missing Ok arm")
	}
	if !hasError {
		v.addError(m.Pos, "non-exhaustive match: missing Error arm")
	}
}

func (v *IRValidation) checkOptionExhaustiveness(m IRMatch) {
	hasSome, hasNone := false, false
	for _, arm := range m.Arms {
		switch arm.Pattern.(type) {
		case IROptionSomePattern:
			hasSome = true
		case IROptionNonePattern:
			hasNone = true
		}
	}
	if !hasSome {
		v.addError(m.Pos, "non-exhaustive match: missing Some arm")
	}
	if !hasNone {
		v.addError(m.Pos, "non-exhaustive match: missing None arm")
	}
}

func (v *IRValidation) checkEnumExhaustiveness(m IRMatch) {
	hasWildcard := false
	matched := make(map[string]bool)
	for _, arm := range m.Arms {
		switch p := arm.Pattern.(type) {
		case IREnumPattern:
			matched[p.GoValue] = true
		case IRWildcardPattern:
			hasWildcard = true
		}
	}
	if hasWildcard {
		return
	}
	// Find the enum type from the first variant
	for _, td := range v.types {
		if !isEnum(td) {
			continue
		}
		for _, c := range td.Constructors {
			goValue := td.Name + c.Name
			if matched[goValue] {
				// This is the right enum type — check all variants
				for _, ctor := range td.Constructors {
					if !matched[td.Name+ctor.Name] {
						v.addError(m.Pos, "non-exhaustive match: missing %s variant", ctor.Name)
					}
				}
				return
			}
		}
	}
}

func (v *IRValidation) checkSumTypeExhaustiveness(m IRMatch) {
	hasWildcard := false
	matched := make(map[string]bool)
	for _, arm := range m.Arms {
		switch p := arm.Pattern.(type) {
		case IRSumTypePattern:
			matched[p.GoType] = true
		case IRSumTypeWildcardPattern:
			hasWildcard = true
		}
	}
	if hasWildcard {
		return
	}
	// Find the sum type from the first variant
	for _, td := range v.types {
		if isEnum(td) || len(td.Constructors) <= 1 {
			continue
		}
		for _, c := range td.Constructors {
			goType := td.Name + c.Name
			if matched[goType] {
				// This is the right sum type — check all variants
				for _, ctor := range td.Constructors {
					if !matched[td.Name+ctor.Name] {
						v.addError(m.Pos, "non-exhaustive match: missing %s variant", ctor.Name)
					}
				}
				return
			}
		}
	}
}
