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
		arg := "."
		if len(os.Args) >= 3 {
			arg = os.Args[2]
		}
		entry, err := resolveEntryPoint(arg)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(runCmd(entry))
	case "build":
		arg := "."
		if len(os.Args) >= 3 {
			arg = os.Args[2]
		}
		entry, err := resolveEntryPoint(arg)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		output := ""
		for i, a := range os.Args {
			if a == "-o" && i+1 < len(os.Args) {
				output = os.Args[i+1]
			}
		}
		os.Exit(buildCmd(entry, output))
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
	case "openapi":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: arca openapi <file.arca>")
			os.Exit(1)
		}
		os.Exit(openapiCmd(os.Args[2]))
	case "health":
		os.Exit(healthCmd())
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
	fmt.Fprintln(os.Stderr, "  run   [path]                Transpile and run (default: ./main.arca)")
	fmt.Fprintln(os.Stderr, "  build [path] [-o out]       Transpile and compile (default: ./main.arca)")
	fmt.Fprintln(os.Stderr, "  emit  <file.arca>            Output generated Go code")
	fmt.Fprintln(os.Stderr, "  fmt   <file.arca>            Format source code in place")
	fmt.Fprintln(os.Stderr, "  openapi <file.arca>          Generate OpenAPI spec")
	fmt.Fprintln(os.Stderr, "  health                       Check environment")
}

func resolveEntryPoint(arg string) (string, error) {
	// If arg is a .arca file, use directly
	if strings.HasSuffix(arg, ".arca") {
		return arg, nil
	}

	// If arg is a directory, look for main.arca inside
	info, err := os.Stat(arg)
	if err == nil && info.IsDir() {
		mainFile := filepath.Join(arg, "main.arca")
		if _, err := os.Stat(mainFile); err == nil {
			return mainFile, nil
		}
		return "", fmt.Errorf("no main.arca found in %s", arg)
	}

	// If no arg or ".", look in current directory
	if arg == "." || arg == "" {
		if _, err := os.Stat("main.arca"); err == nil {
			return "main.arca", nil
		}
		return "", fmt.Errorf("no main.arca found in current directory")
	}

	return "", fmt.Errorf("not a .arca file or directory: %s", arg)
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

type transpileResult struct {
	goCode      string
	goImports   []goImportEntry
}

func transpile(inputPath string) (*transpileResult, error) {
	prog, err := parseFile(inputPath)
	if err != nil {
		return nil, err
	}

	loaded := map[string]bool{inputPath: true}
	prog, err = resolveImports(inputPath, prog, loaded)
	if err != nil {
		return nil, err
	}

	checker := NewChecker()
	if errs := checker.Check(prog); len(errs) > 0 {
		var msgs []string
		for _, e := range errs {
			msgs = append(msgs, e.Message)
		}
		return nil, fmt.Errorf("type errors:\n  %s", strings.Join(msgs, "\n  "))
	}

	codegen := NewCodeGen(prog)
	code := codegen.Generate(prog)
	return &transpileResult{goCode: code, goImports: codegen.goImports}, nil
}

func isStdLib(pkg string) bool {
	// Go standard library packages don't contain dots in the first segment
	parts := strings.SplitN(pkg, "/", 2)
	return !strings.Contains(parts[0], ".")
}

func writeBuildDir(inputPath string, result *transpileResult) (string, error) {
	goCode := result.goCode
	dir := filepath.Join(filepath.Dir(inputPath), "build")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}

	// Clean old .go files
	oldFiles, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	for _, f := range oldFiles {
		os.Remove(f)
	}

	// Write main.go
	goFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(goFile, []byte(goCode), 0644); err != nil {
		return "", err
	}

	// Collect external dependencies
	var externalDeps []string
	for _, imp := range result.goImports {
		if !isStdLib(imp.path) {
			externalDeps = append(externalDeps, imp.path)
		}
	}

	// Write go.mod
	modFile := filepath.Join(dir, "go.mod")
	if _, err := os.Stat(modFile); os.IsNotExist(err) {
		cmd := exec.Command("go", "mod", "init", "arcabuild")
		cmd.Dir = dir
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("go mod init failed: %w", err)
		}
	}

	// Add external dependencies
	for _, dep := range externalDeps {
		cmd := exec.Command("go", "get", dep)
		cmd.Dir = dir
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("go get %s failed: %w", dep, err)
		}
	}

	// Tidy
	if len(externalDeps) > 0 {
		cmd := exec.Command("go", "mod", "tidy")
		cmd.Dir = dir
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("go mod tidy failed: %w", err)
		}
	}

	return dir, nil
}

func healthCmd() int {
	ok := true

	// Check Go
	const minGoMajor, minGoMinor = 1, 18 // generics required

	goPath, err := exec.LookPath("go")
	if err != nil {
		fmt.Println("  go: not found")
		ok = false
	} else {
		out, err := exec.Command("go", "version").Output()
		if err != nil {
			fmt.Println("  go: found but cannot run")
			ok = false
		} else {
			verStr := strings.TrimSpace(string(out))
			// Extract "1.24.3 darwin/amd64" from "go version go1.24.3 darwin/amd64"
			short := strings.TrimPrefix(verStr, "go version go")
			fmt.Printf("  go: %s (%s)\n", short, goPath)
			// Check minimum version
			var major, minor int
			if _, err := fmt.Sscanf(verStr, "go version go%d.%d", &major, &minor); err == nil {
				if major < minGoMajor || (major == minGoMajor && minor < minGoMinor) {
					fmt.Printf("  go: version %d.%d is too old, need >= %d.%d (generics)\n", major, minor, minGoMajor, minGoMinor)
					ok = false
				}
			}
		}
	}

	// Check Go env
	if goPath != "" {
		out, _ := exec.Command("go", "env", "GOPATH").Output()
		fmt.Printf("  GOPATH: %s\n", strings.TrimSpace(string(out)))
		out, _ = exec.Command("go", "env", "GOROOT").Output()
		fmt.Printf("  GOROOT: %s\n", strings.TrimSpace(string(out)))
	}

	// Test compile
	if goPath != "" {
		dir, err := os.MkdirTemp("", "arca-health-*")
		if err == nil {
			defer os.RemoveAll(dir)
			testFile := filepath.Join(dir, "main.go")
			os.WriteFile(testFile, []byte("package main\nfunc main() {}\n"), 0644)
			cmd := exec.Command("go", "build", "-o", filepath.Join(dir, "test"), testFile)
			if err := cmd.Run(); err != nil {
				fmt.Println("  compile: failed")
				ok = false
			} else {
				fmt.Println("  compile: ok")
			}
		}
	}

	if ok {
		fmt.Println("\nAll checks passed.")
		return 0
	}
	fmt.Println("\nSome checks failed.")
	return 1
}

func emitCmd(inputPath string) int {
	result, err := transpile(inputPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Print(result.goCode)
	return 0
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


func runCmd(inputPath string) int {
	result, err := transpile(inputPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	buildDir, err := writeBuildDir(inputPath, result)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error writing build: %v\n", err)
		return 1
	}

	cmd := exec.Command("go", "run", ".")
	cmd.Dir = buildDir
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
	result, err := transpile(inputPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	buildDir, err := writeBuildDir(inputPath, result)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error writing build: %v\n", err)
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

	cmd := exec.Command("go", "build", "-o", absOutput, ".")
	cmd.Dir = buildDir
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
