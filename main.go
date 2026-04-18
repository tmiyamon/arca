package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var showTimings bool

func main() {
	// Extract --timings flag from args
	var filteredArgs []string
	for _, a := range os.Args[1:] {
		if a == "--timings" {
			showTimings = true
		} else {
			filteredArgs = append(filteredArgs, a)
		}
	}
	os.Args = append([]string{os.Args[0]}, filteredArgs...)

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "version", "--version", "-v":
		fmt.Printf("arca %s\n", version)
		os.Exit(0)
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
	case "init":
		name := ""
		if len(os.Args) >= 3 {
			name = os.Args[2]
		}
		os.Exit(initCmd(name))
	case "lsp":
		os.Exit(lspCmd())
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
	fmt.Fprintln(os.Stderr, "  init  [name]                 Create a new project")
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

func formatError(file string, pos Pos, message string) string {
	if pos.Line > 0 {
		return fmt.Sprintf("%s:%d:%d: %s", file, pos.Line, pos.Col, message)
	}
	return fmt.Sprintf("%s: %s", file, message)
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
		return nil, fmt.Errorf("%s:%w", path, err)
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

		// Arca built-in package: pass through, no file lookup
		rootName := strings.Split(imp.Path, ".")[0]
		if lookupArcaPackage(rootName) != nil {
			merged.Decls = append(merged.Decls, decl)
			continue
		}

		// Arca module import — keep in merged for codegen to know module names
		merged.Decls = append(merged.Decls, decl)

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

		// Determine which names to import
		selectiveNames := make(map[string]bool)
		if len(imp.Names) > 0 {
			for _, n := range imp.Names {
				selectiveNames[n] = true
			}
		}

		// Include declarations from imported modules
		for _, d := range modProg.Decls {
			switch dd := d.(type) {
			case FnDecl:
				if dd.Public {
					if len(selectiveNames) > 0 {
						// Selective: only import named functions
						if selectiveNames[dd.Name] {
							merged.Decls = append(merged.Decls, d)
						}
					} else {
						merged.Decls = append(merged.Decls, d)
					}
				}
			case TypeDecl:
				// Types are always imported (needed for type checking)
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

func expandAliases(prog *Program) {
	// Collect alias → module name mapping
	aliases := map[string]string{}
	for _, decl := range prog.Decls {
		if imp, ok := decl.(ImportDecl); ok && imp.Alias != "" {
			parts := strings.Split(imp.Path, ".")
			moduleName := parts[len(parts)-1]
			aliases[imp.Alias] = moduleName
		}
	}
	if len(aliases) == 0 {
		return
	}

	// Walk all declarations and replace alias idents
	for i, decl := range prog.Decls {
		prog.Decls[i] = expandAliasesInDecl(decl, aliases)
	}
}

func expandAliasesInDecl(decl Decl, aliases map[string]string) Decl {
	switch d := decl.(type) {
	case FnDecl:
		d.Body = expandAliasesInExpr(d.Body, aliases)
		return d
	case TypeDecl:
		for i, m := range d.Methods {
			m.Body = expandAliasesInExpr(m.Body, aliases)
			d.Methods[i] = m
		}
		return d
	}
	return decl
}

func expandAliasesInExpr(expr Expr, aliases map[string]string) Expr {
	if expr == nil {
		return nil
	}
	switch e := expr.(type) {
	case FnCall:
		e.Fn = expandAliasesInExpr(e.Fn, aliases)
		for i, a := range e.Args {
			e.Args[i] = expandAliasesInExpr(a, aliases)
		}
		return e
	case FieldAccess:
		e.Expr = expandAliasesInExpr(e.Expr, aliases)
		return e
	case Ident:
		if moduleName, ok := aliases[e.Name]; ok {
			e.Name = moduleName
		}
		return e
	case Block:
		for i, s := range e.Stmts {
			e.Stmts[i] = expandAliasesInStmt(s, aliases)
		}
		e.Expr = expandAliasesInExpr(e.Expr, aliases)
		return e
	case MatchExpr:
		e.Subject = expandAliasesInExpr(e.Subject, aliases)
		for i, arm := range e.Arms {
			e.Arms[i].Body = expandAliasesInExpr(arm.Body, aliases)
		}
		return e
	case BinaryExpr:
		e.Left = expandAliasesInExpr(e.Left, aliases)
		e.Right = expandAliasesInExpr(e.Right, aliases)
		return e
	case ConstructorCall:
		for i, f := range e.Fields {
			e.Fields[i].Value = expandAliasesInExpr(f.Value, aliases)
		}
		return e
	case Lambda:
		e.Body = expandAliasesInExpr(e.Body, aliases)
		return e
	case StringInterp:
		for i, p := range e.Parts {
			e.Parts[i] = expandAliasesInExpr(p, aliases)
		}
		return e
	case ForExpr:
		e.Iter = expandAliasesInExpr(e.Iter, aliases)
		e.Body = expandAliasesInExpr(e.Body, aliases)
		return e
	case ListLit:
		for i, el := range e.Elements {
			e.Elements[i] = expandAliasesInExpr(el, aliases)
		}
		if e.Spread != nil {
			e.Spread = expandAliasesInExpr(e.Spread, aliases)
		}
		return e
	case TupleExpr:
		for i, el := range e.Elements {
			e.Elements[i] = expandAliasesInExpr(el, aliases)
		}
		return e
	case RefExpr:
		e.Expr = expandAliasesInExpr(e.Expr, aliases)
		return e
	}
	return expr
}

func expandAliasesInStmt(stmt Stmt, aliases map[string]string) Stmt {
	switch s := stmt.(type) {
	case LetStmt:
		s.Value = expandAliasesInExpr(s.Value, aliases)
		return s
	case ExprStmt:
		s.Expr = expandAliasesInExpr(s.Expr, aliases)
		return s
	case AssertStmt:
		s.Expr = expandAliasesInExpr(s.Expr, aliases)
		return s
	case DeferStmt:
		s.Expr = expandAliasesInExpr(s.Expr, aliases)
		return s
	}
	return stmt
}

type moduleCode struct {
	packageName string
	goCode      string
	isSubDir    bool // true = separate Go package directory
}

type transpileResult struct {
	goCode      string          // main module (for backwards compat)
	goImports   []goImportEntry
	modules     []moduleCode    // per-module Go code
	goModule    string          // go module path (e.g. "arcabuild")
}

type timings struct {
	parse    time.Duration
	lower    time.Duration
	validate time.Duration
	emit     time.Duration
	goBuild  time.Duration
}

func (t timings) print() {
	fmt.Fprintf(os.Stderr, "  parse:     %s\n", t.parse)
	fmt.Fprintf(os.Stderr, "  lower:     %s\n", t.lower)
	fmt.Fprintf(os.Stderr, "  validate:  %s\n", t.validate)
	fmt.Fprintf(os.Stderr, "  emit:      %s\n", t.emit)
	if t.goBuild > 0 {
		fmt.Fprintf(os.Stderr, "  go build:  %s\n", t.goBuild)
	}
	fmt.Fprintf(os.Stderr, "  total:     %s\n", t.parse+t.lower+t.validate+t.emit+t.goBuild)
}

var timing timings

func transpile(inputPath string) (*transpileResult, error) {
	t0 := time.Now()
	mainProg, err := parseFile(inputPath)
	if err != nil {
		return nil, err
	}

	// Expand aliases before resolving imports (only affects main file)
	expandAliases(mainProg)

	// Collect same-directory and sub-directory module programs
	dir := filepath.Dir(inputPath)
	subModules := map[string]*Program{}   // sub-directory modules
	sameDirFiles := map[string]*Program{} // same-directory .arca files
	collectAllModules(dir, mainProg, subModules, sameDirFiles, map[string]bool{inputPath: true})

	// Flat merge for validation
	loaded := map[string]bool{inputPath: true}
	mergedProg, err := resolveImports(inputPath, mainProg, loaded)
	if err != nil {
		return nil, err
	}
	timing.parse = time.Since(t0)

	t1 := time.Now()
	emitter := &Emitter{}
	goModDir := findGoModDir(filepath.Dir(inputPath))
	goModule := readGoModuleName(goModDir)
	resolver := NewGoTypeResolver(goModDir)

	// Generate per-file Go code for same-dir files
	var modules []moduleCode
	for fileName, fileProg := range sameDirFiles {
		lowerer := NewLowerer(fileProg, "", resolver)
		irProg := lowerer.Lower(fileProg, "main", true)
		code := emitter.Emit(irProg)
		modules = append(modules, moduleCode{packageName: fileName, goCode: code})
	}

	// Generate sub-directory module Go code
	for modName, modProg := range subModules {
		lowerer := NewLowerer(modProg, "", resolver)
		irProg := lowerer.Lower(modProg, modName, true)
		code := emitter.Emit(irProg)
		modules = append(modules, moduleCode{packageName: modName, goCode: code, isSubDir: true})
	}

	// Generate main file
	mainLowerer := NewLowerer(mergedProg, goModule, resolver)
	irProg := mainLowerer.Lower(mainProg, "main", false)
	timing.lower = time.Since(t1)

	// Collect lowering errors
	t2 := time.Now()
	if errs := mainLowerer.Errors(); len(errs) > 0 {
		return nil, &CompileErrors{File: inputPath, Errors: errs}
	}
	timing.validate = time.Since(t2)

	t3 := time.Now()
	// Add sub-module imports
	for modName := range subModules {
		irProg.Imports = append(irProg.Imports, IRImport{Path: goModule + "/" + modName})
	}
	mainCode := emitter.Emit(irProg)
	timing.emit = time.Since(t3)

	// Convert IR imports to goImportEntry for build system
	var goImports []goImportEntry
	for _, imp := range mainLowerer.imports {
		goImports = append(goImports, goImportEntry{path: imp.Path, sideEffect: imp.SideEffect})
	}

	return &transpileResult{
		goCode:    mainCode,
		goImports: goImports,
		modules:   modules,
		goModule:  goModule,
	}, nil
}

func collectAllModules(dir string, prog *Program, subModules map[string]*Program, sameDirFiles map[string]*Program, loaded map[string]bool) {
	for _, decl := range prog.Decls {
		imp, ok := decl.(ImportDecl)
		if !ok || strings.HasPrefix(imp.Path, "go/") {
			continue
		}
		// Skip Arca built-in packages
		rootName := strings.Split(imp.Path, ".")[0]
		if lookupArcaPackage(rootName) != nil {
			continue
		}

		modulePath := filepath.Join(dir, strings.ReplaceAll(imp.Path, ".", "/") + ".arca")
		if loaded[modulePath] {
			continue
		}
		loaded[modulePath] = true

		modProg, err := parseFile(modulePath)
		if err != nil {
			continue
		}

		if strings.Contains(imp.Path, ".") {
			// Sub-directory module
			parts := strings.Split(imp.Path, ".")
			modName := parts[len(parts)-1]
			subModules[modName] = modProg
			collectAllModules(filepath.Dir(modulePath), modProg, subModules, sameDirFiles, loaded)
		} else {
			// Same-directory file
			sameDirFiles[imp.Path] = modProg
		}
	}
}

func collectModulePrograms(dir string, prog *Program, modules map[string]*Program, loaded map[string]bool) {
	for _, decl := range prog.Decls {
		imp, ok := decl.(ImportDecl)
		if !ok || strings.HasPrefix(imp.Path, "go/") {
			continue
		}
		// Same-directory imports (no dots in path) → same package, skip
		if !strings.Contains(imp.Path, ".") {
			continue
		}

		modulePath := filepath.Join(dir, strings.ReplaceAll(imp.Path, ".", "/") + ".arca")
		if loaded[modulePath] {
			continue
		}
		loaded[modulePath] = true

		modProg, err := parseFile(modulePath)
		if err != nil {
			continue
		}

		parts := strings.Split(imp.Path, ".")
		modName := parts[len(parts)-1]
		modules[modName] = modProg

		// Recurse
		collectModulePrograms(filepath.Dir(modulePath), modProg, modules, loaded)
	}
}

// findGoModDir walks up from dir to find the nearest directory containing go.mod.
// Returns "" if no go.mod is found.
func findGoModDir(dir string) string {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(absDir, "go.mod")); err == nil {
			return absDir
		}
		parent := filepath.Dir(absDir)
		if parent == absDir {
			return "" // reached root
		}
		absDir = parent
	}
}

// readGoModuleName reads the module name from go.mod in the given directory.
// Returns "arcabuild" as fallback if go.mod is not found or unreadable.
func readGoModuleName(dir string) string {
	if dir == "" {
		return "arcabuild"
	}
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return "arcabuild"
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return "arcabuild"
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

	// Clean old .go files in root
	oldFiles, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	for _, f := range oldFiles {
		os.Remove(f)
	}

	// Write main.go
	goFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(goFile, []byte(goCode), 0644); err != nil {
		return "", err
	}

	// Write per-module Go files
	for _, mod := range result.modules {
		if mod.isSubDir {
			// Sub-directory module: build/modname/modname.go
			modDir := filepath.Join(dir, mod.packageName)
			if err := os.MkdirAll(modDir, 0755); err != nil {
				return "", err
			}
			modFile := filepath.Join(modDir, mod.packageName+".go")
			if err := os.WriteFile(modFile, []byte(mod.goCode), 0644); err != nil {
				return "", err
			}
		} else {
			// Same-directory file: build/filename.go
			modFile := filepath.Join(dir, mod.packageName+".go")
			if err := os.WriteFile(modFile, []byte(mod.goCode), 0644); err != nil {
				return "", err
			}
		}
	}

	// Copy go.mod/go.sum from project root (if exists)
	projectDir := findGoModDir(filepath.Dir(inputPath))
	if projectDir != "" {
		for _, name := range []string{"go.mod", "go.sum"} {
			src := filepath.Join(projectDir, name)
			if data, err := os.ReadFile(src); err == nil {
				if err := os.WriteFile(filepath.Join(dir, name), data, 0644); err != nil {
					return "", fmt.Errorf("failed to copy %s: %w", name, err)
				}
			}
		}
	} else {
		// No project go.mod: generate minimal go.mod for stdlib-only builds
		modFile := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(modFile); os.IsNotExist(err) {
			cmd := exec.Command("go", "mod", "init", "arcabuild")
			cmd.Dir = dir
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return "", fmt.Errorf("go mod init failed: %w", err)
			}
		}
	}

	// Extract any Arca built-in packages used by the program and add replace directives
	if pkgs := usedArcaPackages(result); len(pkgs) > 0 {
		for _, pkg := range pkgs {
			if err := pkg.extractTo(dir); err != nil {
				return "", fmt.Errorf("failed to extract arca package %s: %w", pkg.Name, err)
			}
		}
		if err := addArcaPackagesToGoMod(filepath.Join(dir, "go.mod"), pkgs); err != nil {
			return "", fmt.Errorf("failed to update go.mod: %w", err)
		}
	}

	return dir, nil
}

func initCmd(name string) int {
	if name == "" {
		fmt.Fprintln(os.Stderr, "Usage: arca init <project-name>")
		return 1
	}

	// Check if directory already exists
	if _, err := os.Stat(name); err == nil {
		fmt.Fprintf(os.Stderr, "Directory %s already exists\n", name)
		return 1
	}

	if err := os.MkdirAll(name, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating directory: %v\n", err)
		return 1
	}

	mainArca := `type Greeting {
  Hello(name: String)
  Goodbye(name: String)
}

fun message(g: Greeting) -> String {
  match g {
    Hello(name) -> "Hello, ${name}!"
    Goodbye(name) -> "Goodbye, ${name}!"
  }
}

fun main() {
  let greet = Greeting.Hello(name: "World")
  println(message(greet))
}
`

	mainPath := filepath.Join(name, "main.arca")
	if err := os.WriteFile(mainPath, []byte(mainArca), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing main.arca: %v\n", err)
		return 1
	}

	// Initialize go.mod
	cmd := exec.Command("go", "mod", "init", name)
	cmd.Dir = name
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing go.mod: %v\n", err)
		return 1
	}

	fmt.Printf("Created %s/\n", name)
	fmt.Println("")
	fmt.Println("  cd " + name)
	fmt.Println("  arca run")
	fmt.Println("")
	return 0
}

const version = "v0.1.0"

func healthCmd() int {
	arcaPath, _ := os.Executable()
	fmt.Printf("  arca: %s (%s)\n", version, arcaPath)
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
	if showTimings {
		timing.print()
	}
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

	t0 := time.Now()
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
	timing.goBuild = time.Since(t0)
	if showTimings {
		timing.print()
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

	t0 := time.Now()
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
	timing.goBuild = time.Since(t0)
	fmt.Fprintf(os.Stderr, "Built: %s\n", outputPath)
	if showTimings {
		timing.print()
	}
	return 0
}
