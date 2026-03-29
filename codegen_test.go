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

			got, err := transpile(arcaFile)
			if err != nil {
				t.Fatalf("transpile error: %v", err)
			}

			if got != string(expected) {
				t.Errorf("output mismatch for %s\n--- expected ---\n%s\n--- got ---\n%s", arcaFile, expected, got)
			}
		})
	}
}

func TestGeneratedGoCompiles(t *testing.T) {
	entries, err := filepath.Glob("testdata/*.arca")
	if err != nil {
		t.Fatal(err)
	}
	for _, arcaFile := range entries {
		name := strings.TrimSuffix(filepath.Base(arcaFile), ".arca")
		t.Run(name, func(t *testing.T) {
			goCode, err := transpile(arcaFile)
			if err != nil {
				t.Fatalf("transpile error: %v", err)
			}

			dir, err := os.MkdirTemp("", "arca-test-*")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)

			goFile := filepath.Join(dir, "main.go")
			if err := os.WriteFile(goFile, []byte(goCode), 0644); err != nil {
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

func TestE2E(t *testing.T) {
	arcaFile := "testdata/hello.arca"
	goCode, err := transpile(arcaFile)
	if err != nil {
		t.Fatalf("transpile error: %v", err)
	}

	dir, err := os.MkdirTemp("", "arca-e2e-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	goFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(goFile, []byte(goCode), 0644); err != nil {
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
