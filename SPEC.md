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

### Type Parameters

```
type Pair[A, B] {
  Pair(first: A, second: B)
}
```

### Functions

```
pub fn status_label(status: OrderStatus) -> String {
  match status {
    Pending -> "pending"
    Confirmed -> "confirmed"
    Shipped -> "shipped"
  }
}

fn internal_helper() -> Int {
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
pub fn find_user(id: Int) -> Result[User, Error] {
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
pub fn find(id: Int) -> Result[User, Error] { ... }

// main.arca
import user
let u = user.find(1)
```

### Imports

```
// Arca modules
import user
import order.item

// Go packages (prefixed with go.)
import go.fmt
import go.database.sql
import go.net.http
```

### Built-in Types

| Type | Description |
|------|-------------|
| Int | Integer |
| Float | Floating point |
| String | UTF-8 string |
| Bool | True / False |
| List[T] | Immutable list |
| Option[T] | Some(T) / None |
| Result[T, E] | Ok(T) / Error(E) |
| (A, B, ...) | Tuple |

### FFI (Go interop)

Go packages are imported with the `go/` prefix and called directly.

```
import go.fmt
import go.os

fmt.Println("hello")
let file = os.Open("data.txt")?
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

### Side Effects

No special syntax. Side effects (I/O, logging, etc.) are called directly.

```
fn main() {
  fmt.Println("hello")
}
```

### Testing

Uses Go's testing package directly.

```
import go/testing

fn test_status_label(t: testing.T) {
  assert status_label(Pending) == "pending"
}
```

Run with `go test` on the generated Go code.

### Comments

```
// single line comment
```

### File Extension

`.arca`
