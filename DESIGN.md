# Arca Design Decisions

**Core philosophy: Type as Single Source of Truth.**
Validation, serialization, schema, domain logic — all derived from the type definition.

This document records language design decisions, their rationale, and trade-offs.

## Naming Conventions

- **Arca source**: `camelCase` for functions, variables, fields
- **Generated Go**: `camelCase` (private), `PascalCase` (public)
- **Types**: `PascalCase` in both Arca and Go
- **Constructors**: `PascalCase` in Arca

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

- Implemented as a **generic struct**, not a pointer
- `Some(x)` → `Some_(x)`, `None` → `None_[T]()`
- Avoids nil leaking into Go code
- Pattern matching: `if value.Valid { ... } else { ... }`

## Result Type

- Implemented as a **generic struct** with `Value`, `Err`, and `IsOk` fields
- `Ok(x)` → `Ok_[T, E](x)`, `Error(e)` → `Err_[T, E](e)`
- `?` operator works on Go FFI calls that return `(T, error)`:
  - Captures multi-return into temp vars
  - Checks error, returns `Err_` if non-nil
  - Otherwise binds the value
- Pattern matching: `if result.IsOk { ... } else { ... }`

## Go FFI

- **Import syntax**: `import go.fmt` (dot separator, unified with Arca modules)
- **Type checking**: Go FFI argument count/types validated via `TypeResolver` (go/types)
- **TypeResolver**: Uses project's `go.mod` for package resolution. `packages.Config.Dir` set to nearest go.mod directory (walked up from .arca file). `CanLoadPackage` checks go.mod `require` entries directly (not module cache). Missing packages produce error with `go get` suggestion.
- **GoPackage struct**: Centralizes Go import path parsing. Handles version suffixes (`github.com/labstack/echo/v5` → ShortName `"echo"`). Used in import registration, type name resolution, and receiver type resolution.
- **Qualified types**: `http.ResponseWriter`, `*http.Request` are passed through as-is
- **Return type mapping** (mechanical, in `goFuncReturnType`):
  - `(T, error)` → `Result[T, error]` (GoMultiReturn)
  - `(T, bool)` → `Option[T]` (GoMultiReturn)
  - `(error)` → `Result[Unit, error]` (GoMultiReturn)
  - `(T)` → `T`
  - `(T1, T2, ...)` → Tuple (GoMultiReturn)
- **GoMultiReturn flag**: `IRFnCall`, `IRMethodCall`, `IRConstructorCall` carry this flag. Emitter generates multi-value receive + wrapping. Consumption sites (let, try, match) read IR types — no ad-hoc detection.
- **Pointer types**: `*Type` syntax exists solely for Go FFI interop
- **Project structure**: `go.mod` at project root, managed by user with `go get`. `build/go.mod` copied from parent.

## Pattern Matching

- **Exhaustive**: compiler enforces all constructors are covered
- **Wildcard**: `_` catches remaining cases
- **Priority order in codegen**:
  1. Result patterns (`Ok`/`Error`) → `if result.IsOk`
  2. List patterns (`[]`/`[first, ..rest]`) → `if len(...)`
  3. Option patterns (`Some`/`None`) → `if value.Valid`
  4. Enum patterns → `switch` on iota
  5. Sum type patterns → `switch v := x.(type)`

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

- `?` syntax is **provisional**
- Alternatives considered: `let!`, `try {}`, `for-yield`
- Decision deferred — `?` works for now, final syntax TBD
- `?` has the downside that the error point is at end of line, easy to miss

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
      "Hello" -> Self.Hello(name: "World")
      _ -> Self.Goodbye(name: "World")
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
