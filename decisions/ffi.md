# Go FFI Decisions

Newest first within this topic.

---

## 2026-04-18: Go `*T` → Option auto-wrap

**Context:** Go FFI pointer returns (`*T`) can be nil at runtime, causing nil-pointer panics. The FFI boundary table (2026-04-15) listed `*T` nullability as "partially in place; systematic rule not yet decided".

**Decision: Go pointer returns are automatically wrapped in `IROptionType` at the IR level.**

| Go return | Arca type |
|---|---|
| `*T` | `Option[*T]` |
| `(*T, error)` | `Result[Option[*T], Error]` |

The wrapping happens in `goFuncReturnType` — the same single function that handles all Go→Arca return type conversion. No ad-hoc detection at consumption sites.

**`?` double-unwrap:** For `Result[Option[*T], Error]`, `?` first unwraps the Result (propagating Error), then unwraps the Option (nil check → Error on None). This is mechanical: `?` on Option generates `NilCheckReturnValues` in the IR, emitted as a nil check + early error return.

**Implementation:**
- `goFuncReturnType` checks if the Go return type is a pointer (`*types.Pointer`). If so, wraps the Arca type in `IROptionType`.
- For `(*T, error)` returns, the Result's inner type becomes `Option[*T]`.
- `NilCheckReturnValues` added to IR for the Option `?` path. Emitted as `if val == nil { return ..., fmt.Errorf("unexpected nil") }`.

**What this completes:** Row 6 of the FFI boundary table (`*T` nullability → Function signature → `Option[T]`). The systematic rule is now implemented.

**Status:** Done

---

## 2026-04-15: FFI has multiple boundaries, one mechanism per danger

**Context:** Earlier discussion oversimplified the FFI boundary as "the constructor of an Arca type". That is only true for one class of danger (structural Go data becoming an Arca-typed value). Other Go dangers — panic, `interface{}`/`any`, `*T` nil, external mutation, goroutines — have their own distinct boundary mechanisms. Conflating them hides the design work.

**Decision: recognize distinct boundaries, each addressing a specific danger.**

| Danger (Go side) | Boundary (Arca side) | Mechanism |
|---|---|---|
| Structural data → Arca value | **Constructor** | Only entry for Arca typed values. Opaque + immutable + no struct literal forbid any other path. |
| `(T, error)` / `error` / `(T, bool)` | Call site | Auto-wrap to `Result` / `Option` via `goFuncReturnType` (implemented 2026-04-05). |
| Go panic | Call site | Auto `recover` around FFI call, convert to `Result[T, PanicError]`. *(not yet designed)* |
| `interface{}` / `any` | Safe cast operator | `cast[T](v: Any) -> Option[T]`. Direct assignment from `any` to a typed Arca slot is not allowed. *(not yet designed)* |
| Go mutation of held reference | Builder / Freeze | Compiler-generated Builder absorbs mutation; Freeze calls the constructor and severs the Go reference. *(design accepted; implementation Phase 2)* |
| `*T` nullability | Function signature | Go `*T` returns surface in Arca as `Option[*T]`. `(*T, error)` → `Result[Option[*T], Error]`. `?` double-unwraps. *(implemented 2026-04-18)* |
| Goroutine shared access | — | Open. Concurrency design not yet undertaken. |

**Design principle behind the table:** *Arca makes guarantees. FFI is only allowed to the extent that guarantees can be preserved across it.* Each row of the table is a specific guarantee Arca promises and the matching mechanism that keeps the guarantee intact when Go values cross the boundary.

**What this replaces.** Earlier proposals (`@adapter` package annotations, public-API purity checks, explicit opaque-type layer, effect system) are unnecessary. Each was trying to be a universal boundary mechanism; the correct view is per-danger boundaries.

**What this is consistent with.**

- Rust's per-call `unsafe` marker and `-sys` / safe-wrapper convention (no single language-wide boundary; different dangers use different markers).
- Kotlin's pragmatic Java interop (Java types visible, Kotlin-typed values get Kotlin guarantees; no artificial wrapping of the ecosystem).
- Swift Codable / Rust serde (compiler-synthesized derive converts external shape to typed values; the constructor is the validating step, not a separate type).

**Explicit non-goals.**

- No `@adapter` package annotation.
- No public-API purity check ("Go types can't appear in public signatures of Go-importing packages"). Go types in signatures are fine; Arca types still get their guarantees.
- No `opaque` keyword separate from existing type semantics. Arca's constrained types are already opaque.
- No effect system at this stage. Layer 1 boundaries don't need effects; concurrency may revisit this.
- No typestate / linear / rank-N polymorphism for Builder. Opaque + immutable + construction-only entry is sufficient; Builder is compile-time synthesized code, not a user-visible type.

**Status:** Design direction accepted. Implementation mapping to Phase 1:

1. ~~Systematic rule for Go `*T` → `Option[T]`.~~ Done (2026-04-18).
2. Panic recovery around FFI calls.
3. Safe cast operator for `any`.
4. Rewrite `examples/todo` to demonstrate that these rules together seal the dangers that leak in the current version.

DB schema projection and other Layer 2 work come after this.

**Reference:** See `decisions/foundations.md` 2026-04-15 for the two-layer framing this derives from.

---

## 2026-04-05: Go FFI return type → Result auto-wrapping

**Context:** Go FFI calls returning `error` or `(T, error)` need to be wrapped in `Result[Unit, error]` or `Result[T, error]`. Current approach uses ad-hoc detection (`isErrorOnlyCall`, `isConstrainedConstructor`) in `lowerLetStmt` — prone to missed cases (e.g. method calls on let-bound variables).

**Decision: Treat Go multi-return as tuple, then mechanically convert to Arca type.**

Go returns are conceptually received as tuples, then converted based on shape:
```
error      → (error)      → Result[Unit, error]
(T, error) → (T, error)   → Result[T, error]
(T, bool)  → (T, bool)    → Option[T]
T          → (T)          → T
(T1, T2)   → (T1, T2)     → (T1, T2)  // Tuple pass-through
```

This conversion happens in `goFuncReturnType` — a single function that maps Go return signatures to Arca IR types. No ad-hoc detection (`isErrorOnlyCall`) needed; the Go function's type signature deterministically decides the Arca type.

**Implementation: Move Result wrapping to IR FnCall level, not let statement level.**

The wrapping should happen when the IR FnCall/MethodCall is created (in lower.go), not when the let statement assigns it. `goFuncReturnType` returns `IRResultType` for error-returning Go calls, `IROptionType` for bool-returning calls. The IR expression carries the correct Arca type from creation. Consumption sites (let, match, expr stmt) just use the type — no special-case detection.

**Design principle: prefer top-down structural design over bottom-up special-case detection.** When a behavior should apply to all instances of a pattern (e.g. "all Go FFI calls returning error"), encode it at the source (FnCall creation) not at consumption sites (let stmt, match, expr stmt). This prevents missed cases as new consumption sites are added.

**Status:** Implemented. `IRConstrainedLetStmt`, `isErrorOnlyCall`, `isConstrainedConstructor` eliminated. `GoMultiReturn` flag on IR call nodes + `goFuncReturnType` returns `goReturnInfo` with full mapping.

---

## 2026-04-04: Go type integration via go/types

**Context:** Arca currently has no knowledge of Go's type system. Go FFI calls (`fmt.Println`, `http.HandleFunc`, etc.) are passed through without type checking. Errors are only caught when Go compiles the generated output, producing Go error messages that point to generated code — confusing for Arca users.

**Problem:** This is not just a "nice to have" — it's a structural gap. Without Go type information:
- Type errors in Go FFI calls only appear at Go compile time with Go file:line references
- Expression type inference falls back to `interface{}` for anything involving Go types
- LSP can't provide hover/completion for Go FFI calls
- No way to validate generated Go correctness before output

**Decision: Integrate `go/types` via `golang.org/x/tools/go/packages` into the lowering phase.**

**How it works:**
1. When lowering encounters `import go "fmt"`, load `fmt`'s type info via `packages.Load`
2. Cache loaded packages (most programs import the same packages)
3. During lowering, when resolving `fmt.Println(x)`:
   - Look up `Println` in `fmt`'s scope → get `*types.Func` → get signature
   - Validate argument count and types against Arca's type info
   - Set accurate return type on the IR node (instead of `interface{}`)
4. Report errors as Arca `file:line:col` messages

**Scope (incremental):**
- Phase 1: Load packages, resolve return types, validate argument count ✅
- Phase 2: Validate argument types (Arca type → Go type mapping) ✅
- Phase 3: Method resolution on Go types (`w.Header().Set(...)`) ✅
- Phase 4: Struct field access type resolution (`r.URL.Path`) ✅

**Why now:** IR is in place. Type info goes into IR nodes during lowering. Emit doesn't need to change. Without IR, this would have required threading type info through the string-building codegen — impractical.

**Dependency:** Adds `golang.org/x/tools/go/packages` to go.mod.

**Architecture: Arca/Go boundary as interface.**
Arca's type world and Go's type world are fundamentally separate. The lowerer must not depend on `go/types` directly. Instead, a `TypeResolver` interface abstracts the boundary:

```go
type TypeResolver interface {
    ResolveFunc(pkg, name string) *FuncInfo
    ResolveType(pkg, name string) *TypeInfo
    ResolveMethod(typ, method string) *FuncInfo
}
```

Implementations:
- `GoTypeResolver` — uses `go/types` via `golang.org/x/tools/go/packages`
- `NullTypeResolver` — returns nil for everything (current behavior, tests)

This keeps lower.go free of `go/types` imports. The Arca→Go type mapping rules are concentrated in `GoTypeResolver`, not scattered across the codebase. If Go's type system changes or a non-Go backend is added, only the resolver implementation changes.

---

## 2026-04-04: Sum type methods — per-variant expansion

**Context:** Methods on sum types (multi-constructor ADTs) generated `func (a ApiError) send(...)` in Go, which is invalid because `ApiError` is an interface. Go doesn't allow methods on interface types.

**Decision:** Methods with `match self` on sum types are expanded into per-variant methods during IR lowering.

Arca source:
```arca
type ApiError {
  NotFound(message: String)
  BadRequest(message: String)
  fun send(w: http.ResponseWriter) {
    match self {
      NotFound(msg) -> sendJson(w, 404, msg)
      BadRequest(msg) -> sendJson(w, 400, msg)
    }
  }
}
```

Generated Go:
```go
type ApiError interface {
    isApiError()
    send(w http.ResponseWriter)  // method in interface
}
func (a ApiErrorNotFound) send(w http.ResponseWriter) { ... }
func (a ApiErrorBadRequest) send(w http.ResponseWriter) { ... }
```

Each variant struct gets its own implementation with the corresponding match arm body. The interface definition includes the method signature so the method is callable on interface-typed values.

This is the idiomatic Go pattern for interface + variant structs.

---

## 2026-03-30: `&` operator for Go FFI

**Context:** Go libraries require `&T` for mutation (db.Get, json.Unmarshal, rows.Scan). Arca is immutable.

**Options:** `&expr` (Go syntax), `ref(expr)` (function), auto-detect (needs Go type info).

**Decision:** `&expr`. Acts as boundary marker — immutability guarantee ends here. Same as Rust's `unsafe` in spirit. All immutable languages (Haskell, OCaml, Gleam) allow FFI mutation.

---

## 2026-03-28: Go FFI — opaque, no type checking

**Context:** Should Arca type-check Go FFI calls?

**Decision:** No. Go compiler catches these. Arca skips type checking for qualified names (contains `.`). Avoids needing Go package type information.

*Note: This was later revisited — see "Go type integration via go/types" (2026-04-04).*
