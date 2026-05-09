# Type System Decisions

Newest first within this topic.

---

## 2026-05-10: Numeric Slice A — 64-bit GOARCH enforcement landed

**Context:** The 2026-05-10 numeric-types decision pins `Int` to `Go int` and requires the host platform to be 64-bit so that `Int` ≡ `Int{bits: 64}` is a true identity. Without enforcement, an Arca program built on GOARCH=386 silently produces a binary whose `Int` is 32-bit, breaking Layer 1 the moment any value crosses the SSOT (`int64` columns, JSON numbers, hash output). Subsequent slices (`bits: N` storage hint, `T(x)?` narrowing, `std.checked.*`) all assume the 64-bit identity, so the seal must land before them.

**Decision:** Two redundant guards.

- Emit injects `//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm` as the first line of every generated Go file (main + per-module). 32-bit targets see the file excluded; `go build` reports "no Go files in ..." rather than producing a Layer 1-violating binary.
- `arca run` and `arca build` call `check64BitTarget()` before invoking the Go toolchain. The check honors `$GOARCH` (cross-compile override) and falls back to `runtime.GOARCH`. Failure prints `arca requires a 64-bit target: GOARCH=<arch> is not supported (Int is fixed to 64 bits)` with exit code 1 — a clearer signal than the toolchain's "no Go files" surface.

The build-tag list and the runtime check share one source of truth (`target_arch.go`: `goBuild64BitTag` constant + `goarch64BitSet` map).

`arca emit` does not precheck — it just prints Go to stdout. The build tag in the printed file is the seal; downstream `go build` will reject the file on a 32-bit target.

**Status:** Landed 2026-05-10. ~100 LoC: `target_arch.go` (new), `emit.go` (one line in `Emit()`), `main.go` (precheck call in `runCmd` / `buildCmd`). All 56 testdata snapshots regenerated to include the build tag; `go test ./...` passes; `go vet` clean. Slice B (`UInt` core type) is the next foundational independent slice.

---

## 2026-05-10: Numeric types — core + constrained type tower

**Context:** B4 (examples/todo migration) hit `sql.Result.LastInsertId()` returning Go `int64` with no Arca representation — Arca modeled `Int` (= Go int) and `Float` (= float64) but nothing else from Go's full numeric tower (`int / int8/16/32/64 / uint / uint8/16/32/64 / float32/64 / byte / rune`). Quick fixes (Int64 primitive / cast syntax / stdlib helper / auto-narrow) sidestepped the policy gap. Full design discussion 2026-05-07 → 2026-05-10 covered concerns: Bytes, cast API, panic policy, arithmetic semantics, BigInt placement.

**Decision:** Three core types `Int / UInt / Float` plus a stdlib/numeric tower expressed as constrained types via `{bits: N}` opt-in storage hint.

- **Core**: `Int = Go int` (64-bit-only enforced via `//go:build` + Arca CLI precheck — refuses GOARCH=386/arm/wasm32/mips/etc.). `UInt = Go uint` added to handle UInt64 representation gap; without it MySQL `BIGINT UNSIGNED`, hash output, and file mode have no representation. `Float = float64`.
- **Tower**: `Int8 = Int{bits: 8}`, `UInt32 = UInt{bits: 32}`, `Float32 = Float{bits: 32}`, etc. — through existing constrained-type machinery, no new primitive category. `bits: N` is one constraint key alongside `min` / `max` / `pattern` / etc., but `bits` alone implies storage = N-bit and value range = N-bit natural; without `bits`, storage stays at base (preserves "Int default = Go int" cultural fit).
- **D2 refined widening (range-aware)** extends to value flow positions (assignment, return, function arg, match arm) and arithmetic operands. When source range ⊆ target range, implicit widen (proven safe, no Result, no panic). Otherwise explicit `T(x)?` (Result-returning narrowing). Cross-base (`Int + UInt`) is a compile error — UInt's max exceeds int64, no safe common base.
- **Cast API**: existing `T(x)?` constructor syntax extends uniformly across struct ctor (`User(id: 1)?`), constrained ctor (`Email("...")?`), and numeric narrowing (`Int8(intval)?`). Internal Go emit retains the `NewT` naming convention (compiler-only, user-invisible). No parallel `T.from(x)` API. Dispatch: source ⊆ T → returns T (no Result); source ⊄ T → returns Result. Bidirectional via type annotation for narrowing.
- **Arithmetic on base types**: panics on overflow per Layer 1 violation detection policy (`decisions/foundations.md` 2026-05-10). Follows Rust 0560 RFC trend (default safe, opt-in unsafe). Silent wrap rejected, supported by production bug evidence: uint underflow leading to huge index, ID exhaustion, JS 2^53 precision loss across API boundaries. `std.checked.{add,sub,mul,div,mod,neg,wrapping*,saturating*}` prelude provides opt-in Result-returning safe arithmetic. Literal range checked statically (`let x: Int8 = 200` produces compile error).
- **`std.bigint.BigInt`**: arbitrary-precision arithmetic, bridges Go's `math/big`. Third numeric layer alongside fast+panic (Int) and explicit Result (std.checked). Heap-allocated and slower; opt-in conversion via `BigInt(x: Int)`. No overflow possible.
- **Bytes**: dedicated type rejected. `List[Byte]` (= Go `[]uint8` = `[]byte`) is sufficient. Binary blob operations (UTF-8 conversion, base64, hash) live as prelude/stdlib functions.

Implementation slices A–G+I detailed in `design_numeric_types.md`. D+F+H bundled (numeric tower + cast + Bindable narrow validation) to preserve Layer 1 consistency — narrow types and Bindable-side narrowing land together so no intermediate state opens a silent overflow path.

**Status:** Designed 2026-05-07, revised 2026-05-10. Memos updated 2026-05-10 (`design_numeric_types.md`, `design_panic_handling.md` 2026-05-10 entry, `design_two_layers.md` 2026-05-10 entry, `project_panic_audit_2026_05_02.md` 2026-05-10 update). Implementation pending (Phase 4: Slices A through I).

---

## 2026-04-04: Variable shadowing in codegen

**Context:** `let email = Email(email)?` inside a function with parameter `email` generated invalid Go — `email := __try_val1` re-declared the parameter in the same scope.

**Decision:** Track declared variable names per function scope. When a let binding shadows an existing variable, codegen generates a suffixed name (`email_2`) and maps subsequent references to the new name.

**Implementation:** `declareVar()` tracks names and returns unique Go names. `varNames` map stores current variable name mapping for Ident resolution. `initFnScope()` registers parameters at function entry.

---

## 2026-04-04: Constrained type constructor returns Result without ?

**Context:** `let email = Email("a@b.c")` with a constrained type generated broken Go code — `NewEmail()` returns `(Email, error)` but codegen assigned it to a single variable.

**Decision:** Constrained type constructors without `?` automatically wrap the result in `Result[T, error]`.

- `Email("a@b.c")?` → propagates error, binds `Email` on success
- `Email("a@b.c")` → returns `Result[Email, error]`, handle with `match`

**Codegen:** Generates temp vars for the `(T, error)` return, then wraps in `Ok_` / `Err_`. Also improved `inferGoType` to resolve `ConstructorCall` types (was falling back to `interface{}`).

---

## 2026-04-04: Associated functions (static fun) and Self

**Context:** Needed type-level functions without `self` (factory constructors like `Greeting.from("Hello")`). Current method system uses implicit `self`, so methods on interface types generated invalid Go (can't have interface receiver).

**Decision: `static fun` keyword + `Self` type reference.**

```arca
type Greeting {
  Hello(name: String)
  static fun from(s: String) -> Greeting {
    match s {
      "Hello" -> Self.Hello(name: "World")
      _ -> Self.Goodbye(name: "World")
    }
  }
}
let g = Greeting.from("Hello")
```

**Why `static fun` over alternatives:**
- Implicit self + body analysis to detect associated functions → caller can't tell from signature alone, confusing
- Explicit `self` parameter (Rust/Python) → would require changing all existing methods, and Arca already has implicit self
- `static fun` (Swift pattern) → one keyword addition, clear at definition site, no existing code changes

**`Self` for type self-reference:** Inside type body (methods and associated functions), `Self` resolves to the enclosing type. Avoids repeating the type name. Follows Rust/Swift convention. Preferred over bare constructors inside type body (which would be unusual — no mainstream language does this).

**Codegen:** `static fun` → Go package-level function (`greetingFrom`). Regular methods → Go methods with receiver.

---

## 2026-04-03: Qualified constructor syntax + arca init

**Context:** Constructors like `Hello(name: "World")` were callable without type qualification, leaking constructor names into global scope. Two types with `Error(message: String)` would collide.

**Decision: Type-qualified constructors for multi-variant types.**

| Form | Syntax | Example |
|------|--------|---------|
| Single-constructor | Unqualified | `User(name: "Alice")` |
| Multi-constructor (sum type) | `Type.Constructor(...)` | `Greeting.Hello(name: "World")` |
| Enum variant | `Type.Variant` | `Color.Red` |
| Type alias | Unqualified | `Email("test@example.com")` |
| Builtins (Ok/Error/Some/None) | Unqualified | `Ok(value)` |
| Match patterns | Always unqualified | `Hello(name) -> ...` |

**Rationale:** Follows Rust/Swift pattern — qualify at construction to avoid name collision, but match patterns are unqualified because the subject's type disambiguates. Single-constructor types are unqualified because type name = constructor name (no ambiguity).

**Builtins (Ok/Error/Some/None):** Unqualified now. Will be explained by a prelude system in the future (auto-imported, like Rust's std::prelude).

---

## 2026-04-03: Constraint compatibility (Level 2)

**Context:** Constrained type aliases need compatibility checking. `AdultAge = Int{min: 18, max: 150}` should be passable where `Age = Int{min: 0, max: 150}` is expected, but not vice versa.

**Design: Dimension-based normalization.**
Constraints are normalized into independent dimensions, each with a unified comparison:

| Kind | Dimensions | Comparison |
|------|-----------|------------|
| Range | Value (min/max), Length (min_length/max_length) | Range inclusion: `A.range ⊆ B.range` |
| Exact | Pattern, Validate | Equality |

**Rules:**
- Source → Target is compatible if source is equal or stricter on all target dimensions
- Target has a dimension source doesn't → source is unbounded → not compatible
- Source has extra dimensions target doesn't → OK (stricter is fine)
- Two unconstrained aliases with different names → nominal, never compatible (UserId ≠ OrderId)

**No structural aliases.** `type X = T` is always a newtype (nominal). Structural aliases have no current use case in Arca. Revisit when function types are added to the type system.

**Codegen:** Type alias parameters always get a Go type conversion (`greet(Age(adult))`). Same-type conversion is no-op in Go.

**Reference:** Ada has the closest feature in a production language (`subtype`). Research/academic: Liquid Haskell, F* (refinement types). Mainstream languages (Rust, Go, Kotlin, TS) don't have this.

**Status:** Implemented but may be removed if practical value doesn't materialize. Main use case is library code accepting wider types with app code using stricter types.

---

## 2026-04-02: Type alias codegen

**Context:** `type Email = String{pattern: ".+@.+"}` was parsed but generated no Go code.

**Decision:** Type aliases generate Go defined types (not Go type aliases):
- `type Email = String{...}` → `type Email string` + `NewEmail(v string) (Email, error)`
- `type UserId = Int` → `type UserId int` (no constructor if no constraints)

Nominal typing: `UserId` and `OrderId` are distinct types even with same constraints.

**Codegen:** `fmt` and `regexp` are auto-imported when constrained type aliases need them.

**OpenAPI:** Type aliases generate standalone schema entries with constraints mapped to JSON Schema.

---

## 2026-04-02: let type annotation

**Context:** `let users = []` generates `[]interface{}{}` in Go — no type info for empty collections. Go FFI functions like `db.Select(&users, ...)` need correctly typed slices.

**Options considered:**
- A) Explicit type annotation: `let users: List[User] = []`
- B) Hindley-Milner type inference (infer from usage context)
- C) Typed empty list literal: `List[User][]`

**Decision:** Option A. Simple, explicit, familiar syntax (Kotlin, TypeScript, Rust all use `let x: T`).

**Codegen rules:**
- Empty list + type annotation → `var users []User` (Go zero value)
- Non-empty value + type annotation → `var users []User = expr`
- No annotation → `users := expr` (Go infers)

**Future:** HM inference (B) may be added later to make annotations optional in more cases.

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

**Decision:** Add methods. Defined inside type body, `self` implicit (not in args). "Types express domains" takes priority over "data/operation separation".

```arca
type User(name: String) {
  fun greet() -> String { "Hello ${self.name}" }
}
```

---

## 2026-03-30: Constrained types — levels and scope

**Context:** Constrained types v1 implemented (construction-time validation). Discussed how deep to take them.

**Levels identified:**
1. Construction validation ✅
2. Constraint compatibility (Age vs AdultAge) ✅
3. Condition narrowing (if age >= 18, treat as AdultAge) — future
4. Go type optimization (Int{0,255} → uint8) — future
5. OpenAPI derivation ✅ / JSON, DB derivation — future (see ideas.md)
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

## 2026-03-29: Builtin names (Ok/Error/Some/None) — shadowing

**Context:** User-defined constructor `Ok` collides with builtin Result's `Ok`.

**Options:** Reserved words, namespaced (`Result.Ok`), shadowing.

**Decision:** Shadowing (same as Rust). Builtins take priority unless user defines same name. Future: warn on shadow.

---

## 2026-03-29: Result/Option as struct not pointer

**Context:** Option was initially `*T` (pointer). Changed to generic struct.

**Reason:** Pointer leaks nil into Go code. Struct keeps nil contained. Safer for Go interop. User explicitly preferred struct approach.

---

## 2026-03-28: Immutability — fully immutable, Go types opaque

**Context:** Arca is immutable, but Go types are mutable.

**Decision:** Arca-defined types are fully immutable (language-guaranteed). Go types are opaque — Arca doesn't guarantee their immutability. Developers know Go's semantics. No wrapper types needed.
