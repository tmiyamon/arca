# Transpiler Decisions

IR pipeline, lowering, validation, codegen. Newest first.

---

## 2026-04-05: Unit in void context generates unused value (open bug)

**Problem:** `Ok(_) -> Unit` in a match arm emits `struct{}{}` in Go. Go rejects this as an unused value (`struct{}{} (value of type struct{}) is not used`).

**Root cause:** Emitter doesn't distinguish between match arms that return a value and match arms in void context (expression statement). In void context, `Unit` should emit nothing or `_ = struct{}{}`.

**Status:** Open.

---

## 2026-04-04: Move type checking to IR

**Context:** Checker runs on AST before lowering. This means type checking and IR type resolution are separate systems with duplicated logic. Adding Go FFI type checking to the checker requires threading TypeResolver through it — but the lowerer already has TypeResolver.

**Problem:** The current `parse → check → lower → emit` pipeline means:
- Checker infers types on AST independently from IR type resolution
- Go FFI type info is only available in the lowerer
- Adding Phase 3/4 (method/field resolution) to the checker duplicates what the lowerer already does
- LSP hover reads from checker symbols, but IR has richer type info

**Decision: Move validation from AST checker to IR-based validation.**

New pipeline: `parse → lower (error-tolerant) → validate IR → emit`

The IR already provides:
- Structurally exhaustive match (IRResultMatch requires both arms)
- Resolved types on every expression
- TypeResolver for Go FFI
- Resolved names (shadowing, Self, constructors)

**Steps:**
1. Make lowerer error-tolerant (don't panic on invalid input)
2. Build IR validator that walks IR nodes and checks types
3. Migrate checker logic to IR validator
4. Remove old AST checker

**Why now:** IR is mature enough. Doing this before Phase 3/4 avoids building Go FFI type checking twice.

---

## 2026-04-04: IR implementation (AST → IR → Go)

**Context:** Decided to introduce IR between AST and Go output to structurally prevent missed cases and separate concerns (see earlier decision entry).

**Implementation:** Three new files:
- `ir.go` — IR node definitions. Match expressions are structurally exhaustive (e.g. `IRResultMatch` requires both `OkArm` and `ErrorArm`). All expressions carry resolved types. All names are Go-safe.
- `lower.go` — AST → IR conversion. Resolves names (Self, self, shadowing, builtins, constructors), classifies match expressions, tracks imports.
- `emit.go` — IR → Go string output. Mechanical walk of IR nodes, no feature-specific branching.

**Key design: value-before-declare for shadowing.** In `let email = Email(email)?`, the RHS must be lowered before declaring the new `email` variable, otherwise the parameter reference gets incorrectly mapped to the shadowed name.

**Verification:** `arca emit-ir` command added for parallel comparison. All testdata files produce semantically identical output to the existing `arca emit`. Performance is equivalent (sub-millisecond).

**Status:** Complete. All commands use the IR pipeline. Old codegen.go, codegen_match.go, codegen_builtins.go removed. Shared helpers moved to helpers.go.

---

## 2026-04-04: IR (intermediate representation) introduction

**Context:** Current architecture is AST → Go codegen directly. Every new feature (constrained types, shadowing, Self, qualified constructors, etc.) requires handling in multiple codegen paths (let statements, expressions, match arms, function returns). Features leak across concerns, causing missed cases and bugs.

**Problem:** Bottom-up feature addition without structural guarantees. Each codegen path must independently check "is this a constrained constructor?", "is this shadowed?", "is this Self?". No mechanism to ensure all cases are covered.

**Decision: Introduce IR between AST and Go output.**

```
Source → AST → IR (normalized) → Go output
```

**IR responsibilities (each as a separate pass):**
1. **Name resolution** — resolve `Self` to concrete type, resolve module-qualified names, variable shadowing (rename to unique Go-safe names)
2. **Constructor resolution** — qualified constructors (`Greeting.Hello`) resolved to concrete Go struct names, constrained constructors wrapped in Result
3. **Error propagation** — `?` operator expanded to try/error pattern
4. **Type resolution** — all expressions annotated with resolved types (needed for codegen type args, and future LSP)

**Go output becomes simple:** Walk IR and emit Go. No special cases, no feature-specific branching. Each IR node directly maps to a Go construct.

**Benefits:**
- Missing cases become structurally visible (if IR doesn't handle it, it fails clearly)
- LSP gets type information for free from the IR
- New features are added as IR passes, not scattered across codegen
- Testing: each pass can be tested independently

**Why now:** Codebase is still small (~1500 lines of codegen). Delaying increases migration cost as more features are added.

**IR node design (initial):**
- Should be close to Go's structure (statements, expressions, declarations) but with Arca's semantics resolved
- Carries type information on every expression node
- All names are Go-safe (no further renaming needed in output)

---

## 2026-04-04: Builtin println/print

**Context:** `import go "fmt"` + `fmt.Println(...)` was required for Hello World. First impression of the language forces Go FFI knowledge.

**Decision:** `println` and `print` as builtin functions, available without import. Maps to `fmt.Println` / `fmt.Print`, with `fmt` auto-imported in generated Go.

**Implementation:** Codegen refactored to body-first generation. Body is generated into a buffer first, then imports are prepended based on what was actually used. This eliminated the `preScan` AST walk (~100 lines) that was needed when imports were emitted before body generation. Adding new builtins now requires a single change in `genExprStr`.

**Future:** These builtins will move to a prelude module when one is implemented.
