# Arca Design Decisions

**Goal: be the most practical language for backend development.**

This is the compass for every design decision. "Practical" is the judgment axis — not safety alone, not expressiveness alone, not deployment ergonomics alone, but the combination that lets a backend team ship correct, maintainable services with minimal friction.

Two properties follow from the goal, not the other way around:

- **Layer 1 — safety as prerequisite.** To stand on top of Go at all, Arca seals Go's root dangers (nil panic, typed nil, panic propagation, `interface{}` traps, zero-value surprise, unintended mutation). This is table stakes for any language that claims to improve on Go; it is not the differentiation.
- **Layer 2 — Single Source of Truth as derived property.** Type definition + constrained types + backend focus yield validation, serialization, schema, domain logic as derivations of a single model. SSOT is a consequence of the design; it is not the stated goal.

**Positioning.** "Practical" alone is not a pitch — every language claims it. Arca's distinctive position is the combination no competitor offers simultaneously: Go's operational characteristics (cold start, single binary, compile speed, ecosystem) + ML-level type expressiveness (ADT, HM inference, constrained types) + SSOT as byproduct + Kotlin-level learning curve. Competitors compromise on at least one axis; Arca targets the intersection.

**FFI design principle (derived from the goal).** *Arca makes guarantees. FFI is only allowed to the extent that guarantees can be preserved across it.* This single rule mechanically decides Layer 1 questions: Go `*T` surfaces as `Option[T]`, Go panic becomes `Result`, `interface{}` requires safe cast, Go mutation is absorbed and released via Builder/Freeze. FFI has distinct boundaries for distinct dangers; no single universal wrapping mechanism is needed. See `decisions/ffi.md` 2026-04-15.

This document records language design decisions, their rationale, and trade-offs.

## Naming Conventions

- **Arca source**: `camelCase` for functions, variables, fields
- **Generated Go**: `camelCase` (private), `PascalCase` (public)
- **Types**: `PascalCase` in both Arca and Go
- **Constructors**: `PascalCase` in Arca

## Arrow Convention

Scala-style separation: `->` for types, `=>` for values.

- `->` — function return type: `fun f(x: Int) -> String`
- `=>` — match arm: `Ok(v) => v * 2`
- `=>` — lambda body: `x => x + 1`, `(x: Int) -> Int => x + 1`

## Constructor Qualification

Constructors require type qualification depending on the type definition:

- **Single-constructor types**: Unqualified. `User(name: "Alice")` — type name = constructor name, no ambiguity.
- **Multi-constructor types (sum types)**: Qualified. `Greeting.Hello(name: "World")` — prevents name collision across types.
- **Enum variants**: Qualified. `Color.Red` — same reason as sum types.
- **Type aliases**: Unqualified. `Email("test@example.com")` — single constructor.
- **Builtins (Ok/Error/Some/None)**: Unqualified — will be explained by prelude in the future.
- **Match patterns**: Always unqualified — subject type determines which constructors are valid.

Design rationale: Two types with `Error(message: String)` would collide without qualification. Single-constructor types have no ambiguity because the type name itself is the constructor. Follows Rust/Swift pattern (qualified construction, unqualified match).

## Builtin Names

`Ok`, `Error`, `Some`, `None` are builtin constructors for Result and Option types.

- They are **not reserved words** — user-defined constructors with the same name are allowed
- User-defined constructors **shadow** builtins (same as Rust's prelude)
- Convention: avoid naming user constructors `Ok`/`Error`/`Some`/`None`
- Future: consider emitting a warning when shadowing occurs

## Immutability

- Arca-defined types are **fully immutable**
- `let` is the only binding form, no `let mut`
- Optional type annotation: `let x: Type = expr`
- Needed for empty collections (`let users: List[User] = []`) where type can't be inferred
- Codegen: `var x Type` for empty lists, `var x Type = expr` otherwise
- Go types from FFI are **opaque** — Arca does not guarantee their immutability
- Go developers are expected to understand Go's mutation semantics at the FFI boundary

## Strings

- `"..."` — single-line, escape sequences (`\n`, `\t`), interpolation `${expr}`
- `"""..."""` — multiline (triple-quoted), raw (no escape processing), with `${expr}` interpolation
- Common leading whitespace stripped based on closing `"""` indentation
- Emit: multiline → Go backtick, falls back to double-quote if content contains backtick
- Interpolation → `fmt.Sprintf` with `%v` placeholders
- Single-line `"..."` rejects literal newlines (lexer error with `"""` suggestion)

## Option Type

- Emits as a single Go `*T` pointer uniformly: `Option[T]` → `*T`, nil = None.
- Collapse rule: when the inner is `Ref[U]` or `Ptr[U]` (both already emit as `*U`), outer Option and inner share the same Go type — `Some(ref)` emits as the ref itself, no extra pointer layer. Non-collapsible inners (value types, nested Option, List, ...) wrap via the `__ptrOf` helper: `Some(10)` → `__ptrOf(10)` (Go disallows `&10` on non-addressable values).
- `None` emits as a typed nil: `(*T)(nil)`.
- FFI boundary: Go `(T, bool)` returns are wrapped via `__optFrom(call())` → `*T` so Go's multi-return shape converts cleanly to Arca's uniform pointer-backed Option.
- Pattern matching: always `if opt != nil { ... } else { ... }`. The `Some(v)` binding passes the pointer through unchanged when inner is `Ref[U]` / `Ptr[U]`, else dereferences once: `v := *opt` for `Option[Int]`, `v := opt` for `Option[Ref[User]]`.
- No split machinery for Option — no `SplitNames` / `ExpandedValues` / `flattenArgs` entries. The post-pass handles Result only; Option flows through single-value paths uniformly.
- Monadic methods (`.map`, `.flatMap`, `.okOr`, `.okOrElse`) desugar to `match` at the AST level — no new IR nodes.

## Result Type

- Emits as native Go `(T, error)` multi-return. No wrapper struct, no `IsOk` / `Value` / `Err` fields.
- `Ok(x)` and `Error(e)` populate `ExpandedValues` during the expand pass so emit writes `return x, nil` / `return zero, e` at leaf positions.
- `?` operator on `Result[T, E]`:
  - Captures multi-return into temp split vars (`__val1, __err1`).
  - `if __err1 != nil { return ..., __err1 }` at the caller's return shape.
  - Otherwise binds the value.
  - Unwraps exactly one layer. To convert an inner `Option`, use `.okOr(err)?` or a monadic pipeline.
- Pattern matching: `if result_err == nil { ... } else { ... }` over the split pair.
- Monadic methods (`.map`, `.flatMap`, `.mapError`) desugar to `match` at the AST level.

## Type Checking

- **HM inference**: Type variables (`IRTypeVar`) + unification for forward type resolution. `Ok(42)`, `None`, `[]` use type variables resolved from later usage (function call args unified with parameter types). Resolution pass after function body lowering patches IR nodes.
- **Bidirectional hints**: top-down `lowerExprHint(expr, hint)` for function args, let annotations, return types, match arms, constructor fields
- **Lambda inference**: parameter types from Go FFI call context and prelude functions (map/filter/fold infer from list element type). Return type inferred from body.
- **Match type inference**: all arm body types unified to determine match expression type
- **Binary expression types**: arithmetic from operands, comparison/logical to bool
- **Constraint compatibility**: `AdultAge → Age` checked as a last-ditch success path inside `Lowerer.unify` (after HM structural unification)
- **Structural checks** (in lower): type existence, arg/field count, match exhaustiveness. No separate validation pass.

## Go FFI

- **Import syntax**: `import go.fmt` (dot separator, unified with Arca modules)
- **Type checking**: Go FFI argument count/types validated via `TypeResolver` (go/types)
- **TypeResolver**: Uses project's `go.mod` for package resolution. `packages.Config.Dir` set to nearest go.mod directory (walked up from .arca file). `CanLoadPackage` checks go.mod `require` entries directly (not module cache). Missing packages produce error with `go get` suggestion.
- **GoPackage struct**: Centralizes Go import path parsing. Handles version suffixes (`github.com/labstack/echo/v5` → ShortName `"echo"`). Used in import registration, type name resolution, and receiver type resolution.
- **Qualified types**: `http.ResponseWriter`, `*http.Request` are passed through as-is
- **Return type mapping** (mechanical, in `goFuncReturnType`):
  - `(T, error)` → `Result[T, error]` (GoMultiReturn)
  - `(*T, error)` → `Result[Option[Ref[T]], error]` (GoMultiReturn)
  - `(T, bool)` → `Option[T]` (GoMultiReturn)
  - `(error)` → `Result[Unit, error]` (GoMultiReturn)
  - `*T` → `Option[Ref[T]]`
  - `(T)` → `T`
  - `(T1, T2, ...)` → Tuple (GoMultiReturn)
- **GoMultiReturn flag**: `IRFnCall`, `IRMethodCall`, `IRConstructorCall` carry this flag. Emitter generates multi-value receive + wrapping. Consumption sites (let, try, match) read IR types — no ad-hoc detection. For `(T, bool)` → `Option[T]`, emit wraps the raw call with `__optFrom(...)` to convert Go's multi-return shape into Arca's pointer-backed Option.
- **Pointer auto-wrap**: `wrapPointerInOption` recursively walks Go-sourced IR types and converts every `IRPointerType` leaf into `IROptionType{IRRefType{...}}`. Applied at return positions today; param / field / generic-inner positions are deferred behind a transitional `unify` compat (see `decisions/ideas.md` 2026-04-19).
- **Ref vs Ptr**: `IRRefType` is Arca's user-facing safe reference (`Ref[T]`), `IRPointerType` is the FFI-internal raw pointer that only exists transiently before being wrapped. Users write `Ref[T]`; `*T` Arca syntax is legacy (remaining in some test sources, smoothed over by the transitional unify compat).
- **Project structure**: `go.mod` at project root, managed by user with `go get`. `build/go.mod` copied from parent.

## Pattern Matching

- **Exhaustive**: compiler enforces all constructors are covered
- **Wildcard**: `_` catches remaining cases
- **Priority order in codegen**:
  1. Result patterns (`Ok`/`Error`) → `if subject_err == nil`
  2. List patterns (`[]`/`[first, ..rest]`) → `if len(...) == 0`
  3. Option patterns (`Some`/`None`) → `if subject != nil` uniformly (Option is always pointer-backed). Binding pass-through when inner is `Ref`/`Ptr`, else deref once.
  4. Enum patterns → `switch` on iota
  5. Sum type patterns → `switch v := x.(type)`
- Patterns preserve the subject's inner type: `Some(v)` on `Option[Ref[User]]` binds `v: Ref[User]`, not `User`. Auto-deref (field/method) keeps access ergonomic.
- Match-as-value with Result/Option-producing arms (`let x = match r { Ok(v) => Ok(f(v)); Error(e) => Error(e) }`): emit declares split vars up front and walks the body with a multi-assign mode; each leaf's `ExpandedValues` populate both names.

## Type Parameters (Generics)

- Syntax: `type Pair[A, B] { ... }`
- Struct types: fully supported with Go generics
- Sum types (interface-based): **not yet supported** with generics due to Go's interface + generics limitations
- Type parameters are checked by name against TypeDecl.Params, not by single-letter convention

## Module System

- 1 file = 1 module
- `import user` resolves to `user.arca` in the same directory
- Currently all modules are **inlined into a single Go file**
- Multi-file Go output planned for when project scale demands it

## Error Propagation

- `?` unwraps exactly one Result layer, propagating `Error` to the enclosing Result context.
- `?` on `Option[T]` inside a Result-returning function is **a compile error** — semantics don't mix. Use `.okOr(err)?` to convert, or a monadic pipeline (`.flatMap(opt => opt.okOr(err))`).
- `?` is only valid inside Result-returning functions or `try {}` blocks — compile error otherwise.
- `try { ... }` block expression creates a Result context where `?` can be used in non-Result functions.
  - Emitted as a Go IIFE: `func() (T, error) { ... }()`
  - Final expression is auto-wrapped in `Ok`
  - HM inference: fresh type var for Ok type, unified with final expression
- `try` is not a keyword — only `try {` triggers recognition. `let try = 42` is valid.
- `?` has the downside that the error point is at end of line, easy to miss.

### Monadic methods (Result/Option)

Available on both types, implemented as AST-level desugar to `match`:

- `Result[T, E]`: `.map(f)`, `.flatMap(f)`, `.mapError(f)`
- `Option[T]`: `.map(f)`, `.flatMap(f)`, `.okOr(err)`, `.okOrElse(fn)`

Useful when the zigzag of `?` would be noisy — e.g. FFI pointer chains:
```arca
url.Parse(s)                        // Result[Option[Ref[URL]], E]
  .flatMap(opt => opt.okOr(NotFound))  // Result[Ref[URL], E]
  .map(u => u.Host)                    // Result[String, E]
```

## Prelude (Built-in Functions)

Built-in functions are defined in `prelude.go` as a map of Arca names to Go translations:

```go
var prelude = map[string]BuiltinDef{
    "println": {GoFunc: "fmt.Println", Import: "fmt"},
    "toBytes": {GoFunc: "[]byte", Lower: customFunc},
    "map":     {GoFunc: "Map_", Builtin: "map"},
}
```

Each entry specifies the Go function name, required imports, helper generation flags, and an optional custom `Lower` function for complex transformations. Adding a new builtin is one line in the map.

The lowerer checks the prelude map for any unrecognized function call. No hardcoded switch cases in lower.go.

## Error Messages

All errors (parse, type check) follow a unified format:

```
file:line:col: message
```

Examples:
```
main.arca:3:11: function 'add' expects 2 arguments, got 1
main.arca:2:11: unknown constructor: User
main.arca:3:3: non-exhaustive match on Color: missing Green, Blue
```

Formatted by `formatError()` in main.go. Checker errors carry `Pos` (line/col) and are combined with the file path at output time.

## Generated Go Code Style

- All generated identifiers follow Go conventions (camelCase/PascalCase)
- Builtin helpers (`Map_`, `Filter_`, `Fold_`, `Option_`, `Result_`, `Ok_`, `Err_`, `Some_`, `None_`, `Ptr_`) use trailing underscore to avoid collision with Go builtins
- Helpers are only emitted when used
- Unused variable bindings in match patterns are suppressed
- Output generated via GoWriter (structured builder with auto-indentation), normalized by `gofmt` in tests

## Things Intentionally Not Included

- **No ad-hoc polymorphism** (type classes/traits) — same as Go. May revisit if needed for io.Reader/io.Writer
- **No macros** — simplicity over metaprogramming
- **No exceptions** — Result type for error handling
- **No null** — Option type instead
- **No mutable variables** — fully immutable
- **No side effect tracking** — pragmatic, Go FFI makes it impractical

## Constrained Types

Types can carry constraints validated at construction time:

```arca
type User {
  User(
    id: Int{min: 1}
    name: String{min_length: 1, max_length: 100}
    email: String{pattern: ".+@.+"}
  )
}
```

- Constraints use `{}` after the type name
- Built-in: `min`, `max`, `min_length`, `max_length`, `pattern`
- Custom: `validate: func_name` (runtime only, not OpenAPI-convertible)
- Constructor auto-generates validation, returns `(T, error)`
- **Immutability guarantees constraints hold permanently after construction**
- Constructor without `?` returns `Result[T, error]` for pattern matching
- Constructor with `?` propagates error (only in Result-returning functions)
- Type aliases generate Go defined types (nominal): `type Email = String{...}` → `type Email string` + `NewEmail()`
- `UserId` and `OrderId` are distinct types even with identical constraints
- No re-constraining aliases: `Email{min_length: 5}` is an error
- No cross-field constraints: use constructor functions
- Constraints are opt-in, not forced on all types

### Design principle: static vs runtime constraints
- **Static** (Arca): Value constraints. Validated at construction, guaranteed by immutability.
- **Runtime** (Go): Execution constraints (timeout, cancellation, context). Inherently mutable.
- Arca guarantees values. Go guarantees execution.

### Constraint compatibility (Level 2)
- Stricter type can be passed where wider type is expected: `AdultAge → Age` ✓, `Age → AdultAge` ✗
- Constraints normalized into dimensions: Value (min/max), Length (min_length/max_length), Pattern, Validate
- Range dimensions: range inclusion. Exact dimensions: equality.
- No structural aliases — `type X = T` is always nominal (newtype)

### Future levels
- Condition-based narrowing
- Go type optimization (Int{0,255} → uint8)
- JSON/OpenAPI/DB schema derivation from constraints

## Tags Block

Go struct tags for external library integration. Separate from constrained types (constraints = domain values, tags = external system mapping).

```arca
type User(id: Int, userName: String) {
  tags { json, db(snake) }
}
```

- `tag_name` — all fields, field name as-is
- `tag_name(snake)` — all fields, case conversion (snake, kebab)
- `tag_name { field: "value" }` — override only specified fields
- `()` for global rules, `{}` for overrides — no ambiguity
- Transitional: stdlib will eventually hide this

## Methods

Methods are defined inside type body with implicit `self`:

```arca
type User {
  User(name: String, age: Int)

  fun greet() -> String { "Hello ${self.name}" }
}
```

- Maps to Go methods: `func (u User) Greet() string`
- Namespace per type — no collision between `User.greet` and `Admin.greet`

## Associated Functions (static fun)

Functions attached to a type but without `self`:

```arca
type Greeting {
  Hello(name: String)
  Goodbye(name: String)

  static fun from(s: String) -> Greeting {
    match s {
      "Hello" => Self.Hello(name: "World")
      _ => Self.Goodbye(name: "World")
    }
  }
}
```

- `static fun` = associated function, no `self` access
- Called as `Type.method(...)` → generates Go package-level function (`greetingFrom(...)`)
- `Self` resolves to the enclosing type name inside methods and associated functions
- Follows Swift's `static func` pattern — explicit, one keyword, no ambiguity between method and associated function

## Go Runtime Primitives

- `defer`, `context`, `goroutine`, `channel` — use Go transparently, don't abstract
- `defer` needed but syntax is "un-type-like" — accepted as Go's domain
- Arca guarantees values (static constraints), Go guarantees execution (runtime constraints)
