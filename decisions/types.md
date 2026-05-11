# Type System Decisions

Newest first within this topic.

---

## 2026-05-12: Seal `/` and `%` Layer 1 holes (Slice B)

**Context:** Slice E4 routed `+ - *` on Int/UInt through panic-checked helpers (`__addInt` etc.) but explicitly skipped `/` and `%`, with the rationale "Go's native div-by-zero panic is enough." A direct check (`go run` on `math.MinInt / -1` with `defer recover`) showed Go's signed `MinInt / -1` **silently wraps to MinInt** with no panic — the Go spec defines this as deterministic, not a runtime error. That's a real Layer 1 silent-overflow hole, parallel to the addition/multiplication overflows that `__addInt` / `__mulInt` were created to seal. Slice E5's stdlib also shipped `CheckedDivInt`/`CheckedDivUInt` but no `CheckedMod*`.

**Decision: Route `/` and `%` on Int/UInt through `__divInt` / `__modInt` / `__divUInt` / `__modUInt` panic helpers, and add the missing `CheckedMod*` stdlib variants.** `arithmeticHelper(kind, op)` table extended with the 4 new entries; `lowerBinaryExpr`'s comment updated to drop the "/ % skip" carve-out. Helper bodies: `__divInt` checks `b == 0` then `a == MinInt && b == -1` (overflow), then native `a / b`. `__modInt` checks `b == 0` only — `MinInt % -1` is mathematically 0 and Go agrees, no overflow case. UInt variants check `b == 0` only. `-1 << 63` literal sidesteps a `math` import for one constant. Stdlib gains `CheckedModInt` / `CheckedModUInt` reusing the existing `ErrDivByZero` sentinel.

`runPanicE2E` helper added so e2e tests can assert "program exits non-zero with substring X" — used for 4 new tests covering div/mod by zero and the MinInt-overflow case (constructed via `-2^62 * 2` to avoid an overflow literal). `arithmetic_panic.arca` extended with `safeDiv` / `safeMod` for snapshot coverage of the new emit paths.

**Note:** This fix only routes when both operands resolve to a numeric `IRNamedType` via `numericRangeOf`. FFI-typed scalars like `math.MinInt` lower to `IRInterfaceType{}` and bypass the helper — a pre-existing routing gap that also affects `+ - *` and is not addressed here.

**Status:** Done. Slice B closes the numeric tower's last Layer 1 hole. Slice E5's "Float / `/ %` は scope 外" comment in `design_numeric_types.md` is now stale — see memory entry update.

---

## 2026-05-11: `__mulInt` division-free overflow check

**Context:** Slice E4 emitted `__mulInt` with `if a != 0 && p/a != b` for signed multiplication overflow. A bench (`arithmetic_panic_bench_test.go`) measured **15x slowdown** vs native (0.57 ns → 8.66 ns/op) and **11.5x on mul-dominated kernels** — far worse than every other panic-checked op (1.7–2.7x). Root cause: integer division on x86 costs ~25–30 cycles vs 1 cycle for native mul, and the `a != 0` short-circuit doesn't help once `a` is nonzero (the common case).

**Decision: Rewrite `__mulInt` using `bits.Mul64` on absolute values.** Take `uint64(-a)` / `uint64(-b)` (two's-complement wraparound gives the correct bit pattern for `MinInt`), `bits.Mul64` to get a 128-bit unsigned result, then compare against the sign-aware limit (`MaxInt64` if signs agree, `1<<63` if signs differ — i.e. allow `MinInt` exactly). No division. Edge cases verified against 12 cases including `MinInt * -1`, `MinInt * 1`, `MaxInt32 * MaxInt32`, etc. `math/bits` was already imported when `__addUInt` / `__mulUInt` were live; the import gate in `lower.go` extended to include `__mulInt`.

Post-change measurements: **MulInt 15.1x → 3.2x**, **MixedKernel 11.5x → 3.8x**. All panic-checked ops now sit in the 1.7–3.8x cost-of-safety band — no outliers, no `unsafe` opt-out needed at this layer.

**Status:** Done. 11 testdata snapshots regenerated; `arithmetic_panic_bench_test.go` carries the helper copies + bench harness for future re-measurement.

---

## 2026-05-10: Numeric D2 refined widening landed

**Context:** Slice E shipped literal hint coercion + cross-base diagnostic + arithmetic panic + `std.checked` but explicitly deferred D2 refined value-flow widening (`let x: Int = int8val` auto-conversion). The user flagged this as missing the B4 motivating case — `let id: Int = res.LastInsertId()?` produces `int64` from Go FFI which Arca treats as `Int64`, and assigning to `Int` (= Go `int`) failed with a generic type-mismatch even though the ranges coincide on a 64-bit target. Forcing user-side `Int(...)?` ceremony for what the design promised as implicit defeats the numeric tower's ergonomic intent.

**Decision:** Two coordinated additions sharing the existing range registry from Slice E1.

- **`numericWideningCompatible(source, target)`** — same-kind only (signed × signed, unsigned × unsigned, float × float); reports whether `source.range ⊆ target.range`. Cross-kind returns false so the dedicated cross-base diagnostic still fires at binary-op sites and explicit `T(x)?` casts remain the path between integer sign-classes / between integer and float.
- **`Lowerer.unify`** appends the new compat check after the structural / constraint / trait branches. unify success no longer requires structural identity for numeric types — the proven-safe widening direction is admissible.
- **`applyNumericWidening(result, hint)`** wraps the lowered result in an `IRFnCall{Fn: IRIdent{GoName: hint.GoName}, Args: [result]}` so emit produces `int(int8val)` / `int64(int32val)` / etc. The wrap fires only at the `lowerExprHint` boundary — every hint-driven site (let bind, fn arg, struct ctor, return) sees it automatically.

**Status:** Landed 2026-05-10. ~80 LoC: `lower.go` (`numericWideningCompatible` + `applyNumericWidening` + unify branch + lowerExprHint wrap), `lower_check_test.go` (2 tests covering both directions), `testdata/widening.arca` + .go demonstrating the conversion shape. examples/todo/main.arca (the B4 source) compiles cleanly without manual cast — `NewTodo(int(id), todo.Body, ...)` materialises automatically. Float-Int auto-widening (`Int → Float`) remains explicit for now; the design memo notes "Int → Float silent Ok" but the same-kind constraint here keeps the change minimal and the explicit `Float(...)?` cast still works.

---

## 2026-05-10: Numeric Slice I — `stdlib.BigInt` landed

**Context:** `Int` panics on overflow (Slice E4) and `stdlib.CheckedAdd*` returns `Err` (Slice E5), but neither helps when the *values themselves* exceed int64 — cryptographic ids, summing large counts, currency in smallest units, etc. The 2026-05-10 numeric tower design positioned arbitrary-precision arithmetic as the third numeric layer and committed to a Go `math/big` bridge. Slice I delivers it.

**Decision:** Single immutable `BigInt` type wrapping `*big.Int`. Operations always allocate a fresh result so the user sees no mutation; performance-conscious callers can drop down to direct `math/big` via FFI if a hot path demands in-place updates. Surface stays method-style to mirror `BindableSlot` etc.: `NewBigInt(v: Int)` factory, `BigIntFromString` for arbitrary input, `Add` / `Sub` / `Mul` / `Div` / `Mod` / `Neg` for arithmetic, `Eq` / `Lt` / `Gt` for comparison, `String` for display, `ToInt` for narrowing back. `Div` and `Mod` reject divisor zero with `ErrDivByZero`; `BigIntFromString` rejects malformed strings with `ErrBigIntInvalid`; `ToInt` rejects out-of-range values with `ErrBigIntRange`. `Le` / `Ge` are not on the surface — derivable as `!Gt` / `!Lt`.

**Status:** Landed 2026-05-10. ~110 LoC: `stdlib/bigint.go` (BigInt + 11 methods + 2 factory funcs + 3 sentinel errors); `stdlib/bigint_test.go` (8 tests covering round-trip / overflow / div-by-zero / comparison / Neg+Mul chain); `testdata/bigint.arca` + .go demonstrating usage; `TestE2EBigInt` exercising the round-trip end-to-end. Numeric tower design now fully implemented except Slice G (B4 verification, user-side).

---

## 2026-05-10: Numeric Slice E — literal coercion, cross-base, arithmetic panic, std.checked landed

**Context:** With Slices A–D+F+H landed, narrow numeric tower types and `T(x)?` casts work, but two Layer 1 leaks remained at value-flow boundaries: (1) `let x: Int8 = 200` failed with a generic type-mismatch instead of an out-of-range diagnostic, and (2) `Int + UInt` passed Arca's lowerer because IRNamedType equality was satisfied at neither side, falling through to invalid Go (`int + uint`) caught only at `go vet`. A third gap was structural: `Int + Int` on the base types silently wrapped on overflow, since lowerBinaryExpr emitted plain Go `a + b`. Slice E plugs the three gaps and adds the opt-in Result-returning surface for arithmetic that should not panic.

**Decision:** Five coordinated pieces sharing one numeric range registry.

- **E1 — `numericRange` registry.** `numericRangeOf(IRType) (numericRange, ok)` resolves built-in Go primitive names (`int8` … `int64`, `uint8` … `uint64`, `float32` / `float64`, plus base `int` / `uint` / `float64`) to their static range. The Kind discriminator (`signed` / `unsigned` / `float`) drives the rest of Slice E's branches.
- **E2 — Literal hint coercion.** `dispatchLowerExpr` for `IntLit` peeks at the hint via `numericRangeOf`. When the hint is a numeric Go primitive and the value fits, the IR's literal type adopts the hint's Go name (`let x: Int8 = 100` lowers to `IRIntLit{Type: int8}`); when the value is out of range, `ErrLiteralOutOfRange` raises with the displayed Arca type and the allowed range. `FloatLit` follows the same shape but only widens to float Kind hints.
- **E3 — Cross-base diagnostic.** `lowerBinaryExpr` checks operand kinds for `+ - * / % == != < > <= >=`; signed × unsigned raises `ErrCrossBaseArithmetic` directing the user to convert one side via `Int(uintval)?` or `UInt(intval)?`.
- **E4 — Arithmetic panic emit.** Same-kind integer `+ - *` lowers to a panic-checked helper call (`__addInt` / `__subInt` / `__mulInt` for signed; `__addUInt` / `__subUInt` / `__mulUInt` for unsigned) instead of a plain `IRBinaryExpr`. Narrow operands wrap in a Go conversion (`int(int8val)`) so the helpers' fixed `(int, int)` / `(uint, uint)` signatures hold; the helper's result type is base, so `Int8 + Int8 → Int` (subsequent narrowing is explicit via `T(x)?`). Unsigned helpers use `math/bits.Add64` / `Mul64` for overflow detection; signed helpers use sign-comparison and divide-back. Float arithmetic skips the panic path (Inf is in spec); `/` and `%` skip too — Go's native panic on integer div-by-zero is the Layer 1 detection signal already, and the divide-back overflow is the rare `MinInt / -1` edge case which the user takes via `stdlib.CheckedDivInt` if it matters.
- **E5 — `stdlib.CheckedAdd*` / `CheckedSub*` / `CheckedMul*` / `CheckedDiv*` × `Int` / `UInt`.** 8 Result-returning functions. Each mirrors its panic-checked emit counterpart but returns `(T, error)` with `ErrOverflow` / `ErrDivByZero` sentinels for `errors.Is` matching. Float-side checked variants are deferred until a real consumer surfaces — without a Layer 1 imperative their semantics are debatable (Inf as overflow vs in-spec).

**Status:** Landed 2026-05-10. ~500 LoC across `lower.go` (range registry + literal coercion + cross-base diag + arithmetic helper dispatch + `widenIntegerToBase` + `arithmeticHelper`); `emit.go` (6 panic helpers); `types.go` (2 new error codes + data types); `stdlib/checked.go` + `stdlib/checked_test.go` (8 funcs + tests); `lower_check_test.go` (4 new diagnostic tests); `testdata/arithmetic_panic.arca` + .go; existing testdata regenerated for the new arithmetic emit shape (`a + b` → `__addInt(a, b)`). Numeric trait as compiler intrinsic (Bindable-style) deferred to a future Slice J — `design_numeric_types.md` deferred section records the Phase 2 trait scope and the LoC estimate (~300 LoC). Slice I (`std.bigint`) remains; Slice G (B4 verification) is on the user side.

---

## 2026-05-10: Numeric Slice D+F+H — tower, cast, Bindable narrow landed

**Context:** With Slice C providing the `bits: N` mechanism, the numeric tower (`Int8` … `Float64`) needs (D) a user-facing surface, (F) a `T(x)?` narrowing cast that returns `Result[T, Error]`, and (H) a way to seal Bindable's SQL Scan path which silently truncated `int64` driver values into narrow fields. Layer 1 demands all three land together: with D alone, narrow tower types exist but can't be populated safely; with D+F but no H, Bindable hosts with narrow fields silently overflow. The original 2026-05-10 design specified Bindable narrow validation via per-field functions in `BindableDict` (option α), but discussion 2026-05-10 reframed the problem after observing user-defined narrow types (`type Score = Int{min, max}`) already work cleanly because their slot is widened to the base Go type and `NewT`'s constructor validates at Freeze.

**Decision:** Three coordinated changes that share one validator function set.

- **D — Tower as built-in primitives.** `Int8` / `Int16` / `Int32` / `Int64` / `UInt8` … / `Float32` / `Float64` resolve in `lowerNamedType` to the corresponding Go type directly. They sit alongside `Int` / `UInt` / `Float` in the prelude switch — no import, no synthesis. User code can still declare `type MyByte = UInt{bits: 8}` via Slice C, but the tower names themselves are reserved (built-in branch matches before the type-alias fallback).
- **F — `T(x)?` cast lowering + validator emit.** `lowerUserConstructorCall` recognises the 13 numeric tower / base names and emits `IRConstructorCall{GoName: "New<T>", Fields: [int64-or-uint64-or-float64-cast(arg)], GoMultiReturn: true, Type: Result[<go>, error]}`. The inserted Go conversion (`int64(x)` / `uint64(x)` / `float64(x)`) lets the validator signature stay fixed regardless of source type; cross-base sources bit-reinterpret here, and the validator's range check catches out-of-range values so Layer 1 stays sealed even when source-kind is wrong (a dedicated cross-base diagnostic is Slice E). `emitBuiltins` emits one `New<T>(v int64|uint64|float64) (<go>, error)` per `narrow_<key>` set in `l.builtins`. Identity validators for `Int` / `Int64` / `UInt` / `UInt64` / `Float` / `Float64` skip the range check but exist for `T(x)?` syntax uniformity until Slice E removes the redundant wrap.
- **H — Bindable wide slots + Freeze re-narrowing.** `synthesizeBindableTypes` checks each field's lowered Go type via `narrowFieldInfo`; when narrow (`int8` / `int16` / `int32` / `uint8` / `uint16` / `uint32` / `float32`), the `BindableSlot[T]` inner type widens to `int64` / `uint64` / `float64`. `synthesizeBindableDispatch` (Freeze body) inserts `__narrowN, __narrowErrN := New<T>(d.Field.Value)` before the host constructor call, propagating the narrow error on failure. This re-uses Slice F's validators — the path is identical to user-defined `Score = Int{min, max}` which already widens (Score's Go base is `int`) and validates at Freeze via `NewScore`. The original 2026-05-10 design memo's α path (per-field validator slice on `BindableDict`) was retired here because Go reflection already knows the narrow Kind, and the wide-slot pattern keeps narrow validation in one place.

**Status:** Landed 2026-05-10. ~330 LoC: `lower.go` (tower switch, `numericTowerCast`, `narrowFieldInfo`, lowerUserConstructorCall cast branch, synthesizeBindableTypes widening, synthesizeBindableDispatch narrowing, math import gate); `emit.go` (validator emitters); `helpers.go` (zero-value table); `testdata/numeric_tower.arca` + `testdata/derive_bindable_narrow.arca`; codegen + e2e tests. `bits_storage.arca` rewritten with non-tower names since `Int32` / `UInt32` / `Float32` are now reserved built-in names (alias declarations using those names emit `type Int32 int32` but later usage resolves through the built-in branch). Slice E adds range-aware widening (`let x: Int = int8val` no-op widen, `Int + UInt` cross-base diagnostic, value-flow auto-narrow when proven safe).

---

## 2026-05-10: Numeric Slice C — `bits: N` storage hint landed

**Context:** The 2026-05-10 numeric-types decision expresses the integer / float tower (`Int8` … `Int64`, `UInt8` … `UInt64`, `Float32`) as constrained types via a `bits: N` constraint key, sharing the existing `min` / `max` / `pattern` constraint machinery. Slice C is the foundational mechanism: every site that translates `Int{bits: 32}` to a Go type, and every diagnostic that catches misuse, must work before the stdlib numeric tower (Slice D+F+H) can land on top.

**Decision:** Treat `bits` as a parser-level constraint key (no grammar change — `parseConstraints` is already key-agnostic) plus a lower-level type override.

- `bitsAllowedFor(base)` lists valid widths per numeric base (`8 / 16 / 32 / 64` for `Int` / `UInt`; `32 / 64` for `Float`). `bitsGoTypeFor(base, bits)` returns the Go type name (`int32`, `uint8`, `float32`, …) or `""` on a mismatch.
- `lowerNamedType` peeks at `nt.Constraints` for the three numeric branches and substitutes the bits-aware Go name when valid; falls back to the base default (`int` / `uint` / `float64`) otherwise. So `Int{bits: 32}` lowers to `IRNamedType{GoName: "int32"}` whether it appears as a type-alias body or inline at a struct field.
- `lowerTypeAliasDecl` now forwards `nt.Constraints` into `lowerType` (previously stripped) so `type Int32 = Int{bits: 32}` emits `type Int32 int32`. `buildAliasValidator` and `buildStructValidator` skip the `bits` key — the storage type *is* the constraint, not a runtime check.
- `validateBitsConstraint` runs at declaration sites (type-alias body, struct field, sum-variant field) and raises `ErrInvalidBitsConstraint` with `InvalidBitsConstraintData` for two failure modes: non-numeric base (allowed list empty → "bits constraint not supported on String; only Int / UInt / Float accept bits") and out-of-range width ("invalid bits value 7 for Int; allowed: 8, 16, 32, 64"). Validation is decl-only to avoid duplicate diagnostics across repeated lowerings of the same usage.

`min` / `max` continue to compose with `bits` (`Int{bits: 8, min: 0}` works — bits picks `int8` storage, `min` adds a runtime check on the int8 value). The `bits` axis is one hard-coded constraint key alongside `min` / `max` / `pattern`; `design_constrained_axes.md` 2026-05-09 records the future axis-registry refactor.

**Status:** Landed 2026-05-10. ~120 LoC across `lower.go` (helpers + `lowerNamedType` branches + decl-site validation), `types.go` (error code + data), and tests (`testdata/bits_storage.arca` + two `lower_check_test` cases). `go test ./...` passes; `go vet` clean. The stdlib numeric tower lands as part of Slice D+F+H bundle; for Slice C alone, user code can already declare its own bits-aware aliases.

---

## 2026-05-10: Numeric Slice B — `UInt` core type landed

**Context:** The 2026-05-10 numeric-types decision adds `UInt = Go uint` alongside `Int` and `Float` to close the UInt64 representation gap surfaced by B4 — MySQL `BIGINT UNSIGNED`, hash output, file mode, and similar Go APIs have no Arca representation without it. Slice B is the foundational plumbing: every site that recognises `Int` as a primitive must recognise `UInt` symmetrically before constrained narrow types (`UInt8 = UInt{bits: 8}`) can land.

**Decision:** Add `UInt` to every Int-handling site, mapping to Go `uint`.

- `lowerNamedType` (`UInt` → `IRNamedType{GoName: "uint"}`)
- `isKnownTypeName` allowlist
- `arcaDisplayName` (`uint` → `UInt` for diagnostics)
- `irTypeToGoString` allowlist (so FFI compatibility checks accept it)
- `irZeroExpr` and `helpers.typeZeroValue` (default `0` for fields/returns)
- OpenAPI schema in `typeAliasToSchema` and `typeRefToSchema` — emits `{"type": "integer", "minimum": 0}` so the unsigned constraint surfaces at the spec boundary.

`unify` is unchanged: `IRNamedType` equality is `GoName`-based, so `Int + UInt` already fails to unify and surfaces as `ErrTypeMismatch`. The dedicated cross-base diagnostic ("UInt's max exceeds int64, no safe common base") is part of Slice E.

Literal hint coercion (`let x: UInt = 5`) is not implemented — `IntLit` always lowers with `GoName: "int"` regardless of hint, matching the current `let x: Float = 5` behaviour. Slice E (D2 refined value-flow + range-aware widening) is the right place for this. For Slice B, UInt values flow via fn signatures, struct fields, arithmetic between UInts, and Go FFI returns of `uint`.

**Status:** Landed 2026-05-10. ~30 LoC across 4 files plus `testdata/uint_basic.arca` covering struct field, method receiver, fn signature, and arithmetic. `go test ./...` passes; `go vet` clean. Slice C (`{bits: N}` storage hint) is the next foundational independent slice.

---

## 2026-05-10: Numeric Slice A — 64-bit GOARCH enforcement landed

**Context:** The 2026-05-10 numeric-types decision pins `Int` to `Go int` and requires the host platform to be 64-bit so that `Int` ≡ `Int{bits: 64}` is a true identity. Without enforcement, an Arca program built on GOARCH=386 silently produces a binary whose `Int` is 32-bit, breaking Layer 1 the moment any value crosses the SSOT (`int64` columns, JSON numbers, hash output). Subsequent slices (`bits: N` storage hint, `T(x)?` narrowing, `std.checked.*`) all assume the 64-bit identity, so the seal must land before them.

**Decision:** Two redundant guards.

- Emit injects `//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm` as the first line of every generated Go file (main + per-module). 32-bit targets see the file excluded; `go build` reports "no Go files in ..." rather than producing a Layer 1-violating binary.
- `arca run` and `arca build` call `check64BitTarget()` before invoking the Go toolchain. The check honors `$GOARCH` (cross-compile override) and falls back to `runtime.GOARCH`. Failure prints `arca requires a 64-bit target: GOARCH=<arch> is not supported (Int is fixed to 64 bits)` with exit code 1 — a clearer signal than the toolchain's "no Go files" surface.

The build-tag list and the runtime check share one source of truth (`target_arch.go`: `goBuild64BitTag` constant + `goarch64BitSet` map).

`arca emit` does not precheck — it just prints Go to stdout. The build tag in the printed file is the seal; downstream `go build` will reject the file on a 32-bit target.

**Status:** Landed 2026-05-10. ~100 LoC: `target_arch.go` (new), `emit.go` (one line in `Emit()`), `main.go` (precheck call in `runCmd` / `buildCmd`). All 56 testdata snapshots regenerated to include the build tag; `go test ./...` passes; `go vet` clean. Slice B (`UInt` core type) is the next foundational independent slice.

---

## 2026-05-10: Numeric types — core + constrained type tower

**Context:** B4 (examples/todo migration) hit `sql.Result.LastInsertId()` returning Go `int64` with no Arca representation — Arca modeled `Int` (= Go int) and `Float` (= float64) but nothing else from Go's full numeric tower (`int / int8/16/32/64 / uint / uint8/16/32/64 / float32/64 / byte / rune`). Quick fixes (Int64 primitive / cast syntax / stdlib helper / auto-narrow) sidestepped the policy gap. Full design discussion 2026-05-07 → 2026-05-10 covered concerns: Bytes, cast API, panic policy, arithmetic semantics, BigInt placement.

**Decision:** Three core types `Int / UInt / Float` plus a stdlib/numeric tower expressed as constrained types via `{bits: N}` opt-in storage hint.

- **Core**: `Int = Go int` (64-bit-only enforced via `//go:build` + Arca CLI precheck — refuses GOARCH=386/arm/wasm32/mips/etc.). `UInt = Go uint` added to handle UInt64 representation gap; without it MySQL `BIGINT UNSIGNED`, hash output, and file mode have no representation. `Float = float64`.
- **Tower**: `Int8 = Int{bits: 8}`, `UInt32 = UInt{bits: 32}`, `Float32 = Float{bits: 32}`, etc. — through existing constrained-type machinery, no new primitive category. `bits: N` is one constraint key alongside `min` / `max` / `pattern` / etc., but `bits` alone implies storage = N-bit and value range = N-bit natural; without `bits`, storage stays at base (preserves "Int default = Go int" cultural fit).
- **D2 refined widening (range-aware)** extends to value flow positions (assignment, return, function arg, match arm) and arithmetic operands. When source range ⊆ target range, implicit widen (proven safe, no Result, no panic). Otherwise explicit `T(x)?` (Result-returning narrowing). Cross-base (`Int + UInt`) is a compile error — UInt's max exceeds int64, no safe common base.
- **Cast API**: existing `T(x)?` constructor syntax extends uniformly across struct ctor (`User(id: 1)?`), constrained ctor (`Email("...")?`), and numeric narrowing (`Int8(intval)?`). Internal Go emit retains the `NewT` naming convention (compiler-only, user-invisible). No parallel `T.from(x)` API. Dispatch: source ⊆ T → returns T (no Result); source ⊄ T → returns Result. Bidirectional via type annotation for narrowing.
- **Arithmetic on base types**: panics on overflow per Layer 1 violation detection policy (`decisions/foundations.md` 2026-05-10). Follows Rust 0560 RFC trend (default safe, opt-in unsafe). Silent wrap rejected, supported by production bug evidence: uint underflow leading to huge index, ID exhaustion, JS 2^53 precision loss across API boundaries. `std.checked.{add,sub,mul,div,mod,neg,wrapping*,saturating*}` prelude provides opt-in Result-returning safe arithmetic. Literal range checked statically (`let x: Int8 = 200` produces compile error).
- **`std.bigint.BigInt`**: arbitrary-precision arithmetic, bridges Go's `math/big`. Third numeric layer alongside fast+panic (Int) and explicit Result (std.checked). Heap-allocated and slower; opt-in conversion via `BigInt(x: Int)`. No overflow possible.
- **Bytes**: dedicated type rejected. `List[Byte]` (= Go `[]uint8` = `[]byte`) is sufficient. Binary blob operations (UTF-8 conversion, base64, hash) live as prelude/stdlib functions.

Implementation slices A–G+I detailed in `design_numeric_types.md`. D+F+H bundled (numeric tower + cast + Bindable narrow validation) to preserve Layer 1 consistency — narrow types and Bindable-side narrowing land together so no intermediate state opens a silent overflow path.

**Status:** Designed 2026-05-07, revised 2026-05-10. Memos updated 2026-05-10 (`design_numeric_types.md`, `design_panic_handling.md` 2026-05-10 entry, `design_two_layers.md` 2026-05-10 entry, `project_panic_audit_2026_05_02.md` 2026-05-10 update). Implementation pending (Phase 4: Slices A through I).

---

## 2026-04-04: Variable shadowing in codegen

**Context:** `let email = Email(email)?` inside a function with parameter `email` generated invalid Go — `email := __try_val1` re-declared the parameter in the same scope.

**Decision:** Track declared variable names per function scope. When a let binding shadows an existing variable, codegen generates a suffixed name (`email_2`) and maps subsequent references to the new name.

**Implementation:** `declareVar()` tracks names and returns unique Go names. `varNames` map stores current variable name mapping for Ident resolution. `initFnScope()` registers parameters at function entry.

---

## 2026-04-04: Constrained type constructor returns Result without ?

**Context:** `let email = Email("a@b.c")` with a constrained type generated broken Go code — `NewEmail()` returns `(Email, error)` but codegen assigned it to a single variable.

**Decision:** Constrained type constructors without `?` automatically wrap the result in `Result[T, error]`.

- `Email("a@b.c")?` → propagates error, binds `Email` on success
- `Email("a@b.c")` → returns `Result[Email, error]`, handle with `match`

**Codegen:** Generates temp vars for the `(T, error)` return, then wraps in `Ok_` / `Err_`. Also improved `inferGoType` to resolve `ConstructorCall` types (was falling back to `interface{}`).

---

## 2026-04-04: Associated functions (static fun) and Self

**Context:** Needed type-level functions without `self` (factory constructors like `Greeting.from("Hello")`). Current method system uses implicit `self`, so methods on interface types generated invalid Go (can't have interface receiver).

**Decision: `static fun` keyword + `Self` type reference.**

```arca
type Greeting {
  Hello(name: String)
  static fun from(s: String) -> Greeting {
    match s {
      "Hello" -> Self.Hello(name: "World")
      _ -> Self.Goodbye(name: "World")
    }
  }
}
let g = Greeting.from("Hello")
```

**Why `static fun` over alternatives:**
- Implicit self + body analysis to detect associated functions → caller can't tell from signature alone, confusing
- Explicit `self` parameter (Rust/Python) → would require changing all existing methods, and Arca already has implicit self
- `static fun` (Swift pattern) → one keyword addition, clear at definition site, no existing code changes

**`Self` for type self-reference:** Inside type body (methods and associated functions), `Self` resolves to the enclosing type. Avoids repeating the type name. Follows Rust/Swift convention. Preferred over bare constructors inside type body (which would be unusual — no mainstream language does this).

**Codegen:** `static fun` → Go package-level function (`greetingFrom`). Regular methods → Go methods with receiver.

---

## 2026-04-03: Qualified constructor syntax + arca init

**Context:** Constructors like `Hello(name: "World")` were callable without type qualification, leaking constructor names into global scope. Two types with `Error(message: String)` would collide.

**Decision: Type-qualified constructors for multi-variant types.**

| Form | Syntax | Example |
|------|--------|---------|
| Single-constructor | Unqualified | `User(name: "Alice")` |
| Multi-constructor (sum type) | `Type.Constructor(...)` | `Greeting.Hello(name: "World")` |
| Enum variant | `Type.Variant` | `Color.Red` |
| Type alias | Unqualified | `Email("test@example.com")` |
| Builtins (Ok/Error/Some/None) | Unqualified | `Ok(value)` |
| Match patterns | Always unqualified | `Hello(name) -> ...` |

**Rationale:** Follows Rust/Swift pattern — qualify at construction to avoid name collision, but match patterns are unqualified because the subject's type disambiguates. Single-constructor types are unqualified because type name = constructor name (no ambiguity).

**Builtins (Ok/Error/Some/None):** Unqualified now. Will be explained by a prelude system in the future (auto-imported, like Rust's std::prelude).

---

## 2026-04-03: Constraint compatibility (Level 2)

**Context:** Constrained type aliases need compatibility checking. `AdultAge = Int{min: 18, max: 150}` should be passable where `Age = Int{min: 0, max: 150}` is expected, but not vice versa.

**Design: Dimension-based normalization.**
Constraints are normalized into independent dimensions, each with a unified comparison:

| Kind | Dimensions | Comparison |
|------|-----------|------------|
| Range | Value (min/max), Length (min_length/max_length) | Range inclusion: `A.range ⊆ B.range` |
| Exact | Pattern, Validate | Equality |

**Rules:**
- Source → Target is compatible if source is equal or stricter on all target dimensions
- Target has a dimension source doesn't → source is unbounded → not compatible
- Source has extra dimensions target doesn't → OK (stricter is fine)
- Two unconstrained aliases with different names → nominal, never compatible (UserId ≠ OrderId)

**No structural aliases.** `type X = T` is always a newtype (nominal). Structural aliases have no current use case in Arca. Revisit when function types are added to the type system.

**Codegen:** Type alias parameters always get a Go type conversion (`greet(Age(adult))`). Same-type conversion is no-op in Go.

**Reference:** Ada has the closest feature in a production language (`subtype`). Research/academic: Liquid Haskell, F* (refinement types). Mainstream languages (Rust, Go, Kotlin, TS) don't have this.

**Status:** Implemented but may be removed if practical value doesn't materialize. Main use case is library code accepting wider types with app code using stricter types.

---

## 2026-04-02: Type alias codegen

**Context:** `type Email = String{pattern: ".+@.+"}` was parsed but generated no Go code.

**Decision:** Type aliases generate Go defined types (not Go type aliases):
- `type Email = String{...}` → `type Email string` + `NewEmail(v string) (Email, error)`
- `type UserId = Int` → `type UserId int` (no constructor if no constraints)

Nominal typing: `UserId` and `OrderId` are distinct types even with same constraints.

**Codegen:** `fmt` and `regexp` are auto-imported when constrained type aliases need them.

**OpenAPI:** Type aliases generate standalone schema entries with constraints mapped to JSON Schema.

---

## 2026-04-02: let type annotation

**Context:** `let users = []` generates `[]interface{}{}` in Go — no type info for empty collections. Go FFI functions like `db.Select(&users, ...)` need correctly typed slices.

**Options considered:**
- A) Explicit type annotation: `let users: List[User] = []`
- B) Hindley-Milner type inference (infer from usage context)
- C) Typed empty list literal: `List[User][]`

**Decision:** Option A. Simple, explicit, familiar syntax (Kotlin, TypeScript, Rust all use `let x: T`).

**Codegen rules:**
- Empty list + type annotation → `var users []User` (Go zero value)
- Non-empty value + type annotation → `var users []User = expr`
- No annotation → `users := expr` (Go infers)

**Future:** HM inference (B) may be added later to make annotations optional in more cases.

---

## 2026-03-30: Methods — decided to add

**Context:** Constrained types were implemented, and we found that domain operations on constrained types (e.g. `Age.increment()`) need to be closed within the type.

**Arguments for methods:**
- Constrained types need per-type operations that respect constraints
- Go FFI already returns objects with methods — inconsistent to not have methods in Arca
- Name collision: `incrementAge` vs `incrementScore` vs `fun Age.increment(self)`
- IDE discoverability: `age.` shows available operations

**Arguments against:**
- Data/operation separation is a clean FP principle
- Adds complexity to the language
- Triggers trait/interface discussions

**Decision:** Add methods. Defined inside type body, `self` implicit (not in args). "Types express domains" takes priority over "data/operation separation".

```arca
type User(name: String) {
  fun greet() -> String { "Hello ${self.name}" }
}
```

---

## 2026-03-30: Constrained types — levels and scope

**Context:** Constrained types v1 implemented (construction-time validation). Discussed how deep to take them.

**Levels identified:**
1. Construction validation ✅
2. Constraint compatibility (Age vs AdultAge) ✅
3. Condition narrowing (if age >= 18, treat as AdultAge) — future
4. Go type optimization (Int{0,255} → uint8) — future
5. OpenAPI derivation ✅ / JSON, DB derivation — future (see ideas.md)
6. Arithmetic propagation (SMT-solver level) — far future
7. Structural constraints (NonEmptyList) — future

**Key insight:** Immutability makes construction-time validation sufficient for permanent guarantees. No other mainstream language has this combination at language level.

**Decision:** Opt-in constraints. Not forced on all types. Default is unconstrained. Strict where you want it.

---

## 2026-03-30: Constrained types — syntax `{}`

**Context:** Needed syntax for type constraints. Considered `[]`, `{}`, `()`, `where`, `<>`, custom delimiters.

**`[]` rejected:** Collides with type parameters. `List[Int][max_length: 10]` is ugly.
**`()` rejected:** Collides with constructor calls.
**`where` rejected:** Reads as a condition, not part of the type.
**`<>` considered:** Possible but not standard.

**Decision:** `{}` — `Int{min: 1}`, `String{max_length: 100}`. Distinguishable from blocks by context (key: value inside).

---

## 2026-03-30: Constrained types — design decisions

- **Two layers:** Built-in constraints (OpenAPI-convertible: min, max, pattern, etc.) + custom validate function (runtime only)
- **Reuse via type alias:** `type Email = String{pattern: ".+@.+"}`. No separate `constraint` keyword.
- **No re-constraining aliases:** `Email{min_length: 5}` is an error.
- **No cross-field constraints:** Use constructor functions manually.
- **Constructor returns Result:** Only when constraints exist. `User(id: 1, name: "Alice")?`

---

## 2026-03-30: Static vs runtime constraints

**Context:** Explored whether timeout/cancellation (context.Context) could be unified with constrained types.

**Analysis:** Constrained types validate at construction (one-time). Context monitors continuously during execution. Different nature — static vs runtime.

**Decision:** Arca guarantees values (static). Go guarantees execution (runtime). Clear boundary. Don't abstract Go's runtime primitives.

---

## 2026-03-29: Builtin names (Ok/Error/Some/None) — shadowing

**Context:** User-defined constructor `Ok` collides with builtin Result's `Ok`.

**Options:** Reserved words, namespaced (`Result.Ok`), shadowing.

**Decision:** Shadowing (same as Rust). Builtins take priority unless user defines same name. Future: warn on shadow.

---

## 2026-03-29: Result/Option as struct not pointer

**Context:** Option was initially `*T` (pointer). Changed to generic struct.

**Reason:** Pointer leaks nil into Go code. Struct keeps nil contained. Safer for Go interop. User explicitly preferred struct approach.

---

## 2026-03-28: Immutability — fully immutable, Go types opaque

**Context:** Arca is immutable, but Go types are mutable.

**Decision:** Arca-defined types are fully immutable (language-guaranteed). Go types are opaque — Arca doesn't guarantee their immutability. Developers know Go's semantics. No wrapper types needed.
