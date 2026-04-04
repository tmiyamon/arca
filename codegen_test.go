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

			if result.goCode != string(expected) {
				t.Errorf("output mismatch for %s\n--- expected ---\n%s\n--- got ---\n%s", arcaFile, expected, result.goCode)
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
}
