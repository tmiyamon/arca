# Arca Language Specification v0.2

A statically typed language with ML-level type safety that compiles to Go.

## Design Principles

- Type safety and correctness first
- Familiar syntax (Rust-influenced, common conventions)
- Go ecosystem and runtime (single binary, fast startup, goroutines)
- Immutable by default
- Go types are opaque — Arca guarantees its own types, not Go's

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
    Pending -> "pending"
    Confirmed -> "confirmed"
    Shipped -> "shipped"
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
(x) => x + 1
(x, y) => x + y
() => 42
```

### Pipe Operator (first argument)

```
users
|> filter((u) => u.age > 20)
|> map((u) => u.name)
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

### Error Propagation (provisional)

```
pub fun findUser(id: Int) -> Result[User, Error] {
  let row = db.query_row("SELECT ...", id)?
  let user = scan(row)?
  Ok(user)
}
```

> Note: The `?` syntax is provisional. Final error propagation syntax TBD.

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
// Arca modules — qualified access
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

Arca uses bidirectional type inference. Types flow both bottom-up (from values) and top-down (from context).

```
fun fetch() -> Result[Int, error] {
  Ok(42)                          // Ok type args inferred from return type
}

fun process(r: Result[Int, error]) { ... }
process(Ok(42))                    // inferred from parameter type

let r: Result[Int, error] = Ok(42) // inferred from annotation

(c) -> error => handler(c)         // lambda param type inferred from call context
```

Type annotations required when context is absent: `let r = Ok(42)` (no annotation, no usage context).

### Built-in Types

| Type | Description |
|------|-------------|
| Unit | No value (void) |
| Int | Integer |
| Float | Floating point |
| String | UTF-8 string |
| Bool | True / False |
| List[T] | Immutable list |
| Option[T] | Some(T) / None |
| Result[T, E] | Ok(T) / Error(E) |
| (A, B, ...) | Tuple |

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
| (T, error) | Result[T, Error] |
| (T, bool) | Option[T] |
| Other multi-return | Tuple |

**Mutability boundary:**
- Arca-defined types are fully immutable (language-guaranteed)
- Go types are opaque — Arca does not guarantee their immutability
- Go developers are expected to understand Go's mutation semantics

### Built-in Functions

```
println("hello")         // print with newline
print("hello")           // print without newline
```

Available without import. Maps to Go's `fmt.Println` / `fmt.Print`.

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
      "Hello" -> Self.Hello(name: "World")
      _ -> Self.Goodbye(name: "World")
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
