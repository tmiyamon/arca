# Type System Decisions

Newest first within this topic.

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
