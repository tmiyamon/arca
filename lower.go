package main

import (
	"fmt"
	"strings"
)

// Lowerer converts an AST Program into an IR Program.
// It resolves names, constructors, builtins, shadowing, and match kinds.
type Lowerer struct {
	types       map[string]TypeDecl
	typeAliases map[string]TypeAliasDecl
	ctorTypes   map[string]string // constructor name → type name
	fnNames     map[string]string // arca name → Go name for pub functions
	functions   map[string]FnDecl
	moduleNames map[string]bool
	goModule    string

	// Per-function state
	declaredVars    map[string]int
	varNames        map[string]string
	currentRetType  Type
	currentReceiver string
	currentTypeName string

	// Collected during lowering
	imports    []IRImport
	builtins   map[string]bool
	tmpCounter int
}

func NewLowerer(prog *Program, goModule string) *Lowerer {
	l := &Lowerer{
		types:       make(map[string]TypeDecl),
		typeAliases: make(map[string]TypeAliasDecl),
		ctorTypes:   make(map[string]string),
		fnNames:     make(map[string]string),
		functions:   make(map[string]FnDecl),
		moduleNames: make(map[string]bool),
		builtins:    make(map[string]bool),
		goModule:    goModule,
	}
	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case TypeDecl:
			l.types[d.Name] = d
			for _, ctor := range d.Constructors {
				l.ctorTypes[ctor.Name] = d.Name
			}
		case TypeAliasDecl:
			l.typeAliases[d.Name] = d
		case ImportDecl:
			if !strings.HasPrefix(d.Path, "go/") {
				parts := strings.Split(d.Path, ".")
				l.moduleNames[parts[len(parts)-1]] = true
			}
			if strings.HasPrefix(d.Path, "go/") {
				l.imports = append(l.imports, IRImport{
					Path:       d.Path[3:],
					SideEffect: d.SideEffect,
				})
			}
		case FnDecl:
			l.functions[d.Name] = d
			if d.Public {
				l.fnNames[d.Name] = snakeToPascal(d.Name)
			}
		}
	}
	return l
}

// Lower converts the entire program.
func (l *Lowerer) Lower(prog *Program) IRProgram {
	var types []IRTypeDecl
	var funcs []IRFuncDecl

	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case TypeDecl:
			types = append(types, l.lowerTypeDecl(d))
			for _, method := range d.Methods {
				funcs = append(funcs, l.lowerMethod(d, method))
			}
		case TypeAliasDecl:
			types = append(types, l.lowerTypeAliasDecl(d))
		case FnDecl:
			if d.ReceiverType == "" {
				funcs = append(funcs, l.lowerFnDecl(d))
			}
		}
	}

	// Collect builtin names
	var builtinNames []string
	for name := range l.builtins {
		builtinNames = append(builtinNames, name)
	}

	// Build final imports including auto-detected ones
	imports := make([]IRImport, len(l.imports))
	copy(imports, l.imports)
	if l.builtins["fmt"] && !l.hasImport("fmt") {
		imports = append(imports, IRImport{Path: "fmt"})
	}
	if l.builtins["regexp"] {
		imports = append(imports, IRImport{Path: "regexp"})
	}

	return IRProgram{
		Imports:  imports,
		Types:    types,
		Funcs:    funcs,
		Builtins: builtinNames,
	}
}

func (l *Lowerer) hasImport(pkg string) bool {
	for _, imp := range l.imports {
		if imp.Path == pkg {
			return true
		}
	}
	return false
}

// --- Type Declarations ---

func (l *Lowerer) lowerTypeDecl(td TypeDecl) IRTypeDecl {
	if isEnum(td) {
		return l.lowerEnumDecl(td)
	}
	if len(td.Constructors) == 1 {
		return l.lowerStructDecl(td)
	}
	return l.lowerSumTypeDecl(td)
}

func (l *Lowerer) lowerEnumDecl(td TypeDecl) IREnumDecl {
	variants := make([]string, len(td.Constructors))
	for i, c := range td.Constructors {
		variants[i] = c.Name
	}
	return IREnumDecl{
		GoName:   td.Name,
		Variants: variants,
	}
}

func (l *Lowerer) lowerStructDecl(td TypeDecl) IRStructDecl {
	ctor := td.Constructors[0]
	fields := make([]IRFieldDecl, len(ctor.Fields))
	for i, f := range ctor.Fields {
		tag := l.genStructTagFromRules(f.Name, td.Tags)
		fields[i] = IRFieldDecl{
			GoName: capitalize(f.Name),
			Type:   l.lowerType(f.Type),
			Tag:    tag,
		}
	}

	var validator *IRValidator
	if l.hasConstraints(td) {
		validator = l.buildStructValidator(td)
	}

	return IRStructDecl{
		GoName:     td.Name,
		TypeParams: td.Params,
		Fields:     fields,
		Tags:       td.Tags,
		Validator:  validator,
	}
}

func (l *Lowerer) lowerSumTypeDecl(td TypeDecl) IRSumTypeDecl {
	variants := make([]IRVariantDecl, len(td.Constructors))
	for i, c := range td.Constructors {
		fields := make([]IRFieldDecl, len(c.Fields))
		for j, f := range c.Fields {
			fields[j] = IRFieldDecl{
				GoName: capitalize(f.Name),
				Type:   l.lowerType(f.Type),
			}
		}
		variants[i] = IRVariantDecl{
			GoName: td.Name + c.Name,
			Fields: fields,
		}
	}
	return IRSumTypeDecl{
		GoName:     td.Name,
		TypeParams: td.Params,
		Variants:   variants,
	}
}

func (l *Lowerer) lowerTypeAliasDecl(d TypeAliasDecl) IRTypeAliasDecl {
	nt, ok := d.Type.(NamedType)
	if !ok {
		return IRTypeAliasDecl{GoName: d.Name, GoBase: "interface{}"}
	}
	goBase := l.goTypeStr(NamedType{Name: nt.Name, Params: nt.Params})

	var validator *IRValidator
	if len(nt.Constraints) > 0 {
		validator = l.buildAliasValidator(d.Name, goBase, nt.Constraints)
	}

	return IRTypeAliasDecl{
		GoName:    d.Name,
		GoBase:    goBase,
		Validator: validator,
	}
}

// --- Validators ---

func (l *Lowerer) hasConstraints(td TypeDecl) bool {
	if len(td.Constructors) != 1 {
		return false
	}
	for _, f := range td.Constructors[0].Fields {
		if nt, ok := f.Type.(NamedType); ok && len(nt.Constraints) > 0 {
			return true
		}
	}
	return false
}

func (l *Lowerer) buildStructValidator(td TypeDecl) *IRValidator {
	ctor := td.Constructors[0]
	var checks []IRValidationCheck
	for _, f := range ctor.Fields {
		nt, ok := f.Type.(NamedType)
		if !ok || len(nt.Constraints) == 0 {
			continue
		}
		fieldVar := snakeToCamel(f.Name)
		for _, c := range nt.Constraints {
			checks = append(checks, IRValidationCheck{
				Kind:     c.Key,
				Field:    fieldVar,
				Value:    l.constExprStr(c.Value),
				ZeroVal:  td.Name + "{}",
				TypeName: f.Name,
			})
		}
	}
	if len(checks) == 0 {
		return nil
	}
	l.builtins["fmt"] = true
	return &IRValidator{Checks: checks}
}

func (l *Lowerer) buildAliasValidator(typeName, goBase string, constraints []Constraint) *IRValidator {
	zeroVal := typeZeroValue(typeName, goBase)
	var checks []IRValidationCheck
	for _, c := range constraints {
		if c.Key == "pattern" {
			l.builtins["regexp"] = true
		}
		checks = append(checks, IRValidationCheck{
			Kind:     c.Key,
			Field:    "v",
			Value:    l.constExprStr(c.Value),
			ZeroVal:  zeroVal,
			TypeName: typeName,
		})
	}
	if len(checks) == 0 {
		return nil
	}
	l.builtins["fmt"] = true
	return &IRValidator{Checks: checks}
}

// constExprStr renders a constraint value expression as a Go string.
func (l *Lowerer) constExprStr(expr Expr) string {
	switch e := expr.(type) {
	case IntLit:
		return fmt.Sprintf("%d", e.Value)
	case FloatLit:
		return fmt.Sprintf("%g", e.Value)
	case StringLit:
		return fmt.Sprintf("%q", e.Value)
	case BoolLit:
		if e.Value {
			return "true"
		}
		return "false"
	case Ident:
		return e.Name
	default:
		return "/* unknown constraint value */"
	}
}

// --- Struct Tags ---

func (l *Lowerer) genStructTagFromRules(fieldName string, rules []TagRule) string {
	if len(rules) == 0 {
		return ""
	}
	var tags []string
	for _, rule := range rules {
		if val, ok := rule.Overrides[fieldName]; ok {
			tags = append(tags, fmt.Sprintf("%s:%q", rule.Name, val))
			continue
		}
		if rule.Case == "" && len(rule.Overrides) > 0 {
			continue
		}
		tagValue := fieldName
		switch rule.Case {
		case "snake":
			tagValue = camelToSnake(fieldName)
		case "kebab":
			tagValue = camelToKebab(fieldName)
		}
		tags = append(tags, fmt.Sprintf("%s:%q", rule.Name, tagValue))
	}
	if len(tags) == 0 {
		return ""
	}
	return "`" + strings.Join(tags, " ") + "`"
}

// --- Functions ---

func (l *Lowerer) lowerFnDecl(fd FnDecl) IRFuncDecl {
	name := fd.Name
	if fd.Public {
		name = snakeToPascal(name)
	}

	params := l.lowerParams(fd.Params)
	var retType IRType
	if fd.ReturnType != nil {
		retType = l.lowerType(fd.ReturnType)
	}

	l.currentRetType = fd.ReturnType
	l.initFnScope(fd.Params)
	body := l.lowerFnBody(fd.Body, fd.ReturnType != nil)
	l.currentRetType = nil
	l.declaredVars = nil
	l.varNames = nil

	return IRFuncDecl{
		GoName:     name,
		Params:     params,
		ReturnType: retType,
		Body:       body,
	}
}

func (l *Lowerer) lowerMethod(td TypeDecl, fd FnDecl) IRFuncDecl {
	if fd.Static {
		return l.lowerAssociatedFunc(td, fd)
	}

	methodName := snakeToCamel(fd.Name)
	if fd.Public {
		methodName = snakeToPascal(fd.Name)
	}

	params := l.lowerParams(fd.Params)
	var retType IRType
	if fd.ReturnType != nil {
		retType = l.lowerType(fd.ReturnType)
	}

	receiver := strings.ToLower(td.Name[:1])
	l.currentRetType = fd.ReturnType
	l.currentReceiver = receiver
	l.currentTypeName = td.Name
	l.initFnScope(fd.Params)
	body := l.lowerFnBody(fd.Body, fd.ReturnType != nil)
	l.currentReceiver = ""
	l.currentTypeName = ""
	l.currentRetType = nil
	l.declaredVars = nil
	l.varNames = nil

	return IRFuncDecl{
		GoName: methodName,
		Receiver: &IRReceiver{
			GoName: receiver,
			Type:   td.Name,
		},
		Params:     params,
		ReturnType: retType,
		Body:       body,
	}
}

func (l *Lowerer) lowerAssociatedFunc(td TypeDecl, fd FnDecl) IRFuncDecl {
	funcName := td.Name + capitalize(fd.Name)
	if !fd.Public {
		funcName = strings.ToLower(td.Name[:1]) + td.Name[1:] + capitalize(fd.Name)
	}

	params := l.lowerParams(fd.Params)
	var retType IRType
	if fd.ReturnType != nil {
		retType = l.lowerType(fd.ReturnType)
	}

	l.currentRetType = fd.ReturnType
	l.currentTypeName = td.Name
	l.initFnScope(fd.Params)
	body := l.lowerFnBody(fd.Body, fd.ReturnType != nil)
	l.currentTypeName = ""
	l.currentRetType = nil
	l.declaredVars = nil
	l.varNames = nil

	return IRFuncDecl{
		GoName:     funcName,
		Params:     params,
		ReturnType: retType,
		Body:       body,
	}
}

func (l *Lowerer) lowerParams(params []FnParam) []IRParamDecl {
	result := make([]IRParamDecl, len(params))
	for i, p := range params {
		result[i] = IRParamDecl{
			GoName: snakeToCamel(p.Name),
			Type:   l.lowerType(p.Type),
		}
	}
	return result
}

func (l *Lowerer) lowerFnBody(body Expr, hasReturn bool) IRExpr {
	return l.lowerExpr(body)
}

// --- Variable Scoping ---

func (l *Lowerer) declareVar(name string) string {
	goName := snakeToCamel(name)
	if l.declaredVars == nil {
		l.declaredVars = make(map[string]int)
	}
	count := l.declaredVars[goName]
	l.declaredVars[goName] = count + 1
	if count > 0 {
		goName = fmt.Sprintf("%s_%d", goName, count+1)
	}
	if l.varNames == nil {
		l.varNames = make(map[string]string)
	}
	l.varNames[snakeToCamel(name)] = goName
	return goName
}

func (l *Lowerer) initFnScope(params []FnParam) {
	l.declaredVars = make(map[string]int)
	l.varNames = make(map[string]string)
	for _, p := range params {
		goName := snakeToCamel(p.Name)
		l.declaredVars[goName] = 1
		l.varNames[goName] = goName
	}
}

func (l *Lowerer) resolveVar(name string) string {
	goName := snakeToCamel(name)
	if l.varNames != nil {
		if mapped, ok := l.varNames[goName]; ok {
			return mapped
		}
	}
	return goName
}

// --- Types ---

func (l *Lowerer) lowerType(t Type) IRType {
	if t == nil {
		return nil
	}
	switch tt := t.(type) {
	case NamedType:
		return l.lowerNamedType(tt)
	case PointerType:
		return IRPointerType{Inner: l.lowerType(tt.Inner)}
	case TupleType:
		elems := make([]IRType, len(tt.Elements))
		for i, e := range tt.Elements {
			elems[i] = l.lowerType(e)
		}
		return IRTupleType{Elements: elems}
	default:
		return IRInterfaceType{}
	}
}

func (l *Lowerer) lowerNamedType(nt NamedType) IRType {
	switch nt.Name {
	case "Unit":
		return IRNamedType{GoName: "struct{}"}
	case "Int":
		return IRNamedType{GoName: "int"}
	case "Float":
		return IRNamedType{GoName: "float64"}
	case "String":
		return IRNamedType{GoName: "string"}
	case "Bool":
		return IRNamedType{GoName: "bool"}
	case "List":
		if len(nt.Params) > 0 {
			return IRListType{Elem: l.lowerType(nt.Params[0])}
		}
		return IRListType{Elem: IRInterfaceType{}}
	case "Option":
		l.builtins["option"] = true
		if len(nt.Params) > 0 {
			return IROptionType{Inner: l.lowerType(nt.Params[0])}
		}
		return IRInterfaceType{}
	case "Result":
		l.builtins["result"] = true
		if len(nt.Params) >= 2 {
			return IRResultType{
				Ok:  l.lowerType(nt.Params[0]),
				Err: l.lowerType(nt.Params[1]),
			}
		}
		if len(nt.Params) == 1 {
			return IRResultType{
				Ok:  l.lowerType(nt.Params[0]),
				Err: IRNamedType{GoName: "error"},
			}
		}
		return IRResultType{
			Ok:  IRInterfaceType{},
			Err: IRNamedType{GoName: "error"},
		}
	case "Self":
		if l.currentTypeName != "" {
			return IRNamedType{GoName: l.currentTypeName}
		}
		return IRNamedType{GoName: "Self"}
	default:
		params := make([]IRType, len(nt.Params))
		for i, p := range nt.Params {
			params[i] = l.lowerType(p)
		}
		return IRNamedType{GoName: nt.Name, Params: params}
	}
}

// goTypeStr renders a Type as a Go type string. Used for validator zero values etc.
func (l *Lowerer) goTypeStr(t Type) string {
	switch tt := t.(type) {
	case NamedType:
		switch tt.Name {
		case "Unit":
			return "struct{}"
		case "Int":
			return "int"
		case "Float":
			return "float64"
		case "String":
			return "string"
		case "Bool":
			return "bool"
		case "List":
			if len(tt.Params) > 0 {
				return "[]" + l.goTypeStr(tt.Params[0])
			}
			return "[]interface{}"
		case "Option":
			l.builtins["option"] = true
			if len(tt.Params) > 0 {
				return "Option_[" + l.goTypeStr(tt.Params[0]) + "]"
			}
			return "interface{}"
		case "Result":
			l.builtins["result"] = true
			if len(tt.Params) >= 2 {
				return "Result_[" + l.goTypeStr(tt.Params[0]) + ", " + l.goTypeStr(tt.Params[1]) + "]"
			}
			if len(tt.Params) == 1 {
				return "Result_[" + l.goTypeStr(tt.Params[0]) + ", error]"
			}
			return "Result_[interface{}, error]"
		default:
			return tt.Name
		}
	case PointerType:
		return "*" + l.goTypeStr(tt.Inner)
	case TupleType:
		if len(tt.Elements) == 2 {
			return fmt.Sprintf("struct{ First %s; Second %s }", l.goTypeStr(tt.Elements[0]), l.goTypeStr(tt.Elements[1]))
		}
		return "interface{}"
	default:
		return "interface{}"
	}
}

// --- Expressions ---

func (l *Lowerer) lowerExpr(expr Expr) IRExpr {
	if expr == nil {
		return nil
	}
	switch e := expr.(type) {
	case IntLit:
		return IRIntLit{Value: e.Value, Type: IRNamedType{GoName: "int"}}
	case FloatLit:
		return IRFloatLit{Value: e.Value, Type: IRNamedType{GoName: "float64"}}
	case StringLit:
		return IRStringLit{Value: e.Value, Type: IRNamedType{GoName: "string"}}
	case BoolLit:
		return IRBoolLit{Value: e.Value, Type: IRNamedType{GoName: "bool"}}
	case Ident:
		return l.lowerIdent(e)
	case StringInterp:
		return l.lowerStringInterp(e)
	case FnCall:
		return l.lowerFnCall(e)
	case FieldAccess:
		return l.lowerFieldAccess(e)
	case ConstructorCall:
		return l.lowerConstructorCall(e)
	case Block:
		return l.lowerBlock(e)
	case MatchExpr:
		return l.lowerMatchExpr(e)
	case Lambda:
		return l.lowerLambda(e)
	case TupleExpr:
		return l.lowerTuple(e)
	case ForExpr:
		return l.lowerForExpr(e)
	case ListLit:
		return l.lowerListLit(e)
	case BinaryExpr:
		return l.lowerBinaryExpr(e)
	case RefExpr:
		inner := l.lowerExpr(e.Expr)
		return IRRefExpr{Expr: inner, Type: IRPointerType{Inner: inner.irType()}}
	case RangeExpr:
		// RangeExpr as standalone expression (not inside for) — shouldn't happen often
		return IRFnCall{
			Func: "__range",
			Args: []IRExpr{l.lowerExpr(e.Start), l.lowerExpr(e.End)},
			Type: IRListType{Elem: IRNamedType{GoName: "int"}},
		}
	default:
		return IRStringLit{Value: "/* unsupported expr */", Type: IRInterfaceType{}}
	}
}

func (l *Lowerer) lowerIdent(e Ident) IRExpr {
	// self → receiver variable
	if e.Name == "self" && l.currentReceiver != "" {
		return IRIdent{GoName: l.currentReceiver, Type: IRNamedType{GoName: l.currentTypeName}}
	}
	// Unit literal
	if e.Name == "Unit" {
		return IRIdent{GoName: "struct{}{}", Type: IRNamedType{GoName: "struct{}"}}
	}
	// None bare identifier
	if e.Name == "None" {
		l.builtins["option"] = true
		return IRNoneExpr{TypeArg: "[any]", Type: IROptionType{Inner: IRInterfaceType{}}}
	}
	// Enum variant bare reference: e.g. `Red` resolves to `ColorRed`
	if typeName := l.findTypeName(e.Name); typeName != "" {
		if td, ok := l.types[typeName]; ok && isEnum(td) {
			return IRIdent{
				GoName: typeName + e.Name,
				Type:   IRNamedType{GoName: typeName},
			}
		}
	}
	// Public function name resolution
	if goName, ok := l.fnNames[e.Name]; ok {
		return IRIdent{GoName: goName, Type: IRInterfaceType{}}
	}
	// Dotted names: Type.method or Go FFI like fmt.Println
	if strings.Contains(e.Name, ".") {
		parts := strings.SplitN(e.Name, ".", 2)
		if td, ok := l.types[parts[0]]; ok {
			for _, m := range td.Methods {
				if m.Name == parts[1] && m.Static {
					funcName := parts[0] + capitalize(parts[1])
					if !m.Public {
						funcName = strings.ToLower(parts[0][:1]) + parts[0][1:] + capitalize(parts[1])
					}
					return IRIdent{GoName: funcName, Type: IRInterfaceType{}}
				}
			}
		}
		return IRIdent{GoName: e.Name, Type: IRInterfaceType{}}
	}
	// Variable resolution with shadowing
	goName := l.resolveVar(e.Name)
	return IRIdent{GoName: goName, Type: IRInterfaceType{}}
}

func (l *Lowerer) lowerStringInterp(si StringInterp) IRExpr {
	l.builtins["fmt"] = true
	var fmtParts []string
	var args []IRExpr
	for _, part := range si.Parts {
		if lit, ok := part.(StringLit); ok {
			fmtParts = append(fmtParts, lit.Value)
		} else {
			fmtParts = append(fmtParts, "%v")
			args = append(args, l.lowerExpr(part))
		}
	}
	fmtStr := strings.Join(fmtParts, "")
	if len(args) == 0 {
		return IRStringLit{Value: fmtStr, Type: IRNamedType{GoName: "string"}}
	}
	return IRStringInterp{
		Format: fmtStr,
		Args:   args,
		Type:   IRNamedType{GoName: "string"},
	}
}

func (l *Lowerer) lowerFnCall(e FnCall) IRExpr {
	// Builtin functions
	if ident, ok := e.Fn.(Ident); ok {
		switch ident.Name {
		case "__try":
			// Try operator: this shouldn't appear as a standalone expression,
			// it is handled at the statement level in lowerStmt.
			// If it leaks here, lower the inner expression.
			if len(e.Args) == 1 {
				return l.lowerExpr(e.Args[0])
			}
		case "println":
			l.builtins["fmt"] = true
			args := l.lowerExprs(e.Args)
			return IRFnCall{Func: "fmt.Println", Args: args, Type: IRNamedType{GoName: "struct{}"}}
		case "print":
			l.builtins["fmt"] = true
			args := l.lowerExprs(e.Args)
			return IRFnCall{Func: "fmt.Print", Args: args, Type: IRNamedType{GoName: "struct{}"}}
		case "to_bytes":
			if len(e.Args) == 1 {
				// []byte(expr) — modeled as a function call
				return IRFnCall{
					Func: "[]byte",
					Args: []IRExpr{l.lowerExpr(e.Args[0])},
					Type: IRListType{Elem: IRNamedType{GoName: "byte"}},
				}
			}
		case "map":
			l.builtins["map"] = true
			args := l.lowerExprs(e.Args)
			return IRFnCall{Func: "Map_", Args: args, Type: IRInterfaceType{}}
		case "filter":
			l.builtins["filter"] = true
			args := l.lowerExprs(e.Args)
			return IRFnCall{Func: "Filter_", Args: args, Type: IRInterfaceType{}}
		case "fold":
			l.builtins["fold"] = true
			args := l.lowerExprs(e.Args)
			return IRFnCall{Func: "Fold_", Args: args, Type: IRInterfaceType{}}
		}
	}

	// Module-qualified call: module.fn(args)
	if fa, ok := e.Fn.(FieldAccess); ok {
		if ident, ok := fa.Expr.(Ident); ok && l.moduleNames[ident.Name] {
			fnName := fa.Field
			if goName, ok := l.fnNames[fnName]; ok {
				fnName = goName
			}
			args := l.lowerCallArgs(e)
			if l.goModule != "" {
				return IRFnCall{
					Func: ident.Name + "." + fnName,
					Args: args,
					Type: IRInterfaceType{},
				}
			}
			return IRFnCall{Func: fnName, Args: args, Type: IRInterfaceType{}}
		}
		// Regular method call: obj.method(args)
		methodName := l.resolveMethodName(fa.Field)
		args := l.lowerCallArgs(e)
		return IRMethodCall{
			Receiver: l.lowerExpr(fa.Expr),
			Method:   methodName,
			Args:     args,
			Type:     IRInterfaceType{},
		}
	}

	args := l.lowerCallArgs(e)
	fnExpr := l.lowerExpr(e.Fn)
	if ident, ok := fnExpr.(IRIdent); ok {
		return IRFnCall{Func: ident.GoName, Args: args, Type: IRInterfaceType{}}
	}
	// Lambda call or other complex expression
	return IRFnCall{Func: "/* complex call */", Args: args, Type: IRInterfaceType{}}
}

// lowerCallArgs lowers function call arguments with context-aware type coercion.
func (l *Lowerer) lowerCallArgs(e FnCall) []IRExpr {
	args := make([]IRExpr, len(e.Args))
	for i, a := range e.Args {
		args[i] = l.lowerArgWithContext(a, e, i)
	}
	return args
}

// lowerArgWithContext handles type alias coercion and empty list/None resolution.
func (l *Lowerer) lowerArgWithContext(expr Expr, call FnCall, argIndex int) IRExpr {
	fnIdent, isFnIdent := call.Fn.(Ident)

	// Empty list with resolvable element type
	if ll, ok := expr.(ListLit); ok && len(ll.Elements) == 0 && ll.Spread == nil {
		if isFnIdent {
			if fn, ok := l.functions[fnIdent.Name]; ok && argIndex < len(fn.Params) {
				if nt, ok := fn.Params[argIndex].Type.(NamedType); ok && nt.Name == "List" && len(nt.Params) > 0 {
					elemTypeStr := l.goTypeStr(nt.Params[0])
					return IRListLit{
						ElemType: elemTypeStr,
						Type:     IRListType{Elem: l.lowerType(nt.Params[0])},
					}
				}
			}
		}
	}

	// None with resolvable inner type
	if ident, ok := expr.(Ident); ok && ident.Name == "None" {
		if isFnIdent {
			if fn, ok := l.functions[fnIdent.Name]; ok && argIndex < len(fn.Params) {
				if nt, ok := fn.Params[argIndex].Type.(NamedType); ok && nt.Name == "Option" && len(nt.Params) > 0 {
					l.builtins["option"] = true
					innerTypeStr := l.goTypeStr(nt.Params[0])
					return IRNoneExpr{
						TypeArg: "[" + innerTypeStr + "]",
						Type:    IROptionType{Inner: l.lowerType(nt.Params[0])},
					}
				}
			}
		}
	}

	// Type alias parameter coercion
	if isFnIdent {
		if fn, ok := l.functions[fnIdent.Name]; ok && argIndex < len(fn.Params) {
			if pnt, ok := fn.Params[argIndex].Type.(NamedType); ok {
				if _, isAlias := l.typeAliases[pnt.Name]; isAlias {
					inner := l.lowerExpr(expr)
					// Wrap in a type conversion call
					return IRFnCall{
						Func: pnt.Name,
						Args: []IRExpr{inner},
						Type: IRNamedType{GoName: pnt.Name},
					}
				}
			}
		}
	}

	return l.lowerExpr(expr)
}

func (l *Lowerer) lowerFieldAccess(e FieldAccess) IRExpr {
	return IRFieldAccess{
		Expr:  l.lowerExpr(e.Expr),
		Field: capitalize(e.Field),
		Type:  IRInterfaceType{},
	}
}

func (l *Lowerer) lowerConstructorCall(e ConstructorCall) IRExpr {
	// Built-in Result constructors
	if e.Name == "Ok" && len(e.Fields) == 1 {
		l.builtins["result"] = true
		val := l.lowerExpr(e.Fields[0].Value)
		typeArgs := l.resultTypeArgs()
		return IROkCall{Value: val, TypeArgs: typeArgs, Type: IRInterfaceType{}}
	}
	if e.Name == "Error" && len(e.Fields) == 1 {
		l.builtins["result"] = true
		val := l.lowerExpr(e.Fields[0].Value)
		typeArgs := l.resultTypeArgs()
		return IRErrorCall{Value: val, TypeArgs: typeArgs, Type: IRInterfaceType{}}
	}
	// Built-in Option constructors
	if e.Name == "Some" && len(e.Fields) == 1 {
		l.builtins["option"] = true
		val := l.lowerExpr(e.Fields[0].Value)
		return IRSomeCall{Value: val, Type: IRInterfaceType{}}
	}
	if e.Name == "None" {
		l.builtins["option"] = true
		return IRNoneExpr{TypeArg: "[any]", Type: IROptionType{Inner: IRInterfaceType{}}}
	}

	return l.lowerUserConstructorCall(e)
}

func (l *Lowerer) lowerUserConstructorCall(cc ConstructorCall) IRExpr {
	typeName := cc.TypeName
	if typeName == "Self" && l.currentTypeName != "" {
		typeName = l.currentTypeName
	}

	var td TypeDecl
	var found bool

	if typeName != "" {
		td, found = l.types[typeName]
	} else {
		for tn, t := range l.types {
			for _, ctor := range t.Constructors {
				if ctor.Name == cc.Name {
					typeName = tn
					td = t
					found = true
					break
				}
			}
			if found {
				break
			}
		}
	}

	if found {
		// Enum variant
		if isEnum(td) {
			return IRIdent{
				GoName: typeName + cc.Name,
				Type:   IRNamedType{GoName: typeName},
			}
		}

		goName := typeName
		if len(td.Constructors) > 1 {
			goName = typeName + cc.Name
		}

		// Constrained type constructor
		if l.hasConstraints(td) {
			l.builtins["fmt"] = true
			fields := l.lowerFieldValues(cc.Fields)
			return IRConstructorCall{
				GoName:        "New" + goName,
				Fields:        fields,
				ReturnsResult: true,
				Type:          IRNamedType{GoName: goName},
			}
		}

		fields := l.lowerFieldValues(cc.Fields)
		typeArgs := ""
		if len(td.Params) > 0 {
			inferredArgs := make([]string, len(cc.Fields))
			for i, f := range cc.Fields {
				inferredArgs[i] = l.inferGoType(f.Value)
			}
			typeArgs = "[" + strings.Join(inferredArgs, ", ") + "]"
		}
		return IRConstructorCall{
			GoName:   goName,
			Fields:   fields,
			TypeArgs: typeArgs,
			Type:     IRNamedType{GoName: typeName},
		}
	}

	// Type alias constructor
	if alias, ok := l.typeAliases[cc.Name]; ok {
		fields := l.lowerFieldValues(cc.Fields)
		if nt, ok := alias.Type.(NamedType); ok && len(nt.Constraints) > 0 {
			l.builtins["fmt"] = true
			return IRConstructorCall{
				GoName:        "New" + cc.Name,
				Fields:        fields,
				ReturnsResult: true,
				Type:          IRNamedType{GoName: cc.Name},
			}
		}
		// Unconstrained alias: simple type conversion
		return IRConstructorCall{
			GoName: cc.Name,
			Fields: fields,
			Type:   IRNamedType{GoName: cc.Name},
		}
	}

	// Unknown constructor
	return IRConstructorCall{
		GoName: cc.Name,
		Fields: l.lowerFieldValues(cc.Fields),
		Type:   IRInterfaceType{},
	}
}

func (l *Lowerer) lowerFieldValues(fields []FieldValue) []IRFieldValue {
	result := make([]IRFieldValue, len(fields))
	for i, f := range fields {
		goName := ""
		if f.Name != "" {
			goName = capitalize(f.Name)
		}
		result[i] = IRFieldValue{
			GoName: goName,
			Value:  l.lowerExpr(f.Value),
		}
	}
	return result
}

func (l *Lowerer) lowerBlock(b Block) IRExpr {
	stmts := make([]IRStmt, 0, len(b.Stmts))
	for _, s := range b.Stmts {
		stmts = append(stmts, l.lowerStmt(s)...)
	}
	var expr IRExpr
	if b.Expr != nil {
		expr = l.lowerExpr(b.Expr)
	}
	return IRBlock{
		Stmts: stmts,
		Expr:  expr,
		Type:  IRInterfaceType{},
	}
}

func (l *Lowerer) lowerLambda(lam Lambda) IRExpr {
	params := make([]IRParamDecl, len(lam.Params))
	for i, p := range lam.Params {
		var typ IRType
		if p.Type != nil {
			typ = l.lowerType(p.Type)
		}
		params[i] = IRParamDecl{GoName: p.Name, Type: typ}
	}
	var retType IRType
	if lam.ReturnType != nil {
		retType = l.lowerType(lam.ReturnType)
	}
	body := l.lowerExpr(lam.Body)
	return IRLambda{
		Params:     params,
		ReturnType: retType,
		Body:       body,
		Type:       IRInterfaceType{},
	}
}

func (l *Lowerer) lowerTuple(t TupleExpr) IRExpr {
	elems := make([]IRExpr, len(t.Elements))
	for i, e := range t.Elements {
		elems[i] = l.lowerExpr(e)
	}
	return IRTupleLit{Elements: elems, Type: IRInterfaceType{}}
}

func (l *Lowerer) lowerForExpr(fe ForExpr) IRExpr {
	binding := snakeToCamel(fe.Binding)
	body := l.lowerExpr(fe.Body)

	if rangeExpr, ok := fe.Iter.(RangeExpr); ok {
		return IRForRange{
			Binding: binding,
			Start:   l.lowerExpr(rangeExpr.Start),
			End:     l.lowerExpr(rangeExpr.End),
			Body:    body,
			Type:    IRNamedType{GoName: "struct{}"},
		}
	}

	return IRForEach{
		Binding: binding,
		Iter:    l.lowerExpr(fe.Iter),
		Body:    body,
		Type:    IRNamedType{GoName: "struct{}"},
	}
}

func (l *Lowerer) lowerListLit(ll ListLit) IRExpr {
	if len(ll.Elements) == 0 && ll.Spread == nil {
		return IRListLit{
			ElemType: "interface{}",
			Type:     IRListType{Elem: IRInterfaceType{}},
		}
	}

	elems := make([]IRExpr, len(ll.Elements))
	for i, e := range ll.Elements {
		elems[i] = l.lowerExpr(e)
	}

	elemType := "interface{}"
	if len(ll.Elements) > 0 {
		elemType = l.inferGoElemType(ll.Elements[0])
	}

	var spread IRExpr
	if ll.Spread != nil {
		spread = l.lowerExpr(ll.Spread)
	}

	return IRListLit{
		ElemType: elemType,
		Elements: elems,
		Spread:   spread,
		Type:     IRListType{Elem: IRNamedType{GoName: elemType}},
	}
}

func (l *Lowerer) lowerBinaryExpr(be BinaryExpr) IRExpr {
	left := l.lowerExpr(be.Left)
	right := l.lowerExpr(be.Right)
	return IRBinaryExpr{
		Op:    be.Op,
		Left:  left,
		Right: right,
		Type:  IRInterfaceType{},
	}
}

// --- Statements ---

func (l *Lowerer) lowerStmt(stmt Stmt) []IRStmt {
	switch s := stmt.(type) {
	case LetStmt:
		return l.lowerLetStmt(s)
	case DeferStmt:
		return []IRStmt{IRDeferStmt{Expr: l.lowerExpr(s.Expr)}}
	case AssertStmt:
		irExpr := l.lowerExpr(s.Expr)
		// Reconstruct expression string for panic message
		exprStr := l.exprToString(s.Expr)
		return []IRStmt{IRAssertStmt{Expr: irExpr, ExprStr: exprStr}}
	case ExprStmt:
		return []IRStmt{IRExprStmt{Expr: l.lowerExpr(s.Expr)}}
	default:
		return nil
	}
}

func (l *Lowerer) lowerLetStmt(s LetStmt) []IRStmt {
	// Destructuring: let [first, ..rest] = expr or let (a, b) = expr
	if s.Pattern != nil {
		return l.lowerLetDestructure(s.Pattern, s.Value)
	}

	// Try operator: let x = expr?
	if call, ok := s.Value.(FnCall); ok {
		if ident, ok := call.Fn.(Ident); ok && ident.Name == "__try" && len(call.Args) == 1 {
			// Lower value BEFORE declaring variable (shadowing must not affect the RHS)
			loweredExpr := l.lowerExpr(call.Args[0])
			goVarName := "_"
			if s.Name != "_" {
				goVarName = l.declareVar(s.Name)
			}
			var retType IRType
			if l.currentRetType != nil {
				retType = l.lowerType(l.currentRetType)
			}
			return []IRStmt{IRTryLetStmt{
				GoName:     goVarName,
				CallExpr:   loweredExpr,
				ReturnType: retType,
			}}
		}
	}

	// Discard: let _ = expr
	if s.Name == "_" {
		return []IRStmt{IRLetStmt{
			GoName: "_",
			Value:  l.lowerExpr(s.Value),
		}}
	}

	// Lower value BEFORE declaring variable (shadowing must not affect the RHS)
	// Constrained constructor without ?: wrap in Result
	if l.isConstrainedConstructor(s.Value) {
		l.builtins["result"] = true
		goType := l.inferGoType(s.Value)
		loweredExpr := l.lowerExpr(s.Value)
		goVarName := l.declareVar(s.Name)
		return []IRStmt{IRConstrainedLetStmt{
			GoName:   goVarName,
			CallExpr: loweredExpr,
			GoType:   goType,
		}}
	}

	loweredValue := l.lowerExpr(s.Value)
	var loweredType IRType
	if s.Type != nil {
		loweredType = l.lowerType(s.Type)
	}
	goVarName := l.declareVar(s.Name)

	return []IRStmt{IRLetStmt{
		GoName: goVarName,
		Value:  loweredValue,
		Type:   loweredType,
	}}
}

func (l *Lowerer) lowerLetDestructure(pat Pattern, value Expr) []IRStmt {
	switch p := pat.(type) {
	case TuplePattern:
		var bindings []IRDestructureBinding
		for i, elemPat := range p.Elements {
			if bp, ok := elemPat.(BindPattern); ok {
				bindings = append(bindings, IRDestructureBinding{
					GoName: snakeToCamel(bp.Name),
					Index:  i,
				})
			}
		}
		return []IRStmt{IRDestructureStmt{
			Kind:     IRDestructureTuple,
			Bindings: bindings,
			Value:    l.lowerExpr(value),
		}}
	case ListPattern:
		var bindings []IRDestructureBinding
		for i, elemPat := range p.Elements {
			if bp, ok := elemPat.(BindPattern); ok {
				bindings = append(bindings, IRDestructureBinding{
					GoName: snakeToCamel(bp.Name),
					Index:  i,
				})
			}
		}
		if p.Rest != "" {
			bindings = append(bindings, IRDestructureBinding{
				GoName: snakeToCamel(p.Rest),
				Index:  len(p.Elements),
				Slice:  true,
			})
		}
		return []IRStmt{IRDestructureStmt{
			Kind:     IRDestructureList,
			Bindings: bindings,
			Value:    l.lowerExpr(value),
		}}
	default:
		return []IRStmt{IRLetStmt{
			GoName: "_",
			Value:  l.lowerExpr(value),
		}}
	}
}

// --- Match Expressions ---

func (l *Lowerer) lowerMatchExpr(me MatchExpr) IRExpr {
	if l.isResultMatch(me) {
		return l.lowerResultMatch(me)
	}
	if l.isOptionMatch(me) {
		return l.lowerOptionMatch(me)
	}
	if l.isListMatch(me) {
		return l.lowerListMatch(me)
	}
	if l.isLiteralMatch(me) {
		return l.lowerLiteralMatch(me)
	}
	if l.isEnumMatch(me) {
		return l.lowerEnumMatch(me)
	}
	return l.lowerSumTypeMatch(me)
}

func (l *Lowerer) isResultMatch(me MatchExpr) bool {
	for _, arm := range me.Arms {
		if cp, ok := arm.Pattern.(ConstructorPattern); ok {
			if cp.Name == "Ok" || cp.Name == "Error" {
				if _, isUserCtor := l.ctorTypes[cp.Name]; !isUserCtor {
					return true
				}
			}
		}
	}
	return false
}

func (l *Lowerer) lowerResultMatch(me MatchExpr) IRExpr {
	subject := l.lowerExpr(me.Subject)
	var okArm IRResultArm
	var errorArm IRResultArm

	for _, arm := range me.Arms {
		cp, ok := arm.Pattern.(ConstructorPattern)
		if !ok {
			continue
		}
		body := l.lowerExpr(arm.Body)
		usedVars := collectUsedIdents(arm.Body)
		if cp.Name == "Ok" {
			var binding *IRBinding
			if len(cp.Fields) > 0 {
				if _, used := usedVars[cp.Fields[0].Binding]; used {
					binding = &IRBinding{
						GoName: snakeToCamel(cp.Fields[0].Binding),
						Source: ".Value",
					}
				}
			}
			okArm = IRResultArm{Binding: binding, Body: body}
		}
		if cp.Name == "Error" {
			var binding *IRBinding
			if len(cp.Fields) > 0 {
				if _, used := usedVars[cp.Fields[0].Binding]; used {
					binding = &IRBinding{
						GoName: snakeToCamel(cp.Fields[0].Binding),
						Source: ".Err",
					}
				}
			}
			errorArm = IRResultArm{Binding: binding, Body: body}
		}
	}

	return IRResultMatch{
		Subject:  subject,
		OkArm:    okArm,
		ErrorArm: errorArm,
		Type:     IRInterfaceType{},
	}
}

func (l *Lowerer) isOptionMatch(me MatchExpr) bool {
	for _, arm := range me.Arms {
		if cp, ok := arm.Pattern.(ConstructorPattern); ok {
			if cp.Name == "Some" || cp.Name == "None" {
				return true
			}
		}
	}
	return false
}

func (l *Lowerer) lowerOptionMatch(me MatchExpr) IRExpr {
	subject := l.lowerExpr(me.Subject)
	var someArm IROptionSomeArm
	var noneBody IRExpr

	for _, arm := range me.Arms {
		cp, ok := arm.Pattern.(ConstructorPattern)
		if !ok {
			continue
		}
		body := l.lowerExpr(arm.Body)
		if cp.Name == "Some" {
			var binding *IRBinding
			if len(cp.Fields) > 0 {
				binding = &IRBinding{
					GoName: snakeToCamel(cp.Fields[0].Binding),
					Source: ".Value",
				}
			}
			someArm = IROptionSomeArm{Binding: binding, Body: body}
		}
		if cp.Name == "None" {
			noneBody = body
		}
	}

	return IROptionMatch{
		Subject: subject,
		SomeArm: someArm,
		NoneArm: noneBody,
		Type:    IRInterfaceType{},
	}
}

func (l *Lowerer) isListMatch(me MatchExpr) bool {
	for _, arm := range me.Arms {
		if _, ok := arm.Pattern.(ListPattern); ok {
			return true
		}
	}
	return false
}

func (l *Lowerer) lowerListMatch(me MatchExpr) IRExpr {
	subject := l.lowerExpr(me.Subject)
	var arms []IRListArm

	for _, arm := range me.Arms {
		body := l.lowerExpr(arm.Body)
		lp, ok := arm.Pattern.(ListPattern)
		if !ok {
			// Wildcard / bind default arm
			arms = append(arms, IRListArm{
				Kind: IRListDefault,
				Body: body,
			})
			continue
		}

		if len(lp.Elements) == 0 && lp.Rest == "" {
			arms = append(arms, IRListArm{
				Kind: IRListEmpty,
				Body: body,
			})
			continue
		}

		var bindings []IRBinding
		usedVars := collectUsedIdents(arm.Body)
		for i, elemPat := range lp.Elements {
			if bp, ok := elemPat.(BindPattern); ok {
				if _, used := usedVars[bp.Name]; used {
					bindings = append(bindings, IRBinding{
						GoName: snakeToCamel(bp.Name),
						Source: fmt.Sprintf("[%d]", i),
					})
				}
			}
		}

		var rest *IRBinding
		if lp.Rest != "" {
			if _, used := usedVars[lp.Rest]; used {
				rest = &IRBinding{
					GoName: snakeToCamel(lp.Rest),
					Source: fmt.Sprintf("[%d:]", len(lp.Elements)),
				}
			}
		}

		kind := IRListExact
		if lp.Rest != "" {
			kind = IRListCons
		}

		arms = append(arms, IRListArm{
			Kind:     kind,
			Elements: bindings,
			Rest:     rest,
			MinLen:   len(lp.Elements),
			Body:     body,
		})
	}

	return IRListMatch{
		Subject: subject,
		Arms:    arms,
		Type:    IRInterfaceType{},
	}
}

func (l *Lowerer) isLiteralMatch(me MatchExpr) bool {
	for _, arm := range me.Arms {
		if _, ok := arm.Pattern.(LitPattern); ok {
			return true
		}
	}
	return false
}

func (l *Lowerer) lowerLiteralMatch(me MatchExpr) IRExpr {
	subject := l.lowerExpr(me.Subject)
	var arms []IRLiteralArm
	var defaultBody *IRExpr

	for _, arm := range me.Arms {
		body := l.lowerExpr(arm.Body)
		switch p := arm.Pattern.(type) {
		case LitPattern:
			arms = append(arms, IRLiteralArm{
				Value: l.litPatternGoStr(p),
				Body:  body,
			})
		case WildcardPattern:
			b := body
			defaultBody = &b
		case BindPattern:
			b := body
			defaultBody = &b
		}
	}

	return IRLiteralMatch{
		Subject: subject,
		Arms:    arms,
		Default: defaultBody,
		Type:    IRInterfaceType{},
	}
}

func (l *Lowerer) litPatternGoStr(lp LitPattern) string {
	switch e := lp.Expr.(type) {
	case IntLit:
		return fmt.Sprintf("%d", e.Value)
	case FloatLit:
		return fmt.Sprintf("%g", e.Value)
	case StringLit:
		return fmt.Sprintf("%q", e.Value)
	case BoolLit:
		if e.Value {
			return "true"
		}
		return "false"
	default:
		return "/* unknown lit */"
	}
}

func (l *Lowerer) isEnumMatch(me MatchExpr) bool {
	for _, arm := range me.Arms {
		if cp, ok := arm.Pattern.(ConstructorPattern); ok {
			typeName := l.findTypeName(cp.Name)
			if td, ok := l.types[typeName]; ok {
				return isEnum(td)
			}
		}
	}
	return false
}

func (l *Lowerer) lowerEnumMatch(me MatchExpr) IRExpr {
	subject := l.lowerExpr(me.Subject)
	var arms []IREnumArm
	var wildcard *IRExpr

	for _, arm := range me.Arms {
		body := l.lowerExpr(arm.Body)
		if cp, ok := arm.Pattern.(ConstructorPattern); ok {
			typeName := l.findTypeName(cp.Name)
			arms = append(arms, IREnumArm{
				GoValue: typeName + cp.Name,
				Body:    body,
			})
		} else {
			b := body
			wildcard = &b
		}
	}

	return IREnumMatch{
		Subject:  subject,
		Arms:     arms,
		Wildcard: wildcard,
		Type:     IRInterfaceType{},
	}
}

func (l *Lowerer) lowerSumTypeMatch(me MatchExpr) IRExpr {
	subject := l.lowerExpr(me.Subject)
	var arms []IRSumTypeArm
	var wildcard *IRSumTypeWildcard

	for _, arm := range me.Arms {
		body := l.lowerExpr(arm.Body)
		switch pat := arm.Pattern.(type) {
		case ConstructorPattern:
			typeName := l.findTypeName(pat.Name)
			variantName := typeName + pat.Name

			usedVars := collectUsedIdents(arm.Body)
			var ctorFields []Field
			if td, ok := l.types[typeName]; ok {
				for _, c := range td.Constructors {
					if c.Name == pat.Name {
						ctorFields = c.Fields
						break
					}
				}
			}

			var bindings []IRBinding
			for i, fp := range pat.Fields {
				if _, used := usedVars[fp.Binding]; used {
					goFieldName := capitalize(fp.Name)
					if i < len(ctorFields) {
						goFieldName = capitalize(ctorFields[i].Name)
					}
					bindings = append(bindings, IRBinding{
						GoName: snakeToCamel(fp.Binding),
						Source: "." + goFieldName,
					})
				}
			}

			arms = append(arms, IRSumTypeArm{
				GoType:   variantName,
				Bindings: bindings,
				Body:     body,
			})
		case WildcardPattern:
			wildcard = &IRSumTypeWildcard{Body: body}
		case BindPattern:
			wildcard = &IRSumTypeWildcard{
				Binding: &IRBinding{GoName: snakeToCamel(pat.Name)},
				Body:    body,
			}
		}
	}

	return IRSumTypeMatch{
		Subject:  subject,
		Arms:     arms,
		Wildcard: wildcard,
		Type:     IRInterfaceType{},
	}
}

// --- Helpers ---

func (l *Lowerer) lowerExprs(exprs []Expr) []IRExpr {
	result := make([]IRExpr, len(exprs))
	for i, e := range exprs {
		result[i] = l.lowerExpr(e)
	}
	return result
}

func (l *Lowerer) findTypeName(ctorName string) string {
	for typeName, td := range l.types {
		for _, c := range td.Constructors {
			if c.Name == ctorName {
				return typeName
			}
		}
	}
	return ""
}

func (l *Lowerer) resultTypeArgs() string {
	if l.currentRetType == nil {
		return ""
	}
	if nt, ok := l.currentRetType.(NamedType); ok && nt.Name == "Result" {
		if len(nt.Params) >= 2 {
			return "[" + l.goTypeStr(nt.Params[0]) + ", " + l.goTypeStr(nt.Params[1]) + "]"
		}
		if len(nt.Params) == 1 {
			return "[" + l.goTypeStr(nt.Params[0]) + ", error]"
		}
	}
	return ""
}

func (l *Lowerer) resolveMethodName(name string) string {
	for _, td := range l.types {
		for _, m := range td.Methods {
			if m.Name == name {
				if m.Public {
					return snakeToPascal(name)
				}
				return snakeToCamel(name)
			}
		}
	}
	return capitalize(name)
}

func (l *Lowerer) isConstrainedConstructor(expr Expr) bool {
	cc, ok := expr.(ConstructorCall)
	if !ok {
		return false
	}
	if alias, ok := l.typeAliases[cc.Name]; ok {
		if nt, ok := alias.Type.(NamedType); ok && len(nt.Constraints) > 0 {
			return true
		}
	}
	typeName := cc.TypeName
	if typeName == "Self" && l.currentTypeName != "" {
		typeName = l.currentTypeName
	}
	if typeName != "" {
		if td, ok := l.types[typeName]; ok {
			return l.hasConstraints(td)
		}
	}
	for _, td := range l.types {
		for _, ctor := range td.Constructors {
			if ctor.Name == cc.Name {
				return l.hasConstraints(td)
			}
		}
	}
	return false
}

func (l *Lowerer) inferGoType(expr Expr) string {
	switch e := expr.(type) {
	case IntLit:
		return "int"
	case FloatLit:
		return "float64"
	case StringLit, StringInterp:
		return "string"
	case BoolLit:
		return "bool"
	case ConstructorCall:
		if _, ok := l.typeAliases[e.Name]; ok {
			return e.Name
		}
		typeName := e.TypeName
		if typeName == "Self" && l.currentTypeName != "" {
			typeName = l.currentTypeName
		}
		if typeName != "" {
			return typeName
		}
		if tn, ok := l.ctorTypes[e.Name]; ok {
			return tn
		}
		return e.Name
	default:
		return "interface{}"
	}
}

func (l *Lowerer) inferGoElemType(expr Expr) string {
	switch expr.(type) {
	case IntLit:
		return "int"
	case FloatLit:
		return "float64"
	case StringLit, StringInterp:
		return "string"
	case BoolLit:
		return "bool"
	default:
		return "interface{}"
	}
}

// exprToString produces a human-readable string representation of an AST expression,
// used for assert panic messages.
func (l *Lowerer) exprToString(expr Expr) string {
	switch e := expr.(type) {
	case IntLit:
		return fmt.Sprintf("%d", e.Value)
	case FloatLit:
		return fmt.Sprintf("%g", e.Value)
	case StringLit:
		return fmt.Sprintf("%q", e.Value)
	case BoolLit:
		if e.Value {
			return "true"
		}
		return "false"
	case Ident:
		return e.Name
	case BinaryExpr:
		return l.exprToString(e.Left) + " " + e.Op + " " + l.exprToString(e.Right)
	case FnCall:
		if ident, ok := e.Fn.(Ident); ok {
			args := make([]string, len(e.Args))
			for i, a := range e.Args {
				args[i] = l.exprToString(a)
			}
			return ident.Name + "(" + strings.Join(args, ", ") + ")"
		}
		return "/* expr */"
	case FieldAccess:
		return l.exprToString(e.Expr) + "." + e.Field
	default:
		return "/* expr */"
	}
}
