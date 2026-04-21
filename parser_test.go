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
	if !strings.Contains(msg, "expected ':'") || !strings.Contains(msg, "type User") {
		t.Errorf("error should point to `type User { fun ... }`; got: %s", msg)
	}
}

func TestParseStaticInTraitRejected(t *testing.T) {
	t.Parallel()
	_, err := parseSource(t, `
trait Display {
  static fun make() -> String
}
`)
	if err == nil {
		t.Fatal("expected error for static fun in trait, got nil")
	}
	if !strings.Contains(err.Error(), "static fun is not supported in trait") {
		t.Errorf("unexpected error: %s", err.Error())
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
