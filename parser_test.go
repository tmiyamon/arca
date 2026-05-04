package main

import (
	"strings"
	"testing"
)

func parseSource(t *testing.T, source string) (*Program, error) {
	t.Helper()
	lexer := NewLexer(source)
	tokens, err := lexer.Tokenize()
	if err != nil {
		t.Fatalf("lexer error: %v", err)
	}
	parser := NewParser(tokens)
	return parser.ParseProgram()
}

func TestParseTraitAndImpl(t *testing.T) {
	t.Parallel()
	prog, err := parseSource(t, `
trait Display {
  fun show() -> String
  fun debug() -> String
}

type User {
  User(name: String)
}

impl User: Display {
  fun show() -> String {
    self.name
  }
  fun debug() -> String {
    self.name
  }
}
`)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	var trait *TraitDecl
	var impl *ImplDecl
	for _, d := range prog.Decls {
		switch dd := d.(type) {
		case TraitDecl:
			trait = &dd
		case ImplDecl:
			impl = &dd
		}
	}
	if trait == nil {
		t.Fatal("TraitDecl not found")
	}
	if trait.Name != "Display" {
		t.Errorf("trait name: want Display, got %s", trait.Name)
	}
	if len(trait.Methods) != 2 {
		t.Fatalf("trait methods: want 2, got %d", len(trait.Methods))
	}
	for _, m := range trait.Methods {
		if m.Body != nil {
			t.Errorf("trait method %q should have nil body", m.Name)
		}
		if m.ReceiverType != "Display" {
			t.Errorf("trait method %q receiver: want Display, got %s", m.Name, m.ReceiverType)
		}
	}
	if impl == nil {
		t.Fatal("ImplDecl not found")
	}
	if impl.TypeName != "User" || impl.TraitName != "Display" {
		t.Errorf("impl TypeName/TraitName: want User/Display, got %s/%s", impl.TypeName, impl.TraitName)
	}
	if len(impl.Methods) != 2 {
		t.Fatalf("impl methods: want 2, got %d", len(impl.Methods))
	}
	for _, m := range impl.Methods {
		if m.Body == nil {
			t.Errorf("impl method %q should have a body", m.Name)
		}
		if m.ReceiverType != "User" {
			t.Errorf("impl method %q receiver: want User, got %s", m.Name, m.ReceiverType)
		}
	}
}

func TestParseInherentImplRejected(t *testing.T) {
	t.Parallel()
	_, err := parseSource(t, `
type User {
  User(name: String)
}

impl User {
  fun show() -> String { self.name }
}
`)
	if err == nil {
		t.Fatal("expected error for inherent impl, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "inherent impl") || !strings.Contains(msg, "type User") {
		t.Errorf("error should point to `type User { fun ... }`; got: %s", msg)
	}
}

func TestParseStaticInImplRejected(t *testing.T) {
	t.Parallel()
	_, err := parseSource(t, `
type User { User(name: String) }
trait Display { fun show() -> String }

impl User: Display {
  static fun make() -> String { "" }
}
`)
	if err == nil {
		t.Fatal("expected error for static fun in impl, got nil")
	}
	if !strings.Contains(err.Error(), "static fun is not supported in impl") {
		t.Errorf("unexpected error: %s", err.Error())
	}
}

// findTypeDecl returns the first TypeDecl named name from prog.
func findTypeDecl(t *testing.T, prog *Program, name string) TypeDecl {
	t.Helper()
	for _, d := range prog.Decls {
		if td, ok := d.(TypeDecl); ok && td.Name == name {
			return td
		}
	}
	t.Fatalf("TypeDecl %q not found", name)
	return TypeDecl{}
}

func TestParseDeriveBindable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		typeName string
		src      string
	}{
		{
			"short record with method block",
			"Todo",
			`type Todo (id: Int, body: String) derive Bindable {
  fun describe() -> String { self.body }
}`,
		},
		{
			"short record without method block",
			"Todo",
			`type Todo (id: Int, body: String) derive Bindable`,
		},
		{
			"sum type",
			"Status",
			`type Status derive Bindable {
  Active
  Archived
}`,
		},
		{
			"long form with constructors",
			"Resp",
			`type Resp derive Bindable {
  Ok(value: Int)
  Err(message: String)
}`,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			prog, err := parseSource(t, tc.src)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			td := findTypeDecl(t, prog, tc.typeName)
			if len(td.Derives) != 1 {
				t.Fatalf("Derives: want 1, got %d", len(td.Derives))
			}
			if td.Derives[0].Name != "Bindable" {
				t.Errorf("Derives[0].Name: want Bindable, got %s", td.Derives[0].Name)
			}
			if td.Derives[0].Pos.Line == 0 || td.Derives[0].Pos.Col == 0 {
				t.Errorf("Derives[0].Pos should be populated, got %+v", td.Derives[0].Pos)
			}
		})
	}
}

func TestParseFnTypeParams(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		src     string
		fnName  string
		params  []TypeParamDecl
	}{
		{
			"single unconstrained",
			`fun identity[T](x: T) -> T { x }`,
			"identity",
			[]TypeParamDecl{{Name: "T"}},
		},
		{
			"single Bindable constraint",
			`fun freeze[T: Bindable](d: T) -> T { d }`,
			"freeze",
			[]TypeParamDecl{{Name: "T", Constraint: "Bindable"}},
		},
		{
			"multiple unconstrained",
			`fun pair[A, B](a: A, b: B) -> A { a }`,
			"pair",
			[]TypeParamDecl{{Name: "A"}, {Name: "B"}},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			prog, err := parseSource(t, tc.src)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			var fn *FnDecl
			for _, d := range prog.Decls {
				if fd, ok := d.(FnDecl); ok && fd.Name == tc.fnName {
					fn = &fd
					break
				}
			}
			if fn == nil {
				t.Fatalf("FnDecl %s not found", tc.fnName)
			}
			if len(fn.TypeParams) != len(tc.params) {
				t.Fatalf("TypeParams: want %d, got %d", len(tc.params), len(fn.TypeParams))
			}
			for i, want := range tc.params {
				got := fn.TypeParams[i]
				if got.Name != want.Name {
					t.Errorf("TypeParams[%d].Name: want %s, got %s", i, want.Name, got.Name)
				}
				if got.Constraint != want.Constraint {
					t.Errorf("TypeParams[%d].Constraint: want %q, got %q", i, want.Constraint, got.Constraint)
				}
			}
		})
	}
}

func TestParseFnMultiConstraintRejected(t *testing.T) {
	t.Parallel()
	_, err := parseSource(t, `fun freeze[T: Bindable + Cloneable](d: T) -> T { d }`)
	if err == nil {
		t.Fatal("expected error for multi-trait constraint, got nil")
	}
	if !strings.Contains(err.Error(), "multi-trait constraint") {
		t.Errorf("error should mention multi-trait constraint; got: %s", err.Error())
	}
}

func TestParseDeriveMultiRejected(t *testing.T) {
	t.Parallel()
	_, err := parseSource(t, `type Todo (id: Int) derive Bindable, Cloneable`)
	if err == nil {
		t.Fatal("expected error for multi-trait derive, got nil")
	}
	if !strings.Contains(err.Error(), "multi-trait `derive`") {
		t.Errorf("error should mention multi-trait derive; got: %s", err.Error())
	}
}

// paramTypeOf returns the type of the first parameter of the first top-level
// function declaration in src. Used as a lightweight probe for function-type
// parse results.
func paramTypeOf(t *testing.T, src string) Type {
	t.Helper()
	prog, err := parseSource(t, src)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	for _, d := range prog.Decls {
		if fd, ok := d.(FnDecl); ok {
			if len(fd.Params) == 0 {
				t.Fatalf("function has no params")
			}
			return fd.Params[0].Type
		}
	}
	t.Fatalf("no function decl in program")
	return nil
}

func TestParseFunctionTypeForms(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		src     string
		assert  func(t *testing.T, typ Type)
	}{
		{
			"single arg",
			`fun f(g: A -> B) { }`,
			func(t *testing.T, typ Type) {
				fn, ok := typ.(FunctionType)
				if !ok || len(fn.Params) != 1 {
					t.Fatalf("want FunctionType with 1 param, got %#v", typ)
				}
				assertNamed(t, fn.Params[0], "A")
				assertNamed(t, fn.Ret, "B")
			},
		},
		{
			"multi arg",
			`fun f(g: (A, B) -> C) { }`,
			func(t *testing.T, typ Type) {
				fn, ok := typ.(FunctionType)
				if !ok || len(fn.Params) != 2 {
					t.Fatalf("want FunctionType with 2 params, got %#v", typ)
				}
				assertNamed(t, fn.Params[0], "A")
				assertNamed(t, fn.Params[1], "B")
				assertNamed(t, fn.Ret, "C")
			},
		},
		{
			"zero arg",
			`fun f(g: () -> C) { }`,
			func(t *testing.T, typ Type) {
				fn, ok := typ.(FunctionType)
				if !ok || len(fn.Params) != 0 {
					t.Fatalf("want FunctionType with 0 params, got %#v", typ)
				}
				assertNamed(t, fn.Ret, "C")
			},
		},
		{
			"higher order param",
			`fun f(g: (A -> B) -> C) { }`,
			func(t *testing.T, typ Type) {
				fn, ok := typ.(FunctionType)
				if !ok || len(fn.Params) != 1 {
					t.Fatalf("want outer FunctionType with 1 param, got %#v", typ)
				}
				inner, ok := fn.Params[0].(FunctionType)
				if !ok || len(inner.Params) != 1 {
					t.Fatalf("want inner FunctionType as param, got %#v", fn.Params[0])
				}
				assertNamed(t, inner.Params[0], "A")
				assertNamed(t, inner.Ret, "B")
				assertNamed(t, fn.Ret, "C")
			},
		},
		{
			"right associative",
			`fun f(g: A -> B -> C) { }`,
			func(t *testing.T, typ Type) {
				// A -> (B -> C)
				fn, ok := typ.(FunctionType)
				if !ok || len(fn.Params) != 1 {
					t.Fatalf("want outer FunctionType with 1 param, got %#v", typ)
				}
				assertNamed(t, fn.Params[0], "A")
				ret, ok := fn.Ret.(FunctionType)
				if !ok {
					t.Fatalf("want Ret to be FunctionType, got %#v", fn.Ret)
				}
				assertNamed(t, ret.Params[0], "B")
				assertNamed(t, ret.Ret, "C")
			},
		},
		{
			"inside generic",
			`fun f(xs: List[A -> B]) { }`,
			func(t *testing.T, typ Type) {
				named, ok := typ.(NamedType)
				if !ok || named.Name != "List" || len(named.Params) != 1 {
					t.Fatalf("want List[_], got %#v", typ)
				}
				inner, ok := named.Params[0].(FunctionType)
				if !ok {
					t.Fatalf("want FunctionType inside List, got %#v", named.Params[0])
				}
				assertNamed(t, inner.Params[0], "A")
				assertNamed(t, inner.Ret, "B")
			},
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.assert(t, paramTypeOf(t, c.src))
		})
	}
}

func TestParseFunctionTypeInReturn(t *testing.T) {
	t.Parallel()
	prog, err := parseSource(t, `fun make() -> A -> B { }`)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	fd, ok := prog.Decls[0].(FnDecl)
	if !ok {
		t.Fatalf("want FnDecl, got %T", prog.Decls[0])
	}
	fn, ok := fd.ReturnType.(FunctionType)
	if !ok {
		t.Fatalf("want ReturnType FunctionType, got %#v", fd.ReturnType)
	}
	assertNamed(t, fn.Params[0], "A")
	assertNamed(t, fn.Ret, "B")
}

func TestParseFunctionTypeTrailingCommaRejected(t *testing.T) {
	t.Parallel()
	_, err := parseSource(t, `fun f(g: (A, B,) -> C) { }`)
	if err == nil {
		t.Fatal("expected error for trailing comma in fn param list")
	}
	if !strings.Contains(err.Error(), "trailing comma") {
		t.Errorf("error should mention trailing comma; got: %s", err.Error())
	}
}

func TestFunctionTypeFormatterRoundTrip(t *testing.T) {
	t.Parallel()
	cases := []string{
		`fun f(g: A -> B) { }`,
		`fun f(g: (A, B) -> C) { }`,
		`fun f(g: () -> C) { }`,
		`fun f(g: (A -> B) -> C) { }`,
		`fun f(g: A -> B -> C) { }`,
		`fun f(xs: List[A -> B]) { }`,
	}
	for _, src := range cases {
		src := src
		t.Run(src, func(t *testing.T) {
			t.Parallel()
			prog1, err := parseSource(t, src)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			formatted := NewFormatter().Format(prog1)
			prog2, err := parseSource(t, formatted)
			if err != nil {
				t.Fatalf("re-parse error on formatted source: %v\nformatted: %s", err, formatted)
			}
			t1 := firstParamType(prog1)
			t2 := firstParamType(prog2)
			if !typesEqual(t1, t2) {
				t.Errorf("round-trip mismatch\n  original:  %#v\n  formatted: %s\n  reparsed:  %#v", t1, formatted, t2)
			}
		})
	}
}

func firstParamType(prog *Program) Type {
	for _, d := range prog.Decls {
		if fd, ok := d.(FnDecl); ok && len(fd.Params) > 0 {
			return fd.Params[0].Type
		}
	}
	return nil
}

// typesEqual compares two AST Type values structurally, ignoring Pos so
// round-trips through the formatter (which drops positions) compare clean.
func typesEqual(a, b Type) bool {
	switch x := a.(type) {
	case NamedType:
		y, ok := b.(NamedType)
		if !ok || x.Name != y.Name || len(x.Params) != len(y.Params) {
			return false
		}
		for i := range x.Params {
			if !typesEqual(x.Params[i], y.Params[i]) {
				return false
			}
		}
		return true
	case PointerType:
		y, ok := b.(PointerType)
		return ok && typesEqual(x.Inner, y.Inner)
	case TupleType:
		y, ok := b.(TupleType)
		if !ok || len(x.Elements) != len(y.Elements) {
			return false
		}
		for i := range x.Elements {
			if !typesEqual(x.Elements[i], y.Elements[i]) {
				return false
			}
		}
		return true
	case FunctionType:
		y, ok := b.(FunctionType)
		if !ok || len(x.Params) != len(y.Params) {
			return false
		}
		for i := range x.Params {
			if !typesEqual(x.Params[i], y.Params[i]) {
				return false
			}
		}
		return typesEqual(x.Ret, y.Ret)
	}
	return a == nil && b == nil
}

func assertNamed(t *testing.T, typ Type, name string) {
	t.Helper()
	nt, ok := typ.(NamedType)
	if !ok || nt.Name != name {
		t.Errorf("want NamedType{Name=%q}, got %#v", name, typ)
	}
}
