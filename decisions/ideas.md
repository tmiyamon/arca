# Ideas (Not Yet Implemented)

Future features and design sketches. Newest first.

---

## 2026-04-21: Trait system Phase 1 — minimum viable traits for Error (design)

**Context:** 2026-04-19 "Error trait interface shape" fixed the Error trait's method surface (`fun message() -> String`), but left the underlying trait system unbuilt. `examples/todo` cannot construct Arca-side errors because there is no way to declare a type satisfies `Error`. 2026-04-11 "Trait system design" laid out the shape but explicitly deferred implementation until needed. That time has arrived — Phase 1 Layer 1 item 4 (todo demo) is blocked on having a real `Error` trait. This entry defines the minimum viable trait system that unblocks Error; richer features (default methods, trait inheritance, generic bounds) remain future work.

**Scope:** Only what Error trait needs to function end-to-end, plus the structural decisions that would be painful to change later (syntax form, IR nodes, Go emit strategy). Everything that can be deferred without forcing migration pain stays deferred.

### Syntax

**Decision:** Separate `impl` blocks, Swift-style `impl Type: Trait { ... }`.

```arca
trait Error {
    fun message() -> String
}

type NotFound {
    NotFound(id: Int)
}

impl NotFound: Error {
    fun message() -> String {
        "not found: " + self.id.toString()
    }
}
```

- Trait, type, and impl are three separate top-level declarations.
- Inline `type User: Trait { ... }` form rejected: prevents adding trait impls for types defined outside the module (stdlib vision requires `impl ext.Type: LocalTrait`).
- Rust-style `impl Trait for User` rejected: `for` keyword would be introduced just for this construct; `:` already means "satisfies / is-a" in Arca (`[T: Display]`, `trait Ord: Eq`, `name: String`), so `impl User: Display` stays consistent.
- The surface syntax can be migrated later with parser-only changes because the IR keeps trait/impl as separate concepts. Syntax is a sugar layer over `TraitDecl` / `TypeDecl` / `ImplDecl`.

### Orphan rule

**Decision:** Adopt Rust's orphan rule, further restricted in Phase 1.

- General rule: `impl T for X` requires *either* `T` or `X` to be declared in the current module.
- Phase 1 restriction: both `T` and `X` must be local. External-type / external-trait impls are Phase 2+.
- Forbidden forever: `impl ExtType: ExtTrait` (true orphan) — coherence problem, same reasoning as Rust.

"Allow impls on Go FFI types" (e.g., `impl sql.Rows: Decodable`) is Phase 2; the orphan rule keeps the door open without enabling it now.

### Trait as type (trait objects)

**Decision:** Trait names are valid type expressions. `Result[T, Error]`, `fun f(e: Error)`, `List[Error]` all compile.

- New IR node: `IRTraitType{Name: string}`, distinct from `IRInterfaceType` (which is `Any`).
- Required for the Error trait to be usable in `Result[T, Error]` — the primary motivating use case.

### Go emit

**Decision:** Trait → Go interface; impl methods → Go methods on the concrete type.

```go
// trait Error { fun message() -> String }
type ArcaError interface {
    Message() string
}

// impl NotFound: Error { fun message() -> String { ... } }
type NotFound struct { id int }

func (n NotFound) Message() string { ... }
```

- Arca method name (camelCase `message`) is capitalised to Go (`Message`) via the existing naming convention — same rule as `pub fun` on `type {}`.
- Go interface naming: `Arca<Trait>` prefix to avoid collision with Go stdlib (`Error`, `Reader` etc. are common).
- Go interface satisfaction is structural, so `impl NotFound: Error` needs no explicit registration — the Go compiler sees `NotFound` has `Message() string` and accepts it wherever `ArcaError` is expected.

### Dispatch

**Decision:** Dynamic dispatch only in Phase 1. Every `e.message()` emits as a Go interface method call, regardless of whether `e`'s static type is `Error` or a concrete impl type.

- Rationale: trait objects are in scope (required for Error), so dynamic dispatch must exist. Adding a separate static / monomorphised path would multiply implementation complexity for marginal gain at this stage.
- Arca rides on Go's interface dispatch: Go runtime handles vtable lookup, and Go's compiler optimises interface calls where profitable. No Arca-side code generation work for dispatch.
- Static dispatch (monomorphisation) is Phase 2 alongside generic bounds (`[T: Display]`).

### Coercion

**Decision:** Values of concrete impl types coerce implicitly to trait types at hint-driven positions. No explicit cast syntax.

- Mechanism: new `autoTraitLift` in `lowerExprHint`, mirroring the existing `autoSomeLift`. When the hint is `IRTraitType{T}` and the expression's type implements `T`, coercion is a no-op in emit (Go interface satisfaction is structural).
- Covered positions: function arguments, let annotations, return types, match arms, constructor fields.
- `Err(NotFound(1))` at a `Result[_, Error]` context automatically flows as `Error`.

### Method resolution

**Decision:** When lowering `x.foo()`, resolve in this order:

1. Inherent methods in `type X { fun foo() ... }` on `x`'s concrete type.
2. Trait impl methods from any in-scope `impl X: T` where `T` declares `foo`.
3. If `x`'s static type is a trait, the trait's method set.

Ambiguity — two or more candidates at the same rank — is a compile error in Phase 1 (see "Method name collision" below).

### Match type narrowing on trait types

**Decision:** The existing match type pattern (`match v { id: T => body }`, introduced 2026-04-19 for `Any`) extends to trait types unchanged.

```arca
fun handle(e: Error) -> String {
    match e {
        id: NotFound => "missing " + id.id.toString()
        id: Timeout  => "timeout"
        _            => e.message()
    }
}
```

- Emits as Go type switch, same as `Any` narrowing.
- No exhaustiveness check — trait types are open universe, new impls can appear anywhere.
- Users who want exhaustive matching define a sum type that implements the trait, and match on the sum type (existing sum-type exhaustiveness check applies).

### `self` syntax

**Decision:** Implicit `self` in trait method signatures and impl bodies, consistent with existing `type { fun }` convention.

```arca
trait Error {
    fun message() -> String       // no `self` in signature
}

impl NotFound: Error {
    fun message() -> String {
        self.id.toString()          // `self` bound in body
    }
}
```

- Rust's explicit `self` param exists because Rust encodes receiver kind (`self`, `&self`, `&mut self`) in the parameter. Arca is immutable + value-receiver-only, so the information content is zero.
- Deviates from the 2026-04-11 "Trait system design" proposal (which wrote `fun display(self)`) — that proposal predated the `type {}` implementation. Implementation-consistent wins over proposal.

### Receiver kind

**Decision:** Value receivers only. Emit as `func (v T) Method() ...` in Go.

- Arca is immutable; there is no mutable receiver semantic to express.
- Go's escape analysis promotes receivers to the heap when needed; no perf cliff.

### `Self` type and `static fun` in trait/impl

**Decision:** Both forbidden inside `trait { ... }` and `impl ... { ... }` in Phase 1.

- `Self` as a type (`fun compare(other: Self) -> Order`) requires object-safety analysis (traits using `Self` outside receiver cannot be used as trait objects, á la Rust's object-safety rules). Not needed for Error; defer.
- `static fun` in trait body has no practical use without generic bounds (`fun make[T: Default]() -> T { T.default() }`). Also deferred.
- Both are already supported in `type {}` bodies (`testdata/static_fun.arca`); the restriction applies only when they appear in a trait/impl context.

### Method name collision

**Decision:** Any collision between candidate methods on the same type is a compile error in Phase 1. No disambiguation syntax.

Collision sources, all rejected:
- Same method name across two trait impls on the same type.
- Method name shared between a trait impl and a `type {}` inherent method.
- Duplicate impl of the same trait on the same type (coherence violation).

Workarounds:
- Access the method through a trait-typed binding: `let d: Display = u; d.show()`.
- Rename one trait's method.

Disambiguation syntax (Rust's `<User as Display>::show(&u)` or similar) is Phase 2 if real demand emerges.

### Inherent `impl` blocks

**Decision:** Forbidden in Phase 1. `impl` always requires a `: Trait` clause. Inherent methods belong in `type { fun ... }`.

- Prevents TMTOWTDI ("two ways to add a method").
- Existing `type {}` body already handles inherent methods.
- Forbid at parser level with a helpful error message pointing to `type {}`.
- Future Phase 2 use case (adding methods to FFI types) can re-open this via a separate extension mechanism without syntax change.

### Error trait Go bridge

**Decision (refines 2026-04-19 "Error trait interface shape"):** Arca's `Error` trait emits as a distinct Go interface `ArcaError { Message() string }`, not as Go's `error` interface. A compiler-generated `Error() string` shim per impl satisfies Go's `error`. Go FFI `error` values are wrapped into `ArcaError` via `__goError` at the FFI boundary.

**Why a distinct interface, not aliasing Go's `error`:**
- SSOT layering: `Error` is an Arca concept; Go's `error` is an FFI implementation detail. Collapsing them locks future extensions (adding `source()`, `cause()`, `kind()` to the trait would diverge from Go's single-method `error`).
- Consistency with all other traits: `Display`, `Debug`, future `Eq`, `Ord` all emit as `Arca<Trait>`. Error as a one-off alias would be the only exception.

**Emit shape:**

```go
// One per trait.
type ArcaError interface {
    Message() string
}

// Per impl: the real method + the Go-error shim.
func (n NotFound) Message() string { return "not found: " + strconv.Itoa(n.id) }
func (n NotFound) Error() string   { return n.Message() }   // auto-generated

// Per project: the FFI wrapper for Go-side errors crossing into Arca.
type __goError struct { inner error }
func (e __goError) Message() string { return e.inner.Error() }
func (e __goError) Error() string   { return e.inner.Error() }
func (e __goError) Unwrap() error   { return e.inner }
```

**Boundary behaviour:**

| Direction | Mechanism | Cost |
|---|---|---|
| Arca `NotFound` → Go `error` param (e.g., `fmt.Errorf("%w", e)`) | Shim `Error() string` on the impl | None |
| Go `error` return → Arca `Error` | Wrap in `__goError{inner: err}` at FFI call site | 1 alloc per non-nil error |
| Arca `Error` → Arca `Error` propagation | No conversion | None |

`__goError` inserts at the same emit site as the existing `GoMultiReturn` handling for `(T, error)` returns. `Unwrap()` is mandatory so Go's `errors.Is` / `errors.As` see through the wrapper.

**Error `match` narrowing:** users distinguish specific error types via the match type pattern:

```arca
match err {
    id: NotFound      => handleNotFound(id)
    id: Unauthorized  => ...
    _                 => err.message()
}
```

Combined with the closed-universe sum type pattern (`type AppError { NotFound(...) | Timeout(...) }` with `impl AppError: Error`), users can get full exhaustiveness when they want it.

### `error` (Go interface) vs `Error` (Arca trait) — distinct concepts

**Decision:** The lowercase `error` (currently Arca's direct alias for Go's `error` interface) and the new `Error` trait are different concepts and must not be conflated.

- `error` = Go FFI implementation detail. Currently accepted as a type name (see `lower.go` allowed-types list) so that `(T, error)` Go returns surface as `Result[T, error]` in Arca.
- `Error` = Arca trait (capitalised per Arca type convention). What users write in Arca source.
- After Phase 1 migration, user-written Arca source uses `Error` only. Lowercase `error` is removed from the language surface. At the FFI boundary, Go `error` values wrap into `__goError` and surface as `Error`.

### Prelude-hosted `trait Error`

**Decision:** `trait Error { fun message() -> String }` is a prelude-built-in, same status as `Option` / `Result`.

- Users can write `Result[T, Error]` without any `import`.
- Compiler bootstraps the trait before lowering user code, so `Error` name resolves from day one even before any impl exists.
- Rejected alternative "user must `import arca.error`": adds ceremony to every file that returns Result, inconsistent with `Option` / `Result` being free.
- Rejected alternative "hardcode without a trait definition": makes `Error` a special case outside the trait system; adding `.message()` methods would need special emit; migration to user-defined Error-like traits would be painful.

### `error` removal migration plan

**Decision:** Plan C — remove lowercase `error` at Slice 4d, the same slice that lands the `__goError` wrapper.

- Slices 4a-4c introduce parser / lower / emit for traits without touching the lowercase `error` path. During this period, both `error` and `Error` coexist in the language (testdata using `Result[T, error]` continues to work).
- Slice 4d: land `__goError` wrapper, remove lowercase `error` from `isKnownTypeName`, change Go FFI `(T, error)` mapping to produce `Result[T, Error]` with `__goError` wrapping, update all testdata / examples / stdlib that referenced `error` to use `Error`.
- Plan A (gradual removal across many PRs) and plan B (everything in one slice) considered; both viable because the test suite catches any un-migrated call site. C chosen because the `__goError` wrap is the natural forcing function for migration — once it exists, lowercase `error` has no remaining purpose.

### Out of scope (Phase 1 explicitly excludes)

- Default method implementations.
- Trait inheritance (`trait Ord: Eq`).
- Generic bounds on functions / types (`fun f[T: Display](x: T)`).
- Monomorphisation / static dispatch.
- Disambiguation syntax for method collision.
- Inherent `impl` blocks.
- `Self` type, `static fun` inside trait/impl.
- Trait impls on Go FFI types (`impl sql.Rows: Decodable`).
- Object-safety analysis (trivially satisfied because Phase 1 forbids `Self` and generic methods).
- Value equality / comparison on trait-typed values (needs `Eq` trait, Phase 2).

### Implementation slices (drafted; not started)

1. **4a** Parser + AST. `TraitDecl`, `ImplDecl` top-level nodes. Methods inside use existing `MethodDecl`. Enforce orphan rule and inherent-impl ban in parser.
2. **4b** Lower. Trait registry, impl registry. `IRTraitType` IR node. Method resolution walks trait impls. `autoTraitLift` in `lowerExprHint`. Method collision check.
3. **4c** Emit. Trait → Go interface. Impl methods → Go receiver methods. No other per-trait emit logic.
4. **4d** Error trait shim. Detect `Error` trait, auto-generate `Error() string` on each impl. Emit `__goError` wrapper helper. Update FFI return handling to wrap errors via `__goError`.
5. **4e** Prelude `trait Error`. Optional: one stdlib error type (e.g., `StringError(msg: String)`) for quick user ergonomics.
6. **4f** Migrate `examples/todo` to use Arca-typed errors.
7. **4g** Docs sync — SPEC.md (trait section), DESIGN.md (trait rationale), this entry's status moved from "design" to "implemented".

**Status:** Slices 4a–4d2 landed 2026-04-22. Parser + AST, lower (IR + method resolution + unify + collision check), emit (trait → `Arca<Name>` interface, impl → exported Go method), prelude `trait Error` + auto `Error()` shim, lowercase `error` removed from the user surface, Go FFI `(T, error)` → `Result[T, Error]` via `__goError` wrap at match `Err` bindings. One deviation from the original design: `Error` emits as Go's stdlib `error` (not a distinct `ArcaError` interface) — the distinct-interface goal is achieved via `__goError` wrapping, which avoids pervasive signature churn across every Result-returning function. Remaining: 4e (stdlib `StringError` convenience type), 4f (`examples/todo` migration), 4g docs (landed 2026-04-22).

---

## 2026-04-19: Any type and match type pattern (implemented)

**Context:** Phase 1 Layer 1 item 3 — safe cast for Go's `interface{}` / `any`. Without this, Go FFI returns typed `any` (e.g. `ctx.Value(k)`, `map[string]any` values) were unusable on the Arca side, and unsafe `v.(T)` remained the only option in Go, with its panic risk. Design predated this session at `memory/design_any_safe_cast.md`.

**Decision (implemented):**

- `Any` type surfaces Go's `interface{}` in Arca. Maps to `IRInterfaceType` at the IR level and `interface{}` at emit.
- Narrowing via `match v { id: T => body, _ => ... }` — a new `TypePattern` AST node, `IRMatchTypePattern` IR node.
- Emits as Go type switch: `switch __tv := v.(type) { case T: id := __tv; ... ; default: ... }`.
- No unsafe `v.(T)` cast syntax in Arca — the failed-assertion panic source is kept out of the language surface.
- Go FFI returns typed `any` / `interface{}` map to `IRInterfaceType` in `goTypeToIRWithVars`, so APIs like `ctx.Value(k)` are usable.
- No exhaustiveness check on type matches — the type universe is open; a wildcard arm (`_ => ...`) is recommended.

**Execution:**
- Slice 3a: `lowerNamedType` "Any" case → `IRInterfaceType{}`; registered in `isKnownTypeName`.
- Slice 3b: Go `any` / `interface{}` → `IRInterfaceType` in `goTypeToIRWithVars`.
- Slice 3c: Parser recognises `Ident ":" Type` in pattern position; `TypePattern{Binding, Target}` AST node.
- Slice 3d: `isTypeMatch` / `lowerTypeMatch` dispatch; binding registered with narrowed type; `IRMatchTypePattern{Binding, Target}`.
- Slice 3e: `emitMatchType` emits `w.SwitchType("__tv", subject, ...)` with per-case `Assign(binding, "__tv")` when the arm's binding name differs from `__tv`.
- Slice 3f: `testdata/any_match.arca` + `.go` snapshot covering Int/String/Bool + default.

**Future sugar (deferred):** `as?` (Swift) and `is` + smart cast (Kotlin). Not needed — match type pattern covers all cases with acceptable ergonomics.

---

## 2026-04-19: Two-stage IR and Option as pointer-backed uniformly (idea)

**Context:** While implementing auto-Some and preparing item 1 (FFI param/field/generic wrap), the test `let x: Option[Int] = Some(10)` exposed a structural bug: emit outputs `x, _ := &10` (invalid Go). Root cause is not a single missing case — it is that IR represents `Option<T>` as a single-slot type while Go emits it as 2-slot `(T, bool)`. The bridging machinery (`ExpandedValues` on leaves, `SplitNames` on declarations, `ctx.splits` on use sites, `flattenArgs` on call args, `resolveMatchBindings` on match subjects, `expandFuncParams` on params) covers the mismatch piecewise. Any new IR position without an updated bridge produces broken Go. This is structural debt, not a local missing case.

Separately: `design_ref_ptr_layers` already specifies "Option<T> where T is primitive → `*T` (nil = None)" as the emit rule, but the current implementation emits `(T, bool)` for `Option<Int>`, `Option<String>`, etc. Spec drift.

**Decision:**

### Part A — Option is pointer-backed uniformly (spec alignment)

Every `Option<T>` emits as a single Go value:

| Arca type | Go emit |
|---|---|
| `Option<Int>` | `*int` |
| `Option<String>` | `*string` |
| `Option<User>` | `*User` |
| `Option<Ref<T>>` | `*T` (collapses — Ref is already `*T`) |
| `Option<Option<Int>>` | `**int` |
| `Option<List<T>>` | `*[]T` |

- `Some(v)` → `&v` (taking address of the value; escape analysis moves to heap if needed)
- `None` → `nil` (typed nil at the Go declaration)
- Match `match opt { Some(v) => ..., None => ... }` → `if opt != nil { v := *opt; ... } else { ... }` (already partially implemented for `Option<Ref<T>>`; generalize)

This eliminates Option-specific split machinery entirely: no Option SplitNames, no Option ExpandedValues, no Option case in `flattenArgs` / `resolveMatchBindings` / `expandFuncParams`. Result keeps multi-return — its `(T, error)` shape is Go-idiomatic and Go's own error convention.

### Part B — Two-stage IR (architectural reframe)

The existing `expandResultOption` post-pass is implicitly a "stage 2" lowering, but it **annotates** rather than rewrites: Option/Result nodes survive into emit, with side-band metadata. Emit then reconciles. That reconciliation is where the bugs live.

Formalize `expandResultOption` into a true lowering: after it runs, Option and Result nodes no longer exist in the IR. Everything is already in its emit-ready shape (pointer types, multi-slot tuples, etc.). Emit becomes mechanical — no bridge interpretation.

Conceptual split:

```
Stage 1 (Logical IR): Option<T> / Result<T,E> alive, single-slot semantics
      ↓ stage2Lower (what expandResultOption grows into)
Stage 2 (Emit-ready IR): Option→Pointer, Result→MultiSlot, Some→Ref, None→typed nil, Ok/Error→tuple
      ↓ emit (mechanical)
Go
```

Mapping (Stage 1 → Stage 2):

| Stage 1 | Stage 2 |
|---|---|
| `IROptionType{T}` | `IRPointerType{T}` (or collapsed if T is already pointer) |
| `IRSomeCall{v}` | `IRRefExpr{v}` (produces `&v`) |
| `IRNoneExpr` | `IRNilLit{typed}` |
| `IRResultType{T,E}` | `IRMultiSlotType{T, error}` (explicit 2-slot) |
| `IROkCall{v}` | `IRMultiValue{v, nil}` |
| `IRErrorCall{e}` | `IRMultiValue{zero(T), e}` |

After stage 2, emit sees only IRPointerType, IRMultiSlotType, IRRefExpr, IRNilLit, IRMultiValue — uniform representation.

### Rationale

- Single ownership for "how does Option emit": stage 2 lowering. Current machinery is 5-7 consumers each with partial knowledge.
- Spec drift auto-resolved (Option<Int> → *int) because spec-compliant rule is the stage 2 rule.
- Bugs like `let x: Option[Int] = Some(10)` become impossible — no splits for Option, so no split-inconsistency to expose.
- Future features (Trait system, effects) won't accumulate new bridging entry points.
- Compile-time cost is marginal — `expandResultOption` is already a linear pass.

### Rejected alternatives

- **Synthetic Option/Result struct types internally** (reverting commit `5fc89da`): recovers uniformity but abandons idiomatic-Go output, conflicts with 2026-04-12 "symmetric boundary" direction.
- **Patch current multi-return bugs without refactor**: fixes today's test but leaves structural debt. Same-shape bugs recur as new positions are added.
- **Keep Option multi-return, only fix flow gaps**: small now, but Option-primitive is already spec-deviant per `design_ref_ptr_layers`; the deviance would compound rather than resolve.

### Execution plan (incremental)

1. **Slice 1: Option pointer-backed + auto-Some.** Change lower/emit paths so every `IROptionType` at emit is a pointer. Remove Option-specific split code paths (`SplitNames` Option branch, `ExpandedValues` on `IRSomeCall`/`IRNoneExpr`, Option case in `flattenArgs` / `resolveMatchBindings` / `expandFuncParams`, Option-split branch in `emitLetStmt`). Generalize pointer-backed match to all `Option<T>`. Auto-Some already lifts to `IRSomeCall` — same IR, just different emit.
2. **Slice 2: FFI param/field/generic wrap (item 1).** Now that Option is uniform, `wrapPointerInOption` at param/field/generic-inner positions produces `Option<Ref<T>>` that emits cleanly. Auto-Some eats the ceremony.
3. **Slice 3 (later): Result stage 2 formalization.** Introduce `IRMultiSlotType`, rewrite Result lowering to produce it, remove Result-specific emit logic.

### Auto-Some interaction

Auto-Some (2026-04-19 companion entry) produces `IRSomeCall` at the logical IR level. Under the new scheme, stage 2 rewrites that to `IRRefExpr`. The lift logic itself is unchanged; the change is downstream in how `IRSomeCall` emits.

**Status:** Idea. Direction settled. Slice 1 starts next. No compile-time perf concern; IR passes stay linear.

**Progress (2026-04-19):** Slice 1 (Option pointer-backed + auto-Some) implemented. Option-specific split machinery removed; `match opt` uniformly `if subject != nil`; FFI `(T, bool)` wrapped via `__optFrom`; `Some(v)` via `__ptrOf(v)` with Ref/Ptr collapse; `None` as typed nil. Known bugs from the session handoff (`let x: Option<T> = None`, `Some(v)` in let position) auto-resolved.

**Progress (2026-04-19, continued):** Slice 2 (FFI param/field/generic-inner wrap) implemented. `wrapPointerInOption` now applied at Go FFI param types in `instantiateGeneric`, struct field resolution in `resolveFieldType`, and recursively through generic inners. Go FFI call arg lowering propagates the wrapped param type as a hint so auto-Some lifts `&v` → `Some(&v)` at the call site — no user ceremony needed. Slice 3 (Result stage-2 formalization with `IRMultiSlotType`) still deferred.

---

## 2026-04-19: Auto-Some — hint-driven implicit Option lift (idea)

**Context:** Item 1 of the SSOT Layer 1 roadmap (the `*T` → `Option<Ref<T>>` extension to param/field/generic-inner positions) was deferred because every FFI call site would need `Some(&v)` / `None` ceremony. The `tags { arca: nonnull }` escape hatch was identified as the relief valve but is unimplemented. Separately, Option construction requires `Some(v)` per explicit-first.

**Decision:** At hint-driven positions where the expected type is `Option<T>` and the value's static type is `T`, auto-lift the value into `Some(v)`. Single layer only. `None` stays explicit. `&` (Ref construction) is **not** auto-inserted.

Applies at: function args against typed params, let / field assignment with explicit type, return positions, match arm results. Patterns are not touched — `Some(v)` / `None` in match stay as-is.

- `let x: Option<Int> = 5` → `Some(5)`
- `let x: Option<Int> = None` → `None`
- `let x: Option<Int> = opt` where `opt: Option<Int>` → pass-through, no double-lift
- `let x: Option<Option<Int>> = 5` → compile error (1-layer rule; forces `Some(Some(5))` vs `Some(None)` intent to be explicit)
- `foo(&v)` where `foo(p: Option<Ref<T>>)` → `foo(Some(&v))` (`&` stays explicit)
- `foo(None)` → `foo(None)`

**Rationale (explicit-first re-reading, not a new exception):**
- Typed `Option<T>` position + value `T`: writing `Some` is information-neutral — the type annotation already says "nullable". Only `None` carries information at that position.
- `&v` vs `v`: value/reference is a genuine 2-choice; auto-insertion would erase intent.
- `Some(Some(v))` vs `Some(None)`: legitimately-nested Option (e.g. `Map<K, Option<T>>.get` distinguishing "key missing" from "key present with null value") relies on the 2-layer distinction. Auto-multi-lift would destroy it.

The existing explicit-first bullet "Option creation: `Some(v)` / `None` required" tightens to "`None` required; `Some(v)` auto-lifted at typed Option positions." No new language exception needed.

**Effect on item 1:** Unblocks param / field / generic-inner wrap without waiting for `tags { arca: nonnull }`. `nonnull` tag is repositioned as a pure safety feature (reject `None` at the type level), not a ceremony reliever. The two are complementary.

Rejected alternatives:
- Multi-layer lift: destroys the `Some(None)` vs `Some(Some(v))` distinction.
- Auto-Ref alongside auto-Some: value/reference is a real 2-choice, not ceremony.
- Lint warning on declared `Option<Option<T>>`: inconsistent with the rest of the language (no other type-shape warnings). Stays legal.

**Status:** Idea. Small implementation (tail of `lowerExprHint` in `lower.go` — inject a `Some` wrap step when hint is `Option<T>` and value type unifies with `T`). Paired with item 1 implementation in the same slice.

**Progress (2026-04-19):** Implemented as `autoSomeLift` at the tail of `lowerExprHint` in `lower.go`. Skips when result is already Option, is an unresolved `IRInterfaceType`, or is a free `IRTypeVar` (leaves those to `checkTypeHint`). Covered by `testdata/auto_some.arca`. Paired with the Option pointer-backed refactor (same 2026-04-19 slice).

---

## 2026-04-19: Error trait interface shape (idea)

**Context:** `Result[T, Error]` is the standard error-carrying type in Arca. Go FFI error values need to flow through as Arca Errors, and Arca Errors need to interoperate with Go's `error` interface at the FFI boundary. To close this loop, the `Error` trait needs a concrete shape. Related: `decisions/ideas.md` 2026-04-12 "Go FFI nullable/pointer ambiguity" (Error representation sub-topic), `decisions/ideas.md` 2026-04-11 "Trait system design".

**Decision:** Minimum trait with one method; compiler bridges to Go's `error` interface.

```arca
trait Error {
  fun message() -> String
}
```

- Arca call: `e.message()`
- Go emit: Arca types implementing `Error` automatically receive a `Error() string` method on the Go side so they satisfy Go's `error` interface transparently. The shim just calls `message()`.
- Arca naming (`message()`, camelCase) stays consistent with Arca conventions — Go interop is an implementation detail of trait emission.

Rejected alternatives:
- `fun Error() -> String` — breaks Arca's camelCase convention just to match Go's case.
- `fun source()`, `fun backtrace()` etc. — premature; add later if a concrete use case appears.

**Status:** Design only. Blocked on trait system implementation (2026-04-11 "Trait system design"). Sub-decisions deferred:
- How are Arca-native named errors (`NotFound`, `Unauthorized`, ...) modeled? (enum / sum type / struct per variant / mix)
- How does Arca Error → Go error and back preserve source / wrap chain (`errors.Is`, `errors.As`)?

---

## 2026-04-19: Ref / Option / Ptr three-layer memory model (idea)

**Core idea:** Separate three concepts that Go conflates into `*T`: `Ref<T>` (safe non-null reference), `Option<T>` (nullable), `Ptr<T>` (FFI-internal, unsafe). Users see `Ref` and `Option`; `Ptr` is compiler-internal. Immutability makes the model work without borrow checker complexity.

**Motivation:** Prior "`*T` → `Option[*T]` auto-wrap" (2026-04-18) was a local fix that conflated "nullable pointer" with "optional pointer". The "double unwrap" framing was a lie — `?` silently stripped two layers at once, mixing Result/Error semantics with Option/Absence semantics. The ChatGPT conversation (2026-04-19 session) framed this more cleanly: concepts are independent and should stay separate.

### Conceptual separation

| Concept | Type | Role |
|---|---|---|
| Value | `T` | Default, immutable, copy semantics |
| Safe reference | `Ref<T>` | Non-null, shared access (immutable) |
| Nullable | `Option<T>` / `T?` | "Value present or absent" |
| FFI-internal | `Ptr<T>` | Raw Go pointer, nullable, compiler-only |

Rules:
- `Ref<T>` is guaranteed non-null (enforced via construction rules, not the type itself)
- `Option<T>` is the only way to express "might be absent"
- `Ptr<T>` exists only in IR / FFI boundary; no user syntax
- Mixing Option with pointer types is natural: `Option<Ref<T>>`

### Syntax

| Surface | Meaning |
|---|---|
| `T` | Value, copy semantics |
| `Ref<T>` | Safe reference, non-null |
| `Option<T>`, `T?` | Nullable, sugar form exists |
| `&v` | Ref-creation expression |
| `Some(v)` / `None` | Option constructors |
| `*T` | Not valid Arca syntax (current `*T` user-facing form is removed) |

### FFI boundary mapping

Every Go `*T` position receives a corresponding Arca type. Defaults are nullable (`Option<Ref<T>>`) because Go pointers can hold nil at any of these positions; stricter types are opt-in via a future `tags { arca: nonnull }` marker.

| Go position | Arca type | Rationale |
|---|---|---|
| Return `*T` | `Option<Ref<T>>` | nil return is "valid absence" |
| Return `(*T, error)` | `Result<Option<Ref<T>>, E>` | same, plus Go's error channel |
| Return `(T, error)` | `Result<T, E>` | unchanged |
| Return `(T, bool)` | `Option<T>` | unchanged |
| Param `f(p *T)` | `Option<Ref<T>>` | Go params accept nil |
| Struct field `F *T` | `Option<Ref<T>>` | Go fields can hold nil |
| Method receiver `(p *T)` | `Ref<T>` at call site | Arca requires non-null for method calls; caller unwraps first |
| Generic inner `List[*T]` | `List[Option<Ref<T>>]` | Go slices/maps may hold nil elements |

Runtime value transitions at the boundary:

| Go value | Arca value |
|---|---|
| `(v, nil)` | `Ok(Some(Ref v))` |
| `(nil, nil)` | `Ok(None)` |
| `(_, err)` | `Err(err)` |
| `nil` alone (return) | `None` |
| `v` alone (return) | `Some(Ref v)` |

`(nil, nil)` is the critical case: it's "successfully found no value", not an error. Converting to `Err(...)` was rejected as semantic corruption.

**Override (future):** `tags { arca: nonnull }` on a Go field, param, or generic argument opts into `Ref<T>` directly, bypassing the Option wrap. Useful when the API author guarantees non-null by construction. Not yet designed in detail.

### IR representation

- `IRRefType{Inner: T}`: new IR node for Arca's safe reference
- `IRPointerType{Inner: T}`: retained, but restricted to FFI-internal use (raw Go pointer before boundary wrapping)
- `IROptionType{Inner: T}`: unchanged
- `IROptionType{Inner: IRRefType{Inner: T}}`: the canonical FFI-wrapped nullable pointer

Go emit:
- `Ref<T>` → `*T`
- `Option<Ref<T>>` → `*T` (nil = None, non-nil = Some)
- `Option<T>` where T is primitive → `*T` (nil = None) — existing convention
- `IROptionType{Inner: IRPointerType}` → collapses to `*T` (don't emit `**T`)

### Ref construction (non-null guarantee)

`Ref<T>` is non-null by **construction rules**, not type-level proof:

| Source | Syntax | Safe? |
|---|---|---|
| Value (lvalue) | `&v` | Yes (v has concrete value) |
| Field | `&user.name` | Yes (user exists) |
| Ref field | `&r.field` | Yes (r is non-null) |
| FFI return | compiler-generated wrapping | Yes (boundary check) |
| Ptr | not allowed directly | Must go through `Option<Ref<T>>` |

No user-visible escape hatch. The only way to get a Ref from a Ptr is through boundary wrapping that produces `Option<Ref<T>>` (requires explicit unwrap).

### Ref as return type

Functions may return `Ref<T>`:
```arca
fun getUser(name: String) -> Ref<User> {
  let u = User(name, ...)
  &u
}
```

`Ref<T>` at a return position guarantees three things:
1. **Non-null** — enforced by construction rules
2. **Valid (non-dangling)** — delegated to Go's GC; escape analysis moves captured locals to the heap
3. **Immutable** — Arca is immutable by default

Lifetime management is not the user's concern. The compiler emits the Go pointer; the GC keeps the referenced value alive as long as any reference exists.

**Typical uses:**
- Avoid copying a large struct
- Interop with Go functions that take `*T`
- Share the same underlying value among multiple callers (equivalent to copy in semantics because of immutability, better in memory)

**Edge cases (all safe):**
- Ref to a local value: Go escape analysis moves it to the heap.
- Ref into a field of another Ref: parent held alive by GC keeps the child valid.
- Ref to a slice element: Arca slices are immutable, so no reallocation invalidates the pointer.

For small primitives (`Int`, `Bool`, short `String`), `Ref<T>` usually costs more than direct-value return. For large structs or FFI interop, it pays off.

### Auto-deref

Explicit-first principle applies, with **one exception**: field access and method calls on `Ref<T>` auto-deref.

| Operation on `r: Ref<T>` | Behavior |
|---|---|
| `r.field` | Auto-deref: returns T's field |
| `r.method()` | Auto-deref: dispatches to T's method (Go handles it) |
| `r.field = x` | Forbidden (immutable) |
| `r == r2` | Explicit (no auto-deref for comparison) |
| Pattern match on r | Explicit deref if needed |
| Arithmetic, format | Explicit operations |

Rationale: field/method access is the most common operation, and `(*r).field` is visually noisy. Rust, Go, Swift, Kotlin, Java all follow this convention. Per the "make-everything-explicit" principle, this is the documented exception.

### Match patterns

Patterns preserve the type they match against — no implicit deref of `Ref<T>`:

| Subject type | Pattern | Binding type |
|---|---|---|
| `Option<Ref<User>>` | `Some(v)` | `v: Ref<User>` |
| `Option<T>` | `Some(v)` | `v: T` |
| `Result<Ref<User>, E>` | `Ok(u)` | `u: Ref<User>` |
| `Result<T, E>` | `Error(e)` | `e: E` |

Usage with auto-deref keeps typical code natural:
```arca
match opt {  // opt: Option<Ref<User>>
  Some(v) => v.name         // v: Ref<User>, field access auto-derefs
  None => "anonymous"
}
```

Rejected alternatives:
- Auto-deref in patterns (`Some(v)` binds `v: User`) — implicit, violates explicit-first.
- Mandatory deref syntax (`Some(*v)` for unwrap) — unnecessary ceremony for the common case (auto-deref covers field/method).

Explicit pattern deref syntax (e.g. `Some(*v)` → `v: User`) is left open for future addition if a concrete need emerges. `Ref v` as a destructuring pattern is not introduced — `Ref<T>` is transparent at the pattern level.

### Generic composition

`Ref<T>` composes with generic types like any other type — no special rule:

| Arca | Go |
|---|---|
| `List<Ref<User>>` | `[]*User` |
| `Map<K, Ref<V>>` | `map[K]*V` |
| `Option<List<Ref<T>>>` | `*[]*T` (list itself nullable) |
| `List<Option<Ref<T>>>` | `[]*T` (nil entries allowed) |

Type preservation at access sites keeps patterns consistent:
```arca
let users: List<Ref<User>> = [&alice, &bob]
let first = users[0]          // first: Ref<User>
let name = users[0].name      // auto-deref on field access
```

Construction uses `&` per the Ref construction rules; literals populate each slot explicitly. Empty collection type inference behaves as for any other element type — driven by annotation hint.

### `?` operator

Single-layer unwrap, context must match:

| Context fn returns | `?` operand | Behavior |
|---|---|---|
| `Result<T, E>` | `Result<T, E>` | Err propagates |
| `Result<T, E>` | `Option<T>` | **Compile error** (no implicit conversion) |
| `Option<T>` | `Option<T>` | None propagates |
| `Option<T>` | `Result<T, E>` | **Compile error** |
| (other) | any | Compile error (need try block or Result return) |

`??` operator is **not** introduced. The "double unwrap" convenience is replaced by explicit conversion.

### Option ↔ Result conversion

No implicit conversion. Two idiomatic paths:

**Monadic pipeline (recommended for FFI chains):**
```arca
fun parseHost(s: String) -> Result<String, E> {
  url.Parse(s)
    .flatMap(opt => opt.okOr(NotFound))
    .map(u => u.Host)
}
```

**Method chain with `?`:**
```arca
fun parseHost(s: String) -> Result<String, E> {
  let u = url.Parse(s)?.okOr(NotFound)?
  Ok(u.Host)
}
```

The monadic form keeps the outer type constant (Result throughout), avoiding the zigzag (Result → Option → Result → T) that the method-chain form produces.

**stdlib required:**
- `Result<T, E>`: `.map(f)`, `.flatMap(f)`, `.mapError(f)`
- `Option<T>`: `.map(f)`, `.flatMap(f)`, `.okOr(err)`, `.okOrElse(fn)`

### Explicit-first principle

Where there's a choice between implicit and explicit, choose explicit:

- Ref creation: `&v` required (no auto)
- Option creation: `Some(v)` / `None` required
- FFI param passing: no auto-address (user writes `Some(&v)` or `None`)
- Option/Result conversion: `.okOr(err)` explicit
- `okOr` eager vs lazy: both provided (`okOr`, `okOrElse`)
- `Ref<mut T>` visibility: forbidden to user (kept internal)

**Exception:** auto-deref on field/method (see above).

### Mutation: `Ref<mut T>` is hidden

Arca is immutable at user level. `Ref<mut T>` does not appear in user syntax. It exists in IR only to let Synthetic Builder absorb FFI mutation (`c.Bind(&target)`, `rows.Scan(&id, &body)`, etc.). The Builder mechanism is the only sanctioned bridge between user-immutable Arca and mutation-requiring Go APIs.

### Synthetic Builder integration

For each Arca type with relevant tags, the compiler generates a per-type Builder. The Builder uses `Ref<mut T>` internally to receive Go mutation, then freezes into an immutable Arca value.

| Sub-decision | Choice |
|---|---|
| A: Generation policy | Tag-based (types with `tags { json, db }` get a Builder) |
| B: User API | stdlib generic functions (`stdlib.bind[T]`, `stdlib.scan[T]`), not per-type methods |
| C: Completeness | Type signature drives: `Option<T>` fields default to `None`, others are required |
| C: Constraint check | At freeze time (not setter time) |
| C: Typestate | Deferred — runtime check is sufficient for now |
| D: FFI operation split | Builder is internal core; stdlib functions are thin wrappers |
| E: Builder type | Per-type, compiler-generated (`__TodoBuilder` etc.), user-invisible |
| F: freeze semantic | Returns `Result<T, E>` (Go FFI errors + Arca constraint errors merged) |

User-facing shape:
```arca
fun handler(c: Ref<echo.Context>) -> Result<Todo, E> {
  let todo = stdlib.bind[Todo](c)?
  // ...
}
```

The Builder itself never appears in user code.

### What this replaces

- 2026-04-18 "`*T` → Option auto-wrap": systematic `*T` handling was a first attempt. This 3-layer model generalizes and cleans it up.
- Current user-facing `*T` syntax (seen in `testdata`, `examples/todo`): removed. Users write `Ref<T>` or rely on FFI-generated types.
- "double unwrap" semantics of `?`: replaced by explicit single-layer unwrap plus monadic conversion.
- `NilCheckReturnValues` IR mechanism: no longer needed — `?` never mixes Option and Result.

### Open questions (deferred)

Error trait interface is now resolved — see the 2026-04-19 "Error trait interface shape" entry above. Sub-questions (how named errors are modelled; how wrap chains propagate) remain open there.

### Prior art and references

- **Rust**: `&T` (non-null), `*const T` (unsafe, nullable), `Option<&T>` (safe nullable reference). Same separation, compiled behind borrow checker.
- **Kotlin / Swift**: "reference type" is the default, nullable via `T?`. Works because class reference ≠ pointer.
- **Zig**: `?T` for optional, `*T` for pointer. Explicit `.?` unwrap.
- **Roc**: platform/app split; app is pure, host is mutable. Analogous to Arca's Builder pattern.

### Status

Idea. Direction is settled. Implementation is partial:

- **Return-position wrapping** (`*T`, `(*T, error)`, and now nested pointers inside generics like `[]*T`): implemented. `wrapPointerInOption` walks the IR recursively and turns every `IRPointerType` leaf into `IROptionType{IRRefType{...}}`.
- **Param / field / generic-inner wrapping**: implemented (2026-04-19 continuation, Slice 2). `wrapPointerInOption` is applied at Go FFI param types (in `instantiateGeneric`), struct field resolution (`resolveFieldType`), and recursively into generic inners. Arg lowering propagates the wrapped param type as a hint into `lowerExprHint` so auto-Some lifts `&v` into `Some(&v)` at FFI call sites — no ceremony. The transitional `unify` compat between `IRPointerType` and `IRRefType` remains because legacy `*T` Arca syntax still parses to `IRPointerType`; removed when that syntax is retired.
- **Receiver**: handled via method dispatch, which unwraps either `IRPointerType` or `IRRefType`. No separate wrap needed.

Why the param/field/generic-inner wrap is deferred:

Forcing `*T` param types to `Option<Ref<T>>` demands `Some(&v)` / `None` ceremony at every FFI call site (or uses at the opt-in `tags { arca: nonnull }` override, which doesn't exist yet). This would rewrite dozens of test sources and stdlib signatures for little ergonomic gain before the override mechanism lands. The decision is recorded; the implementation is paused until either:
- the `arca: nonnull` tag mechanism is implemented (relieves the ceremony for obviously-non-null pointers), or
- a dedicated session can absorb the full migration cost.

Blocks (broader rollout):
- Refactor of remaining `IRPointerType` callers at the param / field / generic-inner paths.
- Design and implementation of `tags { arca: nonnull }` as an ergonomic escape hatch.
- Synthetic Builder generation is its own multi-phase implementation.
- Existing code migration for `testdata`, `examples/todo`, stdlib signatures.

Removing the transitional `unify` compat requires the full rollout above.

No current estimate; this is a multi-week shift in Arca's FFI model.

---

## 2026-04-12: Symmetric boundary conversion — eliminate synthetic types (idea)

**Core principle:** The Go↔Arca type conversion should be symmetric. Currently Arca wraps Go multi-return into Result/Option at call sites (Go→Arca), but doesn't unwrap back at function boundaries (Arca→Go). Making the conversion bidirectional eliminates the need for synthetic runtime types (`Result_`, `Option_`, `Ok_`, `Err_`, `Some_`, `None_`) in generated Go.

**Symmetric mapping:**

| Go | Arca (IR) | Go (emit) |
|---|---|---|
| `(T, error)` | `Result[T, Error]` | `(T, error)` |
| `error` | `Result[Unit, Error]` | `error` |
| `(T, bool)` | `Option[T]` | `(T, bool)` |

- **Call site (Go→Arca):** Go multi-return is wrapped into Result/Option. Already implemented.
- **Function boundary (Arca→Go):** Result/Option is unwrapped back to Go multi-return / error / (T, bool). **New.**

**Why synthetic types disappear:**
- `Result_[T, E]` struct → replaced by native Go `(T, error)` multi-return
- `Option_[T]` struct → replaced by native Go `(T, bool)` multi-return
- `Ok_() / Err_() / Some_() / None_()` → replaced by native tuple construction
- `IsOk / Valid` discriminator flags → replaced by `err == nil` / `bool` check (Go's own conventions)
- Stored Result/Option values → degrade to Arca's existing `IRTupleType` emit (`struct{ First T; Second E }`), no separate synthetic type needed

**What this achieves:**
- Generated Go is **idiomatic Go**. No alien types, no Scala-style synthetic class problem.
- Type safety lives entirely in the **compiler (IR + lowerer)**, not in generated runtime structs.
- Go tools (go vet, godoc, debuggers) work naturally on output.
- `fun handler() -> error` just works — boundary auto-unwraps `Result[Unit, Error]` to Go `error`.
- `?` emits as native `if err != nil { return ..., err }` — standard Go error handling.

**Requires:**
- Error trait (so internal Arca uses `Error` not Go's nullable `error`)
- Trait system (minimal: just `trait Error`)
- emit.go rewrite for Result/Option → native Go patterns
- All testdata snapshot regeneration

**Prior art:** Roc's platform model (pure immutable app, mutable host, typed boundary). Arca's structure is nearly identical — immutable app, Go as mutable host, stdlib as boundary. The difference is Arca rides Go's existing ecosystem.

**Status:** Idea. Direction is settled. Blocked on trait system (minimal Error trait). Estimated scope: 4-5 days.

---

## 2026-04-12: Go FFI nullable/pointer ambiguity and the stdlib boundary (idea)

**Context:** Arca wraps Go's `(T, error)` as `Result[T, error]` and `error` alone as `Result[Unit, error]`. Writing `fun handler() -> error` clashes because the body returns `Result[Unit, error]`. This friction led to a discussion about how to model Go's `error` in Arca's type system, which in turn exposed a deeper problem: **Go's nullable/pointer semantics are implicit and ambiguous, and Arca can't auto-infer intent from Go type signatures alone.**

Go's three meanings of `*T` / interface:
- **Reference** — `func(db *sql.DB)` — not meant to be nil, just passing by reference
- **Nullable value** — `func() *User` — nil = not found
- **Error convention** — `func() error` — nil = success, non-nil = failure

All three look the same in Go's type system. No annotation distinguishes them. The compiler can't decide for the user which is which.

**Conclusion:** The compiler should not try to auto-infer Go's nullable/pointer intent. Instead:

1. **Go FFI stays as-is** — convention-based transformations only (`(T, error)` → Result, `(T, bool)` → Option). Pointer types, interface values, and nullable semantics pass through unchanged.
2. **Safe APIs live in stdlib / Arca modules** — human-written wrappers that translate Go's ambiguous types into precise Arca types (Result, Option, non-nullable values). This is the `design_stdlib_vision.md` direction: Database, Http, Json modules that hide Go internals.
3. **Go FFI direct use = user responsibility** — when users write `import go "..."` and call Go directly, they accept Go's nullable/pointer rules. A potential future `.go` accessor idea marks this boundary visually.

**Prior art: Roc's platform model.** Roc separates app (pure, immutable) from platform (Rust/C host, mutable, impure). All side effects are dispatched through the platform's typed interface; the app never touches foreign memory directly. Arca's structure is nearly identical — app is immutable, Go is the mutable/impure host, stdlib is the boundary. The difference is Arca rides Go's ecosystem (stdlib wraps existing Go libraries) while Roc requires a purpose-built host.

### Error representation (sub-topic, open)

The `error` mapping has a separate design question:

**Option A — `error` = `Result[Unit, Error]` surface alias (current leaning):**
- Semantically correct (Ok = success, Err = failure), `?` works naturally
- Users write `-> error` as a surface shortcut for `-> Result[Unit, Error]`
- Requires `Error` trait (blocked on trait system)
- Matches Rust/Haskell convention

**Option B — `error` = `Option[Error]` (explored and deferred):**
- Initially attractive: Go's `error` is nullable, `Option` models nullable
- Problem: `Option`'s semantic direction inverts for errors — `Some` is the bad path, `None` is the good path. Clashes with `?` / safe navigation semantics where `Some` is normally the good path.
- Problem: if nullable = Option, the same argument applies to ALL Go interfaces/pointers, not just error. Leads to `Option[*User]`, `Option[Handler]`, etc. everywhere — impractical.
- Deferred: not the right model.

**Hard constraint (both options):** `error = Option[error]` (self-referential) is forbidden. The alias must go through `Error` as a trait. Blocked on trait system.

**Status:** Idea. The Go FFI boundary principle (compiler doesn't auto-infer, stdlib wraps) is fairly settled. The error representation (Option A vs B) leans toward A but needs the trait system first.

---

## 2026-04-12: `error` as `Option[Error]`, nullable out of `Result` (idea, see above)

**Explored and deferred.** Initially proposed mapping Go `error` to `Option[Error]` with `Error` as a trait. Ran into two problems: (1) Option's `Some`/`None` semantics invert for errors (Some = bad, None = good), clashing with `?` and safe navigation; (2) if nullable = Option applies to error, it should apply to all Go interfaces/pointers, which is impractical. Folded into the FFI boundary idea above as "Option B (deferred)". The `error = Option[error]` self-referential alias is explicitly forbidden.

See the FFI boundary entry above for the full discussion and the current leaning (Option A: `error` = `Result[Unit, Error]` alias).

---

## 2026-04-11: Trait system design (proposal)

**Context:** Arca needs some form of polymorphism to scale beyond simple scripts. Research showed Kotlin/Rust-style traits fit Arca's positioning (Go ecosystem + type safety). But Arca isn't yet at the size where trait absence blocks development — more fundamental features (module system, error handling) come first.

**Proposed design (not yet implemented):**

- **Nominal traits** — explicit implementation required
- **Arca-defined types only** — Go FFI trait implementation deferred
- **No higher-kinded types** — library authors only need it, aligns with Rust/Kotlin
- **Compiles to Go interfaces** — structural interface is the underlying target
- **Trait bounds on generics** — `fun f[T: Display & Eq](x: T)`
- **Multiple constraints with `&`** — Swift/Scala 3/TypeScript style, avoids `+` arithmetic confusion
- **Default implementations** — in trait body, copy-pasted to each impl (Go embedding can't do open recursion)
- **Trait inheritance** — `trait Ord: Eq` requires supertrait
- **Value receivers only** — Arca is immutable, Go compiler optimizes via escape analysis
- **No static trait functions, no associated types** — use type-level `static fun` or regular functions instead

**Proposed syntax (Swift/Rust hybrid):**

```
trait Display {
    fun display(self) -> String
}

trait Ord: Eq {
    fun compare(self, other: Self) -> Order
}

type User {
    User(name: String)
    fun hello() -> String { "Hello!" }
}

impl User: Display {
    fun display(self) -> String { self.name }
}

impl User: Greeter {
    fun greet(self) -> String { self.display() }
}

fun show[T: Display & Eq](x: T) -> String {
    x.display()
}
```

Key choices from design discussion:
- `with` chaining rejected: whitespace separation breaks visual attachment
- Integrated `impl` blocks inside type rejected: `impl` blocks look like scopes but share type scope
- Separated `impl User: Display` chosen: matches Rust/Swift model, LSP can show all impls
- `&` over `+` for multiple bounds: set-theoretic reading, no arithmetic conflict
- `:` for constraint declarations (trait bounds, trait inheritance), different from `with` for extension (but extension style was rejected)

**Why not yet:** Arca is still at the level where simple scripts work. Traits matter when code grows beyond single-file apps. Module system, better error handling, and stdlib richness come first. Discussion recorded so future implementation starts from a known design baseline.

**Status:** Design only, no implementation. Revisit when trait absence blocks real work.

---

## 2026-04-06: Tag-based snapshot and migration system (idea)

**Context:** While building a todo app with sqlite, realized DB schema should be auto-generated from type definitions. Generalized beyond DB: any external system mapped via tags can benefit from snapshots and migration.

**Idea:** Take per-tag snapshots of type definitions. Derive migrations from diffs between snapshots.

**Arca's responsibility (core):**
- Output intermediate representation from type definitions + tags (type names, fields, types, constraints, tag rules)
- Save snapshots (per tag)
- Output diff (delta from previous snapshot)
- Check (verify snapshot is up-to-date — for CI/git hooks)

**Library responsibility (Arca packages):**
- Convert intermediate representation to concrete output (SQL, Elasticsearch mapping, etc.)
- `import arca.db` — db tag → SQL generation
- `import arca.elastic` — elasticsearch tag → mapping generation

**Per-tag strategy:**
- `snapshot`: Migration required (DB, Elasticsearch — data is persisted)
- `latest`: Generate final form only (JSON API — transfer structures, no migration needed)

**CLI sketch:**
```
arca snapshot db          ← save snapshot of db tag
arca diff db              ← output delta from previous snapshot as IR
arca check db             ← verify snapshot is current (for CI)
```

**Relation to git:** Git holds the change history of type definitions. Snapshots are tied to git commits. Arca manages snapshots on top of git.

**Likely first use case for the Arca package system.** Distributed as tag libraries.

**Open design questions:**
- Non-model migrations (data migration, indexes, partitions): handled by library-managed migration files, not arca
- Integrity: hash the IR to detect tampering, or tie to git commit hashes for reproducibility?
- Git-based approach: no separate snapshot files needed, just record last-migrated commit. But force push can rewrite history, and shallow clones lose it
- Arca hash approach: self-contained, but hash list itself can be tampered with
- Either way, CI check is the practical safety net. Decide at implementation time.

**Status:** Just an idea (brainstorming stage).

---

## 2026-04-05: DB migration from types (idea, superseded by tag-based snapshot system)

**Idea:** Generate DDL intermediate representation from Arca type definitions. Diff between previous and current IR produces migration IR. Concrete DB-specific DDL (PostgreSQL, MySQL) generated by external tools from the IR.

Constrained types map naturally to DB constraints (min/max → CHECK, min_length/max_length → VARCHAR(N), pattern → CHECK regex). Tags block could carry DB-specific hints (primary_key, unique, auto_generated). Relational types (foreign keys) via type aliases like `UserId`.

**Open questions:** DB-specific features (indexes, partitions) may not fit in the intermediate representation. Possible escape hatch: DB-specific override files alongside the IR.

**Status:** Superseded by tag-based snapshot system (2026-04-06).

---

## 2026-04-04: JSON serialize/deserialize (idea)

**Context:** Types with `tags { json }` should get automatic JSON support. Constrained types guarantee that deserialized data is valid.

**Design sketch:**
- `user.toJson()` — `User → String` (serialize, auto-generated when json tag present)
- `User.fromJson(str)` — `String → Result[User, error]` (deserialize + validate)
- Deserialization runs constrained type validation — invalid JSON fields produce typed errors
- Go codegen uses `encoding/json` under the hood

**Open questions:**
- Input type: `String` is sufficient for most web cases. `Bytes` may be needed later.
- Method generation: auto-generated when json tag is present? Or explicit opt-in?
- Nested types: `User` has `Email` field — deserialize must validate Email too
- Error reporting: which field failed? Structured error vs string?

**Status:** Just an idea.

---

## 2026-04-04: Record copy/spread (idea)

**Context:** Immutable types need a way to create modified copies. Writing out all fields manually is verbose.

**Options:**
- Kotlin-style `.copy()`: `let user2 = user.copy(name: "Bob")` — auto-generated method on record types
- JS/TS-style spread: `let user2 = { ...user, name: "Bob" }` — new syntax
- List spread already exists (`[0, ..a]`), record spread would be natural extension

**Open questions:**
- Should copy trigger re-validation on constrained fields? (changed field → yes, unchanged → no?)
- Syntax: `.copy(field: value)` vs `{ ...expr, field: value }`
- Return type: `Result[T, error]` if any constrained field is changed?

**Status:** Just an idea.

---

## 2026-04-04: Constrained field auto-construction (future)

**Context:** A type with constrained fields like `type User(email: Email)` requires manual construction of `Email` before `User`. This leads to boilerplate factory functions (`static fun from(...)`).

**Idea:** `User(email: "b@b.com")` could automatically construct `Email` from `String`, chaining validation. If any constrained field fails, the whole constructor returns `Result[User, error]`.

**Open questions:**
- Should pre-validated values (`let e = Email(...)?; User(email: e)`) skip re-validation?
- Multi-level newtypes: `type CorporateEmail = Email{pattern: ".+@corp\\.com"}` — how deep does auto-construction go?
- Type composition (`A & B`, intersection types) may be needed to express combined constraints cleanly.

**Status:** Not implemented. Requires deeper type system design. Current approach: manual factory functions with `static fun`.

---

## 2026-03-31: `.go` accessor idea

**Context:** Arca stdlib should hide Go. But sometimes raw Go access is needed.

**Idea:** `expr.go.Method()` gives raw Go access. Boundary marker like `&`. Self-responsibility zone.

**Key insight:** Since Arca is a transpiler, `.go` can be compiled away at zero runtime cost. The accessor exists for the compiler to know "this crosses the boundary", not for runtime dispatch.

**Status:** Idea only. Not implemented.
