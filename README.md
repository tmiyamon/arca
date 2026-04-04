# Arca

[![Test](https://github.com/tmiyamon/arca/actions/workflows/test.yml/badge.svg)](https://github.com/tmiyamon/arca/actions/workflows/test.yml)

An expressive language that compiles to Go.

```
type Greeting {
  Hello(name: String)
  Goodbye(name: String)
}

fun message(g: Greeting) -> String {
  match g {
    Hello(name) -> "Hello, ${name}!"
    Goodbye(name) -> "Goodbye, ${name}!"
  }
}

fun main() {
  let greet = Greeting.Hello(name: "World")
  println(message(greet))
}
```

## Why

Go has a great ecosystem, fast compilation, and simple deployment. But its type system lacks algebraic data types, pattern matching, and Result types — making it hard to express domain logic safely.

Arca adds the expressiveness Go is missing. You get Go's runtime, ecosystem, and single-binary deployment with proper ADTs, exhaustive pattern matching, and type-safe error handling.

## Install

```
go install github.com/tmiyamon/arca@latest
```

Requires Go 1.18+.

## Quick Start

```
arca init myapp
cd myapp
arca run
```

## Features

**Algebraic data types** — enums, records, and tagged unions.

```
type Color { Red, Green, Blue }

type User {
  User(name: String, age: Int)
}

type ApiError {
  NotFound(message: String)
  BadRequest(message: String)
}
```

**Exhaustive pattern matching** — the compiler ensures all cases are handled.

```
fun describe(err: ApiError) -> String {
  match err {
    NotFound(msg) -> "Not found: ${msg}"
    BadRequest(msg) -> "Bad request: ${msg}"
  }
}
```

**Result and Option types** — no null, no exceptions.

```
fun findUser(id: Int) -> Result[User, error] {
  let row = db.QueryRow("SELECT ...", id)?
  Ok(User(name: row.name, age: row.age))
}
```

**Constrained types** — validation at construction, guaranteed by immutability.

```
type Email = String{pattern: ".+@.+", max_length: 255}
type Age = Int{min: 0, max: 150}

type User {
  User(
    name: String{min_length: 1, max_length: 100}
    email: Email
    age: Age
  )
}

let user = User(name: "Alice", email: Email("alice@example.com")?, age: Age(30)?)?
// If construction succeeds, constraints hold forever (immutable)
```

**Full Go interop** — use any Go package directly.

```
import go "net/http"

fun main() {
  http.HandleFunc("/hello", handleHello)
  http.ListenAndServe(":8080", nil)
}
```

**Immutable by default** — all values are immutable. Go types at the FFI boundary are opaque.

## Commands

```
arca init <name>       Create a new project
arca run [path]        Transpile and run (default: ./main.arca)
arca build [path]      Transpile and compile to binary
arca emit <file>       Print generated Go code
arca fmt <file>        Format source code
arca health            Check Go installation
```

## Status

Arca is early-stage and under active development. The language works for small programs and prototypes. Expect breaking changes.

See [SPEC.md](SPEC.md) for the language specification and [DECISIONS.md](DECISIONS.md) for design rationale.

## License

MIT
