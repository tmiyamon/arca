package main

import (
	"fmt"
	"strings"
)

// Lowerer converts an AST Program into an IR Program.
// It resolves names, constructors, builtins, shadowing, and match kinds.
type Lowerer struct {
	types        map[string]TypeDecl
	typeAliases  map[string]TypeAliasDecl
	ctorTypes    map[string]string // constructor name → type name
	fnNames      map[string]string // arca name → Go name for pub functions
	functions    map[string]FnDecl
	moduleNames  map[string]bool
	goModule     string
	typeResolver  TypeResolver
	goPackages    map[string]*GoPackage // short name → Go package info

	// Per-function state
	currentRetType  Type
	currentReceiver string
	currentTypeName string

	// Collected during lowering
	imports      []IRImport
	builtins     map[string]bool
	tmpCounter   int
	errors       []LowerError
	symbols      []SymbolInfo // all symbols (flat, for LSP global list)
	rootScope    *Scope       // root of scope tree (preserved after lowering)
	currentScope *Scope       // current scope during lowering
}

type LowerError struct {
	Pos     Pos
	Message string
}

func (l *Lowerer) addError(pos Pos, format string, args ...interface{}) {
	l.errors = append(l.errors, LowerError{Pos: pos, Message: fmt.Sprintf(format, args...)})
}

func (l *Lowerer) recordSymbol(name string, t Type, kind string) {
	l.symbols = append(l.symbols, SymbolInfo{Name: name, Type: t, Kind: kind})
}

// registerSymbol registers a data symbol (variable, parameter, binding) with both
// AST and IR types. All variable bindings must go through this function.
// Returns the resolved Go name (with shadowing suffix if needed).
func (l *Lowerer) registerSymbol(info SymbolRegInfo) string {
	sym := NewSymbolInfo(info.Name, info.Kind)

	// Same-scope shadowing: suffix with count
	if l.currentScope != nil {
		count := l.currentScope.declCount[sym.GoName]
		l.currentScope.declCount[sym.GoName] = count + 1
		if count > 0 {
			sym.GoName = fmt.Sprintf("%s_%d", sym.GoName, count+1)
		}
	}

	if info.ArcaType != nil {
		sym.Type = info.ArcaType
	}
	if info.IRType != nil {
		if _, isInterface := info.IRType.(IRInterfaceType); !isInterface {
			sym.IRType = info.IRType
		}
	}

	// Record in lexical scope
	if l.currentScope != nil {
		l.currentScope.Define(info.Name, &sym)
	}

	// Record in flat list (for LSP global queries)
	l.symbols = append(l.symbols, sym)

	return sym.GoName
}

type SymbolRegInfo struct {
	Name     string
	ArcaType Type   // nullable
	IRType   IRType // nullable
	Kind     string // SymVariable, SymParameter, etc.
}

func (l *Lowerer) withScope(startPos, endPos Pos, symbols []SymbolRegInfo, fn func()) {
	l.currentScope = NewScope(l.currentScope)
	l.currentScope.StartPos = startPos
	l.currentScope.EndPos = endPos
	defer func() { l.currentScope = l.currentScope.parent }()
	for _, s := range symbols {
		l.registerSymbol(s)
	}
	fn()
}

// LookupSymbol finds a symbol by name in the current lexical scope chain.
// Falls back to flat symbol list if no scope is active.
func (l *Lowerer) LookupSymbol(name string) *SymbolInfo {
	if l.currentScope != nil {
		return l.currentScope.Lookup(name)
	}
	// Fallback: flat search
	for i := len(l.symbols) - 1; i >= 0; i-- {
		if l.symbols[i].Name == name {
			return &l.symbols[i]
		}
	}
	return nil
}

// FindSymbolAt finds a symbol by name at a specific source position,
// using the scope tree to resolve lexical scoping.
func (l *Lowerer) FindSymbolAt(name string, pos Pos) *SymbolInfo {
	if l.rootScope != nil {
		scope := l.rootScope.FindScopeAt(pos)
		return scope.Lookup(name)
	}
	return l.LookupSymbol(name)
}

// Types returns the collected type declarations.
func (l *Lowerer) Types() map[string]TypeDecl { return l.types }

// TypeAliases returns the collected type alias declarations.
func (l *Lowerer) TypeAliases() map[string]TypeAliasDecl { return l.typeAliases }

// Functions returns the collected function declarations.
func (l *Lowerer) Functions() map[string]FnDecl { return l.functions }

func (l *Lowerer) Errors() []LowerError {
	return l.errors
}

func NewLowerer(prog *Program, goModule string, resolver TypeResolver) *Lowerer {
	if resolver == nil {
		resolver = NullTypeResolver{}
	}
	root := NewScope(nil)
	l := &Lowerer{
		types:        make(map[string]TypeDecl),
		typeAliases:  make(map[string]TypeAliasDecl),
		ctorTypes:    make(map[string]string),
		fnNames:      make(map[string]string),
		functions:    make(map[string]FnDecl),
		moduleNames:  make(map[string]bool),
		builtins:     make(map[string]bool),
		goModule:     goModule,
		typeResolver: resolver,
		rootScope:    root,
		currentScope: root,
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
				pkg := NewGoPackage(d.Path[3:])
				if l.goPackages == nil {
					l.goPackages = make(map[string]*GoPackage)
				}
				l.goPackages[pkg.ShortName] = pkg
				l.registerSymbol(SymbolRegInfo{Name: pkg.ShortName, Kind: SymPackage})
				l.imports = append(l.imports, IRImport{
					Path:       pkg.FullPath,
					SideEffect: d.SideEffect,
				})
				// Check if the package can be loaded
				if !isStdLib(pkg.FullPath) && !l.typeResolver.CanLoadPackage(pkg.FullPath) {
					l.addError(Pos{}, "package %s not found. Run: go get %s", pkg.FullPath, pkg.FullPath)
				}
			}
		case FnDecl:
			l.functions[d.Name] = d
			if d.Public {
				l.fnNames[d.Name] = snakeToPascal(d.Name)
			}
			l.registerSymbol(SymbolRegInfo{Name: d.Name, Kind: SymFunction})
		}
	}
	return l
}

// Lower converts the entire program.
// pkgName is the Go package name ("main" for main files).
// pubOnly limits function output to pub functions only (for same-dir module files).
func (l *Lowerer) Lower(prog *Program, pkgName string, pubOnly bool) IRProgram {
	var types []IRTypeDecl
	var funcs []IRFuncDecl

	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case TypeDecl:
			types = append(types, l.lowerTypeDecl(d))
			for _, method := range d.Methods {
				funcs = append(funcs, l.lowerMethod(d, method)...)
			}
		case TypeAliasDecl:
			types = append(types, l.lowerTypeAliasDecl(d))
		case FnDecl:
			if d.ReceiverType == "" {
				if pubOnly && !d.Public {
					continue
				}
				funcs = append(funcs, l.lowerFnDecl(d))
			}
		}
	}

	// Expand sum type methods to per-variant implementations
	funcs = l.expandSumTypeMethods(funcs)

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
		Package:  pkgName,
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
	// Collect method signatures for interface definition
	var ifaceMethods []IRInterfaceMethod
	for _, m := range td.Methods {
		if !m.Static {
			name := snakeToCamel(m.Name)
			if m.Public {
				name = snakeToPascal(m.Name)
			}
			var retType IRType
			if m.ReturnType != nil {
				retType = l.lowerType(m.ReturnType)
			}
			ifaceMethods = append(ifaceMethods, IRInterfaceMethod{
				Name:       name,
				Params:     l.lowerParams(m.Params),
				ReturnType: retType,
			})
		}
	}

	return IRSumTypeDecl{
		GoName:           td.Name,
		TypeParams:       td.Params,
		Variants:         variants,
		InterfaceMethods: ifaceMethods,
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
	sp, ep := bodyPos(fd.Body)
	var body IRExpr
	l.withScope(sp, ep, l.paramsToSymbols(fd.Params), func() {
		body = l.lowerFnBody(fd.Body, fd.ReturnType != nil)
	})
	l.currentRetType = nil

	return IRFuncDecl{
		GoName:     name,
		Params:     params,
		ReturnType: retType,
		Body:       body,
		Source:     SourceInfo{Pos: fd.Pos, Name: fd.Name, ReturnType: fd.ReturnType},
	}
}

func (l *Lowerer) lowerMethod(td TypeDecl, fd FnDecl) []IRFuncDecl {
	if fd.Static {
		return []IRFuncDecl{l.lowerAssociatedFunc(td, fd)}
	}

	// Sum type methods: lower as normal method, expand to per-variant later

	methodName := snakeToCamel(fd.Name)
	if fd.Public {
		methodName = snakeToPascal(fd.Name)
	}

	receiver := strings.ToLower(td.Name[:1])
	l.currentRetType = fd.ReturnType
	l.currentReceiver = receiver
	l.currentTypeName = td.Name

	params := l.lowerParams(fd.Params)
	var retType IRType
	if fd.ReturnType != nil {
		retType = l.lowerType(fd.ReturnType)
	}
	sp, ep := bodyPos(fd.Body)
	var body IRExpr
	l.withScope(sp, ep, l.paramsToSymbols(fd.Params), func() {
		body = l.lowerFnBody(fd.Body, fd.ReturnType != nil)
	})
	l.currentReceiver = ""
	l.currentTypeName = ""
	l.currentRetType = nil

	return []IRFuncDecl{{
		GoName: methodName,
		Receiver: &IRReceiver{
			GoName: receiver,
			Type:   td.Name,
		},
		Params:     params,
		ReturnType: retType,
		Body:       body,
		Source:     SourceInfo{Pos: fd.Pos, Name: fd.Name, TypeName: td.Name, ReturnType: fd.ReturnType},
	}}
}

// expandSumTypeMethods transforms methods on sum types (interfaces) into
// per-variant method implementations. Operates on IR after lowering.
func (l *Lowerer) expandSumTypeMethods(funcs []IRFuncDecl) []IRFuncDecl {
	var result []IRFuncDecl
	for _, fn := range funcs {
		if fn.Receiver == nil {
			result = append(result, fn)
			continue
		}
		// Check if receiver type is a sum type
		td, isSumType := l.types[fn.Receiver.Type]
		if !isSumType || len(td.Constructors) <= 1 || isEnum(td) {
			result = append(result, fn)
			continue
		}
		// Expand to per-variant methods
		result = append(result, l.expandToVariants(fn, td)...)
	}
	return result
}

func (l *Lowerer) expandToVariants(fn IRFuncDecl, td TypeDecl) []IRFuncDecl {
	// Find match on self in body
	matchExpr := findIRMatchSelf(fn.Body, fn.Receiver.GoName)
	if matchExpr != nil {
		return l.expandMatchSelf(fn, td, matchExpr)
	}
	// No match self: duplicate body for each variant
	return l.duplicateForVariants(fn, td)
}

// findIRMatchSelf finds an IRMatch on the receiver variable in the IR body.
func findIRMatchSelf(body IRExpr, receiverName string) *IRMatch {
	switch e := body.(type) {
	case IRMatch:
		if ident, ok := e.Subject.(IRIdent); ok && ident.GoName == receiverName {
			return &e
		}
	case IRBlock:
		if e.Expr != nil {
			return findIRMatchSelf(e.Expr, receiverName)
		}
	}
	return nil
}

// expandMatchSelf splits a match-on-self method into per-variant methods,
// each with the corresponding arm's body.
func (l *Lowerer) expandMatchSelf(fn IRFuncDecl, td TypeDecl, m *IRMatch) []IRFuncDecl {
	var funcs []IRFuncDecl
	for _, arm := range m.Arms {
		p, ok := arm.Pattern.(IRSumTypePattern)
		if !ok {
			continue
		}
		// Build body: bind pattern fields from receiver, then arm body
		var stmts []IRStmt
		for _, b := range p.Bindings {
			stmts = append(stmts, IRLetStmt{
				GoName: b.GoName,
				Value:  IRFieldAccess{Expr: IRIdent{GoName: fn.Receiver.GoName}, Field: b.Source[1:]}, // strip leading "."
			})
		}
		var body IRExpr
		if len(stmts) > 0 {
			body = IRBlock{Stmts: stmts, Expr: arm.Body}
		} else {
			body = arm.Body
		}
		funcs = append(funcs, IRFuncDecl{
			GoName:     fn.GoName,
			Receiver:   &IRReceiver{GoName: fn.Receiver.GoName, Type: p.GoType},
			Params:     fn.Params,
			ReturnType: fn.ReturnType,
			Body:       body,
			Source:     fn.Source,
		})
	}
	return funcs
}

// duplicateForVariants creates identical method implementations for each variant.
func (l *Lowerer) duplicateForVariants(fn IRFuncDecl, td TypeDecl) []IRFuncDecl {
	var funcs []IRFuncDecl
	for _, ctor := range td.Constructors {
		variantName := td.Name + ctor.Name
		funcs = append(funcs, IRFuncDecl{
			GoName:     fn.GoName,
			Receiver:   &IRReceiver{GoName: fn.Receiver.GoName, Type: variantName},
			Params:     fn.Params,
			ReturnType: fn.ReturnType,
			Body:       fn.Body,
			Source:     fn.Source,
		})
	}
	return funcs
}

func (l *Lowerer) lowerAssociatedFunc(td TypeDecl, fd FnDecl) IRFuncDecl {
	funcName := td.Name + capitalize(fd.Name)
	if !fd.Public {
		funcName = strings.ToLower(td.Name[:1]) + td.Name[1:] + capitalize(fd.Name)
	}

	l.currentRetType = fd.ReturnType
	l.currentTypeName = td.Name

	params := l.lowerParams(fd.Params)
	var retType IRType
	if fd.ReturnType != nil {
		retType = l.lowerType(fd.ReturnType)
	}
	sp, ep := bodyPos(fd.Body)
	var body IRExpr
	l.withScope(sp, ep, l.paramsToSymbols(fd.Params), func() {
		body = l.lowerFnBody(fd.Body, fd.ReturnType != nil)
	})
	l.currentTypeName = ""
	l.currentRetType = nil

	return IRFuncDecl{
		GoName:     funcName,
		Params:     params,
		ReturnType: retType,
		Body:       body,
		Source:     SourceInfo{Pos: fd.Pos, Name: fd.Name, TypeName: td.Name, ReturnType: fd.ReturnType},
	}
}

func (l *Lowerer) lowerParams(params []FnParam) []IRParamDecl {
	result := make([]IRParamDecl, len(params))
	for i, p := range params {
		result[i] = IRParamDecl{
			GoName: snakeToCamel(p.Name),
			Type:   l.lowerType(p.Type),
			Source: SourceInfo{Type: p.Type},
		}
	}
	return result
}

func (l *Lowerer) lowerFnBody(body Expr, hasReturn bool) IRExpr {
	expr := l.lowerExpr(body)
	if !hasReturn {
		expr = l.markVoidContext(expr)
	}
	return expr
}

// markVoidContext replaces trailing Unit in match arms when the result is not used.
// Applied to void function bodies and expression statements.
func (l *Lowerer) markVoidContext(expr IRExpr) IRExpr {
	if m, ok := expr.(IRMatch); ok {
		for i := range m.Arms {
			m.Arms[i].Body = replaceTrailingUnit(m.Arms[i].Body)
		}
		return m
	}
	if block, ok := expr.(IRBlock); ok && block.Expr != nil {
		block.Expr = l.markVoidContext(block.Expr)
		return block
	}
	return expr
}

// checkTypeHint checks if an expression's type matches the expected hint type.
func (l *Lowerer) checkTypeHint(result IRExpr, hint IRType, sourceExpr Expr) {
	actualType := result.irType()
	if actualType == nil {
		return
	}
	// Skip if either type is unknown
	if _, ok := actualType.(IRInterfaceType); ok {
		return
	}
	if _, ok := hint.(IRInterfaceType); ok {
		return
	}
	if !irTypesMatch(actualType, hint) {
		pos := Pos{}
		if ident, ok := sourceExpr.(Ident); ok {
			pos = ident.Pos
		}
		l.addError(pos, "type mismatch: expected %s, got %s", irTypeDisplayStr(hint), irTypeDisplayStr(actualType))
	}
}

// irTypesMatch checks if two IR types are compatible.
func irTypesMatch(a, b IRType) bool {
	if a == nil || b == nil {
		return true
	}
	// Both unknown = compatible
	if _, aOk := a.(IRInterfaceType); aOk {
		return true
	}
	if _, bOk := b.(IRInterfaceType); bOk {
		return true
	}
	switch at := a.(type) {
	case IRNamedType:
		if bt, ok := b.(IRNamedType); ok {
			return at.GoName == bt.GoName
		}
	case IRPointerType:
		if bt, ok := b.(IRPointerType); ok {
			return irTypesMatch(at.Inner, bt.Inner)
		}
	case IRResultType:
		if bt, ok := b.(IRResultType); ok {
			return irTypesMatch(at.Ok, bt.Ok) && irTypesMatch(at.Err, bt.Err)
		}
	case IROptionType:
		if bt, ok := b.(IROptionType); ok {
			return irTypesMatch(at.Inner, bt.Inner)
		}
	case IRListType:
		if bt, ok := b.(IRListType); ok {
			return irTypesMatch(at.Elem, bt.Elem)
		}
	}
	return true // default: assume compatible for unknown combos
}

// irTypeDisplayStr returns a human-readable type name for error messages.
func irTypeDisplayStr(t IRType) string {
	switch tt := t.(type) {
	case IRNamedType:
		return tt.GoName
	case IRPointerType:
		return "*" + irTypeDisplayStr(tt.Inner)
	case IRResultType:
		return "Result[" + irTypeDisplayStr(tt.Ok) + ", " + irTypeDisplayStr(tt.Err) + "]"
	case IROptionType:
		return "Option[" + irTypeDisplayStr(tt.Inner) + "]"
	case IRListType:
		return "List[" + irTypeDisplayStr(tt.Elem) + "]"
	default:
		return "unknown"
	}
}

// bodyPos extracts start/end position from a function body expression.
func bodyPos(body Expr) (Pos, Pos) {
	if b, ok := body.(Block); ok {
		return b.Pos, b.EndPos
	}
	return Pos{}, Pos{}
}

// --- Variable Scoping ---

// paramsToSymbols converts function parameters to symbol registration info.
func (l *Lowerer) paramsToSymbols(params []FnParam) []SymbolRegInfo {
	syms := make([]SymbolRegInfo, len(params))
	for i, p := range params {
		var irType IRType
		if p.Type != nil {
			irType = l.lowerType(p.Type)
		}
		syms[i] = SymbolRegInfo{
			Name:     p.Name,
			ArcaType: p.Type,
			IRType:   irType,
			Kind:     SymParameter,
		}
	}
	return syms
}

func (l *Lowerer) resolveVar(name string) string {
	if l.currentScope != nil {
		if sym := l.currentScope.Lookup(name); sym != nil {
			return sym.GoName
		}
	}
	return snakeToCamel(name)
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
		case "Self":
			if l.currentTypeName != "" {
				return l.currentTypeName
			}
			return "Self"
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
	return l.lowerExprHint(expr, nil)
}

func (l *Lowerer) lowerExprHint(expr Expr, hint IRType) IRExpr {
	result := l.lowerExprInner(expr, hint)
	if hint != nil && result != nil {
		l.checkTypeHint(result, hint, expr)
	}
	return result
}

func (l *Lowerer) lowerExprInner(expr Expr, hint IRType) IRExpr {
	if expr == nil {
		return nil
	}
	switch e := expr.(type) {
	case IntLit:
		return IRIntLit{Value: e.Value, Type: IRNamedType{GoName: "int"}}
	case FloatLit:
		return IRFloatLit{Value: e.Value, Type: IRNamedType{GoName: "float64"}}
	case StringLit:
		return IRStringLit{Value: e.Value, Type: IRNamedType{GoName: "string"}, Multiline: e.Multiline}
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
		return l.lowerLambdaHint(e, hint)
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
	// Variable resolution via lexical scope
	goName := l.resolveVar(e.Name)
	var arcaType Type
	irType := IRType(IRInterfaceType{})
	found := false
	if l.currentScope != nil {
		if sym := l.currentScope.Lookup(e.Name); sym != nil {
			arcaType = sym.Type
			if sym.IRType != nil {
				irType = sym.IRType
			}
			found = true
		}
	}
	if !found && l.currentScope != nil {
		l.addError(e.Pos, "undefined variable: %s", e.Name)
	}
	return IRIdent{GoName: goName, Type: irType, Source: SourceInfo{Type: arcaType}}
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
		return IRStringLit{Value: fmtStr, Type: IRNamedType{GoName: "string"}, Multiline: si.Multiline}
	}
	return IRStringInterp{
		Format:    fmtStr,
		Args:      args,
		Type:      IRNamedType{GoName: "string"},
		Multiline: si.Multiline,
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
		default:
			// Check prelude builtins
			if def, ok := prelude[ident.Name]; ok {
				args := l.lowerExprs(e.Args)
				if def.Import != "" {
					l.builtins[def.Import] = true
				}
				if def.Builtin != "" {
					l.builtins[def.Builtin] = true
				}
				if def.Lower != nil {
					if result := def.Lower(args); result != nil {
						return result
					}
				} else {
					return IRFnCall{Func: def.GoFunc, Args: args, Type: IRInterfaceType{}, Source: SourceInfo{Pos: e.Pos}}
				}
			}
		}
	}

	// Go FFI or module-qualified call
	if fa, ok := e.Fn.(FieldAccess); ok {
		// Go FFI call: strconv.Itoa(...), http.HandleFunc(...)
		if ident, ok := fa.Expr.(Ident); ok {
			if _, isGoPkg := l.goPackages[ident.Name]; isGoPkg {
				goCallName := ident.Name + "." + fa.Field
				args := l.lowerCallArgs(e)
				ret := l.resolveGoCall(goCallName, args, e.Pos)
				return IRFnCall{Func: goCallName, Args: args, Type: ret.Type, GoMultiReturn: ret.GoMultiReturn, Source: SourceInfo{Pos: e.Pos}}
			}
		}
		// Arca module-qualified call
		if ident, ok := fa.Expr.(Ident); ok && l.moduleNames[ident.Name] {
			fnName := fa.Field
			if goName, ok := l.fnNames[fnName]; ok {
				fnName = goName
			}
			args := l.lowerCallArgs(e)
			if l.goModule != "" {
				return IRFnCall{
					Func:   ident.Name + "." + fnName,
					Args:   args,
					Type:   IRInterfaceType{},
					Source: SourceInfo{Pos: e.Pos, Name: fa.Field},
				}
			}
			return IRFnCall{Func: fnName, Args: args, Type: IRInterfaceType{}, Source: SourceInfo{Pos: e.Pos, Name: fa.Field}}
		}
		// Regular method call: obj.method(args)
		methodName := l.resolveMethodName(fa.Field)
		args := l.lowerCallArgs(e)
		receiver := l.lowerExpr(fa.Expr)
		ret := l.resolveMethodReturnType(receiver, fa.Field)
		return IRMethodCall{
			Receiver:      receiver,
			Method:        methodName,
			Args:          args,
			Type:          ret.Type,
			GoMultiReturn: ret.GoMultiReturn,
		}
	}

	args := l.lowerCallArgs(e)
	fnExpr := l.lowerExpr(e.Fn)
	if ident, ok := fnExpr.(IRIdent); ok {
		arcaName := ""
		if id, ok := e.Fn.(Ident); ok {
			arcaName = id.Name
		}
		// Try Arca function return type first
		var fnType IRType = IRInterfaceType{}
		var goMultiReturn bool
		if id, ok := e.Fn.(Ident); ok {
			if fn, ok := l.functions[id.Name]; ok && fn.ReturnType != nil {
				fnType = l.lowerType(fn.ReturnType)
			}
		}
		// Fall back to Go FFI resolution
		if _, isInterface := fnType.(IRInterfaceType); isInterface {
			ret := l.resolveGoCall(ident.GoName, args, e.Pos)
			fnType = ret.Type
			goMultiReturn = ret.GoMultiReturn
		}
		return IRFnCall{Func: ident.GoName, Args: args, Type: fnType, GoMultiReturn: goMultiReturn, Source: SourceInfo{Pos: e.Pos, Name: arcaName}}
	}
	// Lambda call or other complex expression
	return IRFnCall{Func: "/* complex call */", Args: args, Type: IRInterfaceType{}, Source: SourceInfo{Pos: e.Pos}}
}

// resolveGoCall validates a Go FFI function call and returns the resolved return info.
// Checks argument count and types against the Go function signature.
func (l *Lowerer) resolveGoCall(goName string, args []IRExpr, pos Pos) goReturnInfo {
	if !strings.Contains(goName, ".") {
		return goReturnInfo{Type: IRInterfaceType{}}
	}
	parts := strings.SplitN(goName, ".", 2)
	pkgShort := parts[0]
	funcName := parts[1]

	goPkg, ok := l.goPackages[pkgShort]
	if !ok {
		return goReturnInfo{Type: IRInterfaceType{}}
	}

	info := l.typeResolver.ResolveFunc(goPkg.FullPath, funcName)
	if info == nil {
		return goReturnInfo{Type: IRInterfaceType{}}
	}

	// Validate argument count
	minArgs := len(info.Params)
	if info.Variadic {
		minArgs-- // last param is variadic, not required
	}
	if !info.Variadic && len(args) != len(info.Params) {
		l.addError(pos, "'%s' expects %d arguments, got %d", goName, len(info.Params), len(args))
	} else if info.Variadic && len(args) < minArgs {
		l.addError(pos, "'%s' expects at least %d arguments, got %d", goName, minArgs, len(args))
	}

	// Validate argument types (Phase 2)
	for i, arg := range args {
		argType := irTypeToGoString(arg.irType())
		if argType == "" {
			continue // unknown type — skip
		}

		var paramType string
		if i < len(info.Params) {
			paramType = info.Params[i].Type
		}

		// Variadic: args beyond param count check against the variadic element type
		if info.Variadic && i >= len(info.Params)-1 {
			variadicSliceType := info.Params[len(info.Params)-1].Type
			// "[]string" → "string", "[]any" → "any"
			if strings.HasPrefix(variadicSliceType, "[]") {
				paramType = variadicSliceType[2:]
			}
		}

		if paramType != "" && !goTypesCompatible(argType, paramType) {
			l.addError(pos, "argument %d of '%s' expects %s, got %s", i+1, goName, paramType, argType)
		}
	}

	return l.goFuncReturnType(info)
}

// irTypeToGoString converts an IRType to a Go type string for comparison.
// Returns "" if the type is unknown/interface{}.
func irTypeToGoString(t IRType) string {
	if t == nil {
		return ""
	}
	switch tt := t.(type) {
	case IRNamedType:
		switch tt.GoName {
		case "int", "float64", "string", "bool", "byte", "struct{}":
			return tt.GoName
		default:
			return "" // user-defined or complex — skip check
		}
	case IRListType:
		elem := irTypeToGoString(tt.Elem)
		if elem != "" {
			return "[]" + elem
		}
		return ""
	case IRInterfaceType:
		return "" // unknown — skip check
	default:
		return ""
	}
}

// goTypesCompatible checks if an Arca-resolved Go type is compatible with a Go parameter type.
func goTypesCompatible(arcaType, goParam string) bool {
	if arcaType == goParam {
		return true
	}
	// interface{}/any accepts everything
	if goParam == "any" || goParam == "interface{}" {
		return true
	}
	// Variadic slice: []any, []interface{}, []string etc.
	if strings.HasPrefix(goParam, "[]") && strings.HasPrefix(arcaType, "[]") {
		return goTypesCompatible(arcaType[2:], goParam[2:])
	}
	return false
}

// goReturnInfo holds the resolved Arca type and whether the Go function returns multiple values.
type goReturnInfo struct {
	Type          IRType
	GoMultiReturn bool // true if Go func returns multiple values (needs multi-value receive)
}

// goFuncReturnType converts a FuncInfo's return types to an Arca IR type.
// Go multi-return is mechanically mapped:
//
//	()           → Unit
//	(T)          → T
//	(error)      → Result[Unit, error]
//	(T, error)   → Result[T, error]
//	(T, bool)    → Option[T]
//	(T1, T2)     → (T1, T2)
//	(T1, T2, T3) → (T1, T2, T3)
func (l *Lowerer) goFuncReturnType(info *FuncInfo) goReturnInfo {
	if len(info.Results) == 0 {
		return goReturnInfo{Type: IRNamedType{GoName: "struct{}"}}
	}
	if len(info.Results) == 1 {
		if info.Results[0].Type == "error" {
			return goReturnInfo{
				Type:          IRResultType{Ok: IRNamedType{GoName: "struct{}"}, Err: IRNamedType{GoName: "error"}},
				GoMultiReturn: true,
			}
		}
		return goReturnInfo{Type: goTypeToIR(info.Results[0].Type)}
	}
	if len(info.Results) == 2 {
		if info.Results[1].Type == "error" {
			return goReturnInfo{
				Type:          IRResultType{Ok: goTypeToIR(info.Results[0].Type), Err: IRNamedType{GoName: "error"}},
				GoMultiReturn: true,
			}
		}
		if info.Results[1].Type == "bool" {
			return goReturnInfo{
				Type:          IROptionType{Inner: goTypeToIR(info.Results[0].Type)},
				GoMultiReturn: true,
			}
		}
	}
	// 2+ non-special or 3+ returns → Tuple
	elems := make([]IRType, len(info.Results))
	for i, r := range info.Results {
		elems[i] = goTypeToIR(r.Type)
	}
	return goReturnInfo{
		Type:          IRTupleType{Elements: elems},
		GoMultiReturn: true,
	}
}

// goTypeToIRName converts a go/types type string to a short Go type name.
// goTypeToIR converts a go/types type string to an IRType, handling pointer types.
func goTypeToIR(goType string) IRType {
	if strings.HasPrefix(goType, "*") {
		return IRPointerType{Inner: IRNamedType{GoName: goTypeToIRName(goType[1:])}}
	}
	return IRNamedType{GoName: goTypeToIRName(goType)}
}

// goTypeToIRName converts a go/types type string to a short Go type name.
// "net/http.ResponseWriter" → "http.ResponseWriter"
// "github.com/labstack/echo/v5.Echo" → "echo.Echo"
// Pointer types should be handled by goTypeToIR before calling this.
func goTypeToIRName(goType string) string {
	dotIdx := strings.LastIndex(goType, ".")
	if dotIdx < 0 {
		return goType // no dot = primitive type ("int", "string", etc.)
	}
	pkgPath := goType[:dotIdx]
	typeName := goType[dotIdx+1:]
	shortPkg := NewGoPackage(pkgPath).ShortName
	return shortPkg + "." + typeName
}

// resolveReceiverGoType extracts the Go package and type name from an IR expression's type.
func (l *Lowerer) resolveReceiverGoType(expr IRExpr) (pkg, typ string, ok bool) {
	irType := expr.irType()
	if irType == nil {
		return "", "", false
	}
	named, isNamed := irType.(IRNamedType)
	if !isNamed {
		if ptr, isPtr := irType.(IRPointerType); isPtr {
			if inner, isInner := ptr.Inner.(IRNamedType); isInner {
				named = inner
				isNamed = true
			}
		}
	}
	if !isNamed || !strings.Contains(named.GoName, ".") {
		return "", "", false
	}
	parts := strings.SplitN(named.GoName, ".", 2)
	if goPkg, exists := l.goPackages[parts[0]]; exists {
		return goPkg.FullPath, parts[1], true
	}
	return "", "", false
}

// resolveMethodReturnType resolves the return type of a method call on a Go or Arca type.
func (l *Lowerer) resolveMethodReturnType(receiver IRExpr, method string) goReturnInfo {
	// Try Go FFI type first
	pkg, typ, ok := l.resolveReceiverGoType(receiver)
	if ok {
		info := l.typeResolver.ResolveMethod(pkg, typ, method)
		if info != nil {
			return l.goFuncReturnType(info)
		}
	}

	// Try Arca type
	arcaTypeName := l.resolveReceiverArcaType(receiver)
	if arcaTypeName != "" {
		if td, ok := l.types[arcaTypeName]; ok {
			for _, m := range td.Methods {
				if m.Name == method || snakeToCamel(m.Name) == method || snakeToPascal(m.Name) == method {
					if m.ReturnType != nil {
						return goReturnInfo{Type: l.lowerType(m.ReturnType)}
					}
					return goReturnInfo{Type: IRNamedType{GoName: "struct{}"}}
				}
			}
		}
	}

	return goReturnInfo{Type: IRInterfaceType{}}
}

// resolveFieldType resolves the type of a field access on a Go or Arca type.
func (l *Lowerer) resolveFieldType(receiver IRExpr, field string) IRType {
	// Try Go FFI type first
	pkg, typ, ok := l.resolveReceiverGoType(receiver)
	if ok {
		typeInfo := l.typeResolver.ResolveType(pkg, typ)
		if typeInfo != nil {
			for _, f := range typeInfo.Fields {
				if f.Name == field {
					return IRNamedType{GoName: goTypeToIRName(f.Type)}
				}
			}
		}
	}

	// Try Arca type
	arcaTypeName := l.resolveReceiverArcaType(receiver)
	if arcaTypeName != "" {
		if td, ok := l.types[arcaTypeName]; ok {
			// field is capitalized Go name, findField uses Arca name
			for _, ctor := range td.Constructors {
				for i, f := range ctor.Fields {
					if capitalize(f.Name) == field {
						return l.lowerType(ctor.Fields[i].Type)
					}
				}
			}
		}
	}

	return IRInterfaceType{}
}

// resolveReceiverArcaType extracts the Arca type name from an IR expression.
func (l *Lowerer) resolveReceiverArcaType(expr IRExpr) string {
	irType := expr.irType()
	if irType == nil {
		return ""
	}
	if named, ok := irType.(IRNamedType); ok {
		if !strings.Contains(named.GoName, ".") {
			return named.GoName
		}
	}
	return ""
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

	// Lambda with missing parameter types: infer from call context
	if lam, ok := expr.(Lambda); ok {
		if paramTypes := l.resolveCallParamFuncType(call, argIndex); paramTypes != nil {
			lam = l.inferLambdaParamTypes(lam, paramTypes)
		}
		return l.lowerLambda(lam)
	}

	// Resolve expected type for hint-based type checking
	var hint IRType
	if fnIdent, ok := call.Fn.(Ident); ok {
		if fn, ok := l.functions[fnIdent.Name]; ok && argIndex < len(fn.Params) {
			hint = l.lowerType(fn.Params[argIndex].Type)
		}
	}
	return l.lowerExprHint(expr, hint)
}

// resolveCallParamFuncType resolves the Go function type for a parameter at argIndex.
// Returns the FuncInfo if the parameter is a function type, nil otherwise.
func (l *Lowerer) resolveCallParamFuncType(call FnCall, argIndex int) *FuncInfo {
	if fa, ok := call.Fn.(FieldAccess); ok {
		// Method call: resolve receiver type → method signature → param type
		if ident, ok := fa.Expr.(Ident); ok {
			if _, isGoPkg := l.goPackages[ident.Name]; isGoPkg {
				// Go FFI package function
				if goPkg, ok := l.goPackages[ident.Name]; ok {
					if info := l.typeResolver.ResolveFunc(goPkg.FullPath, fa.Field); info != nil {
						if argIndex < len(info.Params) {
							return l.parseFuncType(info.Params[argIndex].Type)
						}
					}
				}
				return nil
			}
		}
		// Method on receiver
		receiver := l.lowerExpr(fa.Expr)
		pkg, typ, ok := l.resolveReceiverGoType(receiver)
		if ok {
			if info := l.typeResolver.ResolveMethod(pkg, typ, fa.Field); info != nil {
				if argIndex < len(info.Params) {
					return l.parseFuncType(info.Params[argIndex].Type)
				}
			}
		}
	}
	return nil
}

// parseFuncType parses a Go type string like "func(echo.Context) error" into FuncInfo.
// Also resolves type aliases (e.g. echo.HandlerFunc → func(Context) error).
func (l *Lowerer) parseFuncType(goType string) *FuncInfo {
	if !strings.HasPrefix(goType, "func(") {
		// Try resolving as a type alias with underlying func type
		resolved := l.typeResolver.ResolveUnderlying(goType)
		if resolved != "" && strings.HasPrefix(resolved, "func(") {
			goType = resolved
		} else {
			return nil
		}
	}
	// Use TypeResolver to get detailed function signature
	// For now, extract param types from the string
	// "func(echo.Context) error" → params: ["echo.Context"], results: ["error"]
	inner := goType[5:] // strip "func("
	parenEnd := strings.Index(inner, ")")
	if parenEnd < 0 {
		return nil
	}
	paramStr := inner[:parenEnd]
	resultStr := strings.TrimSpace(inner[parenEnd+1:])

	info := &FuncInfo{}
	if paramStr != "" {
		for _, p := range strings.Split(paramStr, ", ") {
			p = strings.TrimSpace(p)
			// Named params: "c *echo.Context" → type is "*echo.Context"
			if spaceIdx := strings.LastIndex(p, " "); spaceIdx >= 0 {
				p = p[spaceIdx+1:]
			}
			info.Params = append(info.Params, ParamInfo{Type: p})
		}
	}
	if resultStr != "" {
		info.Results = append(info.Results, ParamInfo{Type: resultStr})
	}
	return info
}

// inferLambdaParamTypes fills in missing parameter types from a Go function signature.
func (l *Lowerer) inferLambdaParamTypes(lam Lambda, funcType *FuncInfo) Lambda {
	for i := range lam.Params {
		if lam.Params[i].Type == nil && i < len(funcType.Params) {
			goType := funcType.Params[i].Type
			lam.Params[i].Type = l.goTypeToArcaType(goType)
		}
	}
	return lam
}

// goTypeToArcaType converts a Go type string to an Arca AST type.
func (l *Lowerer) goTypeToArcaType(goType string) Type {
	if strings.HasPrefix(goType, "*") {
		inner := l.goTypeToArcaType(goType[1:])
		if inner != nil {
			return PointerType{Inner: inner}
		}
		return nil
	}
	return NamedType{Name: goTypeToIRName(goType)}
}

func (l *Lowerer) lowerFieldAccess(e FieldAccess) IRExpr {
	receiver := l.lowerExpr(e.Expr)
	fieldType := l.resolveFieldType(receiver, capitalize(e.Field))
	return IRFieldAccess{
		Expr:  receiver,
		Field: capitalize(e.Field),
		Type:  fieldType,
	}
}

func (l *Lowerer) lowerConstructorCall(e ConstructorCall) IRExpr {
	// Built-in Result constructors
	if e.Name == "Ok" && len(e.Fields) == 1 {
		l.builtins["result"] = true
		val := l.lowerExpr(e.Fields[0].Value)
		typeArgs := l.resultTypeArgs()
		okType := val.irType()
		return IROkCall{Value: val, TypeArgs: typeArgs, Type: IRResultType{Ok: okType, Err: IRNamedType{GoName: "error"}}}
	}
	if e.Name == "Error" && len(e.Fields) == 1 {
		l.builtins["result"] = true
		val := l.lowerExpr(e.Fields[0].Value)
		typeArgs := l.resultTypeArgs()
		errType := val.irType()
		return IRErrorCall{Value: val, TypeArgs: typeArgs, Type: IRResultType{Ok: IRInterfaceType{}, Err: errType}}
	}
	// Built-in Option constructors
	if e.Name == "Some" && len(e.Fields) == 1 {
		l.builtins["option"] = true
		val := l.lowerExpr(e.Fields[0].Value)
		return IRSomeCall{Value: val, Type: IROptionType{Inner: val.irType()}}
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

		// Constrained type constructor: NewType returns (T, error)
		if l.hasConstraints(td) {
			l.builtins["fmt"] = true
			fields := l.lowerFieldValues(cc.Fields)
			return IRConstructorCall{
				GoName:        "New" + goName,
				Fields:        fields,
				GoMultiReturn: true,
				Type:          IRResultType{Ok: IRNamedType{GoName: goName}, Err: IRNamedType{GoName: "error"}},
				Source:        SourceInfo{Pos: cc.Pos, Name: cc.Name, TypeName: typeName},
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
			Source:   SourceInfo{Pos: cc.Pos, Name: cc.Name, TypeName: typeName},
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
				GoMultiReturn: true,
				Type:          IRResultType{Ok: IRNamedType{GoName: cc.Name}, Err: IRNamedType{GoName: "error"}},
				Source:        SourceInfo{Pos: cc.Pos, Name: cc.Name, TypeName: cc.Name},
			}
		}
		// Unconstrained alias: simple type conversion
		return IRConstructorCall{
			GoName: cc.Name,
			Fields: fields,
			Type:   IRNamedType{GoName: cc.Name},
			Source: SourceInfo{Pos: cc.Pos, Name: cc.Name, TypeName: cc.Name},
		}
	}

	// Unknown constructor
	return IRConstructorCall{
		GoName: cc.Name,
		Fields: l.lowerFieldValues(cc.Fields),
		Type:   IRInterfaceType{},
		Source: SourceInfo{Pos: cc.Pos, Name: cc.Name},
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
			Source: SourceInfo{Name: f.Name},
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

func (l *Lowerer) lowerLambdaHint(lam Lambda, hint IRType) IRExpr {
	return l.lowerLambda(lam)
}

func (l *Lowerer) lowerLambda(lam Lambda) IRExpr {
	params := make([]IRParamDecl, len(lam.Params))
	var lamSymbols []SymbolRegInfo
	for i, p := range lam.Params {
		var typ IRType
		if p.Type != nil {
			typ = l.lowerType(p.Type)
		}
		params[i] = IRParamDecl{GoName: p.Name, Type: typ}
		lamSymbols = append(lamSymbols, SymbolRegInfo{
			Name:   p.Name,
			IRType: typ,
			Kind:   SymParameter,
		})
	}
	var retType IRType
	if lam.ReturnType != nil {
		retType = l.lowerType(lam.ReturnType)
	}
	sp, ep := bodyPos(lam.Body)
	var body IRExpr
	l.withScope(sp, ep, lamSymbols, func() {
		body = l.lowerExpr(lam.Body)
	})
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
	forSymbols := []SymbolRegInfo{{Name: fe.Binding, Kind: SymVariable}}
	sp, ep := bodyPos(fe.Body)

	if rangeExpr, ok := fe.Iter.(RangeExpr); ok {
		var body IRExpr
		l.withScope(sp, ep, forSymbols, func() {
			body = l.lowerExpr(fe.Body)
		})
		return IRForRange{
			Binding: binding,
			Start:   l.lowerExpr(rangeExpr.Start),
			End:     l.lowerExpr(rangeExpr.End),
			Body:    body,
			Type:    IRNamedType{GoName: "struct{}"},
		}
	}

	var body IRExpr
	l.withScope(sp, ep, forSymbols, func() {
		body = l.lowerExpr(fe.Body)
	})
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
		// Try operator in expression statement: expr? → let _ = expr?
		if call, ok := s.Expr.(FnCall); ok {
			if ident, ok := call.Fn.(Ident); ok && ident.Name == "__try" && len(call.Args) == 1 {
				loweredExpr := l.lowerExpr(call.Args[0])
				var retType IRType
				if l.currentRetType != nil {
					retType = l.lowerType(l.currentRetType)
				}
				if isIRResultType(retType) {
					l.builtins["result"] = true
				}
				return []IRStmt{IRTryLetStmt{
					GoName:     "_",
					CallExpr:   loweredExpr,
					ReturnType: retType,
				}}
			}
		}
		expr := l.lowerExpr(s.Expr)
		expr = l.markVoidContext(expr)
		return []IRStmt{IRExprStmt{Expr: expr}}
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
				// Try unwraps Result: the variable gets the Ok type
				var irType IRType
				if rt, ok := loweredExpr.irType().(IRResultType); ok {
					irType = rt.Ok
				}
				goVarName = l.registerSymbol(SymbolRegInfo{
					Name:     s.Name,
					ArcaType: l.inferASTType(call.Args[0]),
					IRType:   irType,
					Kind:     SymVariable,
				})
			}
			var retType IRType
			if l.currentRetType != nil {
				retType = l.lowerType(l.currentRetType)
			}
			if isIRResultType(retType) {
				l.builtins["result"] = true
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
	loweredValue := l.lowerExpr(s.Value)

	// GoMultiReturn calls that return Result need builtins
	if isGoMultiReturn(loweredValue) {
		if _, ok := loweredValue.irType().(IRResultType); ok {
			l.builtins["result"] = true
		}
		if _, ok := loweredValue.irType().(IROptionType); ok {
			l.builtins["option"] = true
		}
	}
	var loweredType IRType
	if s.Type != nil {
		loweredType = l.lowerType(s.Type)
	}
	var arcaType Type
	if s.Type != nil {
		arcaType = s.Type
	} else {
		arcaType = l.inferASTType(s.Value)
	}
	goVarName := l.registerSymbol(SymbolRegInfo{
		Name:     s.Name,
		ArcaType: arcaType,
		IRType:   loweredValue.irType(),
		Kind:     SymVariable,
	})

	return []IRStmt{IRLetStmt{
		GoName: goVarName,
		Value:  loweredValue,
		Type:   loweredType,
	}}
}

// inferASTType infers the Arca AST type of an expression (for symbol recording).
func (l *Lowerer) inferASTType(expr Expr) Type {
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
	case ConstructorCall:
		if e.TypeName != "" {
			return NamedType{Name: e.TypeName}
		}
		if typeName, ok := l.ctorTypes[e.Name]; ok {
			return NamedType{Name: typeName}
		}
		if _, ok := l.typeAliases[e.Name]; ok {
			return NamedType{Name: e.Name}
		}
	case FnCall:
		if ident, ok := e.Fn.(Ident); ok {
			if fn, ok := l.functions[ident.Name]; ok {
				return fn.ReturnType
			}
		}
	case ListLit:
		if len(e.Elements) > 0 {
			elemType := l.inferASTType(e.Elements[0])
			if elemType != nil {
				return NamedType{Name: "List", Params: []Type{elemType}}
			}
		}
		return NamedType{Name: "List"}
	}
	return nil
}

func (l *Lowerer) lowerLetDestructure(pat Pattern, value Expr) []IRStmt {
	switch p := pat.(type) {
	case TuplePattern:
		var bindings []IRDestructureBinding
		for i, elemPat := range p.Elements {
			if bp, ok := elemPat.(BindPattern); ok {
				l.registerSymbol(SymbolRegInfo{Name: bp.Name, Kind: SymVariable})
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
				l.registerSymbol(SymbolRegInfo{Name: bp.Name, Kind: SymVariable})
				bindings = append(bindings, IRDestructureBinding{
					GoName: snakeToCamel(bp.Name),
					Index:  i,
				})
			}
		}
		if p.Rest != "" {
			l.registerSymbol(SymbolRegInfo{Name: p.Rest, Kind: SymVariable})
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
	var arms []IRMatchArm

	for _, arm := range me.Arms {
		cp, ok := arm.Pattern.(ConstructorPattern)
		if !ok {
			continue
		}
		usedVars := collectUsedIdents(arm.Body)
		if cp.Name == "Ok" {
			var binding *IRBinding
			var armSymbols []SymbolRegInfo
			if len(cp.Fields) > 0 {
				if _, used := usedVars[cp.Fields[0].Binding]; used {
					binding = &IRBinding{
						GoName: snakeToCamel(cp.Fields[0].Binding),
						Source: ".Value",
					}
				}
				rt, ok := subject.irType().(IRResultType)
				if !ok {
					l.addError(me.Pos, "cannot infer Result type for match subject")
				} else {
					armSymbols = append(armSymbols, SymbolRegInfo{
						Name: cp.Fields[0].Binding, IRType: rt.Ok, Kind: SymVariable,
					})
				}
			}
			sp, ep := arm.Pos, arm.EndPos
			var body IRExpr
			l.withScope(sp, ep, armSymbols, func() {
				body = l.lowerExpr(arm.Body)
			})
			arms = append(arms, IRMatchArm{Pattern: IRResultOkPattern{Binding: binding}, Body: body})
		}
		if cp.Name == "Error" {
			var binding *IRBinding
			var armSymbols []SymbolRegInfo
			if len(cp.Fields) > 0 {
				if _, used := usedVars[cp.Fields[0].Binding]; used {
					binding = &IRBinding{
						GoName: snakeToCamel(cp.Fields[0].Binding),
						Source: ".Err",
					}
				}
				rt, ok := subject.irType().(IRResultType)
				if !ok {
					l.addError(me.Pos, "cannot infer Result type for match subject")
				} else {
					armSymbols = append(armSymbols, SymbolRegInfo{
						Name: cp.Fields[0].Binding, IRType: rt.Err, Kind: SymVariable,
					})
				}
			}
			sp, ep := arm.Pos, arm.EndPos
			var body IRExpr
			l.withScope(sp, ep, armSymbols, func() {
				body = l.lowerExpr(arm.Body)
			})
			arms = append(arms, IRMatchArm{Pattern: IRResultErrorPattern{Binding: binding}, Body: body})
		}
	}

	return IRMatch{Subject: subject, Arms: arms, Type: IRInterfaceType{}, Pos: me.Pos}
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
	var arms []IRMatchArm

	for _, arm := range me.Arms {
		cp, ok := arm.Pattern.(ConstructorPattern)
		if !ok {
			continue
		}
		if cp.Name == "Some" {
			var binding *IRBinding
			var armSymbols []SymbolRegInfo
			if len(cp.Fields) > 0 {
				binding = &IRBinding{
					GoName: snakeToCamel(cp.Fields[0].Binding),
					Source: ".Value",
				}
				ot, ok := subject.irType().(IROptionType)
				if !ok {
					l.addError(me.Pos, "cannot infer Option type for match subject")
				} else {
					armSymbols = append(armSymbols, SymbolRegInfo{
						Name: cp.Fields[0].Binding, IRType: ot.Inner, Kind: SymVariable,
					})
				}
			}
			sp, ep := arm.Pos, arm.EndPos
			var body IRExpr
			l.withScope(sp, ep, armSymbols, func() {
				body = l.lowerExpr(arm.Body)
			})
			arms = append(arms, IRMatchArm{Pattern: IROptionSomePattern{Binding: binding}, Body: body})
		}
		if cp.Name == "None" {
			sp, ep := arm.Pos, arm.EndPos
			var body IRExpr
			l.withScope(sp, ep, nil, func() {
				body = l.lowerExpr(arm.Body)
			})
			arms = append(arms, IRMatchArm{Pattern: IROptionNonePattern{}, Body: body})
		}
	}

	return IRMatch{Subject: subject, Arms: arms, Type: IRInterfaceType{}, Pos: me.Pos}
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
	var arms []IRMatchArm

	for _, arm := range me.Arms {
		lp, ok := arm.Pattern.(ListPattern)
		if !ok {
			// Wildcard / bind default arm
			sp, ep := arm.Pos, arm.EndPos
			var body IRExpr
			l.withScope(sp, ep, nil, func() {
				body = l.lowerExpr(arm.Body)
			})
			arms = append(arms, IRMatchArm{Pattern: IRListDefaultPattern{}, Body: body})
			continue
		}

		if len(lp.Elements) == 0 && lp.Rest == "" {
			sp, ep := arm.Pos, arm.EndPos
			var body IRExpr
			l.withScope(sp, ep, nil, func() {
				body = l.lowerExpr(arm.Body)
			})
			arms = append(arms, IRMatchArm{Pattern: IRListEmptyPattern{}, Body: body})
			continue
		}

		// Collect arm symbols from list pattern bindings
		var armSymbols []SymbolRegInfo
		for _, elemPat := range lp.Elements {
			if bp, ok := elemPat.(BindPattern); ok {
				armSymbols = append(armSymbols, SymbolRegInfo{
					Name: bp.Name, Kind: SymVariable,
				})
			}
		}
		if lp.Rest != "" {
			armSymbols = append(armSymbols, SymbolRegInfo{
				Name: lp.Rest, Kind: SymVariable,
			})
		}

		sp, ep := arm.Pos, arm.EndPos
		var body IRExpr
		l.withScope(sp, ep, armSymbols, func() {
			body = l.lowerExpr(arm.Body)
		})

		usedVars := collectUsedIdents(arm.Body)
		var bindings []IRBinding
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

		if lp.Rest != "" {
			arms = append(arms, IRMatchArm{
				Pattern: IRListConsPattern{Elements: bindings, Rest: rest, MinLen: len(lp.Elements)},
				Body:    body,
			})
		} else {
			arms = append(arms, IRMatchArm{
				Pattern: IRListExactPattern{Elements: bindings, MinLen: len(lp.Elements)},
				Body:    body,
			})
		}
	}

	return IRMatch{Subject: subject, Arms: arms, Type: IRInterfaceType{}, Pos: me.Pos}
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
	var arms []IRMatchArm

	for _, arm := range me.Arms {
		body := l.lowerExpr(arm.Body)
		switch p := arm.Pattern.(type) {
		case LitPattern:
			arms = append(arms, IRMatchArm{Pattern: IRLiteralPattern{Value: l.litPatternGoStr(p)}, Body: body})
		case WildcardPattern:
			arms = append(arms, IRMatchArm{Pattern: IRLiteralDefaultPattern{}, Body: body})
		case BindPattern:
			arms = append(arms, IRMatchArm{Pattern: IRLiteralDefaultPattern{}, Body: body})
		}
	}

	return IRMatch{Subject: subject, Arms: arms, Type: IRInterfaceType{}, Pos: me.Pos}
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
	var arms []IRMatchArm

	for _, arm := range me.Arms {
		body := l.lowerExpr(arm.Body)
		if cp, ok := arm.Pattern.(ConstructorPattern); ok {
			typeName := l.findTypeName(cp.Name)
			arms = append(arms, IRMatchArm{Pattern: IREnumPattern{GoValue: typeName + cp.Name}, Body: body})
		} else {
			arms = append(arms, IRMatchArm{Pattern: IRWildcardPattern{}, Body: body})
		}
	}

	return IRMatch{Subject: subject, Arms: arms, Type: IRInterfaceType{}, Pos: me.Pos}
}

func (l *Lowerer) lowerSumTypeMatch(me MatchExpr) IRExpr {
	subject := l.lowerExpr(me.Subject)
	var arms []IRMatchArm

	for _, arm := range me.Arms {
		switch pat := arm.Pattern.(type) {
		case ConstructorPattern:
			typeName := l.findTypeName(pat.Name)
			variantName := typeName + pat.Name

			var ctorFields []Field
			if td, ok := l.types[typeName]; ok {
				for _, c := range td.Constructors {
					if c.Name == pat.Name {
						ctorFields = c.Fields
						break
					}
				}
			}

			var armSymbols []SymbolRegInfo
			for i, fp := range pat.Fields {
				if i < len(ctorFields) {
					armSymbols = append(armSymbols, SymbolRegInfo{
						Name:     fp.Binding,
						ArcaType: ctorFields[i].Type,
						Kind:     SymVariable,
					})
				}
			}

			sp, ep := arm.Pos, arm.EndPos
			var body IRExpr
			l.withScope(sp, ep, armSymbols, func() {
				body = l.lowerExpr(arm.Body)
			})
			usedVars := collectUsedIdents(arm.Body)

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

			arms = append(arms, IRMatchArm{
				Pattern: IRSumTypePattern{GoType: variantName, Bindings: bindings},
				Body:    body,
			})
		case WildcardPattern:
			sp, ep := arm.Pos, arm.EndPos
			var body IRExpr
			l.withScope(sp, ep, nil, func() {
				body = l.lowerExpr(arm.Body)
			})
			arms = append(arms, IRMatchArm{Pattern: IRSumTypeWildcardPattern{}, Body: body})
		case BindPattern:
			body := l.lowerExpr(arm.Body)
			arms = append(arms, IRMatchArm{
				Pattern: IRSumTypeWildcardPattern{Binding: &IRBinding{GoName: snakeToCamel(pat.Name)}},
				Body:    body,
			})
		}
	}

	return IRMatch{Subject: subject, Arms: arms, Type: IRInterfaceType{}, Pos: me.Pos}
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
