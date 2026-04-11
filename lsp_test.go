package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
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
