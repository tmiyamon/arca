# Arca Design Decision Log

Design discussions and their reasoning. Newest first.

---

## 2026-04-04: IR (intermediate representation) introduction

**Context:** Current architecture is AST → Go codegen directly. Every new feature (constrained types, shadowing, Self, qualified constructors, etc.) requires handling in multiple codegen paths (let statements, expressions, match arms, function returns). Features leak across concerns, causing missed cases and bugs.

**Problem:** Bottom-up feature addition without structural guarantees. Each codegen path must independently check "is this a constrained constructor?", "is this shadowed?", "is this Self?". No mechanism to ensure all cases are covered.

**Decision: Introduce IR between AST and Go output.**

```
Source → AST → IR (normalized) → Go output
```

**IR responsibilities (each as a separate pass):**
1. **Name resolution** — resolve `Self` to concrete type, resolve module-qualified names, variable shadowing (rename to unique Go-safe names)
2. **Constructor resolution** — qualified constructors (`Greeting.Hello`) resolved to concrete Go struct names, constrained constructors wrapped in Result
3. **Error propagation** — `?` operator expanded to try/error pattern
4. **Type resolution** — all expressions annotated with resolved types (needed for codegen type args, and future LSP)

**Go output becomes simple:** Walk IR and emit Go. No special cases, no feature-specific branching. Each IR node directly maps to a Go construct.

**Benefits:**
- Missing cases become structurally visible (if IR doesn't handle it, it fails clearly)
- LSP gets type information for free from the IR
- New features are added as IR passes, not scattered across codegen
- Testing: each pass can be tested independently

**Why now:** Codebase is still small (~1500 lines of codegen). Delaying increases migration cost as more features are added.

**IR node design (initial):**
- Should be close to Go's structure (statements, expressions, declarations) but with Arca's semantics resolved
- Carries type information on every expression node
- All names are Go-safe (no further renaming needed in output)

---

## 2026-04-04: IR implementation (AST → IR → Go)

**Context:** Decided to introduce IR between AST and Go output to structurally prevent missed cases and separate concerns (see earlier decision entry).

**Implementation:** Three new files:
- `ir.go` — IR node definitions. Match expressions are structurally exhaustive (e.g. `IRResultMatch` requires both `OkArm` and `ErrorArm`). All expressions carry resolved types. All names are Go-safe.
- `lower.go` — AST → IR conversion. Resolves names (Self, self, shadowing, builtins, constructors), classifies match expressions, tracks imports.
- `emit.go` — IR → Go string output. Mechanical walk of IR nodes, no feature-specific branching.

**Key design: value-before-declare for shadowing.** In `let email = Email(email)?`, the RHS must be lowered before declaring the new `email` variable, otherwise the parameter reference gets incorrectly mapped to the shadowed name.

**Verification:** `arca emit-ir` command added for parallel comparison. All testdata files produce semantically identical output to the existing `arca emit`. Performance is equivalent (sub-millisecond).

**Status:** Complete. All commands use the IR pipeline. Old codegen.go, codegen_match.go, codegen_builtins.go removed. Shared helpers moved to helpers.go.

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
- Phase 1: Load packages, validate function call argument count, resolve return types
- Phase 2: Validate argument types (need Arca type ↔ Go type mapping)
- Phase 3: Method resolution on Go types (`w.Header().Set(...)`)
- Phase 4: Struct field access validation

**Why now:** IR is in place. Type info goes into IR nodes during lowering. Emit doesn't need to change. Without IR, this would have required threading type info through the string-building codegen — impractical.

**Dependency:** Adds `golang.org/x/tools/go/packages` to go.mod.

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

## 2026-04-04: Constrained field auto-construction (future)

**Context:** A type with constrained fields like `type User(email: Email)` requires manual construction of `Email` before `User`. This leads to boilerplate factory functions (`static fun from(...)`).

**Idea:** `User(email: "b@b.com")` could automatically construct `Email` from `String`, chaining validation. If any constrained field fails, the whole constructor returns `Result[User, error]`.

**Open questions:**
- Should pre-validated values (`let e = Email(...)?; User(email: e)`) skip re-validation?
- Multi-level newtypes: `type CorporateEmail = Email{pattern: ".+@corp\\.com"}` — how deep does auto-construction go?
- Type composition (`A & B`, intersection types) may be needed to express combined constraints cleanly.

**Status:** Not implemented. Requires deeper type system design. Current approach: manual factory functions with `static fun`.

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

## 2026-04-04: Builtin println/print

**Context:** `import go "fmt"` + `fmt.Println(...)` was required for Hello World. First impression of the language forces Go FFI knowledge.

**Decision:** `println` and `print` as builtin functions, available without import. Maps to `fmt.Println` / `fmt.Print`, with `fmt` auto-imported in generated Go.

**Implementation:** Codegen refactored to body-first generation. Body is generated into a buffer first, then imports are prepended based on what was actually used. This eliminated the `preScan` AST walk (~100 lines) that was needed when imports were emitted before body generation. Adding new builtins now requires a single change in `genExprStr`.

**Future:** These builtins will move to a prelude module when one is implemented.

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

**Also added: `arca init <name>`** — creates a new project directory with a `main.arca` template showcasing ADT + pattern matching + string interpolation. Enables `arca init myapp && cd myapp && arca run` onboarding flow.

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

## 2026-04-02: struct tags — implemented as tags block

**Requirements:**
1. Arbitrary tag keys (json, db, yaml, elastic, custom...)
2. Auto-generation from field names with conversion rules
3. Override only exceptions
4. Natural Arca syntax

**Key insight:** struct tag is "mapping to external system's world". Constrained types are "domain value constraints". Different concerns, must be separate.

**Approaches explored and rejected:**
- Mix in `{}` — leaks via type alias, mixes concerns
- Separate types per layer (UserRow, UserResponse) — clean but verbose
- `#annotation` / `@annotation` on fields — no auto-generation rules
- Config file (arca.toml) — too far from type definition
- `layer` blocks — over-designed for what it does

**Decision:** `tags` block with `()` for global rules, `{}` for overrides.

```arca
type User(id: Int, userName: String) {
  tags {
    json,
    db(snake),
    elastic { userName: "full_name" }
  }
}
```

Rules:
- `tag_name` → all fields, field name as-is
- `tag_name(snake)` → all fields, case conversion
- `tag_name { field: "value" }` → only specified fields
- Comma-separated OK: `tags { json, db(snake) }`

**Transitional:** stdlib will eventually hide struct tags. This is the bridge to Go's ecosystem.

---

## 2026-03-31: 1:1 file mapping and visibility

**Context:** Previously all same-directory .arca files merged into one .go file. Changed to 1 .arca = 1 .go.

**Decision:** Each .arca generates its own .go file. Same-directory files share `package main`. Sub-directory modules get separate Go package.

**pub = package-level visibility (not file-level).** Same as Go. Same directory can access non-pub functions. If file-level privacy needed, move to separate directory.

**Why:** Go compiler handles same-package type resolution across files. Simpler than merging. Easier to debug (1:1 source mapping).

---

## 2026-03-31: Package and import system

**Context:** Needed package system for real projects. Investigated Go, Rust, Kotlin, Python, TS approaches.

**Decisions:**
- **Package unit:** Directory = package. Package name = directory name (implicit, no `package` declaration needed)
- **Circular imports:** Forbidden (Go constraint from transpilation)
- **No wildcard imports:** Explicit is better. Go, Gleam, Elm also have no wildcard.
- **Versioning:** No version-in-path (Go's `/v2` is universally disliked)

**Import syntax (Kotlin/Rust inspired):**
```arca
import user                    // user.find() — qualified
import user.{find, create}     // find(), create() — selective
import user as u               // u.find() — alias
import go "fmt"                // Go packages
import go _ "modernc.org/sqlite" // side-effect
```

**Why not Go style:** Go's "last segment = package name" is implicit and confusing (documented complaints). Arca requires explicit qualification or selective import.

**Why not Python style:** `from x import *` causes namespace pollution. Arca forbids it.

**Design principle:** Generated Go should be valid, idiomatic Go. Arca abstractions (alias, namespaces) are resolved before codegen, not leaked into Go output.

**Implementation notes:**
- Alias expansion happens before import resolution (main file only). `u.find()` → `user.find()` at AST level.
- Module-qualified calls (`user.find()`) resolved to flat calls (`find()`) in codegen since all modules merge into one Go file.
- FieldAccess module resolution only happens inside FnCall context, preventing collision with variable names (e.g. parameter `u` vs alias `u`).
- Types are always imported regardless of selective import (needed for type checking).

---

## 2026-03-31: Entry point resolution

**Context:** Needed `arca build` to work without specifying file every time.

**Decision:** Three patterns:
- `arca build` → finds `main.arca` in current directory
- `arca build cmd/server` → finds `main.arca` in directory
- `arca build main.arca` → direct file (backwards compat)

Follows Go convention. Package system deferred — currently 1 file = 1 module, directory is just structure.

---

## 2026-03-31: `.go` accessor idea

**Context:** Arca stdlib should hide Go. But sometimes raw Go access is needed.

**Idea:** `expr.go.Method()` gives raw Go access. Boundary marker like `&`. Self-responsibility zone.

**Key insight:** Since Arca is a transpiler, `.go` can be compiled away at zero runtime cost. The accessor exists for the compiler to know "this crosses the boundary", not for runtime dispatch.

**Status:** Idea only. Not implemented.

---

## 2026-03-31: struct tags — rethinking (superseded by 2026-04-02)

**Context:** Realized json/db metadata belongs to fields, not types. `type ProductId = Int{min: 1, json: "id"}` breaks when reused — json key leaks to other fields.

**Current state at this point:** Mixed in `{}`. Works but conceptually wrong.

**Options considered:**
- Separate types per layer (User, UserRow, UserResponse) + spread syntax for conversion
- Trait to mark types as JsonModel/DbModel
- Auto-generate from field names (camelCase → json, snake_case → db)
- `.go` accessor to drop to raw Go when needed

**Decision at this point:** Unresolved. Led to further discussion and eventually the tags block solution.

---

## 2026-03-30: Pipe operator — keeping it

**Context:** Methods were added. Considered dropping pipe. But Go generics can't add new type parameters to methods (`func (l List[T]) Map[U](...) List[U]` is illegal). Collection operations (map/filter/fold) can't be method chains.

**Decision:** Keep both. Clear split:
- **Methods** — type domain operations (`user.toJson()`)
- **Pipe** — collection operations (`users |> map(...) |> filter(...)`)

Not ideal (two styles) but technically necessary.

---

## 2026-03-30: struct tags from constrained types (superseded by 2026-04-02)

**Context:** gin + sqlx need `json:"name" db:"name"` struct tags. Where to put this metadata?

**Options:** Annotations (@json), separate tags block, auto-generate from field names, mix into constrained types `{}`.

**Decision at this point:** Mix into `{}`. String-valued keys become struct tags, numeric-valued keys become validation constraints. Single source of truth. `Int{min: 1, json: "id", db: "id"}`.

---

## 2026-03-30: Short record syntax

**Context:** `type User { User(name: String) }` is redundant for single-constructor types.

**Decision:** `type User(name: String)` as shorthand. Equivalent AST. Formatter outputs short form when no methods.

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

## 2026-03-30: defer — decided not to add, then added

**Context:** Needed for resource cleanup. Explored alternatives.

**Options considered:**
- Go's `defer` — works but "breaks the flow" of reading top-to-bottom
- RAII/Drop (Rust) — ideal but needs type info about which Go types need cleanup
- `use` keyword — still needs to know what method to call
- `with` block (Python/Kotlin) — control structure, not type-oriented
- Wrapper functions (Scala's `Using`) — hides defer in library

**Initial decision:** Don't add defer. Use Go-side wrapper functions.

**Later reversed:** Added `defer` as-is from Go. Pragmatic. `defer db.Close()` is needed and Go developers understand it.

---

## 2026-03-29: Pipe operator vs methods (superseded by 2026-03-30 "keeping it")

**Context:** Go FFI returns objects with methods. Arca has pipe `|>` but no methods.

**Analysis:**
- Languages with pipe (Gleam, Elm, Elixir) don't have methods
- Languages with methods (Rust, Go, Kotlin) don't have pipe
- Both solve the same problem (function chaining)
- F# has both (because .NET) — results in mixed style
- Arca on Go = same situation as F# on .NET

**Decision at this point:** Keep pipe for now. Will likely drop when methods are added. Later discovered pipe is permanently needed due to Go generics constraint on collection methods.

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

## 2026-03-28: Import syntax — dot separator (superseded by 2026-03-30 string literals)

**Context:** SPEC had `go/fmt`, implementation had `go.fmt`.

**Analysis:** `/` is Go convention but most languages use `.` for imports (Java, Kotlin, Scala, Python, C#). Mixing `.` and `/` for Arca vs Go modules is inconsistent.

**Decision at this point:** All dots. `import go.fmt`, `import go.database.sql`, `import user`.

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
- `fn` / `type` / `match` (later changed: fn→fun, snake_case→camelCase)
- snake_case in Arca, camelCase in generated Go (later unified to camelCase)

---

## 2026-03-28: Why Arca exists

**Problem:** PHP/Laravel project (63K lines, 75 models, Cloud Run) needs better language.

**Evaluated:** Kotlin (not strict enough), Gleam (syntax deviates from conventions), Rust (borrow checker overkill, slow compile), Scala (DX bad), OCaml (no web ecosystem), Go (no ADT/Result).

**Solution:** New language. ML type safety + Go ecosystem. Transpiles to Go for single binary, fast startup, full Go library access.

**Existing alternatives found:** Borgo (dead), Soppo (immature), Dingo (Go-syntax-bound, limits expression).

**Key user values:** Correctness, expressiveness, familiar conventions, Go's pragmatic benefits.
