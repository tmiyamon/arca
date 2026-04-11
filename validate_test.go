package main

import (
	"testing"
)

func hasErrorCode(errs []CompileError, code ErrorCode) bool {
	for _, e := range errs {
		if e.Code == code {
			return true
		}
	}
	return false
}

func validateSource(source string) []CompileError {
	lexer := NewLexer(source)
	tokens, err := lexer.Tokenize()
	if err != nil {
		return []CompileError{{Data: MessageData{Text: "lexer error: " + err.Error()}}}
	}
	parser := NewParser(tokens)
	prog, err := parser.ParseProgram()
	if err != nil {
		return []CompileError{{Data: MessageData{Text: "parse error: " + err.Error()}}}
	}

	lowerer := NewLowerer(prog, "", nil)
	irProg := lowerer.Lower(prog, "main", false)

	// Collect errors from both lowerer (hint-based) and validator
	errs := lowerer.Errors()
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
	if !hasErrorCode(errs, ErrUnknownType) {
		t.Errorf("unexpected error: %s", errs[0].Message())
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
	if !hasErrorCode(errs, ErrUnknownType) {
		t.Errorf("unexpected error: %s", errs[0].Message())
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
	if !hasErrorCode(errs, ErrWrongFieldCount) {
		t.Errorf("expected ErrWrongFieldCount, got: %v", errs)
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
	if !hasErrorCode(errs, ErrUnknownField) {
		t.Errorf("unexpected error: %s", errs[0].Message())
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
    Red => "red"
    _ => "other"
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
    Red => "red"
    Green => "green"
    Blue => "blue"
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
    Red => "red"
    _ => "other"
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
		if e.Code == ErrUnknownType {
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
    Active => True
    Inactive => False
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
	if !hasErrorCode(errs, ErrWrongArgCount) {
		t.Errorf("unexpected error: %s", errs[0].Message())
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

func TestIfExprBranchTypeMismatch(t *testing.T) {
	t.Parallel()
	// `let x = if cond { 42 } else { "hello" }` has no outer hint, so the
	// branch type unification is the only check. Previously silent, letting
	// the mismatch leak to the Go compiler.
	errs := validateSource(`
fun main() {
  let x = if 1 > 0 { 42 } else { "hello" }
}
`)
	if !hasErrorCode(errs, ErrTypeMismatch) {
		t.Fatalf("expected ErrTypeMismatch for if/else branch disagreement, got: %v", errs)
	}
}

func TestGenericConstructorSameParamMismatch(t *testing.T) {
	t.Parallel()
	// `Pair[A]` with two fields of type A must unify both args to the
	// same concrete type. Passing Int and String must fail at the second
	// field, not silently accept and leak to Go.
	errs := validateSource(`
type Pair[A] {
  Pair(first: A, second: A)
}

fun main() {
  let _ = Pair(first: 1, second: "hello")
}
`)
	if !hasErrorCode(errs, ErrTypeMismatch) {
		t.Fatalf("expected ErrTypeMismatch, got: %v", errs)
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

// HM inference relies on propagating a hint (let annotation, return type,
// function param) into the RHS so bare constructors like Ok/Error/None and
// empty list/map literals can be resolved. These tests lock down the
// happy-path and the mismatch-path for each hint source.
func TestHMInferenceFromHint(t *testing.T) {
	t.Parallel()

	t.Run("ok_from_let_annotation", func(t *testing.T) {
		t.Parallel()
		errs := validateSource(`
fun main() {
  let r: Result[Int, error] = Ok(42)
}
`)
		if len(errs) > 0 {
			t.Errorf("unexpected errors: %v", errs)
		}
	})

	t.Run("ok_type_mismatch_vs_annotation", func(t *testing.T) {
		t.Parallel()
		errs := validateSource(`
fun main() {
  let r: Result[String, error] = Ok(42)
}
`)
		if !hasErrorCode(errs, ErrTypeMismatch) {
			t.Errorf("expected ErrTypeMismatch, got: %v", errs)
		}
	})

	t.Run("none_from_return_type", func(t *testing.T) {
		t.Parallel()
		errs := validateSource(`
fun find() -> Option[Int] {
  None
}
`)
		if len(errs) > 0 {
			t.Errorf("unexpected errors: %v", errs)
		}
	})

	t.Run("empty_list_from_let_annotation", func(t *testing.T) {
		t.Parallel()
		errs := validateSource(`
fun main() {
  let xs: List[Int] = []
}
`)
		if len(errs) > 0 {
			t.Errorf("unexpected errors: %v", errs)
		}
	})

	t.Run("result_error_arm_binding_type_flows", func(t *testing.T) {
		t.Parallel()
		// The `e` binding in `Error(e)` takes the Result's Err type.
		// Here Err = error, so passing `e` to a String param must mismatch.
		errs := validateSource(`
fun try() -> Result[Int, error] { Ok(1) }

fun greet(name: String) -> String { name }

fun main() {
  match try() {
    Ok(_) => "ok"
    Error(e) => greet(e)
  }
}
`)
		if !hasErrorCode(errs, ErrTypeMismatch) {
			t.Errorf("expected ErrTypeMismatch from error-arm binding flow, got: %v", errs)
		}
	})
}

// Parse errors must not be silently misclassified as type errors — they
// should surface as ErrUnspecified (the zero code) so hasErrorCode checks
// for real codes don't accidentally pass.
// Each error must be anchored at an accurate source position (line:col) so
// CLI output and LSP diagnostics can point the user at the right spot. These
// tests lock in the contract for the common error kinds; without them,
// reshuffling the lowerer can silently regress positions without breaking
// anything else.
func TestErrorPositions(t *testing.T) {
	t.Parallel()

	find := func(errs []CompileError, code ErrorCode) *CompileError {
		for i := range errs {
			if errs[i].Code == code {
				return &errs[i]
			}
		}
		return nil
	}

	t.Run("undefined_variable", func(t *testing.T) {
		t.Parallel()
		// x is on line 3, column 11 (1-based).
		errs := validateSource(`
fun main() {
  println(x)
}
`)
		e := find(errs, ErrUnknownVariable)
		if e == nil {
			t.Fatalf("expected ErrUnknownVariable, got: %v", errs)
		}
		if e.Pos.Line != 3 || e.Pos.Col != 11 {
			t.Errorf("undefined variable: expected 3:11, got %d:%d", e.Pos.Line, e.Pos.Col)
		}
	})

	t.Run("unused_package", func(t *testing.T) {
		t.Parallel()
		errs := validateSource(`
import go "strconv"
import go "time"
fun main() { let _ = strconv.Itoa(42) }
`)
		e := find(errs, ErrUnusedPackage)
		if e == nil {
			t.Fatalf("expected ErrUnusedPackage, got: %v", errs)
		}
		if e.Pos.Line != 3 || e.Pos.Col != 1 {
			t.Errorf("unused package: expected 3:1, got %d:%d", e.Pos.Line, e.Pos.Col)
		}
	})

	t.Run("unknown_type", func(t *testing.T) {
		t.Parallel()
		// Unknown type `Bogus` in the constructor field list.
		errs := validateSource(`
type Order {
  Order(id: Int, status: Bogus)
}
`)
		e := find(errs, ErrUnknownType)
		if e == nil {
			t.Fatalf("expected ErrUnknownType, got: %v", errs)
		}
		if e.Pos.Line != 3 {
			t.Errorf("unknown type: expected line 3, got line %d", e.Pos.Line)
		}
	})

	t.Run("non_exhaustive_match", func(t *testing.T) {
		t.Parallel()
		// The match starts on line 8 — non-exhaustive (missing Blue).
		errs := validateSource(`
type Color {
  Red
  Green
  Blue
}
fun pick(c: Color) -> String {
  match c {
    Red => "r"
    Green => "g"
  }
}
`)
		e := find(errs, ErrNonExhaustiveMatch)
		if e == nil {
			t.Fatalf("expected ErrNonExhaustiveMatch, got: %v", errs)
		}
		if e.Pos.Line != 8 {
			t.Errorf("non-exhaustive match: expected line 8, got line %d", e.Pos.Line)
		}
	})
}

func TestParseErrorIsNotTypeMismatch(t *testing.T) {
	t.Parallel()
	errs := validateSource(`
fun broken( {
  42
}
`)
	if len(errs) == 0 {
		t.Fatal("expected a parse error")
	}
	if hasErrorCode(errs, ErrTypeMismatch) {
		t.Error("parse error must not report as ErrTypeMismatch")
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
    Wrap(value) => greet(value)
  }
}
`)
	if len(errs) == 0 {
		t.Fatal("expected error for passing Int to String param via pattern binding")
	}
}

func TestValidateConstraintCompatibility(t *testing.T) {
	t.Parallel()
	// AdultAge => Age: compatible (stricter range fits in wider range)
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

	// Age => AdultAge: NOT compatible (wider range doesn't fit in stricter)
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
    Ok(n) => println(n)
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
    Ok(n) => println(n)
    Error(e) => println(e)
  }
}
`)
	hasExhaustiveErr := false
	for _, e := range errs {
		if e.Code == ErrNonExhaustiveMatch {
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
	if !hasErrorCode(errs, ErrUnknownVariable) {
		t.Errorf("expected ErrUnknownVariable, got: %v", errs)
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
	if !hasErrorCode(errs, ErrTypeMismatch) {
		t.Fatal("expected ErrTypeMismatch for return type")
	}

	// Match arm type mismatch
	errs = validateSource(`
fun test(x: Int) -> String {
  match x {
    1 => "one"
    2 => 42
    _ => "other"
  }
}
`)
	if !hasErrorCode(errs, ErrTypeMismatch) {
		t.Fatal("expected ErrTypeMismatch for match arm")
	}
}

func TestUnusedPackage(t *testing.T) {
	t.Parallel()
	// `time` is imported but never referenced → should report ErrUnusedPackage
	errs := validateSource(`
import go "strconv"
import go "time"
fun main() {
  let _ = strconv.Itoa(42)
}
`)
	if !hasErrorCode(errs, ErrUnusedPackage) {
		t.Fatalf("expected ErrUnusedPackage for unused 'time' import, got: %v", errs)
	}
	// Message must follow the "<reason>: <name>" convention used by other errors.
	for _, e := range errs {
		if e.Code == ErrUnusedPackage && e.Message() != "unused package: time" {
			t.Errorf("unexpected message format: %q", e.Message())
		}
	}
	for _, e := range errs {
		if e.Code == ErrUnusedPackage {
			if data, ok := e.Data.(UnusedPackageData); !ok || data.Name != "time" {
				t.Errorf("expected ErrUnusedPackage for 'time', got: %v", e)
			}
			if e.Pos.Line != 3 {
				t.Errorf("expected unused-import error at line 3, got line %d", e.Pos.Line)
			}
		}
	}
}

func TestUsedPackageNoError(t *testing.T) {
	t.Parallel()
	// All imports are used (fmt via string interpolation, strconv via function call)
	errs := validateSource(`
import go "fmt"
import go "strconv"
fun main() {
  let s = strconv.Itoa(42)
  println("${s}")
  let _ = fmt.Sprintf("x")
}
`)
	for _, e := range errs {
		if e.Code == ErrUnusedPackage {
			t.Errorf("unexpected unused-package error: %s", e.Message())
		}
	}
}

func TestSideEffectImportNotFlagged(t *testing.T) {
	t.Parallel()
	// Side-effect imports (`import go _ "..."`) must never be flagged as unused.
	errs := validateSource(`
import go _ "embed"
fun main() {
  println("hi")
}
`)
	for _, e := range errs {
		if e.Code == ErrUnusedPackage {
			t.Errorf("side-effect import incorrectly flagged as unused: %s", e.Message())
		}
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
		if e.Code == ErrUnknownVariable {
			t.Errorf("unexpected error: %s", e.Message())
		}
	}
}
