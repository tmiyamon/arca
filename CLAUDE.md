# Arca — Development Guide

An expressive language that compiles to Go.

## Pipeline

```
Source (.arca) → Parse (AST) → Lower (IR) → Validate (IR) → Emit (Go)
                                  ↓
                               LSP (hover, diagnostics)
```

- Lowerer is error-tolerant: produces IR even for invalid input
- IR is the single source of truth for type information
- TypeResolver interface decouples Arca/Go type worlds

## File Structure

| File | Role |
|---|---|
| `ast.go` | AST node definitions |
| `lexer.go` | Tokenizer |
| `parser.go` | Recursive descent parser |
| `ir.go` | IR node definitions (resolved names, types, structurally exhaustive match) |
| `lower.go` | AST → IR (name resolution, constructor resolution, match classification, Go FFI type checking) |
| `validate.go` | IR validation (type checking, arg validation) |
| `emit.go` | IR → Go output (mechanical, no feature-specific logic) |
| `prelude.go` | Built-in function definitions (println, map, filter, etc.) |
| `type_resolver.go` | TypeResolver interface |
| `go_type_resolver.go` | go/types implementation |
| `lsp.go` | LSP server (diagnostics, hover) |
| `types.go` | Shared type utilities (type comparison, constraint dimensions) |
| `helpers.go` | Shared utilities (GoPackage struct for import path parsing) |
| `main.go` | CLI (run, build, emit, init, fmt, health, lsp, version) |
| `formatter.go` | Arca source formatter |
| `openapi.go` | OpenAPI spec generation |

## Key Design Decisions

- **IR abstraction**: New features go in `lower.go` or `prelude.go`. `emit.go` should rarely change.
- **Unified IRMatch**: Single `IRMatch` with typed `IRMatchPattern` for all match kinds. Exhaustiveness checked in validate, not structurally enforced by IR types.
- **Lexical Scope tree**: `withScope(startPos, endPos, symbols, fn)` manages scope push/pop + symbol registration. All variable bindings go through `registerSymbol`. Scope tree preserved for LSP `FindSymbolAt`.
- **Variable shadowing**: Lower RHS before declaring variable name.
- **Sum type methods**: Lowered as normal methods, then expanded to per-variant Go methods by `expandSumTypeMethods` IR post-pass.
- **Prelude**: Built-in functions defined in one map. Adding a builtin = one line.
- **TypeResolver boundary**: Lowerer never imports go/types directly.
- **GoMultiReturn**: Go FFI calls carry `GoMultiReturn` flag + `IRResultType`/`IROptionType`. `goFuncReturnType` maps `(T, error)` → Result, `(T, bool)` → Option, 3+ → Tuple. Consumption sites read IR types, no ad-hoc detection.
- **Project go.mod**: TypeResolver uses nearest go.mod (walked up from .arca file) for package resolution. `goModule` read from go.mod, not hardcoded.

## Adding a New Language Feature

1. AST node in `ast.go` (if new syntax)
2. Parser support in `parser.go`
3. IR node in `ir.go`
4. Lowering in `lower.go`
5. Emission in `emit.go` (only if new IR node type)
6. Validation in `validate.go` (if type checking needed)
7. Tests in `testdata/` + `codegen_test.go`

## Adding a New Built-in Function

Add one entry to `prelude.go`. No other changes needed.

## Testing

- Snapshot: `testdata/*.arca` → `*.go` pairs (auto-discovered)
- go vet: all generated Go validated
- E2E: `runE2E(t, file, expected)` helper
- All tests use `t.Parallel()`
- Run: `go test ./...`

## Documentation

- `SPEC.md` — Language specification
- `DESIGN.md` — Design rationale
- `DECISIONS.md` — Decision log with dates and context (newest first)
- Update all three + this file when making changes
