# Arca — Development Guide

An expressive language that compiles to Go.

## Pipeline

```
Source (.arca) → Parse (AST) → Lower (IR) → Emit (Go)
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
| `lower.go` | AST → IR (name resolution, constructor resolution, match classification, Go FFI type checking, bidirectional type checking, structural checks: exhaustiveness, arg/field count, type existence) |
| `emit.go` | IR → Go output via GoWriter (mechanical, no feature-specific logic) |
| `gowriter.go` | Structured Go code builder with auto-indentation |
| `prelude.go` | Built-in function definitions (println, map, filter, take, takeWhile, len, etc.) |
| `arca_packages.go` | Arca package registry: bundles built-in packages (stdlib) via go:embed |
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
- **Lexical Scope tree**: `withScope(startPos, endPos, symbols, fn)` manages scope push/pop + symbol registration. All symbols (variables, params, functions, packages) go through `registerSymbol` → `NewSymbolInfo`. GoName auto-resolved by kind. Scope tree preserved for LSP `FindSymbolAt`.
- **Variable shadowing**: Lower RHS before declaring variable name.
- **Sum type methods**: Lowered as normal methods, then expanded to per-variant Go methods by `expandSumTypeMethods` IR post-pass.
- **Arrow convention**: `->` for types (return type annotations), `=>` for values (match arms, lambda body). Scala-style separation.
- **Prelude**: Built-in functions defined in one map. Adding a builtin = one line. Includes map, filter, fold, take, takeWhile, len.
- **TypeResolver boundary**: Lowerer never imports go/types directly.
- **GoMultiReturn**: Go FFI calls carry `GoMultiReturn` flag + `IRResultType`/`IROptionType`. `goFuncReturnType` maps `(T, error)` → Result, `(*T, error)` → `Result[Option[Ref[T]], Error]`, `(T, bool)` → Option, `*T` → `Option[Ref[T]]`, 3+ → Tuple. Consumption sites read IR types, no ad-hoc detection. For `(T, bool)` → `Option[T]`, emit wraps the raw Go call with `__optFrom(...)` since Arca's Option is pointer-backed (`*T`), not multi-return.
- **IRRefType**: Arca's user-facing safe reference (`Ref[T]`), emitted as Go `*T`. Distinct from `IRPointerType` (FFI-internal raw pointer). `wrapPointerInOption` recursively walks a Go-sourced IR type and converts every `IRPointerType` leaf into `IROptionType{IRRefType{...}}` — applied at return, param, field, and generic-inner positions. Go FFI param types are propagated as hints into arg lowering so auto-Some lifts `&v` → `Some(&v)` automatically. A transitional `unify` compat accepts `IRPointerType` and `IRRefType` interchangeably only because legacy user-written `*T` Arca syntax still parses to `IRPointerType`; retire when the syntax is dropped.
- **Monadic methods on Result/Option**: `.map(f)`, `.flatMap(f)`, `.mapError(f)` on Result; `.map(f)`, `.flatMap(f)`, `.okOr(err)`, `.okOrElse(fn)` on Option. Implemented as AST-level desugar to `match` — no new IR nodes, no new emit code. Entry point: `tryDesugarMonadicMethod` in `lower.go`.
- **`?` single-layer**: unwraps exactly one Result layer. Mixing with Option in the same function signature is a compile error; use `.okOr(err)?` or a monadic pipeline to convert. `match opt` is uniformly `if subject != nil { ... }` — Option is always pointer-backed (`*T`), so the discriminator is nil-check for every inner type. Binding is pass-through when inner is `Ref`/`Ptr` (collapse), otherwise `v := *opt` (deref).
- **Option pointer-backed**: `Option[T]` → `*T` uniformly. `Some(v)` emits as `__ptrOf(v)` (helper wraps non-addressable values) except when Inner is `Ref`/`Ptr`, where it collapses to the value itself. `None` → `(*T)(nil)`. No split machinery (no `SplitNames` / `ExpandedValues` / `flattenArgs` entries for Option) — the post-pass handles only Result.
- **Auto-Some**: at hint-driven typed `Option[T]` positions, a value of type `T` is implicitly lifted into `Some(v)`. Single-layer only — `Option[Option[T]]` still needs explicit `Some(Some(v))` / `Some(None)`. `None` and `&` are never auto-inserted. Entry point: `autoSomeLift` at the tail of `lowerExprHint` in `lower.go`.
- **Any + match type pattern**: `Any` maps to `IRInterfaceType` (Go `interface{}`). Narrowing via `match v { id: T => body }` — `TypePattern` AST → `IRMatchTypePattern` IR → Go `switch v := subject.(type) { case T: ... }`. Any type-pattern arm promotes the whole match to a type switch; wildcard arm becomes `default`. No exhaustiveness check — open universe.
- **main() -> Result**: `fun main() -> Result[_, _]` is lowered as a normal Result-returning function; emit wraps it in a Go `main()` IIFE that prints Err to stderr and `os.Exit(1)`. Mirrors Rust's `fn main() -> Result<(), Error>`. `fmt` / `os` auto-imported. Entry: `emitResultMainWrapper` in `emit.go`.
- **try block**: `try { ... }` block expression creates a Result context for `?` in non-Result functions. Emitted as Go IIFE. HM inference uses fresh type var + unify for Ok type; final expr wrapped in `IROkCall`. `try` is not a keyword — only `try {` recognized.
- **? compile error**: `?` outside Result functions and try blocks is a compile error (not panic).
- **Project go.mod**: TypeResolver uses nearest go.mod (walked up from .arca file) for package resolution. `goModule` read from go.mod, not hardcoded.
- **HM type inference**: `InferScope` struct (per-function) holds type variables, substitution, and type param vars. `withInferScope(fn)` creates fresh scope. `unify(a, b)` for constraint solving, `resolveDeep` for substitution. Ok/Error/None/empty list use type variables resolved from call-site argument-parameter unification. Type parameters become `IRTypeVar` inside function bodies. Go generic functions are instantiated with fresh type vars at each call (`instantiateGeneric`), unified with arg types, explicit type args, and hint — all via the same HM path. Explicit type args `f[T](args)` supplement when context is insufficient.
- **Bidirectional type checking**: `lowerExprHint(expr, hint)` propagates expected types top-down. Covers function args, let annotations, return types, match arms, constructor fields. `checkTypeHint` is a thin wrapper routing hint checks through `Lowerer.unify` (single HM core — `irTypesMatch` removed). Constraint compatibility (`AdultAge → Age`) is folded into `Lowerer.unify` as a fallback success path. Lambda param types inferred from Go FFI call context and prelude functions.
- **GoWriter**: Structured Go code builder in `gowriter.go`. `emit.go` uses GoWriter methods (`If`, `Switch`, `Func`, `Method`, `For`, `Assign`, `Unreachable`, etc.) instead of manual string formatting. `Unreachable()` for exhaustive match stubs (distinct from `Panic()`). Auto-indentation eliminates `indent string` parameter threading. Output is `gofmt`-normalized in tests.
- **Arca packages**: Built-in packages bundled with the arca binary via `go:embed`. `arca_packages.go` defines the registry. `import stdlib` works without any go.mod setup. Type resolution loads from embed.FS via `go/parser` + `go/types`. Build extracts to `build/<pkg>/` with `replace` directive for `go run`. Persistent cache at `~/.cache/arca/packages/` for LSP go-to-definition (resolves embed paths to real files).
- **LSP features**: Hover (symbol type + signature), diagnostics (parse/lower errors), go-to-definition (variables, parameters, functions, types, packages, Go FFI members and methods), completion (triggered by `.`, chained access, Arca fields + Go package members + Go type methods). Per-session resolver cache (`resolverCache` keyed by goModDir) avoids re-loading Go packages on every LSP request. Symbol positions tracked via `SymbolRegInfo.Pos` through `registerSymbol`.

## Adding a New Language Feature

1. AST node in `ast.go` (if new syntax — all Expr nodes must embed `NodePos` for source position, enforced by `exprPos()` interface)
2. Parser support in `parser.go` (use `AtTok(tok)` for NodePos construction)
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
