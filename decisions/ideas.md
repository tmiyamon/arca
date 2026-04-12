# Ideas (Not Yet Implemented)

Future features and design sketches. Newest first.

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
