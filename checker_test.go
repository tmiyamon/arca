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
fun make() -> String {
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

fun make() -> Point {
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

fun make() -> Point {
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

fun name(c: Color) -> String {
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

fun name(c: Color) -> String {
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

fun name(c: Color) -> String {
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
fun make() -> Bogus {
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

pub fun is_active(u: User) -> Bool {
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

func TestCheckerWrongArgCount(t *testing.T) {
	errs := checkSource(`
fun add(a: Int, b: Int) -> Int {
  a + b
}

fun main() {
  add(1)
}
`)
	if len(errs) == 0 {
		t.Fatal("expected error for wrong argument count")
	}
	if !strings.Contains(errs[0].Message, "expects 2 arguments, got 1") {
		t.Errorf("unexpected error: %s", errs[0].Message)
	}
}

func TestCheckerWrongArgType(t *testing.T) {
	errs := checkSource(`
fun greet(name: String) -> String {
  name
}

fun main() {
  greet(42)
}
`)
	if len(errs) == 0 {
		t.Fatal("expected error for wrong argument type")
	}
	if !strings.Contains(errs[0].Message, "expects String, got Int") {
		t.Errorf("unexpected error: %s", errs[0].Message)
	}
}

func TestCheckerReturnTypeMismatch(t *testing.T) {
	errs := checkSource(`
fun get_name() -> String {
  42
}
`)
	if len(errs) == 0 {
		t.Fatal("expected error for return type mismatch")
	}
	if !strings.Contains(errs[0].Message, "returns String but body has type Int") {
		t.Errorf("unexpected error: %s", errs[0].Message)
	}
}

func TestCheckerConstructorFieldTypeMismatch(t *testing.T) {
	errs := checkSource(`
type Point {
  Point(x: Int, y: Int)
}

fun make() -> Point {
  Point(x: "hello", y: 2)
}
`)
	if len(errs) == 0 {
		t.Fatal("expected error for field type mismatch")
	}
	if !strings.Contains(errs[0].Message, "field 'x' of Point expects Int, got String") {
		t.Errorf("unexpected error: %s", errs[0].Message)
	}
}

func TestCheckerLetInference(t *testing.T) {
	errs := checkSource(`
fun greet(name: String) -> String {
  name
}

fun main() {
  let x = 42
  greet(x)
}
`)
	if len(errs) == 0 {
		t.Fatal("expected error for passing Int to String param")
	}
	if !strings.Contains(errs[0].Message, "expects String, got Int") {
		t.Errorf("unexpected error: %s", errs[0].Message)
	}
}

func TestCheckerMatchPatternBindingType(t *testing.T) {
	errs := checkSource(`
type Wrapper {
  Wrap(value: Int)
}

fun greet(name: String) -> String {
  name
}

fun use(w: Wrapper) -> String {
  match w {
    Wrap(value) -> greet(value)
  }
}
`)
	if len(errs) == 0 {
		t.Fatal("expected error for passing Int to String param via pattern binding")
	}
	if !strings.Contains(errs[0].Message, "expects String, got Int") {
		t.Errorf("unexpected error: %s", errs[0].Message)
	}
}
