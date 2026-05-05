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
	lowerer.Lower(prog, "main", false)
	return lowerer.Errors()
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
	if !hasErrorCode(errs, ErrUnknownType) {
		t.Errorf("expected ErrUnknownType, got: %v", errs)
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
  let r: Result[Int, Error] = Ok(42)
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
  let r: Result[String, Error] = Ok(42)
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
fun try() -> Result[Int, Error] { Ok(1) }

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
fun test() -> Result[String, Error] {
  let adult = AdultAge(25)?
  Ok(greet(adult))
}
fun main() { let _ = test() }
`)
	if len(errs) != 0 {
		t.Fatalf("expected no errors for AdultAge->Age, got: %v", errs)
	}

	// Age => AdultAge: NOT compatible (wider range doesn't fit in stricter)
	errs = validateSource(`
type Age = Int{min: 0, max: 150}
type AdultAge = Int{min: 18, max: 150}
fun drink(age: AdultAge) -> String { "cheers" }
fun test() -> Result[String, Error] {
  let age = Age(10)?
  Ok(drink(age))
}
fun main() { let _ = test() }
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

func TestTryOutsideResultContext(t *testing.T) {
	t.Parallel()
	// ? in non-Result function without try block is a compile error
	errs := validateSource(`
import go "strconv"
fun main() {
  let n = strconv.Atoi("42")?
  println(n)
}
`)
	if !hasErrorCode(errs, ErrTryOutsideResultContext) {
		t.Errorf("expected ErrTryOutsideResultContext, got: %v", errs)
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
fun test() -> Result[Int, Error] {
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

func TestTraitImplLowersWithoutErrors(t *testing.T) {
	t.Parallel()
	errs := validateSource(`
trait Display {
  fun show() -> String
}

type User {
  User(name: String)
}

impl User: Display {
  fun show() -> String {
    self.name
  }
}
`)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got: %v", errs)
	}
}

func TestTraitAsTypeAcceptsConcreteValue(t *testing.T) {
	t.Parallel()
	// Passing a concrete User (impl Display) where Display is expected must
	// pass hint-driven unify via traitImplCompatible.
	errs := validateSource(`
trait Display {
  fun show() -> String
}

type User {
  User(name: String)
}

impl User: Display {
  fun show() -> String {
    self.name
  }
}

fun render(d: Display) -> String {
  d.show()
}

fun main() {
  let u = User(name: "A")
  render(u)
}
`)
	if len(errs) != 0 {
		t.Fatalf("expected no errors for concrete→trait coercion, got: %v", errs)
	}
}

func TestTraitConcreteWithoutImplRejected(t *testing.T) {
	t.Parallel()
	// Display hint with a Dog value that does not impl Display → mismatch.
	errs := validateSource(`
trait Display {
  fun show() -> String
}

type Dog {
  Dog(name: String)
}

fun render(d: Display) -> String {
  "x"
}

fun main() {
  let d = Dog(name: "Rex")
  render(d)
}
`)
	if !hasErrorCode(errs, ErrTypeMismatch) {
		t.Fatalf("expected ErrTypeMismatch for concrete without impl, got: %v", errs)
	}
}

func TestTraitMethodCollisionWithInherent(t *testing.T) {
	t.Parallel()
	// Inherent `show` on User + impl Display with `show` → collision.
	errs := validateSource(`
trait Display {
  fun show() -> String
}

type User {
  User(name: String)

  fun show() -> String { self.name }
}

impl User: Display {
  fun show() -> String { self.name }
}
`)
	if !hasErrorCode(errs, ErrTraitMethodCollision) {
		t.Fatalf("expected ErrTraitMethodCollision, got: %v", errs)
	}
}

func TestTraitImplTargetUnknownType(t *testing.T) {
	t.Parallel()
	errs := validateSource(`
trait Display {
  fun show() -> String
}

impl Nope: Display {
  fun show() -> String { "x" }
}
`)
	if !hasErrorCode(errs, ErrUnknownType) {
		t.Fatalf("expected ErrUnknownType for impl on missing type, got: %v", errs)
	}
}

// analyzeTraitObjectSafety classifies traits for B-series Synthetic
// Builder dispatch routing (decisions/ffi.md 2026-05-02 refined). Parser
// relaxations have landed (B1b: Self in non-receiver positions; B1c:
// static fun in trait body), so non-object-safe traits parsable from
// source live in TestStage2_DropsDictionaryTraits and the source-level
// cases below. The hand-constructed cases here pin the analyzer's
// detection of every trigger independent of parser surface.

func TestAnalyzeTraitObjectSafety_ParsedTraitsAreVtable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
	}{
		{
			name: "single &self method",
			source: `
trait Display {
  fun show() -> String
}
`,
		},
		{
			name: "void method",
			source: `
trait Logger {
  fun log()
}
`,
		},
		{
			name: "method with parameters",
			source: `
trait Comparator {
  fun compare(other: Int) -> Int
}
`,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lexer := NewLexer(tc.source)
			tokens, err := lexer.Tokenize()
			if err != nil {
				t.Fatalf("lex: %v", err)
			}
			prog, err := NewParser(tokens).ParseProgram()
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			var trait TraitDecl
			for _, d := range prog.Decls {
				if td, ok := d.(TraitDecl); ok {
					trait = td
					break
				}
			}
			if trait.Name == "" {
				t.Fatalf("no trait found in source")
			}
			if got := analyzeTraitObjectSafety(trait); got != TraitKindVtable {
				t.Errorf("expected Vtable, got %s", got)
			}
		})
	}
}

func TestAnalyzeTraitObjectSafety_NonObjectSafeIsDictionary(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		trait TraitDecl
	}{
		{
			name: "static fun",
			trait: TraitDecl{Name: "Default", Methods: []FnDecl{
				{Name: "make", Static: true, ReturnType: NamedType{Name: "Self"}},
			}},
		},
		{
			name: "Self in return type",
			trait: TraitDecl{Name: "Cloneable", Methods: []FnDecl{
				{Name: "clone", ReturnType: NamedType{Name: "Self"}},
			}},
		},
		{
			name: "Self in parameter",
			trait: TraitDecl{Name: "Combiner", Methods: []FnDecl{
				{Name: "combine", Params: []FnParam{{Name: "other", Type: NamedType{Name: "Self"}}}},
			}},
		},
		{
			name: "Self nested under generic",
			trait: TraitDecl{Name: "Producer", Methods: []FnDecl{
				{Name: "produce", ReturnType: NamedType{
					Name:   "List",
					Params: []Type{NamedType{Name: "Self"}},
				}},
			}},
		},
		{
			name: "Self under pointer",
			trait: TraitDecl{Name: "Borrower", Methods: []FnDecl{
				{Name: "borrow", ReturnType: PointerType{Inner: NamedType{Name: "Self"}}},
			}},
		},
		{
			name: "associated type declaration alone",
			trait: TraitDecl{
				Name:       "Container",
				AssocTypes: []TraitAssocTypeDecl{{Name: "Item"}},
				Methods:    []FnDecl{{Name: "size", ReturnType: NamedType{Name: "Int"}}},
			},
		},
		{
			name: "associated type via Self.X in return",
			trait: TraitDecl{
				Name: "Producer",
				Methods: []FnDecl{
					{Name: "make", ReturnType: AssocTypeName{Recv: "Self", Name: "Output"}},
				},
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := analyzeTraitObjectSafety(tc.trait); got != TraitKindDictionary {
				t.Errorf("expected Dictionary, got %s", got)
			}
		})
	}
}

func TestLowerTraitDecl_SetsKindFromAnalysis(t *testing.T) {
	t.Parallel()
	// Validate that lowerTraitDecl stamps the Kind on every emitted
	// IRTraitDecl. Currently parsed traits all classify as Vtable, so this
	// check pins the wiring in place ahead of B1b's parser relaxations.
	src := `
trait Display {
  fun show() -> String
}
`
	tokens, err := NewLexer(src).Tokenize()
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	prog, err := NewParser(tokens).ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	l := NewLowerer(prog, "main", &NullTypeResolver{})
	out := l.Lower(prog, "main", false)
	var found bool
	for _, td := range out.Types {
		if trait, ok := td.(IRTraitDecl); ok && trait.GoName == traitGoName("Display") {
			found = true
			if trait.Kind != TraitKindVtable {
				t.Errorf("expected Vtable kind on emitted trait, got %s", trait.Kind)
			}
		}
	}
	if !found {
		t.Fatalf("Display trait not found in lowerer output")
	}
}

// Stage 2 lowering drops dictionary-kind IRTraitDecl nodes — they have no
// Go-interface representation, so emit must never see them. Vtable traits
// pass through untouched. Cloneable triggers Dictionary via Self in return
// (B1b); Default triggers via static fun (B1c); Bindable triggers via
// associated type + Self.Builder method signature (B1d).
func TestStage2_DropsDictionaryTraits(t *testing.T) {
	t.Parallel()
	src := `
trait Display {
  fun show() -> String
}
trait Cloneable {
  fun clone() -> Self
}
trait Default {
  static fun make() -> String
}
trait Bindable {
  type Builder
  static fun arcaBuilder() -> Self.Builder
  fun freeze(b: Self.Builder) -> Self
}
`
	tokens, err := NewLexer(src).Tokenize()
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	prog, err := NewParser(tokens).ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	l := NewLowerer(prog, "main", &NullTypeResolver{})
	out := l.Lower(prog, "main", false)
	var foundDisplay, foundCloneable, foundDefault, foundBindable bool
	for _, td := range out.Types {
		trait, ok := td.(IRTraitDecl)
		if !ok {
			continue
		}
		switch trait.GoName {
		case traitGoName("Display"):
			foundDisplay = true
		case traitGoName("Cloneable"):
			foundCloneable = true
		case traitGoName("Default"):
			foundDefault = true
		case traitGoName("Bindable"):
			foundBindable = true
		}
	}
	if !foundDisplay {
		t.Errorf("Vtable trait Display dropped from output")
	}
	if foundCloneable {
		t.Errorf("Dictionary trait Cloneable should be dropped, but found in output")
	}
	if foundDefault {
		t.Errorf("Dictionary trait Default (static fun) should be dropped, but found in output")
	}
	if foundBindable {
		t.Errorf("Dictionary trait Bindable (associated type) should be dropped, but found in output")
	}
}

// Referencing a dictionary-kind trait in a type position (parameter, return,
// let annotation, ...) is rejected with ErrUnsupportedFeature in B1b.
// B2 will replace this with hidden-parameter dictionary injection.
func TestLower_DictionaryTraitAsType_Errors(t *testing.T) {
	t.Parallel()
	errs := validateSource(`
trait Cloneable {
  fun clone() -> Self
}

fun handle(c: Cloneable) {}
`)
	if !hasErrorCode(errs, ErrUnsupportedFeature) {
		t.Fatalf("expected ErrUnsupportedFeature for Cloneable in param position, got: %v", errs)
	}
}

// `impl X: Cloneable { ... }` against a dictionary-kind trait is rejected in
// B1b — the impl has no Go-interface to satisfy, and the dispatch mechanism
// (dictionary struct) doesn't land until B2.
func TestLower_ImplDictionaryTrait_Errors(t *testing.T) {
	t.Parallel()
	errs := validateSource(`
trait Cloneable {
  fun clone() -> Self
}

type Box (value: Int)

impl Box: Cloneable {
  fun clone() -> Self {
    self
  }
}
`)
	if !hasErrorCode(errs, ErrUnsupportedFeature) {
		t.Fatalf("expected ErrUnsupportedFeature for impl of Cloneable, got: %v", errs)
	}
}

// `derive Bindable` on a sum (multi-constructor) type is rejected — Q10
// in the 2026-05-04 refined entry defers sum-type Builder to B5.
func TestLower_DeriveBindableOnSumType_Errors(t *testing.T) {
	t.Parallel()
	errs := validateSource(`
type Status derive Bindable {
  Active
  Archived
}
`)
	if !hasErrorCode(errs, ErrUnsupportedFeature) {
		t.Fatalf("expected ErrUnsupportedFeature for derive Bindable on sum type, got: %v", errs)
	}
}

// B2b/B3a synthesises a `<TypeName>Draft` IRStructDecl for each
// `derive Bindable` host, with field types referencing the stdlib-shared
// `stdlib.BindableSlot[Inner]`. BindableSlot itself is no longer
// compiler-emitted — it lives in `stdlib/bindable.go`.
func TestLower_DeriveBindable_SynthesisesDraft(t *testing.T) {
	t.Parallel()
	src := `type Todo (id: Int, body: String) derive Bindable`
	tokens, err := NewLexer(src).Tokenize()
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	prog, err := NewParser(tokens).ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	l := NewLowerer(prog, "main", &NullTypeResolver{})
	out := l.Lower(prog, "main", false)
	var draft *IRStructDecl
	for i := range out.Types {
		if sd, ok := out.Types[i].(IRStructDecl); ok && sd.GoName == "TodoDraft" {
			draft = &sd
			break
		}
	}
	if draft == nil {
		t.Fatalf("TodoDraft not synthesised; got types: %v", out.Types)
	}
	if len(draft.Fields) != 2 {
		t.Fatalf("TodoDraft fields: want 2, got %d", len(draft.Fields))
	}
	for i, f := range draft.Fields {
		named, ok := f.Type.(IRNamedType)
		if !ok || named.GoName != "stdlib.BindableSlot" || len(named.Params) != 1 {
			t.Errorf("field %d (%s) type: want stdlib.BindableSlot[T], got %T %v", i, f.GoName, f.Type, f.Type)
		}
	}
}

// B2c+B2f: synthesises the BindableDict struct, a `(d TodoDraft) Freeze`
// method with per-field unset checks, a `todoDraft()` factory, and a
// `__TodoBindable` global var that references both via fn-name + method
// expression.
func TestLower_DeriveBindable_SynthesisesDispatch(t *testing.T) {
	t.Parallel()
	src := `type Todo (id: Int, body: String) derive Bindable`
	tokens, err := NewLexer(src).Tokenize()
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	prog, err := NewParser(tokens).ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	l := NewLowerer(prog, "main", &NullTypeResolver{})
	out := l.Lower(prog, "main", false)

	// BindableDict now lives in stdlib; only the Draft + Freeze + factory
	// + dict-instance global are synthesised in user code.
	var freezeFn, factoryFn *IRFn
	for i := range out.Funcs {
		fn := out.Funcs[i]
		if fn.GoName == "Freeze" && fn.Receiver != nil && fn.Receiver.Type == "TodoDraft" {
			freezeFn = &fn
		}
		if fn.GoName == "todoDraft" {
			factoryFn = &fn
		}
	}
	if freezeFn == nil {
		t.Fatalf("(TodoDraft) Freeze method not synthesised")
	}
	body, ok := freezeFn.Body.(IRBlock)
	if !ok {
		t.Fatalf("Freeze body: want IRBlock, got %T", freezeFn.Body)
	}
	if len(body.Stmts) != 3 {
		t.Errorf("Freeze body stmts: want 3, got %d", len(body.Stmts))
	}
	if factoryFn == nil {
		t.Fatalf("todoDraft factory not synthesised")
	}

	if len(out.Globals) != 1 {
		t.Fatalf("globals: want 1, got %d", len(out.Globals))
	}
	gv := out.Globals[0]
	if gv.GoName != "__TodoBindable" {
		t.Errorf("global name: want __TodoBindable, got %s", gv.GoName)
	}
	cc, ok := gv.Init.(IRConstructorCall)
	if !ok || cc.GoName != "stdlib.BindableDict" {
		t.Errorf("global init: want stdlib.BindableDict ctor, got %T %v", gv.Init, gv.Init)
	}
}

// B2d/B2e: `fun f[T: Bindable](x: T) -> T` lowers cleanly. After B2e, the
// IRFn carries the expanded type-param list `[T, __draftT]` and gains a
// hidden `__bindableT BindableDict[T, __draftT]` value param so emit
// produces a Go signature ready for dictionary dispatch.
func TestLower_FnTypeParams_BindableConstraint(t *testing.T) {
	t.Parallel()
	src := `fun freeze[T: Bindable](x: T) -> T { x }`
	tokens, err := NewLexer(src).Tokenize()
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	prog, err := NewParser(tokens).ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	l := NewLowerer(prog, "main", &NullTypeResolver{})
	out := l.Lower(prog, "main", false)
	if len(l.errors) != 0 {
		t.Fatalf("unexpected errors: %v", l.errors)
	}
	var fn *IRFn
	for i := range out.Funcs {
		if out.Funcs[i].GoName == "freeze" {
			fn = &out.Funcs[i]
			break
		}
	}
	if fn == nil {
		t.Fatalf("freeze IRFn not found")
	}
	wantTP := []string{"T", "__draftT"}
	if len(fn.TypeParams) != len(wantTP) || fn.TypeParams[0] != wantTP[0] || fn.TypeParams[1] != wantTP[1] {
		t.Errorf("TypeParams: want %v, got %v", wantTP, fn.TypeParams)
	}
	if len(fn.Params) < 1 || fn.Params[0].GoName != "__bindableT" {
		t.Errorf("first param: want __bindableT, got %+v", fn.Params)
	}
}

// B2e: calling a `[T: Bindable]`-constrained Arca fn with explicit type
// args rewrites the call to inject `__<Type>Bindable` as a hidden value
// arg and `<Type>Draft` into the type-args string.
func TestLower_BindableCallSite_InjectsDict(t *testing.T) {
	t.Parallel()
	src := `
type Todo (id: Int, body: String) derive Bindable
fun makeIt[T: Bindable]() -> Int { 0 }
fun main() {
  let _ = makeIt[Todo]()
}
`
	tokens, err := NewLexer(src).Tokenize()
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	prog, err := NewParser(tokens).ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	l := NewLowerer(prog, "main", &NullTypeResolver{})
	out := l.Lower(prog, "main", false)
	if len(l.errors) != 0 {
		t.Fatalf("unexpected errors: %v", l.errors)
	}
	// Locate the call inside main's body.
	var mainFn *IRFn
	for i := range out.Funcs {
		if out.Funcs[i].GoName == "main" {
			mainFn = &out.Funcs[i]
			break
		}
	}
	if mainFn == nil {
		t.Fatalf("main IRFn not found")
	}
	call := findFirstFnCall(mainFn.Body, "makeIt")
	if call == nil {
		t.Fatalf("makeIt call not found in main")
	}
	if len(call.Args) != 1 {
		t.Fatalf("call.Args: want 1 (hidden dict), got %d", len(call.Args))
	}
	dict, ok := call.Args[0].(IRIdent)
	if !ok || dict.GoName != "__TodoBindable" {
		t.Errorf("hidden arg: want IRIdent __TodoBindable, got %T %v", call.Args[0], call.Args[0])
	}
	if call.TypeArgs != "[Todo, TodoDraft]" {
		t.Errorf("TypeArgs: want [Todo, TodoDraft], got %q", call.TypeArgs)
	}
}

// findFirstFnCall walks an IR expression looking for the first IRFnCall
// whose Source.Name (the Arca-surface name) matches `name`.
func findFirstFnCall(e IRExpr, name string) *IRFnCall {
	switch x := e.(type) {
	case IRFnCall:
		if x.Source.Name == name {
			return &x
		}
		for _, a := range x.Args {
			if c := findFirstFnCall(a, name); c != nil {
				return c
			}
		}
	case IRBlock:
		for _, s := range x.Stmts {
			if ls, ok := s.(IRLetStmt); ok {
				if c := findFirstFnCall(ls.Value, name); c != nil {
					return c
				}
			}
			if es, ok := s.(IRExprStmt); ok {
				if c := findFirstFnCall(es.Expr, name); c != nil {
					return c
				}
			}
		}
		if x.Expr != nil {
			return findFirstFnCall(x.Expr, name)
		}
	}
	return nil
}

func TestLower_FnUnknownConstraint_Errors(t *testing.T) {
	t.Parallel()
	errs := validateSource(`fun f[T: Cloneable](x: T) -> T { x }`)
	if !hasErrorCode(errs, ErrUnsupportedFeature) {
		t.Fatalf("expected ErrUnsupportedFeature for unknown constraint, got: %v", errs)
	}
}

// B2f: `Todo.draft()` resolves to the synthesised factory and `d.freeze()`
// to the Draft inherent method, completing the user-facing Bindable surface.
func TestLower_DeriveBindable_UserSurface(t *testing.T) {
	t.Parallel()
	src := `
type Todo (id: Int, body: String) derive Bindable
fun main() -> Result[Unit, Error] {
  let d = Todo.draft()
  let _ = d.freeze()?
  Ok(())
}
`
	tokens, err := NewLexer(src).Tokenize()
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	prog, err := NewParser(tokens).ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	l := NewLowerer(prog, "main", &NullTypeResolver{})
	l.Lower(prog, "main", false)
	if len(l.errors) != 0 {
		t.Fatalf("unexpected errors: %v", l.errors)
	}
}

// `derive Bindable` registers the type as bindable; B2b+ consume the registry
// to drive Draft / Dictionary synthesis. B2a only validates that the trait
// name is recognised and tracks the type.
func TestLower_DeriveBindable_RegistersType(t *testing.T) {
	t.Parallel()
	src := `type Todo (id: Int, body: String) derive Bindable`
	tokens, err := NewLexer(src).Tokenize()
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	prog, err := NewParser(tokens).ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	l := NewLowerer(prog, "main", &NullTypeResolver{})
	l.Lower(prog, "main", false)
	if !l.bindableTypes["Todo"] {
		t.Errorf("expected bindableTypes[\"Todo\"] = true, got false; map: %v", l.bindableTypes)
	}
	if len(l.errors) != 0 {
		t.Errorf("unexpected errors: %v", l.errors)
	}
}

// `derive Foo` for any name other than the registered intrinsic Bindable is
// rejected. MVP supports only `derive Bindable`.
func TestLower_DeriveUnknown_Errors(t *testing.T) {
	t.Parallel()
	errs := validateSource(`type Box (value: Int) derive Cloneable`)
	if !hasErrorCode(errs, ErrUnsupportedFeature) {
		t.Fatalf("expected ErrUnsupportedFeature for derive Cloneable, got: %v", errs)
	}
}

// Manual `impl T: Bindable { ... }` is rejected — Bindable is a compiler
// intrinsic, only `derive Bindable` activates synthesis (B2a).
func TestLower_ManualImplBindable_Errors(t *testing.T) {
	t.Parallel()
	errs := validateSource(`
type Box (value: Int)

impl Box: Bindable {
  fun freeze() -> Box { self }
}
`)
	if !hasErrorCode(errs, ErrUnsupportedFeature) {
		t.Fatalf("expected ErrUnsupportedFeature for manual impl Bindable, got: %v", errs)
	}
}
