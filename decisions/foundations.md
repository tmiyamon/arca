# Foundation Decisions

Why Arca exists and core philosophy.

---

## 2026-05-10: Layer 1 expansion + panic policy revision

**Context:** The numeric types design (`decisions/types.md` 2026-05-10) raised whether arithmetic overflow should be a Layer 1 violation. The original Layer 1 list (2026-04-15, `design_two_layers.md`) enumerated FFI-derived dangers (nil, `interface{}`, panic propagation, mutation, zero value, non-exhaustive switch) but didn't include internal-arithmetic concerns. Silent wrap on `Int + Int` (Go default) corrupts SSOT data through stored-but-wrong values, violating Layer 1's core motivation that model values must always be valid. Production bug evidence: uint underflow leading to huge index, ID exhaustion, MySQL `BIGINT UNSIGNED` truncation, JS 2^53 precision loss. The original "no panic from generated code" policy (`design_panic_handling.md` 2026-04-18) precluded panic-emit as a detection mechanism, yet Result-default for arithmetic is not viable (Rust 0560 RFC rejected it as an ergonomic disaster).

**Decision:**

(1) **Layer 1 principle reframed**: "seal every path that can deliver invalid values to SSOT data". Seal mechanism is chosen by path nature:
- Internal-bug paths (violations the type system cannot prevent at compile time) → panic emit. Request-scoped recovery via net/http handles fail-safe behavior in API context; CLI/batch contexts terminate the process with stack trace.
- Business-error paths (runtime validation failures, parse failures, IO failures) → Result returned, user handles explicitly.
- FFI-derived paths (Go's unsafe behaviors leaking in) → wrap at API boundary (Option / Result / Builder).

(2) **Old "panic 全面禁止" policy revised** to "panic is the Layer 1 violation detection signal". Panic permissible for:
- Arithmetic overflow, slice out-of-bounds, div-by-zero, structural unreachable, user-explicit `assert`.

Panic not permissible for:
- Internal compiler bugs, business validation errors, FFI-derived business failures (these flow through Result).

(3) **Arithmetic overflow added to Layer 1 list**. Silent wrap not adopted. Proven-safe widening (e.g., `Int8 + Int8 → Int`) widens to base; proven-unsafe arithmetic (e.g., `Int + Int`) emits panic. `std.checked.*` prelude provides Result-returning opt-in, `std.bigint` provides arbitrary-precision opt-in. Aligns with the modern trend: Rust 0560 RFC, Swift trap-default, Zig illegal-behavior. Go 2 had a parallel proposal (Ian Lance Taylor) that was rejected for backward compatibility — Arca is greenfield and not bound by that constraint.

(4) **Deferred Layer 1 candidates** (awaiting future design sessions):
- String UTF-8 invalid (whether to enforce UTF-8 validity at Bytes ↔ String boundary; depends on String design session).
- Float NaN/Inf (whether constrained-type axis like `Float{finite: true}` should seal them at SSOT boundary; coupled with `design_constrained_axes.md` axis registry discussion).
- Time arithmetic overflow (subsumed under general arithmetic overflow principle, no separate entry needed).

**Status:** Principle reframing and policy revision landed in memos on 2026-05-10 (`design_two_layers.md` 2026-05-10 entry, `design_panic_handling.md` 2026-05-10 entry, `project_panic_audit_2026_05_02.md` 2026-05-10 update). The arithmetic-overflow seal implementation ships with `decisions/types.md` 2026-05-10 numeric Slice E. Performance cost measurement (arithmetic check insertion) is a deferred task after Slice E.

---

## 2026-04-15: Goal framing — "best practical backend language"

**Goal (internal compass):** Be the most practical language for backend development.

Everything else is consequence, not identity:

- **Layer 1 — Safety prerequisite.** To stand on top of Go at all, Arca must seal Go's root dangers (nil panic, typed nil, `interface{}` trap, panic leak, zero value surprise, unintended mutation). Without this, any downstream value proposition is undermined. This is table stakes, not differentiation.
- **Layer 2 — SSOT as derived property.** Type-driven + constrained types + backend focus naturally yield Single Source of Truth: model definition → OpenAPI / DB schema / validator / (future: client SDK, form, event schema). SSOT emerges from the design; it isn't the stated goal.
- Go runtime (cold start, single binary, compile speed, ecosystem): picked because it serves practicality on deployment.
- ML-level types (ADT, HM inference, constrained types): picked because they serve practicality on correctness.
- Low learning curve (Kotlin-level): picked because impractical learning kills adoption.

**Positioning note.** "Most practical backend language" is the compass but not a standalone pitch (every language claims practicality). External positioning is the *combination no competitor offers at once*: Go operational characteristics + Rust-level type expressiveness + SSOT as derived byproduct + easy learning curve. Competitors each compromise on one axis; Arca targets the intersection.

**Design principle derived from the goal.** *Arca makes guarantees. FFI is only allowed to the extent that guarantees can be preserved across it.* This single rule mechanically decides Layer 1 questions (nil → `Option[T]`, panic → `Result`, `any` → safe cast, mutation → Builder/Freeze).

**Phase ordering.** Layer 1 safety completion precedes Layer 2 projection work. An SSOT built on unsealed Go dangers is unreliable: derivations would project invalid values.

---

## 2026-03-28: Why Arca exists

**Problem:** PHP/Laravel project (63K lines, 75 models, Cloud Run) needs better language.

**Evaluated:** Kotlin (not strict enough), Gleam (syntax deviates from conventions), Rust (borrow checker overkill, slow compile), Scala (DX bad), OCaml (no web ecosystem), Go (no ADT/Result).

**Solution:** New language. ML type safety + Go ecosystem. Transpiles to Go for single binary, fast startup, full Go library access.

**Existing alternatives found:** Borgo (dead), Soppo (immature), Dingo (Go-syntax-bound, limits expression).

**Key user values:** Correctness, expressiveness, familiar conventions, Go's pragmatic benefits.
