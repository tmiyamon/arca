# Arca

[![Test](https://github.com/tmiyamon/arca/actions/workflows/test.yml/badge.svg)](https://github.com/tmiyamon/arca/actions/workflows/test.yml)

**In Arca, constructing a value establishes its invariants once.
Immutability keeps them true for the value's lifetime.** From one
declaration, the JSON binding, SQL column mapping, constructor, and
validator all derive — Single Source of Truth as a property of the
type system, not a discipline.

Arca compiles to Go. The pitch isn't "ADTs on top of Go"; it's making
backend invariants enforceable at the type level without giving up
Go's deployment story.

---

## What Arca does

One declaration is the whole model:

```arca
type Todo (
  id: Int{min: 1}
  body: String{min_length: 1, max_length: 255}
  done: Bool
) derive Bindable {
  tags { json, db(snake) }
}
```

From that, the compiler produces:

- a JSON unmarshaller that validates `min` / `max` / `pattern` constraints
- a SQL row scanner with `snake_case` column mapping
- a constructor `NewTodo(id, body, done)` that rejects invalid values
- an immutable Go struct — if a `Todo` exists at runtime, the constraints
  hold for its entire lifetime

A handler that reads JSON, persists, returns the value:

```arca
fun createTodo(c: Ref[echo.Context], db: Ref[sql.DB]) -> Result[Todo, Error] {
  let draft = stdlib.BindJSON[Todo](c.Request())?    // JSON in, validated
  let res = db.Exec("insert into todos(body) values(?)", draft.body)?
  let id = res.LastInsertId()?                       // (int64, error) → Result[Int, _]
  Todo(id, draft.body, false)                        // construction validates
}
```

5 lines. The equivalent Go is ~25 lines of `c.Bind`, manual validation,
`if err != nil`, `int64`-to-`int` conversion, and re-affirming the
invariants at each boundary.

## Why this matters

In Go, in Java, in TypeScript, your `User` model is restated in five
places that drift: the struct, the JSON unmarshaller, the SQL row
scanner, a validation function, and the constructor. Constraints get
re-stated, partly checked, occasionally contradictory. Adding a field
touches five files; forgetting one is a Tuesday bug.

In Arca, the declaration is the canonical model. The other four derive.

## Three mechanisms

The "define once, derive everywhere" property doesn't come from one
big feature. Three smaller properties combine:

1. **Constrained types are real types.** `Int{min: 1, max: 100}` is not
   a comment or a runtime check — it's a type. The constructor either
   succeeds (and the value carries the constraint forward) or returns
   `Error`.
2. **Values are immutable.** Once constructed, a value can't be mutated
   into invalidity. The constraint holds for the value's entire
   lifetime — no defensive recheck at downstream layers.
3. **`derive` synthesizes the boilerplate.** `derive Bindable` produces
   the JSON binding, SQL scanner, and constructor from the type
   definition. No macros, no reflection — the compiler synthesizes the
   code visibly.

Constrained types centralize invalidity at construction time.
Immutability makes invalidity unreachable past construction. Derive
makes the wiring free.

## Not refinement types

`Int{min: 1, max: 100}` looks like a refinement type, the way Liquid
Haskell or F* might write it. It isn't, by design.

Arca constraints are:

- **Compiler-known.** A fixed set of axes (`min` / `max` / `pattern` /
  `min_length` / `max_length` / `bits` / `validate`), not arbitrary
  predicates. No theorem prover; no SMT solver.
- **Derivable.** Each axis maps to JSON schema, OpenAPI, SQL column
  constraints, validator code. `pattern: ".+@.+"` becomes a regex check
  in the generated unmarshaller; `max_length: 255` can project to a SQL
  column-length constraint where the dialect supports it.
- **Project into generated code.** Constraints aren't just type-level
  facts — they project into the generated Go as conditional checks at
  construction sites.

A full refinement type system would let you write `Int{isPrime(x)}` or
`String{validateBusinessRule(x)}`. Arca rejects those on purpose: an
arbitrary predicate can't be derived into a JSON validator, can't
project to SQL, can't be checked at the API boundary. The moment you
allow them, SSOT breaks.

This is a **derivation-oriented constraint system**, not a weak
refinement type system. The two have different goals.

## Go: reinterpret, don't bind

Arca compiles to Go, so you get single-binary deploys, ms cold start,
the entire Go ecosystem (every `database/sql` driver, every AWS / GCP
SDK), and fast compile.

But Arca doesn't *bind* Go APIs — it *reinterprets* them into the
invariant model:

- Go's `*T` arrives as `Option[Ref[T]]` — nil panic is impossible.
- Go's `(T, error)` arrives as `Result[T, Error]` — errors are visible
  in the signature.
- Go's silent integer overflow becomes a panic-checked operation.
- Go's `MinInt / -1` silent wrap becomes a typed panic.
- Go's `interface{}` requires explicit `match` narrowing.

No separate FFI policy is written. The SSOT property of the type
system rejects unsafe wrappings mechanically — every Go signature is
either reinterpreted into Arca's shape, or refused.

## Install

```sh
go install github.com/tmiyamon/arca@latest
```

Requires Go 1.18+. Stand-alone binary distribution is planned.

## Quickstart

```sh
arca init myapp
cd myapp
arca run
```

A minimal program that exercises the three mechanisms:

```arca
type Email = String{pattern: ".+@.+", max_length: 255}
type Age = Int{min: 0, max: 150}

type User (
  name: String{min_length: 1, max_length: 100}
  email: Email
  age: Age
)

fun main() -> Result[Unit, Error] {
  let alice = User("Alice", Email("alice@example.com")?, Age(30)?)?
  println("created: ${alice.name}")
  Ok(())
}
```

`examples/todo` is a working Echo + SQLite server that demonstrates
Bindable + constraints + Result against a real backend stack.

## Commands

```
arca init <name>       Create a new project
arca run [path]        Transpile and run (default: ./main.arca)
arca build [path]      Transpile and compile to binary
arca emit <file>       Print generated Go code
arca fmt <file>        Format source code
arca health            Check Go installation
arca lsp               Run the language server
```

## Docs

- [SPEC.md](SPEC.md) — language reference
- [DESIGN.md](DESIGN.md) — design rationale, expression ladder,
  rejection list
- [DECISIONS.md](DECISIONS.md) — decision log (newest first)

---

## Deep philosophy

### Two layers, not parallel

- **Layer 1 — safety as prerequisite.** Arca seals Go's root dangers
  (nil panic, typed-nil, panic propagation, `interface{}` traps,
  zero-value surprise, silent overflow, divide-by-zero on `MinInt / -1`).
  Table stakes for any language claiming to improve on Go. Safety is
  not the pitch.
- **Layer 2 — Single Source of Truth as the pitch.** Constrained types
  + immutability + derive yield validation, serialization, schema, and
  domain logic as derivations of a single declaration. This is the
  reason to use Arca.

Layer 1 exists because Layer 2 can't stand on Go's holes. Without nil
sealing, the construction-time guarantee leaks at the FFI boundary.
Without overflow checks, a constrained `Int{min: 0}` can wrap to
negative. Layer 1 is the prerequisite, not a co-pitch.

### What's out of scope

The full list lives in [DESIGN.md](DESIGN.md). The shape of the discipline:

- **No effect system, no refinement types, no contracts** — constrained
  types + Result cover the surface; three validation surfaces produce
  three places where the same invariant is half-stated.
- **No macros, no reflection** — `derive` is the only synthesis
  mechanism, and each derive target requires a decision-log entry.
- **No multi-target** — Go runtime, end of story. Dilutes FFI design
  and breaks SSOT (the canonical model is a Go struct).
- **No mutability, no exceptions, no null** — Layer 1 closes these by
  construction.

Each item recurs as a "wouldn't this be nice?" temptation; the list
makes re-litigation cheap.

### Where Arca sits

Type expressiveness comparable to Haskell or Scala — ADTs, HM inference,
constrained types, exhaustive `match` — combined with the operational
characteristics of Go: single binary, ms cold start, fast compile,
mature backend ecosystem. SSOT as a derived property — one model, four
derivations, immutability making the derivation honest.

Haskell and Scala give you the types but not the deployment story.
Go gives you the deployment story but not the types. Rust gives you
both with a borrowing model that backend code doesn't need. TypeScript
gives you the types but not the runtime. Arca targets the gap.

## Status

Layer 1 is sealed end-to-end. Layer 2 is shipping in slices: Bindable,
traits (Phase 1), the full numeric tower (Int / UInt / Float with
`bits` storage hints, panic-checked arithmetic, `T(x)?` cast), `?`
chain through expression position, `stdlib.BigInt`, and an LSP that
does hover / completion / go-to-definition.

Past prototype; before 1.0. Surface API may still shift. Used in
personal projects; not yet production-deployed at scale.

## License

MIT
