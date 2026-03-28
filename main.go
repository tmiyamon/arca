package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: mlgo <file.ml>")
		os.Exit(1)
	}

	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
		os.Exit(1)
	}

	lexer := NewLexer(string(data))
	tokens, err := lexer.Tokenize()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Lexer error: %v\n", err)
		os.Exit(1)
	}

	parser := NewParser(tokens)
	prog, err := parser.ParseProgram()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Parse error: %v\n", err)
		os.Exit(1)
	}

	codegen := NewCodeGen(prog)
	output := codegen.Generate(prog)
	fmt.Print(output)
}
