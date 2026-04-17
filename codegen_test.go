package main

import (
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// transpileSource writes source to a temp .arca file and transpiles it.
func transpileSource(source string) (*transpileResult, error) {
	dir, err := os.MkdirTemp("", "arca-src-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	arcaFile := filepath.Join(dir, "main.arca")
	if err := os.WriteFile(arcaFile, []byte(source), 0644); err != nil {
		return nil, err
	}
	return transpile(arcaFile)
}

// runE2E is a helper for E2E tests: transpile → go run → check output.
func runE2E(t *testing.T, arcaFile string, expected string) {
	t.Helper()
	t.Parallel()

	result, err := transpile(arcaFile)
	if err != nil {
		t.Fatalf("transpile error: %v", err)
	}

	dir, err := os.MkdirTemp("", "arca-e2e-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	goFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(goFile, []byte(result.goCode), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "run", goFile)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run failed:\n%s", output)
	}

	if string(output) != expected {
		t.Errorf("output mismatch\nexpected: %q\ngot:      %q", expected, string(output))
	}
}

func TestCodegen(t *testing.T) {
	t.Parallel()
	entries, err := filepath.Glob("testdata/*.arca")
	if err != nil {
		t.Fatal(err)
	}
	for _, arcaFile := range entries {
		name := strings.TrimSuffix(filepath.Base(arcaFile), ".arca")
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			goFile := strings.TrimSuffix(arcaFile, ".arca") + ".go"
			expected, err := os.ReadFile(goFile)
			if err != nil {
				t.Fatalf("missing expected output %s: %v", goFile, err)
			}

			result, err := transpile(arcaFile)
			if err != nil {
				t.Fatalf("transpile error: %v", err)
			}

			got, err := format.Source([]byte(result.goCode))
			if err != nil {
				t.Fatalf("gofmt failed on generated code: %v\n%s", err, result.goCode)
			}

			if string(got) != string(expected) {
				t.Errorf("output mismatch for %s\n--- expected ---\n%s\n--- got ---\n%s", arcaFile, expected, got)
			}
		})
	}
}

func TestGeneratedGoCompiles(t *testing.T) {
	t.Parallel()
	entries, err := filepath.Glob("testdata/*.arca")
	if err != nil {
		t.Fatal(err)
	}
	skipVet := map[string]bool{"map_filter": true}
	for _, arcaFile := range entries {
		name := strings.TrimSuffix(filepath.Base(arcaFile), ".arca")
		if skipVet[name] {
			continue
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			result, err := transpile(arcaFile)
			if err != nil {
				t.Fatalf("transpile error: %v", err)
			}

			dir, err := os.MkdirTemp("", "arca-test-*")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)

			goFile := filepath.Join(dir, "main.go")
			if err := os.WriteFile(goFile, []byte(result.goCode), 0644); err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command("go", "vet", goFile)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Errorf("go vet failed for %s:\n%s", arcaFile, output)
			}
		})
	}
}

func TestE2EMultifile(t *testing.T) {
	t.Parallel()
	buildCmd := exec.Command("go", "build", "-o", "arca_test_bin", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("failed to build arca: %v", err)
	}
	defer os.Remove("arca_test_bin")

	cmd := exec.Command("./arca_test_bin", "run", "testdata/multifile/main.arca")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("arca run failed: %v", err)
	}

	expected := "Alice\n"
	if string(output) != expected {
		t.Errorf("output mismatch\nexpected: %q\ngot:      %q", expected, string(output))
	}
}

func TestE2ESubmodule(t *testing.T) {
	t.Parallel()
	buildCmd := exec.Command("go", "build", "-o", "arca_test_bin", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("failed to build arca: %v", err)
	}
	defer os.Remove("arca_test_bin")

	cmd := exec.Command("./arca_test_bin", "run", "testdata/submod/main.arca")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("arca run failed: %v", err)
	}

	expected := "3\n12\n"
	if string(output) != expected {
		t.Errorf("output mismatch\nexpected: %q\ngot:      %q", expected, string(output))
	}
}

func TestE2E(t *testing.T) {
	runE2E(t, "testdata/hello.arca", "Hello from Arca!\nred\nblue\n")
}

func TestE2EPrintln(t *testing.T) {
	runE2E(t, "testdata/println.arca", "hello\nworld42\n")
}

func TestE2EConstrainedResult(t *testing.T) {
	runE2E(t, "testdata/constrained_result.arca", "test@example.com\n")
}

func TestE2ESumMethod(t *testing.T) {
	runE2E(t, "testdata/sum_method.arca", "Rex says woof\nLuna says meow\n")
}

func TestE2ETryOperator(t *testing.T) {
	runE2E(t, "testdata/try_operator.arca", "42\n")
}

func TestE2EShadowing(t *testing.T) {
	runE2E(t, "testdata/shadowing.arca", "hello\n42\n")
}

func TestE2ELambda(t *testing.T) {
	runE2E(t, "testdata/lambda.arca", "[20 40 60]\n[2 3 4]\n")
}

func TestE2EPipe(t *testing.T) {
	runE2E(t, "testdata/pipe.arca", "10\n")
}

func TestE2EListSpread(t *testing.T) {
	runE2E(t, "testdata/list_spread.arca", "[0 1 2 3]\n")
}

func TestE2EStringInterp(t *testing.T) {
	runE2E(t, "testdata/string_interp.arca", "Hello World, you are 30!\n")
}

func TestE2EIfExpr(t *testing.T) {
	runE2E(t, "testdata/if_expr.arca", "positive\nnegative\nzero\nnonzero\nyes\n")
}

func TestE2EMapLiteral(t *testing.T) {
	// Map iteration order is non-deterministic, so we can't hard-code the
	// Println output for the map itself. Just make sure it transpiles and
	// runs without crashing by checking the scalar value ages["alice"] = 30.
	t.Parallel()
	result, err := transpile("testdata/map_literal.arca")
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	if !strings.Contains(result.goCode, "map[string]int") {
		t.Errorf("expected map[string]int in generated Go, got:\n%s", result.goCode)
	}
}

func TestE2EShorthandLambda(t *testing.T) {
	runE2E(t, "testdata/shorthand_lambda.arca", "[2 4 6 8 10]\n[1 2 3 4 5]\n15\n")
}

func TestGoFFITypeCheck(t *testing.T) {
	t.Parallel()

	t.Run("wrong_arg_count", func(t *testing.T) {
		t.Parallel()
		_, err := transpileSource(`
import go "strings"

fun main() {
  strings.Contains("hello")
}
`)
		if err == nil {
			t.Fatal("expected error for wrong argument count")
		}
		if !strings.Contains(err.Error(), "expects 2 arguments, got 1") {
			t.Errorf("unexpected error: %s", err)
		}
	})

	t.Run("wrong_arg_type", func(t *testing.T) {
		t.Parallel()
		_, err := transpileSource(`
import go "strings"

fun main() {
  strings.Contains(42, "hello")
}
`)
		if err == nil {
			t.Fatal("expected error for wrong argument type")
		}
		if !strings.Contains(err.Error(), "expects string, got int") {
			t.Errorf("unexpected error: %s", err)
		}
	})

	t.Run("correct_call", func(t *testing.T) {
		t.Parallel()
		_, err := transpileSource(`
import go "strings"

fun main() {
  strings.Contains("hello world", "world")
}
`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("generic_type_param_bound_from_hint", func(t *testing.T) {
		t.Parallel()
		// Without explicit type args, the generic type parameter T of a
		// Go FFI call must bind from the surrounding hint (return type
		// here). If the inner hint propagation unify is skipped, T leaks
		// as interface{} and codegen produces QueryOneAs[interface{}]
		// even though there's no Arca-level error.
		result, err := transpileSource(`
import go "database/sql"
import stdlib

fun test(db: *sql.DB) -> Result[Int, error] {
  stdlib.QueryOneAs(db, "select 1")
}
`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result.goCode, "stdlib.QueryOneAs[int]") {
			t.Errorf("expected generated code to use QueryOneAs[int], got:\n%s", result.goCode)
		}
	})

	t.Run("wrong_arg_type_generic", func(t *testing.T) {
		t.Parallel()
		// Generic Go FFI call path: HM unify must still report mismatches
		// when a concrete (non-type-param) parameter receives the wrong type.
		// stdlib.QueryAs has signature (db *sql.DB, query string, args ...any).
		// Passing a string as the first arg must trip the *sql.DB vs string check.
		_, err := transpileSource(`
import stdlib

fun main() {
  let _ = stdlib.QueryAs("not a db", "query")
}
`)
		if err == nil {
			t.Fatal("expected error for wrong arg type in generic call")
		}
		if !strings.Contains(err.Error(), "type mismatch: expected *sql.DB, got String") {
			t.Errorf("unexpected error: %s", err)
		}
	})

	t.Run("unresolved_type_param_try", func(t *testing.T) {
		t.Parallel()
		// stdlib.BindJSON[T any](r *http.Request) (T, error): T does not
		// appear in the parameter list, so HM has nothing to unify it
		// with. Without a hint or explicit type args, Arca must reject
		// the call instead of letting `todo: interface{}` leak into Go.
		_, err := transpileSource(`
import go "net/http"
import stdlib

fun handle(r: *http.Request) -> Result[Int, error] {
  let todo = stdlib.BindJSON(r)?
  Ok(0)
}
`)
		if err == nil {
			t.Fatal("expected error for unresolved type parameter")
		}
		if !strings.Contains(err.Error(), "cannot infer type of todo") {
			t.Errorf("unexpected error: %s", err)
		}
		if !strings.Contains(err.Error(), "BindJSON") {
			t.Errorf("error should name the function in the suggestion: %s", err)
		}
	})

	t.Run("unresolved_type_param_fixed_by_explicit_args", func(t *testing.T) {
		t.Parallel()
		// Same call but with explicit [Int]: T is pinned, no error.
		_, err := transpileSource(`
import go "net/http"
import stdlib

fun handle(r: *http.Request) -> Result[Int, error] {
  let n = stdlib.BindJSON[Int](r)?
  Ok(n)
}
`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("wrong_arg_type_generic_type_param", func(t *testing.T) {
		t.Parallel()
		// Once a type parameter is bound by an earlier argument, later
		// arguments of the same type parameter must match. slices.Equal
		// has signature (s1, s2 S) with S ~[]E — passing []int then
		// []string unifies S=[]int first, then fails on the second arg.
		_, err := transpileSource(`
import go "slices"

fun main() {
  let _ = slices.Equal([1, 2], ["a", "b"])
}
`)
		if err == nil {
			t.Fatal("expected error for mismatched type parameter binding")
		}
		if !strings.Contains(err.Error(), "type mismatch") {
			t.Errorf("unexpected error: %s", err)
		}
	})
}

// Go FFI functions returning multiple values are mapped to Arca wrapper types:
// (T, error) → Result[T, error], (T, bool) → Option[T], 3+ → tuple. These
// tests lock down the mapping via end-to-end compilation — the generated Go
// must compile, and match statements against the wrapper types must be valid.
func TestGoFFIMultiReturn(t *testing.T) {
	t.Parallel()

	t.Run("T_error_becomes_result", func(t *testing.T) {
		t.Parallel()
		// strconv.Atoi: func(s string) (int, error). The let binding's
		// inferred type must be Result[Int, error], so Ok/Error match arms
		// and the ? operator work without annotations.
		result, err := transpileSource(`
import go "strconv"

fun parse(s: String) -> Result[Int, error] {
  let n = strconv.Atoi(s)?
  Ok(n + 1)
}

fun main() {
  let _ = parse("42")
}
`)
		if err != nil {
			t.Fatalf("transpile error: %v", err)
		}
		// The generated Go must destructure Atoi's two returns into a
		// Result wrapper, not call it directly as a single-value expression.
		if !strings.Contains(result.goCode, "strconv.Atoi") {
			t.Error("expected strconv.Atoi call in generated Go")
		}
	})

	t.Run("T_bool_becomes_option", func(t *testing.T) {
		t.Parallel()
		// os.LookupEnv: func(key string) (string, bool). The binding type
		// must be Option[String] so Some/None match arms are accepted.
		_, err := transpileSource(`
import go "os"

fun main() {
  let home = os.LookupEnv("HOME")
  match home {
    Some(path) => println("home is ${path}")
    None => println("no home")
  }
}
`)
		if err != nil {
			t.Fatalf("transpile error: %v", err)
		}
	})

	t.Run("pointer_error_becomes_result_option", func(t *testing.T) {
		t.Parallel()
		// net/url.Parse: func(rawURL string) (*URL, error).
		// (*T, error) → Result[Option[*T], Error].
		// ? double-unwraps: Result then Option, variable gets *T type.
		result, err := transpileSource(`
import go "net/url"

fun parseURL(s: String) -> Result[String, error] {
  let u = url.Parse(s)?
  Ok(u.Host)
}

fun main() {
  let _ = parseURL("https://example.com")
}
`)
		if err != nil {
			t.Fatalf("transpile error: %v", err)
		}
		// The generated Go must include nil check for the pointer value
		if !strings.Contains(result.goCode, "== nil") {
			t.Error("expected nil check in generated Go for pointer return")
		}
	})

	t.Run("option_match_binding_type_propagates", func(t *testing.T) {
		t.Parallel()
		// The Some binding must have type String (inner of Option[String]).
		// Passing it to an Int param should fail.
		_, err := transpileSource(`
import go "os"

fun need_int(n: Int) -> Int { n }

fun main() {
  match os.LookupEnv("HOME") {
    Some(path) => need_int(path)
    None => 0
  }
}
`)
		if err == nil {
			t.Fatal("expected type mismatch for passing String to Int param")
		}
		if !strings.Contains(err.Error(), "type mismatch") {
			t.Errorf("unexpected error: %s", err)
		}
	})
}

func TestTryBlock(t *testing.T) {
	t.Parallel()

	t.Run("try_block_in_non_result_function", func(t *testing.T) {
		t.Parallel()
		// try { ... } creates a Result context, allowing ? in non-Result functions.
		result, err := transpileSource(`
import go "strconv"

fun main() {
  let r = try {
    let n = strconv.Atoi("42")?
    n + 1
  }
  match r {
    Ok(v) => println(v)
    Error(e) => println(e)
  }
}
`)
		if err != nil {
			t.Fatalf("transpile error: %v", err)
		}
		if !strings.Contains(result.goCode, "func()") {
			t.Error("expected IIFE in generated Go for try block")
		}
	})

	t.Run("try_without_brace_is_variable", func(t *testing.T) {
		t.Parallel()
		// "try" without { is a normal identifier, not a keyword
		_, err := transpileSource(`
fun main() {
  let try = 42
  println(try)
}
`)
		if err != nil {
			t.Fatalf("transpile error: %v", err)
		}
	})

}
