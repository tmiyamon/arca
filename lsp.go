package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tliron/commonlog"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/tliron/glsp/server"
)

const lspName = "arca-lsp"

var (
	lspHandler protocol.Handler
	lspVersion = version
)

// fileStore keeps in-memory file contents for open documents.
var fileStore = map[string]string{}

func lspCmd() int {
	commonlog.Configure(1, nil) // minimal logging

	lspHandler = protocol.Handler{
		Initialize:             lspInitialize,
		Initialized:            lspInitialized,
		Shutdown:               lspShutdown,
		TextDocumentDidOpen:    lspDidOpen,
		TextDocumentDidChange:  lspDidChange,
		TextDocumentDidClose:   lspDidClose,
		TextDocumentDidSave:    lspDidSave,
		TextDocumentHover:      lspHover,
	}

	srv := server.NewServer(&lspHandler, lspName, false)
	if err := srv.RunStdio(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func lspInitialize(ctx *glsp.Context, params *protocol.InitializeParams) (any, error) {
	syncKind := protocol.TextDocumentSyncKindFull
	capabilities := protocol.InitializeResult{
		Capabilities: protocol.ServerCapabilities{
			TextDocumentSync: &protocol.TextDocumentSyncOptions{
				OpenClose: boolPtr(true),
				Change:    &syncKind,
				Save:      &protocol.SaveOptions{IncludeText: boolPtr(true)},
			},
			HoverProvider: &protocol.HoverOptions{},
		},
		ServerInfo: &protocol.InitializeResultServerInfo{
			Name:    lspName,
			Version: &lspVersion,
		},
	}
	return capabilities, nil
}

func lspInitialized(ctx *glsp.Context, params *protocol.InitializedParams) error {
	return nil
}

func lspShutdown(ctx *glsp.Context) error {
	return nil
}

func lspDidOpen(ctx *glsp.Context, params *protocol.DidOpenTextDocumentParams) error {
	uri := params.TextDocument.URI
	fileStore[uri] = params.TextDocument.Text
	return lspDiagnose(ctx, uri, params.TextDocument.Text)
}

func lspDidChange(ctx *glsp.Context, params *protocol.DidChangeTextDocumentParams) error {
	uri := params.TextDocument.URI
	if len(params.ContentChanges) > 0 {
		// Full sync — last change has the full text
		if change, ok := params.ContentChanges[len(params.ContentChanges)-1].(protocol.TextDocumentContentChangeEventWhole); ok {
			fileStore[uri] = change.Text
			return lspDiagnose(ctx, uri, change.Text)
		}
	}
	return nil
}

func lspDidClose(ctx *glsp.Context, params *protocol.DidCloseTextDocumentParams) error {
	delete(fileStore, params.TextDocument.URI)
	return nil
}

func lspDidSave(ctx *glsp.Context, params *protocol.DidSaveTextDocumentParams) error {
	uri := params.TextDocument.URI
	if params.Text != nil {
		fileStore[uri] = *params.Text
		return lspDiagnose(ctx, uri, *params.Text)
	}
	if text, ok := fileStore[uri]; ok {
		return lspDiagnose(ctx, uri, text)
	}
	return nil
}

// --- Diagnostics ---

func lspDiagnose(ctx *glsp.Context, uri string, source string) error {
	filePath := strings.TrimPrefix(string(uri), "file://")
	diagnostics := collectDiagnostics(source, filePath)
	ctx.Notify(protocol.ServerTextDocumentPublishDiagnostics, protocol.PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: diagnostics,
	})
	return nil
}

func collectDiagnostics(source string, filePath string) []protocol.Diagnostic {
	diagnostics := []protocol.Diagnostic{} // must be empty slice, not nil (Neovim requires it)
	severity := protocol.DiagnosticSeverityError

	// Parse
	lexer := NewLexer(source)
	tokens, err := lexer.Tokenize()
	if err != nil {
		diagnostics = append(diagnostics, protocol.Diagnostic{
			Range:    posToRange(extractPosFromError(err.Error())),
			Severity: &severity,
			Source:   strPtr(lspName),
			Message:  err.Error(),
		})
		return diagnostics
	}

	parser := NewParser(tokens)
	prog, err := parser.ParseProgram()
	if err != nil {
		pos := extractPosFromError(err.Error())
		diagnostics = append(diagnostics, protocol.Diagnostic{
			Range:    posToRange(pos),
			Severity: &severity,
			Source:   strPtr(lspName),
			Message:  err.Error(),
		})
		return diagnostics
	}

	// Lower → validate
	goModDir := findGoModDir(filepath.Dir(filePath))
	resolver := NewGoTypeResolver(goModDir)
	lowerer := NewLowerer(prog, "", resolver)
	irProg := lowerer.Lower(prog, "main", false)

	for _, e := range lowerer.Errors() {
		diagnostics = append(diagnostics, protocol.Diagnostic{
			Range:    posToRange(e.Pos),
			Severity: &severity,
			Source:   strPtr(lspName),
			Message:  e.Message,
		})
	}

	validator := NewIRValidation(lowerer)
	for _, e := range validator.Validate(irProg) {
		diagnostics = append(diagnostics, protocol.Diagnostic{
			Range:    posToRange(e.Pos),
			Severity: &severity,
			Source:   strPtr(lspName),
			Message:  e.Message,
		})
	}

	return diagnostics
}

// --- Hover ---

func lspHover(ctx *glsp.Context, params *protocol.HoverParams) (*protocol.Hover, error) {
	uri := params.TextDocument.URI
	source, ok := fileStore[uri]
	if !ok {
		return nil, nil
	}

	line := int(params.Position.Line) + 1     // LSP is 0-based, Arca is 1-based
	col := int(params.Position.Character) + 1

	filePath := strings.TrimPrefix(string(uri), "file://")
	info := getHoverInfo(source, filePath, line, col)
	if info == "" {
		return nil, nil
	}

	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.MarkupKindMarkdown,
			Value: info,
		},
	}, nil
}

func getHoverInfo(source string, filePath string, line, col int) string {
	// Parse and lower to get type info
	lexer := NewLexer(source)
	tokens, err := lexer.Tokenize()
	if err != nil {
		return ""
	}

	parser := NewParser(tokens)
	prog, err := parser.ParseProgram()
	if err != nil {
		return ""
	}

	goModDir := findGoModDir(filepath.Dir(filePath))
	resolver := NewGoTypeResolver(goModDir)
	lowerer := NewLowerer(prog, "", resolver)
	lowerer.Lower(prog, "main", false)

	// Find the token at the given position
	word := getWordAt(source, line, col)
	if word == "" {
		return ""
	}

	// Check if this is a field access: receiver.field
	receiver := getReceiverAt(source, line, col)
	if receiver != "" {
		if sym := lowerer.LookupSymbol(receiver); sym != nil {
			if fieldType := lookupFieldType(lowerer.Types(), sym.Type, word); fieldType != nil {
				return fmt.Sprintf("```arca\n%s: %s\n```", word, typeName(fieldType))
			}
		}
	}

	// Look up local variables and parameters
	if sym := lowerer.LookupSymbol(word); sym != nil {
		return fmt.Sprintf("```arca\n%s %s: %s\n```", sym.Kind, sym.Name, typeName(sym.Type))
	}

	// Look up functions
	functions := lowerer.Functions()
	if fn, ok := functions[word]; ok {
		return formatFnHover(fn)
	}

	// Look up methods (including static fun) in all types
	types := lowerer.Types()
	for _, td := range types {
		for _, m := range td.Methods {
			if m.Name == word {
				return formatMethodHover(td.Name, m)
			}
		}
	}

	// Look up types
	if td, ok := types[word]; ok {
		return formatTypeHover(td)
	}

	// Look up type aliases
	typeAliases := lowerer.TypeAliases()
	if ta, ok := typeAliases[word]; ok {
		return formatTypeAliasHover(ta)
	}

	return ""
}

func formatFnHover(fn FnDecl) string {
	params := make([]string, len(fn.Params))
	for i, p := range fn.Params {
		params[i] = fmt.Sprintf("%s: %s", p.Name, typeName(p.Type))
	}
	ret := ""
	if fn.ReturnType != nil {
		ret = " -> " + typeName(fn.ReturnType)
	}
	return fmt.Sprintf("```arca\nfun %s(%s)%s\n```", fn.Name, strings.Join(params, ", "), ret)
}

func formatTypeHover(td TypeDecl) string {
	if isEnum(td) {
		variants := make([]string, len(td.Constructors))
		for i, c := range td.Constructors {
			variants[i] = c.Name
		}
		return fmt.Sprintf("```arca\ntype %s { %s }\n```", td.Name, strings.Join(variants, ", "))
	}
	if len(td.Constructors) == 1 {
		ctor := td.Constructors[0]
		fields := make([]string, len(ctor.Fields))
		for i, f := range ctor.Fields {
			fields[i] = fmt.Sprintf("%s: %s", f.Name, typeName(f.Type))
		}
		return fmt.Sprintf("```arca\ntype %s(%s)\n```", td.Name, strings.Join(fields, ", "))
	}
	return fmt.Sprintf("```arca\ntype %s\n```", td.Name)
}

func formatMethodHover(ownerType string, fn FnDecl) string {
	params := make([]string, len(fn.Params))
	for i, p := range fn.Params {
		params[i] = fmt.Sprintf("%s: %s", p.Name, typeName(p.Type))
	}
	ret := ""
	if fn.ReturnType != nil {
		ret = " -> " + typeName(fn.ReturnType)
	}
	prefix := "fun"
	if fn.Static {
		prefix = "static fun"
	}
	return fmt.Sprintf("```arca\n%s %s.%s(%s)%s\n```", prefix, ownerType, fn.Name, strings.Join(params, ", "), ret)
}

func formatTypeAliasHover(ta TypeAliasDecl) string {
	return fmt.Sprintf("```arca\ntype %s = %s\n```", ta.Name, typeName(ta.Type))
}

// --- Helpers ---

// getReceiverAt returns the identifier before a dot if the cursor is on a field/method name.
// e.g. for "user.email" with cursor on "email", returns "user".
func getReceiverAt(source string, line, col int) string {
	lines := strings.Split(source, "\n")
	if line < 1 || line > len(lines) {
		return ""
	}
	lineText := lines[line-1]

	// Find the start of the current word
	start := col - 1
	for start > 0 && isIdentChar(lineText[start-1]) {
		start--
	}
	// Check if there's a dot before
	if start < 1 || lineText[start-1] != '.' {
		return ""
	}
	// Find the receiver word before the dot
	dotPos := start - 1
	end := dotPos
	recStart := end
	for recStart > 0 && isIdentChar(lineText[recStart-1]) {
		recStart--
	}
	if recStart == end {
		return ""
	}
	return lineText[recStart:end]
}

// lookupFieldType finds a field's type in an Arca type definition.
func lookupFieldType(types map[string]TypeDecl, ownerType Type, fieldName string) Type {
	if ownerType == nil {
		return nil
	}
	nt, ok := ownerType.(NamedType)
	if !ok {
		return nil
	}
	td, ok := types[nt.Name]
	if !ok {
		return nil
	}
	if f := findField(td, fieldName); f != nil {
		return f.Type
	}
	return nil
}

func getWordAt(source string, line, col int) string {
	lines := strings.Split(source, "\n")
	if line < 1 || line > len(lines) {
		return ""
	}
	lineText := lines[line-1]
	if col < 1 || col > len(lineText)+1 {
		return ""
	}

	// Find word boundaries
	start := col - 1
	end := col - 1
	for start > 0 && isIdentChar(lineText[start-1]) {
		start--
	}
	for end < len(lineText) && isIdentChar(lineText[end]) {
		end++
	}
	if start == end {
		return ""
	}
	return lineText[start:end]
}

func isIdentChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

func posToRange(pos Pos) protocol.Range {
	line := uint32(0)
	char := uint32(0)
	if pos.Line > 0 {
		line = uint32(pos.Line - 1)
	}
	if pos.Col > 0 {
		char = uint32(pos.Col - 1)
	}
	return protocol.Range{
		Start: protocol.Position{Line: line, Character: char},
		End:   protocol.Position{Line: line, Character: char},
	}
}

func extractPosFromError(msg string) Pos {
	// Parse "line:col: message" format
	var line, col int
	if _, err := fmt.Sscanf(msg, "%d:%d:", &line, &col); err == nil {
		return Pos{Line: line, Col: col}
	}
	return Pos{Line: 1, Col: 1}
}

func boolPtr(b bool) *bool    { return &b }
func strPtr(s string) *string { return &s }
