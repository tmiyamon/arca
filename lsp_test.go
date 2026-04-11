package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// Test go to definition
func TestDefinitionBasic(t *testing.T) {
	t.Parallel()
	source := `import go "fmt"

type User {
    User(name: String)
}

fun greet(u: User) -> String {
    u.name
}

fun main() {
    let alice = User(name: "Alice")
    fmt.Println(greet(alice))
}
`
	cases := []struct {
		desc      string
		line, col int
		wantLine  int
		wantCol   int
	}{
		{"User type in greet param", 7, 15, 3, 6},
		{"greet call", 13, 17, 7, 5},
		{"alice var usage", 13, 23, 12, 9},
		{"u param in body", 8, 5, 7, 11},
		{"fmt package usage", 13, 5, 1, 1}, // fmt.Println → import line
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			got := getDefinitionPos(source, "/tmp/def_test.arca", tc.line, tc.col)
			if got.Line == 0 {
				t.Errorf("definition at line %d col %d returned nothing", tc.line, tc.col)
				return
			}
			if got.Line != tc.wantLine || got.Col != tc.wantCol {
				t.Errorf("[%s] at %d:%d → got %d:%d, want %d:%d",
					tc.desc, tc.line, tc.col, got.Line, got.Col, tc.wantLine, tc.wantCol)
			}
		})
	}
}

// Test go to definition for Arca stdlib
func TestDefinitionStdlib(t *testing.T) {
	t.Parallel()
	source := `import go "fmt"
import stdlib

fun main() {
    let data = stdlib.Encode("hello")
    fmt.Println(data)
}
`
	file, pos := getDefinitionLocation(source, "/tmp/def_stdlib_test.arca", 5, 24)
	t.Logf("stdlib.Encode → file=%q pos=%v", file, pos)
	if file == "" {
		t.Fatal("expected a file path")
	}
	// Simulate LSP path resolution
	resolved := resolveEmbedFilePath(file)
	t.Logf("resolved: %q", resolved)
	if _, err := os.Stat(resolved); err != nil {
		t.Errorf("resolved path should exist: %v", err)
	}
}

// Test go to definition for Go FFI members
func TestDefinitionFFI(t *testing.T) {
	t.Parallel()
	source := `import go "fmt"

fun main() {
    fmt.Println("hello")
}
`
	// fmt.Println → should resolve to fmt package's Println
	file, pos := getDefinitionLocation(source, "/tmp/ffi_test.arca", 4, 9)
	if pos.Line == 0 {
		t.Errorf("expected location for fmt.Println, got nothing")
	}
	t.Logf("fmt.Println → %s:%d:%d", file, pos.Line, pos.Col)
	if file == "" {
		t.Errorf("expected file path (Go stdlib), got empty")
	}
	if !strings.Contains(file, "print.go") && !strings.Contains(file, "fmt") {
		t.Errorf("expected fmt package file, got %s", file)
	}
}

// Test completion
func TestCompletion(t *testing.T) {
	t.Parallel()
	source := `import go "fmt"

type User {
    User(name: String, age: Int)
}

fun main() {
    let u = User(name: "Alice", age: 30)
    u.
    fmt.
}
`
	// Test Arca type field completion at u.
	items := getCompletionItems(source, "/tmp/comp_test.arca", 9, 7)
	if len(items) == 0 {
		t.Errorf("expected completion items for u., got none")
	}
	var names []string
	for _, item := range items {
		names = append(names, item.Label)
	}
	t.Logf("u. completions: %v", names)
	hasName := false
	hasAge := false
	for _, n := range names {
		if n == "Name" || n == "name" {
			hasName = true
		}
		if n == "Age" || n == "age" {
			hasAge = true
		}
	}
	if !hasName || !hasAge {
		t.Errorf("expected name and age fields, got %v", names)
	}

	// Test Go package completion at fmt.
	items2 := getCompletionItems(source, "/tmp/comp_test.arca", 10, 9)
	if len(items2) == 0 {
		t.Errorf("expected completion items for fmt., got none")
	}
	var names2 []string
	for _, item := range items2 {
		names2 = append(names2, item.Label)
	}
	t.Logf("fmt. completions (first 5): %v", names2[:min(5, len(names2))])
	hasPrintln := false
	for _, n := range names2 {
		if n == "Println" {
			hasPrintln = true
			break
		}
	}
	if !hasPrintln {
		t.Errorf("expected Println in fmt completions, got %d items", len(names2))
	}
}

// Test chained completion (a.b.c)
func TestCompletionChained(t *testing.T) {
	t.Parallel()
	source := `import go "fmt"

type Inner {
    Inner(value: Int)
}

type Outer {
    Outer(inner: Inner, name: String)
}

fun main() {
    let o = Outer(inner: Inner(value: 42), name: "hello")
    o.inner.
}
`
	items := getCompletionItems(source, "/tmp/chained_test.arca", 13, 13)
	if len(items) == 0 {
		t.Errorf("expected completions for o.inner., got none")
	}
	var names []string
	for _, item := range items {
		names = append(names, item.Label)
	}
	t.Logf("o.inner. completions: %v", names)
	found := false
	for _, n := range names {
		if n == "value" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'value' field, got %v", names)
	}
}

// Test hover on let with explicit type args call
func TestHoverTypeArgsCall(t *testing.T) {
	t.Parallel()
	source := `import go "fmt"
import stdlib

type Todo {
    Todo(id: Int, body: String)
}

fun main() {
    let todos = stdlib.Decode[Todo](toBytes("{}"))
    fmt.Println(todos)
}
`
	// Hover on 'todos' variable (let binding)
	got := getHoverInfo(source, "/tmp/hover_type_args.arca", 9, 9)
	t.Logf("hover on todos: %q", got)
	if got == "" {
		t.Errorf("expected hover for todos variable, got empty")
	}
}

// Test that explicit type args flow through HM inference
func TestExplicitTypeArgsHMFlow(t *testing.T) {
	t.Parallel()
	source := `import go "fmt"
import stdlib

type User {
    User(name: String)
}

fun process(r: Result[User, error]) -> String {
    match r {
        Ok(u) => u.name
        Error(_) => "error"
    }
}

fun main() {
    let r = stdlib.Decode[User](toBytes("{}"))
    fmt.Println(process(r))
}
`
	// Hover on r — should be Result[User, error]
	got := getHoverInfo(source, "/tmp/hm_flow_test.arca", 16, 9)
	t.Logf("hover on r: %q", got)
	if !strings.Contains(got, "User") {
		t.Errorf("expected r to be Result[User, error], got %q", got)
	}
}

// Test chained completion with Go FFI (similar to todo example)
func TestCompletionChainedFFI(t *testing.T) {
	t.Parallel()
	source := `import go "database/sql"

type App {
    App(db: *sql.DB)

    fun close() {
        self.db.
    }
}
`
	items := getCompletionItems(source, "/tmp/chained_ffi_test.arca", 7, 17)
	if len(items) == 0 {
		t.Errorf("expected completions for self.db., got none")
	}
	var names []string
	for _, item := range items {
		names = append(names, item.Label)
	}
	t.Logf("self.db. completions (first 10): %v", names[:min(10, len(names))])
	hasClose := false
	for _, n := range names {
		if n == "Close" {
			hasClose = true
		}
	}
	if !hasClose {
		t.Errorf("expected Close method, got %d items", len(names))
	}
}

// Test self completion in method body
func TestCompletionSelf(t *testing.T) {
	t.Parallel()
	source := `type User {
    User(name: String, age: Int)

    fun greet() -> String {
        self.
    }
}
`
	items := getCompletionItems(source, "/tmp/self_test.arca", 5, 14)
	if len(items) == 0 {
		t.Errorf("expected completions for self., got none")
	}
	var names []string
	for _, item := range items {
		names = append(names, item.Label)
	}
	t.Logf("self. completions: %v", names)
	foundName := false
	for _, n := range names {
		if n == "name" {
			foundName = true
		}
	}
	if !foundName {
		t.Errorf("expected 'name' field in self completions, got %v", names)
	}
}

func BenchmarkCompletion(b *testing.B) {
	source := `import go "fmt"

type User {
    User(name: String, age: Int)
}

fun main() {
    let u = User(name: "Alice", age: 30)
    u.
    fmt.
}
`
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = getCompletionItems(source, "/tmp/bench.arca", 10, 9)
	}
}

// Test: todo example should not produce spurious undefined errors
func TestTodoNoUndefined(t *testing.T) {
	t.Parallel()
	source, err := os.ReadFile("examples/todo/main.arca")
	if err != nil {
		t.Skip("todo example not available")
	}
	diags := collectDiagnostics(string(source), "examples/todo/main.arca")
	for _, d := range diags {
		t.Logf("diag: line=%d col=%d msg=%q", d.Range.Start.Line+1, d.Range.Start.Character+1, d.Message)
	}
	for _, d := range diags {
		if strings.Contains(d.Message, "undefined") {
			t.Errorf("unexpected undefined error: %v - %s", d.Range.Start, d.Message)
		}
	}
}

// Test regression: match binding used inside MapLit should not be dropped
// (collectUsedIdents must walk MapLit)
func TestMatchBindingUsedInMapLit(t *testing.T) {
	t.Parallel()
	source := `import go "fmt"

fun process(r: Result[Int, error]) -> Map[String, error] {
    match r {
        Ok(_) => {"status": fmt.Errorf("ok")}
        Error(e) => {"error": e}
    }
}
`
	_ = source
	// Just verify emit doesn't produce "undefined: e"
	lexer := NewLexer(source)
	tokens, _ := lexer.Tokenize()
	parser := NewParser(tokens)
	prog, _ := parser.ParseProgram()
	lowerer := NewLowerer(prog, "", NewGoTypeResolver(""))
	irProg := lowerer.Lower(prog, "main", false)
	emitter := &Emitter{}
	code := emitter.Emit(irProg)
	t.Logf("emitted:\n%s", code)
	// Look for the error binding assignment
	if !strings.Contains(code, "e := ") && !strings.Contains(code, "e =") {
		t.Errorf("expected 'e := ...' binding in generated code")
	}
}

// Test: match arm binding should be accessible inside a map literal as Go FFI arg
func TestMatchBindingInMapLiteral(t *testing.T) {
	t.Parallel()
	source := `import go "net/http"

fun main() {
    let r: Result[Int, error] = Ok(42)
    match r {
        Ok(v) => http.StatusOK
        Error(e) => http.StatusInternalServerError
    }
    let m = {"error": "test"}
}
`
	diags := collectDiagnostics(source, "/tmp/match_map_test.arca")
	for _, d := range diags {
		t.Logf("diag: line=%d col=%d msg=%q", d.Range.Start.Line+1, d.Range.Start.Character+1, d.Message)
	}
}

// Debug: print scope tree
func TestScopeTreeDebug(t *testing.T) {
	source := `import go "fmt"

type User {
    User(name: String, age: Int)
}

fun greet(u: User) -> String {
    "Hello, ${u.name}!"
}
`
	lexer := NewLexer(source)
	tokens, err := lexer.Tokenize()
	if err != nil {
		t.Fatal(err)
	}
	parser := NewParser(tokens)
	prog, err := parser.ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	resolver := NewGoTypeResolver("")
	lowerer := NewLowerer(prog, "", resolver)
	lowerer.Lower(prog, "main", false)

	// Print scope tree
	var printScope func(s *Scope, depth int)
	printScope = func(s *Scope, depth int) {
		indent := strings.Repeat("  ", depth)
		t.Logf("%sscope start=%v end=%v", indent, s.StartPos, s.EndPos)
		for _, sym := range s.symbols {
			t.Logf("%s  sym: %s (%v)", indent, sym.Name, sym.Kind)
		}
		for _, c := range s.Children {
			printScope(c, depth+1)
		}
	}
	printScope(lowerer.rootScope, 0)

	// Try lookup at line 7 col 11 (parameter u)
	if scope := lowerer.rootScope.FindScopeAt(Pos{7, 11}); scope != nil {
		t.Logf("\nscope at 7:11: start=%v end=%v", scope.StartPos, scope.EndPos)
		if sym := scope.Lookup("u"); sym != nil {
			t.Logf("found u: %v", sym)
		} else {
			t.Logf("u NOT found at 7:11")
		}
	}
}

// Test hover on more complex code with constructor calls and match
func TestHoverComplex(t *testing.T) {
	t.Parallel()
	source := `import go "fmt"

type User {
    User(name: String, age: Int)
}

fun greet(u: User) -> String {
    "Hello, ${u.name}!"
}

fun main() {
    let u = User(name: "Alice", age: 30)
    fmt.Println(greet(u))
}
`
	cases := []struct {
		desc      string
		line, col int
	}{
		{"User type def", 3, 6},
		{"u parameter at col 11", 7, 11},
		{"u parameter at col 12", 7, 12},
		{"u in greet body col 15", 8, 15},
		{"User constructor", 12, 13},
		{"u variable in main", 12, 9},
		{"greet call", 13, 17},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			got := getHoverInfo(source, "/tmp/hover_complex.arca", tc.line, tc.col)
			if got == "" {
				t.Errorf("hover at line %d col %d returned empty", tc.line, tc.col)
			}
			t.Logf("[%s] line %d col %d → %q", tc.desc, tc.line, tc.col, got)
		})
	}
}

// Test hover on the actual todo example
func TestHoverTodoExample(t *testing.T) {
	t.Parallel()
	source, err := os.ReadFile("examples/todo/main.arca")
	if err != nil {
		t.Skip("todo example not available")
	}
	// Try a few positions
	cases := []struct {
		desc      string
		line, col int
	}{
		{"App type", 9, 7},        // type App
		{"db field", 10, 4},       // db field
		{"stdlib import", 7, 13},  // import stdlib
		{"app param", 41, 17},     // app: App
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			got := getHoverInfo(string(source), "examples/todo/main.arca", tc.line, tc.col)
			t.Logf("[%s] line %d col %d → %q", tc.desc, tc.line, tc.col, got)
		})
	}
}

// Test syntax error diagnostics
func TestDiagnosticsSyntaxError(t *testing.T) {
	t.Parallel()
	source := `import go "fmt"

fun main() {
    let x = unclosed
    fmt.Println(x
}
`
	diags := collectDiagnostics(source, "/tmp/syntax_test.arca")
	if len(diags) == 0 {
		t.Errorf("expected at least one diagnostic for syntax error, got none")
	}
	for _, d := range diags {
		t.Logf("diag: line=%d msg=%q", d.Range.Start.Line, d.Message)
	}
}

// Lower-phase errors (unused imports, undefined variables, type mismatches)
// must reach LSP diagnostics the same way parse errors do. Regression guard
// for the "I want it to show in the LSP too" requirement.
func TestDiagnosticsLowerErrors(t *testing.T) {
	t.Parallel()

	t.Run("unused_package", func(t *testing.T) {
		t.Parallel()
		source := `import go "strconv"
import go "time"

fun main() {
    let _ = strconv.Itoa(42)
}
`
		diags := collectDiagnostics(source, "/tmp/diag_unused.arca")
		if !hasDiag(diags, "unused package: time") {
			t.Errorf("expected 'unused package: time' diagnostic, got: %v", diagMessages(diags))
		}
		// Must be anchored at the import line (2, 0-indexed → 1).
		for _, d := range diags {
			if strings.Contains(d.Message, "unused package: time") && d.Range.Start.Line != 1 {
				t.Errorf("unused-import diagnostic anchored at line %d, want 1", d.Range.Start.Line)
			}
		}
	})

	t.Run("undefined_variable", func(t *testing.T) {
		t.Parallel()
		source := `fun main() {
    println(x)
}
`
		diags := collectDiagnostics(source, "/tmp/diag_undef.arca")
		if !hasDiag(diags, "undefined variable: x") {
			t.Errorf("expected 'undefined variable: x' diagnostic, got: %v", diagMessages(diags))
		}
	})

	t.Run("wrong_arg_count", func(t *testing.T) {
		t.Parallel()
		source := `fun add(a: Int, b: Int) -> Int { a + b }

fun main() {
    add(1)
}
`
		diags := collectDiagnostics(source, "/tmp/diag_argcount.arca")
		if !hasDiagContaining(diags, "expects 2 arguments, got 1") {
			t.Errorf("expected wrong-arg-count diagnostic, got: %v", diagMessages(diags))
		}
	})
}

func hasDiag(diags []protocol.Diagnostic, msg string) bool {
	for _, d := range diags {
		if d.Message == msg {
			return true
		}
	}
	return false
}

func hasDiagContaining(diags []protocol.Diagnostic, sub string) bool {
	for _, d := range diags {
		if strings.Contains(d.Message, sub) {
			return true
		}
	}
	return false
}

func diagMessages(diags []protocol.Diagnostic) []string {
	out := make([]string, len(diags))
	for i, d := range diags {
		out[i] = d.Message
	}
	return out
}

// Test that hover returns non-empty for common cases.
func TestHoverBasic(t *testing.T) {
	t.Parallel()
	source := `import go "fmt"

fun main() {
    let x = 42
    fmt.Println(x)
}
`
	cases := []struct {
		desc      string
		line, col int
		want      string // substring to look for
	}{
		{"variable x in let", 4, 9, "x"},
		{"variable x in fmt.Println", 5, 17, "x"},
		{"function main", 3, 5, "main"},
		{"fmt package", 5, 5, "fmt"},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			got := getHoverInfo(source, "/tmp/hover_test.arca", tc.line, tc.col)
			if got == "" {
				t.Errorf("hover at line %d col %d returned empty", tc.line, tc.col)
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("hover at line %d col %d:\n  got: %q\n  want substring: %q", tc.line, tc.col, got, tc.want)
			}
			fmt.Printf("[%s] line %d col %d → %s\n", tc.desc, tc.line, tc.col, got)
		})
	}
}
