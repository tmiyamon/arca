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
- `try { ... }` is a block expression that returns `Result[T, Error]` where T is the type of the final expression
- `try` is not a keyword — only `try {` is recognized as a try block. `let try = 42` is valid

### Monadic methods on Result/Option

- `Result[T, E]`: `.map(f)`, `.flatMap(f)`, `.mapError(f)`
- `Option[T]`: `.map(f)`, `.flatMap(f)`, `.okOr(err)`, `.okOrElse(fn)`

Example, converting a Go FFI pointer return into a `Result[T, E]` without the `?`-zigzag:

```
fun parseHost(s: String) -> Result[String, error] {
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
let r = Ok(42)                     // Result[Int, error] — error type defaults
let x = None                       // Option[T] — T resolved from later usage
let items = []                     // []T — T resolved from later usage

fun process(r: Result[Int, error]) { ... }
process(r)                          // r's type variable resolved to Result[Int, error]

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
| Int | Integer |
| Float | Floating point |
| String | UTF-8 string |
| Bool | True / False |
| List[T] | Immutable list |
| Map[K, V] | Hash map (Go map under the hood) |
| Option[T] | Some(T) / None |
| Result[T, E] | Ok(T) / Error(E) |
| Any | unknown-typed (maps to Go `interface{}`) |
| (A, B, ...) | Tuple |

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
| Int | `min`, `max` |
| Float | `min`, `max` |
| String | `min_length`, `max_length`, `pattern` |
| List | `min_length`, `max_length` |
| All | `validate` (custom function) |

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
fun run() -> Result[Unit, error] {
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
