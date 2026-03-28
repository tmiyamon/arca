# Goux Language Specification v0.1

A statically typed language with ML-level type safety that compiles to Go.

## Design Principles

- Type safety and correctness first
- Familiar syntax (Rust-influenced, common conventions)
- Go ecosystem and runtime (single binary, fast startup, goroutines)
- Immutable by default

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
```

### String Interpolation

```
let greeting = "Hello ${name}, you are ${age} years old!"
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
// user.gx
pub fn find(id: Int) -> Result[User, Error] { ... }

// main.gx
import user
let u = user.find(1)
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

### FFI (Go interop)

Mutable Go values are wrapped in opaque types at the FFI boundary.
Goux code remains fully immutable.

### Comments

```
// single line comment
```

### File Extension

`.gx`
