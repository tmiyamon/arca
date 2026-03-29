package main

import (
	"strings"
	"testing"
)

func checkSource(source string) []CheckError {
	lexer := NewLexer(source)
	tokens, err := lexer.Tokenize()
	if err != nil {
		return []CheckError{{Message: "lexer error: " + err.Error()}}
	}
	parser := NewParser(tokens)
	prog, err := parser.ParseProgram()
	if err != nil {
		return []CheckError{{Message: "parse error: " + err.Error()}}
	}
	checker := NewChecker()
	return checker.Check(prog)
}

func TestCheckerUnknownType(t *testing.T) {
	errs := checkSource(`
type Order {
  Order(id: Int, status: Unknown)
}
`)
	if len(errs) == 0 {
		t.Fatal("expected error for unknown type")
	}
	if !strings.Contains(errs[0].Message, "unknown type: Unknown") {
		t.Errorf("unexpected error: %s", errs[0].Message)
	}
}

func TestCheckerUnknownConstructor(t *testing.T) {
	errs := checkSource(`
fn make() -> String {
  Bogus(id: 1)
}
`)
	if len(errs) == 0 {
		t.Fatal("expected error for unknown constructor")
	}
	if !strings.Contains(errs[0].Message, "unknown constructor: Bogus") {
		t.Errorf("unexpected error: %s", errs[0].Message)
	}
}

func TestCheckerWrongFieldCount(t *testing.T) {
	errs := checkSource(`
type Point {
  Point(x: Int, y: Int)
}

fn make() -> Point {
  Point(x: 1)
}
`)
	if len(errs) == 0 {
		t.Fatal("expected error for wrong field count")
	}
	if !strings.Contains(errs[0].Message, "expects 2 fields, got 1") {
		t.Errorf("unexpected error: %s", errs[0].Message)
	}
}

func TestCheckerWrongFieldName(t *testing.T) {
	errs := checkSource(`
type Point {
  Point(x: Int, y: Int)
}

fn make() -> Point {
  Point(x: 1, z: 2)
}
`)
	if len(errs) == 0 {
		t.Fatal("expected error for wrong field name")
	}
	if !strings.Contains(errs[0].Message, "no field named 'z'") {
		t.Errorf("unexpected error: %s", errs[0].Message)
	}
}

func TestCheckerNonExhaustiveMatch(t *testing.T) {
	errs := checkSource(`
type Color {
  Red
  Green
  Blue
}

fn name(c: Color) -> String {
  match c {
    Red -> "red"
    Green -> "green"
  }
}
`)
	if len(errs) == 0 {
		t.Fatal("expected error for non-exhaustive match")
	}
	if !strings.Contains(errs[0].Message, "missing Blue") {
		t.Errorf("unexpected error: %s", errs[0].Message)
	}
}

func TestCheckerExhaustiveMatchOk(t *testing.T) {
	errs := checkSource(`
type Color {
  Red
  Green
  Blue
}

fn name(c: Color) -> String {
  match c {
    Red -> "red"
    Green -> "green"
    Blue -> "blue"
  }
}
`)
	if len(errs) > 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
}

func TestCheckerWildcardMatchOk(t *testing.T) {
	errs := checkSource(`
type Color {
  Red
  Green
  Blue
}

fn name(c: Color) -> String {
  match c {
    Red -> "red"
    _ -> "other"
  }
}
`)
	if len(errs) > 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
}

func TestCheckerUnknownReturnType(t *testing.T) {
	errs := checkSource(`
fn make() -> Bogus {
  42
}
`)
	if len(errs) == 0 {
		t.Fatal("expected error for unknown return type")
	}
	if !strings.Contains(errs[0].Message, "unknown type: Bogus") {
		t.Errorf("unexpected error: %s", errs[0].Message)
	}
}

func TestCheckerValidCodeNoErrors(t *testing.T) {
	errs := checkSource(`
type Status {
  Active
  Inactive
}

type User {
  User(name: String, status: Status)
}

pub fn is_active(u: User) -> Bool {
  match u.status {
    Active -> True
    Inactive -> False
  }
}
`)
	if len(errs) > 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
}
