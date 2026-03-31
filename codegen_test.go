package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
