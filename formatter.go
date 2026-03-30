package main

import (
	"fmt"
	"strings"
)

type Formatter struct {
	buf    strings.Builder
	indent int
}

func NewFormatter() *Formatter {
	return &Formatter{}
}

func (f *Formatter) Format(prog *Program) string {
	for i, decl := range prog.Decls {
		if i > 0 {
			f.writeln("")
		}
		f.formatDecl(decl)
	}
	return f.buf.String()
}

func (f *Formatter) write(s string) {
	f.buf.WriteString(s)
}

func (f *Formatter) writeln(s string) {
	f.buf.WriteString(s)
	f.buf.WriteString("\n")
}

func (f *Formatter) writeIndent() {
	for i := 0; i < f.indent; i++ {
		f.write("  ")
	}
}

func (f *Formatter) formatDecl(decl Decl) {
	switch d := decl.(type) {
	case ImportDecl:
		f.formatImport(d)
	case TypeDecl:
		f.formatTypeDecl(d)
	case FnDecl:
		f.formatFnDecl(d)
	}
}

func (f *Formatter) formatImport(d ImportDecl) {
	if strings.HasPrefix(d.Path, "go/") {
		goPath := d.Path[3:]
		if d.SideEffect {
			f.writeln(fmt.Sprintf("import go _ %q", goPath))
		} else {
			f.writeln(fmt.Sprintf("import go %q", goPath))
		}
	} else {
		f.writeln("import " + d.Path)
	}
}

func (f *Formatter) formatTypeDecl(d TypeDecl) {
	// Short record form: type Name(fields...)
	if len(d.Constructors) == 1 && d.Constructors[0].Name == d.Name && len(d.Methods) == 0 {
		ctor := d.Constructors[0]
		f.write("type " + d.Name + "(")
		for i, field := range ctor.Fields {
			if i > 0 {
				f.write(", ")
			}
			f.write(field.Name + ": " + f.formatType(field.Type))
		}
		f.writeln(")")
		return
	}

	f.writeln("type " + d.Name + " {")
	f.indent++
	for _, ctor := range d.Constructors {
		f.writeIndent()
		f.write(ctor.Name)
		if len(ctor.Fields) > 0 {
			f.write("(")
			for i, field := range ctor.Fields {
				if i > 0 {
					f.write(", ")
				}
				f.write(field.Name + ": " + f.formatType(field.Type))
			}
			f.write(")")
		}
		f.writeln("")
	}
	if len(d.Methods) > 0 {
		f.writeln("")
		for _, method := range d.Methods {
			f.writeIndent()
			if method.Public {
				f.write("pub ")
			}
			f.write("fun " + method.Name + "(")
			for i, p := range method.Params {
				if i > 0 {
					f.write(", ")
				}
				f.write(p.Name + ": " + f.formatType(p.Type))
			}
			f.write(")")
			if method.ReturnType != nil {
				f.write(" -> " + f.formatType(method.ReturnType))
			}
			f.writeln(" {")
			f.indent++
			f.formatBody(method.Body)
			f.indent--
			f.writeIndent()
			f.writeln("}")
		}
	}
	f.indent--
	f.writeln("}")
}

func (f *Formatter) formatType(t Type) string {
	switch tt := t.(type) {
	case NamedType:
		if len(tt.Params) > 0 {
			params := make([]string, len(tt.Params))
			for i, p := range tt.Params {
				params[i] = f.formatType(p)
			}
			return tt.Name + "[" + strings.Join(params, ", ") + "]"
		}
		return tt.Name
	case PointerType:
		return "*" + f.formatType(tt.Inner)
	case TupleType:
		elems := make([]string, len(tt.Elements))
		for i, e := range tt.Elements {
			elems[i] = f.formatType(e)
		}
		return "(" + strings.Join(elems, ", ") + ")"
	default:
		return "?"
	}
}

func (f *Formatter) formatFnDecl(d FnDecl) {
	if d.Public {
		f.write("pub ")
	}
	f.write("fun " + d.Name + "(")
	for i, p := range d.Params {
		if i > 0 {
			f.write(", ")
		}
		f.write(p.Name + ": " + f.formatType(p.Type))
	}
	f.write(")")
	if d.ReturnType != nil {
		f.write(" -> " + f.formatType(d.ReturnType))
	}
	f.writeln(" {")
	f.indent++
	f.formatBody(d.Body)
	f.indent--
	f.writeln("}")
}

func (f *Formatter) formatBody(expr Expr) {
	switch e := expr.(type) {
	case Block:
		for _, stmt := range e.Stmts {
			f.formatStmt(stmt)
		}
		if e.Expr != nil {
			f.writeIndent()
			f.writeln(f.formatExpr(e.Expr))
		}
	default:
		f.writeIndent()
		f.writeln(f.formatExpr(expr))
	}
}

func (f *Formatter) formatStmt(stmt Stmt) {
	switch s := stmt.(type) {
	case LetStmt:
		f.writeIndent()
		if s.Pattern != nil {
			f.writeln("let " + f.formatPattern(s.Pattern) + " = " + f.formatExpr(s.Value))
		} else {
			f.writeln("let " + s.Name + " = " + f.formatExpr(s.Value))
		}
	case DeferStmt:
		f.writeIndent()
		f.writeln("defer " + f.formatExpr(s.Expr))
	case AssertStmt:
		f.writeIndent()
		f.writeln("assert " + f.formatExpr(s.Expr))
	case ExprStmt:
		f.writeIndent()
		switch e := s.Expr.(type) {
		case ForExpr:
			f.formatForExpr(e)
		default:
			f.writeln(f.formatExpr(s.Expr))
		}
	}
}

func (f *Formatter) formatForExpr(e ForExpr) {
	f.writeln("for " + e.Binding + " in " + f.formatExpr(e.Iter) + " {")
	f.indent++
	f.formatBody(e.Body)
	f.indent--
	f.writeIndent()
	f.writeln("}")
}

func (f *Formatter) formatExpr(expr Expr) string {
	switch e := expr.(type) {
	case IntLit:
		return fmt.Sprintf("%d", e.Value)
	case FloatLit:
		return fmt.Sprintf("%g", e.Value)
	case StringLit:
		return fmt.Sprintf("%q", e.Value)
	case StringInterp:
		return f.formatStringInterp(e)
	case BoolLit:
		if e.Value {
			return "True"
		}
		return "False"
	case Ident:
		return e.Name
	case FnCall:
		args := make([]string, len(e.Args))
		for i, a := range e.Args {
			args[i] = f.formatExpr(a)
		}
		return f.formatExpr(e.Fn) + "(" + strings.Join(args, ", ") + ")"
	case FieldAccess:
		return f.formatExpr(e.Expr) + "." + e.Field
	case ConstructorCall:
		if len(e.Fields) == 0 {
			return e.Name
		}
		fields := make([]string, len(e.Fields))
		for i, fv := range e.Fields {
			if fv.Name != "" {
				fields[i] = fv.Name + ": " + f.formatExpr(fv.Value)
			} else {
				fields[i] = f.formatExpr(fv.Value)
			}
		}
		return e.Name + "(" + strings.Join(fields, ", ") + ")"
	case MatchExpr:
		return f.formatMatchExpr(e)
	case Lambda:
		return f.formatLambda(e)
	case BinaryExpr:
		return f.formatExpr(e.Left) + " " + e.Op + " " + f.formatExpr(e.Right)
	case ListLit:
		return f.formatListLit(e)
	case TupleExpr:
		elems := make([]string, len(e.Elements))
		for i, el := range e.Elements {
			elems[i] = f.formatExpr(el)
		}
		return "(" + strings.Join(elems, ", ") + ")"
	case RangeExpr:
		return f.formatExpr(e.Start) + ".." + f.formatExpr(e.End)
	default:
		return "/* ? */"
	}
}

func (f *Formatter) formatStringInterp(si StringInterp) string {
	var buf strings.Builder
	buf.WriteRune('"')
	for _, part := range si.Parts {
		if lit, ok := part.(StringLit); ok {
			buf.WriteString(lit.Value)
		} else {
			buf.WriteString("${")
			buf.WriteString(f.formatExpr(part))
			buf.WriteString("}")
		}
	}
	buf.WriteRune('"')
	return buf.String()
}

func (f *Formatter) formatMatchExpr(me MatchExpr) string {
	var buf strings.Builder
	buf.WriteString("match " + f.formatExpr(me.Subject) + " {\n")
	f.indent++
	for _, arm := range me.Arms {
		buf.WriteString(strings.Repeat("  ", f.indent))
		buf.WriteString(f.formatPattern(arm.Pattern) + " -> " + f.formatExpr(arm.Body) + "\n")
	}
	f.indent--
	buf.WriteString(strings.Repeat("  ", f.indent) + "}")
	return buf.String()
}

func (f *Formatter) formatLambda(l Lambda) string {
	params := make([]string, len(l.Params))
	for i, p := range l.Params {
		if p.Type != nil {
			params[i] = p.Name + ": " + f.formatType(p.Type)
		} else {
			params[i] = p.Name
		}
	}
	ret := ""
	if l.ReturnType != nil {
		ret = " -> " + f.formatType(l.ReturnType)
	}
	return "(" + strings.Join(params, ", ") + ")" + ret + " => " + f.formatExpr(l.Body)
}

func (f *Formatter) formatListLit(l ListLit) string {
	if len(l.Elements) == 0 && l.Spread == nil {
		return "[]"
	}
	elems := make([]string, len(l.Elements))
	for i, e := range l.Elements {
		elems[i] = f.formatExpr(e)
	}
	if l.Spread != nil {
		elems = append(elems, ".."+f.formatExpr(l.Spread))
	}
	return "[" + strings.Join(elems, ", ") + "]"
}

func (f *Formatter) formatPattern(pat Pattern) string {
	switch p := pat.(type) {
	case ConstructorPattern:
		if len(p.Fields) == 0 {
			return p.Name
		}
		fields := make([]string, len(p.Fields))
		for i, fp := range p.Fields {
			fields[i] = fp.Binding
		}
		return p.Name + "(" + strings.Join(fields, ", ") + ")"
	case WildcardPattern:
		return "_"
	case BindPattern:
		return p.Name
	case LitPattern:
		return f.formatExpr(p.Expr)
	case ListPattern:
		if len(p.Elements) == 0 && p.Rest == "" {
			return "[]"
		}
		elems := make([]string, len(p.Elements))
		for i, ep := range p.Elements {
			elems[i] = f.formatPattern(ep)
		}
		if p.Rest != "" {
			elems = append(elems, ".."+p.Rest)
		}
		return "[" + strings.Join(elems, ", ") + "]"
	case TuplePattern:
		elems := make([]string, len(p.Elements))
		for i, ep := range p.Elements {
			elems[i] = f.formatPattern(ep)
		}
		return "(" + strings.Join(elems, ", ") + ")"
	default:
		return "?"
	}
}
