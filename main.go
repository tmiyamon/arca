package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "run":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: arca run <file.arca>")
			os.Exit(1)
		}
		os.Exit(runCmd(os.Args[2]))
	case "build":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: arca build <file.arca> [-o output]")
			os.Exit(1)
		}
		output := ""
		if len(os.Args) >= 5 && os.Args[3] == "-o" {
			output = os.Args[4]
		}
		os.Exit(buildCmd(os.Args[2], output))
	case "emit":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: arca emit <file.arca>")
			os.Exit(1)
		}
		os.Exit(emitCmd(os.Args[2]))
	default:
		// Backwards compat: if arg looks like a file, treat as emit
		if strings.HasSuffix(cmd, ".arca") {
			os.Exit(emitCmd(cmd))
		}
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage: arca <command> [arguments]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  run   <file.arca>           Transpile and run")
	fmt.Fprintln(os.Stderr, "  build <file.arca> [-o out]   Transpile and compile to binary")
	fmt.Fprintln(os.Stderr, "  emit  <file.arca>            Output generated Go code")
}

func transpile(inputPath string) (string, error) {
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return "", fmt.Errorf("error reading file: %w", err)
	}

	lexer := NewLexer(string(data))
	tokens, err := lexer.Tokenize()
	if err != nil {
		return "", fmt.Errorf("lexer error: %w", err)
	}

	parser := NewParser(tokens)
	prog, err := parser.ParseProgram()
	if err != nil {
		return "", fmt.Errorf("parse error: %w", err)
	}

	codegen := NewCodeGen(prog)
	return codegen.Generate(prog), nil
}

func writeBuildGo(inputPath string, goCode string) (string, error) {
	dir := filepath.Join(filepath.Dir(inputPath), "build")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}

	base := strings.TrimSuffix(filepath.Base(inputPath), ".arca")
	goFile := filepath.Join(dir, base+".go")
	if err := os.WriteFile(goFile, []byte(goCode), 0644); err != nil {
		return "", err
	}
	return goFile, nil
}

func emitCmd(inputPath string) int {
	goCode, err := transpile(inputPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Print(goCode)
	return 0
}

func runCmd(inputPath string) int {
	goCode, err := transpile(inputPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	goFile, err := writeBuildGo(inputPath, goCode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error writing build file: %v\n", err)
		return 1
	}

	cmd := exec.Command("go", "run", goFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "error running: %v\n", err)
		return 1
	}
	return 0
}

func buildCmd(inputPath string, outputPath string) int {
	goCode, err := transpile(inputPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	goFile, err := writeBuildGo(inputPath, goCode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error writing build file: %v\n", err)
		return 1
	}

	if outputPath == "" {
		base := strings.TrimSuffix(filepath.Base(inputPath), ".arca")
		outputPath = base
	}

	absOutput, err := filepath.Abs(outputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error resolving output path: %v\n", err)
		return 1
	}

	cmd := exec.Command("go", "build", "-o", absOutput, goFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "error building: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "Built: %s\n", outputPath)
	return 0
}
