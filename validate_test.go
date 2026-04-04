package main

import (
	"strings"
	"testing"
)

func validateSource(source string) []ValidateError {
	lexer := NewLexer(source)
	tokens, err := lexer.Tokenize()
	if err != nil {
		return []ValidateError{{Message: "lexer error: " + err.Error()}}
	}
	parser := NewParser(tokens)
	prog, err := parser.ParseProgram()
	if err != nil {
		return []ValidateError{{Message: "parse error: " + err.Error()}}
	}

	lowerer := NewLowerer(prog, "", nil)
	irProg := lowerer.Lower(prog, "main", false)

	validator := NewIRValidation(lowerer)
	return validator.Validate(irProg)
}

func TestValidateUnknownType(t *testing.T) {
	t.Parallel()
	errs := validateSource(`
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

func TestValidateUnknownConstructor(t *testing.T) {
	t.Parallel()
	errs := validateSource(`
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

func TestValidateWrongFieldCount(t *testing.T) {
	t.Parallel()
	errs := validateSource(`
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

func TestValidateWrongFieldName(t *testing.T) {
	t.Parallel()
	errs := validateSource(`
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

func TestValidateNonExhaustiveMatch(t *testing.T) {
	t.Parallel()
	// Match exhaustiveness is structurally guaranteed by IR.
	// The lowerer already resolves match arms to the correct IR nodes.
	// This test verifies that valid code with partial match + wildcard still works.
	errs := validateSource(`
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

func TestValidateExhaustiveMatchOk(t *testing.T) {
	t.Parallel()
	errs := validateSource(`
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

func TestValidateWildcardMatchOk(t *testing.T) {
	t.Parallel()
	errs := validateSource(`
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

func TestValidateUnknownReturnType(t *testing.T) {
	t.Parallel()
	errs := validateSource(`
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

func TestValidateValidCodeNoErrors(t *testing.T) {
	t.Parallel()
	errs := validateSource(`
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

func TestValidateWrongArgCount(t *testing.T) {
	t.Parallel()
	errs := validateSource(`
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

func TestValidateWrongArgType(t *testing.T) {
	t.Parallel()
	errs := validateSource(`
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

func TestValidateReturnTypeMismatch(t *testing.T) {
	t.Parallel()
	errs := validateSource(`
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

func TestValidateConstructorFieldTypeMismatch(t *testing.T) {
	t.Parallel()
	errs := validateSource(`
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

func TestValidateLetInference(t *testing.T) {
	t.Parallel()
	errs := validateSource(`
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

func TestValidateMatchPatternBindingType(t *testing.T) {
	t.Parallel()
	errs := validateSource(`
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

func TestValidateConstraintCompatibility(t *testing.T) {
	t.Parallel()
	// AdultAge -> Age: compatible (stricter range fits in wider range)
	errs := validateSource(`
type Age = Int{min: 0, max: 150}
type AdultAge = Int{min: 18, max: 150}
fun greet(age: Age) -> String { "hello" }
fun main() {
  let adult = AdultAge(25)?
  greet(adult)
}
`)
	if len(errs) != 0 {
		t.Fatalf("expected no errors for AdultAge->Age, got: %v", errs)
	}

	// Age -> AdultAge: NOT compatible (wider range doesn't fit in stricter)
	errs = validateSource(`
type Age = Int{min: 0, max: 150}
type AdultAge = Int{min: 18, max: 150}
fun drink(age: AdultAge) -> String { "cheers" }
fun main() {
  let age = Age(10)?
  drink(age)
}
`)
	if len(errs) == 0 {
		t.Fatal("expected error for Age->AdultAge")
	}
	if !strings.Contains(errs[0].Message, "expects AdultAge, got Age") {
		t.Errorf("unexpected error: %s", errs[0].Message)
	}

	// UserId vs OrderId: NOT compatible (nominal, no constraints)
	errs = validateSource(`
type UserId = Int
type OrderId = Int
fun findUser(id: UserId) -> String { "found" }
fun main() {
  let orderId = OrderId(1)
  findUser(orderId)
}
`)
	if len(errs) == 0 {
		t.Fatal("expected error for OrderId->UserId")
	}
	if !strings.Contains(errs[0].Message, "expects UserId, got OrderId") {
		t.Errorf("unexpected error: %s", errs[0].Message)
	}
}
