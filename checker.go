package main

import (
	"fmt"
	"strings"
)

type CheckError struct {
	Message string
}

func (e CheckError) Error() string {
	return e.Message
}

type Checker struct {
	types     map[string]TypeDecl
	ctorTypes map[string]string // constructor name -> type name
	functions map[string]FnDecl
	errors    []CheckError
}

func NewChecker() *Checker {
	return &Checker{
		types:     make(map[string]TypeDecl),
		ctorTypes: make(map[string]string),
		functions: make(map[string]FnDecl),
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
		case FnDecl:
			c.functions[d.Name] = d
		}
	}

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

func (c *Checker) addError(format string, args ...interface{}) {
	c.errors = append(c.errors, CheckError{Message: fmt.Sprintf(format, args...)})
}

// --- Type Declaration Checks ---

func (c *Checker) checkTypeDecl(td TypeDecl) {
	for _, ctor := range td.Constructors {
		for _, field := range ctor.Fields {
			c.checkTypeExists(field.Type)
		}
	}
}

func (c *Checker) checkTypeExists(t Type) {
	switch tt := t.(type) {
	case NamedType:
		if !c.isKnownType(tt.Name) {
			c.addError("unknown type: %s", tt.Name)
		}
		for _, param := range tt.Params {
			c.checkTypeExists(param)
		}
	case TupleType:
		for _, elem := range tt.Elements {
			c.checkTypeExists(elem)
		}
	}
}

func (c *Checker) isKnownType(name string) bool {
	builtins := map[string]bool{
		"Int": true, "Float": true, "String": true, "Bool": true,
		"List": true, "Option": true, "Result": true,
	}
	if builtins[name] {
		return true
	}
	_, ok := c.types[name]
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
	// Check body
	c.checkExpr(fd.Body)
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
		c.checkExpr(e.Fn)
		for _, arg := range e.Args {
			c.checkExpr(arg)
		}
	case FieldAccess:
		c.checkExpr(e.Expr)
	case Block:
		for _, stmt := range e.Stmts {
			c.checkStmt(stmt)
		}
		c.checkExpr(e.Expr)
	case BinaryExpr:
		c.checkExpr(e.Left)
		c.checkExpr(e.Right)
	case Lambda:
		c.checkExpr(e.Body)
	case ForExpr:
		c.checkExpr(e.Iter)
		c.checkExpr(e.Body)
	case StringInterp:
		for _, part := range e.Parts {
			c.checkExpr(part)
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
	case ExprStmt:
		c.checkExpr(s.Expr)
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

	typeName, ok := c.ctorTypes[cc.Name]
	if !ok {
		c.addError("unknown constructor: %s", cc.Name)
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
		c.addError("constructor %s expects %d fields, got %d", cc.Name, len(ctor.Fields), len(cc.Fields))
		return
	}

	// Check named fields match
	for _, fv := range cc.Fields {
		if fv.Name != "" {
			found := false
			for _, cf := range ctor.Fields {
				if cf.Name == fv.Name {
					found = true
					break
				}
			}
			if !found {
				c.addError("constructor %s has no field named '%s'", cc.Name, fv.Name)
			}
		}
		c.checkExpr(fv.Value)
	}
}

// --- Match Exhaustiveness ---

func (c *Checker) checkMatchExpr(me MatchExpr) {
	for _, arm := range me.Arms {
		c.checkExpr(arm.Body)
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
		return // Can't determine type, skip exhaustiveness check
	}

	td, ok := c.types[matchedType]
	if !ok {
		return
	}

	// Check if there's a wildcard or bind pattern (catches all)
	for _, arm := range me.Arms {
		switch arm.Pattern.(type) {
		case WildcardPattern, BindPattern:
			return // Wildcard covers everything
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
		c.addError("non-exhaustive match on %s: missing %s", matchedType, strings.Join(missing, ", "))
	}
}
