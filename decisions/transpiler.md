# Transpiler Decisions

IR pipeline, lowering, validation, codegen. Newest first.

---

## 2026-04-11: Detect unresolved generic type parameters at the binding site

**Context:** `let todo = stdlib.BindJSON(req)?` called a generic function whose `T any` type parameter did not appear in the parameter list, so HM had no argument to unify `T` with. The Arca compiler silently let `T` flow as an unresolved `IRTypeVar`, which then decayed to `interface{}` in codegen. Downstream usage like `todo.body` surfaced as confusing `type interface{} has no field or method Body` errors from the Go compiler — wrong layer, wrong source positions.

**Decision:** Report `ErrCannotInferTypeParam` at the `let` binding site when the RHS is a generic call whose type parameter is still unresolved after lowering.

**Implementation:**
- New error code `ErrCannotInferTypeParam` with `CannotInferTypeParamData{Binding, Suggestion}`. Message: `cannot infer type of <name> — add explicit type args, e.g. f[T](...)`.
- Added `Pos` to `IRLetStmt` (previously the node had no position) so the diagnostic anchors at the `let` keyword.
- `resolveStmtTypes` checks every `IRLetStmt` and `IRTryLetStmt`: after `resolveDeep`, if the value type still contains an `IRTypeVar`, emit the error.
- Guard: only fires when the RHS is a direct call (`IRFnCall`/`IRMethodCall`) and there is no explicit `let` annotation, so legitimate uses like `let users: List[User] = []` and `let empty = []` keep working.
- New helper `containsUnresolvedTypeVar(IRType)` walks pointer/list/map/option/result/tuple/named types.

**Why the binding site:** errors can fire at the call expression, but binding-site anchoring matches how the user reads and fixes the code — they see `let todo = ...` and add `[Todo]` directly. The explicit-type-args suggestion in the message names the function (from `callFuncName`) to guide the fix.

**Status:** Done

---

## 2026-04-11: `if`/`match` in value position via body-mode refactor

**Context:** `let x = if cond { a } else { b }` and `let x = match ... { ... }` emitted `/* unsupported expr */` because `emitExpr` had no case for `IRIfExpr`/`IRMatch`. The Arca spec advertises `if` and `match` as expressions, so this was a latent gap caught while writing regression tests.

**Decision:** Generalize the existing `emitReturnExpr` / `emitVoidBody` pair into a single `emitBody(e, mode)` that takes a `bodyMode{leaf, valueCtx}`. Three modes share the same traversal:

- `returnMode()` — leaves emit `return <expr>`, `valueCtx=true` (panic fallback for non-exhaustive match)
- `voidMode()` — leaves emit `<expr>` as an expression statement, `valueCtx=false`
- `assignMode(goName)` — leaves emit `<goName> = <expr>` via a new `GoWriter.Set` helper, `valueCtx=true`

**Implementation:**
- Replaced `isReturn bool` with `mode bodyMode` in `emitIfExpr` and all six `emitMatch*` variants. The recursion through arm bodies becomes `em.emitBody(body, mode)` instead of branching on `isReturn`.
- `emitLetStmt` detects `IRIfExpr`/`IRMatch` in `stmt.Value`, declares a `var` of the inferred type, then calls `emitBody(value, assignMode(goName))`.
- Fixed `lowerBlockHint` to propagate the tail expression's type into `IRBlock.Type` (was hard-coded to `interface{}`), so the `var` declaration gets a concrete Go type.
- `GoWriter.Set` emits `x = expr` (plain assignment) so the assignment inside each arm writes into the outer `var` rather than shadowing it via `:=`.

**Ergonomics:** match in value position worked for free once the refactor landed — same traversal, same leaf mechanism.

**Status:** Done

---

## 2026-04-11: Unused package detection

**Context:** Unused Go imports used to surface as `./main.go:N:M: "time" imported and not used` from the Go compiler — wrong file, wrong line, invisible to the LSP. Users couldn't see the problem at the Arca source site.

**Decision:** Detect unused imports in the lowerer and report them as `ErrUnusedPackage` with the Arca source position.

**Implementation:**
- Extended `GoPackage` with `Pos`, `SideEffect`, `Used` fields (rather than adding parallel maps) so all import-site state lives on one struct.
- New `lookupGoPackage(name)` on `Lowerer` — the only sanctioned way to read `l.goPackages` at resolution sites; sets `Used=true` on hit.
- Switched all `l.goPackages[name]` call sites (call dispatch, method receiver, func param resolution, bare package ident, qualified Go type in `lowerNamedType`) to `lookupGoPackage`.
- At the end of `Lower()`, iterate `l.goPackages` and emit `ErrUnusedPackage` for any entry that is not side-effect, not `Used`, and not covered by a `builtins[name]` flag (needed because string interpolation auto-enables `fmt` without a lookup).
- Errors flow through the existing `lowerer.Errors()` channel, so both CLI output and LSP diagnostics pick them up automatically.

**Message convention:** `unused package: <name>` — matches the `<reason>: <name>` style of `undefined variable:`, `unknown type:`, etc., instead of copying Go's "imported and not used" phrasing.

**Status:** Done

---

## 2026-04-11: Explicit type args and HM for Go generics

**Context:** After HM was introduced for Arca-side types, Go generic function calls still relied on string-based `deriveTypeArgs` (hint pattern matching) and a separate `applyExplicitTypeArgs` path for explicit type args. Three disjoint ways of handling the same concept: Go generic type parameters.

**Decision:** Unify into one HM-based path.

**Implementation:**
- `FuncInfo.TypeParams` carries generic type parameter names (from `sig.TypeParams()`).
- `instantiateGeneric(info)` creates fresh `IRTypeVar` for each type param, substitutes them into parameter types and return type via `goTypeToIRWithVars`.
- `resolveGoCall` detects generic calls, unifies arg types with substituted param types, and returns `TypeVars` map.
- `lowerFnCallWithHint` unifies explicit type args (`f[T](args)`) with the vars map, then unifies the return type with the hint (if any).
- `buildGoTypeArgsStr` builds the Go `[T, U]` string from resolved vars or explicit args.
- `deriveTypeArgs` deleted.

**Result:** `stdlib.Decode[User](data)`, `let r: Result[User, error] = stdlib.Decode(data)?`, `sort.Slice(s, (i,j) => s[i] < s[j])` — all go through the same HM unification.

**Status:** Done

---

## 2026-04-09: HM type inference

**Context:** Bidirectional hint system couldn't resolve types when no hint was available. `let r = Ok(42)` required explicit annotation. `let x = None` needed usage context but hints only flow top-down.

**Decision:** Add HM-style type inference with type variables and unification.

**Implementation:**
- `IRTypeVar` in IR for unresolved type variables
- `freshTypeVar()`, `unify(a, b)`, `resolve(t)`, `resolveDeep(t)` in Lowerer
- Ok/Error default error type to `error`, Ok type uses type variable when no hint
- None and empty `[]` use type variables for inner/element type
- Call-site unification: function args unified with parameter types
- Resolution pass after function body lowering: walks IR, resolves type variables, recomputes TypeArgs strings
- Binary expression type inference from operands
- Match expression type from arm body unification
- Lambda return type from body expression
- Prelude return type inference: map returns list of lambda return type, filter preserves input type
- Pipe chain type propagation: `[1,2,3] |> filter(x -> x > 2) |> map(x -> x * 10)` fully inferred
- Let chain propagation: type variables in scope flow through `let y = x` to later usage

**Result:** `let r = Ok(42)`, `let x = None; f(x)`, `let items = []; g(items)`, pipe chains all work without type annotations. Function signatures remain explicit (Rust/Kotlin style).

**Status:** Done

---

## 2026-04-09: GoWriter integration into emit

**Context:** `emit.go` used `writeln(fmt.Sprintf("%s...", indent, ...))` with manual `\t` indentation. Every body/stmt/match function threaded an `indent string` parameter. Error-prone (easy to forget indent level) and hard to read.

**Decision:** Integrate GoWriter (`gowriter.go`) as the sole output mechanism for emit. GoWriter provides structured methods (`If`, `IfElse`, `Switch`, `Case`, `Func`, `Method`, `For`, `Assign`, `Return`, etc.) with automatic indentation tracking.

**Changes:**
- Emitter.buf → Emitter.w (*GoWriter)
- Remove `indent string` parameter from all body/stmt/match emit functions
- Add `Indent()`/`Dedent()` to GoWriter for cases not covered by Block
- Fix `Const` to use Go's `()` syntax instead of `{}`
- Fix `Switch`/`SwitchType` to Go convention (case at same indent as switch)
- Snapshot tests compare `gofmt`-normalized output via `go/format.Source`
- All `testdata/*.go` snapshots updated to gofmt-canonical form

**Result:** `writeln` bridge fully eliminated. emit.go reads as structured Go generation, not string concatenation. Format differences absorbed by gofmt in tests.

**Status:** Done

---

## 2026-04-08: Bidirectional type checking

**Context:** Type checking was split between lower (bottom-up IR type inference) and validate (post-hoc AST-level checks). Lambda parameter types couldn't be inferred. Match arm type mismatches weren't detected. Constraint compatibility was only in validate.

**Decision:** `lowerExprHint(expr, hint)` propagates expected types top-down during lowering. Combined with bottom-up `irType()`, this is bidirectional type checking.

**Phases implemented:**
1. Lambda param type inference from Go FFI call context (resolveCallParamFuncType, ResolveUnderlying for type aliases)
2. Function argument type mismatch via hint in lowerCallArgs
3. Let annotation, return type, match arm body hints via lowerBlockHint/lowerFnBody/matchHint
4. Constraint compatibility moved to irTypesMatch (isConstraintCompatible). Constructor field type checking via hint. Validate type checks removed (kept: existence, count, exhaustiveness)

5. Constructor type arg inference: Ok/Error/None type args derived from hint. Nested propagation (Ok value gets Result.Ok as hint). `irTypeEmitStr` for Go type string generation.

**What validate still does:** type existence (checkTypeExists), argument count, constructor field count/name, match exhaustiveness.

**Known limitation:** Type parameters detected by single-letter heuristic (`A`-`Z`), not from type param declarations. Should resolve against actual type parameter list from TypeDecl.

**Status:** Implemented.

---

## 2026-04-07: Undefined variable detection and IR type propagation

**Context:** Variables like `db` in a method body (should be `self.db`) silently passed through to Go, causing Go compile errors instead of Arca errors. Also, builtin constructors (`Ok`, `Error`, `Some`) had `IRInterfaceType{}` as their type, preventing match arm binding type inference.

**Decision:** Detect undefined variables in `lowerIdent` via lexical scope lookup. If a name is not in scope, not a function, not a Go package — error. Also fix type propagation:

- `Ok(42)` → `IRResultType{Ok: int, Err: error}` (was `IRInterfaceType{}`)
- `Error(e)` → `IRResultType{Ok: unknown, Err: e.type}`
- `Some(x)` → `IROptionType{Inner: x.type}`
- Arca function calls resolve return type from `l.functions` declarations
- Match arm binding type inference errors if subject type is not Result/Option (bug, not limitation)

**Also fixed:** destructure bindings, for loop bindings registered via `registerSymbol`. testdata/map_filter.arca had undefined `nums()` function.

**Status:** Implemented.

---

## 2026-04-07: Sum type method expansion as IR post-pass

**Context:** `lowerSumTypeMethod` was a 70-line special case in lower.go, interleaving AST traversal (`findMatchSelf`) with per-variant IR generation. Duplicated logic with `lowerMethod`.

**Decision:** Lower sum type methods as normal methods. `expandSumTypeMethods` IR post-pass transforms them to per-variant implementations after lowering.

- `match self` methods: split by arm, each variant gets corresponding arm body
- Non-match methods: body duplicated for all variants
- `findIRMatchSelf` operates on IR (not AST)
- All non-static methods added to interface definition (no `findMatchSelf` check needed)

**Status:** Implemented.

---

## 2026-04-05: Unified IRMatch + IRMatchPattern design

**Context:** Match expressions are 6 separate IR types (IRResultMatch, IROptionMatch, IREnumMatch, IRSumTypeMatch, IRListMatch, IRLiteralMatch). The "Unit in void context" bug requires void arm handling in all 6 emit functions. Also, binding structures are duplicated across match types.

**Decision: Unify to single IRMatch with typed patterns.**

```go
type IRMatch struct {
    Subject IRExpr
    Arms    []IRMatchArm
    Type    IRType
}

type IRMatchArm struct {
    Pattern IRMatchPattern
    Body    IRExpr
}

type IRMatchPattern interface { irMatchPatternNode() }

// Pattern types express match kind + bindings
type IRResultOkPattern struct { Binding *IRBinding }
type IRResultErrorPattern struct { Binding *IRBinding }
type IROptionSomePattern struct { Binding *IRBinding }
type IROptionNonePattern struct {}
type IREnumPattern struct { GoValue string }
type IRSumTypePattern struct { GoType string; Bindings []IRBinding }
type IRSumTypeWildcardPattern struct { Binding *IRBinding }
type IRListEmptyPattern struct {}
type IRListExactPattern struct { Elements []IRBinding }
type IRListConsPattern struct { Elements []IRBinding; Rest *IRBinding; MinLen int }
type IRListDefaultPattern struct { Binding *IRBinding }
type IRLiteralPattern struct { Value string }
type IRLiteralDefaultPattern struct {}
type IRWildcardPattern struct {}
```

**Benefits:**
- Void arm (IRVoidExpr) handling in one place
- Bindings expressed in patterns, not duplicated across arm types
- New patterns added without new IR node types
- Exhaustiveness validation via pattern kind checking
- Emit dispatches on pattern type, not match type

**Exhaustiveness (validate):**
- Result: must have OkPattern + ErrorPattern
- Option: must have SomePattern + NonePattern
- Enum: all variants or wildcard
- Same guarantees, checked in validate instead of structurally enforced

**Status:** Implemented. Legacy 6 match types removed. IRVoidExpr pending.

---

## 2026-04-05: Unit in void context generates unused value

**Problem:** `Ok(_) -> Unit` in a match arm emits `struct{}{}` in Go. Go rejects this as an unused value (`struct{}{} (value of type struct{}) is not used`).

**Root cause:** Emitter doesn't distinguish between match arms that return a value and match arms in void context (expression statement). In void context, `Unit` should emit nothing or `_ = struct{}{}`.

**Status:** Implemented. IRVoidExpr + markVoidContext in lower, condition inversion in emit for Result/Option.

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
