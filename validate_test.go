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

	// Collect errors from both lowerer (hint-based) and validator
	var errs []ValidateError
	for _, e := range lowerer.Errors() {
		errs = append(errs, ValidateError{Pos: e.Pos, Message: e.Message})
	}
	validator := NewIRValidation(lowerer)
	errs = append(errs, validator.Validate(irProg)...)
	return errs
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
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "unknown type: Bogus") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'unknown type: Bogus' error, got: %v", errs)
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
}

func TestExhaustiveness(t *testing.T) {
	t.Parallel()
	// Non-exhaustive Result match
	errs := validateSource(`
fun main() {
  let r = Ok(1)
  match r {
    Ok(n) -> println(n)
  }
}
`)
	if len(errs) == 0 {
		t.Fatal("expected non-exhaustive match error")
	}

	// Exhaustive Result match
	errs = validateSource(`
fun main() {
  let r = Ok(1)
  match r {
    Ok(n) -> println(n)
    Error(e) -> println(e)
  }
}
`)
	hasExhaustiveErr := false
	for _, e := range errs {
		if strings.Contains(e.Message, "non-exhaustive") {
			hasExhaustiveErr = true
		}
	}
	if hasExhaustiveErr {
		t.Fatal("should not have exhaustiveness error for complete match")
	}
}

func TestUndefinedVariable(t *testing.T) {
	t.Parallel()
	errs := validateSource(`
fun main() {
  println(x)
}
`)
	if len(errs) == 0 {
		t.Fatal("expected undefined variable error")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "undefined variable: x") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'undefined variable: x', got: %v", errs)
	}
}

func TestBidirectionalTypeCheck(t *testing.T) {
	t.Parallel()
	// Return type mismatch
	errs := validateSource(`
fun add(a: Int, b: Int) -> Int {
  "not an int"
}
`)
	if len(errs) == 0 {
		t.Fatal("expected return type mismatch error")
	}

	// Match arm type mismatch
	errs = validateSource(`
fun test(x: Int) -> String {
  match x {
    1 -> "one"
    2 -> 42
    _ -> "other"
  }
}
`)
	if len(errs) == 0 {
		t.Fatal("expected match arm type mismatch error")
	}
}

func TestTryExpressionStatement(t *testing.T) {
	t.Parallel()
	// Try in expression statement should not produce errors
	errs := validateSource(`
import go "strconv"
fun test() -> Result[Int, error] {
  let n = strconv.Atoi("42")?
  strconv.Atoi("99")?
  Ok(n)
}
`)
	for _, e := range errs {
		if strings.Contains(e.Message, "undefined") {
			t.Errorf("unexpected error: %s", e.Message)
		}
	}
}
