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
		TextDocumentDefinition: lspDefinition,
		TextDocumentCompletion: lspCompletion,
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
			HoverProvider:      &protocol.HoverOptions{},
			DefinitionProvider: &protocol.DefinitionOptions{},
			CompletionProvider: &protocol.CompletionOptions{
				TriggerCharacters: []string{"."},
			},
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
			Message:  e.Message(),
		})
	}

	validator := NewIRValidation(lowerer)
	for _, e := range validator.Validate(irProg) {
		diagnostics = append(diagnostics, protocol.Diagnostic{
			Range:    posToRange(e.Pos),
			Severity: &severity,
			Source:   strPtr(lspName),
			Message:  e.Message(),
		})
	}

	return diagnostics
}

// --- Hover ---

func lspDefinition(ctx *glsp.Context, params *protocol.DefinitionParams) (any, error) {
	uri := params.TextDocument.URI
	source, ok := fileStore[uri]
	if !ok {
		return nil, nil
	}

	line := int(params.Position.Line) + 1
	col := int(params.Position.Character) + 1

	filePath := strings.TrimPrefix(string(uri), "file://")
	defFile, defPos := getDefinitionLocation(source, filePath, line, col)
	if defPos.Line == 0 {
		return nil, nil
	}

	// Default to the current file's URI; override if definition is elsewhere (Go FFI)
	defURI := uri
	if defFile != "" && defFile != filePath {
		defURI = "file://" + defFile
	}

	return protocol.Location{
		URI:   defURI,
		Range: posToRange(defPos),
	}, nil
}

// getDefinitionPos is kept for testing within the current file.
func getDefinitionPos(source string, filePath string, line, col int) Pos {
	_, pos := getDefinitionLocation(source, filePath, line, col)
	return pos
}

// getDefinitionLocation returns (file, pos) for the definition of the symbol at
// the given position. file is "" if the definition is in the same source.
func getDefinitionLocation(source string, filePath string, line, col int) (string, Pos) {
	// Parse and lower
	lexer := NewLexer(source)
	tokens, err := lexer.Tokenize()
	if err != nil {
		return "", Pos{}
	}
	parser := NewParser(tokens)
	prog, err := parser.ParseProgram()
	if err != nil {
		return "", Pos{}
	}
	goModDir := findGoModDir(filepath.Dir(filePath))
	resolver := NewGoTypeResolver(goModDir)
	lowerer := NewLowerer(prog, "", resolver)
	lowerer.Lower(prog, "main", false)

	word := getWordAt(source, line, col)
	if word == "" {
		return "", Pos{}
	}

	// Go FFI: package.member or receiver.method
	receiver := getReceiverAt(source, line, col)
	if receiver != "" {
		if sym := lowerer.FindSymbolAt(receiver, Pos{line, col}); sym != nil {
			// Go package-level member: fmt.Println, http.StatusOK
			if sym.Kind == SymPackage {
				if pkg, ok := lowerer.GoPackages()[sym.Name]; ok {
					if file, pos := resolver.MemberPos(pkg.FullPath, word); pos.Line != 0 {
						return file, pos
					}
				}
			}
			// Go method on a typed receiver: e.Start, db.Query
			if sym.IRType != nil {
				if shortPkg, typeName := extractPackageAndType(sym.IRType); shortPkg != "" {
					if pkg, ok := lowerer.GoPackages()[shortPkg]; ok {
						if file, pos := resolver.MethodPos(pkg.FullPath, typeName, word); pos.Line != 0 {
							return file, pos
						}
					}
				}
			}
		}
	}

	// Look up symbol via scope tree
	if sym := lowerer.FindSymbolAt(word, Pos{line, col}); sym != nil {
		return "", sym.Pos
	}

	// Function lookup (for calls to pub functions etc.)
	if fn, ok := lowerer.Functions()[word]; ok {
		return "", fn.NamePos
	}

	// Type lookup
	if td, ok := lowerer.Types()[word]; ok {
		return "", td.Pos
	}

	return "", Pos{}
}

// extractPackageAndType returns the Go package path and type name from an IR type.
// Returns ("", "") if the type is not a Go-qualified named type.
func extractPackageAndType(t IRType) (string, string) {
	named, ok := t.(IRNamedType)
	if !ok {
		return "", ""
	}
	// GoName may be "*echo.Context" or "echo.Context"
	name := strings.TrimPrefix(named.GoName, "*")
	parts := strings.SplitN(name, ".", 2)
	if len(parts) != 2 {
		return "", ""
	}
	// We need the full package path, not just the short name.
	// This is a best-effort: short-name lookup via resolver isn't straightforward here.
	// Just return as "shortName", "TypeName" and let caller figure out path.
	return parts[0], parts[1]
}

func lspCompletion(ctx *glsp.Context, params *protocol.CompletionParams) (any, error) {
	uri := params.TextDocument.URI
	source, ok := fileStore[uri]
	if !ok {
		return nil, nil
	}

	line := int(params.Position.Line) + 1
	col := int(params.Position.Character) + 1

	filePath := strings.TrimPrefix(string(uri), "file://")
	items := getCompletionItems(source, filePath, line, col)
	if items == nil {
		return nil, nil
	}
	return items, nil
}

// getCompletionItems returns completion items at the given position.
// Currently only supports `.` completion after a receiver identifier.
func getCompletionItems(source string, filePath string, line, col int) []protocol.CompletionItem {
	// Find the receiver just before the cursor (must be immediately after a dot)
	receiver := getReceiverBeforeDot(source, line, col)
	if receiver == "" {
		return nil
	}

	// Insert a placeholder identifier after the dot so the source parses.
	patched := insertCompletionPlaceholder(source, line, col)

	// Parse and lower the patched source
	lexer := NewLexer(patched)
	tokens, err := lexer.Tokenize()
	if err != nil {
		return nil
	}
	parser := NewParser(tokens)
	prog, err := parser.ParseProgram()
	if err != nil {
		return nil
	}
	goModDir := findGoModDir(filepath.Dir(filePath))
	resolver := NewGoTypeResolver(goModDir)
	lowerer := NewLowerer(prog, "", resolver)
	lowerer.Lower(prog, "main", false)

	sym := lowerer.FindSymbolAt(receiver, Pos{line, col})
	if sym == nil {
		return nil
	}

	var items []protocol.CompletionItem

	// Package members
	if sym.Kind == SymPackage {
		if pkg, ok := lowerer.GoPackages()[sym.Name]; ok {
			for _, m := range resolver.PackageMembers(pkg.FullPath) {
				items = append(items, memberToCompletion(m))
			}
		}
		return items
	}

	// Arca type fields
	if sym.Type != nil {
		if nt, ok := sym.Type.(NamedType); ok {
			if td, ok := lowerer.Types()[nt.Name]; ok && len(td.Constructors) > 0 {
				for _, f := range td.Constructors[0].Fields {
					items = append(items, protocol.CompletionItem{
						Label:  f.Name,
						Kind:   completionKindPtr(protocol.CompletionItemKindField),
						Detail: strPtr(typeName(f.Type)),
					})
				}
			}
		}
	}

	// Go FFI type methods/fields
	if sym.IRType != nil {
		if shortPkg, typeName := extractPackageAndType(sym.IRType); shortPkg != "" {
			if pkg, ok := lowerer.GoPackages()[shortPkg]; ok {
				for _, m := range resolver.TypeMembers(pkg.FullPath, typeName) {
					items = append(items, memberToCompletion(m))
				}
			}
		}
	}

	return items
}

func memberToCompletion(m MemberInfo) protocol.CompletionItem {
	var kind protocol.CompletionItemKind
	switch m.Kind {
	case "func":
		kind = protocol.CompletionItemKindFunction
	case "method":
		kind = protocol.CompletionItemKindMethod
	case "field":
		kind = protocol.CompletionItemKindField
	case "type":
		kind = protocol.CompletionItemKindClass
	case "var":
		kind = protocol.CompletionItemKindVariable
	case "const":
		kind = protocol.CompletionItemKindConstant
	default:
		kind = protocol.CompletionItemKindText
	}
	return protocol.CompletionItem{
		Label:  m.Name,
		Kind:   &kind,
		Detail: strPtr(m.Detail),
	}
}

func completionKindPtr(k protocol.CompletionItemKind) *protocol.CompletionItemKind {
	return &k
}

// insertCompletionPlaceholder inserts dummy identifiers after any dangling
// dots in the source so incomplete expressions like `u.` or `fmt.` parse.
// This handles the case where the user is actively typing in one place
// while another line already has a trailing dot.
func insertCompletionPlaceholder(source string, line, col int) string {
	const placeholder = "_arca_completion_placeholder_"
	lines := strings.Split(source, "\n")
	for i, lineText := range lines {
		// Find trailing dots followed only by whitespace to end-of-line
		trimmed := strings.TrimRight(lineText, " \t")
		if strings.HasSuffix(trimmed, ".") {
			// Check that the char before the dot is an identifier
			if len(trimmed) >= 2 && isIdentChar(trimmed[len(trimmed)-2]) {
				lines[i] = trimmed + placeholder + lineText[len(trimmed):]
			}
		}
	}
	return strings.Join(lines, "\n")
}

// getReceiverBeforeDot returns the identifier immediately before a '.' at the cursor.
// Returns "" if the character before cursor is not part of a receiver.name pattern.
func getReceiverBeforeDot(source string, line, col int) string {
	lines := strings.Split(source, "\n")
	if line < 1 || line > len(lines) {
		return ""
	}
	lineText := lines[line-1]
	// Cursor position is 1-based; we want the char index where the dot is (or just before)
	// Scan back from col-1 to find a '.' and then the identifier before it
	pos := col - 1
	if pos > len(lineText) {
		pos = len(lineText)
	}
	// Find the nearest '.' at or before cursor
	dotIdx := -1
	for i := pos - 1; i >= 0; i-- {
		c := lineText[i]
		if c == '.' {
			dotIdx = i
			break
		}
		if !isIdentChar(c) {
			return ""
		}
	}
	if dotIdx < 0 {
		return ""
	}
	// Now find the identifier ending at dotIdx-1
	end := dotIdx
	start := end
	for start > 0 && isIdentChar(lineText[start-1]) {
		start--
	}
	if start == end {
		return ""
	}
	return lineText[start:end]
}

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

	// Check if this is a field access: receiver.field or receiver.method
	receiver := getReceiverAt(source, line, col)
	if receiver != "" {
		if sym := lowerer.FindSymbolAt(receiver, Pos{line, col}); sym != nil {
			// Arca field
			if fieldType := lookupFieldType(lowerer.Types(), sym.Type, word); fieldType != nil {
				return fmt.Sprintf("```arca\n%s: %s\n```", word, typeName(fieldType))
			}
			// Go FFI method or field on typed receiver
			if hover := lookupGoMemberHover(sym.IRType, word, lowerer); hover != "" {
				return hover
			}
			// Go package-level member (e.g. http.StatusOK)
			if sym.Kind == SymPackage {
				if hover := lookupGoPkgMemberHover(sym.Name, word, lowerer); hover != "" {
					return hover
				}
			}
		}
	}

	// Look up symbol via scope tree
	if sym := lowerer.FindSymbolAt(word, Pos{line, col}); sym != nil {
		return formatSymbolHover(sym, lowerer)
	}

	// Look up functions (not in scope — e.g. pub functions from other modules)
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

func irTypeDisplayName(t IRType) string {
	switch tt := t.(type) {
	case IRNamedType:
		return tt.GoName
	case IRPointerType:
		return "*" + irTypeDisplayName(tt.Inner)
	case IRResultType:
		return "Result[" + irTypeDisplayName(tt.Ok) + ", " + irTypeDisplayName(tt.Err) + "]"
	case IROptionType:
		return "Option[" + irTypeDisplayName(tt.Inner) + "]"
	case IRListType:
		return "List[" + irTypeDisplayName(tt.Elem) + "]"
	case IRTupleType:
		names := make([]string, len(tt.Elements))
		for i, e := range tt.Elements {
			names[i] = irTypeDisplayName(e)
		}
		return "(" + strings.Join(names, ", ") + ")"
	default:
		return "unknown"
	}
}

func lookupGoPkgMemberHover(pkgShort, member string, lowerer *Lowerer) string {
	pkg, ok := lowerer.GoPackages()[pkgShort]
	if !ok {
		return ""
	}
	// Try as function
	if info := lowerer.TypeResolver().ResolveFunc(pkg.FullPath, member); info != nil {
		params := make([]string, len(info.Params))
		for i, p := range info.Params {
			name := p.Name
			if name == "" {
				name = fmt.Sprintf("arg%d", i)
			}
			params[i] = fmt.Sprintf("%s: %s", name, goTypeToIRName(p.Type))
		}
		ret := ""
		if len(info.Results) > 0 {
			retTypes := make([]string, len(info.Results))
			for i, r := range info.Results {
				retTypes[i] = goTypeToIRName(r.Type)
			}
			ret = " -> " + strings.Join(retTypes, ", ")
		}
		return fmt.Sprintf("```go\nfun %s.%s(%s)%s\n```", pkgShort, member, strings.Join(params, ", "), ret)
	}
	// Try as type
	if info := lowerer.TypeResolver().ResolveType(pkg.FullPath, member); info != nil {
		return fmt.Sprintf("```go\ntype %s.%s\n```", pkgShort, member)
	}
	// Package-level constant/variable — just show the name
	return fmt.Sprintf("```go\n%s.%s\n```", pkgShort, member)
}

func lookupGoMemberHover(irType IRType, member string, lowerer *Lowerer) string {
	if irType == nil {
		return ""
	}
	// Extract Go package and type name from IR type
	var named IRNamedType
	switch tt := irType.(type) {
	case IRNamedType:
		named = tt
	case IRPointerType:
		if inner, ok := tt.Inner.(IRNamedType); ok {
			named = inner
		} else {
			return ""
		}
	default:
		return ""
	}
	if !strings.Contains(named.GoName, ".") {
		return ""
	}
	parts := strings.SplitN(named.GoName, ".", 2)
	pkg, ok := lowerer.GoPackages()[parts[0]]
	if !ok {
		return ""
	}
	info := lowerer.TypeResolver().ResolveMethod(pkg.FullPath, parts[1], member)
	if info == nil {
		return ""
	}
	// Format method signature
	params := make([]string, len(info.Params))
	for i, p := range info.Params {
		name := p.Name
		if name == "" {
			name = fmt.Sprintf("arg%d", i)
		}
		params[i] = fmt.Sprintf("%s: %s", name, goTypeToIRName(p.Type))
	}
	ret := ""
	if len(info.Results) > 0 {
		retTypes := make([]string, len(info.Results))
		for i, r := range info.Results {
			retTypes[i] = goTypeToIRName(r.Type)
		}
		ret = " -> " + strings.Join(retTypes, ", ")
	}
	return fmt.Sprintf("```go\nfun %s.%s(%s)%s\n```", parts[1], member, strings.Join(params, ", "), ret)
}

func formatSymbolHover(sym *SymbolInfo, lowerer *Lowerer) string {
	switch sym.Kind {
	case SymPackage:
		if pkg, ok := lowerer.GoPackages()[sym.Name]; ok {
			return fmt.Sprintf("```arca\nimport go \"%s\"\n```", pkg.FullPath)
		}
		return fmt.Sprintf("```arca\nimport go \"%s\"\n```", sym.Name)
	case SymFunction:
		if fn, ok := lowerer.Functions()[sym.Name]; ok {
			return formatFnHover(fn)
		}
	case SymVariable, SymParameter:
		typeStr := typeName(sym.Type)
		if typeStr == "unknown" && sym.IRType != nil {
			typeStr = irTypeDisplayName(sym.IRType)
		}
		return fmt.Sprintf("```arca\n%s %s: %s\n```", sym.Kind, sym.Name, typeStr)
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
		End:   protocol.Position{Line: line, Character: char + 1},
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
