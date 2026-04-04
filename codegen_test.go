package main

import (
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

func TestCodegen(t *testing.T) {
	entries, err := filepath.Glob("testdata/*.arca")
	if err != nil {
		t.Fatal(err)
	}
	for _, arcaFile := range entries {
		name := strings.TrimSuffix(filepath.Base(arcaFile), ".arca")
		t.Run(name, func(t *testing.T) {
			goFile := strings.TrimSuffix(arcaFile, ".arca") + ".go"
			expected, err := os.ReadFile(goFile)
			if err != nil {
				t.Fatalf("missing expected output %s: %v", goFile, err)
			}

			result, err := transpile(arcaFile)
			if err != nil {
				t.Fatalf("transpile error: %v", err)
			}

			if result.goCode != string(expected) {
				t.Errorf("output mismatch for %s\n--- expected ---\n%s\n--- got ---\n%s", arcaFile, expected, result.goCode)
			}
		})
	}
}

func TestGeneratedGoCompiles(t *testing.T) {
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
	buildCmd := exec.Command("go", "build", "-o", "arca_test_bin", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("failed to build arca: %v", err)
	}
	defer os.Remove("arca_test_bin")

	cmd := exec.Command("./arca_test_bin", "run", "testdata/multifile/main.arca")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("arca run failed:\n%s", output)
	}

	expected := "Alice\n"
	if string(output) != expected {
		t.Errorf("output mismatch\nexpected: %q\ngot:      %q", expected, string(output))
	}
}

func TestE2ESubmodule(t *testing.T) {
	// Build arca binary first
	buildCmd := exec.Command("go", "build", "-o", "arca_test_bin", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("failed to build arca: %v", err)
	}
	defer os.Remove("arca_test_bin")

	cmd := exec.Command("./arca_test_bin", "run", "testdata/submod/main.arca")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("arca run failed:\n%s", output)
	}

	expected := "3\n12\n"
	if string(output) != expected {
		t.Errorf("output mismatch\nexpected: %q\ngot:      %q", expected, string(output))
	}
}

func TestE2EPrintln(t *testing.T) {
	arcaFile := "testdata/println.arca"
	result, err := transpile(arcaFile)
	if err != nil {
		t.Fatalf("transpile error: %v", err)
	}

	dir, err := os.MkdirTemp("", "arca-e2e-println-*")
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

	expected := "hello\nworld42\n"
	if string(output) != expected {
		t.Errorf("output mismatch\nexpected: %q\ngot:      %q", expected, string(output))
	}
}

func TestE2EConstrainedResult(t *testing.T) {
	arcaFile := "testdata/constrained_result.arca"
	result, err := transpile(arcaFile)
	if err != nil {
		t.Fatalf("transpile error: %v", err)
	}

	dir, err := os.MkdirTemp("", "arca-e2e-constrained-result-*")
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

	expected := "test@example.com\n"
	if string(output) != expected {
		t.Errorf("output mismatch\nexpected: %q\ngot:      %q", expected, string(output))
	}
}

func TestE2ESumMethod(t *testing.T) {
	arcaFile := "testdata/sum_method.arca"
	result, err := transpile(arcaFile)
	if err != nil {
		t.Fatalf("transpile error: %v", err)
	}

	dir, err := os.MkdirTemp("", "arca-e2e-sum-method-*")
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

	expected := "Rex says woof\nLuna says meow\n"
	if string(output) != expected {
		t.Errorf("output mismatch\nexpected: %q\ngot:      %q", expected, string(output))
	}
}

func TestE2ETryOperator(t *testing.T) {
	arcaFile := "testdata/try_operator.arca"
	result, err := transpile(arcaFile)
	if err != nil {
		t.Fatalf("transpile error: %v", err)
	}

	dir, err := os.MkdirTemp("", "arca-e2e-try-operator-*")
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

	expected := "42\n"
	if string(output) != expected {
		t.Errorf("output mismatch\nexpected: %q\ngot:      %q", expected, string(output))
	}
}

func TestE2EShadowing(t *testing.T) {
	arcaFile := "testdata/shadowing.arca"
	result, err := transpile(arcaFile)
	if err != nil {
		t.Fatalf("transpile error: %v", err)
	}

	dir, err := os.MkdirTemp("", "arca-e2e-shadowing-*")
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

	expected := "hello\n42\n"
	if string(output) != expected {
		t.Errorf("output mismatch\nexpected: %q\ngot:      %q", expected, string(output))
	}
}

func TestE2ELambda(t *testing.T) {
	arcaFile := "testdata/lambda.arca"
	result, err := transpile(arcaFile)
	if err != nil {
		t.Fatalf("transpile error: %v", err)
	}

	dir, err := os.MkdirTemp("", "arca-e2e-lambda-*")
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

	expected := "[20 40 60]\n[2 3 4]\n"
	if string(output) != expected {
		t.Errorf("output mismatch\nexpected: %q\ngot:      %q", expected, string(output))
	}
}

func TestE2EPipe(t *testing.T) {
	arcaFile := "testdata/pipe.arca"
	result, err := transpile(arcaFile)
	if err != nil {
		t.Fatalf("transpile error: %v", err)
	}

	dir, err := os.MkdirTemp("", "arca-e2e-pipe-*")
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

	expected := "10\n"
	if string(output) != expected {
		t.Errorf("output mismatch\nexpected: %q\ngot:      %q", expected, string(output))
	}
}

func TestE2EListSpread(t *testing.T) {
	arcaFile := "testdata/list_spread.arca"
	result, err := transpile(arcaFile)
	if err != nil {
		t.Fatalf("transpile error: %v", err)
	}

	dir, err := os.MkdirTemp("", "arca-e2e-list-spread-*")
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

	expected := "[0 1 2 3]\n"
	if string(output) != expected {
		t.Errorf("output mismatch\nexpected: %q\ngot:      %q", expected, string(output))
	}
}

func TestE2EStringInterp(t *testing.T) {
	arcaFile := "testdata/string_interp.arca"
	result, err := transpile(arcaFile)
	if err != nil {
		t.Fatalf("transpile error: %v", err)
	}

	dir, err := os.MkdirTemp("", "arca-e2e-string-interp-*")
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

	expected := "Hello World, you are 30!\n"
	if string(output) != expected {
		t.Errorf("output mismatch\nexpected: %q\ngot:      %q", expected, string(output))
	}
}

func TestGoFFITypeCheck(t *testing.T) {
	// Test 1: Wrong argument count for a Go function
	t.Run("wrong_arg_count", func(t *testing.T) {
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

	// Test 2: Wrong argument type for a Go function
	t.Run("wrong_arg_type", func(t *testing.T) {
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

	// Test 3: Correct Go FFI call compiles fine
	t.Run("correct_call", func(t *testing.T) {
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
}

func TestE2E(t *testing.T) {
	arcaFile := "testdata/hello.arca"
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

	expected := "Hello from Arca!\nred\nblue\n"
	if string(output) != expected {
		t.Errorf("output mismatch\nexpected: %q\ngot:      %q", expected, string(output))
	}
}
