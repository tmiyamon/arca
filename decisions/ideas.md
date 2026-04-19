# Ideas (Not Yet Implemented)

Future features and design sketches. Newest first.

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

1. Generic types with Ref: `List<Ref<T>>`, `Map<K, Ref<V>>` — presumably allowed but unexplored.
2. Error type unification: how are Arca-native errors (e.g., `NotFound`) modeled? Ties to Error trait design.
3. Match patterns on Ref/Option: does `Some(r)` give `Ref<T>` to bind? Pattern deref syntax?
4. Ref as return type semantics: dangling-ness is handled by Go GC, but the type invariant is TBD.

### Prior art and references

- **Rust**: `&T` (non-null), `*const T` (unsafe, nullable), `Option<&T>` (safe nullable reference). Same separation, compiled behind borrow checker.
- **Kotlin / Swift**: "reference type" is the default, nullable via `T?`. Works because class reference ≠ pointer.
- **Zig**: `?T` for optional, `*T` for pointer. Explicit `.?` unwrap.
- **Roc**: platform/app split; app is pure, host is mutable. Analogous to Arca's Builder pattern.

### Status

Idea. Direction is settled. Implementation is partial:

- **Return-position wrapping** (`*T`, `(*T, error)`): implemented. `wrapPointerInOption` produces `IROptionType{IRRefType{T}}` at the FFI return boundary.
- **Param / field / generic-inner wrapping**: pending. These positions still carry raw `IRPointerType` through to Arca-facing IR; a transitional `unify` compat accepts `IRPointerType` and `IRRefType` interchangeably so user-written `Ref[T]` annotations can meet FFI types.
- **Receiver**: handled via method dispatch, which unwraps either `IRPointerType` or `IRRefType`. No separate wrap needed.

Blocks (broader rollout):
- Refactor of remaining `IRPointerType` callers at the param / field / generic-inner paths.
- Monadic stdlib methods (`flatMap`, `map`, `okOr`, etc.) — landed 2026-04-19.
- Synthetic Builder generation is its own multi-phase implementation.
- Existing code migration for `testdata`, `examples/todo`, stdlib signatures.

Removing the transitional `unify` compat requires:
1. Wrap param types with `Option<Ref<...>>` at the Go FFI parameter-lowering path.
2. Same for struct field types.
3. Same for generic inner arguments in Go generic instantiations.
4. Update callers to use `Some(&v)` / `None` (or the nonnull tag override).
5. Drop the compat branches in `unify`.

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
