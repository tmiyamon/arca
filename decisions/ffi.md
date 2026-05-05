# Go FFI Decisions

Newest first within this topic.

---

## 2026-05-04 (refined): Synthetic Builder — Bindable as compiler intrinsic + naming finalisation

**Supersedes the 2026-05-02 (refined) entry's Bindable trait shape, derive syntax, and naming.** The dispatch model from that entry (vtable + dictionary hybrid, object-safety routing) carries forward unchanged — what changes is how Bindable itself is positioned and spelled.

Reviewing the 2026-05-02 (refined) sketch in design discussion surfaced four structural problems:

- Treating Bindable as a user-defined trait forces every Phase 1 trait restriction (no `Self` / no `static fun` / no associated types / no generic bounds) to gain a "constraint-only dictionary trait" exception. The exception is real but it scatters trait machinery across user-facing surface for what is mechanically one compiler-managed mechanism.
- `arcaBuilder` as a method name leaks Go-emit-side concern (the `arca` prefix exists to avoid collision with user code in Go) into the Arca-side surface where users will read and write it.
- `Builder` struct + per-field `bool` flags (sketch G1) tracks "was this field set" through path-dependent runtime state. Layer 1's panic-prevention contract is structural, not path-dependent — `Option` is pointer-backed precisely so nil-check is structural. Bindable's field-state encoding should match.
- `derive(Bindable)` as a function-call-shaped annotation between the type name and field tuple sits visually adjacent to `pub` / `static` modifiers but parses differently. Arca's existing decl-modifier slot is bare-keyword (not function-call), and `derive` belongs there.

**Decision: Bindable is a compiler intrinsic, not a user trait.** Same category as `Result` / `Option` — the name is reserved, manual `impl T: Bindable` is rejected by parser/lower, only `derive Bindable` activates synthesis. This dissolves the "constraint-only trait" exception: Phase 1 trait restrictions stay as written for user traits, and Bindable's machinery (associated `Draft` type, `static fun draft()`, generic dispatch dictionary) is compiler-internal, not surface trait syntax.

**`derive` syntax — modifier-style, block-adjacent (P2 form):**

```arca
type Todo (
  id: Int
  body: String { max_length: 255 }
  startedAt: Option[Time]
) derive Bindable {
  // optional method block
}

// block-less form: derive at tail
type Status derive Bindable { Active, Archived }
```

Rule: `derive` sits immediately before the body block `{ }`; if there is no block, `derive` goes at the tail. Same rule for product and sum types. Multiple traits: `derive Bindable, Clone` — but MVP accepts one only (multi-derive is parser-rejected until the trait-composition session lands).

**Naming finalisation:**

| Concept | Name | Rationale |
|---|---|---|
| Synthesised mutable type | `Todo.Draft` | Associated type on Bindable; `Draft` (vs `Builder`) avoids implying a setter chain that doesn't exist — population is by FFI mutation (json/scan), not user `.id(1).body("…")`. |
| Per-field state | `BindableSlot[T] = Set(T) \| Unset` | Sum type, structurally panic-free. Pattern-match (`match slot { Set(v) => …; Unset => … }`) is the only access; no flag-bool path-dependency. |
| Factory | `Todo.draft()` | Static method on the host type (no `arca` prefix). Returns `Todo.Draft`. |
| Finaliser | `d.freeze()` | Inherent method on the Draft type, returns `Result[Todo, Error]`. Calls `NewTodo(...)` for constraint validation. Name preserved from Haskell `runST` lineage. |

**`BindableSlot[T]` as sum type (G3):** each Draft field has type `BindableSlot[T]`, where:

```arca
type BindableSlot[T] {
  Set(value: T)
  Unset
}
```

Compiler emits `Set` for FFI-populated fields, `Unset` for absent ones; `freeze` pattern-matches `Unset` → returns `Err(MissingFieldError)`, `Set(v)` → unwraps for `NewTodo` call. No `nil`, no flag bool, no order-of-write dependence. The inability to construct an invalid Draft state is structural, not enforced by check.

**Dispatch dictionary — generic struct + function-pointer fields (Q4b-fp):**

```go
// emitted alongside Todo / NewTodo:
type BindableDict[T any, B any] struct {
    Draft  func() B
    Freeze func(B) (T, error)
    // (future: Bind / Scan / Decode populate hooks)
}

var __TodoBindable = BindableDict[Todo, TodoDraft]{
    Draft:  func() TodoDraft { return TodoDraft{} },
    Freeze: func(d TodoDraft) (Todo, error) { return NewTodo(...) },
}
```

Generic functions taking `[T: Bindable]` receive a hidden `__bindableT` parameter (modifier form, positioned immediately after type params). Call sites are rewritten at lower:

```arca
fun bindJSON[T: Bindable](r: Ref[http.Request]) -> Result[T, Error] {
  let b = __bindableT.draft()
  json.unmarshal(r.body, &b)?
  __bindableT.freeze(b)
}

let todo = bindJSON[Todo](r)?    // lower rewrites to bindJSON(__TodoBindable, r)?
```

`currentDictParams` tracks transitive chains: a generic function calling another generic function passes its own `__bindableT` through, no per-call resolution at every depth.

**Instance naming convention:** `__<TypeName><TraitName>` (e.g. `__TodoBindable`, `__StatusBindable`). Underscore prefix marks compiler-emitted, the concatenation collision-checks against user code that already cannot start identifiers with `__`.

**Constraint syntax — single only:** MVP accepts `[T: Bindable]` exactly. Multi-trait bound (`[T: A + B]` / `&` / `with` / `where` clause) is deferred to a dedicated trait-composition session — the design space spans `+` precedence with type-arg syntax, intersection vs union semantics, where-clause vs inline form, and is unrelated to Bindable's correctness.

**Known trade-off (X1):** Arca's `[T: Bindable]` carries one type parameter; the Go-emitted `BindableDict[T, B]` carries two. The associated type `B = T::Draft` is resolved at lower (the dictionary instance picks the concrete pair) but the Go-side type cannot collapse to a single param without associated-type support in Go's type system. This leak does not surface in Arca code — `BindableDict` is compiler-internal — but anyone reading emitted Go sees the two-param form.

**Rejected: `type[X]` fusion syntax.** A proposal to write `type[Bindable] Todo (...)` (subscript-style derive on the `type` keyword) was considered. Rejected because `[T]` is already Arca's type-parameter syntax (`type Pair[A, B]`), and overloading the same bracket position with a different role (derive list vs type params) creates visual ambiguity at every type decl. Future attribute systems may revisit this with a different sigil.

**Stdlib refactor (B3, unchanged from 2026-05-02 entry):** `BindJSON` / `QueryAs` / `Decode` switch from runtime reflection over `T` to dictionary-driven `draft()` → populate → `freeze()`. The reflection path retires.

**Implementation slices (B2 expansion, supersedes B2 in 2026-05-02 entry):**

| Slice | Content | Est. |
|---|---|---|
| B2a (landed) | `derive Bindable` parser + AST + Bindable intrinsic registration | ~200 |
| B2b (landed) | `Todo.Draft` synthesis + `BindableSlot[T]` IR + Go emit (G3 sum type) | ~400 |
| B2c (landed) | `BindableDict[T, B]` struct emission + `__TodoBindable` instance | ~300 |
| B2d (landed) | `[T: Bindable]` constraint parser + IR + lower resolution | ~150 |
| B2e (landed) | `__bindableT` hidden-param injection + call site rewrite + transitive chain | ~400 |
| B2f | `Todo.draft()` factory + `d.freeze()` synthesis + `NewTodo` integration | ~200 |

Total ~1650, 3-4 sessions. Slice boundaries keep test suite green.

See `design_bindable_type_ideas.md` for nine deferred ideas surfaced in this discussion (BindableSlot as general "presence tracking" abstraction, compiler-intrinsic trait category, R3 sister-trait shape, trait-as-namespace syntax, type composition syntax, phantom-type rejection rationale, user-facing builder promotion, `type[X]` fusion re-evaluation, 3-layer invariant model).

**Status:** Design refined. B1 landed end-to-end (B1a + B1b + B1c + B1d); B2a–B2e landed; B2f next.

---

## 2026-05-02 (refined): Synthetic Builder — Vtable + Dictionary hybrid dispatch

**Supersedes the same-day "Synthetic Builder — FFI-only MVP via `derive`" entry below.** The earlier entry settled on a marker `Bindable` trait + Go-side type-assertion (Path 1 in working notes). Reviewing it surfaced three structural problems:

- `derive(Bindable[TodoBuilder])` is chicken-and-egg — `TodoBuilder` is what derive itself produces, can't be referenced in the annotation.
- Phase 1 trait restrictions (no `Self` / no `static fun` / no associated types / no generic bounds) exist precisely to keep traits object-safe for Go interface vtable dispatch. Bindable needs every one of those — Self-referencing Builder type, static factory, associated type. The marker-trait workaround pushes the abstraction outside Arca into Go-side compiler magic — a structural leak.
- Path 1 was a short-term cost minimisation. Walking through Rust's two-mode dispatch model and Haskell/Scala's dictionary passing showed dictionary fits Arca's Go target better than monomorphisation (no native-binary / frame-budget / no-std rationale to justify code bloat).

**Decision: Phase 1 vtable trait stays unchanged; add a parallel dictionary-passing dispatch for traits that can't be object-safe.** Per-trait routing is automatic by object-safety analysis (Rust model — same trait keyword, dispatch chosen from the trait's body):

- Trait body satisfies object safety (only `&self` methods, no `Self` outside receiver, no static fn, no associated types) → emit as Go interface, dispatched via vtable. Existing Phase 1 behaviour, no change. Usable as a type (`fn handle(e: Error)`, `List[Error]`).
- Trait body fails object safety (has `Self` / static fun / associated type) → "constraint-only" trait. Per-`derive` compiler emits a dictionary struct; generic functions take the dictionary as a hidden parameter; call sites are rewritten to inject the dictionary. Not usable as a type — `let xs: List[Bindable] = …` is a compile error directing the user to vtable-style interfaces.

```arca
// Phase 1, vtable (unchanged):
trait Error { fun message() -> String }

// New constraint-only, dictionary:
trait Bindable {
  type Builder
  static fun arcaBuilder() -> Self::Builder
  fun freeze(b: Self::Builder) -> Result[Self, Error]
}

type Todo derive(Bindable) (
  id: Int
  body: String { max_length: 255 }
)
// compiler emits alongside Todo + NewTodo:
//   type TodoBuilder (id: Int, body: String) { json/db tags }
//   fun (b: TodoBuilder) freeze() -> Result[Todo, Error] { NewTodo(b.id, b.body) }
//   var TodoBindable = BindableDict[Todo, TodoBuilder] { … }    // hidden dict instance

fun stdlib.bindJSON[T: Bindable](r: Ref[http.Request]) -> Result[T, Error] {
  // compiler injects hidden `dict: BindableDict[T, _]` parameter
  let b = dict.arcaBuilder()
  json.unmarshal(req.body, &b)?
  dict.freeze(b)
}

let todo = stdlib.bindJSON[Todo](r)?    // compiler rewrites to inject TodoBindable
```

`derive(Bindable)` takes no type arg; the Builder type is conventionally named `<T>Builder` and emitted in the same scope. User never writes the Builder name in derive; user code may reference `TodoBuilder` directly if they want the lower-level form, but stdlib helpers hide it.

**Why dictionary, not monomorphisation:** Arca targets Go (managed runtime, GC, vtable dispatch already pervasive). Rust's monomorphisation rationale — `no_std`, frame budget, native binary size, zero indirect-call cost — does not apply. Dictionary passing matches Arca's situation: one indirect call per trait method (comparable to existing Go interface dispatch), no per-T code-body duplication, no library-distribution complications. Scala 3 (`given/using`) and Swift (Protocol Witness Tables) use this hybrid model for the same reason. Across modern languages, vtable + dictionary coexistence is the mainstream pattern; pure monomorphisation (Rust, C++) is a systems-language outlier.

**Stdlib refactor:** existing `BindJSON` / `QueryAs` / `Decode` switch from "runtime reflection over T + post-hoc ArcaValidate" to "construct Builder via dictionary, populate via Bind / Scan / Unmarshal, Freeze with validation". The reflection path retires.

**Future extension paths (out of MVP):**

- User-facing builder promotion (the conventional `<T>Builder` and `freeze()` are kept stable so promotion is additive — user can already write `let b = TodoBuilder(); … b.freeze()?` if they want)
- `extern { }` escape hatch for arbitrary Go mutation calls
- Sum type Builder with discriminator field
- Arca-side accessors making T's Go fields private at runtime

**Implementation slices (multi-session):**

- B1 — Object-safety analysis pass; trait kind tagging (vtable vs dictionary) inferred from trait body
  - B1a (landed) — `TraitKind` + `IRTraitDecl.Kind`, `analyzeTraitObjectSafety` analyser; every parsed trait still classifies as Vtable (parser restrictions block the alternatives), so no codegen change. Hand-constructed TraitDecls in tests cover the Dictionary path.
  - B1b (landed) — `Self` in trait method return / parameter positions parses + lowers cleanly; the analyser routes such traits to Dictionary; `stage2LowerTypes` drops dictionary IRTraitDecl nodes so emit stays mechanical; usage sites (trait as type, `impl X: Trait`) reject with `ErrUnsupportedFeature` until B2 lands. Parser already accepted `Self` — no parser change required.
  - B1c (landed) — `static fun` in trait body now parses; the trait-decl loop accepts an optional `static` modifier and stamps `FnDecl.Static`. Analyser + stage2 drop + usage rejection from B1a/B1b carry the rest. The parser block on `static fun` in **impl** body stays in place — Phase 1 still has no impl path for dictionary traits.
  - B1d (landed) — `type Foo` declarations and `Self.Foo` references in trait method signatures now parse. Syntax: dot rather than `::` (matches Arca's existing `sql.DB` / `record.field` path convention; Rust's `::` is not the universal associated-type marker — Swift / Scala / OCaml all use `.`). New AST nodes `TraitAssocTypeDecl` and `AssocTypeName` keep associated-type access distinct from qualified-type fold so B2 can substitute by structural match. `lowerType` lowers `AssocTypeName` to opaque `IRInterfaceType`; analyser routes the trait through Dictionary so stage2 drops it. B2 introduces the dedicated IR node and substitution.
- B2 — Dictionary struct emission for `derive`-marked types; generic-function hidden-parameter insertion at lower
- B3 — `derive(Trait)` syntax in parser + AST; `Bindable` trait registered in prelude
- B4 — Stdlib helpers constrained to `T: Bindable`, implementation switched to dictionary + Freeze; reflection path retires
- B5 — `examples/todo` migrated; sum type Builder demo (deferrable)

**Status:** Design refined. B1 landed end-to-end (B1a + B1b + B1c + B1d); B2–B5 across multiple sessions.

---

## 2026-05-02: Synthetic Builder — FFI-only MVP via `derive`

**Context:** The 2026-04-15 FFI table accepted Synthetic Builder as the boundary mechanism for Go mutation absorption, with implementation deferred. `design_ffi_synthetic_builder.md` settled the theoretical framing (compile-time synthesis, runST / typestate / linear / serde-derive lineage) but left the implementation shape open. Current stdlib (`BindJSON`, `QueryAs`, `Decode`) uses runtime reflection over T directly — `structScanPtrs` walks fields by name, `json.Unmarshal` writes into T, then `ArcaValidate` is invoked post-hoc. This leaks Go-side mutability via public fields on T, conflates wire format with domain type, and won't roundtrip sum types.

**Decision: scope MVP to FFI use only. Builder is hidden inside stdlib helpers; user code never names `Builder` or writes `&`.** `BindJSON[Todo](r)?` works as today from the user's view; what changes is the implementation underneath.

**Synthesis trigger: trait + `derive` annotation, not stdlib-function-name detection.** Compiler-stdlib coupling is limited to one trait name (`Bindable`); stdlib is free to rename / split / extend helpers without compiler changes.

```arca
type Todo derive(Bindable) (
  id: Int
  body: String { max_length: 255 }
  startedAt: Option[Time]
)
```

`derive(Bindable)` sits in the same modifier slot as `pub` / `static` — between the type name and the field tuple — matching Arca's existing decl-modifier style. Multiple traits: `derive(Bindable, Clone)`.

Compiler emits, alongside `Todo` and `NewTodo`:

- `TodoBuilder` Go struct (mutable, public fields, mirroring Todo's shape with Go-side types and `json` / `db` tags)
- `func (b *TodoBuilder) Freeze() (Todo, error)` calling `NewTodo(...)` for constraint validation
- `Bindable` trait impl on `Todo` exposing the builder factory

Stdlib helpers (`BindJSON` / `QueryAs` / `Decode`) take `T: Bindable`. Implementation switches from "Unmarshal into T + ArcaValidate" to "construct Builder via trait, populate via Bind / Scan / Unmarshal, Freeze with validation". The reflection-based path retires.

**Why FFI-only, not general user-facing Builder.** A user-facing builder (`let b = User.builder(); b.id = 1; let u = b.freeze()?`) was considered but deferred — Arca's record literal `User(id: 1, name: "...")` already covers ergonomic construction, and adding user-facing builder API doubles the design surface (validation timing, partial state, fluent vs named-field). FFI absorption is the unique value the mechanism provides today; user-facing promotion is purely additive once the underlying synthesis is in place.

**Future extension paths (out of MVP scope):**

- User-facing builder promotion — builder name and `Freeze` signature kept stable so this is additive
- `extern { }` escape hatch for Go calls no derived helper covers
- Sum type Builder with discriminator field
- Arca-side accessors making T's Go fields private at runtime (lowering all field-access sites required)

**Implementation slices (multi-session):**

- B1 — Parser + AST for `derive(Trait)` modifier on type decl; `Bindable` trait registered in prelude; compiler synthesizes Builder struct and Freeze for derive-marked types
- B2 — Stdlib helpers constrained to `T: Bindable`, implementation switched to Builder + Freeze; reflection path retires
- B3 — Migrate `examples/todo` to `derive(Bindable)`; verify end-to-end roundtrip
- B4 — Sum type Builder demo (validates the design beyond plain structs); deferrable

**Status:** Design specified. Implementation deferred — slices B1–B4 across multiple sessions.

---

## 2026-05-02: Layer 1 panic audit

**Context:** The 2026-04-15 FFI table set Layer 1 safety as "Arca prevents panic from generated code" via Option / safe cast / bounds. `*T` auto-wrap, `?` compile-error, Any + match type pattern all landed; bounds was the deferred piece. Verifying what is actually sealed before declaring Layer 1 done.

**Findings:**

Compile-time panic emission in generated Go is contained to 2 intentional sources — `Assert` (`emit.go:597`) for user-explicit `assert expr` and `Unreachable` (`go_lower.go` `buildEnumSwitch` / `buildSumSwitch` / `buildListIfChain`) as exhaustive-match Go-side default fillers. No accidental emission paths.

Runtime panic vectors:

- Nil deref: sealed (`*T` auto-wrap to `Option[Ref[T]]`)
- Type assertion failure: sealed (match type pattern is the only narrowing path)
- `?` outside Result: sealed (compile error since 2026-04-18)
- **Index out of bounds:** unsealed. `lowerIndexAccess` (`lower.go:3566`) lowers `arr[i]` to raw Go `arr[i]`
- **Integer division by zero:** unsealed. `a / b` passes through `TkSlash` binary op in `parser.go:980`
- **Slice / range:** indeterminate. `RangeExpr` standalone lowering at `lower.go:2252` carries a "shouldn't happen often" comment — survey before claiming either way

**Status:** Audit complete. 2-3 vectors remain before Layer 1 panic-prevention is structurally closed. Implementation deferred. Resume checklist in memory `project_panic_audit_2026_05_02.md`.

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
