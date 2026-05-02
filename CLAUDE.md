# Arca — Development Guide

An expressive language that compiles to Go.

## Pipeline

```
Source (.arca)
  → Parse (AST)
  → Lower (Stage 1 IR — Arca-semantic)
  → stage2Lower (Stage 1 → Stage 2 IR — Go-structure-near)
  → Emit (Go text — pretty-printer)
                                  ↓
                               LSP (hover, diagnostics)
```

- Lowerer is error-tolerant: produces Stage 1 IR even for invalid input
- Stage 1 IR is the single source of truth for type information
- Stage 2 IR mirrors Go syntax (GoIfElse / GoSwitch / GoMultiAssign / GoIIFE / …) so emit makes no semantic decisions
- TypeResolver interface decouples Arca/Go type worlds

## File Structure

| File | Role |
|---|---|
| `ast.go` | AST node definitions |
| `lexer.go` | Tokenizer |
| `parser.go` | Recursive descent parser |
| `ir.go` | Stage 1 IR node definitions (resolved names, types, structurally exhaustive match) |
| `go_ir.go` | Stage 2 IR — Go-structure-near nodes (GoIfElse / GoSwitch / GoMultiAssign / GoIIFE / GoPtrOf / GoOptFromCall / GoTypedNil / GoErrorWrap / …) |
| `lower.go` | AST → Stage 1 IR (name resolution, constructor resolution, match classification, Go FFI type checking, bidirectional type checking, structural checks: exhaustiveness, arg/field count, type existence) |
| `go_lower.go` | Stage 1 → Stage 2 IR rewrite (`stage2Lower` + `walkLambdasInExpr`). All match dispatch, let intent, constructor wrap, IIFE / control-flow leaf-position decisions land here so emit has nothing to decide. |
| `emit.go` | Stage 2 IR → Go output via GoWriter. Pure pretty-printer — no IR-shape decisions, no string-concat reconstruction. |
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

- **IR abstraction**: New features go in `lower.go` or `prelude.go`. `emit.go` should rarely change. Any new Go-output shape lands as a Stage 2 node in `go_ir.go` + a `go_lower.go` rewrite, never as a print-time decision in `emit.go`.
- **Two-stage IR**: Stage 1 (Arca-semantic, `ir.go`) is what `lower.go` produces. Stage 2 (Go-structure-near, `go_ir.go`) is what `stage2Lower` rewrites Stage 1 into — `IRMatch` becomes `GoIfElse` / `GoSwitch` / `GoTypeSwitch`; `IRSomeCall` / `IRNoneExpr` / `IRFnCall.GoMultiReturn` become `GoPtrOf` / `GoTypedNil` / `GoOptFromCall`; `IRTryBlock` becomes `GoIIFE`; `IRTryLetStmt` expands into `GoMultiAssign` + `GoIfElse{GoReturn}`. After `stage2Lower`, no Stage 1 control-flow / let-overload / Result-Option-constructor nodes remain. emit walks Stage 2 mechanically.
- **Unified IRMatch**: Single `IRMatch` with typed `IRMatchPattern` is the Stage 1 shape; `go_lower.go` splits it into kind-specific Stage 2 nodes. Exhaustiveness checked in validate, not structurally enforced by IR types.
- **Lexical Scope tree**: `withScope(startPos, endPos, symbols, fn)` manages scope push/pop + symbol registration. All symbols (variables, params, functions, packages) go through `registerSymbol` → `NewSymbolInfo`. GoName auto-resolved by kind. Scope tree preserved for LSP `FindSymbolAt`.
- **Variable shadowing**: Lower RHS before declaring variable name.
- **Sum type methods**: Lowered as normal methods, then expanded to per-variant Go methods by `expandSumTypeMethods` IR post-pass.
- **Arrow convention**: `->` for types (return type annotations), `=>` for values (match arms, lambda body). Scala-style separation.
- **Prelude**: Built-in functions defined in one map. Adding a builtin = one line. Includes map, filter, fold, take, takeWhile, len.
- **TypeResolver boundary**: Lowerer never imports go/types directly.
- **GoMultiReturn**: Go FFI calls carry `GoMultiReturn` flag + `IRResultType`/`IROptionType`. `goFuncReturnType` maps `(T, error)` → Result, `(*T, error)` → `Result[Option[Ref[T]], Error]`, `(T, bool)` → Option, `*T` → `Option[Ref[T]]`, 3+ → Tuple. Consumption sites read IR types, no ad-hoc detection. For `(T, bool)` → `Option[T]`, `stage2Lower` rewrites the call to `GoOptFromCall{Call}` so emit prints `__optFrom(<call>)` mechanically; Arca's Option is pointer-backed (`*T`), not multi-return.
- **IRRefType**: Arca's user-facing safe reference (`Ref[T]`), emitted as Go `*T`. Distinct from `IRPointerType` (FFI-internal raw pointer). `wrapPointerInOption` recursively walks a Go-sourced IR type and converts every `IRPointerType` leaf into `IROptionType{IRRefType{...}}` — applied at return, param, field, and generic-inner positions. Go FFI param types are propagated as hints into arg lowering so auto-Some lifts `&v` → `Some(&v)` automatically. A transitional `unify` compat accepts `IRPointerType` and `IRRefType` interchangeably only because legacy user-written `*T` Arca syntax still parses to `IRPointerType`; retire when the syntax is dropped.
- **Monadic methods on Result/Option**: `.map(f)`, `.flatMap(f)`, `.mapError(f)` on Result; `.map(f)`, `.flatMap(f)`, `.okOr(err)`, `.okOrElse(fn)` on Option. Implemented as AST-level desugar to `match` — no new IR nodes, no new emit code. `resultMonadicMethods` / `optionMonadicMethods` tables in `lower.go` are the SSOT: `tryDesugarMonadicMethod` dispatches from them and LSP completion reads the same tables via `monadicMethodsFor(irType)` so the two paths cannot drift.
- **`?` in expression position**: `?` is a postfix operator parsed in `parseUnaryExpr` so it binds tighter than binary operators (`f()? * 2` = `(f()?) * 2`). In any value-position the lowerer produces `IRTryExpr{Inner, OkType, ErrType, ReturnType}`; `stage2Walker` deep-walks data expressions via `hoistTryInExpr`, pushing `__try<N>, __try<N>_err := …` + `if __try<N>_err != nil { return zero, __try<N>_err }` onto a hoist buffer and substituting the expression with `__try<N>`. Statement-producing methods flush the buffer before each emitted stmt. Lambda / try-block boundaries are opaque (each owns its own walker / return type). Statement-level `let x = expr?` and bare `expr?` keep the existing `IRTryLetStmt` shortcut path.
- **`?` single-layer**: unwraps exactly one Result layer. Mixing with Option in the same function signature is a compile error; use `.okOr(err)?` or a monadic pipeline to convert. Stage 2 rewrites `match opt` uniformly to `GoIfElse{Cond: subject != nil}` — Option is always pointer-backed (`*T`), so the discriminator is nil-check for every inner type. Binding is pass-through when inner is `Ref`/`Ptr` (collapse), otherwise `v := *opt` via `GoDeref`.
- **Option pointer-backed**: `Option[T]` → `*T` uniformly. `go_lower.go` rewrites `Some(v)` to `GoPtrOf{Inner: v}` (which emits as `__ptrOf(v)`) except when Inner is `Ref`/`Ptr`, where it collapses to `v` directly. `None` → `GoTypedNil{GoType: irTypeStr(Inner)}` for typed positions, bare `nil` otherwise. Result keeps `(T, error)` multi-return at the Go boundary; Stage 2 emits `GoMultiAssign` and `GoReturn` to handle the split.
- **Auto-Some**: at hint-driven typed `Option[T]` positions, a value of type `T` is implicitly lifted into `Some(v)`. Single-layer only — `Option[Option[T]]` still needs explicit `Some(Some(v))` / `Some(None)`. `None` and `&` are never auto-inserted. Entry point: `autoSomeLift` at the tail of `lowerExprHint` in `lower.go`.
- **Any + match type pattern**: `Any` maps to `IRInterfaceType` (Go `interface{}`). Narrowing via `match v { id: T => body }` — `TypePattern` AST → `IRMatchTypePattern` IR → Go `switch v := subject.(type) { case T: ... }`. Any type-pattern arm promotes the whole match to a type switch; wildcard arm becomes `default`. No exhaustiveness check — open universe.
- **Traits (Phase 1)**: `trait Name { sig }` + separate `impl Type: Trait { body }`. AST: `TraitDecl` / `ImplDecl`. IR: `IRTraitType{Name}` for trait-as-type positions, `IRTraitDecl` emits as `type Arca<Name> interface { ... }`. Impl methods are force-marked `Public` in `lowerImplDecl` so the Go method set is exported and satisfies the interface structurally (no `is<Trait>` marker). Method resolution walks `l.types[T].Methods` → `l.impls[T]` → trait method set. `Lowerer.unify` gains `traitImplCompatible` (concrete X unifies with trait T when `impl X: T` is registered) so hint-driven coercion passes without new IR nodes. Dispatch is dynamic only; Phase 1 forbids default methods, trait inheritance, generic bounds, `Self`/`static fun` in trait/impl, inherent `impl`, and cross-method-name ambiguity.
- **Error trait bridge**: `trait Error { fun message() -> String }` is prelude-registered via `registerPreludeTraits` (no import). `IRTraitType{Error}` emits as Go's stdlib `error` (the one exception to the `Arca<Name>` rule) — `ArcaError` is not emitted because mapping to `error` collapses the FFI interop surface. Impls of `Error` get an auto-generated `fun error() -> String { self.message() }` synthesized by `lowerImplDecl`, so the concrete type also satisfies Go's `error`. Go FFI `(T, error)` returns produce `IRResultType{Err: IRTraitType{"Error"}}`; `Lowerer.unify` and `InferScope.unify` treat `IRTraitType{"Error"}` and `IRNamedType{"error"}` as interchangeable so internal IR sites still constructing the named type keep flowing. At match `Err` bindings, when the subject Err is `IRTraitType{Error}`, `go_lower.go` wraps the binding RHS in `GoErrorWrap{Inner: ErrVar}` so trait methods (`.message()`) resolve. `__goError` (`{Message, Error, Unwrap}`) is emitted on demand.
- **main() -> Result**: `fun main() -> Result[_, _]` is lowered as a normal Result-returning function; emit wraps it in a Go `main()` IIFE that prints Err to stderr and `os.Exit(1)`. Mirrors Rust's `fn main() -> Result<(), Error>`. `fmt` / `os` auto-imported. Entry: `emitResultMainWrapper` in `emit.go`.
- **try block**: `try { ... }` block expression creates a Result context for `?` in non-Result functions. `go_lower.go` rewrites `IRTryBlock` to `GoIIFE{RetType: Result[Ok, error], Body}` so emit prints `func() (T, error) { ... }()` mechanically. HM inference uses fresh type var + unify for Ok type; final expr wrapped in `IROkCall`. `try` is not a keyword — only `try {` recognized.
- **? compile error**: `?` outside Result functions and try blocks is a compile error (not panic).
- **Project go.mod**: TypeResolver uses nearest go.mod (walked up from .arca file) for package resolution. `goModule` read from go.mod, not hardcoded.
- **HM type inference**: `InferScope` struct (per-function) holds type variables, substitution, and type param vars. `withInferScope(fn)` creates fresh scope. `unify(a, b)` for constraint solving, `resolveDeep` for substitution. Ok/Error/None/empty list use type variables resolved from call-site argument-parameter unification. Type parameters become `IRTypeVar` inside function bodies. Go generic functions are instantiated with fresh type vars at each call (`instantiateGeneric`), unified with arg types, explicit type args, and hint — all via the same HM path. Explicit type args `f[T](args)` supplement when context is insufficient.
- **Bidirectional type checking**: `lowerExprHint(expr, hint)` propagates expected types top-down. Covers function args, let annotations, return types, match arms, constructor fields. `checkTypeHint` is a thin wrapper routing hint checks through `Lowerer.unify` (single HM core — `irTypesMatch` removed). Constraint compatibility (`AdultAge → Age`) is folded into `Lowerer.unify` as a fallback success path. Lambda param types inferred from Go FFI call context and prelude functions.
- **GoWriter**: Structured Go code builder in `gowriter.go`. `emit.go` uses GoWriter methods (`If`, `Switch`, `Func`, `Method`, `For`, `Assign`, `Unreachable`, etc.) instead of manual string formatting. `Unreachable()` for exhaustive match stubs (distinct from `Panic()`). Auto-indentation eliminates `indent string` parameter threading. Output is `gofmt`-normalized in tests.
- **Arca packages**: Built-in packages bundled with the arca binary via `go:embed`. `arca_packages.go` defines the registry. `import stdlib` works without any go.mod setup. Type resolution loads from embed.FS via `go/parser` + `go/types`. Build extracts to `build/<pkg>/` with `replace` directive for `go run`. Persistent cache at `~/.cache/arca/packages/` for LSP go-to-definition (resolves embed paths to real files).
- **LSP features**: Hover (symbol type + signature), diagnostics (parse/lower errors), go-to-definition (variables, parameters, functions, types, packages, Go FFI members and methods), completion (triggered by `.`, chained access + call-chain receivers like `foo().`, Arca fields + Go package members + Go type methods + prelude-provided monadic methods on Result/Option). `getReceiverBeforeDot` + `insertCompletionPlaceholder` walk through balanced `()` / `[]` so call / index expressions are valid receivers. Per-session resolver cache (`resolverCache` keyed by goModDir) avoids re-loading Go packages on every LSP request. Symbol positions tracked via `SymbolRegInfo.Pos` through `registerSymbol`.
- **Function types**: `A -> B` / `(A, B) -> C` as a first-class `IRFnType{Params, Ret}`, emitted as Go `func(A, B) C`. n-ary (not curried), invariant under unify, displays as `A -> B` in hover / diagnostics. Lambda param type inference flows through one mechanism — `lowerLambdaHint` → `applyLambdaHint` consumes an `IRFnType` hint and fills untyped `lam.Params[i].Type`. Every lambda-accepting call shape produces that hint: Arca user fns via `l.functions`, Go FFI via `resolveCallParamIRType` (+ `ResolveUnderlying` peels aliases like `echo.HandlerFunc`), prelude via `BuiltinDef.Signature` (fresh-typevar per call), monadic methods via `monadicMethodInfo.LamArg`. No separate lambda-typing path.

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
