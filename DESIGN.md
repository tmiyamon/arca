# Arca Design Decisions

**Core philosophy: Type as Single Source of Truth.**
Validation, serialization, schema, domain logic — all derived from the type definition.

This document records language design decisions, their rationale, and trade-offs.

## Naming Conventions

- **Arca source**: `snake_case` for functions, variables, fields
- **Generated Go**: `camelCase` (private), `PascalCase` (public)
- **Types**: `PascalCase` in both Arca and Go
- **Constructors**: `PascalCase` in Arca

## Builtin Names

`Ok`, `Error`, `Some`, `None` are builtin constructors for Result and Option types.

- They are **not reserved words** — user-defined constructors with the same name are allowed
- User-defined constructors **shadow** builtins (same as Rust's prelude)
- Convention: avoid naming user constructors `Ok`/`Error`/`Some`/`None`
- Future: consider emitting a warning when shadowing occurs

## Immutability

- Arca-defined types are **fully immutable**
- `let` is the only binding form, no `let mut`
- Go types from FFI are **opaque** — Arca does not guarantee their immutability
- Go developers are expected to understand Go's mutation semantics at the FFI boundary

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
- **Type checking**: Go FFI calls are **not type-checked** by Arca. Go compiler catches these.
- **Qualified types**: `http.ResponseWriter`, `*http.Request` are passed through as-is
- **Return type mapping**:
  - `(T, error)` → captured by `?` operator
  - No automatic wrapping into Result struct
- **Pointer types**: `*Type` syntax exists solely for Go FFI interop

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
- Type aliases for reusable constraints: `type Email = String{pattern: ".+@.+"}`
- No re-constraining aliases: `Email{min_length: 5}` is an error
- No cross-field constraints: use constructor functions
- Constraints are opt-in, not forced on all types

### Design principle: static vs runtime constraints
- **Static** (Arca): Value constraints. Validated at construction, guaranteed by immutability.
- **Runtime** (Go): Execution constraints (timeout, cancellation, context). Inherently mutable.
- Arca guarantees values. Go guarantees execution.

### Future levels
- Constraint compatibility checking (Age vs AdultAge)
- Condition-based narrowing
- Go type optimization (Int{0,255} → uint8)
- JSON/OpenAPI/DB schema derivation from constraints

## Methods (planned)

Methods are needed for constrained types to keep domain operations closed:

```arca
type Age = Int{min: 0, max: 150}

fn Age.increment(self) -> Age {
  Age(self.value + 1)?
}
```

- Syntax: `fn Type.method(self) -> RetType { ... }`
- Maps to Go methods: `func (a Age) Increment() (Age, error)`
- Namespace per type — no collision between `Age.increment` and `Score.increment`
- Decision driven by: constrained types need methods, Go FFI already uses methods, pipe operator becomes redundant
- Pipe operator will likely be dropped once methods are added

## Go Runtime Primitives

- `defer`, `context`, `goroutine`, `channel` — use Go transparently, don't abstract
- `defer` needed but syntax is "un-type-like" — accepted as Go's domain
- Arca guarantees values (static constraints), Go guarantees execution (runtime constraints)
