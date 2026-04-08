package main

import (
	"fmt"
	"strings"
)

// GoWriter provides structured Go code generation with automatic indentation.
// Replaces manual fmt.Sprintf + writeln for Go code output.
type GoWriter struct {
	buf    strings.Builder
	indent int
}

func NewGoWriter() *GoWriter {
	return &GoWriter{}
}

func (w *GoWriter) String() string {
	return w.buf.String()
}

// --- Basic output ---

func (w *GoWriter) Line(format string, args ...any) {
	if format == "" {
		w.buf.WriteString("\n")
		return
	}
	w.writeIndent()
	fmt.Fprintf(&w.buf, format, args...)
	w.buf.WriteString("\n")
}

func (w *GoWriter) Raw(s string) {
	w.buf.WriteString(s)
}

// --- Blocks ---

func (w *GoWriter) Block(body func()) {
	w.buf.WriteString("{\n")
	w.indent++
	body()
	w.indent--
	w.writeIndent()
	w.buf.WriteString("}")
}

func (w *GoWriter) BlockLn(body func()) {
	w.Block(body)
	w.buf.WriteString("\n")
}

// --- Control flow ---

func (w *GoWriter) If(cond string, body func()) {
	w.writeIndent()
	fmt.Fprintf(&w.buf, "if %s ", cond)
	w.BlockLn(body)
}

func (w *GoWriter) IfElse(cond string, ifBody, elseBody func()) {
	w.writeIndent()
	fmt.Fprintf(&w.buf, "if %s ", cond)
	w.Block(ifBody)
	w.buf.WriteString(" else ")
	w.BlockLn(elseBody)
}

func (w *GoWriter) Switch(expr string, body func()) {
	w.writeIndent()
	fmt.Fprintf(&w.buf, "switch %s ", expr)
	w.BlockLn(body)
}

func (w *GoWriter) SwitchType(varName, expr string, body func()) {
	w.writeIndent()
	fmt.Fprintf(&w.buf, "switch %s := %s.(type) ", varName, expr)
	w.BlockLn(body)
}

func (w *GoWriter) Case(label string, body func()) {
	w.writeIndent()
	fmt.Fprintf(&w.buf, "case %s:\n", label)
	w.indent++
	body()
	w.indent--
}

func (w *GoWriter) Default(body func()) {
	w.writeIndent()
	w.buf.WriteString("default:\n")
	w.indent++
	body()
	w.indent--
}

func (w *GoWriter) For(expr string, body func()) {
	w.writeIndent()
	fmt.Fprintf(&w.buf, "for %s ", expr)
	w.BlockLn(body)
}

// --- Functions ---

func (w *GoWriter) Func(name, params, ret string, body func()) {
	w.writeIndent()
	if ret != "" {
		fmt.Fprintf(&w.buf, "func %s(%s) %s ", name, params, ret)
	} else {
		fmt.Fprintf(&w.buf, "func %s(%s) ", name, params)
	}
	w.BlockLn(body)
}

func (w *GoWriter) Method(receiver, name, params, ret string, body func()) {
	w.writeIndent()
	if ret != "" {
		fmt.Fprintf(&w.buf, "func (%s) %s(%s) %s ", receiver, name, params, ret)
	} else {
		fmt.Fprintf(&w.buf, "func (%s) %s(%s) ", receiver, name, params)
	}
	w.BlockLn(body)
}

// --- Statements ---

func (w *GoWriter) Return(expr string) {
	w.writeIndent()
	if expr == "" {
		w.buf.WriteString("return\n")
	} else {
		fmt.Fprintf(&w.buf, "return %s\n", expr)
	}
}

func (w *GoWriter) Assign(name, expr string) {
	w.writeIndent()
	fmt.Fprintf(&w.buf, "%s := %s\n", name, expr)
}

func (w *GoWriter) AssignMulti(names, expr string) {
	w.writeIndent()
	fmt.Fprintf(&w.buf, "%s := %s\n", names, expr)
}

func (w *GoWriter) Var(name, typ string) {
	w.writeIndent()
	fmt.Fprintf(&w.buf, "var %s %s\n", name, typ)
}

func (w *GoWriter) VarAssign(name, typ, expr string) {
	w.writeIndent()
	fmt.Fprintf(&w.buf, "var %s %s = %s\n", name, typ, expr)
}

func (w *GoWriter) Stmt(expr string) {
	w.writeIndent()
	fmt.Fprintf(&w.buf, "%s\n", expr)
}

func (w *GoWriter) Defer(expr string) {
	w.writeIndent()
	fmt.Fprintf(&w.buf, "defer %s\n", expr)
}

func (w *GoWriter) Panic(expr string) {
	w.writeIndent()
	fmt.Fprintf(&w.buf, "panic(%s)\n", expr)
}

// --- Type declarations ---

func (w *GoWriter) Struct(name string, body func()) {
	w.writeIndent()
	fmt.Fprintf(&w.buf, "type %s struct ", name)
	w.BlockLn(body)
}

func (w *GoWriter) Interface(name string, body func()) {
	w.writeIndent()
	fmt.Fprintf(&w.buf, "type %s interface ", name)
	w.BlockLn(body)
}

func (w *GoWriter) Field(name, typ, tag string) {
	w.writeIndent()
	if tag != "" {
		fmt.Fprintf(&w.buf, "%s %s %s\n", name, typ, tag)
	} else {
		fmt.Fprintf(&w.buf, "%s %s\n", name, typ)
	}
}

func (w *GoWriter) TypeAlias(name, typ string) {
	w.writeIndent()
	fmt.Fprintf(&w.buf, "type %s %s\n", name, typ)
}

func (w *GoWriter) Const(body func()) {
	w.writeIndent()
	w.buf.WriteString("const ")
	w.BlockLn(body)
}

// --- Internal ---

func (w *GoWriter) writeIndent() {
	for i := 0; i < w.indent; i++ {
		w.buf.WriteString("\t")
	}
}
