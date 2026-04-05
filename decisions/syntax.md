# Syntax Decisions

Language syntax, naming, import system. Newest first.

---

## 2026-04-02: struct tags — implemented as tags block

**Requirements:**
1. Arbitrary tag keys (json, db, yaml, elastic, custom...)
2. Auto-generation from field names with conversion rules
3. Override only exceptions
4. Natural Arca syntax

**Key insight:** struct tag is "mapping to external system's world". Constrained types are "domain value constraints". Different concerns, must be separate.

**Approaches explored and rejected:**
- Mix in `{}` — leaks via type alias, mixes concerns
- Separate types per layer (UserRow, UserResponse) — clean but verbose
- `#annotation` / `@annotation` on fields — no auto-generation rules
- Config file (arca.toml) — too far from type definition
- `layer` blocks — over-designed for what it does

**Decision:** `tags` block with `()` for global rules, `{}` for overrides.

```arca
type User(id: Int, userName: String) {
  tags {
    json,
    db(snake),
    elastic { userName: "full_name" }
  }
}
```

Rules:
- `tag_name` → all fields, field name as-is
- `tag_name(snake)` → all fields, case conversion
- `tag_name { field: "value" }` → only specified fields
- Comma-separated OK: `tags { json, db(snake) }`

**Transitional:** stdlib will eventually hide struct tags. This is the bridge to Go's ecosystem.

---

## 2026-03-31: Package and import system

**Context:** Needed package system for real projects. Investigated Go, Rust, Kotlin, Python, TS approaches.

**Decisions:**
- **Package unit:** Directory = package. Package name = directory name (implicit, no `package` declaration needed)
- **Circular imports:** Forbidden (Go constraint from transpilation)
- **No wildcard imports:** Explicit is better. Go, Gleam, Elm also have no wildcard.
- **Versioning:** No version-in-path (Go's `/v2` is universally disliked)

**Import syntax (Kotlin/Rust inspired):**
```arca
import user                    // user.find() — qualified
import user.{find, create}     // find(), create() — selective
import user as u               // u.find() — alias
import go "fmt"                // Go packages
import go _ "modernc.org/sqlite" // side-effect
```

**Why not Go style:** Go's "last segment = package name" is implicit and confusing (documented complaints). Arca requires explicit qualification or selective import.

**Why not Python style:** `from x import *` causes namespace pollution. Arca forbids it.

**Design principle:** Generated Go should be valid, idiomatic Go. Arca abstractions (alias, namespaces) are resolved before codegen, not leaked into Go output.

**Implementation notes:**
- Alias expansion happens before import resolution (main file only). `u.find()` → `user.find()` at AST level.
- Module-qualified calls (`user.find()`) resolved to flat calls (`find()`) in codegen since all modules merge into one Go file.
- FieldAccess module resolution only happens inside FnCall context, preventing collision with variable names (e.g. parameter `u` vs alias `u`).
- Types are always imported regardless of selective import (needed for type checking).

---

## 2026-03-30: Pipe operator — keeping it

**Context:** Methods were added. Considered dropping pipe. But Go generics can't add new type parameters to methods (`func (l List[T]) Map[U](...) List[U]` is illegal). Collection operations (map/filter/fold) can't be method chains.

**Decision:** Keep both. Clear split:
- **Methods** — type domain operations (`user.toJson()`)
- **Pipe** — collection operations (`users |> map(...) |> filter(...)`)

Not ideal (two styles) but technically necessary.

---

## 2026-03-30: Short record syntax

**Context:** `type User { User(name: String) }` is redundant for single-constructor types.

**Decision:** `type User(name: String)` as shorthand. Equivalent AST. Formatter outputs short form when no methods.

---

## 2026-03-30: Unit type

**Context:** Go functions returning `error` only (no value) need Result wrapping. `Result[???, error]`.

**Decision:** `Unit` type. `Result[Unit, error]` for error-only functions. `Ok(Unit)` for success. Go generates `struct{}`.

---

## 2026-03-30: `let _` for discarding values

**Context:** Go FFI calls return values that Arca doesn't need. Go rejects unused variables.

**Decision:** `let _ = expr` discards value. `let _ = expr?` discards success value but propagates error. Explicit discard — don't allow implicit.

---

## 2026-03-30: Import redesign — string literals

**Context:** `import go.modernc.org.sqlite` broke because dot-to-slash conversion mangled domain names. TLD hacks were fragile.

**Decision:** `import go "path"` with string literal. Go package paths passed through verbatim. Side-effect: `import go _ "pkg"`. No conversion, no bugs.

---

## 2026-03-30: Function keyword — `fun`

**Context:** Was `fn` (Rust style). Changed to camelCase, making `fn` + camelCase an uncommon combination (only Zig).

**Analysis:** Arca has methods, constrained types, type-driven design — closer to Kotlin/Swift than Rust/Gleam. `fn` is function-oriented/FP culture. `fun`/`func` is method-oriented/OOP-lean culture.

**Options:** `fn` (Rust/Zig), `func` (Go/Swift), `fun` (Kotlin/Koka/Mint)

**Decision:** `fun`. Short, modern, matches Kotlin (closest in philosophy). Go's `func` was considered but `fun` is 1 char shorter.

---

## 2026-03-30: Naming convention — camelCase

**Context:** Arca was using snake_case, requiring snake→camelCase conversion in codegen for Go output.

**Arguments for camelCase:**
- Go uses camelCase — no conversion needed
- Kotlin/Java analogy: same runtime, same naming convention
- Go FFI calls (`r.URL.Query().Get("id")`) already camelCase — mixing with snake_case looks inconsistent
- Less codegen complexity, fewer bugs

**Arguments for snake_case:**
- Rust/Gleam/Python convention
- Functional/ML language culture

**Decision:** camelCase. Arca sits on Go's world, should match Go's conventions. Like Kotlin matches Java.

---

## 2026-03-30: defer — decided not to add, then added

**Context:** Needed for resource cleanup. Explored alternatives.

**Options considered:**
- Go's `defer` — works but "breaks the flow" of reading top-to-bottom
- RAII/Drop (Rust) — ideal but needs type info about which Go types need cleanup
- `use` keyword — still needs to know what method to call
- `with` block (Python/Kotlin) — control structure, not type-oriented
- Wrapper functions (Scala's `Using`) — hides defer in library

**Initial decision:** Don't add defer. Use Go-side wrapper functions.

**Later reversed:** Added `defer` as-is from Go. Pragmatic. `defer db.Close()` is needed and Go developers understand it.

---

## 2026-03-29: Pipe operator vs methods (superseded by 2026-03-30 "keeping it")

**Context:** Go FFI returns objects with methods. Arca has pipe `|>` but no methods.

**Analysis:**
- Languages with pipe (Gleam, Elm, Elixir) don't have methods
- Languages with methods (Rust, Go, Kotlin) don't have pipe
- Both solve the same problem (function chaining)
- F# has both (because .NET) — results in mixed style
- Arca on Go = same situation as F# on .NET

**Decision at this point:** Keep pipe for now. Will likely drop when methods are added. Later discovered pipe is permanently needed due to Go generics constraint on collection methods.

---

## 2026-03-29: UFCS (Uniform Function Call Syntax) — rejected

**Context:** Considered D/Nim's UFCS as alternative to methods. `user.toJson()` = `toJson(user)`.

**Pros:** No method concept needed, data/operation separation preserved.
**Cons:** "Is this a method or UFCS?" confusion. Mixed reception in D/Nim community. Doesn't solve name collision without overloading.

**Decision:** Rejected. Prefer explicit methods over implicit UFCS.

---

## 2026-03-29: interface/trait — deferred

**Context:** Go interfaces require methods. Arca has no methods.

**Analysis:**
- trait needed for: Go interface satisfaction, ad-hoc polymorphism, resource cleanup abstraction
- trait NOT needed for: most Arca code (ADT + pattern match suffices)
- Languages without trait (Gleam, Elm, C) work fine with ADT + pattern match
- Go FFI interface satisfaction can be worked around (http.HandleFunc instead of http.Handler)

**Decision:** Deferred. Revisit when io.Reader/io.Writer becomes blocking.

---

## 2026-03-28: Import syntax — dot separator (superseded by 2026-03-30 string literals)

**Context:** SPEC had `go/fmt`, implementation had `go.fmt`.

**Analysis:** `/` is Go convention but most languages use `.` for imports (Java, Kotlin, Scala, Python, C#). Mixing `.` and `/` for Arca vs Go modules is inconsistent.

**Decision at this point:** All dots. `import go.fmt`, `import go.database.sql`, `import user`.

---

## 2026-03-31: struct tags — rethinking (superseded by 2026-04-02)

**Context:** Realized json/db metadata belongs to fields, not types. `type ProductId = Int{min: 1, json: "id"}` breaks when reused — json key leaks to other fields.

**Current state at this point:** Mixed in `{}`. Works but conceptually wrong.

**Options considered:**
- Separate types per layer (User, UserRow, UserResponse) + spread syntax for conversion
- Trait to mark types as JsonModel/DbModel
- Auto-generate from field names (camelCase → json, snake_case → db)
- `.go` accessor to drop to raw Go when needed

**Decision at this point:** Unresolved. Led to further discussion and eventually the tags block solution.

---

## 2026-03-30: struct tags from constrained types (superseded by 2026-04-02)

**Context:** gin + sqlx need `json:"name" db:"name"` struct tags. Where to put this metadata?

**Options:** Annotations (@json), separate tags block, auto-generate from field names, mix into constrained types `{}`.

**Decision at this point:** Mix into `{}`. String-valued keys become struct tags, numeric-valued keys become validation constraints. Single source of truth. `Int{min: 1, json: "id", db: "id"}`.

---

## 2026-03-28: Language spec — initial decisions

Documented in one session:
- Type parameters: `[]`
- Error propagation: `?` (provisional)
- Lambda: `(x) => expr`
- Variables: `let` only, immutable
- Pipe: `|>` first argument
- String interpolation: `"Hello ${name}"`
- Newline-based statements
- `pub` visibility
- 1 file = 1 module
- `fn` / `type` / `match` (later changed: fn→fun, snake_case→camelCase)
- snake_case in Arca, camelCase in generated Go (later unified to camelCase)
