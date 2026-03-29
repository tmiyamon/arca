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
	case "fmt":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: arca fmt <file.arca>")
			os.Exit(1)
		}
		os.Exit(fmtCmd(os.Args[2]))
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
	fmt.Fprintln(os.Stderr, "  fmt   <file.arca>            Format source code in place")
}

func parseFile(path string) (*Program, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("error reading file %s: %w", path, err)
	}
	lexer := NewLexer(string(data))
	tokens, err := lexer.Tokenize()
	if err != nil {
		return nil, fmt.Errorf("%s: lexer error: %w", path, err)
	}
	parser := NewParser(tokens)
	prog, err := parser.ParseProgram()
	if err != nil {
		return nil, fmt.Errorf("%s: parse error: %w", path, err)
	}
	return prog, nil
}

func resolveImports(inputPath string, prog *Program, loaded map[string]bool) (*Program, error) {
	dir := filepath.Dir(inputPath)
	merged := &Program{}

	for _, decl := range prog.Decls {
		imp, ok := decl.(ImportDecl)
		if !ok {
			merged.Decls = append(merged.Decls, decl)
			continue
		}

		// Go imports pass through
		if strings.HasPrefix(imp.Path, "go/") {
			merged.Decls = append(merged.Decls, decl)
			continue
		}

		// Arca module import
		modulePath := filepath.Join(dir, strings.ReplaceAll(imp.Path, ".", "/") + ".arca")
		if loaded[modulePath] {
			continue
		}
		loaded[modulePath] = true

		modProg, err := parseFile(modulePath)
		if err != nil {
			return nil, err
		}

		// Recursively resolve imports in the imported module
		modProg, err = resolveImports(modulePath, modProg, loaded)
		if err != nil {
			return nil, err
		}

		// Only include pub declarations from imported modules
		for _, d := range modProg.Decls {
			switch dd := d.(type) {
			case FnDecl:
				if dd.Public {
					merged.Decls = append(merged.Decls, d)
				}
			case TypeDecl:
				// Types are always visible (needed for type checking)
				merged.Decls = append(merged.Decls, d)
			case ImportDecl:
				// Pass through Go imports from imported modules
				if strings.HasPrefix(dd.Path, "go/") {
					merged.Decls = append(merged.Decls, d)
				}
			}
		}
	}

	return merged, nil
}

func transpile(inputPath string) (string, error) {
	prog, err := parseFile(inputPath)
	if err != nil {
		return "", err
	}

	loaded := map[string]bool{inputPath: true}
	prog, err = resolveImports(inputPath, prog, loaded)
	if err != nil {
		return "", err
	}

	checker := NewChecker()
	if errs := checker.Check(prog); len(errs) > 0 {
		var msgs []string
		for _, e := range errs {
			msgs = append(msgs, e.Message)
		}
		return "", fmt.Errorf("type errors:\n  %s", strings.Join(msgs, "\n  "))
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

func fmtCmd(inputPath string) int {
	prog, err := parseFile(inputPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	formatter := NewFormatter()
	output := formatter.Format(prog)
	if err := os.WriteFile(inputPath, []byte(output), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing file: %v\n", err)
		return 1
	}
	return 0
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
