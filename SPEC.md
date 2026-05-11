# Arca Language Specification v0.2

A statically typed language for backend development that compiles to Go.

## Design Principles

**Goal: be the most practical language for backend development.** Other properties are consequences of pursuing this goal (see `DESIGN.md`).

- **Layer 1 — safety prerequisite**: nil panic, typed nil, panic propagation, `interface{}` traps, zero-value surprise, unintended mutation are sealed at the FFI boundary. Required to stand on top of Go.
- **Layer 2 — SSOT as derived property**: validation, serialization, schema, domain logic derive from a single model definition. Constrained types + ADT + backend focus yield this as a byproduct.
- ML-level type safety and correctness (ADT, HM inference, constrained types)
- Familiar syntax (Rust-influenced, common conventions)
- Go ecosystem and runtime (single binary, fast startup, goroutines)
- Immutable by default
- Go types are opaque — Arca guarantees its own types, not Go's
- FFI principle: *Arca makes guarantees. FFI is only allowed to the extent that guarantees can be preserved across it.*

## Syntax Summary

### Type Definitions (ADT)

```
// Fieldless variants = enum
type OrderStatus {
  Pending
  Confirmed
  Shipped
}

// Single constructor = record
type Order {
  Order(id: Int, status: OrderStatus, name: String)
}

// Multiple constructors with fields = tagged union
type ApiResponse {
  Success(data: Order)
  ErrorResponse(message: String, code: Int)
}
```

### Constructor Syntax

```
// Single-constructor type: use type name directly
let user = User(name: "Alice", age: 30)

// Multi-constructor type: qualify with type name
let greet = Greeting.Hello(name: "World")
let err = ApiError.NotFound(message: "not found")

// Enum: qualify with type name
let color = Color.Red

// Builtins (Ok/Error/Some/None): always unqualified
let result = Ok(user)
let opt = Some("hello")

// Match patterns: always unqualified (subject type known)
match greet {
  Hello(name) -> "Hello, ${name}!"
  Goodbye(name) -> "Goodbye, ${name}!"
}
```

### Type Parameters

```
type Pair[A, B] {
  Pair(first: A, second: B)
}
```

### Functions

```
pub fun statusLabel(status: OrderStatus) -> String {
  match status {
    Pending => "pending"
    Confirmed => "confirmed"
    Shipped => "shipped"
  }
}

fun internalHelper() -> Int {
  42
}
```

### Pattern Matching (exhaustive)

```
match response {
  Success(data) -> process(data)
  ErrorResponse(message, code) -> log(message)
}
```

**Match type pattern (narrowing):** `id: Type => body` narrows an `Any` subject to a concrete type. Within the arm body, `id` has the narrowed type (smart cast). Emits as Go `switch v := x.(type)`.

```
fun describe(v: Any) -> String {
  match v {
    n: Int => "int: ${n}"
    s: String => "string: ${s}"
    _ => "unknown"
  }
}
```

No exhaustiveness check — the universe of types is open. A wildcard arm (`_`) is recommended.

### Variables (immutable)

```
let name = "hello"
let count = 42

// Type annotation (optional)
let users: List[User] = []
let count: Int = 42
```

### Tuples

```
let pair = (1, "hello")
let (x, y) = pair

// Type annotation
let pair: (Int, String) = (1, "hello")
```

### Lambdas

```
// Full form with type annotations
(x: Int) -> Int => x + 1
(x: Int, y: Int) -> Int => x + y

// Shorthand (types inferred from context)
x => x + 1
(x, y) => x + y
```

### Function Types

```
// Single parameter (bare form)
Int -> Int

// Multiple parameters (parenthesised)
(Int, String) -> Bool

// Zero parameters
() -> Unit

// Higher order (right-associative: A -> B -> C = A -> (B -> C))
(Int -> Int) -> Int
```

Function types are first-class and usable in any type position:

```
fun apply(f: Int -> Int, x: Int) -> Int { f(x) }
let h: (Int, Int) -> Int = (a, b) => a + b
type Handler { Handler(fn: Ctx -> Result[Unit, Error]) }
```

Param type inference flows through the type annotation: `apply(n => n + 1, 42)`
gets `n: Int` from `f: Int -> Int` — no explicit annotation needed at the lambda.

### If Expression

```
let result = if x > 0 { "positive" } else { "negative" }

// else if chains
if n > 0 {
    "positive"
} else if n < 0 {
    "negative"
} else {
    "zero"
}

// Without else (void context)
if condition {
    doSomething()
}
```

### Index Access

```
let first = nums[0]
let item = items[i]
```

### Pipe Operator (first argument)

```
users
|> filter(u => u.age > 20)
|> map(u => u.name)
|> fold(0, (acc, x) => acc + x)
```

### String Interpolation

```
let greeting = "Hello ${name}, you are ${age} years old!"
```

### For Loop (collection traversal)

```
for x in list {
  process(x)
}

// Range
for i in 0..10 {
  process(i)
}
```

### main() -> Result

Arca allows `fun main() -> Result[Unit, Error]` so `?` works at the top level without a wrapping `try {}`. On `Err`, the error is printed to stderr and the process exits with status 1. On `Ok`, the process exits normally.

```
fun main() -> Result[Unit, Error] {
  let n = strconv.Atoi("42")?
  println(n)
  Ok(())
}
```

This mirrors Rust's `fn main() -> Result<(), Error>`. `main` without a return type continues to work as before.

### Error Propagation

```
// ? in Result-returning functions
pub fun findUser(id: Int) -> Result[User, Error] {
  let row = db.query_row("SELECT ...", id)?
  let user = scan(row)?
  Ok(user)
}

// try {} block — creates a Result context for ? in non-Result functions
fun main() {
  let result = try {
    let row = db.query_row("SELECT ...", 1)?
    let user = scan(row)?
    user
  }
  match result {
    Ok(user) => println(user.name)
    Error(err) => println("failed: ${err}")
  }
}
```

- `?` unwraps `Result` one layer, propagating `Error` to the enclosing Result context
- `?` on `Option[T]` is **a compile error** in a Result-returning function — the two semantics do not mix. Convert explicitly with `.okOr(err)?`, or use a monadic pipeline (`.flatMap`, `.map`).
- `?` is only valid inside Result-returning functions or `try {}` blocks (compile error otherwise)
- `?` is a postfix operator that binds tighter than binary operators, so `f()? * 2` parses as `(f()?) * 2` and is allowed in any expression position — `match expr? { … }`, `f(g()?)`, `Ok(g()? + h()?)`, `let x = g()? + 1`. The compiler hoists each `?` into a synthetic split + nil-check + early return ahead of the enclosing statement
- After `?`, the postfix chain (`.field`, `.method(args)`, `[idx]`, `[T](args)`) continues on the unwrapped value: `sql.Open(path)?.okOr(err)`, `boxFor(n)?.double()`, `f()?.bar()?.baz` all parse
- `try { ... }` is a block expression that returns `Result[T, Error]` where T is the type of the final expression
- `try` is not a keyword — only `try {` is recognized as a try block. `let try = 42` is valid

### Monadic methods on Result/Option

- `Result[T, E]`: `.map(f)`, `.flatMap(f)`, `.mapError(f)`
- `Option[T]`: `.map(f)`, `.flatMap(f)`, `.okOr(err)`, `.okOrElse(fn)`

Example, converting a Go FFI pointer return into a `Result[T, E]` without the `?`-zigzag:

```
fun parseHost(s: String) -> Result[String, Error] {
  url.Parse(s)
    .flatMap(opt => opt.okOr(NotFound))
    .map(u => u.Host)
}
```

### Safe reference type

- `Ref[T]` — non-null reference to an immutable value. Emits as Go `*T`.
- Construction: `&v` takes a reference from an lvalue (including `&r.field`).
- Access: field and method calls auto-deref (`r.field`, `r.method()`). Other operations (comparison, pattern binding, etc.) are explicit.
- `Option[Ref[T]]` is the only way to spell "nullable reference"; Go `*T` FFI returns land here automatically.

### Visibility

- `pub` = public
- No modifier = module-private

### Modules

- 1 file = 1 module
- File name = module name

```
// user.arca
pub fun find(id: Int) -> Result[User, Error] { ... }

// main.arca
import user
let u = user.find(1)
```

### Imports

```
// Arca built-in packages — bundled with the arca binary
import stdlib
stdlib.Encode(value)

// Arca modules — qualified access (same-directory .arca files)
import user
user.find(1)

// Selective import — direct access
import user.{find, create}
find(1)

// Alias
import user as u
u.find(1)

// Submodule
import util.math
util.math.add(1, 2)

// Go packages (string literal path)
import go "fmt"
import go "database/sql"
import go "github.com/gin-gonic/gin"

// Side-effect import (DB drivers etc.)
import go _ "modernc.org/sqlite"

// No wildcard imports
```

### Type Inference

Arca uses HM-style type inference with type variables and unification. Types flow bottom-up (from values), top-down (from context), and forward (from usage).

```
let r = Ok(42)                     // Result[Int, Error] — error type defaults
let x = None                       // Option[T] — T resolved from later usage
let items = []                     // []T — T resolved from later usage

fun process(r: Result[Int, Error]) { ... }
process(r)                          // r's type variable resolved to Result[Int, Error]

map(nums, x => x * 2)              // x type inferred from list element type
sort.Slice(s, (i, j) => s[i] < s[j])  // i, j inferred from Go FFI signature
```

**Auto-Some:** At typed `Option[T]` positions a bare value of type `T` is implicitly lifted into `Some(v)`. Single-layer only — nested `Option[Option[T]]` still requires explicit `Some(Some(v))` or `Some(None)`. `None` is never auto-inserted; `&` (Ref construction) is never auto-inserted.

```
fun describe(n: Option[Int]) -> String { ... }

let x: Option[Int] = 10       // auto-Some → Some(10)
let y: Option[Int] = None     // explicit, required
describe(42)                   // auto-Some → describe(Some(42))
describe(None)                 // explicit None
```

Explicit type arguments via `f[T](args)` when inference can't determine:

```
let user = stdlib.Decode[User](data)?   // Decode's T = User
println(stdlib.Decode[User](data)?)      // inline without let annotation
```

Function signatures require explicit types (Rust/Kotlin style). Inference operates within function bodies.

### Built-in Types

| Type | Description |
|------|-------------|
| Unit | No value (void) |
| Int | 64-bit signed integer (Go `int`; 64-bit-only target) |
| UInt | 64-bit unsigned integer (Go `uint`) |
| Float | 64-bit floating point (Go `float64`) |
| Int8 / Int16 / Int32 / Int64 | Narrow signed integers (Go `int8` … `int64`) |
| UInt8 / UInt16 / UInt32 / UInt64 | Narrow unsigned integers (Go `uint8` … `uint64`) |
| Float32 / Float64 | Narrow floats (Go `float32` / `float64`) |
| String | UTF-8 string |
| Bool | True / False |
| List[T] | Immutable list |
| Map[K, V] | Hash map (Go map under the hood) |
| Option[T] | Some(T) / None |
| Result[T, E] | Ok(T) / Error(E) |
| Any | unknown-typed (maps to Go `interface{}`) |
| (A, B, ...) | Tuple |
| A -> B, (A, B) -> C | Function types |

`Int` is fixed at 64 bits. Generated Go files carry a `//go:build` constraint
that excludes 32-bit GOARCH (`386`, `arm`, `mips`, `mipsle`, etc.); `arca run`
and `arca build` refuse to invoke the Go toolchain on a 32-bit target.

The narrow tower (`Int8` … `Int64`, `UInt8` … `UInt64`, `Float32` / `Float64`)
maps directly to the corresponding Go primitive. Conversions go through the
`T(x)?` constructor syntax shared with constrained types and structs:

```
let i: Int = 100
let i8 = Int8(i)?            // Ok(int8(100))
let r = Int8(200)            // Result[Int8, Error] — out of range
let u32 = UInt32(20)?        // explicit narrowing to uint32
let f32 = Float32(3.14)?     // float64 → float32, errors when out of range
```

`T(x)?` always returns `Result[T, Error]` for narrow targets; future range-
aware widening will skip the wrap when the source range proves to fit.
Cross-base casts (`Int(uintval)?` etc.) work but currently bit-reinterpret at
the Go boundary; the validator's range check still seals Layer 1, but the
diagnostic for an out-of-range source kind is less specific until the cross-
base diagnostic lands.

Numeric literal hint coercion (`let x: Int8 = 100`) types the literal at
the hint's Go type when the value fits; out-of-range literals fail at
compile time (`let x: Int8 = 200` rejected with line:col diagnostic).

Same-kind widening fires implicitly when the source range fits the
target — `Int8 → Int` / `Int32 → Int64` / `UInt8 → UInt32` flow without
a cast, and the compiler inserts the Go conversion (`int(int8val)`)
behind the scenes. Asymmetric: narrowing (`Int → Int8`) still requires
an explicit `Int8(...)?` cast. Cross-kind (signed↔unsigned, integer↔
float) keeps requiring an explicit cast as well — the cross-base
diagnostic catches the mistake before emit.

`+ - *` on `Int` / `UInt` operands route through panic-checked emit
helpers (`__addInt`, `__subUInt`, `__mulInt`, ...) — overflow / underflow
trips a runtime panic so silent wrap is no longer a Layer 1 leak. Narrow
operands widen to base before the helper sees them; the result is base-
typed (`Int8 + Int8 → Int`) so subsequent narrowing is explicit via the
`T(x)?` cast. Float arithmetic stays plain (Inf is in IEEE 754 spec).
Mixing `Int` and `UInt` in a single arithmetic or comparison op surfaces
as a compile error directing the user to `Int(...)?` or `UInt(...)?`.

For arithmetic that should bubble up errors instead of panic, `stdlib`
provides Result-returning checked variants:

```
import stdlib

let r = stdlib.CheckedAddInt(a, b)?    // (int, error) — overflow → Err
let q = stdlib.CheckedDivInt(a, 0)     // → Err(ErrDivByZero)
```

Coverage: `CheckedAdd / Sub / Mul / Div` × `Int / UInt` (8 functions). Float
checked variants land when there is a real consumer.

For values that exceed `Int` / `UInt`'s 64-bit range entirely (cryptographic
ids, large-counter sums, smallest-unit currency), `stdlib.BigInt` provides
arbitrary-precision arithmetic via Go's `math/big`:

```
import stdlib

let a = stdlib.NewBigInt(1000000)
let b = a.Mul(a)                          // 10^12, still fits Int
let huge = stdlib.BigIntFromString("123456789012345678901234567890")?
println(huge.Mul(huge).String())          // arbitrary precision
let narrow = huge.ToInt()                  // Result — Err on overflow
```

`BigInt` is heap-allocated and slower than `Int`; use it only when range
forces the choice. Conversion to / from `Int` is explicit (`NewBigInt(v)`
to widen, `b.ToInt()?` to narrow).

### Map

```
let users: Map[String, Int] = {"alice": 30, "bob": 25}
let age = users["alice"]            // direct access (zero value if missing)
let empty: Map[String, Int] = {}    // empty map (hint required)
```

Key/value types are inferred from the first entry. Empty map `{}` requires
a type annotation hint. Iteration order is random (inherent to Go maps).

### Strings

```
"Hello"                          // plain string
"Hello ${name}!"                 // string interpolation
"""
  SELECT *
  FROM users
  WHERE name = ${name}
  """                            // multiline string (triple-quoted, with interpolation)
```

- `"..."` — single-line string with escape sequences (`\n`, `\t`, `\\`, `\"`)
- `"""..."""` — multiline string, raw (no escape processing), with `${}` interpolation
- Common leading whitespace is stripped based on indentation of closing `"""`
- Compiles to Go `fmt.Sprintf` (interpolation) or string literal / backtick (plain)

### FFI (Go interop)

Go packages are imported with string literal paths.

```
import go "fmt"
import go "os"

fmt.Println("hello")
let file = os.Open("data.txt")?
```

**Address operator (`&`) for Go FFI:**

```
let user = User(id: 0, name: "", email: "")
let _ = db.Get(&user, "SELECT ...")?
// & marks the boundary where immutability guarantee ends
```

**Discarding values:**

```
let _ = db.Exec("INSERT ...")?   // discard success value, propagate error
```

**Return type conversion (automatic):**

| Go return type | Arca type |
|----------------|-----------|
| `(T, error)` | `Result[T, Error]` |
| `(*T, error)` | `Result[Option[Ref[T]], Error]` |
| `(T, bool)` | `Option[T]` |
| `*T` | `Option[Ref[T]]` |
| Other multi-return | Tuple |

Go pointer returns are automatically wrapped in `Option[Ref[T]]` — `Ref[T]` is the safe non-null reference, `Option` carries the nullability. To reach the underlying `Ref[T]`, use `.okOr(err)?` on the Option or pattern-match; `?` only unwraps the Result layer, not the Option.

**Mutability boundary:**
- Arca-defined types are fully immutable (language-guaranteed)
- Go types are opaque — Arca does not guarantee their immutability
- Go developers are expected to understand Go's mutation semantics

### Built-in Functions

```
println("hello")         // print with newline
print("hello")           // print without newline
len(items)               // list length
map(list, x => x * 2)   // transform elements
filter(list, x => x > 0) // select elements
fold(list, 0, (acc, x) => acc + x) // reduce
take(list, 3)            // first n elements
takeWhile(list, x => x > 0) // elements while predicate holds
```

Available without import.

### Side Effects

No special syntax. Side effects (I/O, logging, etc.) are called directly.

```
fun main() {
  println("hello")
}
```

### Testing

Uses Go's testing package directly.

```
import go "testing"

fun test_statusLabel(t: testing.T) {
  assert statusLabel(Pending) == "pending"
}
```

Run with `go test` on the generated Go code.

### Constrained Types

```
// Constraints on fields
type User {
  User(
    id: Int{min: 1}
    name: String{min_length: 1, max_length: 100}
    email: String{pattern: ".+@.+"}
  )
}

// Type alias (always nominal — creates distinct Go type)
type PositiveInt = Int{min: 1}
type Email = String{pattern: ".+@.+", max_length: 255}

// Custom validation
type EvenInt = Int{validate: isEven}

// Constructor with ? — propagates error
let user = User(id: 1, name: "Alice", email: "a@b.com")?

// Constructor without ? — returns Result for pattern matching
let result = Email("test@example.com")
match result {
  Ok(email) -> println("valid: ${email}")
  Error(err) -> println("invalid: ${err}")
}

// Constraint compatibility: stricter type passable where wider type expected
type Age = Int{min: 0, max: 150}
type AdultAge = Int{min: 18, max: 150}

fun greet(age: Age) -> String { ... }
let adult = AdultAge(25)?
greet(adult)  // OK: AdultAge range ⊆ Age range
```

Built-in constraints:

| Type | Constraints |
|------|-------------|
| Int | `min`, `max`, `bits` (8 / 16 / 32 / 64) |
| UInt | `min`, `max`, `bits` (8 / 16 / 32 / 64) |
| Float | `min`, `max`, `bits` (32 / 64) |
| String | `min_length`, `max_length`, `pattern` |
| List | `min_length`, `max_length` |
| All | `validate` (custom function) |

`bits: N` is a storage hint, not a runtime check — the chosen Go type's range
*is* the constraint. `Int{bits: 32}` emits as Go `int32`, `UInt{bits: 8}` as
`uint8`, `Float{bits: 32}` as `float32`. Use it via type alias for naming
(`type Int32 = Int{bits: 32}`) or inline at field positions
(`Counter(small: Int{bits: 16})`). `bits` is rejected on non-numeric bases.

### Methods

```
type User {
  User(name: String, age: Int)

  fun greet(greeting: String) -> String {
    "${greeting}, ${self.name}!"
  }
}

let user = User(name: "Alice", age: 30)
user.greet("Hello")  // "Hello, Alice!"
```

### Traits

Traits declare a named method set; `impl` attaches that method set to a concrete type. Dispatch is dynamic — any value of the concrete type can be passed wherever the trait type is expected.

```
trait Display {
  fun show() -> String
}

type User {
  User(name: String)
}

impl User: Display {
  fun show() -> String { self.name }
}

fun render(d: Display) -> String { d.show() }
```

- `trait Name { fun sig() -> T ... }` — method signatures, no body. `self` is implicit.
- `impl Type: Trait { fun sig() -> T { body } ... }` — method implementations, `self` is implicit, signatures must match. Multiple trait impls per type allowed; inherent methods belong in `type { ... }` (no inherent `impl` form).
- **Orphan rule:** Phase 1 requires both the type and the trait to be declared in the current module.
- **Trait as type:** trait names are valid type expressions (`Result[T, Error]`, `fun f(e: Display)`, `List[Display]`).
- **Coercion:** concrete values implicitly coerce to a trait type at hint-driven positions (function args, let annotations, return types, constructor fields).
- **Match type pattern** narrows a trait object to a concrete type: `match e { id: NotFound => ..., _ => e.message() }`. No exhaustiveness — trait types are an open universe.
- **Forbidden in Phase 1:** default methods, trait inheritance (`trait Ord: Eq`), generic bounds (`[T: Display]`), `Self` and `static fun` inside trait/impl, inherent `impl` blocks, disambiguation syntax for method collisions (any ambiguity is a compile error).

#### The `Error` trait

`Error` is a prelude-built-in trait available without import:

```
trait Error {
  fun message() -> String
}
```

- Impls get an auto-generated `Error() string` shim so they also satisfy Go's stdlib `error` interface: `NotFound{id: 1}` flows into both `Result[_, Error]` and Go APIs expecting `error`.
- Go FFI `(T, error)` returns surface as `Result[T, Error]` on the Arca side. Match `Err` bindings are wrapped via an internal `__goError` adapter so trait methods (`.message()`) resolve on the bound value regardless of source.
- Lowercase `error` is **not** a user-writable type name; always write `Error`.

### Bindable (compiler intrinsic)

`Bindable` is a built-in trait reserved for compiler-synthesised dictionary dispatch — used by stdlib helpers (`BindJSON`, `QueryAs`, `Decode`) to absorb Go-side mutation across the FFI boundary. Manual `impl T: Bindable` is a compile error; activation is via `derive Bindable` on the type declaration.

```
type Todo (
  id: Int
  body: String
) derive Bindable

let d = Todo.draft()
let todo = d.freeze()?    // -> Result[Todo, Error]
```

Compiler synthesises (per derive-marked type T):

- `T.Draft` — mutable shadow type with `BindableSlot[FieldType]` fields (`Set(v) | Unset` sum)
- `T.draft()` — factory returning a fresh empty `T.Draft`
- `(d T.Draft) freeze() -> Result[T, Error]` — returns `Err("<T>.<field> is unset")` for the first unset slot, otherwise calls `NewT` (constrained types) or builds `T{...}` directly (unconstrained)

Generic functions accept Bindable types via `[T: Bindable]`:

```
fun roundtrip[T: Bindable](raw: String) -> Result[T, Error] {
  let d = __bindableT.draft()
  // populate d via FFI (json.Unmarshal, rows.Scan, ...)
  __bindableT.freeze(d)
}

let todo = roundtrip[Todo](raw)?
```

- `derive` sits between the type header and any body block; if no block, it goes at the tail. Product (single-constructor) types only — sum-type `derive Bindable` is a compile error.
- `[T: Bindable]` is the only constraint accepted at MVP. Unknown traits (`[T: Cloneable]`) and multi-trait bounds (`[T: A + B]`) are rejected.
- Call sites with explicit type args inject the matching dispatch dictionary automatically; transitive generic calls (`outer[T: Bindable]` calling `inner[T]()`) forward the caller's hidden parameter.
- `Bindable` is reserved — `trait Bindable { ... }` and `impl X: Bindable` are both compile errors.
- The concept is intrinsic but the runtime types `BindableSlot[T]` and `BindableDict[T, B]` are hosted in the stdlib package (so stdlib helpers can share a single definition). The compiler auto-imports stdlib whenever a `derive Bindable` host appears; user code never writes the import or names the types directly.

### Associated Functions (static fun)

```
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

let greet = Greeting.from("Hello")
```

- `static fun` = no `self`, called as `Type.method(...)`
- `Self` refers to the enclosing type inside methods and associated functions

### Tags Block (Go struct tags)

```
// Simple — field names as-is
type User(id: Int, name: String) {
  tags { json, db }
}

// With conversion rule
type User(id: Int, userName: String) {
  tags { json, db(snake) }
}
// → Go: UserName string `json:"userName" db:"user_name"`

// Individual overrides
type User(id: Int, userName: String) {
  tags {
    json,
    db(snake),
    elastic {
      userName: "full_name"
    }
  }
}
```

### Defer

```
fun run() -> Result[Unit, Error] {
  let db = sql.Open("sqlite", ":memory:")?
  defer db.Close()
  // db is automatically closed when function returns
  Ok(Unit)
}
```

### Short Record Syntax

```
// Shorthand for single-constructor types
type Point(x: Int, y: Int)

// Equivalent to
type Point {
  Point(x: Int, y: Int)
}
```

### Assert

```
assert add(1, 2) == 3
assert x > 0
```

### Comments

```
// single line comment
```

### Project Structure

```
myapp/
├── go.mod                 // Go module (arca init creates, user manages with go get)
├── main.arca              // entry point
├── user.arca              // same package (same dir)
├── db.arca
├── cmd/
│   ├── server/
│   │   └── main.arca
│   └── cli/
│       └── main.arca
└── util/
    └── math.arca          // sub-package (import util.math)
```

Generated Go (1:1 file mapping):
```
build/
├── go.mod                 // copied from project root
├── main.go                // package main
├── user.go                // package main (same dir = same package)
├── db.go                  // package main
└── math/
    └── math.go            // package math (sub-directory = separate package)
```

```
arca init myapp            // create new project (with go.mod)
arca build                 // build ./main.arca
arca build cmd/server      // build cmd/server/main.arca
arca run                   // run ./main.arca
```

### Visibility

- `pub` = exported to other packages (Go PascalCase)
- No `pub` = package-internal (same directory can access)
- Same directory = same package (Go convention)

### File Extension

`.arca`
