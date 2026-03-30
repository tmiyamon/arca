# Arca Design Decision Log

Design discussions and their reasoning. Newest first.

---

## 2026-03-30: `&` operator for Go FFI

**Context:** Go libraries require `&T` for mutation (db.Get, json.Unmarshal, rows.Scan). Arca is immutable.

**Options:** `&expr` (Go syntax), `ref(expr)` (function), auto-detect (needs Go type info).

**Decision:** `&expr`. Acts as boundary marker — immutability guarantee ends here. Same as Rust's `unsafe` in spirit. All immutable languages (Haskell, OCaml, Gleam) allow FFI mutation.

---

## 2026-03-30: Unit type

**Context:** Go functions returning `error` only (no value) need Result wrapping. `Result[???, error]`.

**Decision:** `Unit` type. `Result[Unit, error]` for error-only functions. `Ok(Unit)` for success. Go generates `struct{}`.

---

## 2026-03-30: `let _` for discarding values

**Context:** Go FFI calls return values that Arca doesn't need. Go rejects unused variables.

**Decision:** `let _ = expr` discards value. `let _ = expr?` discards success value but propagates error. Explicit discard — don't allow implicit.

---

## 2026-03-30: Import redesign — string literals

**Context:** `import go.modernc.org.sqlite` broke because dot-to-slash conversion mangled domain names. TLD hacks were fragile.

**Decision:** `import go "path"` with string literal. Go package paths passed through verbatim. Side-effect: `import go _ "pkg"`. No conversion, no bugs.

---

## 2026-03-30: Function keyword — `fun`

**Context:** Was `fn` (Rust style). Changed to camelCase, making `fn` + camelCase an uncommon combination (only Zig).

**Analysis:** Arca has methods, constrained types, type-driven design — closer to Kotlin/Swift than Rust/Gleam. `fn` is function-oriented/FP culture. `fun`/`func` is method-oriented/OOP-lean culture.

**Options:** `fn` (Rust/Zig), `func` (Go/Swift), `fun` (Kotlin/Koka/Mint)

**Decision:** `fun`. Short, modern, matches Kotlin (closest in philosophy). Go's `func` was considered but `fun` is 1 char shorter.

---

## 2026-03-30: Naming convention — camelCase

**Context:** Arca was using snake_case, requiring snake→camelCase conversion in codegen for Go output.

**Arguments for camelCase:**
- Go uses camelCase — no conversion needed
- Kotlin/Java analogy: same runtime, same naming convention
- Go FFI calls (`r.URL.Query().Get("id")`) already camelCase — mixing with snake_case looks inconsistent
- Less codegen complexity, fewer bugs

**Arguments for snake_case:**
- Rust/Gleam/Python convention
- Functional/ML language culture

**Decision:** camelCase. Arca sits on Go's world, should match Go's conventions. Like Kotlin matches Java.

---

## 2026-03-30: Methods — decided to add

**Context:** Constrained types were implemented, and we found that domain operations on constrained types (e.g. `Age.increment()`) need to be closed within the type.

**Arguments for methods:**
- Constrained types need per-type operations that respect constraints
- Go FFI already returns objects with methods — inconsistent to not have methods in Arca
- Name collision: `incrementAge` vs `incrementScore` vs `fun Age.increment(self)`
- IDE discoverability: `age.` shows available operations

**Arguments against:**
- Data/operation separation is a clean FP principle
- Adds complexity to the language
- Triggers trait/interface discussions

**Decision:** Add methods. Syntax: `fun Type.method(self) -> RetType`. "Types express domains" takes priority over "data/operation separation".

---

## 2026-03-30: Constrained types — levels and scope

**Context:** Constrained types v1 implemented (construction-time validation). Discussed how deep to take them.

**Levels identified:**
1. Construction validation ✅
2. Constraint compatibility (Age vs AdultAge) — planned
3. Condition narrowing (if age >= 18, treat as AdultAge) — future
4. Go type optimization (Int{0,255} → uint8) — future
5. JSON/OpenAPI/DB derivation — planned
6. Arithmetic propagation (SMT-solver level) — far future
7. Structural constraints (NonEmptyList) — future

**Key insight:** Immutability makes construction-time validation sufficient for permanent guarantees. No other mainstream language has this combination at language level.

**Decision:** Opt-in constraints. Not forced on all types. Default is unconstrained. Strict where you want it.

---

## 2026-03-30: Constrained types — syntax `{}`

**Context:** Needed syntax for type constraints. Considered `[]`, `{}`, `()`, `where`, `<>`, custom delimiters.

**`[]` rejected:** Collides with type parameters. `List[Int][max_length: 10]` is ugly.
**`()` rejected:** Collides with constructor calls.
**`where` rejected:** Reads as a condition, not part of the type.
**`<>` considered:** Possible but not standard.

**Decision:** `{}` — `Int{min: 1}`, `String{max_length: 100}`. Distinguishable from blocks by context (key: value inside).

---

## 2026-03-30: Constrained types — design decisions

- **Two layers:** Built-in constraints (OpenAPI-convertible: min, max, pattern, etc.) + custom validate function (runtime only)
- **Reuse via type alias:** `type Email = String{pattern: ".+@.+"}`. No separate `constraint` keyword.
- **No re-constraining aliases:** `Email{min_length: 5}` is an error.
- **No cross-field constraints:** Use constructor functions manually.
- **Constructor returns Result:** Only when constraints exist. `User(id: 1, name: "Alice")?`

---

## 2026-03-30: Static vs runtime constraints

**Context:** Explored whether timeout/cancellation (context.Context) could be unified with constrained types.

**Analysis:** Constrained types validate at construction (one-time). Context monitors continuously during execution. Different nature — static vs runtime.

**Decision:** Arca guarantees values (static). Go guarantees execution (runtime). Clear boundary. Don't abstract Go's runtime primitives.

---

## 2026-03-30: defer — decided not to add (for now)

**Context:** Needed for resource cleanup. Explored alternatives.

**Options considered:**
- Go's `defer` — works but "breaks the flow" of reading top-to-bottom
- RAII/Drop (Rust) — ideal but needs type info about which Go types need cleanup
- `use` keyword — still needs to know what method to call
- `with` block (Python/Kotlin) — control structure, not type-oriented
- Wrapper functions (Scala's `Using`) — hides defer in library

**Key insight:** "What method to defer" varies per type (Close, Cancel, Unlock, Rollback). Can't automate without trait/interface. Wrapper functions can absorb defer on Go side.

**Decision:** Don't add defer to Arca yet. Use Go-side wrapper functions. Revisit when trait/interface is added.

---

## 2026-03-29: Pipe operator vs methods

**Context:** Go FFI returns objects with methods. Arca has pipe `|>` but no methods.

**Analysis:**
- Languages with pipe (Gleam, Elm, Elixir) don't have methods
- Languages with methods (Rust, Go, Kotlin) don't have pipe
- Both solve the same problem (function chaining)
- F# has both (because .NET) — results in mixed style
- Arca on Go = same situation as F# on .NET

**Decision:** Keep pipe for now. Will likely drop when methods are added. Redundancy is acceptable during transition.

---

## 2026-03-29: UFCS (Uniform Function Call Syntax) — rejected

**Context:** Considered D/Nim's UFCS as alternative to methods. `user.toJson()` = `toJson(user)`.

**Pros:** No method concept needed, data/operation separation preserved.
**Cons:** "Is this a method or UFCS?" confusion. Mixed reception in D/Nim community. Doesn't solve name collision without overloading.

**Decision:** Rejected. Prefer explicit methods over implicit UFCS.

---

## 2026-03-29: interface/trait — deferred

**Context:** Go interfaces require methods. Arca has no methods.

**Analysis:**
- trait needed for: Go interface satisfaction, ad-hoc polymorphism, resource cleanup abstraction
- trait NOT needed for: most Arca code (ADT + pattern match suffices)
- Languages without trait (Gleam, Elm, C) work fine with ADT + pattern match
- Go FFI interface satisfaction can be worked around (http.HandleFunc instead of http.Handler)

**Decision:** Deferred. Revisit when io.Reader/io.Writer becomes blocking.

---

## 2026-03-29: Builtin names (Ok/Error/Some/None) — shadowing

**Context:** User-defined constructor `Ok` collides with builtin Result's `Ok`.

**Options:** Reserved words, namespaced (`Result.Ok`), shadowing.

**Decision:** Shadowing (same as Rust). Builtins take priority unless user defines same name. Future: warn on shadow.

---

## 2026-03-29: Result/Option as struct not pointer

**Context:** Option was initially `*T` (pointer). Changed to generic struct.

**Reason:** Pointer leaks nil into Go code. Struct keeps nil contained. Safer for Go interop. User explicitly preferred struct approach.

---

## 2026-03-28: Import syntax — dot separator

**Context:** SPEC had `go/fmt`, implementation had `go.fmt`.

**Analysis:** `/` is Go convention but most languages use `.` for imports (Java, Kotlin, Scala, Python, C#). Mixing `.` and `/` for Arca vs Go modules is inconsistent.

**Decision:** All dots. `import go.fmt`, `import go.database.sql`, `import user`.

---

## 2026-03-28: Go FFI — opaque, no type checking

**Context:** Should Arca type-check Go FFI calls?

**Decision:** No. Go compiler catches these. Arca skips type checking for qualified names (contains `.`). Avoids needing Go package type information.

---

## 2026-03-28: Immutability — fully immutable, Go types opaque

**Context:** Arca is immutable, but Go types are mutable.

**Decision:** Arca-defined types are fully immutable (language-guaranteed). Go types are opaque — Arca doesn't guarantee their immutability. Developers know Go's semantics. No wrapper types needed.

---

## 2026-03-28: Language spec — initial decisions

Documented in one session:
- Type parameters: `[]`
- Error propagation: `?` (provisional)
- Lambda: `(x) => expr`
- Variables: `let` only, immutable
- Pipe: `|>` first argument
- String interpolation: `"Hello ${name}"`
- Newline-based statements
- `pub` visibility
- 1 file = 1 module
- `fun` / `type` / `match`
- camelCase in Arca, camelCase in generated Go

---

## 2026-03-28: Why Arca exists

**Problem:** PHP/Laravel project (63K lines, 75 models, Cloud Run) needs better language.

**Evaluated:** Kotlin (not strict enough), Gleam (syntax deviates from conventions), Rust (borrow checker overkill, slow compile), Scala (DX bad), OCaml (no web ecosystem), Go (no ADT/Result).

**Solution:** New language. ML type safety + Go ecosystem. Transpiles to Go for single binary, fast startup, full Go library access.

**Existing alternatives found:** Borgo (dead), Soppo (immature), Dingo (Go-syntax-bound, limits expression).

**Key user values:** Correctness, expressiveness, familiar conventions, Go's pragmatic benefits.
